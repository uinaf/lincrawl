# Distribution

Status: active

`lincrawl` ships as a Go CLI. `main` is verify-gated; release is automatic.

## Verify → release contract

`.github/workflows/ci.yml` runs two jobs:

- `verify`: PR + push to main. `./scripts/verify` (tidy diff, vet, tests,
  race tests, offline smoke, guard, whitespace check, release-check).
  `permissions: { contents: read }`. Concurrency cancels in-progress.
- `release`: only on `push` to `main` after `verify` succeeds. Permissions
  `contents: write`. Non-cancellable concurrency `release-${repo}-main`
  so two pushes serialize.

Both jobs respect `[skip ci]` in the commit subject.

## Semantic release

`.releaserc.json` drives `@semantic-release/commit-analyzer` with the
Conventional Commits preset. Every type (`feat`, `fix`, `perf`, `revert`,
`build`, `ci`, `docs`, `test`, `refactor`, `chore`) maps to a `patch`
release during the `0.0.x` bootstrap; bump the rules to `minor`/`major`
when the contract stabilizes.

Release notes come from
`@semantic-release/release-notes-generator`. No committed `CHANGELOG.md`;
the GitHub release page is the canonical changelog.

## Build pipeline

`.goreleaser.yaml` builds `lincrawl` for `darwin/linux/windows` ×
`amd64/arm64` with `CGO_ENABLED=0`, embeds version/commit/date into
`internal/buildinfo`, and uploads tarballs (zip on Windows) plus a
checksums file. `release.mode: append` and
`release.replace_existing_artifacts: false` fail closed so a re-run
never silently overwrites a published artifact.

`./scripts/release-check` runs `goreleaser check` locally and also
greps `.goreleaser.yaml` for `replace_existing_artifacts: true`, failing
if anything ever flips it.

## Workflow hardening

- `permissions: {}` at every workflow root; jobs grant the minimum
  scope they need (`contents: read` or `contents: write`).
- All third-party actions are pinned to commit SHAs with version
  comments.
- `actions/checkout` uses `persist-credentials: false` so the workflow
  token does not linger on disk.
- `actions/setup-go` reads the version from `go.mod` so the toolchain
  matches the repo at every checkout.

## Secret-scanning + CodeQL

- `.github/workflows/secret-scan.yml`: TruffleHog OSS pinned to
  `v3.95.3`. Runs on every branch push and every PR to `main`.
  `--only-verified` so the gate fails only on real credentials.
- `.github/workflows/codeql.yml`: GitHub-hosted SAST. Push to `main`,
  every PR, plus a weekly cron.

## Local hooks

Activate with `git config core.hooksPath .git-hooks`. Per-clone, opt-in,
not destructive.

- `commit-msg`: enforces the Conventional Commits regex.
- `pre-push`: `go test ./... && go vet ./... && ./scripts/smoke &&
  lincrawl guard --json`.

## Tenant boundary on release

The release pipeline never reads `LINEAR_API_KEY`. Artifacts are pure
build output. Tenant data belongs in a tenant-controlled store
repository, not here.
