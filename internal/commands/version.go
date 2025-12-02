package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vinhtran/git-smart/internal/version"
	"github.com/vinhtran/git-smart/pkg/logger"
)

type versionOptions struct {
	timeout time.Duration
}

var (
	versionCmd = &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Show current version and check for updates",
		RunE:    runVersion,
	}
	versionOpts versionOptions
)

func init() {
	rootCmd.AddCommand(versionCmd)

	versionCmd.Flags().DurationVar(&versionOpts.timeout, "timeout", 10*time.Second, "Timeout for version check request")
}

func runVersion(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), versionOpts.timeout)
	defer cancel()

	log := logger.L().With("command", "version")

	fmt.Printf("Current version: %s\n", version.Current)

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		// Do not fail the command just because version check failed.
		log.InfoContext(ctx, "Failed to check latest version", "error", err)
		return nil
	}

	if latest == "" || latest == version.Current {
		fmt.Println("You are on the latest version.")
		return nil
	}

	fmt.Printf("A new version is available: %s (current %s)\n", latest, version.Current)
	fmt.Print("Do you want to update now? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		fmt.Println("Update skipped.")
		return nil
	}

	if err := performSelfUpdate(ctx); err != nil {
		return fmt.Errorf("failed to update git-smart: %w", err)
	}

	fmt.Println("Update completed. Please re-run the command.")
	return nil
}

func fetchLatestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, version.LatestURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("unexpected status %d from version server: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

// performSelfUpdate tries to pull and rebuild the CLI binary.
// For safety, it only runs if you configure the local repo path
// via the GIT_SMART_HOME environment variable.
func performSelfUpdate(ctx context.Context) error {
	repoDir := strings.TrimSpace(os.Getenv("GIT_SMART_HOME"))
	if repoDir == "" {
		return fmt.Errorf("GIT_SMART_HOME is not set; please update manually (git pull && go build)")
	}

	// 1. git pull --rebase
	gitCmd := exec.CommandContext(ctx, "git", "pull", "--rebase")
	gitCmd.Dir = repoDir
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("git pull --rebase failed: %w", err)
	}

	// 2. go build -o <current-binary> ./cmd/smartgit
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine current executable path: %w", err)
	}

	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", exePath, "./cmd/smartgit")
	buildCmd.Dir = repoDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	// In case the binary was rebuilt in a different directory, ensure path is absolute.
	if !filepath.IsAbs(exePath) {
		if abs, err := filepath.Abs(exePath); err == nil {
			_ = abs // silence unused variable, kept for future use if needed
		}
	}

	return nil
}

// checkForUpdateOnStartup runs a lightweight version check on every CLI invocation.
// It prints a warning if a newer version is available but never fails the command.
func checkForUpdateOnStartup(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	log := logger.L().With("command", "startup-version-check")

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		log.DebugContext(ctx, "version check on startup failed", "error", err)
		return
	}

	if latest == "" || latest == version.Current {
		return
	}

	fmt.Fprintf(os.Stderr, "Warning: a new version of sg is available: %s (current %s). Run 'sg version' to update.\n", latest, version.Current)
}
