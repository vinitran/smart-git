package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/vinhtran/git-smart/internal/git"
	"github.com/vinhtran/git-smart/pkg/logger"
)

type switchOptions struct {
	timeout time.Duration
}

var (
	switchCmd = &cobra.Command{
		Use:     "switch <branch>",
		Aliases: []string{"sw"},
		Short:   "Switch to a branch and pull from origin with rebase",
		Args:    cobra.ExactArgs(1),
		RunE:    runSwitch,
	}
	switchOpts switchOptions
)

func init() {
	rootCmd.AddCommand(switchCmd)

	switchCmd.Flags().DurationVar(&switchOpts.timeout, "timeout", 45*time.Second, "Timeout for the branch switch and pull operation")
}

func runSwitch(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), switchOpts.timeout)
	defer cancel()

	targetBranch := args[0]

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	log := logger.L().With("command", "switch", "path", wd, "target_branch", targetBranch)

	if err := git.EnsureRepository(ctx, wd); err != nil {
		return err
	}

	log.InfoContext(ctx, "Checking out target branch")
	if err := git.CheckoutBranch(ctx, wd, targetBranch); err != nil {
		return err
	}

	log.InfoContext(ctx, "Pulling latest changes with rebase from origin")
	if err := git.PullRebase(ctx, wd, "origin", targetBranch); err != nil {
		return err
	}

	fmt.Printf("Switched to '%s' and rebased on origin/%s.\n", targetBranch, targetBranch)
	return nil
}
