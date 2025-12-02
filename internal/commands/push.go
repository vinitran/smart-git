package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vinhtran/git-smart/internal/git"
	"github.com/vinhtran/git-smart/pkg/logger"
)

type pushOptions struct {
	timeout time.Duration
}

var (
	pushCmd = &cobra.Command{
		Use:     "push",
		Aliases: []string{"p"},
		Short:   "Push the current branch to origin, setting upstream if needed",
		RunE:    runPush,
	}
	pushOpts pushOptions
)

func init() {
	rootCmd.AddCommand(pushCmd)

	pushCmd.Flags().DurationVar(&pushOpts.timeout, "timeout", 45*time.Second, "Timeout for the git push operation")
}

func runPush(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), pushOpts.timeout)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	log := logger.L().With("command", "push", "path", wd)

	if err := git.EnsureRepository(ctx, wd); err != nil {
		return err
	}

	repoInfo, err := git.GetRepoInfo(ctx, wd)
	if err != nil {
		return err
	}

	branch, err := git.CurrentBranch(ctx, wd)
	if err != nil {
		return err
	}
	if branch == "" {
		return fmt.Errorf("could not determine current branch")
	}

	// If we are on a protected branch (main/master/develop/dev), suggest creating
	// a feature branch derived from the latest commit message and pushing that
	// instead of pushing directly to the protected branch.
	if isProtectedBranch(branch) {
		subject, err := git.LastCommitSubject(ctx, wd)
		if err != nil {
			return err
		}
		suggested := deriveBranchNameFromCommit(subject)

		fmt.Printf("On protected branch '%s'.\n", branch)
		fmt.Printf("Suggested branch: %s\n", suggested)
		fmt.Print("Create, switch, and push this branch to origin? (Y/n): ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "" && answer != "y" && answer != "yes" {
			fmt.Println("Push aborted. No branch was created or pushed.")
			return nil
		}

		log.InfoContext(ctx, "Creating and checking out suggested feature branch from protected branch",
			"from", branch, "to", suggested)
		if err := git.CreateAndCheckoutBranch(ctx, wd, suggested); err != nil {
			return err
		}

		branch = suggested
	}

	hasUpstream, err := git.HasUpstream(ctx, wd)
	if err != nil {
		return err
	}

	if hasUpstream {
		log.InfoContext(ctx, "Pushing current branch to origin", "branch", branch)
		if err := git.PushCurrentBranch(ctx, wd, false); err != nil {
			return err
		}
		fmt.Printf("Pushed branch '%s' to origin.\n", branch)
	} else {
		log.InfoContext(ctx, "No upstream set for current branch, pushing with -u", "branch", branch)
		if err := git.PushCurrentBranch(ctx, wd, true); err != nil {
			return err
		}
		fmt.Printf("Pushed branch '%s' to origin and set upstream tracking.\n", branch)
	}

	if url := buildBranchURL(repoInfo.Remote, branch); url != "" {
		fmt.Printf("Branch URL: %s\n", url)
	}

	return nil
}

// buildBranchURL attempts to generate a GitHub branch URL from the remote URL and branch name.
// It supports common SSH and HTTPS GitHub URL formats and returns an empty string if it cannot
// confidently derive a URL.
func buildBranchURL(remote, branch string) string {
	remote = strings.TrimSpace(remote)
	branch = strings.TrimSpace(branch)
	if remote == "" || branch == "" {
		return ""
	}

	const host = "github.com"

	// SSH format: git@github.com:owner/repo.git
	if strings.HasPrefix(remote, "git@"+host+":") {
		path := strings.TrimPrefix(remote, "git@"+host+":")
		if strings.HasSuffix(path, ".git") {
			path = strings.TrimSuffix(path, ".git")
		}
		if path == "" {
			return ""
		}
		return fmt.Sprintf("https://%s/%s/tree/%s", host, path, branch)
	}

	// HTTPS/HTTP/git formats: https://github.com/owner/repo.git
	for _, prefix := range []string{"https://" + host + "/", "http://" + host + "/", "git://" + host + "/"} {
		if strings.HasPrefix(remote, prefix) {
			path := strings.TrimPrefix(remote, prefix)
			// Remove possible trailing .git or slash.
			if strings.HasSuffix(path, ".git") {
				path = strings.TrimSuffix(path, ".git")
			}
			path = strings.TrimSuffix(path, "/")
			if path == "" {
				return ""
			}
			return fmt.Sprintf("https://%s/%s/tree/%s", host, path, branch)
		}
	}

	return ""
}
