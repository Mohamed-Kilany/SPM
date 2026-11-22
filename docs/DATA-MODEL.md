# Data model

SPM keeps a normalized, project-scoped PostgreSQL schema. The schema is owned by
the embedded migrations in `api/internal/migrate/sql/`; this document describes
the invariants that the API, the UI and future migrations must preserve.

## 1. Scope and source of truth

| Layer | Location |
|---|---|
| Forward migrations | `api/internal/migrate/sql/NNN_*.sql` |
| Contract / rollback companions | `api/internal/migrate/rollback/NNN_*.sql` |
| Expand-only template | `api/internal/migrate/migration_template.sql` |
| Migration runner | `api/internal/migrate/migrate.go` |
| Schema invariants | `api/internal/migrate/schema_test.go` |

Migrations are applied by the API at startup in lexical order and recorded in
`schema_migrations`. Each migration runs inside its own transaction. The
`000_create_gitea_db.sql` bootstrap is psql-only and is skipped by the runner.

`schema_test.go` fails the build when:

- a migration version is duplicated or out of order;
- the latest migration is not the expected one;
- a revisioned planning table is missing its column or trigger;
- a rollback companion misses an object created by its forward migration;
- a rollback script is embedded in the running API;
- a foreign key has no index whose leading column is the referencing column.

## 2. Entity hierarchy

```text
Project
├── Project members ── Users
├── Project label
├── Custom field
│   └── Custom field value ── (work item, reserved for test case)
├── Saved filter
├── PRD
│   └── Epic ── (optional PRD)
│       └── Feature ── (required Epic)
│           ├── Work item ── (optional Feature, self-referencing parent)
│           └── Test case ── (optional Feature)
│               ├── Test step ── (optional shared step template)
│               └── Test case version
│                   └── Test case version step
├── Test shared step ── (project-scoped template)
├── Test plan
│   └── Test suite
│       └── Test suite case ── Test case
├── Sprint
├── Release
│   └── Release scope exception
├── Environment
├── Git repository
│   ├── Branch
│   ├── Commit
│   └── Pull request
│       └── Pull request review
├── Build
├── Deployment ── Environment
│   ├── Deployment approval
│   └── Health check
├── Test run ── Environment, Release, Sprint, Build
│   └── Test result ── Test case
│       └── Test step result ── (optional Test step)
├── Artifact link
├── Activity event
├── Comment
└── Attachment
```

A user is global. A user sees a project only through `project_members`, which
carries the project role (`owner`, `maintainer`, `developer`, `tester`, `viewer`).
Global roles (`admin`, `member`, `viewer`) are separate from project roles.

## 3. Delete and cascade rules

There are no soft-deleted rows. Deletes are hard deletes; the schema decides
whether a dependent row disappears with its parent (`CASCADE`) or is kept and
unlinked (`SET NULL`). A custom field is not soft-deleted: archiving sets
`archived_at`, which hides the definition while its values stay queryable, and
only an explicit purge deletes rows.

| Relationship | Action | Meaning |
|---|---|---|
| `projects` → project-scoped tables | `CASCADE` | Deleting a project removes all of its planning, delivery, test and collaboration rows. |
| `custom_fields` → `custom_field_values` | `CASCADE` | Purging a definition removes its stored values; archiving keeps them. |
| `epics` → `features` | `CASCADE` | A feature cannot exist without its epic. |
| `test_cases` → `test_steps` | `CASCADE` | Steps are owned by their case. |
| `test_cases` → `test_case_versions` | `CASCADE` | Version history is owned by its case. |
| `test_case_versions` → `test_case_version_steps` | `CASCADE` | Snapshot steps are owned by their version. |
| `test_shared_steps` → `test_steps` | `SET NULL` | Deleting a template keeps the copied text and only drops provenance. |
| `test_plans` → `test_suites` | `CASCADE` | Suites are owned by their plan. |
| `test_suites` → `test_suite_cases` | `CASCADE` | Membership belongs to its suite. |
| `test_cases` → `test_suite_cases` | `CASCADE` | Deleting a case removes its suite membership. |
| `test_runs` → `test_results` | `CASCADE` | Results are owned by their run. |
| `test_plans`/`test_suites` → `test_runs` | `SET NULL` | Historical runs keep `plan_name`/`suite_name` snapshots. |
| `test_results` → `test_step_results` | `CASCADE` | Step results are owned by their result. |
| `test_steps` → `test_step_results` | `SET NULL` | Step results keep the text they snapshotted when the live step is replaced. |
| `git_repositories` → branches, commits, pull requests | `CASCADE` | Repository history is removed with the repository. |
| `pull_requests` → reviews | `CASCADE` | Reviews are owned by their pull request. |
| `deployments` → approvals, health checks | `CASCADE` | Evidence is owned by its deployment. |
| `prds` → `epics` | `SET NULL` | The epic remains in the project but loses its PRD link. |
| `features` → work items, test cases | `SET NULL` | Delivery and test artifacts survive feature removal and become explicitly unlinked. |
| `work_items` → child work items | `SET NULL` | A child is promoted to a root item instead of being deleted. |
| `releases` → `release_scope_exceptions` | `CASCADE` | Freeze exceptions disappear with their release. |
| users → ownership, authorship, actor columns | `SET NULL` | History is preserved without the user reference. |
| users → `project_members` | `CASCADE` | Membership ends when the user is deleted. |

