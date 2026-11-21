# Test plans, suites, case versions and run snapshots

Test management has these layers:

```text
Test plan (cycle, dates, owner)
└── Test suite (ordered selection, assignee, archive)
    └── Test suite case (ordered membership)
Test run ── snapshots plan_id, suite_id, plan_name, suite_name
└── Test result ── snapshots test_cases.revision in case_revision
    └── Test step result ── snapshots step text, position and revision
```

A **test case** remains the reusable definition. A **plan** schedules a cycle, a
**suite** keeps an ordered selection inside that plan, and a **run** executes
whatever its scope resolved to. Runs copy the plan and suite names and each
case's revision, so renaming or archiving later never rewrites history.

Every case create and edit also writes an immutable **case version**: the same
case fields plus its steps exactly as they were at that revision. Run results
reference the revision they executed, and their step results snapshot the step
text, so a later rewrite of the case cannot change what a completed run shows.

## Case versions

`POST` and `PUT` on a test case insert one row into `test_case_versions` and its
`test_case_version_steps` in the same transaction as the case write. The snapshot
records:

- the case fields (`title`, `description`, `preconditions`, `type`, `priority`,
  `status`, `automation_ref`, `owner_id`, `tags`);
- `feature_title`, so history survives a feature rename;
- `changed_by`/`changed_by_name`, so the author is readable even after a rename;
- every step's `position`, `action`, `expected_result` and `test_data`.

`GET /projects/{id}/test-cases/{caseId}/versions` returns the newest revision
first, including each version's steps, plus `total`, `before` and `limit` paging.
Versions are never updated or deleted through the API; deleting the case
cascades its history. Migration `020_test_case_versions` backfilled one snapshot
for every existing case at its current revision, so history starts complete.

## Shared step templates

`test_shared_steps` is a project-scoped library of reusable steps (`name`,
`action`, `expected_result`, `test_data`). Inserting a template into a case
**copies** the text and records `test_steps.shared_step_id` as provenance.

Shared steps are revisioned: create returns the token, update and delete require
`If-Match`/`expected_version`, and the same `428`/`409`/`400` rules apply. Names
are unique per project. Editing or deleting a template never rewrites a case that
already used it: deletion nulls `shared_step_id` and keeps the copied text.

## JSON export and import

`GET /projects/{id}/test-cases/export` returns the portable document
`spm.test-cases.v1`:

```json
{
  "format": "spm.test-cases.v1",
  "exported_at": "2026-11-21T10:00:00Z",
  "project_id": "…",
  "cases": [{ "title": "…", "steps": [{ "action": "…", "expected_result": "…" }] }]
}
```

`POST /projects/{id}/test-cases/import` accepts the same shape. The document is
validated completely before the first insert and applied in one transaction, so
a single invalid case rejects the whole import. A case without `feature_id` is
resolved by case-insensitive `feature_title` within the project; an unknown
title is a `422`. Each imported case gets a version snapshot and the response
lists `id`, `title` and `revision` per case.

## Step ordering

The step editor supports drag-free ordering: move a step up or down with the
arrow buttons or with `Alt+↑` / `Alt+↓` while the step is focused. Step
positions are rewritten as a contiguous 1-based sequence in the same update, and
the new case version snapshots the new order.

## Lifecycle

Plans start as `draft`. They can move to `active`, back to `draft`, or to
`archived`. `archived` is terminal: an archived plan cannot be reactivated and
cannot accept new suites, membership changes or runs. Plans are never hard
deleted through the API; historical runs keep their snapshot. This is the
"explicit confirmation" substitute from `TST-01`: deletion is not offered at
all.

Suites are ordered by `position` inside a plan. A suite can be archived and
restored. An archived suite rejects membership changes and new runs, but remains
readable in history.

