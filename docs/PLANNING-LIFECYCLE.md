# Planning lifecycle: sprints and releases

This runbook describes the sprint and release lifecycles added by `PLN-03`.
The rules live in the API; the UI exposes the same transitions and never writes
a terminal status directly.

## Sprint lifecycle

```
planned ──start──▶ active ──close──▶ completed
   │                  │
   └────cancel────────┴──▶ cancelled
```

- **Create** (`POST /api/v1/projects/:id/sprints`) always creates a `planned`
  sprint. A goal and dates may be supplied, but are validated again at start.
- **Edit** (`PUT /api/v1/projects/:id/sprints/:sprintId`) changes metadata only.
  The only accepted status change is `planned|active → cancelled`.
- **Start** (`POST /api/v1/projects/:id/sprints/:sprintId/start`) requires a
  non-empty goal, a start date, an end date and `end >= start`. The project row
  is locked for the transaction and the new range is rejected with
  `409 SPRINT_OVERLAP` when an inclusive `daterange` overlaps another `active`
  sprint in the project.
- **Close preview** (`GET .../close-preview?policy=backlog|next_sprint`) lists
  the work that will move and the points/items that stay. `next_sprint_id` is
  required for the `next_sprint` policy and must identify a `planned` or
  `active` sprint in the same project.
- **Close** (`POST .../close`) performs the same validation, moves every
  non-terminal work item to the selected sprint or the backlog, records
  `close_policy`, `carry_over_count`, `completed_at` and `completed_by`, and
  writes an audit event. A start/close operation accepts `If-Match` or
  `expected_version`.

Terminal work item statuses are `done`, `closed`, `verified` and `cancelled`.
Terminal work stays attached to the closed sprint. `cancelled` work is not
carried over, but it is also never counted as completed in metrics, velocity or
the close preview.

### Metrics

`GET .../sprints/:sprintId/metrics?time_zone=Europe/Berlin` returns:

| Field | Definition |
| --- | --- |
| `committed_points` | Estimate points of every work item currently in the sprint. |
| `completed_points` | Estimate points of `done`, `closed` and `verified` items. |
| `remaining_points` | `committed_points - completed_points`. |
| `velocity_points` | Mean completed points of the latest three completed sprints, including this sprint when it is completed. `null` until at least one sprint has completed. |
| `velocity_sample_size` | Number of completed sprints in the mean (1–3). |
| `capacity_used_percent` | `completed_points / capacity * 100` when capacity is positive. |
| `days_total`, `days_elapsed`, `days_remaining` | Inclusive calendar-day counts. |

Sprint dates are PostgreSQL `DATE` values. "Today" is evaluated in the requested
IANA time zone (`time_zone`, default `UTC`); the binary embeds the IANA database
with `time/tzdata`, so Alpine images do not depend on the host zoneinfo package.

## Release lifecycle

```
planned ──start──▶ in_progress ──▶ released
   │                    │
   └───cancel───────────┴──▶ cancelled
```

`released` cannot be entered until the release has a recorded sign-off. The
release row keeps its human `version` string separate from the numeric
`revision` concurrency token.

### Freeze

- `POST .../releases/:releaseId/freeze` with a reason records `frozen_at`,
  `frozen_by` and `freeze_reason`; owner/maintainer only. A released or
  cancelled release cannot be frozen.
- `DELETE .../releases/:releaseId/freeze` reverses it. Both actions are
  audited.
- While a release is frozen, creating or updating a work item, feature or epic
  that changes its release scope returns `409 RELEASE_FROZEN` unless a matching
  row exists in `release_scope_exceptions`.
- An exception is either an exact `entity_id`, or type-wide with a `NULL`
  `entity_id`. Duplicate exceptions return `409 RELEASE_EXCEPTION_EXISTS`.
- Read exceptions with `GET .../releases/:releaseId/exceptions`; the UI records
  them from the release card.

