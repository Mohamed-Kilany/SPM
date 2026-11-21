# Test plans, suites and run snapshots

Test management has four layers:

```text
Test plan (cycle, dates, owner)
└── Test suite (ordered selection, assignee, archive)
    └── Test suite case (ordered membership)
Test run ── snapshots plan_id, suite_id, plan_name, suite_name
└── Test result ── snapshots test_cases.revision in case_revision
```

A **test case** remains the reusable definition. A **plan** schedules a cycle, a
**suite** keeps an ordered selection inside that plan, and a **run** executes
whatever its scope resolved to. Runs copy the plan and suite names and each
case's revision, so renaming or archiving later never rewrites history.

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

Each result also creates `not_run` step results for the steps that existed at
run creation. Later edits to a test case do not rewrite an existing run.

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

## Evidence

- `api/internal/migrate/sql/019_test_plans_suites.sql` and its rollback
  companion define the schema and revision triggers.
- `api/internal/migrate/schema_test.go` enforces the latest migration, indexes
  and rollback completeness.
- `api/internal/workspace/handler_test_plans_test.go` covers plan transitions,
  date parsing and case deduplication.
- `ui/e2e/linked-sdlc.spec.ts` creates a plan, creates a suite, adds a case by
  CSV, runs the suite and asserts the snapshot context in the run detail.
