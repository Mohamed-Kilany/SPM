# Backup and restore

This document is the operator runbook for SPM backup bundles, verification,
retention and restore drills. It describes what is implemented today and what is
still missing; `OPS-01` remains **in progress** until point-in-time recovery and
a measured full-stack RTO are demonstrated.

Related contracts:

- [`docs/MIGRATIONS.md`](MIGRATIONS.md) covers the pre-migration backup check
  used by `MIGRATION_REQUIRE_BACKUP`.
- [`docs/OBSERVABILITY.md`](OBSERVABILITY.md) defines the `spm_backup_*` metric
  catalog and alert queries.
- [`docs/RELEASING.md`](RELEASING.md) defines where the backup fits in a
  promotion.

## 1. Current status

| Capability | Status |
|---|---|
| On-demand full backup bundle | Implemented (`scripts/backup.ps1`) |
| Scheduled execution | Operator-managed schedule; no built-in scheduler |
| Chunked AES-256-GCM encryption | Implemented, required unless `-AllowUnencrypted` |
| Per-file SHA-256 manifest and verification | Implemented (`backupctl verify`) |
| Off-host copy with checksum comparison | Implemented (`SPM_BACKUP_OFFHOST_DIR`) |
| Retention by count and age | Implemented (`backupctl retention`) |
| Backup age/failure telemetry and alerts | Implemented (`spm_backup_*`) |
| Restore of databases and volumes | Implemented (`scripts/restore.ps1`) |
| Non-destructive database drill | Implemented (`scripts/restore-drill.ps1`) |
| WAL archiving and point-in-time recovery | **Not implemented** (`OPS-03`) |
| Full isolated-stack restore drill and measured RTO | **Not yet demonstrated** (`OPS-01`/`OPS-03`) |

The only proven recovery point is the last completed bundle. RPO is therefore
the backup interval plus the time to detect and fix a failure, which does not
meet the 15-minute target in `OPS-01`. That target requires continuous WAL
archiving and is tracked by `OPS-03`.

## 2. Data inventory

A full bundle captures the following data:

| Data | Source | Bundle path |
|---|---|---|
| SPM database | PostgreSQL database from `POSTGRES_DB` (default `spm`) | `databases/spm.dump` |
| Gitea database | PostgreSQL database from `GITEA_DB` (default `git`) | `databases/git.dump` |
| Gitea repositories and LFS | Compose volume `<project>_gitea_data` | `volumes/gitea_data.tar` |
| NATS JetStream | Compose volume `<project>_nats_data` | `volumes/nats_data.tar` |
| Work item attachments | Compose volume `<project>_attachments_data` | `volumes/attachments_data.tar` |
| k3s state (optional) | Compose volume `<project>_k3s_data` | `volumes/k3s_data.tar` with `-IncludeK3s` |
| Deployment configuration | Compose files, `nats/`, `envoy/`, `k8s/`, `scripts/`, `.env.example` | `config/` |
| Secrets | `.env`, only when the bundle is encrypted | `config/.env` |
| Provenance | Git commit, tag, host, image list, Docker version | `metadata/` |

Argo CD state is declarative: the applications are rebuilt from `k8s/` and the
Gitea manifest repository. That is why `k3s_data` is not archived by default.
Take `-IncludeK3s` when local cluster state is not reproducible from those
sources.

Every required volume must exist. The script refuses to create a bundle when a
volume is missing rather than silently producing an incomplete backup.

## 3. Bundle format

A bundle is a POSIX tar archive. The first entry is `manifest.json`; all other
entries are regular files under `databases/`, `volumes/`, `config/` or
`metadata/`. Directories, symlinks, hard links, devices, duplicate entries and
`..` traversal are rejected on unpack.

The manifest schema is versioned:

