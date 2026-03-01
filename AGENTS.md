# AGENTS.md

`resume` is a zero-dependency Go CLI for native session migration between AI coding tools.
Current POC scope is:

- `claude -> codex`
- `codex -> claude`

The project goal is deterministic, inspectable, native-session conversion with strict error handling and no silent data loss.

## Core Principles

- **Strict flags, no prompts**: required flags must hard-fail with usage exit code (`2`).
- **Native-first writes**: write real target session stores, not summaries.
- **Deterministic conversion**: same input should produce structurally equivalent output each run (IDs may differ when regeneration is required).
- **Safety over cleverness**: preserve order and pairing semantics; do not invent model behavior.
- **No metadata leakage**: do not migrate tool-runner scaffolding/system envelopes as chat content.

## Command Discovery

Before changing CLI behavior, confirm actual flags/help text from the binary:

```bash
resume --help
resume --from claude --to codex --help
```

Never assume interfaces from memory. Validate against current code.

If `resume` is not installed locally during development, use the built binary (`./bin/resume`) rather than `go run` for behavior checks.

## Repository Topology

- `cmd/resume/main.go`: CLI entrypoint and exit code handling
- `internal/cli`: option parsing + validation
- `internal/app`: orchestration pipeline (`Run`)
- `internal/session`: shared IR types/interfaces
- `internal/claude`: Claude native loader/writer
- `internal/codex`: Codex native loader/writer
- `internal/converter`: IR-to-target conversion logic
- `testdata/`: fixtures/golden inputs as coverage grows

## Build and Test

Use these project commands:

```bash
make fmt
make test
make build
```

Direct equivalents:

```bash
go fmt ./...
go test ./...
go build -o bin/resume ./cmd/resume
```

Always run `make test` before committing.

## First-Party Invocation Policy

- User-facing docs/examples must use `resume ...` commands.
- Black-box validation must use installed/built binary (`resume` or `./bin/resume`), not `go run`.
- `go run` is acceptable only for local debugging, never as canonical usage in docs/handoffs.

## Testing Discipline (Non-Negotiable)

- Use TDD for behavior changes (flags, parsing, mapping, serialization, write semantics).
- Start with a failing test, then implement the smallest passing change.
- Keep tests close to behavior:
  - CLI validation in `internal/cli/*_test.go`
  - pipeline behavior in `internal/app/run_test.go`
  - format and native I/O in `internal/claude/*_test.go` and `internal/codex/*_test.go`
- Test both success and failure paths, including exit code mapping.
- For JSONL/SQLite behavior, assert parsed fields, not only string contains.

## Conversion Invariants

Any migration change must preserve these invariants unless explicitly revised:

- Ordered event stream is preserved.
- Tool call/result linkage is preserved.
- Missing tool results get synthetic output (`[no output recorded]`).
- Rewritten target call IDs are unique.
- Direction-specific normalization is explicit (example: shell/Bash mapping).
- Non-conversation scaffolding is filtered when required by policy.

For `codex -> claude`, never migrate system scaffolding as user/assistant chat:

- permissions/collaboration envelopes
- environment-context wrappers
- AGENTS.md injection blocks
- subagent notification wrappers
- local-command caveat/stdout/stderr wrapper tags

## Native Store Safety

- Prefer atomic file writes (temp file + rename).
- Use DB transactions for thread/index mutations.
- Do not partially commit multi-step writes.
- On write failure, return typed errors mapped to exit code `4`.

## Debugging and Fix Workflow

- Reproduce first using a fixture or real session ID.
- Change one logical thing at a time.
- Re-run focused tests, then full suite.
- Validate with a real CLI invocation after tests:
  - `resume --from ... --to ... --id ...`
  - fallback in dev shells: `./bin/resume --from ... --to ... --id ...`
- Record what was validated (IDs, output paths, and expected artifacts).

## Release and Distribution Mindset

- Treat every CLI contract change as a release-facing change.
- Keep CLI UX stable and explicit for package users (Homebrew and other installers).
- Do not document ephemeral dev-only invocation patterns as public usage.
- Prefer semver-compatible evolution: deprecate before removing flags or behaviors.

## Definition of Done

A change is done only when all are true:

- New/changed behavior has targeted tests.
- `make fmt` and `make test` pass.
- CLI behavior and exit code semantics are validated.
- No unrelated instruction/system noise is migrated as conversation.
- Handoff includes commands run and concrete verification points.

## Commit and Review Guardrails

- One logical change per commit.
- Do not mix refactors with behavior changes unless unavoidable.
- Preserve existing public CLI contract unless explicitly changed.
- If contract changes, update tests and `README.md` in the same change.