`SET NULL` is deliberate, not an orphan: the column is nullable, the API treats
the missing link as "not linked", and nothing in the UI assumes a target still
exists. New tables must follow the same rule: `NOT NULL` parents cascade,
optional references set null.

`project_labels` is a registry, not the source of the work item label value:
`work_items.labels` stays the rendered array. Renaming a label rewrites every
matching array entry in the same transaction; deleting a label strips the name
from every work item before removing the registry row. This keeps label display
working even if the registry row is gone.

`test_case_versions` and `test_case_version_steps` are immutable: the API only
inserts them, never updates them, and reading history is a plain query. A live
case can therefore be rewritten or deleted-safe (cascading its own history)
without ever mutating a snapshot that a run already recorded.

`work_items.parent_id` additionally has a `CHECK (parent_id IS NULL OR parent_id
<> id)` constraint (`work_items_no_self_parent`) so a direct write cannot create
a self-cycle. Deeper cycles are rejected by the application hierarchy validation.

## 4. Optimistic concurrency

Twelve project-scoped tables carry an internal concurrency token:

`prds`, `epics`, `features`, `work_items`, `test_plans`, `test_suites`,
`test_cases`, `test_runs`, `test_shared_steps`, `sprints`, `releases`,
`saved_filters`.

```sql
revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0)
```

`revision` is **not** the human-facing version. `prds.version` and
`releases.version` are editable display strings (`"1.0"`, `"2026.11"`). The
numeric `revision` exists only to detect stale writes.

A shared `BEFORE UPDATE` trigger (`planning_bump_revision()`) sets
`NEW.revision := OLD.revision + 1`. The trigger is the only writer of the
column: a client-supplied value is overwritten, including in internal updates
and webhook-driven refreshes. `planning_bump_revision()` is revoked from
`PUBLIC`.

The API compares the token inside the mutation predicate:

```sql
UPDATE prds SET ... WHERE id = $1 AND project_id = $2
  AND ($3::bigint = 0 OR revision = $3::bigint)
RETURNING revision;
```

`0` is the sentinel for `If-Match: *`. A missing token is `428
REVISION_REQUIRED`, a stale token is `409 REVISION_CONFLICT` with the current
`revision` in `current_version` and in the `ETag`, and a malformed token is
`400 INVALID_REVISION`. Create and update responses return
`{"id": ..., "revision": N}`; deletes return
`{"id": ..., "revision": N, "deleted": true}`.

Test runs are revisioned but have no update endpoint. Their revision is returned
on create and bumped by the internal status refresh; result and step patches do
not require a token. Test suites are scoped through `test_plans.project_id`
rather than a project column of their own; their revision token is checked
inside the update transaction. Every other revisioned table enforces a token on
`PUT`, `PATCH` (work-item status) and `DELETE`.

Test runs also snapshot `plan_id`, `suite_id`, `plan_name` and `suite_name`, and
each `test_results` row stores the `case_revision` it executed against. Step
results snapshot their `position`, `action`, `expected_result`, `test_data` and
`case_revision`, so replacing a case's steps leaves completed runs readable.
These snapshots are append-only history: renaming or archiving a plan, suite or
case never rewrites a completed run. See
[`TEST-MANAGEMENT.md`](TEST-MANAGEMENT.md).

The HTTP contract is generated into [`openapi.yaml`](openapi.yaml); the
concurrency rules are summarized in [`API.md`](API.md). Each rejected stale write
increments `spm_planning_conflicts_total{resource}` with a bounded resource
label (`prd`, `epic`, `feature`, `work item`, `test case`, `sprint`, `release`,
`saved filter`).