```json
{
  "schema_version": 1,
  "created_at": "2026-11-24T12:00:00Z",
  "host": "spm-prod-1",
  "commit": "v0.3.1",
  "tool_version": "dev",
  "databases": ["spm", "git"],
  "volumes": ["gitea_data", "nats_data", "attachments_data"],
  "entries": [
    {
      "path": "databases/spm.dump",
      "kind": "database",
      "size": 12345678,
      "sha256": "..."
    }
  ],
  "total_bytes": 23456789
}
```

`backupctl verify` recomputes every payload digest and rejects a manifest that
is missing an entry, contains an extra file, has an unknown field, has a
mismatched size/digest or whose declared total does not match the sum of its
entries. Verification is mandatory in `scripts/backup.ps1` before the job
reports success and in `scripts/restore.ps1` before anything is changed.

Encrypted bundles start with the ASCII magic `SPMBK1\n` and an 8-byte key
identifier, followed by length-prefixed AES-256-GCM chunks (1 MiB plaintext per
chunk) and an authenticated zero-length trailer. The chunk index and length are
bound as additional authenticated data, so reordering, truncation or bit flips
fail closed. The key identifier is derived from SHA-256 of the key; it is not a
secret and lets an operator identify the required key without opening the
bundle.

## 4. Key management

Generate the key once per environment and store it outside the backup
destination:

```powershell
cd api
go run ./cmd/backupctl keygen -output D:\secure\spm-backup.key
```

`keygen` writes 64 hexadecimal characters and prints the short key ID. It
refuses to overwrite an existing file unless `-force` is passed. On Unix the
file is created with mode `0600`; on Windows restrict the ACL to the backup
service account and administrators.

`backupctl` accepts a key file containing any of:

- 32 raw bytes,
- 64 hexadecimal characters,
- standard base64 encoding of 32 bytes.

Rules:

- `SPM_BACKUP_KEY_FILE` is read by `backup.ps1` and `restore.ps1`. Never place
  the key in the repository, the bundle, Compose files or a CI variable that is
  visible to pull requests.
- The key is not embedded in the bundle and there is no recovery path without
  it. Losing the key makes every encrypted bundle unreadable.
- Keep old keys until every bundle encrypted with them has expired. Rotation is:
  create a new key, update the operator secret, run a fresh backup, verify it,
  then retire the old key only after the old bundles are pruned. Re-sealing an
  existing bundle with `backupctl open`/`seal` is supported when a bundle must
  outlive its key. Never pass `-force` to `keygen` while bundles encrypted with
  the existing key are still retained.
- `-AllowUnencrypted` exists for local development only. An unencrypted bundle
  omits `.env` and must never leave the host.

## 5. Running a backup

Prerequisites: Docker with the Compose v2 plugin, a running stack, Go (or a
prebuilt `backupctl`), and an encryption key.

```powershell
$env:SPM_BACKUP_KEY_FILE = 'D:\secure\spm-backup.key'
$env:SPM_BACKUP_OFFHOST_DIR = '\\nas\spm-backups'
.\scripts\backup.ps1
```

The script:

1. resolves the Compose project and `postgres` container;
2. dumps every configured database with `pg_dump -Fc` and proves each dump is
   readable with `pg_restore -l`;
3. archives the volumes with read-only mounts and lists each archive to prove
   it is readable;
4. captures configuration and provenance metadata;
5. packs and, unless `-AllowUnencrypted` is set, seals the bundle;
6. verifies the final artifact, writes `<bundle>.sha256`, and optionally copies
   both files off-host and compares the remote digest;
7. records the status file and applies retention.

The final bundle is `spm-backup-<UTC timestamp>.tar.enc` (or `.tar` for an
unencrypted bundle). The script prints the path, size and SHA-256. Treat that
digest as the chain-of-custody record for the release or incident.

Database dumps are transactionally consistent per database, but the two
databases and the volumes are captured sequentially while the stack is running.
For a cross-store consistent recovery point, stop `api_backend`, `gitea` and
`nats` before the job and restart them afterwards, or take storage snapshots.
That quiesce step is not automated yet; without it, a restore can contain a
database row whose attachment or repository file was captured a few seconds
earlier or later. Record the chosen mode in the environment register.

