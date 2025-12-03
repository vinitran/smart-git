package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vinhtran/git-smart/internal/ai"
	"github.com/vinhtran/git-smart/internal/git"
	"github.com/vinhtran/git-smart/pkg/logger"
)

type commandSuggestOptions struct {
	timeout    time.Duration
	maxTokens  int
	autoAccept bool
	dryRun     bool
}

var (
	commandSuggestCmd = &cobra.Command{
		Use:   "command",
		Short: "Ask AI to suggest shell commands from a natural language request",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runCommandSuggest,
	}
	commandSuggestOpts commandSuggestOptions
)

func init() {
	rootCmd.AddCommand(commandSuggestCmd)

	commandSuggestCmd.Flags().DurationVar(&commandSuggestOpts.timeout, "timeout", 45*time.Second, "Timeout for the Gemini command suggestion request")
	commandSuggestCmd.Flags().IntVar(&commandSuggestOpts.maxTokens, "max-tokens", 512, "Maximum tokens for Gemini output when suggesting commands")
	commandSuggestCmd.Flags().BoolVar(&commandSuggestOpts.autoAccept, "auto-accept", false, "Automatically run the top suggestion without asking for confirmation")
	commandSuggestCmd.Flags().BoolVar(&commandSuggestOpts.dryRun, "dry-run", false, "Only show suggested commands without executing anything")
}

func runCommandSuggest(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), commandSuggestOpts.timeout)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	log := logger.L().With("command", "command", "path", wd)

	message := strings.TrimSpace(strings.Join(args, " "))
	if message == "" {
		return fmt.Errorf("request message must not be empty")
	}

	// Build system context for the AI.
	sysCtx := ai.SystemContext{
		OS:         "", // filled by SuggestCommands when empty
		Shell:      strings.TrimSpace(os.Getenv("SHELL")),
		WorkingDir: wd,
	}

	// Try to enrich with git context; if we are not in a repo, that's fine.
	if repoInfo, err := git.GetRepoInfo(ctx, wd); err == nil {
		sysCtx.InGitRepo = true
		sysCtx.Repo = repoInfo
	}

	apiKey, err := resolveAPIKey(ctx)
	if err != nil {
		return err
	}

	client := ai.NewClient(apiKey, commandSuggestOpts.maxTokens)

	log.InfoContext(ctx, "Requesting Gemini command suggestions")
	suggestions, err := client.SuggestCommands(ctx, message, sysCtx)
	if err != nil {
		return err
	}

	primary := suggestions[0]
	primary.Risk = normalizeRisk(primary.Command, primary.Risk)

	if commandSuggestOpts.dryRun {
		renderSuggestions(message, primary, suggestions)
		fmt.Println("Dry run mode: no commands will be executed.")
		return nil
	}

	if commandSuggestOpts.autoAccept {
		renderSuggestions(message, primary, suggestions)
		return runSuggestedCommand(ctx, primary)
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		renderSuggestions(message, primary, suggestions)
		fmt.Print("Run this command? [y]es / [n]o / [e]dit / [s]ee more: ")

		line, _ := reader.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))

		switch choice {
		case "y", "yes", "":
			return runSuggestedCommand(ctx, primary)
		case "n", "no":
			fmt.Println("Cancelled. No command was executed.")
			return nil
		case "e", "edit":
			fmt.Print("Enter the exact command you want to run: ")
			line, _ := reader.ReadString('\n')
			edited := strings.TrimSpace(line)
			if edited != "" {
				primary.Command = edited
				primary.Risk = normalizeRisk(primary.Command, primary.Risk)
			}
		case "s", "see", "more":
			selected, ok := selectAlternateSuggestion(reader, suggestions, primary)
			if ok {
				primary = selected
				primary.Risk = normalizeRisk(primary.Command, primary.Risk)
			}
		default:
			fmt.Println("Invalid choice, please try again.")
		}
	}
}

