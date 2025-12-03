package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
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
		Use:     "command",
		Aliases: []string{"cmd"},
		Short:   "Ask AI to suggest shell commands from a natural language request",
		Args:    cobra.MinimumNArgs(1),
		RunE:    runCommandSuggest,
	}
	commandSuggestOpts commandSuggestOptions
)

const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
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

	if commandSuggestOpts.dryRun {
		renderSuggestions(message, suggestions)
		fmt.Println("Dry run mode: no commands will be executed.")
		return nil
	}

	if commandSuggestOpts.autoAccept {
		primary := suggestions[0]
		primary.Risk = normalizeRisk(primary.Command, primary.Risk)
		renderSuggestions(message, suggestions)
		return runSuggestedCommand(ctx, primary)
	}

	selected, ok := chooseSuggestionInteractive(message, suggestions)
	if !ok {
		fmt.Println("Cancelled. No command was executed.")
		return nil
	}
	selected.Risk = normalizeRisk(selected.Command, selected.Risk)
	return runSuggestedCommand(ctx, selected)
}

func renderSuggestions(message string, suggestions []ai.SuggestedCommand) {
	divider := strings.Repeat("─", 50)
	fmt.Printf("%s%s%s\n", colorCyan, divider, colorReset)
	fmt.Printf("%ssg cmd%s  %s\n", colorCyan, colorReset, message)
	fmt.Printf("%s%s%s\n", colorCyan, divider, colorReset)

	limit := len(suggestions)
	if limit > 2 {
		limit = 2
	}

	for i := 0; i < limit; i++ {
		s := suggestions[i]
		risk := strings.ToUpper(string(s.Risk))
		riskColor := colorForRisk(s.Risk)
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			desc = "no description"
		}
		fmt.Printf("[%d] %s%s%s  %s(%s)%s - %s\n",
			i+1,
			colorCyan, s.Command, colorReset,
			riskColor, risk, colorReset,
			desc,
		)
	}

	cancelIndex := limit + 1
	fmt.Printf("[%d] %sCancel%s\n", cancelIndex, colorRed, colorReset)
	fmt.Printf("%s%s%s\n", colorCyan, divider, colorReset)
}

func chooseSuggestionInteractive(message string, suggestions []ai.SuggestedCommand) (ai.SuggestedCommand, bool) {
	limit := len(suggestions)
	if limit > 2 {
		limit = 2
	}

	// Build option labels (including Cancel).
	items := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		s := suggestions[i]
		risk := strings.ToUpper(string(s.Risk))
		riskLabel := risk
		if s.Risk == ai.RiskLevelHigh {
			riskLabel = fmt.Sprintf("%s%s%s", colorRed, risk, colorReset)
		}
		items = append(items, fmt.Sprintf("%s  (%s)", s.Command, riskLabel))
	}
	items = append(items, "Cancel")

	summary := buildRequestSummary(message, suggestions)

	prompt := promptui.Select{
		Label:    fmt.Sprintf("%s", summary),
		Items:    items,
		Size:     len(items),
		HideHelp: true,
		Templates: &promptui.SelectTemplates{
			Label:    fmt.Sprintf("%s{{ . }}%s", colorCyan, colorReset),
			Active:   fmt.Sprintf("%s▸ {{ . | cyan }}%s", colorCyan, colorReset),
			Inactive: "  {{ . }}",
			Selected: fmt.Sprintf("%s✓{{ . }}%s", colorGreen, colorReset),
		},
	}

	index, _, err := prompt.Run()
	if err != nil {
		return ai.SuggestedCommand{}, false
	}

	if index >= limit {
		return ai.SuggestedCommand{}, false
	}

	return suggestions[index], true
}

// buildRequestSummary creates a very short, human-readable summary line
// based primarily on the first AI suggestion's description, falling back
// to the original message when needed.
func buildRequestSummary(message string, suggestions []ai.SuggestedCommand) string {
	base := strings.TrimSpace(message)
	if len(suggestions) > 0 {
		desc := strings.TrimSpace(suggestions[0].Description)
		if desc != "" {
			base = desc
		}
	}
	if len(base) > 60 {
		base = base[:57] + "..."
	}
	return base
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

func colorForRisk(r ai.RiskLevel) string {
	switch r {
	case ai.RiskLevelLow:
		return colorGreen
	case ai.RiskLevelMedium:
		return colorYellow
	case ai.RiskLevelHigh:
		return colorRed
	default:
		return colorReset
	}
}