On Linux, volume archives are written by a root container into the staging
directory. Run the job as a user that can clean that directory, or remove
root-owned leftovers with `sudo` before the next run; a leftover staging
directory does not invalidate a completed bundle.

Environment defaults:

| Variable | Default | Meaning |
|---|---|---|
| `SPM_BACKUP_OUTPUT_DIR` | `<repo>/backups` | Local bundle destination |
| `SPM_BACKUP_KEY_FILE` | empty | AES-256 key file; encryption is refused when empty |
| `SPM_BACKUP_OFFHOST_DIR` | empty | Copy destination for the bundle and sidecar |
| `SPM_BACKUP_REQUIRE_OFFHOST` | `false` | Fail the job when no off-host destination is configured |
| `SPM_BACKUP_KEEP_LAST` | `7` | Newest bundles always retained |
| `SPM_BACKUP_KEEP_DAYS` | `30` | Bundles older than this are removed |
| `SPM_BACKUP_STATUS_FILE` | `<output>/backup-status.json` | Status file read by the API |
| `SPM_BACKUP_ALERT_WEBHOOK` | empty | JSON `POST` target for failure alerts |

`-SkipRetention`, `-KeepLast`, `-KeepDays`, `-OffHostDirectory`, `-KeyFile`,
`-IncludeK3s`, `-AllowUnencrypted` and `-BackupctlPath` override the defaults.
If `backupctl` is not supplied, the script builds it into
`<output>/.tools/` with the local Go toolchain.

## 6. Scheduling

There is no embedded scheduler; use the host or cluster scheduler. The backup
must run as an account that can reach Docker, the repository checkout and the
key, and it must not overlap with another backup.

Windows Task Scheduler, daily at 01:00:

```powershell
$action = New-ScheduledTaskAction -Execute 'powershell.exe' `
  -Argument '-NoProfile -ExecutionPolicy Bypass -File D:\Projects\spm\scripts\backup.ps1'
$trigger = New-ScheduledTaskTrigger -Daily -At 01:00
Register-ScheduledTask -TaskName 'SPM backup' -Action $action -Trigger $trigger `
  -User 'SPMBackup' -RunLevel Highest
```

Linux with `pwsh` and cron, daily at 01:00:

```cron
0 1 * * * cd /opt/spm && SPM_BACKUP_KEY_FILE=/secure/spm-backup.key \
  SPM_BACKUP_OFFHOST_DIR=/mnt/backups /usr/bin/pwsh -NoProfile \
  -File ./scripts/backup.ps1 >> /var/log/spm-backup.log 2>&1
```

A daily full bundle does **not** meet a 15-minute RPO. Until `OPS-03` adds WAL
archiving, choose the interval the business can tolerate, keep the backup on a
separate failure domain, and record the accepted RPO in the environment
register. Do not shorten the interval by stacking overlapping full backups.

Verify the schedule after any host, credential or volume change: the status
file's `last_success_at` must advance, and `spm_backup_status_present` must stay
`1`.

## 7. Off-host copy and retention

`SPM_BACKUP_OFFHOST_DIR` copies the bundle and its `.sha256` sidecar, then
re-hashes the remote copy and fails on a mismatch. Use a different host,
failure domain or object store gateway; a directory on the same disk is not an
off-host copy. Set `SPM_BACKUP_REQUIRE_OFFHOST=true` in production so a missing
destination fails the job instead of only warning.

Retention keeps the newest `SPM_BACKUP_KEEP_LAST` bundles and every bundle newer
than `SPM_BACKUP_KEEP_DAYS`; a bundle is deleted only when it exceeds both
limits. Preview with:

```powershell
api\backups\.tools\backupctl retention -directory .\backups -keep-last 7 -keep-days 30
```

