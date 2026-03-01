# Contributing

Thanks for contributing to `resume`.

## Development Setup

Prerequisites:

- Go 1.24+

Clone and build:

```bash
make build
./bin/resume --help
```

## Local Validation

Before opening a PR:

```bash
make fmt
make test
go vet ./...
```

## Coding Rules

- Keep CLI behavior explicit and deterministic.
- Add tests for all behavior changes (flags, mapping, conversion, native writes).
- Avoid mixing unrelated refactors and behavior changes in one PR.
- Preserve cross-tool conversion invariants (event ordering, tool call/result linkage).

## Pull Requests

1. Create a branch from `main` (or `master` if that is default in this repo).
2. Keep the PR focused and small enough to review.
3. Fill the PR template and include validation commands/results.
4. Ensure CI is green before merge.

## Releases

Releases are tag-driven through GitHub Actions.

- Push a semantic version tag (`vX.Y.Z` or `X.Y.Z`).
- CI builds cross-platform binaries and publishes GitHub Release assets.
- Checksums are attached automatically.
- Homebrew tap update is attempted only when required secrets/vars are configured.

Example:

```bash
git tag v0.1.0
git push origin v0.1.0
```
