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
		Use:     "sg",
		Aliases: []string{"smartgit"},
		Short:   "SmartGit is a git companion CLI powered by AI reviews",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := setupLogger(cmd.Context()); err != nil {
				return err
			}
			// Best-effort version check; never fail the actual command.
			checkForUpdateOnStartup(cmd.Context())
			return nil
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
	// Default to error level to keep output quiet.
	level := slog.LevelError
	// --verbose: show info logs.
	if verbose {
		level = slog.LevelInfo
	}
	// --debug: show debug logs.
	if debug {
		level = slog.LevelDebug
	}
	logger.Setup(level)
	return nil
}
