package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/vinhtran/git-smart/pkg/logger"
)

var (
	rootCmd = &cobra.Command{
		Use:   "smartgit",
		Short: "SmartGit is a git companion CLI powered by AI reviews",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return setupLogger(cmd.Context())
		},
	}
	verbose bool
	debug   bool
)

// Execute runs the root command for SmartGit.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
}

func setupLogger(ctx context.Context) error {
	// Default: keep logging very quiet (only errors).
	level := slog.LevelError
	// If either verbose or debug is enabled, show debug/info logs.
	if verbose || debug {
		level = slog.LevelDebug
	}
	logger.Setup(level)
	return nil
}
