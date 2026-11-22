# Labels, custom fields and saved filters

PLN-05 adds three project-scoped customization primitives without changing the
shape of `work_items`:

- reusable, colored labels;
- typed custom fields backed by a side table;
- named saved filters for planning, board and test views.

The HTTP operations are part of the generated [`openapi.yaml`](openapi.yaml)
contract. The schema is migration `021_labels_custom_fields_saved_filters` with
the contract companion in `api/internal/migrate/rollback/`.

## 1. Labels

Labels remain stored on the work item in `work_items.labels text[]`; the
`project_labels` table is the reusable registry that adds a color token and a
single rename/delete source of truth.

| Rule | Behavior |
|---|---|
| Normalization | Lowercase, trim, collapse whitespace; letters, digits, spaces, hyphens and underscores only; 1–24 characters. |
| Work item limit | 10 labels per item. |
| Colors | `slate`, `blue`, `green`, `amber`, `red`, `purple`, `pink`, `teal`. |
| Automatic registration | Creating or updating a work item registers any unknown label with color `slate`. Invalid or legacy labels are skipped without failing the work item write. |
| Rename | `PUT /projects/{id}/labels/{labelId}` updates the registry and rewrites every `work_items.labels` usage in the same transaction. |
| Delete | `DELETE /projects/{id}/labels/{labelId}` removes the registry row and strips the name from every work item; other labels are untouched. |
| Duplicate name | `409 LABEL_EXISTS`. |
| Permissions | Read: `project.read`. Create, rename and delete: `planning.write`. |

Endpoints:

| Method | Path |
|---|---|
| `GET` | `/api/v1/projects/{id}/labels` |
| `POST` | `/api/v1/projects/{id}/labels` |
| `PUT` | `/api/v1/projects/{id}/labels/{labelId}` |
| `DELETE` | `/api/v1/projects/{id}/labels/{labelId}` |

The workspace payload includes `labels`, so the board can color and filter
existing labels without an extra request. The UI accepts comma-separated
labels, suggests registered names through a datalist and renders color chips on
cards and in the item dialog.

## 2. Custom fields

### Definitions

`custom_fields` defines a typed attribute. The definition is project-scoped and
uses a stable key, so a rename never invalidates stored values.

| Type | Value column | Validation |
|---|---|---|
| `text` | `value_text` | Trimmed; at most 2,000 characters. |
| `number` | `value_number` | Finite JSON number; optional `config.min` and `config.max`, inclusive. |
| `select` | `value_text` | Must equal a configured option value; at least one option, at most 50. |
| `date` | `value_date` | `YYYY-MM-DD`. |
| `user` | `value_user_id` | Must reference a current project member. |

- `key` is lowercase, starts with a letter and uses letters, digits or
  underscores (maximum 40). When omitted it is derived from the name.
- `options` are `{value, label, color?}`. Values match
  `^[a-z0-9][a-z0-9_-]{0,39}$`; labels default to the value.
- `config` is only meaningful for `number`; other types store `{}`.
- `required` fields must be present and non-empty on every values write.
- `position` controls display order; `archived_at` hides a field without
  deleting its values.
- A field whose type would change while values exist returns
  `409 FIELD_TYPE_LOCKED`. Archive it and create a new field instead.
- Duplicate keys return `409 FIELD_EXISTS`.

Permission matrix:

| Action | Capability |
|---|---|
| List fields and read values | `project.read` |
| Create, update, archive or restore a definition | `planning.write` |
| Write work item values | `work_item.write`, except testers |
| Purge a definition and its values | `settings.write` and `?purge=true` |

### Values

Values live in `custom_field_values`, one row per populated value:

```sql
UNIQUE (field_id, entity_type, entity_id)
```

`entity_type` is currently `work_item`; the check also reserves `test_case` for
future use. A missing row means "not set", including for required fields.
Because values are rows, adding a field or a value never alters an existing
table.

`PUT /projects/{id}/work-items/{itemId}/custom-fields` replaces the full set:

```json
{
  "values": {
    "0f0b8f18-0d1b-4f3d-a5ea-2f5a1f2d4c11": 8,
    "8a2f1130-8a4f-4f4b-a8b0-5f2b8c0d1e22": "high"
  }
}
```

- Omitted or empty values are cleared.
- Archived fields are read-only and their existing values are preserved.
- Unknown or foreign field IDs return `422 INVALID_FIELD`.
- Type or option errors return `422 INVALID_VALUE`; a missing required field
  returns `422 REQUIRED_FIELD`.
- The response is the resulting `CustomFieldValue[]`.
- The update records `work_item.custom_fields.update` and emits the normal
  `workspace.changed` activity event.

Endpoints:

| Method | Path |
|---|---|
| `GET` | `/api/v1/projects/{id}/custom-fields` (`?include_archived=true` optional) |
| `POST` | `/api/v1/projects/{id}/custom-fields` |
| `PUT` | `/api/v1/projects/{id}/custom-fields/{fieldId}` |
| `DELETE` | `/api/v1/projects/{id}/custom-fields/{fieldId}` (archive; `?purge=true` destroys) |
| `GET` | `/api/v1/projects/{id}/work-items/{itemId}/custom-fields` |
| `PUT` | `/api/v1/projects/{id}/work-items/{itemId}/custom-fields` |

