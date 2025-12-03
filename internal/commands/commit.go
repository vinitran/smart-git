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
	"github.com/vinhtran/git-smart/internal/git"
	"github.com/vinhtran/git-smart/pkg/logger"
)

type commitOptions struct {
	timeout time.Duration
}

var (
	commitCmd = &cobra.Command{
		Use:   "commit",
		Short: "Stage all changes and create a commit message suggested by AI",
		RunE:  runCommit,
	}
	commitOpts commitOptions
)

func init() {
	rootCmd.AddCommand(commitCmd)

	commitCmd.Flags().DurationVar(&commitOpts.timeout, "timeout", 45*time.Second, "Timeout for the Gemini commit message request")
}

func runCommit(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), commitOpts.timeout)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	log := logger.L().With("command", "commit", "path", wd)

	if err := git.EnsureRepository(ctx, wd); err != nil {
		return err
	}

	status, err := git.StatusPorcelain(ctx, wd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		fmt.Println("There are no changes to commit.")
		return nil
	}

	apiKey, err := resolveAPIKey(ctx)
	if err != nil {
		return err
	}

	// Build a diff that represents everything that would be committed,
	// without staging anything yet (to avoid touching the working tree
	// before the user has seen the privacy assessment).
	stagedDiff, err := git.GetStagedDiff(ctx, wd)
	if err != nil {
		return err
	}
	workingDiff, err := git.GetWorkingTreeDiff(ctx, wd)
	if err != nil {
		return err
	}

	var diffBuilder strings.Builder
	diffBuilder.WriteString(stagedDiff)
	if strings.TrimSpace(workingDiff) != "" {
		if diffBuilder.Len() > 0 {
			diffBuilder.WriteString("\n")
		}
		diffBuilder.WriteString(workingDiff)
	}
	diff := strings.TrimSpace(diffBuilder.String())
	if diff == "" {
		fmt.Println("There are no changes to commit.")
		return nil
	}

	repoInfo, err := git.GetRepoInfo(ctx, wd)
	if err != nil {
		return err
	}

	client := ai.NewClient(apiKey, 256)

	req := ai.CommitAnalysisRequest{
		Diff:     diff,
		RepoInfo: repoInfo,
	}

	log.InfoContext(ctx, "Requesting Gemini commit message and privacy analysis")

	analysis, err := client.AnalyzeCommit(ctx, req)
	if err != nil {
		return err
	}

	message := strings.TrimSpace(analysis.CommitMessage)
	if message == "" {
		return errors.New("AI returned an empty commit message")
	}

	branchName := strings.TrimSpace(analysis.BranchName)

	fmt.Println("Proposed commit message:")
	fmt.Println("------------------------")
	fmt.Println(message)
	fmt.Println("------------------------")

	risk := strings.ToLower(strings.TrimSpace(analysis.PrivacyRisk))
	if risk == "" {
		risk = "low"
	}

	if risk == "high" || risk == "medium" {
		fmt.Println("Potential sensitive/private information detected in this commit:")
		for _, reason := range analysis.PrivacyReasons {
			if strings.TrimSpace(reason) == "" {
				continue
			}
			fmt.Printf("- %s\n", reason)
		}
		fmt.Printf("Privacy risk level reported by AI: %s\n", risk)
		fmt.Print("Do you still want to proceed with staging and committing these changes? (y/N): ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Commit aborted due to potential sensitive information.")
			return nil
		}
	}

	protectedBranch := isProtectedBranch(repoInfo.Branch)

	// If we are on a protected branch (like main/develop), create and switch
	// to a feature/fix branch before staging and committing.
	if protectedBranch {
		finalBranchName := branchName
		if finalBranchName == "" {
			finalBranchName = deriveBranchNameFromCommit(message)
		}
		fmt.Printf("Creating and switching to branch: %s\n", finalBranchName)
		if err := git.CreateAndCheckoutBranch(ctx, wd, finalBranchName); err != nil {
			return err
		}
	}

	log.InfoContext(ctx, "Staging all changes after AI analysis")
	if err := git.AddAll(ctx, wd); err != nil {
		return err
	}

	log.InfoContext(ctx, "Creating git commit with AI generated message")
	if err := git.Commit(ctx, wd, message); err != nil {
		return err
	}

	fmt.Println("Commit created successfully.")
	return nil
}

// isProtectedBranch reports whether the given branch should be treated as protected.
func isProtectedBranch(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "main", "master", "develop", "dev":
		return true
	default:
		return false
	}
}

// deriveBranchNameFromCommit generates a reasonable branch name from a Conventional
// Commit style header. It falls back to a generic feature branch if parsing fails.
func deriveBranchNameFromCommit(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return "feature/changes"
	}

	// Extract type, optional scope and description from "<type>(<scope>)!: desc"
	colonIdx := strings.Index(header, ":")
	if colonIdx == -1 {
		return "feature/" + slugify(header)
	}

	prefix := strings.TrimSpace(header[:colonIdx])
	desc := strings.TrimSpace(header[colonIdx+1:])

	// Remove optional breaking change indicator "!" before colon.
	if strings.HasSuffix(prefix, "!") {
		prefix = strings.TrimSuffix(prefix, "!")
		prefix = strings.TrimSpace(prefix)
	}

	// Extract type from "<type>(scope)" or "<type>"
	typePart := prefix
	if parenIdx := strings.Index(prefix, "("); parenIdx != -1 {
		typePart = prefix[:parenIdx]
	}
	typePart = strings.ToLower(strings.TrimSpace(typePart))

	category := mapCommitTypeToBranchCategory(typePart)

	descSlug := slugify(desc)
	if len(descSlug) > 40 {
		descSlug = descSlug[:40]
	}
	if descSlug == "" {
		descSlug = "changes"
	}

	return fmt.Sprintf("%s/%s", category, descSlug)
}

func mapCommitTypeToBranchCategory(t string) string {
	switch t {
	case "feat":
		return "feature"
	case "fix":
		return "fix"
	case "refactor":
		return "refactor"
	case "perf":
		return "perf"
	case "style":
		return "style"
	case "test":
		return "test"
	case "docs":
		return "docs"
	case "build":
		return "build"
	case "ops":
		return "ops"
	case "chore":
		return "chore"
	case "revert":
		return "revert"
	default:
		return "feature"
	}
}

// slugify converts an arbitrary description into a lowercase, kebab-case slug
// suitable for use in a branch name.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		// Treat any non-alphanumeric character as a separator.
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	return result
}
