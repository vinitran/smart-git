package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/vinhtran/git-smart/internal/git"
)

const (
	// defaultModel is aligned with the curl example using v1beta Gemini API.
	// Users can override this via the GEMINI_MODEL environment variable.
	defaultModel      = "gemini-2.0-flash"
	defaultBaseURL    = "https://generativelanguage.googleapis.com/v1beta"
	maxDiffCharacters = 12000
)

// Client handles calls to the Gemini 2.5 Flash API.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
	maxTokens  int
	baseURL    string
}

// RiskLevel represents the AI-assessed risk when running a suggested command.
// It is intentionally simple to keep the UX and safety logic straightforward.
type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

// SuggestedCommand is a single CLI command recommendation returned by the AI.
type SuggestedCommand struct {
	Command     string    `json:"command"`
	Description string    `json:"description"`
	Risk        RiskLevel `json:"risk"`
	Reason      string    `json:"reason,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
}

// SystemContext describes the runtime environment so the AI can tailor
// suggestions (e.g., macOS vs Linux, git repo vs plain folder).
type SystemContext struct {
	OS         string       `json:"os"`
	Shell      string       `json:"shell"`
	WorkingDir string       `json:"working_dir"`
	InGitRepo  bool         `json:"in_git_repo"`
	Repo       git.RepoInfo `json:"repo"`
}

// commandSuggestionEnvelope is the JSON wrapper we expect from Gemini.
// Keeping this type private avoids leaking transport details to callers.
type commandSuggestionEnvelope struct {
	Commands []SuggestedCommand `json:"commands"`
}

// ReviewRequest bundles the information sent to Gemini for analysis.
type ReviewRequest struct {
	Diff      string
	RepoInfo  git.RepoInfo
	Mode      string
	Language  string
	Short     bool
	CreatedAt time.Time
}

// ReviewResponse encapsulates the text returned by Gemini.
type ReviewResponse struct {
	Text string
}

// CommitAnalysisRequest carries the diff used to generate a commit message
// and to check for potential sensitive/private information.
type CommitAnalysisRequest struct {
	Diff     string
	RepoInfo git.RepoInfo
}

// CommitAnalysisResponse wraps the AI-generated commit message,
// suggested branch name, and a simple privacy/sensitivity assessment.
type CommitAnalysisResponse struct {
	CommitMessage  string   `json:"commit_message"`
	BranchName     string   `json:"branch_name"`
	PrivacyRisk    string   `json:"privacy_risk"`              // "low", "medium", "high"
	PrivacyReasons []string `json:"privacy_reasons,omitempty"` // human-readable reasons
}

// NewClient creates a Gemini client.
func NewClient(apiKey string, maxTokens int) *Client {
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = defaultModel
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		maxTokens: maxTokens,
		baseURL:   defaultBaseURL,
	}
}

// SuggestCommands asks Gemini to propose CLI commands for a natural-language
// request, given the current system context. The AI is instructed to respond
// with a strict, machine-parseable JSON structure.
func (c *Client) SuggestCommands(ctx context.Context, message string, sysCtx SystemContext) ([]SuggestedCommand, error) {
	var suggestions []SuggestedCommand

	message = strings.TrimSpace(message)
	if message == "" {
		return suggestions, errors.New("message must not be empty")
	}

	// Fill in OS from runtime as a fallback if the caller did not populate it.
	if strings.TrimSpace(sysCtx.OS) == "" {
		sysCtx.OS = runtime.GOOS
	}

	var builder strings.Builder
	builder.WriteString("You are an expert command-line assistant.\n")
	builder.WriteString("Your job is to translate a user's natural language request into safe, concrete shell commands for their environment.\n")
	builder.WriteString("Always prefer read-only or low-risk commands when possible (inspect, list, show status) over destructive operations.\n")
	builder.WriteString("If a task could be done in multiple ways, choose the safest and simplest command first.\n")
	builder.WriteString("\n")
	builder.WriteString("User request (natural language):\n")
	builder.WriteString(message)
	builder.WriteString("\n\n")
	builder.WriteString("System context (may be approximate):\n")
	builder.WriteString(fmt.Sprintf("- OS: %s\n", sysCtx.OS))
	builder.WriteString("- When OS is \"darwin\", treat it as macOS. Prefer built-in macOS tools such as: top, vm_stat, df, ps, iostat, etc.\n")
	builder.WriteString("- Avoid suggesting Linux-only tools on macOS such as free, /proc-based commands, or other utilities that are not available by default.\n")
	builder.WriteString(fmt.Sprintf("- Shell: %s\n", sysCtx.Shell))
	builder.WriteString(fmt.Sprintf("- Working directory: %s\n", sysCtx.WorkingDir))
	if sysCtx.InGitRepo {
		builder.WriteString(fmt.Sprintf("- Git repo path: %s\n", sysCtx.Repo.Path))
		builder.WriteString(fmt.Sprintf("- Git branch: %s\n", sysCtx.Repo.Branch))
		builder.WriteString(fmt.Sprintf("- Git remote: %s\n", sysCtx.Repo.Remote))
	} else {
		builder.WriteString("- Not inside a git repository.\n")
	}
	builder.WriteString("\n")
	builder.WriteString("JSON response requirements (very important):\n")
	builder.WriteString("- Respond ONLY as a single valid JSON object, with no extra text, no explanation, no markdown, and no code fences.\n")
	builder.WriteString("- The JSON must have exactly this shape and key names:\n")
	builder.WriteString(`{"commands":[{"command":"<shell command>","description":"<short human explanation>","risk":"<low|medium|high>","reason":"<why this command fits>","tags":["tag1","tag2"]}]}` + "\n")
	builder.WriteString("- The top-level object MUST contain a \"commands\" array.\n")
	builder.WriteString("- Put the BEST, safest command that most directly satisfies the request as the FIRST element in the array.\n")
	builder.WriteString("- You may include up to 3 commands total. If only one command is clearly best, return a single-element array.\n")
	builder.WriteString("- The \"command\" value must be a single-line shell command ready to paste into a terminal.\n")
	builder.WriteString("- The \"description\" must be short, clear, and end without a period.\n")
	builder.WriteString("- The \"risk\" field must be one of exactly: low, medium, high (lowercase).\n")
	builder.WriteString("- Use risk=low for read-only commands (viewing status, logs, memory, disk, etc.).\n")
	builder.WriteString("- Use risk=medium for commands that modify local state but are reversible or low impact.\n")
	builder.WriteString("- Use risk=high ONLY for destructive or hard-to-undo actions (deleting data, rewriting git history, formatting disks, etc.).\n")
	builder.WriteString("- Avoid suggesting high-risk commands unless the user explicitly asks for a destructive operation.\n")
	builder.WriteString("- The \"reason\" field should briefly explain why the command is appropriate for the request.\n")
	builder.WriteString("- The \"tags\" field is optional but recommended; use simple tags like system, git, network, process, disk, ram, cpu.\n")
	builder.WriteString("- Do NOT wrap the JSON in ``` or ```json. Do NOT add any commentary before or after the JSON.\n")

	userPrompt := builder.String()

	payload := generateContentRequest{
		Contents: []content{
			{
				Role: "user",
				Parts: []part{
					{Text: userPrompt},
				},
			},
		},
		GenerationConfig: &generationConfig{
			MaxOutputTokens: intPtr(c.maxTokens),
			Temperature:     floatPtr(0.4),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return suggestions, err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return suggestions, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return suggestions, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var apiErr map[string]any
		_ = json.NewDecoder(httpResp.Body).Decode(&apiErr)
		return suggestions, fmt.Errorf("gemini API error: status=%d body=%v", httpResp.StatusCode, apiErr)
	}

	var genResp generateContentResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&genResp); err != nil {
		return suggestions, err
	}

	text, err := genResp.extractText()
	if err != nil {
		return suggestions, err
	}

	rawJSON := extractJSONBlock(text)
	if strings.TrimSpace(rawJSON) == "" {
		return suggestions, fmt.Errorf("failed to find JSON object in Gemini response: %q", text)
	}

	var envelope commandSuggestionEnvelope
	if err := json.Unmarshal([]byte(rawJSON), &envelope); err != nil {
		return suggestions, fmt.Errorf("failed to parse command suggestions JSON from Gemini: %w; raw=%q", err, rawJSON)
	}

	// Normalize risk values and filter out clearly invalid entries.
	for _, s := range envelope.Commands {
		cmd := strings.TrimSpace(s.Command)
		if cmd == "" {
			continue
		}
		desc := strings.TrimSpace(s.Description)
		risk := RiskLevel(strings.ToLower(strings.TrimSpace(string(s.Risk))))
		if risk == "" {
			risk = RiskLevelLow
		}
		switch risk {
		case RiskLevelLow, RiskLevelMedium, RiskLevelHigh:
			// ok
		default:
			risk = RiskLevelMedium
		}

		suggestions = append(suggestions, SuggestedCommand{
			Command:     cmd,
			Description: desc,
			Risk:        risk,
			Reason:      strings.TrimSpace(s.Reason),
			Tags:        s.Tags,
		})
	}

	if len(suggestions) == 0 {
		return suggestions, errors.New("Gemini returned no usable command suggestions")
	}

	return suggestions, nil
}

// ReviewDiff sends the diff to Gemini for feedback and returns the response text.
func (c *Client) ReviewDiff(ctx context.Context, req ReviewRequest) (ReviewResponse, error) {
	var resp ReviewResponse

	if req.Diff == "" {
		return resp, errors.New("diff is empty")
	}

	userPrompt := buildPrompt(req)
	payload := generateContentRequest{
		Contents: []content{
			{
				Role: "user",
				Parts: []part{
					{Text: userPrompt},
				},
			},
		},
		GenerationConfig: &generationConfig{
			MaxOutputTokens: intPtr(c.maxTokens),
			Temperature:     floatPtr(0.4),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return resp, err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return resp, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var apiErr map[string]any
		_ = json.NewDecoder(httpResp.Body).Decode(&apiErr)
		return resp, fmt.Errorf("gemini API error: status=%d body=%v", httpResp.StatusCode, apiErr)
	}

	var genResp generateContentResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&genResp); err != nil {
		return resp, err
	}

	text, err := genResp.extractText()
	if err != nil {
		return resp, err
	}

	resp.Text = text
	return resp, nil
}

// AnalyzeCommit asks Gemini to suggest a commit message for the given diff,
// and to flag potential leakage of private or sensitive information.
func (c *Client) AnalyzeCommit(ctx context.Context, req CommitAnalysisRequest) (CommitAnalysisResponse, error) {
	var resp CommitAnalysisResponse

	if strings.TrimSpace(req.Diff) == "" {
		return resp, errors.New("diff is empty")
	}

	var builder strings.Builder
	builder.WriteString("You are an experienced software engineer and security-conscious reviewer.\n")
	builder.WriteString("Task 1: Analyze the git diff and produce a short, simple git commit message following the Conventional Commits style described below.\n")
	builder.WriteString("Task 2: Check if the diff might leak private or sensitive information (secrets, keys, tokens, passwords, personal data, internal URLs, etc.).\n")
	builder.WriteString("Commit message requirements (very important):\n")
	builder.WriteString("- Use Conventional Commits format: <type>(<optional scope>): <description>\n")
	builder.WriteString("- Valid types: feat, fix, refactor, perf, style, test, docs, build, ops, chore, revert.\n")
	builder.WriteString("- Choose type based on change kind: feat for new feature, fix for bug fix, docs for documentation only, refactor for internal restructuring without behavior change, perf for performance optimizations, build for build/CI/deps, ops for infra/operations, chore for general maintenance.\n")
	builder.WriteString("- Scope is optional; when used, keep it short and related to component/module (e.g., auth, download, api).\n")
	builder.WriteString("- Description rules:\n")
	builder.WriteString("  * Use imperative, present tense: add, fix, update, remove, refactor, etc.\n")
	builder.WriteString("  * Do not capitalize the first letter of the description.\n")
	builder.WriteString("  * Do not end the description with a period.\n")
	builder.WriteString("  * Keep the description very short and easy to understand (target <= 50 characters).\n")
	builder.WriteString("  * Prefer simple, everyday English and avoid complex or fancy wording.\n")
	builder.WriteString("- For breaking changes, use an exclamation mark before the colon in the header, e.g.: feat(api)!: remove status endpoint\n")
	builder.WriteString("- For breaking changes, also add a footer line starting with BREAKING CHANGE: followed by a short explanation. You may add an empty line before the footer.\n")
	builder.WriteString("- In most cases, only use a single-line header without a body. Add a body only when it is really necessary to explain something important.\n")
	builder.WriteString("- Do NOT include markdown formatting, bullet characters, code fences, or backticks in the commit message.\n")
	builder.WriteString("- Do NOT include any commentary or explanation around the commit message.\n")
	builder.WriteString("Branch naming requirements (very important):\n")
	builder.WriteString("- Suggest a branch name suitable for feature or fix branches, following this pattern as closely as possible:\n")
	builder.WriteString("  <category>/<short-kebab-description>\n")
	builder.WriteString("- Valid category prefixes include: feature, fix, hotfix, refactor, docs, chore, test, perf, ops, build.\n")
	builder.WriteString("- Derive the description from the commit message description; use lowercase letters, numbers, and dashes only.\n")
	builder.WriteString("- Keep branch names reasonably short (for example, under 40 characters after the category/ prefix).\n")
	builder.WriteString("- Example branch names: feature/add-smartgit-commit-flow, fix/login-timeout, docs/update-readme.\n")
	builder.WriteString("JSON response requirements (very important):\n")
	builder.WriteString("- Respond ONLY as a single valid JSON object, with no extra text, no explanation, no markdown, and no code fences.\n")
	builder.WriteString("- The JSON must have exactly this shape and key names:\n")
	builder.WriteString(`{"commit_message": "<commit message>", "branch_name": "<branch name>", "privacy_risk": "<low|medium|high>", "privacy_reasons": ["reason 1", "reason 2"]}` + "\n")
	builder.WriteString("- Do NOT wrap the JSON in ``` or ```json. Do NOT add any commentary before or after the JSON.\n")
	builder.WriteString("Requirements for commit_message:\n")
	builder.WriteString("- Usually just a single short header line (max ~72 characters, target <= 50 characters).\n")
	builder.WriteString("- Only add an optional body (after a blank line) when absolutely needed to clarify complex changes.\n")
	builder.WriteString("- Do NOT include markdown formatting, bullet points, quotes, or backticks.\n")
	builder.WriteString("- Do NOT include any surrounding commentary, only the commit message text itself.\n")
	builder.WriteString("Requirements for privacy_risk:\n")
	builder.WriteString("- Use only one of: low, medium, high.\n")
	builder.WriteString("- Use \"high\" if there is a clear chance of credentials, tokens, secrets, or personal data being exposed.\n")
	builder.WriteString("Requirements for privacy_reasons:\n")
	builder.WriteString("- Provide short, human-readable reasons if risk is medium or high; can be empty for low.\n")
	builder.WriteString(fmt.Sprintf("Repository path: %s\nBranch: %s\nRemote: %s\n",
		req.RepoInfo.Path,
		req.RepoInfo.Branch,
		req.RepoInfo.Remote,
	))
	builder.WriteString("Git diff:\n")
	builder.WriteString("---\n")
	builder.WriteString(trimDiff(req.Diff))
	builder.WriteString("\n---\n")

	userPrompt := builder.String()

	payload := generateContentRequest{
		Contents: []content{
			{
				Role: "user",
				Parts: []part{
					{Text: userPrompt},
				},
			},
		},
		GenerationConfig: &generationConfig{
			MaxOutputTokens: intPtr(256),
			Temperature:     floatPtr(0.3),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return resp, err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return resp, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var apiErr map[string]any
		_ = json.NewDecoder(httpResp.Body).Decode(&apiErr)
		return resp, fmt.Errorf("gemini API error: status=%d body=%v", httpResp.StatusCode, apiErr)
	}

	var genResp generateContentResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&genResp); err != nil {
		return resp, err
	}

	text, err := genResp.extractText()
	if err != nil {
		return resp, err
	}

	clean := extractJSONBlock(text)
	if strings.TrimSpace(clean) == "" {
		return resp, fmt.Errorf("failed to find JSON object in Gemini response: %q", text)
	}

	var parsed CommitAnalysisResponse
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return resp, fmt.Errorf("failed to parse commit analysis JSON from Gemini: %w; raw=%q", err, clean)
	}

	parsed.CommitMessage = strings.TrimSpace(parsed.CommitMessage)
	parsed.BranchName = strings.TrimSpace(parsed.BranchName)
	parsed.PrivacyRisk = strings.ToLower(strings.TrimSpace(parsed.PrivacyRisk))
	resp = parsed
	return resp, nil
}

// extractJSONBlock tries to pull the first top-level JSON object from a text response.
func extractJSONBlock(s string) string {
	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}

	// Simple brace matching to find the matching closing brace.
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

func buildPrompt(req ReviewRequest) string {
	lang := strings.ToLower(req.Language)
	if lang != "vi" {
		lang = "en"
	}

	modeLabel := "staged changes"
	if req.Mode == "last-commit" {
		modeLabel = "latest commit"
	}

	var builder strings.Builder
	builder.WriteString("You are an experienced software engineer performing a code review for git changes.\n")
	builder.WriteString("Provide structured feedback with sections: Overview, Risks/Bugs, Refactoring Ideas, Testing Suggestions, Commit Message feedback.\n")
	if req.Short {
		builder.WriteString("Focus on the most critical issues and keep the response concise.\n")
	}
	if lang == "vi" {
		builder.WriteString("Respond in Vietnamese with clear, natural language.\n")
	} else {
		builder.WriteString("Respond in English with clear, natural language.\n")
	}
	builder.WriteString(fmt.Sprintf("Repository path: %s\nBranch: %s\nRemote: %s\nReview target: %s\nDate: %s\n",
		req.RepoInfo.Path,
		req.RepoInfo.Branch,
		req.RepoInfo.Remote,
		modeLabel,
		req.CreatedAt.Format(time.RFC3339),
	))
	builder.WriteString("Git diff:\n")
	builder.WriteString("---\n")
	builder.WriteString(trimDiff(req.Diff))
	builder.WriteString("\n---\n")
	builder.WriteString("Deliver actionable insights and mention missing tests or risks explicitly.\n")
	return builder.String()
}

func trimDiff(diff string) string {
	diff = strings.TrimSpace(diff)
	if len(diff) <= maxDiffCharacters {
		return diff
	}
	return diff[:maxDiffCharacters] + "\n... (diff truncated)"
}

type generateContentRequest struct {
	Contents         []content         `json:"contents"`
	GenerationConfig *generationConfig `json:"generationConfig,omitempty"`
	SafetySettings   []any             `json:"safetySettings,omitempty"`
	Tools            []any             `json:"tools,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts,omitempty"`
}

type part struct {
	Text string `json:"text,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
}

type generateContentResponse struct {
	Candidates []struct {
		Content content `json:"content"`
	} `json:"candidates"`
}

func (resp generateContentResponse) extractText() (string, error) {
	if len(resp.Candidates) == 0 {
		return "", errors.New("gemini response contained no candidates")
	}

	for _, candidate := range resp.Candidates {
		for _, p := range candidate.Content.Parts {
			text := strings.TrimSpace(p.Text)
			if text != "" {
				return text, nil
			}
		}
	}

	return "", errors.New("gemini response contained no non-empty text parts in any candidate")
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