## API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/projects/{id}/test-plans` | Create a plan (`draft` by default) |
| `PUT` | `/projects/{id}/test-plans/{planId}` | Update name, description, owner, dates or status |
| `PUT` | `/projects/{id}/test-plans/{planId}/suite-order` | Rewrite the order of every active suite |
| `POST` | `/projects/{id}/test-plans/{planId}/suites` | Create a suite |
| `PUT` | `/projects/{id}/test-plans/{planId}/suites/{suiteId}` | Update a suite or archive/restore it |
| `POST` | `/projects/{id}/test-plans/{planId}/suites/{suiteId}/cases` | Add cases by IDs, filter or CSV |
| `DELETE` | `/projects/{id}/test-plans/{planId}/suites/{suiteId}/cases/{caseId}` | Remove one membership row |
| `POST` | `/projects/{id}/test-cases` | Create a case and its first version snapshot |
| `PUT` | `/projects/{id}/test-cases/{caseId}` | Edit a case and snapshot the new revision |
| `DELETE` | `/projects/{id}/test-cases/{caseId}` | Delete a case, its steps and its history |
| `GET` | `/projects/{id}/test-cases/{caseId}/versions` | List case versions, newest first |
| `GET` | `/projects/{id}/test-cases/export` | Export every case as `spm.test-cases.v1` JSON |
| `POST` | `/projects/{id}/test-cases/import` | Import cases all-or-nothing |
| `POST` | `/projects/{id}/test-shared-steps` | Create a shared step template |
| `PUT` | `/projects/{id}/test-shared-steps/{sharedStepId}` | Update a template |
| `DELETE` | `/projects/{id}/test-shared-steps/{sharedStepId}` | Delete a template; copies keep their text |
| `POST` | `/projects/{id}/test-runs` | Create a run from IDs, filter, suite, plan or every case |
| `POST` | `/projects/{id}/test-runs/{runId}/finish` | Finish a run and return its summary |
| `PATCH` | `/projects/{id}/test-runs/{runId}/results/{resultId}` | Record a case-level result |
| `PATCH` | `/projects/{id}/test-runs/{runId}/results/{resultId}/steps/{stepResultId}` | Record a step result |
| `POST` | `/projects/{id}/test-runs/{runId}/results/{resultId}/steps/{stepResultId}/evidence` | Upload scanned step evidence |
| `POST` | `/projects/{id}/test-runs/{runId}/results/{resultId}/bug` | Create a linked bug from a failure |

Every mutation returns the standard revision envelope and ETag. Updates,
reorders and membership changes require `If-Match`, `?expected_version=` or an
`expected_version` body field:

- missing token → `428 REVISION_REQUIRED`;
- stale token → `409 REVISION_CONFLICT` with `current_version` and ETag;
- malformed token → `400 INVALID_REVISION`.

A run created from an archived plan returns `409 PLAN_ARCHIVED`; from an
archived suite it returns `409 TEST_SUITE_ARCHIVED`. Mutating an archived plan
returns `409 PLAN_STATE` and an archived suite returns `409 TEST_SUITE_ARCHIVED`.

## Membership resolution

`POST .../cases` accepts any combination of:

- `test_case_ids`: explicit IDs, all of which must belong to the project;
- `filter`: `feature_id`, `type`, `priority`, `status`; supplied fields are
  combined with `AND`;
- `csv`: one exact title per line. A leading `title`, `case` or `name` header is
  ignored, matching is case-insensitive, and the first created case wins a
  duplicate title.

The request is all-or-nothing. An unknown CSV title or an ID from another
project rejects the whole request. Every selector is merged, deduplicated and
appended in title order. Cases already in the suite are skipped and reported as
`skipped`; the response also returns `added`, `requested` and the new revision.

Reordering requires the **complete** active suite set. A partial list is a `422`
so a stale cart cannot silently drop a suite.

## Run scope

Run creation resolves cases in this precedence order:

1. `test_case_ids` — explicit selection;
2. `filter` — same filter object as suite membership;
3. `suite_id` — the suite's ordered membership;
4. `plan_id` — the union of cases in the plan's active suites;
5. no selection — every test case in the project.

`suite_id` must belong to `plan_id` when both are supplied. The run stores:

| Column | Meaning |
|---|---|
| `plan_id` / `suite_id` | Snapshot links; `ON DELETE SET NULL` so history survives plan removal |
| `plan_name` / `suite_name` | Names at run creation, unaffected by later renames |
| `test_results.case_revision` | `test_cases.revision` the result executed against |

Each result also creates `not_run` step results for every step in the case
revision it executes, copying `position`, `action`, `expected_result` and
`test_data` from `test_case_version_steps`. `test_step_id` links the current live
step when its position still matches and is nullable otherwise
(`ON DELETE SET NULL`), so editing the case later never rewrites an existing run.

| Column | Meaning |
|---|---|
| `test_step_results.case_revision` | Revision the step was executed against |
| `test_step_results.position` / `action` / `expected_result` / `test_data` | Snapshot of the version's step |
| `test_step_results.test_step_id` | Optional link to the live step, by position |

## Focused runner

`TestRunRunner.vue` executes one run in a dedicated modal, one step at a time.
The left list shows every step grouped by case with a status dot; the current
panel renders the snapshotted action, expected result and test data so later
case edits never change what the tester sees.

