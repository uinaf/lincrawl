# Open-source readiness

Checklist before flipping `uinaf/lincrawl` visibility from private to
public. Run when there is a deliberate plan to publish; do not flip
visibility ad hoc.

## Repo metadata

- [ ] `README.md` leads with value and the fastest path to use.
- [ ] `LICENSE` is MIT.
- [ ] `SECURITY.md` describes private vulnerability reporting.
- [ ] `CONTRIBUTING.md` covers local setup, validation, and commit style.
- [ ] `AGENTS.md` is the canonical agent guide; `CLAUDE.md` is a
      symlink to it.
- [ ] `skills/lincrawl/SKILL.md` exists with YAML frontmatter.

## Tenant-data residue scan

- [ ] `./scripts/verify` runs `lincrawl guard --json` clean.
- [ ] `git ls-files -z | xargs -0 grep -nE 'lin_api_[A-Za-z0-9_-]{20,}'`
      returns nothing.
- [ ] `git grep -nE '"(workspaceId|teamId|issueId|organizationId)" *: *"[0-9a-fA-F-]{36}"'`
      returns nothing outside `testdata/synthetic/`.
- [ ] `git grep -nE 'https://linear\\.app/[A-Za-z0-9_-]+/issue/'`
      returns nothing.
- [ ] **Git history**: `git log --all -p | grep -E '...'` for the same
      patterns. Anything found requires a history rewrite + force-push
      before going public.

## Branch protection ruleset

Apply to `main` before flipping visibility:

```bash
gh api -X POST repos/uinaf/lincrawl/rulesets \
  -f name=protect-main-release-flow \
  -f target=branch -f enforcement=active \
  --raw-field 'conditions={"ref_name":{"include":["~DEFAULT_BRANCH"],"exclude":[]}}' \
  --raw-field 'rules=[
    {"type":"deletion"},
    {"type":"non_fast_forward"},
    {"type":"required_linear_history"},
    {"type":"pull_request","parameters":{"required_review_thread_resolution":true}},
    {"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,
       "required_status_checks":[{"context":"verify","integration_id":15368}]}}]'
```

Verify with `gh api repos/uinaf/lincrawl/rulesets`.

## CI / release sanity

- [ ] `verify` workflow has run successfully on `main`.
- [ ] `release` workflow has run successfully at least once
      (semantic-release tag + GoReleaser binary upload).
- [ ] No GitHub Actions secrets contain tenant credentials (`LINEAR_API_KEY`
      lives only on operator machines).
- [ ] CodeQL workflow has analyzed the repo without findings, or
      findings are triaged.
- [ ] TruffleHog secret-scan workflow has run on the latest push.

## Public surface review

- [ ] No internal URLs, internal hostnames, or workspace IDs in docs.
- [ ] Sample commands in README/AGENTS/SKILL use placeholder identifiers
      (`<id>`, `<workspace>`, `LIN-1`).
- [ ] Fixtures under `testdata/synthetic/` use synthetic IDs only.

## Owner sign-off

Once every box above is checked, the repo can flip to public. Document
the date and any waivers in this file under a "History" section.

## History

(Empty — repo is still private.)
