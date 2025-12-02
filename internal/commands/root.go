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
}

func setupLogger(ctx context.Context) error {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	logger.Setup(level)
	return nil
}
