# resume

`resume` is a Go CLI POC that converts sessions between Claude and Codex native stores.

## Scope (POC)

- Directions supported:
  - `claude -> codex`
  - `codex -> claude`
- Interactive TUI picker (search + arrow-key navigation) when `--id` is omitted
- Writes to native Codex stores (`sessions/`, `state_*.sqlite`, `session_index.jsonl`)
- No compaction in this POC

## Requirements

- Go 1.24+

## Usage

```bash
resume --from claude --to codex --id <claude_session_id>
resume --from codex --to claude --id <codex_thread_id>
resume --from codex --to claude
```

## Install

- Homebrew (when tap is configured):
  - `brew install <tap>/resume`
- Direct install script:
  - `curl -fsSL https://raw.githubusercontent.com/mithileshchellappan/resume/main/install.sh | bash`

Optional flags:

- `--claude-home` (default `~/.claude`)
- `--codex-home` (default `~/.codex`)
- `--id` (optional; if omitted, interactive picker is used)
- `--interactive` (optional compatibility flag to force picker)
- `--source-folder` (folder filter for interactive picker; defaults to current directory)
- `--cwd`
- `--title`
- `--dry-run`

When using the interactive picker in a real terminal, use arrow keys (`↑`/`↓`) or `j`/`k`, then press `Enter` to choose.
After interactive selection and successful migration (non-`--dry-run`), `resume` automatically launches the target tool resume command:
- `codex resume <thread_id>` for `claude -> codex`
- `claude --resume <session_id>` for `codex -> claude`

## Exit Codes

- `0` success
- `2` usage/validation
- `3` conversion/schema errors
- `4` native write/index errors

## Dev

```bash
make fmt
make vet
make test
make build
```

Run with:

```bash
./bin/resume --help
```

## CI and Release

- CI workflow: `.github/workflows/tests.yml`
  - runs `gofmt` check, `go vet`, tests, and build smoke checks on PRs and pushes.
- Release workflow: `.github/workflows/release.yml`
  - triggers on semver tags (`vX.Y.Z` or `X.Y.Z`)
  - builds cross-platform binaries
  - generates SHA256 checksums
  - publishes GitHub Release assets
  - optionally updates Homebrew tap if configured

## Homebrew Release Configuration (Optional)

Set these in GitHub repository settings to auto-update your tap on release:

- Secret: `HOMEBREW_TAP_TOKEN`
- Variable: `HOMEBREW_TAP_REPO` (example: `mithileshchellappan/homebrew-tap`)
- Variable: `HOMEBREW_FORMULA_NAME` (default: `resume`)

## OSS Project Files

- [LICENSE](./LICENSE)
- [CONTRIBUTING.md](./CONTRIBUTING.md)
- [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md)
- [SECURITY.md](./SECURITY.md)
- Issue and PR templates under `.github/`
