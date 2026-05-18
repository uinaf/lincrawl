# Contributing

`lincrawl` is the reusable Linear crawler core. Tenant credentials, real
Linear data, generated artifacts, and any transcript-derived examples stay
outside this repository.

## Local setup

```bash
cp .env.example .env.local   # optional, only needed for live calls
$EDITOR .env.local
chmod 600 .env.local

./scripts/smoke
./scripts/verify
```

`LINCRAWL_HOME=<scratch-dir>` keeps runtime state out of the default user
location. `./scripts/smoke` already does this.

## Validation

`./scripts/verify` is the local gate. It runs:

1. `go mod tidy` (verifies `go.mod`/`go.sum` are clean)
2. `go vet ./...`
3. `go test ./...`
4. `go test -race ./...`
5. `./scripts/smoke`
6. `git diff --check` (trailing whitespace)

CI mirrors the same gate.

## Commit style

Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat`,
`fix`, `refactor`, `docs`, `chore`, `test`). Mark breaking changes with `!`
or a `BREAKING CHANGE:` footer.

## Tenant data boundary

- Read-only against Linear. Never add write-back operations.
- Do not commit real workspace IDs, issue bodies, comments, labels, or
  attachments. Synthetic fixtures only, under `testdata/synthetic/`.
- Real archive output, encrypted snapshots, and runtime state belong in a
  tenant-controlled store repository, not here.

## Reporting issues

Open issues against the upstream repository. For vulnerabilities, see
[Security](SECURITY.md).
