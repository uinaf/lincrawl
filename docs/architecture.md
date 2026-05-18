# Architecture

Status: active

`lincrawl` is a local-first Linear archive CLI.
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
internal/cli/          Kong command tree, JSON output, classified
                       errors, field masks, input validation,
                       output-path sandbox (symlink-resolved)
internal/config/       env + .env.local loading with redacted presence
                       flags
internal/buildinfo/    version/commit/date strings (overridden via
                       -ldflags during GoReleaser build)
internal/linear/       entity types, fixture loader, GraphQL client
                       with typed errors (RateLimited / Auth / NotFound
                       / APIError), Retry-After + jittered backoff,
                       injectable Sleep/Now, paginate helper
internal/store/        crawlkit-backed SQLite (WAL + busy_timeout +
                       mmap_size + schema_migrations) + FTS5 (bm25
                       weighted) + idempotent ingest with content_hash
                       short-circuit + raw_blobs table + NDJSON-or-
                       Snapshot ingest stream + read-only open variant
internal/syncer/       fixture / entity / exact / tail / streaming
                       sync with 60s overlap window, wall-clock budget,
                       cursor-stall + empty-page-with-hasNext guards,
                       cursor persistence
internal/lock/         file lock (<db>.lock O_EXCL) for concurrent
                       sync runs
internal/guard/        working-tree scan honoring .gitignore; rejects
                       tenant leaks, plaintext archives, real op://
                       references, Linear tokens, Linear URLs / UUIDs
testdata/synthetic/    snapshot.json fixtures for deterministic tests
skills/lincrawl/       agent skill (YAML frontmatter + workflows)
```

Built on [`github.com/openclaw/crawlkit`](https://github.com/openclaw/crawlkit)
v0.6.0 for the SQLite open/PRAGMA/schema-version/read-only primitives.

## Store

The local SQLite archive is the source of truth for what lincrawl has seen.
crawlkit opens it with WAL + synchronous=NORMAL + temp_store=MEMORY +
mmap_size=256MB + busy_timeout=5000; files are created at `0600`, the
parent directory at `0700`, so the archive is not world-readable on
shared machines. Schema:

- `schema_migrations(version)` — refuses to open if the db's version
  exceeds the supported version.
- `teams`, `workflow_states`, `users`, `labels`, `projects`
- `issues` with foreign keys to `teams`, `projects`, `workflow_states`,
  `users` (assignee, creator), plus a `content_hash` column. Missing
  references are stub-inserted on ingest so a sparse entity pull does
  not block issue ingest. Re-ingesting an unchanged issue (same
  content_hash) short-circuits before touching the upsert or FTS path.
- `issue_labels` join table — purged and rebuilt per issue on every
  refresh.
- `comments` — purged and rebuilt per issue on refresh, so deleted
  upstream comments disappear locally.
- `issue_fts` FTS5 mirror over `identifier`, `title`, `description`,
  and concatenated comment bodies. `bm25` weighted `(0, 10, 5, 1)` so
  title hits dominate. `SafeSnippet` sanitizes control chars and byte-
  budgets the output.
- Composite indexes: `issues(team_id, updated_at desc)`,
  `issues(state_id, updated_at desc)`,
  `issues(assignee_id, updated_at desc)`,
  `issues(project_id, updated_at desc)`,
  `comments(issue_id, created_at, id)`.
- `raw_blobs(sha256, kind, entity_id, payload, ingested_at)` — table
  reserved for raw provider payload retention (replay/backfill from
  disk without re-crawling). Not populated yet; live sync currently
  retains only the typed Snapshot.
- `sync_state(source_name, entity_type, entity_id, value, updated_at)`
  — crawlkit/state composite-key schema; `linear/issues_tail/default`
  persists the live tail-sync cursor for `sync --resume`. `SaveCursor`/
  `LoadCursor` wrap it for API stability.

`OpenReadOnly` is used by `search`, `show`, `status`, and `export` so
they never contend with a concurrent writer holding the WAL.

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

Every sync mode runs under a `<db>.lock` file lock so two `lincrawl
sync` invocations cannot race on the same archive.

- **Fixture sync** loads `testdata/synthetic/snapshot.json` (or any
  `*.snapshot.json` file under a fixture directory). It validates
  referential integrity at parse time so fixture typos fail fast.
- **Stdin sync** (`sync --stdin`) reads either a single `Snapshot` JSON
  document or an NDJSON envelope stream.
- **Entity sync** (`sync --entities`) drains every page of teams,
  workflow states, users, labels, and projects via a shared paginator.
- **Live tail sync** (`sync --updated-since <ts|24h|7d>`) issues
  `issues(filter:{updatedAt:{gte:$since}}, first:N, after:$cursor,
  orderBy: updatedAt)`. `since` is shifted backwards by 60s on every
  run so updates inside the same second cannot escape between runs.
  Paginates with three guards: cursor-stall (`endCursor` repeats), 4
  consecutive empty pages with `hasNextPage=true`, and a 30-minute
  wall-clock budget. Drains nested `labels` / `comments` connections
  for each issue. Bounds the request size against `--max-issues` per
  page. Persists the high-water mark to `sync_state.linear.issues_tail`.
  `--resume` reads that stored mark for the next run.
- **Exact sync** (`sync --issue <id-or-identifier>`) hydrates one issue.
- **Streaming sync** (`sync --updated-since --ndjson`) writes each
  ingested issue as a JSON line; the cursor only advances for pages
  whose `onIssue` callbacks all returned nil, so a broken stdout pipe
  does not lose unconsumed issues.

The Linear client retries 429/5xx (with `Retry-After` if the server
sent one) up to `MaxAttempts` times with exponential backoff + jitter
through a context-aware sleeper. Authentication failures (401/403) and
not-found errors are surfaced immediately without retry.

Provider access uses the official Linear GraphQL API. UI scraping,
undocumented endpoints, rate-limit bypass, and credential sharing are
out of scope.

## Agent use

`describe --json` reports the full machine-readable surface. `doctor
--offline --json` reports redacted credential presence (`set` / `unset`)
and the resolved on-disk paths. `--fields a,b,c` on `show`/`search`/`status`
trims response payloads to a whitelist; unknown fields produce a
validation error that lists the known set. See [skills/lincrawl/SKILL.md](../skills/lincrawl/SKILL.md)
for per-workflow guidance and the error-code cheat sheet.
