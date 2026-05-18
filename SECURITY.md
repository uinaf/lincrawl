# Security

`lincrawl` reads from Linear and writes local archives. Treat tenant data with
care.

## Reporting a vulnerability

Report vulnerabilities privately to the repository maintainer rather than
filing a public issue. Do not include real tenant data, tokens, or proof of
concepts that depend on tenant secrets.

## Local handling

- Keep `LINEAR_API_KEY` in `.env.local` (git-ignored) or a 1Password-backed
  environment, never in committed files or shell history.
- Tenant runtime state, encrypted snapshots, and exports belong outside this
  repository, in a tenant-controlled store.
- Pre-flight `op whoami --account=<your-account>` before pulling
  secrets through 1Password; surface missing auth instead of retrying.

## Boundary

- Read-only against Linear; no write-back endpoints.
- No undocumented endpoints, scraping, or rate-limit bypass.
- No committed real Linear data of any kind.
