# Agent Guide

`lincrawl` is a local-first Linear archive CLI. It syncs Linear (teams,
projects, issues, comments, labels, states) into a local SQLite archive,
searches it offline, and writes encrypted `*.jsonl.zst.age` snapshots
that tenant-controlled subscriber stores can verify and re-import.
Tenant data, credentials, generated artifacts, snapshots, logs, reports,
and transcript-like content must not be committed here.

## Fast start

```bash
./scripts/smoke
./scripts/verify
```

Use `LINCRAWL_HOME=<scratch-dir>` for local CLI runs that should not touch the
default user state. The smoke script already does this.

## Checks

- `./scripts/smoke` exercises the offline CLI with synthetic Linear fixtures.
- `./scripts/verify` runs module tidy check, `go vet`, `go test`,
  `go test -race`, `./scripts/smoke`, and a whitespace check.

## Configuration

Auth comes from environment variables loaded out of `.env.local` (git-ignored)
or the surrounding shell. `LINEAR_API_KEY` is the only required key, and only
for live calls. See `.env.example` for the full set.

`config.LoadRuntime()` reads `.env.local` if present, then resolves paths from
`LINCRAWL_HOME` (override) or the XDG defaults.

## Repo shape

```text
cmd/lincrawl/          CLI entrypoint
internal/cli/          command parsing, JSON output, errors, field masks,
                       input validation, output-path sandbox
internal/config/       env + local config loading with redaction
internal/buildinfo/    version/commit/date strings (overridden by ldflags)
internal/linear/       entity types, fixture loader, GraphQL client with
                       typed errors + Retry-After + injectable Sleep/Now
internal/store/        crawlkit-backed SQLite + FTS5 + ingest + export +
                       content_hash short-circuit + raw_blobs table
internal/syncer/       fixture / entity / exact / tail / streaming sync
                       with overlap window + wall-clock budget +
                       stall guards
internal/lock/         file lock (<db>.lock O_EXCL) for sync runs
internal/guard/        working-tree scan (.gitignore-aware) for tenant
                       leaks, plaintext archives, secrets, op:// refs
testdata/synthetic/    deterministic fixture issues/comments/labels
skills/lincrawl/       agent skill (YAML frontmatter + workflows)
```

Built on [`github.com/openclaw/crawlkit`](https://github.com/openclaw/crawlkit)
for the SQLite open/PRAGMA/schema-version/read-only primitives.

`CLAUDE.md` is a symlink to this file; keep one authored agent guide.

## Boundary

- Read-only against Linear. No write-back (issues, comments, labels, states).
- No undocumented endpoints or rate-limit bypass.
- No real Linear data, workspace IDs, issue bodies, comments, labels, or
  exports in this repo.
- Tenant-specific operating state (live archive, encrypted snapshots,
  manifests, runbooks) belongs in a tenant-controlled store repository,
  not here.

## Local secrets

Locally, prefer your 1Password CLI integration (`op whoami --account=<your-account>`).
This repo's verify works without secrets; pull credentials only when running
live sync.

## Agent DX surfaces

- `lincrawl describe --json` returns the full machine-readable surface:
  `schema_version: "lincrawl.cli.v1"`, per-command args (name, type,
  required), per-flag (name, type, required, default, help),
  `mutually_exclusive` flag groups, `mutates` boolean for side-effect
  classification, `examples[]` and `notes[]` per command, the exit-code
  table (`ok` 0, `internal` 1, `usage` 2, `not_found` 3, `validation`
  4, `config` 5), and the field-mask vocabulary for `show`, `search`,
  `status`.
- `lincrawl describe <command> --json` returns just one command's
  schema for cheap agent introspection.
- Every command defaults to `--json`. NDJSON streaming is available on
  `search` (per row) and on `sync --updated-since` (per ingested issue).
- Errors are JSON on stderr: `{"code","exit","message"}`. Use the `exit`
  field, not the raw process exit status, to classify failures.
- `lincrawl guard --json` scans the working tree before commit; honors
  `.gitignore` in git checkouts.
- `--fields a,b,c` works on `show`, `search`, and `status`. Unknown fields
  produce a `validation` error listing the known fields. Always pass
  `--fields` from your skill prompt to cap token spend.
- `--dry-run` works on every `sync` mode and reports the plan without
  writing.
- Inputs are hardened: issue refs must match a UUID or `TEAM-N` shape, queries
  reject control chars and are length-capped, fixture paths reject
  percent-encoded segments. `--out` is sandboxed to the working directory.
  Read-only against Linear by construction.
- Agent skill: [skills/lincrawl/SKILL.md](skills/lincrawl/SKILL.md) is the
  structured guide; read it before driving lincrawl in a chain.
- Raw passthrough: `lincrawl query --graphql ... --vars '{...}'` accepts any
  Linear GraphQL document so agents are not boxed in by built-in syncs.
  `--graphql-file` reads from a sandboxed CWD path.
- Stdin ingest: `lincrawl sync --stdin` reads either a single `Snapshot`
  JSON document or an NDJSON stream of `{"kind","item"}` envelopes (the
  shape `export` emits), so `export | sync --stdin` round-trips.
- Export: `lincrawl export --out <path>` writes a canonical NDJSON dump of
  the entire local archive (teams, states, users, labels, projects, issues,
  including embedded labels and comments per issue). `--out` is sandboxed
  to the working directory with symlink resolution; `--out -` writes to
  stdout.

## Docs

- [Agent skill](skills/lincrawl/SKILL.md): structured per-workflow guide
- [Architecture](docs/architecture.md): CLI, store, sync shape
- [Tenant data boundary](docs/tenant-data-boundary.md): what stays out
- [Roadmap](docs/roadmap.md): current surface and next work
- [Bootstrap plan](docs/plans/bootstrap-claude-prompt.md): original prompt
