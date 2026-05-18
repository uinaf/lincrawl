# lincrawl

Local-first Linear work-graph archive CLI.

`lincrawl` syncs Linear teams, projects, issues, comments, labels, and
workflow states into a private local SQLite archive, runs FTS5 search
offline, exports a canonical JSONL dump, and publishes encrypted
`*.jsonl.zst.age` snapshots to a tenant-controlled store that
subscribers can verify and re-import. Read-only against Linear by
construction.

This repository is the generic crawler core. Tenant credentials, real
workspace identifiers, issue bodies, comments, snapshots, logs, reports,
screenshots, and transcript-derived examples do not belong here.

## Install

From a checkout:

```bash
go run ./cmd/lincrawl version --json
./scripts/smoke
```

## Quick start

Keep local state outside tracked paths:

```bash
export LINCRAWL_HOME=/tmp/lincrawl-home
go run ./cmd/lincrawl doctor --offline --json
```

Offline path with synthetic fixtures (no `LINEAR_API_KEY` required):

```bash
go run ./cmd/lincrawl sync --fixture testdata/synthetic --json
go run ./cmd/lincrawl search "ingest" --json
go run ./cmd/lincrawl show LIN-1 --json
```

Live path against Linear (reads `LINEAR_API_KEY` from `.env.local` or env):

```bash
go run ./cmd/lincrawl sync --entities --json
go run ./cmd/lincrawl sync --updated-since 24h --max-issues 200 --json
go run ./cmd/lincrawl sync --resume --max-issues 1000 --json
go run ./cmd/lincrawl sync --issue LIN-42 --json
```

## Commands

JSON by default everywhere. Errors are JSON envelopes on stderr.

| Command | Purpose |
|---|---|
| `doctor --offline` | Report resolved paths and redacted credential presence |
| `describe` | Machine-readable schema for every command (args, flags, exit codes, field masks, mutually-exclusive groups) |
| `status` | Local archive row counts |
| `sync` | Ingest from `--fixture`, `--stdin`, `--entities`, `--updated-since`, `--resume`, or `--issue`; supports `--dry-run`, `--ndjson`, `--max-issues`, `--page-size` |
| `search <query>` | FTS5 search; supports `--fields`, `--limit`, `--raw`, `--ndjson` |
| `show <id-or-identifier>` | Resolve one issue by UUID or `TEAM-N`; supports `--fields` |
| `query --graphql … --vars …` | Pass-through to the Linear GraphQL API for raw queries |
| `export --out <path>` | Canonical NDJSON dump of the local archive; `--out` is sandboxed to CWD |
| `guard` | Scan the working tree for tenant leaks, plaintext archives, secrets |
| `version` | Build version, commit, date |

Round-trip via NDJSON:

```bash
lincrawl export --out ./snapshots/lincrawl.jsonl --json
LINCRAWL_HOME=$(mktemp -d) lincrawl sync --stdin --json \
  < ./snapshots/lincrawl.jsonl
```

## Configuration

`LINEAR_API_KEY` is the only required key, and only for live calls. Put it
in a git-ignored `.env.local`. `LINCRAWL_HOME` overrides the XDG data dir.
See [`.env.example`](.env.example) for the full set.

## Docs

- [Agent guide](AGENTS.md) — operator/agent contract
- [Agent skill](skills/lincrawl/SKILL.md) — structured per-workflow guidance
- [Architecture](docs/architecture.md) — CLI, store, sync shape
- [Roadmap](docs/roadmap.md) — what's done and what's next
- [Tenant data boundary](docs/tenant-data-boundary.md) — what stays out
- [Bootstrap plan](docs/plans/bootstrap-claude-prompt.md) — original prompt
- [Contributing](CONTRIBUTING.md) — local setup and validation
- [Security](SECURITY.md) — private vulnerability reporting

## Verification

```bash
./scripts/verify           # tidy, vet, test, race, smoke, guard, release-check, whitespace
./scripts/local-live-smoke # opt-in bounded live Linear proof, env-gated
```

CI runs `verify` on every PR and push. Tagged releases (`v0.0.x`) come
from `main` via semantic-release + GoReleaser; see [Distribution](docs/distribution.md).

## Acknowledgements

Built on [`openclaw/crawlkit`](https://github.com/openclaw/crawlkit) for
the SQLite open / PRAGMA cocktail / schema-version / read-only / state
primitives. The CLI surface, agent-DX shape, structured error envelope,
and release pipeline mirror conventions from sibling crawlers in the
[`openclaw`](https://github.com/openclaw) family.

## License

MIT. See [License](LICENSE).
