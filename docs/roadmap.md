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
  `mutates` classification, examples, notes, exit-code table,
  field-mask vocabulary). Recursive walker so multi-word commands like
  `store verify` are discoverable.
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
- **Encrypted snapshot pipeline**: `archive` (fixture), `publish`
  (local store), `import` (single snapshot), `store verify` (manifest +
  canonical artifact layout), `subscribe` (verify + ingest every
  snapshot in a tenant store). Artifacts are zstd-compressed and
  age-encrypted (`*.jsonl.zst.age`) with X25519 or SSH recipients.
- Repository guard (`guard --json`) blocking committable plaintext
  archives, generated artifacts, secret-looking values, real Linear
  URLs, and transcript-like data outside `testdata/synthetic/`.
- CI verify on PRs and `goreleaser release` on tags via semantic-release
  for `0.0.x` bootstrap.
- `--dry-run` on every `sync` mode.
- Input hardening: identifier regex, control-character rejection, output
  path sandbox, FTS5 phrase quoting, manifest schema-version allowlist.
- Local archive at `0700/0600` permissions; cross-process file lock
  around every writer.
- Agent skill at `skills/lincrawl/SKILL.md` (YAML frontmatter + workflows).
- `./scripts/smoke` and `./scripts/verify` for the local gate.

## Next

- Raw provider payload retention populated from live sync (table exists;
  writes are not wired).
- Active-window resume with per-issue commit so a mid-page crash on a
  large backfill loses one issue, not a page.
- `content_hash` short-circuit reuse on `import`/`subscribe` so
  ingesting an unchanged snapshot is a no-op.

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
