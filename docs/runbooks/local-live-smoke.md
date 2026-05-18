# Local live smoke

Bounded proof against the real Linear API. Not part of `./scripts/verify`;
operators run it locally before declaring a release safe.

## Why

`./scripts/verify` covers only the offline path (synthetic fixtures, FTS,
ingest, export). The live tail, entity sync, retry/backoff, and rate-limit
plumbing need real Linear to exercise. This runbook is the sanctioned
way to do that without leaking tenant data into the repo.

## Pre-flight

Set `LINEAR_API_KEY` in your shell or `.env.local`. Make sure the token
is **read-only** (Linear personal API key with read scopes only).

```bash
op whoami --account=<your-account>   # if pulling from 1Password
go run ./cmd/lincrawl doctor --json | jq '.linear_api_token'   # expect "set"
```

## Run the smoke

```bash
./scripts/local-live-smoke
```

Default behavior:

- `LINCRAWL_HOME=$(mktemp -d)` so no state lingers on disk.
- `doctor --offline` confirms config resolves.
- `sync --entities --dry-run` then `sync --entities` exercises the
  paginated entity pull and writes teams/states/users/labels/projects.
- `status` reports the resulting counts.

## Bigger blast radius

Opt in with env vars. None are set by default.

| Variable | Effect |
|---|---|
| `LINCRAWL_LIVE_HOME` | Reuse this persistent state dir instead of a temp dir. Useful for inspecting the resulting DB. |
| `LINCRAWL_LIVE_UPDATED_SINCE` | Run a tail sync at this window (e.g. `1h`, `24h`, `7d`). |
| `LINCRAWL_LIVE_MAX_ISSUES` | Cap issues for the tail run (default `5`). |

Example:

```bash
LINCRAWL_LIVE_UPDATED_SINCE=24h LINCRAWL_LIVE_MAX_ISSUES=20 ./scripts/local-live-smoke
```

## Privacy

- Do not paste smoke output containing real issue identifiers, titles,
  bodies, or comments into commits, PRs, or chat messages. The smoke
  command itself silences command stdout precisely because output is
  tenant-shaped.
- Do not commit `LINCRAWL_LIVE_HOME` to the repo. Use a directory
  outside the working tree (e.g. `~/.cache/lincrawl-live/`).
- If you must share an output snippet for debugging, redact the
  identifier and title before sending.

## What this does NOT do

- It is not a regression suite. Use `./scripts/verify` for that.
- It does not exercise encrypted snapshot publish/import — those
  commands are deferred (see [Roadmap](../roadmap.md)).
- It does not modify Linear. The CLI is read-only by construction.
