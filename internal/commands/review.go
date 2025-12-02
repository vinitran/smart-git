package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vinhtran/git-smart/internal/ai"
	"github.com/vinhtran/git-smart/internal/config"
	"github.com/vinhtran/git-smart/internal/git"
	"github.com/vinhtran/git-smart/pkg/logger"
)

type reviewOptions struct {
	lastCommit bool
	short      bool
	raw        bool
	language   string
	maxTokens  int
	timeout    time.Duration
}

var (
	reviewCmd = &cobra.Command{
		Use:     "review",
		Aliases: []string{"rv"},
		Short:   "AI review for git diffs or commits",
		RunE:    runReview,
	}
	opts reviewOptions
)

func init() {
	rootCmd.AddCommand(reviewCmd)

	reviewCmd.Flags().BoolVar(&opts.lastCommit, "last-commit", false, "Review the latest commit instead of staged changes")
	reviewCmd.Flags().BoolVar(&opts.short, "short", true, "Return a concise summary instead of a full review")
	reviewCmd.Flags().BoolVar(&opts.raw, "raw", false, "Print the raw response from Gemini without formatting")
	reviewCmd.Flags().StringVar(&opts.language, "language", "en", "Language for the review response (en|vi)")
	reviewCmd.Flags().IntVar(&opts.maxTokens, "max-tokens", 1024, "Maximum tokens for Gemini 2.5 Flash output")
	reviewCmd.Flags().DurationVar(&opts.timeout, "timeout", 45*time.Second, "Timeout for the Gemini review request")
}

func runReview(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	log := logger.L().With("command", "review", "path", wd)
	if err := git.EnsureRepository(ctx, wd); err != nil {
		return err
	}

	repoInfo, err := git.GetRepoInfo(ctx, wd)
	if err != nil {
		return err
	}

	diff, mode, err := selectDiff(ctx, wd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(diff) == "" {
		fmt.Println("There are no changes to review.")
		return nil
	}

	apiKey, err := resolveAPIKey(ctx)
	if err != nil {
		return err
	}

	client := ai.NewClient(apiKey, opts.maxTokens)

	request := ai.ReviewRequest{
		Diff:      diff,
		RepoInfo:  repoInfo,
		Mode:      mode,
		Language:  opts.language,
		Short:     opts.short,
		CreatedAt: time.Now(),
	}

	log.InfoContext(ctx, "Requesting Gemini 2.5 Flash review",
		"mode", mode, "language", opts.language)

	resp, err := client.ReviewDiff(ctx, request)
	if err != nil {
		return err
	}

	printReview(resp.Text)
	return nil
}

func selectDiff(ctx context.Context, dir string) (string, string, error) {
	if opts.lastCommit {
		diff, err := git.GetLastCommitDiff(ctx, dir)
		return diff, "last-commit", err
	}
	// Prefer staged changes; if none, fall back to working tree diff.
	stagedDiff, err := git.GetStagedDiff(ctx, dir)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(stagedDiff) != "" {
		return stagedDiff, "staged", nil
	}

	wtDiff, err := git.GetWorkingTreeDiff(ctx, dir)
	return wtDiff, "working-tree", err
}

func printReview(text string) {
	if opts.raw {
		fmt.Println(text)
		return
	}

	divider := strings.Repeat("-", 60)
	fmt.Println(divider)
	fmt.Println("AI Review:")
	fmt.Println(divider)
	fmt.Println(text)
	fmt.Println(divider)
}

func resolveAPIKey(ctx context.Context) (string, error) {
	if key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); key != "" {
		return key, nil
	}

	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if key := strings.TrimSpace(cfg.GeminiAPIKey); key != "" {
		return key, nil
	}

	fmt.Print("Enter your Gemini API key (it will be stored for future use): ")
	reader := bufio.NewReader(os.Stdin)
	key, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("API key must not be empty")
	}

	cfg.GeminiAPIKey = key
	if model := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); model != "" {
		cfg.GeminiModel = model
	}

	if err := config.Save(cfg); err != nil {
		return "", err
	}

	fmt.Println("API key saved to SmartGit config.")
	return key, nil
}
