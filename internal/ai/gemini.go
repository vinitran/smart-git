package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

// CommitAnalysisResponse wraps the AI-generated commit message
// and a simple privacy/sensitivity assessment.
type CommitAnalysisResponse struct {
	CommitMessage  string   `json:"commit_message"`
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
	builder.WriteString("Task 1: Analyze the git diff and produce a high-quality git commit message following the Conventional Commits style used in this project.\n")
	builder.WriteString("Task 2: Check if the diff might leak private or sensitive information (secrets, keys, tokens, passwords, personal data, internal URLs, etc.).\n")
	builder.WriteString("Commit message requirements (very important):\n")
	builder.WriteString("- Use Conventional Commits format: <type>(<scope>): <subject>\n")
	builder.WriteString("- Valid types: feat, fix, docs, refactor, test, perf, style, chore, ci, build, revert.\n")
	builder.WriteString("- Pick the most appropriate type based on the change (e.g., feat for new feature, fix for bug fix, docs for documentation only).\n")
	builder.WriteString("- Scope is optional; when used, keep it short and related to component/module (e.g., auth, download, api).\n")
	builder.WriteString("- Subject: imperative mood (add, fix, update, remove, refactor, etc.), max ~50 characters, no trailing period.\n")
	builder.WriteString("- Optional body: one or more lines after a blank line, wrapped at ~72 characters, describing WHY the change was made.\n")
	builder.WriteString("- Do NOT include markdown formatting, bullet characters, code fences, or backticks in the commit message.\n")
	builder.WriteString("- Do NOT include any commentary or explanation around the commit message.\n")
	builder.WriteString("JSON response requirements (very important):\n")
	builder.WriteString("- Respond ONLY as a single valid JSON object, with no extra text, no explanation, no markdown, and no code fences.\n")
	builder.WriteString("- The JSON must have exactly this shape and key names:\n")
	builder.WriteString(`{"commit_message": "<commit message>", "privacy_risk": "<low|medium|high>", "privacy_reasons": ["reason 1", "reason 2"]}` + "\n")
	builder.WriteString("- Do NOT wrap the JSON in ``` or ```json. Do NOT add any commentary before or after the JSON.\n")
	builder.WriteString("Requirements for commit_message:\n")
	builder.WriteString("- First line: short summary (max ~72 characters).\n")
	builder.WriteString("- Optional body: one or more lines after a blank line, explaining rationale and key changes.\n")
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
