# Release model

This document defines the environments, versioning, release, rollback and migration rules for
SPM. It is the operational companion to [`TASKS.md`](../TASKS.md) and
[`SECURITY.md`](../SECURITY.md).

## 1. Environments

| Environment | Source | Purpose | Data |
|---|---|---|---|
| Local | Working tree | Development and debugging | Disposable Compose volumes |
| CI | Pull request merge commit | Verification authority | Ephemeral; no external data |
| Staging | Tagged release `vX.Y.Z` | Release rehearsal and rollback drill | Production-like, non-customer data |
| Production | The same tagged release, promoted | Customer workload | Real data; backed up before upgrade |

There is no long-lived `staging` branch. Staging and production deploy the same immutable tag;
only configuration differs. A commit that is not on `master` and not tagged is never deployed.

Staging is an operator-provided target; this repository defines the contract and the commands but
does not provision the host. Until the operations runbooks (`OPS-09`, `OPS-12`) provide it, treat
the staging rows and the rollback rehearsal as the required procedure, not as recorded results.

`master` is the integration line. It is protected, squash-only and green before merge. A tag is a
release promise; a branch head is not.

## 2. Versioning

Releases use `MAJOR.MINOR.PATCH` with a `v` prefix (`v0.3.1`). The project is pre-1.0: breaking
changes may land in a minor release, and the changelog marks them `BREAKING`.

| Component | Version source | Rule |
|---|---|---|
| API | Build metadata injected at link time from the git tag | `--version` and `/health` report the tag |
| UI | `VITE_APP_VERSION` build argument (the tag) | Shown in the sidebar footer |
| AI service | `SPM_AI_VERSION` environment variable | Echoed by `/health` |
| Images | Same tag as the release | `spm-api:<tag>`, `spm-ui:<tag>`, `spm-ai:<tag>` |

The version is derived from the tag: the release workflow extracts it from
`github.ref_name`, passes it to the image builds and the API reports it from the binary. For a
manual tagged deployment, set `SPM_VERSION` (without the `v`) in `.env` before building; the
compose defaults are deliberately local-safe (`dev`).

## 3. Release artifacts

A release consists of:

- The annotated git tag `vX.Y.Z` on a `master` commit.
- The `CHANGELOG.md` section for that version.
- Three image SBOMs (SPDX JSON) and three Trivy SARIF reports, attached to the GitHub release.
- The images built from the tag, locally or in the release workflow.

Container images are not pushed to a registry yet, because no registry or deployment policy
exists in this repository. Consumers build from the tag with `docker compose build`, or pull from
the registry chosen by `OPS-07`. Image signing and provenance attestation are delivered by
`OPS-07`; until then no signed-image claim is made.

## 4. Preparing a release

Prerequisites: a clean `master` checkout that is up to date with `origin/master`, `gh`
authenticated, and a green pull-request gate on the release commit.

```powershell
git switch master
git pull --ff-only
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1 -Version 0.3.1
```

The script:

1. Validates the version against `vMAJOR.MINOR.PATCH` and rejects an existing tag.
2. Generates the changelog entry from conventional-commit subjects and task IDs.
3. Creates the `chore/release-v0.3.1` branch, commits `CHANGELOG.md`, pushes it and opens the release
   pull request.
4. Stops. It never tags an unmerged commit.

Merge the release pull request through the normal gate (`chore/release-*` is still a pull
request), then tag the merge commit:

```powershell
git switch master
git pull --ff-only
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1 -Tag 0.3.1
```

`-Tag` refuses to run when the working tree is dirty, when `master` is behind its remote, or when
the commit is not on `origin/master`. It creates an annotated tag and pushes it.

Pushing the tag starts `.github/workflows/release.yml`, which:

- Verifies the tag is valid semver, its commit is contained in `origin/master`, and no forbidden
  secret or key file is tracked.
- Re-runs the supply-chain checks.
- Builds the API, UI and AI images, generates SBOMs and Trivy HIGH/CRITICAL reports, and uploads
  them as release evidence.
- Generates the changelog section from conventional commits in the previous-tag..tag range and
  creates or updates the GitHub release. Prerelease tags (containing `-`) are marked as
  prereleases. Attaches the SBOMs and SARIF reports to the release.

