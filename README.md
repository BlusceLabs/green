<p align="center">
  <img src="docs/assets/green-logo.png" alt="green" width="385">
</p>

<p align="center"><strong>A terminal coding agent you own.</strong></p>

<p align="center">
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
  <img alt="25+ providers" src="https://img.shields.io/badge/providers-25+-34E2EA">
  <br>
  <strong>English</strong> | <a href="README_ZH.md">中文</a>
</p>

green is an AI coding agent for your local terminal. It can inspect a repository,
edit files, run commands, use browser/terminal helpers, and keep durable local
sessions while you choose the model and the permission level.

```bash
green
green exec "fix the failing test in ./pkg"
green exec --output-format stream-json < turns.jsonl
```

## Why green

- **Use the model you want.** Bring OpenAI, Anthropic, Gemini, Groq, OpenRouter,
  DeepSeek, Mistral, xAI, Qwen, Kimi, GitHub Models, Ollama, LM Studio, or any
  OpenAI-/Anthropic-compatible endpoint.
- **Stay in control.** File writes, shell commands, network access, and
  out-of-workspace writes go through green's permission and sandbox policy.
- **Works in the terminal.** The TUI has model/provider pickers, image input,
  slash commands, live plan/tool rendering, scrollback, themes, and resume/fork
  support.
- **Works without the TUI.** `green exec` is scriptable, supports text/JSON/
  stream-JSON I/O, isolated worktrees, spec-first runs, and meaningful exit
  codes for CI.
- **Keeps context local.** Sessions are stored on disk, searchable, resumable,
  and never uploaded as telemetry by green.
- **Extensible when you need it.** Use MCP servers, skills, plugins, hooks, and
  specialist subagents from the same CLI.

## Install

### npm

```bash
npm install -g @bluscelabs/green
green
```

The npm package installs a small wrapper plus the matching green binary for your
platform from GitHub Releases. It supports Linux, macOS, and Windows on x64 and
arm64.

### Bun

Bun does not run dependency lifecycle scripts by default, so the `postinstall`
that fetches the green binary is skipped and the first run fails with
`No native binary found next to the npm wrapper`.

The simplest fix is to trust the package after installing, which runs the
blocked postinstall. This works for project and global installs:

```bash
# project install
bun add @bluscelabs/green
bun pm trust @bluscelabs/green

# global install
bun add -g @bluscelabs/green
bun pm -g trust @bluscelabs/green
```

Alternatives: allow the postinstall up front by adding
`"trustedDependencies": ["@bluscelabs/green"]` to your project's package.json
before `bun add`, or run the installer manually
(`node node_modules/@bluscelabs/green/scripts/postinstall.mjs`) on Bun versions
that do not have `bun pm trust`.

### Install scripts

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/BlusceLabs/green/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/BlusceLabs/green/main/scripts/install.ps1 | iex
```

### From source

Source builds require Go 1.25+.

```bash
git clone https://github.com/BlusceLabs/green.git
cd green
go run ./cmd/green
```

Release installers and the npm wrapper require published GitHub Release assets.
If you are testing before the first public release, build from source:

```bash
go build -o green ./cmd/green
```

On Linux, build the sandbox helper too if you want native sandboxing:

```bash
go build -o green-linux-sandbox ./cmd/green-linux-sandbox
go build -o green-seccomp ./cmd/green-seccomp   # optional compatibility wrapper
```

Put `green` and `green-linux-sandbox` in the same directory on `PATH`
(`~/.local/bin` is a good default). macOS does not need an extra helper binary.
Windows source builds can use the main `green.exe` as their sandbox helper; release
archives still ship standalone Windows helper executables.

More install details: [docs/INSTALL.md](docs/INSTALL.md).

## First Run

Start the TUI:

```bash
green
```

The setup wizard helps you pick a provider and model. You can also configure
providers from the command line:

```bash
green setup
green providers list
green models list
green doctor
```

For API providers, set the matching environment variable before setup or enter
the key in the wizard:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export LONGCAT_API_KEY=...
```

To configure Meituan LongCat (LongCat-2.0) directly, run:

```bash
green providers setup longcat --set-active
```

For local models, run Ollama or LM Studio and then use `green setup` or
`green providers detect`.

## Daily Use

### Interactive TUI

```bash
green
```

Useful controls:

| Control | Action |
|---|---|
| `Enter` | send the prompt |
| `/` | open slash-command suggestions |
| `Shift+Tab` | cycle permission mode |
| `Ctrl+B` | show/hide the sidebar |
| `Ctrl+C` | cancel or exit |

Common slash commands:

| Command | Purpose |
|---|---|
| `/model`, `/provider` | switch the active model/provider |
| `/spec`, `/plan` | draft and review a plan before building |
| `/image` | attach an image for vision-capable models |
| `/resume`, `/rewind` | continue or roll back local sessions |
| `/loop` | repeat a prompt or custom `/command` on an interval (`/loop 5m /babysit-prs`) or self-paced |
| `/compact`, `/context` | manage context usage |
| `/permissions`, `/tools` | inspect available tools and policy |
| `/add-dir` | allow an extra write directory for this session |
| `/theme`, `/doctor`, `/config` | adjust appearance and inspect setup |

### Headless `exec`

```bash
green exec "explain internal/agent/loop.go"
green exec --model claude-sonnet-4.5 "refactor the config loader"
green exec --use-spec "add rate limiting to the API client"
green exec --worktree "try the migration in an isolated worktree"
green exec --resume
green exec --fork <session-id> "try the other approach"
```

Programmatic use:

```bash
green exec --input-format stream-json --output-format stream-json < turns.jsonl
```

The stream-JSON contract is documented in
[docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md).

