# Roadmap

Status: active

This roadmap lists the current product direction. Completed bootstrap notes
and tenant-specific operating details do not belong in this repository.

## Available now

- Go CLI with JSON-first output, structured error envelopes on stderr,
  and classified exit codes (`ok`, `internal`, `usage`, `not_found`,
  `validation`, `config`).
- `describe --json`: full machine-readable schema for every command
  (args, flags with type/required/default, mutually-exclusive groups,
  exit-code table, field-mask vocabulary).
- `doctor --offline --json` with redacted credential presence flags.
- Fixture sync from `testdata/synthetic/`, stdin sync of `Snapshot` JSON
  or NDJSON envelopes, entity sync (paginated), exact-by-id sync, bounded
  tail sync against Linear with `--updated-since` / `--resume` and
  `sync_state`-persisted high-water mark.
- Cursor-stall detection and a hard page cap on every paginator.
- Streaming tail sync (`sync --updated-since --ndjson`) that only commits
  the cursor for pages fully drained to the consumer.
- FTS5-backed `search` over issue identifier, title, description, and
  comments with `bm25` ordering, snippet markers, `--fields` field masks,
  and `--ndjson` per-row streaming.
- `show` resolving by Linear UUID or `TEAM-N` identifier with `--fields`.
- `query` raw GraphQL passthrough (inline or file-backed).
- `export --out` canonical NDJSON dump, sandboxed to CWD with symlink
  resolution; `export | sync --stdin` round-trips losslessly.
- `--dry-run` on every `sync` mode.
- Input hardening: identifier regex, control-character rejection, output
  path sandbox, FTS5 phrase quoting.
- Local archive at `0700/0600` permissions.
- Agent skill at `skills/lincrawl/SKILL.md` (YAML frontmatter + workflows).
- `./scripts/smoke` and `./scripts/verify` for the local gate.

## Next

- Encrypted snapshot pipeline (`archive`, `publish`, `import`,
  `store verify`, `subscribe`) — wraps `export` output in zstd + age and
  adds manifest tracking. Mirrors the fincrawl artifact shape
  (`*.jsonl.zst.age`).
- Repository guard (`guard --json`) blocking committable plaintext
  archives, generated artifacts, secret-looking values, real Linear
  URLs, and transcript-like data outside `testdata/synthetic/`.
- CI verify on PRs and `goreleaser release` on tags via semantic-release
  for `0.0.x` bootstrap.

## Later

- Cycles and initiatives as first-class entities once issue/project
  sync is proven.
- Attachment metadata.
- Live API-derived `describe` (introspection schema for the Linear types
  lincrawl actually uses).
- Response sanitization (Model Armor or equivalent) on raw `query`
  results to defend against prompt-injection embedded in tenant data.
- Bounded scheduled crawl in the tenant store once incremental sync is
  proven cost-stable.
- MCP surface alongside the CLI.

## Non-goals

- Write-back to Linear (issues, comments, labels, states, projects)
- Undocumented endpoints or UI scraping
- Committed real Linear data, snapshots, or transcript-derived examples
- Multi-tenant orchestration in this repository