The workflow publishes; it never commits to `master` and never opens a pull request.

A dry run on a scratch tag (`v0.0.0-rc.1`) is valid; the workflow treats any valid semver tag the
same way.

## 5. Promotion

1. The release pull request is green and merged; the tag is created.
2. Deploy the tag to staging and run the staging smoke check:
   ```powershell
   $env:SPM_VERSION = '0.3.1'
   docker compose up -d --build
   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -CheckStack
   ```
3. Back up production: `scripts/backup.ps1`.
4. Deploy the identical tag to production with `SPM_VERSION` set to the same value and the same
   build command.
5. Verify `/health` reports the tag, the API ready endpoint is green, and the UI footer shows the
   tag. Watch logs and the deployment webhook path for one release cycle.

Artifacts are promoted by tag, not rebuilt. Local and production builds of the same tag must
produce functionally identical images.

## 6. Migration and deploy order

Migrations are embedded in the API binary and applied at startup, in lexical order, inside a
transaction. They are forward-only: there is no down migration.

Every schema change follows expand/contract across two releases:

1. **Expand** (`vN`): add nullable columns, new tables or new indexes concurrently. The new code
   writes both old and new shapes when needed. The previous application version (`vN-1`) remains
   compatible with the schema.
2. **Migrate**: backfill data in bounded batches, with progress logging and an idempotent query.
3. **Switch**: the new code reads the new shape. Keep the old shape writable until `vN+1`.
4. **Contract** (`vN+1` or later): drop the old column, table or index only after `vN` has been
   stable for a full release cycle and no rollback to `vN-1` is expected.

Deploy order is therefore:

1. Backup.
2. Start the new API image; migrations run before the server accepts traffic.
3. Wait for `/health/ready`.
4. Deploy the UI and AI images.
5. Run the smoke check.

Never run a destructive migration in the same release that introduces the replacement read path.
Never assume the database schema is newer than the application during a rollback window.

## 7. Rollback

### 7.1 Application rollback

The preferred rollback is redeploying the previous tag from a clean checkout:

```powershell
$tag = 'v0.3.0'
git fetch --tags
git switch --detach $tag
$env:SPM_VERSION = $tag.TrimStart('v')
docker compose up -d --build
```

Rules:

- Roll back the whole stack to the same tag. Do not mix API, UI and AI versions from different
  tags.
- The database is **not** rolled back. Because migrations are expand/contract, the previous
  application must still work against the expanded schema. If it cannot, the release is not
  eligible to ship.
- If a destructive migration has already run, roll forward with a fix. Restoring the pre-release
  backup is a last resort and requires accepting data loss for the interval since the backup.

### 7.2 Rollback rehearsal

Before the first production release, and at least once per minor version, rehearse in staging:

1. Deploy tag `vN` and run the smoke check.
2. Deploy tag `vN-1` over it without restoring the database.
3. Confirm `/health/ready`, login, project listing and one write path work.
4. Record the date, tags and result in the release pull request.

`OPS-09` owns the recurring drill and its evidence. A release that changes the schema or a
required configuration value cannot be promoted until that drill has passed for the minor line.

### 7.3 Reverting a bad change

If the defect is isolated, revert the squash commit through a `fix/revert-...` pull request and
release a patch. Never force-push or re-tag a published version.

## 8. Compatibility

- The API supports the current and previous minor client (`N` and `N-1`). Breaking HTTP changes
  follow the versioning policy in [`docs/API.md`](API.md).
- The database supports the current application and the immediately previous tag during the
  rollback window.
- Configuration is additive. A new required variable must have a safe default for one release
  and be documented in the README and `.env.example` before it becomes mandatory.
- The AI service is stateless; it can be rolled back independently only when the API contract
  between them is unchanged.

## 9. Changelog

`CHANGELOG.md` is generated, not hand-edited, except for the summary line. Entries are grouped by
type and include the pull-request number and every task ID found in the commit or pull request:

- `BREAKING CHANGES`
- `Features`
- `Fixes`
- `Performance`
- `Documentation`
- `Tests`
- `Chores`

Regenerate the unreleased section at any time:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/changelog.ps1 -Unreleased
```

Compare two refs:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/changelog.ps1 -From v0.2.0 -To v0.3.0
```
