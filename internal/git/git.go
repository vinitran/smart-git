package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// RepoInfo captures helpful metadata about the current repository.
type RepoInfo struct {
	Path   string
	Branch string
	Remote string
}

var (
	// ErrNotRepository indicates the command was executed outside a git repo.
	ErrNotRepository = errors.New("current directory is not inside a git repository")
)

// Run executes a git command within dir and returns combined stdout/stderr.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String(), nil
}

// EnsureRepository verifies that the current folder is a git repo.
func EnsureRepository(ctx context.Context, dir string) error {
	output, err := Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return ErrNotRepository
	}
	if strings.TrimSpace(output) != "true" {
		return ErrNotRepository
	}
	return nil
}

// GetRepoInfo fetches repo metadata for AI context.
func GetRepoInfo(ctx context.Context, dir string) (RepoInfo, error) {
	var info RepoInfo
	info.Path = dir

	if err := EnsureRepository(ctx, dir); err != nil {
		return info, err
	}

	if branch, err := Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}

	if remote, err := Run(ctx, dir, "config", "--get", "remote.origin.url"); err == nil {
		info.Remote = strings.TrimSpace(remote)
	}

	return info, nil
}

// GetStagedDiff returns the staged diff (git diff --cached).
func GetStagedDiff(ctx context.Context, dir string) (string, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "diff", "--cached")
	return out, err
}

// GetWorkingTreeDiff returns the unstaged diff (git diff).
func GetWorkingTreeDiff(ctx context.Context, dir string) (string, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "diff")
	return out, err
}

// GetLastCommitDiff returns the diff for the latest commit (git show HEAD).
func GetLastCommitDiff(ctx context.Context, dir string) (string, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "show", "HEAD")
	return out, err
}

// StatusPorcelain returns git status in porcelain format.
func StatusPorcelain(ctx context.Context, dir string) (string, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "status", "--porcelain")
	return out, err
}

// AddAll stages all changes (tracked and untracked) in the repository.
func AddAll(ctx context.Context, dir string) error {
	if err := EnsureRepository(ctx, dir); err != nil {
		return err
	}
	_, err := Run(ctx, dir, "add", "-A")
	return err
}

// Commit creates a new commit with the given message.
func Commit(ctx context.Context, dir, message string) error {
	if err := EnsureRepository(ctx, dir); err != nil {
		return err
	}
	_, err := Run(ctx, dir, "commit", "-m", message)
	return err
}

// CurrentBranch returns the current branch name.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// CreateAndCheckoutBranch creates a new branch from the current HEAD and checks it out.
func CreateAndCheckoutBranch(ctx context.Context, dir, name string) error {
	if err := EnsureRepository(ctx, dir); err != nil {
		return err
	}
	_, err := Run(ctx, dir, "checkout", "-b", name)
	return err
}

// PushCurrentBranch pushes the current branch to origin, optionally setting upstream.
func PushCurrentBranch(ctx context.Context, dir string, setUpstream bool) error {
	branch, err := CurrentBranch(ctx, dir)
	if err != nil {
		return err
	}
	if branch == "" {
		return errors.New("could not determine current branch name")
	}

	args := []string{"push"}
	if setUpstream {
		args = append(args, "-u")
	}
	args = append(args, "origin", branch)

	_, err = Run(ctx, dir, args...)
	return err
}

// HasUpstream reports whether the current branch has an upstream configured.
func HasUpstream(ctx context.Context, dir string) (bool, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return false, err
	}
	// This command fails if there is no upstream.
	_, err := Run(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return false, nil
	}
	return true, nil
}

// LastCommitSubject returns the subject line of the latest commit (git log -1 --pretty=%s).
func LastCommitSubject(ctx context.Context, dir string) (string, error) {
	if err := EnsureRepository(ctx, dir); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "log", "-1", "--pretty=%s")
	return strings.TrimSpace(out), err
}