## 5. Foreign keys and indexes

PostgreSQL indexes the referenced side of a foreign key (the primary key or
unique constraint) but never the referencing side. Every foreign-key column in
this schema therefore has an index whose **leading** column is that column:

- deletion of a referenced row can find dependents without a sequential scan;
- `ON DELETE SET NULL` and `ON DELETE CASCADE` stay predictable as tables grow;
- traceability joins start from an indexed path.

The invariant is tested, not documented by convention:
`TestEveryForeignKeyHasALeadingIndex` parses all embedded migrations and fails if
a foreign key lacks a leading index. Migration `016_planning_revisions` closed
the gaps (for example `project_members.user_id`, `work_items.reporter_id`,
`test_results.bug_work_item_id` and the deployment provenance columns).
Migration `021_labels_custom_fields_saved_filters` follows the same rule for
`project_labels`, `custom_fields`, `custom_field_values` and `saved_filters`
(`project_labels_project_idx`, `custom_fields_project_idx`,
`custom_field_values_entity_idx`, `saved_filters_owner_idx` and their peers).

List queries use project-scoped composite indexes, for example
`prds (project_id, created_at DESC)`, `work_items (project_id, updated_at DESC)`
and `deployments (project_id, environment_id, created_at DESC)`. New list
queries must reuse one of these indexes or add a new one in an expand-only
migration.

## 6. Uniqueness and identity

- Entity primary keys are UUID v7 (`uuidv7()`), time-ordered and safe to expose.
- Work items also carry a per-project `item_number` rendered as
  `<project key>-<number>`.
- Email addresses and usernames are unique.
- Project membership is unique on `(project_id, user_id)`.
- `artifact_links` is unique on the link tuple, so linking the same artifacts
  twice is idempotent.
- `test_case_versions` is unique on `(test_case_id, revision)` and
  `test_case_version_steps` on `(version_id, position)`; both are append-only and
  have no update or delete endpoint.
- `test_shared_steps` is unique on `(project_id, name)`, so templates stay
  addressable by name within a project.
- `project_labels` is unique on `(project_id, name)`, so a label name has one
  color token and one rename history.
- `custom_fields` is unique on `(project_id, key)`, so a stable key addresses
  one definition.
- `custom_field_values` is unique on `(field_id, entity_type, entity_id)`, so a
  work item has at most one value per field; archiving a definition keeps its
  rows, purging deletes them.
- `saved_filters_default_idx` is a partial unique index on
  `(project_id, owner_id, scope) WHERE is_default`, so each owner has at most
  one default view per surface.
- `saved_filters.swimlane` is constrained to `none`, `assignee`, `priority` or
  `type` (migration `022`); `columns` remains a JSON array of visible card
  fields for board views.
- `schema_migrations.version` is unique; a migration can never be applied twice.

## 7. Project settings documents

`projects.settings` is a JSON document rather than a set of columns because its
shape is policy, not identity. Two rules keep it safe:

- the API decodes it through `decodeSettings`, which merges a partial document
  onto `DefaultProjectSettings`, so a missing key never means "zero".
- `UpdateSettings` replaces the whole document. Clients must send every
  section they want to keep; the UI re-sends `merge_policy`, `board` and the
  delivery fields together.

The board policy is stored at `settings.board`:

```json
{
  "board": {
    "wip_limits": { "in_progress": 4, "review": 3 },
    "wip_hard_block": false
  }
}
```

`wip_limits` is keyed by board column (`backlog`, `ready`, `in_progress`,
`review`, `qa`, `done`, `blocked`), validated to the range 0–999 and normalized
so zero limits are removed. `wip_hard_block` is opt-in. Migration `022` adds the
view-side counterpart, `saved_filters.swimlane`; no column is added to
`projects` for the policy itself. See [`BOARD-VIEWS.md`](BOARD-VIEWS.md).

## 8. Changing the model

See [`MIGRATION-GUIDE.md`](MIGRATION-GUIDE.md). In short: copy
`migration_template.sql`, keep the forward migration additive and idempotent, add
the rollback companion under `migrate/rollback/`, extend `schema_test.go` when a
new invariant is introduced, and rehearse forward/backward semantics before the
PR. Destructive SQL never ships in the same change as the code that stops using
the old shape.
