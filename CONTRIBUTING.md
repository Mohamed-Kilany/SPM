# Contributing

Thanks for improving SPM. This repository is maintained as a production-grade, offline-first
software delivery platform, so changes are expected to be small, reviewable and backed by
evidence.

## Source of truth

| Document | Purpose |
|---|---|
| [`TASKS.md`](TASKS.md) | Delivery plan, task IDs, acceptance criteria and status |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Runtime topology and service responsibilities |
| [`docs/ENTERPRISE_SPEC.md`](docs/ENTERPRISE_SPEC.md) | Product intent and non-negotiable outcomes |
| [`docs/API.md`](docs/API.md) | HTTP contract source map and versioning policy |
| [`SECURITY.md`](SECURITY.md) | Vulnerability disclosure and supply-chain policy |

## Branch and pull-request rules

- `master` is protected. All changes arrive through a pull request; direct pushes and force
  pushes are rejected.
- Use one branch per task and one purpose per pull request. Branch prefixes:
  `feature/`, `fix/`, `change/`, `docs/`, `chore/`, `test/`, `perf/`.
- Reference the task ID (for example `BASE-07` or `GOV-01`) in the PR title or body.
- Fill in [the pull-request template](.github/pull_request_template.md) with verification
  commands and results. CI is the final authority.
- Merge method is **squash only**. Delete the branch after merge. History stays linear.
- Do not commit secrets, `.env` files, kubeconfigs, private keys, database dumps, backups,
  models or build output. Secret scanning and push protection are enabled.
- Dependabot is the only accepted bot author. Do not add other automation that commits or opens
  pull requests.

## Local development

Prerequisites: Go 1.27.4 (installed automatically by the `go` directive), Node 24, Python 3.12,
and Docker for the Compose stack. The unit-test path does not require Docker.

```powershell
# API
cd api
go build ./...
go test ./... -count=1

# UI
cd ui
npm ci
npm run typecheck
npm test
npm run build

# AI
cd ai
python -m pip install -r requirements-test.txt
python -m pytest -q

# Dependency and contract checks
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/supply-chain.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1 -Check
```

The full Docker-based gate is `scripts/verify.ps1`. Integration tests
(`api/tests/integration`) and Playwright E2E (`ui/e2e`) are documented in `TASKS.md` as
`QAT-02` and `QAT-03`; they are not part of the required pull-request gate yet.

## Changing the HTTP contract

Route registration and auth metadata live in `api/internal/server`. After adding, changing or
removing a route:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1
```

Commit the regenerated `docs/openapi.yaml` with the code. The gate fails if the document is
stale, if a route is undocumented, or if the UI client targets a path that does not exist.

## Commit messages

Use Conventional Commits: `feat(scope): summary`, `fix(scope): summary`,
`docs(scope): summary`, `chore(scope): summary`, `test(scope): summary`. Keep the summary in the
imperative mood. Include the task ID in the PR body rather than stuffing it into every commit.

## Review and definition of done

- The nine required contexts on the pull request are green.
- New behavior has tests in the same change; bug fixes include a regression test.
- Documentation and `TASKS.md` evidence are updated when behavior or claims change.
- No generated noise, dead code or unrelated formatting changes are included.
- Security-sensitive changes describe the authorization check and the negative test.

There is currently one maintainer, so GitHub cannot require a second human approval without
blocking all work. The required checks, protected branch and PR-only history are enforced; ask
for review when another maintainer is available.

## Reporting security issues

Never open a public issue or pull request for a suspected vulnerability. Follow
[`SECURITY.md`](SECURITY.md).