### Sign-off and release

1. `POST .../releases/:releaseId/sign-off` with at least 10 characters of
   evidence evaluates the same readiness gates as the reports view.
2. A failing gate returns `409 RELEASE_NOT_READY` and the gate payload. A
   released or cancelled release cannot be signed off.
3. The response stores `signed_off_at`, `signed_off_by` and the evidence. The
   audit event and activity feed store the evidence SHA-256, not the evidence
   text.
4. Move `in_progress → released` with `PUT .../releases/:releaseId` and a
   revision token; the server rejects the transition if `signed_off_at` is
   empty.

## API surface

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/projects/:id/sprints` | Create a planned sprint |
| `PUT` | `/projects/:id/sprints/:sprintId` | Edit metadata or cancel |
| `POST` | `/projects/:id/sprints/:sprintId/start` | Start a planned sprint |
| `GET` | `/projects/:id/sprints/:sprintId/metrics` | Commit/completion/velocity metrics |
| `GET` | `/projects/:id/sprints/:sprintId/close-preview` | Preview carry-over |
| `POST` | `/projects/:id/sprints/:sprintId/close` | Close and carry over |
| `POST` | `/projects/:id/releases` | Create a planned/in-progress release |
| `PUT` | `/projects/:id/releases/:releaseId` | Edit or change lifecycle state |
| `POST` | `/projects/:id/releases/:releaseId/freeze` | Freeze scope |
| `DELETE` | `/projects/:id/releases/:releaseId/freeze` | Unfreeze scope |
| `GET` | `/projects/:id/releases/:releaseId/exceptions` | List scope exceptions |
| `POST` | `/projects/:id/releases/:releaseId/exceptions` | Record a scope exception |
| `POST` | `/projects/:id/releases/:releaseId/sign-off` | Record readiness sign-off |

Lifecycle sub-resources also accept `If-Match` (or body `expected_version`) on
start, close, freeze, unfreeze and sign-off. Missing/stale/malformed tokens use
the standard `428 REVISION_REQUIRED`, `409 REVISION_CONFLICT` and
`400 INVALID_REVISION` responses. Create and mutate responses include the new
revision and emit an `ETag`.

## Errors

| Code | Meaning |
| --- | --- |
| `SPRINT_USE_LIFECYCLE` | Status was changed outside the start/close/cancel endpoints. |
| `SPRINT_NOT_PLANNED` / `SPRINT_NOT_ACTIVE` | Lifecycle action used from the wrong state. |
| `SPRINT_GOAL_REQUIRED` / `SPRINT_DATES_REQUIRED` | Start validation failed. |
| `SPRINT_OVERLAP` | Another active sprint uses an overlapping inclusive date range. |
| `SPRINT_NOT_OPEN` | Carry-over target is not planned or active. |
| `RELEASE_USE_LIFECYCLE` | Release was created with an invalid initial status. |
| `RELEASE_STATE` | Lifecycle action used from the wrong release state. |
| `RELEASE_SIGN_OFF_REQUIRED` | `released` was requested before sign-off. |
| `RELEASE_NOT_READY` | Sign-off readiness gates failed. |
| `RELEASE_FROZEN` | Scope change blocked by a freeze without an exception. |
| `RELEASE_ALREADY_FROZEN` / `RELEASE_NOT_FROZEN` | Freeze state mismatch. |
| `RELEASE_EXCEPTION_EXISTS` | Duplicate exact or type-wide exception. |

## Verification

```powershell
cd api
go test ./... -count=1
go vet ./...
go run ./cmd/openapi -check

cd ..\ui
npm run typecheck
npm test
npm run build
```

Migration `018_sprint_release_lifecycle.sql` is forward-only and additive. Its
contract companion in `api/internal/migrate/rollback/` is reviewed but is not
embedded or applied during a routine rollback; it is reserved for a planned
removal after every reader of the new columns is gone.
