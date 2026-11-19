# Security policy

## Reporting a vulnerability

Report suspected vulnerabilities privately through GitHub:

1. Open the repository **Security** tab.
2. Choose **Advisories** and then **Report a vulnerability**.
3. Include the affected version or commit, a minimal reproduction, the impact, and any
   sanitized logs.

Do **not** open a public issue, pull request or discussion for a suspected vulnerability, and do
not include secrets, access tokens, customer data or weaponized exploit code in a public place.
If GitHub private reporting is unavailable, contact the maintainer listed in the repository
profile.

We will acknowledge a report within **2 business days**, provide an initial assessment within
**5 business days**, and coordinate disclosure after a fix is available. These are response
targets, not contractual SLAs.

For non-security help, see [`SUPPORT.md`](SUPPORT.md). For branch, commit and pull-request
rules, see [`CONTRIBUTING.md`](CONTRIBUTING.md). Conduct reports follow
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) and are also private.

## Supported versions

The project is pre-release: there are no published release tags yet. `master` is the only
supported line and receives security fixes. Once releases are tagged (see the release model
tracked by `BASE-08` in `TASKS.md`), the latest minor and the previous minor will be supported;
older minors will receive a fix only when the vulnerability is critical and backporting is
feasible.

## Scope

In scope:

- The Go API in `api/`, the Vue client in `ui/` and the FastAPI service in `ai/`.
- The Compose stacks, Dockerfiles, scripts and GitHub Actions workflows in this repository.
- Authentication and authorization, project isolation, webhook handling, SSE tickets, file
  exports, and AI retrieval and answer assembly.

Out of scope:

- Vulnerabilities in third-party services or infrastructure that this repository does not
  control.
- Social engineering, physical access, denial of service against a single development machine,
  and reports from automated scanners without a demonstrated impact.
- Dependency versions that are already documented in the exception register below, unless you can
  demonstrate a reachable impact that the compensating control does not cover.

## Supply-chain controls

| Control | Where it runs | Current state |
|---|---|---|
| Go module checksums | `go mod verify` in the Go job | Required |
| Lockfile installs | `npm ci` in the UI job; exact pins in `ai/requirements.txt` | Required |
| Reachable Go vulnerabilities | `govulncheck` v1.10.0 in the Go job | **Blocking**; baseline is clean |
| JavaScript advisories | `npm audit --audit-level=high` in the UI job | **Blocking** for high/critical |
| Python advisories | `pip-audit` 2.10.1 against `ai/requirements-test.txt` (production plus test pins) | **Blocking**; baseline is clean |
| SBOMs | Syft SPDX JSON for the API, UI and AI images in the Images job | Generated for every pull request, retained 30 days |
| Image scanning | Trivy HIGH/CRITICAL, unfixed ignored, SARIF evidence in the Images job | **Advisory**; see the promotion rule |
| Secret scanning | GitHub secret scanning with push protection | Enabled; no ignored alerts |
| Dependency updates | Dependabot alerts, automated security fixes and grouped security PRs | Enabled; Dependabot is the only accepted bot author (`ADR-023`) |
| Image signing | Not implemented in this repository | Tracked by `OPS-07`; see below |

The Images job is a required context, so a failure to generate an SBOM, upload evidence, or run
the scanner fails the pull request. Scan **findings** are advisory until the image baseline is
clean.

### Promotion rule for image findings

Trivy findings are recorded with `exit-code: 0`. An image HIGH or CRITICAL finding with an
available fix must be remediated within **7 days** or entered in the exception register. When
two consecutive image builds show no unremediated HIGH/CRITICAL finding with an available fix,
the maintainer changes `exit-code` to `1` in the same pull request that updates this document.
The required context name does not change.

### Image signing and admission

Image signing, provenance attestation and admission verification are tracked by `OPS-07`
because they require a registry and deployment policy that this repository does not yet have.
`OPS-07` consumes the SBOMs and scan evidence produced here. Until then, no release claim of
signed images is made. Verification commands will be published in that task's documentation.

## Exception register

An exception is valid only when it records an owner, a reason, a compensating control and an
expiry date. Expired exceptions fail review and must be renewed or fixed.

| ID | Finding | Severity | Scope | Owner | Reason and compensating control | Expires |
|---|---|---|---|---|---|---|
| `SC-001` | `GHSA-3g3m-2fjr-6pw4` / `glob` reached through `js-beautify` and `@vue/test-utils` | Moderate | UI build-time dev dependency; the runtime image ships only built static assets | Maintainer | No untrusted input is passed to the test tooling, and the package is not present in the running container. Resolving it requires a breaking `npm audit fix --force`. Re-evaluate with the next test-utils release. | 2027-02-28 |
| `SC-002` | Trivy image findings with no upstream fix | High/Critical (unfixed) | Base images | Maintainer | `ignore-unfixed: true` keeps unfixed upstream issues out of the blocking path; fixed findings follow the promotion rule. | Reviewed monthly |

Current open Dependabot alerts: **0**. Current open secret-scanning alerts: **0**. These counts
are verified with the GitHub API before each task closure and are not inferred from the
dependency-review job.

## Release security checklist

Before tagging a release:

- The pull request gate is green, including `govulncheck`, `npm audit`, `pip-audit`, the OpenAPI
  contract check and CodeQL.
- Dependabot and secret-scanning alerts are triaged; every exception has an owner and an
  unexpired date.
- The SBOM and Trivy evidence artifacts from the release commit are archived.
- No secret, kubeconfig, `.env` file, database dump or build output is present in the tag.
- The rollback path in the release model tracked by `BASE-08` is current.

## Verification commands

```powershell
# Whole supply-chain check set required to be clean locally
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/supply-chain.ps1

# Individual checks
cd api
go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...

cd ..\ui
npm audit --audit-level=high

cd ..\ai
python -m pip_audit -r requirements-test.txt --progress-spinner off
```

SBOM and image-scan evidence is produced only in CI because it requires the built images. Use a
completed PR run of **Images: docker build** and download the `supply-chain-evidence` artifact.
