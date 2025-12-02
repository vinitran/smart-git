## SmartGit

SmartGit is a Go-based companion CLI that augments your Git workflow with Gemini 2.5 Flash code reviews delivered straight from the terminal.

### Features (MVP)

- `smartgit review`: send staged changes or the latest commit diff to Gemini for structured feedback (overview, risks, refactors, tests, commit message hints).
- Supports `--last-commit`, `--short`, `--raw`, `--language`, `--max-tokens`, and `--verbose` flags.
- Pure shell execution of Git commands—no direct GitHub/GitLab API integrations yet.

### Getting Started

1. **Install**: `go install github.com/vinhtran/git-smart/cmd/smartgit@latest`
2. **Configure Gemini API key**:
   - Recommended: `export GEMINI_API_KEY="your-key"` (the CLI will use it directly).
   - If the env var is not set, SmartGit will prompt for the key on first run and store it at `~/.config/smartgit/config.json` so you are not asked again.
   - Optional model override: `export GEMINI_MODEL="gemini-2.5-flash"`
3. **Use the CLI**:
   ```bash
   # Review staged changes
   smartgit review

   # Review the latest commit with verbose logging
   smartgit review --last-commit --verbose

   # Concise Vietnamese response
   smartgit review --short --language=vi
   ```

### How It Works

1. SmartGit shells out to Git (`git diff --cached` or `git show HEAD`) to capture context.
2. The diff plus repo metadata (path, branch, remote) is trimmed and sent to Gemini 2.5 Flash through the official REST endpoint.
3. The structured response is printed to your terminal.

### Development

```bash
go test ./...
go run ./cmd/smartgit review --short
```

### Roadmap

- Auto commit/push/merge workflows (`smartgit push merge`)
- AI-assisted commit message generation
- Provider-agnostic AI adapters with local LLM support

