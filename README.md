## sg (smart-git)

`sg` is a small Go-based CLI that helps you work faster with Git by adding AI (Gemini) assistance directly in your terminal.

### Installation

#### Option 1: Using `install.sh` (recommended for macOS/Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/vinitran/smart-git/main/install.sh | bash
```

The script will:
- Detect your OS and architecture (Intel vs Apple Silicon).
- Read the latest version from GitHub.
- Download the appropriate prebuilt binary and install it to `~/.local/bin/sg`.
- Append `~/.local/bin` to your `PATH` in `~/.zshrc` (if it is not already there).

After installation, open a new terminal (or run `source ~/.zshrc`) and verify:

```bash
sg version
```

#### Option 2: Using `go install` (requires Go)

```bash
go install github.com/vinitran/smart-git/cmd/smartgit@latest
```

Ensure `~/go/bin` is in your `PATH` (if it is not already):

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

After that, you can create an alias called `sg` if you prefer a shorter command name.

### Gemini API key configuration

`sg` uses Gemini to review code and generate commit messages.

- Recommended: configure via environment variables:

  ```bash
  export GEMINI_API_KEY="your-key"
  # (optional) override model if you want
  export GEMINI_MODEL="gemini-2.0-flash"
  ```

- If `GEMINI_API_KEY` is not set, the first run will prompt for the key and store it at `~/.config/smartgit/config.json` so that you are not asked again.

### Core commands

#### 1. `sg cm` – AI commit message + commit

Create a commit with an AI-suggested message, plus a simple privacy/sensitivity check.

```bash
sg cm
```

Behavior:
- Analyzes all changes (staged + unstaged) to generate a Conventional Commits style message.
- Prints the proposed commit message for you to review.
- Warns if potential secrets or sensitive data are detected and asks for confirmation before committing.
- If you are on a protected branch (`main`, `master`, `develop`, `dev`), it suggests creating a feature branch and committing there instead.

Aliases:
- `sg commit`

#### 2. `sg p` – Push the current branch

```bash
sg p
```

Behavior:
- If the current branch already has an upstream: runs `git push origin <branch>`.
- If there is no upstream yet: runs `git push -u origin <branch>`.
- If you are on a protected branch (`main`, `master`, `develop`, `dev`), the CLI suggests creating a new branch from the latest commit and pushing that branch instead of pushing directly to the protected branch.

Aliases:
- `sg push`

#### 3. `sg sw <branch>` – Switch branch + pull --rebase

```bash
sg sw main
```

Equivalent to:

```bash
git checkout main
git pull --rebase origin main
```

This is handy for quickly switching to a base branch (like `main`) and rebasing onto the latest remote state.

Aliases:
- `sg switch <branch>`

### Code review with AI

You can still ask Gemini to review your diffs:

```bash
sg rv                    # short review of staged changes
sg review                # same as rv
sg review --last-commit  # review the latest commit
gsg review --short --language=vi  # concise review in Vietnamese
```

Options:
- `--last-commit`: review the last commit instead of staged changes.
- `--short`: focus on the most important feedback.
- `--raw`: print the raw Gemini response.
- `--language`: `en` or `vi`.
- `--max-tokens`: control Gemini output length.
- `--verbose` / `--debug`: enable more detailed logging.

### Version & auto-update

```bash
sg version
```

- Prints the current CLI version.
- Checks the latest version from the `VERSION` file hosted on GitHub.
- If a newer version exists:
  - You will see a warning when `sg` starts: a newer version is available.
  - `sg version` can optionally perform a self-update if you configure `GIT_SMART_HOME` to point at your local clone of the repo.

### Development

```bash
go test ./...
go run ./cmd/smartgit version
```

The project follows idiomatic Go practices, uses `cobra` for CLI structure, and `slog` for structured logging.
