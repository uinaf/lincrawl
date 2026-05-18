---
name: lincrawl
version: 0.1.0
description: |
  Local-first Linear archive CLI. Read-only against Linear; writes a SQLite
  archive at $LINCRAWL_HOME with FTS5 search. JSON by default, structured
  errors, --fields field masks, --dry-run on every sync mode, --out export
  sandboxed to CWD, raw GraphQL passthrough.
discover:
  describe: lincrawl describe --json
  doctor: lincrawl doctor --offline --json
defaults:
  output: json
  exit_codes:
    ok: 0
    internal: 1
    usage: 2
    not_found: 3
    validation: 4
    config: 5
---

# lincrawl agent skill

## Always

- `--json` is default; parse stdout as JSON unless you pass `--ndjson` or
  `--out`. Errors come on stderr as `{"code","exit","message"}`.
- Branch on the `exit` field of the error envelope, not on raw exit status
  alone. `3` is not-found (recoverable), `4` is validation (you sent bad
  input), `5` is config (LINEAR_API_KEY missing).
- Use `--fields` on `show`, `search`, and `status` to keep responses small.
  Unknown fields produce a validation error that lists the known set, so
  you can recover with one retry.
- Use `--dry-run` on every `sync` mode the first time you call it with new
  arguments — it returns the plan (mode, counts, since, end_cursor) without
  touching SQLite.
- Use `--max-issues` whenever you sync tail issues to cap blast radius. The
  CLI bounds per-request size against this number.
- Use `--ndjson` on `search` and on `sync --updated-since` for streaming
  page-by-page output instead of a single envelope.

## Never

- Do not retry on `exit: 4` (validation) without changing the input.
- Do not pass user-supplied query text raw into `search` — let lincrawl
  quote it as an FTS5 phrase. Only pass `--raw` when you control the query
  syntax.
- Do not commit `.env.local` or `$LINCRAWL_HOME/lincrawl.db` — the latter
  holds tenant issue bodies and comments at 0600 / 0700.

## Workflows

### Bootstrap a fresh archive against live Linear

```bash
export LINCRAWL_HOME=$(mktemp -d)
lincrawl doctor --offline --json | jq '.linear_api_token'    # expect "set"
lincrawl sync --entities --json | jq '.counts'
lincrawl sync --updated-since 30d --max-issues 500 --json | jq '{pages, issues_pulled, new_high_water}'
lincrawl status --json --fields counts
```

### Incremental refresh on a schedule

```bash
lincrawl sync --resume --max-issues 1000 --json | jq '{pages, issues_pulled, new_high_water}'
```

`--resume` reads the saved high-water mark; idempotent upserts handle the
overlap-on-boundary case so duplicates do not appear.

### Hydrate one issue you have an identifier for

```bash
lincrawl sync --issue LIN-42 --dry-run --json
lincrawl sync --issue LIN-42 --json | jq '.issue | {identifier, title}'
lincrawl show LIN-42 --fields identifier,title,team_key,state_name,labels --json
```

### Search the local archive

```bash
lincrawl search "billing" --fields identifier,title,snippet --json
lincrawl search "billing" --ndjson | head -20         # stream rows
lincrawl search 'state:"In Progress"' --raw --json    # opt into raw FTS5
```

### Run a raw Linear query you wrote yourself

```bash
lincrawl query --graphql 'query Me { viewer { id name email } }' --json
lincrawl query --graphql-file ./my-query.graphql --vars '{"teamId":"abc"}' --json
```

`query` does not write to the store; it returns the raw `data` envelope so
you can post-process however you like.

### Snapshot the local archive for handoff

```bash
mkdir -p ./snapshots
lincrawl export --out ./snapshots/lincrawl-$(date -u +%Y%m%dT%H%M%SZ).jsonl --json
```

`--out` is sandboxed to the working directory; symlinks are resolved and
paths that escape are rejected. Re-import into a fresh lincrawl with
`sync --stdin` — the same NDJSON stream that `export` emits round-trips
losslessly:

```bash
LINCRAWL_HOME=$(mktemp -d) lincrawl sync --stdin --json \
  < ./snapshots/lincrawl-*.jsonl
```

`sync --stdin` also accepts a single `Snapshot` JSON document (the same
shape as `testdata/synthetic/snapshot.json`).

## Error handling cheat sheet

| code | exit | meaning | retry? |
|---|---|---|---|
| `ok` | 0 | success | n/a |
| `internal` | 1 | bug in lincrawl or SQLite I/O | no — file an issue |
| `usage` | 2 | argument parse failed | yes after fixing args |
| `not_found` | 3 | requested issue absent | yes against a different id |
| `validation` | 4 | input rejected (bad identifier, percent-encoded path, unknown field, etc.) | yes after fixing input |
| `config` | 5 | LINEAR_API_KEY missing or unreadable | no until env is set |