## Safety Model

green is designed to make side effects visible.

- Workspace reads are allowed by default.
- File writes are limited to the workspace unless you grant another directory.
- Shell commands, network access, destructive commands, and elevated actions are
  permission-gated.
- `--add-dir <path>` and `/add-dir <path>` grant additional write roots without
  giving the agent the whole filesystem.
- Unsafe/autonomous modes are explicit opt-ins.
- Secrets are redacted from tool output and logs where green controls the surface.

Example:

```bash
green --add-dir ../docs-site
green exec --add-dir ../shared "update both repos"
```

Sandbox behavior can be inspected with:

```bash
green sandbox policy
green sandbox grants list
```

## Web And Local Control

green includes local file/search/edit/shell tools, `web_fetch` for public URLs,
and MCP support for additional tools.

For local dev servers, use shell commands such as `curl` through `exec_command`
so the normal sandbox and permission policy applies. Long-running commands stay
attached to a background terminal session and can be listed or stopped from the
TUI.

The npm package also includes browser and terminal helper packages used by local
browser/terminal tools. Source builds can use the same helpers when they are on
`PATH` or configured in green's local-control settings.

## Common Commands

```text
green                  interactive TUI
green exec             one-shot or scripted agent run
green setup            first-run provider setup
green auth             OAuth/login helpers for supported providers
green models           model registry and capabilities
green providers        provider profiles and detection
green doctor           setup, key, and connectivity checks
green context          context-budget report
green repo-map         deterministic repository map
green repo-info        local repository summary
green search | find    search local session history
green sessions         inspect, resume, fork, and rewind sessions
green spec             manage spec-mode drafts
green specialist       manage specialist subagents
green skills           manage markdown instruction skills
green plugins          manage plugins
green hooks            manage lifecycle hooks
green mcp              manage MCP servers and tools
green serve --mcp      expose green tools over MCP stdio
green sandbox          inspect sandbox policy and grants
green worktrees        prepare isolated git worktrees
green verify           detect and run local verification checks
green changes          inspect and commit local git changes
green usage            token usage and estimated cost
green cron             scheduled agent jobs
green learn            self-improving learning loop: memory, profile, reflect, nudge, skills
green contextfile      durable project/user context files (Hermes-style)
green trajectory       capture a session for training & eval, with compression
green gateway          reach the agent from chat platforms (local, Telegram, Discord, Slack, WhatsApp via whatsmeow, Email/IMAP+SMTP, Signal)
green recall           search past sessions and synthesize (Hermes recall)
green lsp              Language Server Protocol from the CLI (diagnostics, goto, find)
green update           check for newer releases
```

## Extending green

### Project and personal instructions

green appends project-specific guidance to the system prompt from the first
`AGENTS.md`, `green.md`, or `.green/AGENTS.md` file found in each directory from
the git root down to your current working directory (checked in that order
per directory). Files are injected general-to-specific, capped at 8 KiB per
file and 32 KiB total.

A personal `green.md` under `config.UserConfigDir()/green/green.md`
(`$XDG_CONFIG_HOME/green/green.md` or `~/.config/green/green.md` on Linux/macOS,
`%AppData%\Roaming\green\green.md` on Windows) applies across every workspace, ahead of any project guidelines.

### Plugins

Plugins are discovered from `~/.config/green/plugins/<name>/plugin.json` (user
scope — `$XDG_CONFIG_HOME` or `~/.config` on every OS, independent of the
`config.UserConfigDir()` path used above) and `<cwd>/.green/plugins/<name>/plugin.json`
(project scope — resolved from the current working directory, not the repo
root), and managed with `green plugins`. A manifest can declare:

- `tools` — custom tools (`command`, `args`, `inputSchema`, and a
  `permission` of `prompt` or `deny`; `allow` is honored only when manifest tool
  auto-approval is enabled)
- `hooks` — commands run on `beforeTool`, `afterTool`, `sessionStart`, or
  `sessionEnd`
- `prompts` and `skills` — additional prompt/skill files

MCP servers (`green mcp`) and standalone markdown skills (`green skills`) use
the same extension points and can also be wired up outside of a plugin
manifest.

## Appearance And Accessibility

| Control | Effect |
|---|---|
| `NO_COLOR=<anything>` | disables color output |
| `green_THEME=<name>` | selects the startup theme (`auto`, `dark`, `light`, or a color theme like `dracula`, `nord`, `gruvbox`, `tokyo-night`, `catppuccin`, `one-dark`, `solarized-dark`, `rose-pine`, `everforest`, `solarized-light`) |
| `--theme <name>` | selects the TUI theme from the CLI (same names) |
| `/theme` | opens the theme picker inside the TUI (live preview; `/theme <name>` switches directly) |
| `green_NO_FADE=1` | disables streaming fade animation |

Meaning does not rely on color alone; diffs, permissions, and statuses also use
text or glyph markers.

## Development

```bash
go test ./...
go run ./cmd/green-release build
go run ./cmd/green-release smoke
go run ./cmd/green-perf-bench
```

Cross-compile examples:

```bash
go run ./cmd/green-release build --goos linux --goarch amd64
go run ./cmd/green-release build --goos windows --goarch amd64 --output dist/green.exe
```

## Documentation

- [Install](docs/INSTALL.md)
- [Update flow](docs/UPDATE.md)
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md)
- [Specialists](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [Benchmarks](docs/BENCHMARK.md)
- [Performance](docs/PERFORMANCE.md)
- [Agent evals](docs/AGENT_EVALS.md)

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), run the
relevant tests, and open a focused pull request.

Security reports should follow [SECURITY.md](SECURITY.md).

## License

green is released under the [MIT License](LICENSE).
