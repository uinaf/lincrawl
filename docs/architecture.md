# Architecture

Status: active

`lincrawl` is a local-first Linear archive CLI in the uinaf crawler family.
The reusable repo owns generic crawling, storage, search, export, and
agent-facing mechanics; tenant credentials, real Linear data, runtime state,
generated artifacts, and transcript-derived examples stay outside this repo.

## Current surface

```bash
lincrawl version --json
lincrawl describe --json
lincrawl doctor --offline --json
lincrawl status --json [--fields counts]

lincrawl sync --fixture testdata/synthetic --json
lincrawl sync --stdin --json < snapshot.jsonl
lincrawl sync --entities --json
lincrawl sync --updated-since 24h --max-issues 200 --json
lincrawl sync --resume --max-issues 1000 --json
lincrawl sync --issue LIN-42 --json
lincrawl sync --updated-since 24h --ndjson      # streams one JSON object per issue

lincrawl search "<query>" [--limit N] [--raw] [--ndjson] [--fields a,b,c] --json
lincrawl show <id-or-identifier> [--fields a,b,c] --json

lincrawl query --graphql 'query { viewer { id } }' [--vars '{...}'] --json
lincrawl query --graphql-file ./op.graphql --vars '{"id":"..."}' --json
lincrawl export --out ./snapshots/lincrawl.jsonl --json
lincrawl export --out -                          # NDJSON to stdout
```

Every command takes `--dry-run` where it would mutate. JSON is the default
output; errors are JSON envelopes on stderr (`{"code","exit","message"}`).
Exit codes: `0 ok`, `1 internal`, `2 usage`, `3 not_found`, `4 validation`,
`5 config`. `describe --json` lists every command's args, flags (with type,
default, required), mutually-exclusive flag groups, the exit-code table,
and the field-mask vocabulary for `show`, `search`, and `status`.

## Deferred

- Encrypted snapshot pipeline (`archive`, `publish`, `import`,
  `store verify`, `subscribe`) — would wrap `export` output in zstd + age
  and add manifest tracking for the tenant store
- Repository guard (`guard --json`) for committable plaintext / generated
  artifacts / leaked tenant data
- CI verify on PRs + `goreleaser` releases
- Cycles, initiatives, attachment metadata

## Package shape

```text
cmd/lincrawl/          CLI entrypoint (main is one line)
internal/cli/          Kong command tree, JSON output, errors, field masks,
                       input validation, output-path sandbox
internal/config/       env + .env.local loading with redacted presence flags
internal/buildinfo/    version/commit/date strings (overridden via -ldflags)
internal/linear/       Linear entity types, fixture loader, GraphQL client
                       (typed queries + paginate helper)
internal/store/        SQLite schema + FTS5 + idempotent ingest +
                       search/show/export + NDJSON-or-Snapshot ingest stream
internal/syncer/       Fixture, entity, exact, tail, and streaming sync
                       orchestration with cursor persistence
testdata/synthetic/    snapshot.json fixtures for deterministic tests
skills/lincrawl/       Agent skill (YAML frontmatter + workflows)
```

## Store

The local SQLite archive is the source of truth for what lincrawl has seen.
Files are created at `0600`, the parent directory at `0700`, so the archive
is not world-readable on shared machines. Schema:

- `teams`, `workflow_states`, `users`, `labels`, `projects`
- `issues` with foreign keys to `teams`, `projects`, `workflow_states`,
  `users` (assignee, creator). Missing references are stub-inserted on
  ingest so a sparse entity pull does not block issue ingest.
- `issue_labels` join table — purged and rebuilt per issue on every refresh
- `comments` — also purged and rebuilt per issue on refresh, so deleted
  upstream comments disappear locally
- `issue_fts` FTS5 mirror over `identifier`, `title`, `description`, and
  concatenated comment bodies, with `bm25` ordering and snippet markers
- `sync_state(scope, cursor, high_water_mark, updated_at)` — `issues.tail`
  scope persists the live tail-sync cursor for `sync --resume`

Search uses FTS5 with `bm25` ordering, snippet markers, and a configurable
limit. User queries are wrapped as FTS5 phrases by default (`--raw` opts
out). `show` resolves either a Linear UUID or the `TEAM-N` identifier form
case-insensitively.

Ingest is fully idempotent: every entity uses `INSERT … ON CONFLICT(id) DO
UPDATE`, FTS rows are rebuilt per issue inside the same transaction.

`ExportNDJSON` emits one `{"kind":"team|state|user|label|project|issue","item":{...}}`
line per record; `IngestStream` accepts either that NDJSON shape or a
single `Snapshot` JSON document, so `export | sync --stdin` round-trips
without loss.

## Sync

- **Fixture sync** loads `testdata/synthetic/snapshot.json` (or any
  `*.snapshot.json` file under a fixture directory). It validates
  referential integrity at parse time so fixture typos fail fast.
- **Stdin sync** (`sync --stdin`) reads either a single `Snapshot` JSON
  document or an NDJSON envelope stream.
- **Entity sync** (`sync --entities`) drains every page of teams,
  workflow states, users, labels, and projects via a shared paginator.
- **Live tail sync** (`sync --updated-since <ts|24h|7d>`) issues
  `issues(filter:{updatedAt:{gte:$since}}, first:N, after:$cursor,
  orderBy: updatedAt)`, paginates with cursor-stall detection and a hard
  page cap, drains nested `labels`/`comments` connections for each issue,
  bounds the request size against `--max-issues`, and persists the
  high-water mark to `sync_state.issues.tail`. `--resume` reads that
  stored mark for the next run.
- **Exact sync** (`sync --issue <id-or-identifier>`) hydrates one issue.
- **Streaming sync** (`sync --updated-since --ndjson`) writes each
  ingested issue as a JSON line; the cursor only advances for pages
  whose `onIssue` callbacks all returned nil, so a broken stdout pipe
  does not lose unconsumed issues.

Provider access uses the official Linear GraphQL API. UI scraping,
undocumented endpoints, rate-limit bypass, and credential sharing are out
of scope.

## Agent use

`describe --json` reports the full machine-readable surface. `doctor
--offline --json` reports redacted credential presence (`set` / `unset`)
and the resolved on-disk paths. `--fields a,b,c` on `show`/`search`/`status`
trims response payloads to a whitelist; unknown fields produce a
validation error that lists the known set. See [skills/lincrawl/SKILL.md](../skills/lincrawl/SKILL.md)
for per-workflow guidance and the error-code cheat sheet.