Deleting a definition archives it by default. Archiving keeps every stored
value and allows a later restore. Purging is explicit, requires
`settings.write`, deletes the values and then the definition, and is recorded
as `custom_field.purge`. The UI separates Archive, Restore and Purge so data
loss is never a side effect of removing a field from the board.

## 3. Saved filters

A saved filter stores the JSON filter payload, optional layout metadata, pinning
and a per-owner default flag. Board views additionally capture swimlane, sort
and visible card fields; the full layout guide is
[`BOARD-VIEWS.md`](BOARD-VIEWS.md).

| Field | Values |
|---|---|
| `scope` | `planning`, `board`, `test` |
| `visibility` | `private` (default) or `shared` |
| `filters` | Free-form JSON; the board stores `search`, `type`, `feature`, `sprint`, `label`, `custom` keyed by field key and a `quick` array. |
| `sort` | `{"field": ..., "direction": "asc"\|"desc"}` for board views. |
| `columns` | Up to 20 names, each 1–40 characters. For board views these are the visible card fields. |
| `swimlane` | `none` (default), `assignee`, `priority` or `type`; migration `022`. |
| `pinned` | Shared/private ordering hint. |
| `is_default` | At most one default per owner and scope. |

`is_default` is enforced by a partial unique index:

```sql
CREATE UNIQUE INDEX saved_filters_default_idx
    ON saved_filters (project_id, owner_id, scope)
    WHERE is_default;
```

Setting a new default clears the previous default for the same owner and
scope in the same transaction.

Visibility and permissions:

| Action | Rule |
|---|---|
| Read | Shared views, the caller's own private views, and all views for owners, maintainers and admins. |
| Create private | `work_item.write`. |
| Create shared | `planning.write`. |
| Edit/delete | The owner, `settings.write`, project owner or maintainer. |
| Change private → shared | `planning.write`. |

Endpoints:

| Method | Path |
|---|---|
| `GET` | `/api/v1/projects/{id}/saved-filters?scope=board` |
| `POST` | `/api/v1/projects/{id}/saved-filters` |
| `PUT` | `/api/v1/projects/{id}/saved-filters/{filterId}` |
| `DELETE` | `/api/v1/projects/{id}/saved-filters/{filterId}` |

`PUT` uses the shared optimistic-concurrency contract (`If-Match`,
`expected_version` or the body field). `saved_filters` carries a `revision`
column and the same `planning_revision` trigger as the other planning tables.

The board is URL-backed. A selected view adds `view=<id>` and the individual
layout values to the query string — `swimlane`, `sort`, `fields`, `quick`,
`search`, `type`, `feature`, `sprint`, `label` and `cf_<field-key>` — so
refreshing or sharing the URL reproduces the board. A default view is applied
on load even without `view=<id>`. Private views are returned only to their
owner; a shared URL still requires project access. See
[`BOARD-VIEWS.md`](BOARD-VIEWS.md) for the complete state model and the WIP
policy.

## 4. Schema and migration

Migration `021_labels_custom_fields_saved_filters.sql` is expand-only:

- creates `project_labels`, `custom_fields`, `custom_field_values` and
  `saved_filters`;
- backfills existing normalized work item labels into `project_labels` with
  `ON CONFLICT DO NOTHING`;
- indexes every foreign-key column as its leading column
  (`project_labels_project_idx`, `custom_fields_project_idx`,
  `custom_field_values_entity_idx`, `saved_filters_owner_idx`, and peers);
- installs the `planning_revision` trigger on `saved_filters`.

The rollback companion drops the trigger, indexes and tables in dependency
order. It deliberately keeps `work_items.labels`, because the array is the
display value that survives label deletion. The migration is covered by
`TestLabelsCustomFieldsSavedFiltersRollbackIsComplete` and by the schema-wide
foreign-key/index audit.

Migration `022_board_view_swimlanes.sql` is expand-only and adds
`saved_filters.swimlane` with a `none` default and a check constraint on the
four supported groupings. Its rollback companion drops the column. The pair is
covered by `TestBoardViewSwimlaneRollbackIsComplete`.

## 5. UI

- **Settings → Labels:** create, edit, recolor and delete labels with usage
  counts.
- **Settings → Custom fields:** typed definition editor, option list, numeric
  range, required flag, archive/restore and explicit purge.
- **Board:** label and custom-field filters, colored chips on cards, select
  value chips, saved-view selector, create/update/delete views, pinned and
  default flags, URL-backed filters, swimlane grouping, quick-filter chips,
  sort and visible-field editors.
- **Settings → Board policy:** per-column WIP limits with an explicit hard-block
  toggle; the board shows `current/limit` badges and pre-checks a drop when the
  hard block is enabled.
- **Work item dialog:** label suggestions, typed custom-field editors, required
  validation and member/date/number inputs.

All UI actions are capability-gated; the API repeats every check because the
client is never trusted.