func renderSuggestions(message string, primary ai.SuggestedCommand, all []ai.SuggestedCommand) {
	divider := strings.Repeat("-", 60)
	fmt.Println(divider)
	fmt.Printf("Your request: %s\n", message)
	fmt.Println(divider)

	fmt.Println("Top suggestion:")
	fmt.Printf("  > %s\n", primary.Command)
	if strings.TrimSpace(primary.Description) != "" {
		fmt.Printf("    Description: %s\n", primary.Description)
	}

	riskLabel := strings.ToUpper(string(primary.Risk))
	fmt.Printf("    Risk: %s\n", riskLabel)
	if strings.TrimSpace(primary.Reason) != "" {
		fmt.Printf("    Reason: %s\n", primary.Reason)
	}

	if len(all) > 1 {
		fmt.Printf("\nThere are %d more suggestion(s). Press [s]ee more to view details.\n", len(all)-1)
	}
	fmt.Println(divider)
}

func selectAlternateSuggestion(reader *bufio.Reader, suggestions []ai.SuggestedCommand, current ai.SuggestedCommand) (ai.SuggestedCommand, bool) {
	if len(suggestions) <= 1 {
		fmt.Println("There is only one suggestion at the moment.")
		return current, false
	}

	fmt.Println("Other suggestions:")
	for i, s := range suggestions {
		fmt.Printf("  %d) %s (risk: %s)\n", i+1, s.Command, strings.ToUpper(string(s.Risk)))
		if desc := strings.TrimSpace(s.Description); desc != "" {
			fmt.Printf("     - %s\n", desc)
		}
	}

	fmt.Print("Enter a number to pick a different suggestion (or press Enter to keep current): ")
	line, _ := reader.ReadString('\n')
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return current, false
	}

	idx, err := strconv.Atoi(trimmed)
	if err != nil || idx < 1 || idx > len(suggestions) {
		fmt.Println("Invalid number, keeping the current suggestion.")
		return current, false
	}

	return suggestions[idx-1], true
}

func runSuggestedCommand(ctx context.Context, suggestion ai.SuggestedCommand) error {
	cmdStr := strings.TrimSpace(suggestion.Command)
	if cmdStr == "" {
		return fmt.Errorf("no valid command to execute")
	}

	fmt.Printf("About to execute: %s\n", cmdStr)

	switch suggestion.Risk {
	case ai.RiskLevelHigh:
		fmt.Println("WARNING: this command is considered HIGH RISK.")
		fmt.Print("Type \"yes\" to confirm execution, or press Enter to cancel: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "yes" {
			fmt.Println("Cancelled due to high risk.")
			return nil
		}
	case ai.RiskLevelMedium:
		fmt.Println("Note: this command has MEDIUM RISK (it may change system state).")
	}

	fmt.Printf("Running: %s\n", cmdStr)

	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "sh"
	}

	execCmd := exec.CommandContext(ctx, shell, "-c", cmdStr)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin

	if err := execCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ProcessState != nil {
			fmt.Printf("Command exited with status code %d\n", exitErr.ProcessState.ExitCode())
		} else {
			fmt.Printf("Error while executing command: %v\n", err)
		}
		return err
	}

	return nil
}

// normalizeRisk adjusts the AI-reported risk with simple rule-based checks
// on the command string. This is a lightweight safety net and does not try
// to be perfect.
func normalizeRisk(cmd string, aiRisk ai.RiskLevel) ai.RiskLevel {
	cmdLower := strings.ToLower(cmd)

	// Destructive patterns that should always be considered high risk.
	dangerousPatterns := []string{
		"rm -rf /",
		"rm -rf /*",
		":(){:|:&};:",
		"mkfs",
		"dd if=",
		"mklabel gpt",
	}
	for _, p := range dangerousPatterns {
		if strings.Contains(cmdLower, p) {
			return ai.RiskLevelHigh
		}
	}

	// If AI says low but the command clearly modifies state, bump to medium.
	if aiRisk == ai.RiskLevelLow {
		modifyingPrefixes := []string{
			"rm ", "mv ", "cp ", "sudo ", "chmod ", "chown ",
			"git reset", "git push", "git rebase", "git checkout",
		}
		for _, p := range modifyingPrefixes {
			if strings.HasPrefix(cmdLower, p) {
				return ai.RiskLevelMedium
			}
		}
	}

	if aiRisk == "" {
		return ai.RiskLevelLow
	}
	return aiRisk
}