Add `-apply` to delete. Retention removes the bundle and its `.sha256` sidecar.
Apply the same policy to the off-host copy with the storage lifecycle rules;
the local retention job cannot see the remote directory.

## 8. Verification and telemetry

Verify any bundle at any time without restoring it:

```powershell
api\backups\.tools\backupctl verify -bundle .\backups\spm-backup-20261124-010000.tar.enc `
  -key-file D:\secure\spm-backup.key
```

`inspect` reads only the manifest and is suitable for a quick inventory; it
does not prove payload integrity. `verify` streams the whole bundle and must be
used before a restore and after any copy.

The backup job writes `SPM_BACKUP_STATUS_FILE` atomically (mode `0600`). The API
mounts the same file read-only and exposes it on the internal metrics listener
when `BACKUP_STATUS_FILE` is set. On Linux, run the backup job as a UID that the
API container can read, or grant a shared group; a `0600` file owned by a
different user makes the collector report read errors even while backups are
healthy. The metric catalog is in [`docs/OBSERVABILITY.md`](OBSERVABILITY.md);
the minimum alert set is:

```promql
# No successful backup in 26 hours.
time() - spm_backup_last_success_timestamp_seconds > 26 * 3600

# The last attempt failed, or failures are repeating.
spm_backup_consecutive_failures > 0
increase(spm_backup_failures_total[1h]) > 0

# Status file missing or unreadable; the collector is blind.
spm_backup_status_present == 0

# Last restore drill failed or is older than 90 days.
spm_backup_last_drill_ok == 0
time() - spm_backup_last_drill_timestamp_seconds > 90 * 24 * 3600

