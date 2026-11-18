# mktauto: instructions for AI coding assistants

The terminal tool of MKTAuto (Go). Read by Codex and other agents; `CLAUDE.md` imports it for Claude Code.

## Layout
| Path | What |
|---|---|
| `cmd/mktauto` | `main` (version stamped with `-ldflags "-X main.version=…"`) |
| `internal/app` | commands: `app.go` (dispatch, usage, exit codes), `post.go` (wizard + flags), `wizard.go` (step engine: Esc goes back), `batch.go`, `watch.go`, `manage.go`, `dashboard.go`, `insights.go`, `mcp.go` (MCP server), `completion.go`, `preset.go`, `ai.go`, `alttext.go` |
| `internal/api` | HTTP client and types: must match the MKTAuto backend's DTOs |
| `internal/compose` | the post plan and rules mirrored from the backend (`PlatformRules`, `CaptionComposer`) |
| `internal/media`, `internal/upload`, `internal/ui`, `internal/auth`, `internal/history`, `internal/update` | files, uploads, screens, sign-in, local history, self-update |

## Rules
- The server is the authority; local checks only warn early. Keep `internal/api` and `internal/compose` in step with
  the backend (the main repository's `AGENTS.md` and its ADRs).
- A new server endpoint the tool needs must be allowed in the backend's `CliAuthFilter` (permission and project scope).
- Never put secrets in the repository or the binary; tokens live in the OS keychain (or the config folder file store).
- Every `mktauto post` flag must be in `postHelp` (a test checks it) and in tab completion.
- Interactive questions go through `asker` (`p.ask.form`, `p.ask.screen`) so Esc goes back; scripts use flags and must
  never block on a prompt (`!e.Interactive`).
- `--json` data goes to `e.Data` (not `os.Stdout` directly); `mktauto mcp` must print nothing but JSON-RPC on stdout.

## Commands
`go vet ./... && MKTAUTO_TOKEN_STORE=file MKTAUTO_NO_UPDATE_CHECK=1 go test -race ./...` · tests use a fake server
(`internal/app/app_test.go`); `MKTAUTO_FAKE_SERVE=/tmp/fake go test ./internal/app -run TestServeFake -timeout 0` serves it
for trying the wizard by hand. Releases: tag `vX.Y.Z` (see the main repository's `.claude/skills/release-cli`).
