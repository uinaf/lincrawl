# Tenant data boundary

`lincrawl` is the reusable crawler core. Tenant data — real Linear workspaces,
issue bodies, comments, exports, snapshots, logs, or any transcript-derived
example — belongs in tenant-controlled stores, not in this repository.

## What must not be committed

- Real Linear API keys, OAuth client secrets, or refresh tokens
- Real workspace IDs, organization names, team keys other than the synthetic
  `LIN-N` examples, real user IDs, names, or emails
- Real issue titles, descriptions, comments, attachments, label sets, or
  cycle/initiative payloads
- Plaintext archive output (`*.jsonl`, `*.jsonl.zst`, `*.tar.zst`, `*.db`)
- Encrypted snapshots (`*.jsonl.zst.age`, `*.tar.zst.age`) — these belong in
  the tenant-controlled store repository
- Logs, screenshots, HAR captures, query traces, or any transcript-derived
  fixture

## What may be committed

- Synthetic fixtures under `testdata/synthetic/` whose IDs follow the
  `team-syn-*`, `user-syn-*`, `LIN-N` naming convention
- Generic policy docs, runbooks, and command examples that use placeholder
  identifiers (`<id>`, `<recipient>`, `<workspace>`)
- The reusable crawler source, tests, scripts, CI, and release tooling

## Local handling

- `.env.local` is git-ignored. Keep `LINEAR_API_KEY` there, never in shell
  history or committed files
- `LINCRAWL_HOME` overrides the default XDG data directory; smoke and verify
  scripts use scratch directories under `tmp/`
- Local SQLite archives, `*-wal`, `*-shm`, and any `tmp/` content are ignored
  by `.gitignore`

## Tenant-controlled store

The tenant-controlled tenant store for `lincrawl` lives in
[`<tenant-store>`](https://github.com/<tenant-store>).
That repository owns runbooks, manifest metadata, and any encrypted
snapshots; this repository owns the generic CLI shape.
