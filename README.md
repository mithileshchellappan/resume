# resume

`resume` is a Go CLI POC that converts a Claude session into a native Codex session so it appears in Codex resume flows.

## Scope (POC)

- Direction supported: `claude -> codex`
- No TUI; strict flag-based CLI
- Writes to native Codex stores (`sessions/`, `state_*.sqlite`, `session_index.jsonl`)
- No compaction in this POC

## Requirements

- Go 1.24+

## Usage

```bash
resume --from claude --to codex --id <claude_session_id>
```

Optional flags:

- `--claude-home` (default `~/.claude`)
- `--codex-home` (default `~/.codex`)
- `--cwd`
- `--title`
- `--dry-run`

## Exit Codes

- `0` success
- `2` usage/validation
- `3` conversion/schema errors
- `4` native write/index errors

## Dev

```bash
make fmt
make test
make build
```
