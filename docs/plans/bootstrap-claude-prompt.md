# Claude Bootstrap Prompt

Use this prompt to build `lincrawl`, a Linear crawler in the uinaf crawler
family, plus prepare its put.io-managed private crawl store.

```md
You are building `lincrawl`, a Linear crawler in the uinaf crawler family, plus preparing its put.io-managed private crawl store.

Repos already exist locally:
- Core crawler: `/Users/altay/projects/uinaf/lincrawl`
- Private put.io store: `/Users/altay/projects/putdotio/putio-lincrawl-store`
- put.io workspace registry: `/Users/altay/projects/putdotio/putio-frontend-workspace`

Start by reading:
- `/Users/altay/projects/uinaf/workspace/AGENTS.md`
- `/Users/altay/projects/uinaf/workspace/docs/specs/crawlkit-crawler-family.md`
- `/Users/altay/projects/putdotio/putio-frontend-workspace/AGENTS.md`
- `/Users/altay/projects/putdotio/putio-frontend-workspace/docs/shared-secrets.md`

Then inspect live precedents:
- `/Users/altay/projects/uinaf/fincrawl`
- `/Users/altay/projects/putdotio/putio-fincrawl-store`
- `/Users/altay/projects/putdotio/putio-notcrawl-store`
- `openclaw/crawlkit`
- sibling crawlkit crawler repos if present locally

Important current setup:
- `/Users/altay/projects/uinaf/lincrawl/.env.local` already exists and contains a read-only Linear token as `LINEAR_API_KEY`
- `.env.local` is intentionally ignored by git; do not print, move, commit, or copy the token
- A 1Password SSH key item has been created for this work; use the workspace secret flow when store automation or encrypted artifact access needs it
- `putdotio/putio-lincrawl-store` is registered in `putio-frontend-workspace/repos.json` as a private frontend-owned crawl store

1Password batching:
- At the beginning, inspect the repo docs and decide the smallest complete set of secrets needed for this task.
- Do one upfront 1Password pass, then avoid interrupting repeatedly for piecemeal secret prompts.
- Use the workspace secret flow from `putio-frontend-workspace/docs/shared-secrets.md`.
- Check CLI access once with `op whoami --account=putdotio.1password.com`.
- Pull only what is needed for this task, likely:
  - the read-only Linear API token if `.env.local` is missing or needs refresh
  - the SSH key item created for this work if the private store needs encrypted artifact or deploy access
- Do not print secret values.
- Do not commit secret values, real `.env.local`, decrypted outputs, plaintext snapshots, logs, or tenant artifacts.
- If 1Password access is blocked or approval times out, stop with one concise checklist of missing items instead of retrying in a loop.

Core crawler goal:
Build a Go, crawlkit-based local-first Linear archive, similar in spirit to `fincrawl`, but generic to Linear. Use `lincrawl` as the repo/CLI name.

First useful slice:
- CLI with `doctor`, `sync`, `search`, and `show`
- Linear GraphQL client using official API only
- Auth from env/config, starting with `LINEAR_API_KEY`
- SQLite-backed local archive
- Incremental sync by `updatedAt` or equivalent high-water mark
- Core entities: teams, projects, issues, comments, labels, cycles, initiatives if practical; issues/comments/projects/teams are required for MVP
- FTS search over issue title/body/comments/project/team names
- JSON output where useful for agent workflows
- Synthetic fixtures and tests only

Store goal:
Prepare `putio-lincrawl-store` as the put.io frontend-managed private crawl store for Linear crawl artifacts, following the fincrawl/notcrawl store pattern.

Store constraints:
- Keep real Linear data, plaintext outputs, logs, reports, workspace IDs, issue bodies, comments, snapshots, and credentials out of git
- Store repo may document local/manual encrypted snapshot handling
- Do not add cron or recurring GitHub Actions yet
- Keep workflows manual-only until `lincrawl` proves bounded incremental sync is cheap and reliable
- If adding secret docs, reference the 1Password/shared-secrets flow, not raw secret values

Implementation flow:
1. Check git status in all three repos, read the relevant secret docs, and do one upfront 1Password batching pass for every secret needed. Preserve unrelated changes.
2. Plan briefly from actual code/docs: what files, contracts, verification, and non-goals.
3. Scaffold/implement `lincrawl` in Go using crawlkit-aligned patterns.
4. Add focused tests with synthetic Linear GraphQL fixtures.
5. Add `README`, `.env.example`, privacy/boundary docs, and a bootstrap/MVP plan if the repo pattern expects it.
6. Set up `putio-lincrawl-store` only as far as safe for an empty manual private store: README, ignore rules, state/artifact directories, manifest/docs/scripts if matching existing stores.
7. Run repo guardrails. Prefer `make verify` or `./scripts/verify`; otherwise run format, build, lint/typecheck, and tests explicitly.
8. Run a final secret/tenant residue check before summarizing.

Definition of done:
- `lincrawl` builds and tests pass
- auth contract is documented without exposing the token
- `.env.example` uses placeholders only
- `.env.local` stays ignored
- `putio-lincrawl-store` has a safe manual-only store shape
- no real Linear data or secrets are committed
- verification commands and remaining gaps are clear

Stop and ask if:
- repo naming conflicts with existing checked-in state
- crawlkit integration needs a design choice that changes shared mechanics
- official Linear API access cannot support the MVP without a workaround
- the store setup would require committing real data, secrets, or enabling recurring automation
```