- **Status shortcuts:** `P` passed, `F` failed, `B` blocked, `S` skipped. The
  buttons carry the same hints. `←`/`→` (and `↑`/`↓`) move between steps,
  `G` raises a linked bug from the current failed step and `E` opens the
  evidence picker. Shortcuts are ignored while focus is in an input, textarea
  or select.
- **Autosave:** step notes are saved 700 ms after the last keystroke. Status
  changes save immediately. Closing or finishing flushes pending drafts, so a
  status click never waits for the note debounce.
- **Resume:** the current step, note drafts and evidence URLs are stored under
  `spm.runner.<projectId>.<runId>`. Reopening a run restores the draft and
  continues at the last step; finishing a run clears the key. Corrupt storage
  is ignored rather than blocking execution.
- **Deep link:** `?tab=tests&run=<runId>&focus=1` opens the focused runner;
  without `focus=1` the read-only run detail opens.
- **Bug shortcut:** a failed step shows "Raise linked bug" (or `G`), prefilled
  with the case title, position, expected and actual results. TST-04 extends
  this into full defect traceability.

### Step evidence

`POST .../steps/{stepResultId}/evidence` is multipart with one `file` part.
Evidence reuses the work item attachment pipeline: extension allow-list,
magic-byte validation, active-markup rejection, EICAR detection, the configured
`ATTACHMENT_MAX_BYTES` limit (10 MiB by default) and ClamAV when
`CLAMAV_ADDR` is set. The attachment row uses
`entity_type='test_step_result'` and `entity_id=<stepResultId>`; the step's
`evidence_url` is set to the project-scoped download URL. Downloads require
`scan_status='clean'`. Replacing a file points the step at the new attachment;
the previous file stays addressable by ID for audit purposes.

## Finishing and pass rate

`POST .../test-runs/{runId}/finish` is idempotent:

- An in-progress run with `not_run` cases returns `409 RUN_INCOMPLETE` unless
  the body is `{"force": true}`. The UI opens a confirmation instead of
  finishing silently.
- A forced finish leaves `not_run` counts in the summary and records the
  outcome as `incomplete`.
- Otherwise the outcome is `failed` when any case failed and `passed` when all
  remaining cases passed, were blocked or were skipped.
- Repeating finish on a completed run returns the same counts with
  `already_completed: true`; it never double counts the metric.

Automatic completion still happens when the last case receives a result, so
finish is primarily the explicit boundary and summary view. Runs snapshot the
case revision, so a completed run's summary is stable.

`spm_test_runs_completed_total{outcome}` is incremented exactly once per
transition to `completed`, with `outcome` limited to `passed`, `failed` or
`incomplete`. The pass rate is:

```text
passed / (passed + failed)
```

Blocked, skipped and `not_run` cases are excluded from the denominator and
reported separately; a forced incomplete run is visible through its
`incomplete` counter rather than being hidden in the pass rate.

## Assignment and validation

- Plan owners and suite assignees must be project members (`422
  VALIDATION_ERROR`).
- Plan names and suite names are required, trimmed and limited to 255
  characters.
- `end_date` must not be before `start_date`.
- A new plan cannot start as `archived`.
- Filter enums match the case enums: `manual|automated`,
  `low|medium|high|critical`, `draft|ready|deprecated`.

## UI

The Test lab exposes a plan board above the case/run lists. It provides plan
creation and activation/archive, suite creation, up/down ordering, filter or CSV
case ingestion, membership removal and "Run suite". The run dialog exposes all
five scopes and shows the resolved case count before starting. Actions that the
API would reject (archived plan or suite) are hidden or disabled.

The case editor adds step reorder buttons and `Alt+↑`/`Alt+↓`, shared-template
insertion and a template manager, a per-case version history modal, and
Export/Import JSON buttons. Run step results render from their snapshot columns
first, falling back to live steps only when `test_step_id` is still linked.

## Evidence

- `api/internal/migrate/sql/019_test_plans_suites.sql` and its rollback
  companion define the plan/suite schema and revision triggers.
- `api/internal/migrate/sql/020_test_case_versions.sql` and its rollback
  companion define versions, shared steps and step-result snapshots, including
  the backfill of existing cases and the rollback treatment of snapshot-only
  step results.
- `api/internal/migrate/schema_test.go` enforces the latest migration, indexes
  and rollback completeness.
- `api/internal/workspace/handler_test_plans_test.go` covers plan transitions,
  date parsing and case deduplication.
- `api/internal/workspace/handler_test_versions_test.go` covers request
  normalization for versioned case and shared-step writes.
- `ui/e2e/linked-sdlc.spec.ts` creates a plan, creates a suite, adds a case by
  CSV, runs the suite and asserts the snapshot context in the run detail.