# Last successful bundle is unencrypted in an environment that requires it.
spm_backup_encrypted == 0
```

A failed job also writes `last_error`, increments `consecutive_failures`, and
posts to `SPM_BACKUP_ALERT_WEBHOOK` when configured. The webhook payload is a
small JSON object (`text`, `host`, `error`); point it at the incident channel or
an alert gateway, not at an endpoint that stores arbitrary payloads.

## 9. Restore procedure

Restore is destructive: it replaces both databases and, unless `-DatabaseOnly`
is passed, wipes and re-extracts the Gitea, NATS and attachments volumes.
Configuration files are extracted for the operator but are never applied
automatically.

Before restoring:

1. Declare the incident and record the target bundle SHA-256.
2. Verify the bundle with `backupctl verify` and confirm the key ID matches the
   key in use.
3. Confirm the current stack is the intended target and that no other operator
   is writing to it.
4. Keep the pre-incident bundle and logs; do not prune during a restore.
5. Confirm the expected recovery point and accept the data loss between it and
   the incident.

Run:

```powershell
$env:SPM_RESTORE_CONFIRM = 'YES'
$env:SPM_BACKUP_KEY_FILE = 'D:\secure\spm-backup.key'
.\scripts\restore.ps1 -BackupPath .\backups\spm-backup-20261124-010000.tar.enc
```

The script verifies the bundle first, unpacks it into `backups/.restore/`,
stops `api_backend` and `gitea` (and `nats` for a volume restore), restores every
`databases/*.dump` with `pg_restore --clean --if-exists`, optionally wipes and
extracts every `volumes/*.tar`, restarts the services and waits up to 120
seconds for `/health/ready`. It removes the extracted directory unless
`-KeepExtracted` is passed; that directory contains configuration and secrets,
so keep it only in a restricted location and delete it after use.

Use `-DatabaseOnly` when the volumes are intact and only PostgreSQL must be
recovered. For a legacy single-database `.dump`, pass the file as `-BackupPath`;
the script restores it into `POSTGRES_DB` (default `spm`) without touching
volumes.

After a restore:

1. Check `/health/ready`, the UI, Gitea repositories and attachments.
2. Compare row counts and the newest records with the expected recovery point.
3. Run the drill's schema checks against the restored databases.
4. Re-run `scripts/backup.ps1` to create a fresh verified bundle.
5. Record the restore duration, data-loss window and follow-up actions in the
   incident review.

## 10. Restore drill

The drill restores database dumps into throwaway `spm_drill_<timestamp>_<db>`
databases on the running PostgreSQL instance. It never stops services, never
writes to live databases or volumes and never applies volume archives. It
compares table counts,
`schema_migrations` counts, Gitea `version` presence and unvalidated foreign
keys with the live database, lists every volume archive, and writes JSON and
Markdown reports under `backups/drills/`. It updates the status file with
`verified` and `drill-ok`/`drill-failed`.

```powershell
$env:SPM_BACKUP_KEY_FILE = 'D:\secure\spm-backup.key'
.\scripts\restore-drill.ps1 -BundlePath .\backups\spm-backup-20261124-010000.tar.enc
```

The report records the drill ID, start/finish time, host, bundle SHA-256,
encryption and verification state, database restore seconds, total seconds,
per-database fingerprints and errors. Attach it to the quarterly operations
review and assign an owner to every error.

The drill does **not** prove a full-stack RTO. A complete drill must restore
into an isolated Compose project (separate project name, ports and volumes),
start every service, run the smoke check and time the recovery. Perform it at
least quarterly and after a material architecture change; record the measured
RTO, RPO and gaps. Until that evidence exists, the `OPS-01` RTO criterion stays
open.

## 11. RPO and RTO

| Objective | Target | Current evidence |
|---|---|---|
| RPO | ≤ 15 minutes | Not met. Base bundles only; the achievable RPO is the backup interval plus detection time. WAL/PITR is `OPS-03`. |
| RTO | ≤ 2 hours | Not demonstrated. Database restore time is measured by the drill; full-stack recovery is a manual isolated-stack drill. |

Recovery point and time depend on bundle size, disk throughput, the number of
volumes and whether the key and off-host copy are reachable. Measure them on the
production hardware; do not extrapolate from a laptop or a small staging
dataset.

## 12. Failure handling

| Symptom | Action |
|---|---|
| `Refusing to create an unencrypted backup` | Set `SPM_BACKUP_KEY_FILE` or pass `-KeyFile`; do not use `-AllowUnencrypted` outside local development |
| `postgres service is not running` | Start the stack and confirm `docker compose ps` is healthy |
| Volume does not exist | Start the stack once so Compose creates the volume; do not skip the volume |
| Off-host checksum mismatch | Treat the copy as corrupt, inspect the destination, retry, and keep the local bundle |
| `backupctl verify` fails | Do not delete the bundle; preserve it for investigation, take a new backup if the source is healthy |
| Restore refuses without `SPM_RESTORE_CONFIRM=YES` | Confirm the incident and target, then set it for the current shell only |
| Restore finishes but readiness fails | Keep services running, inspect API and PostgreSQL logs, and do not start a second restore over the same data |
| Status file is missing or unreadable | Check the API mount and permissions; the API reports `spm_backup_status_present 0` |

## 13. Security caveats

- Bundles contain repositories, attachments, database contents and, when
  encrypted, `.env`. Treat every bundle as production-confidential.
- Encryption protects data at rest and in transit only when the ciphertext is
  copied, not when an operator extracts it. Restrict the staging, restore and
  drill directories.
- The key must not be stored beside the bundle. Use the host secret store or a
  sealed envelope held by a different operator.
- Unencrypted bundles omit `.env` but still contain databases and volumes; they
  are local-development artifacts only.
- `backups/` is git-ignored. Never commit a bundle, sidecar, drill report,
  status file or extracted configuration.
- Delete extracted bundles after a restore or drill unless an incident requires
  forensic retention; if retained, protect them like the key.
- Off-host storage must enforce immutability or versioning where the threat
  model includes ransomware or a compromised operator account.
