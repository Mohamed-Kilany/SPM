# Board views, swimlanes and WIP limits

The project board is server-backed and URL-addressable. A **saved view** is a
named `saved_filters` row that captures everything needed to reproduce the
board: facet filters, quick filters, sort, swimlane grouping, visible card
fields and presentation flags. The board itself is stateless; the URL and the
saved view are the two sources of state.

This guide covers BRD-01 and BRD-02. Label and custom-field administration is
described in [`PLANNING-CUSTOMIZATION.md`](PLANNING-CUSTOMIZATION.md); the HTTP
contract is generated into [`openapi.yaml`](openapi.yaml).

## 1. View model

Views reuse the `saved_filters` table from migration `021`; migration `022`
adds the `swimlane` column. No new endpoint is introduced.

| Field | Type | Meaning |
|---|---|---|
| `scope` | `planning`, `board`, `test` | `board` for board views. |
| `visibility` | `private` (default) or `shared` | Private views are visible to the owner; shared views to every project member. |
| `filters` | JSON object | Facet filters plus the `quick` array. |
| `sort` | JSON object | `{"field": ..., "direction": "asc"\|"desc"}`. |
| `columns` | string[] | Visible card fields (not database columns). Up to 20 entries, each 1–40 characters. |
| `swimlane` | enum | `none` (default), `assignee`, `priority`, `type`. |
| `pinned` | boolean | Pinned views sort before unpinned views. |
| `is_default` | boolean | At most one default per owner and `scope`. |
| `revision` | integer | Optimistic-concurrency token; `PUT` and `DELETE` require `If-Match`. |

A board view therefore captures all five pieces of BRD-01 layout state:

1. **Filters** — `search`, `type`, `feature`, `sprint`, `label`, `custom`
   keyed by field key, and `quick`.
2. **Swimlane** — one of the four values above.
3. **Sort** — one of the documented sort options.
4. **Visible fields** — the subset of card fields to render.
5. **Presentation flags** — `pinned` and `is_default`.

Unknown `swimlane` values are rejected by `validateSavedFilter`. An empty value
normalizes to `none`, so older clients can omit the field. `columns` is free
text at the API level, but the UI only writes the seven known field keys and
ignores unknown values when a view is applied.

### Default view

`is_default` is enforced by the partial unique index
`saved_filters_default_idx (project_id, owner_id, scope) WHERE is_default`.
Creating or updating a default clears the previous default for the same owner
and scope in the same transaction.

When the board is opened without `?view=`:

1. the signed-in user's default view is applied;
2. otherwise the first default view visible to the caller is applied (normally
   a shared default; maintainers and admins can see every view);
3. otherwise the built-in layout (priority sort, no swimlanes, every field,
   no quick filters) is used.

The default is per user and is re-applied on every load, so it survives reload
without pinning the view id in the URL. Selecting a different view writes
`?view=<id>`; editing filters or layout clears `view` and keeps the explicit
URL state instead.

## 2. URL state

The board writes its state to the project workspace route on every change, so a
refresh, a bookmark or a pasted link reproduces the board. Private views remain
owner-only; sharing a URL never bypasses project membership.

| Query parameter | Values |
|---|---|
| `tab` | `board` |
| `view` | Saved view id. Omit for the default view or explicit URL state. |
| `swimlane` | `assignee`, `priority`, `type` (omitted for `none`). |
| `sort` | `<field>:<direction>`, for example `due_soon:asc` (omitted for `priority:asc`). |
| `fields` | Comma-separated visible field keys (omitted when every field is visible). |
| `quick` | Comma-separated quick-filter keys. |
| `search` | Free text matched against the work item key, title and feature name. |
| `type` | Work item type. |
| `feature` | Feature id. |
| `sprint` | Sprint id. |
| `label` | Label name. |
| `cf_<field-key>` | Custom-field filter value. |
| `item` | Open work item id for the detail drawer. |

Facet filters keep their existing encoding from PLN-05. Quick filters are
deduplicated when read and written.

## 3. Quick filters

Quick filters are one-click toggles in the board toolbar:

| Key | Label | Matches |
|---|---|---|
| `mine` | Assigned to me | `assignee_id` equals the signed-in user. |
| `unassigned` | Unassigned | `assignee_id` is empty. |
| `blocked` | Blocked | `status` is `blocked`. |
| `bugs` | Defects | `type` is `bug`. |
| `due_soon` | Due in 7 days | `due_date` is set and not later than now + 7 days. |

Multiple quick filters are **AND**-combined with each other and with the facet
filters. `mine` + `bugs` therefore means "my defects", not "my work or
defects". Unscheduled cards never match `due_soon`.

## 4. Sort

| Option id | Label | Ordering |
|---|---|---|
| `priority:asc` | Priority: highest first | Critical → high → medium → low → unset. |
| `priority:desc` | Priority: lowest first | Reverse of the above. |
| `newest:desc` | Newest first | Newest `created_at` first. |
| `oldest:asc` | Oldest first | Oldest `created_at` first. |
| `due_soon:asc` | Due soonest first | Earliest `due_date` first. |
| `due_later:desc` | Due latest first | Latest `due_date` first. |
| `key:asc` | Key: ascending | Lowest `item_number` first. |
| `key:desc` | Key: descending | Highest `item_number` first. |

Ordering is applied inside each column, not across the whole board. Cards
without a due date always sort **last** for both date options, regardless of
direction, so an unscheduled item never looks urgent. Ties break by descending
`item_number`, which keeps the order stable between renders.

## 5. Swimlanes

`swimlane` groups the same seven columns into horizontal lanes:

| Value | Lane key | Lane order |
|---|---|---|
| `none` | `all` | One lane, no header. |
| `assignee` | User id or `unassigned` | Member display name; unassigned last. |
| `priority` | `critical`, `high`, `medium`, `low`, `none` | Critical first, unset last. |
| `type` | `story`, `task`, `bug`, `spike`, `chore`, `subtask`, `none` | Story first, unknown types last. |

Every lane renders every column, including empty ones. The card order inside a
lane is the selected sort order, and the lane order is deterministic rather
than dependent on data arrival. WIP badges are evaluated against the whole
project, not against the lane, because a WIP limit is a board-column policy.

## 6. Visible card fields

A view's `columns` array selects which card fields are rendered:

| Key | Shows |
|---|---|
| `priority` | Priority pill. |
| `feature` | Parent work item link, or the feature title. |
| `labels` | Up to three label chips. |
| `assignee` | Assignee initials or "Unassigned". |
| `due_date` | Due-date chip with an overdue/due-soon tone. |
| `delivery` | Linked branch and pull-request counts. |
| `custom` | Up to two select-type custom-field values. |

All seven are enabled when a new view is created. The board editor allows any
subset; if an existing view has an empty `columns` array, the UI falls back to
all fields rather than rendering blank cards. Hidden fields are not removed
from the work item; this is presentation only.

## 7. WIP policy

WIP limits live in the project settings document:

```json
{
  "merge_policy": { "...": "..." },
  "board": {
    "wip_limits": { "in_progress": 4, "review": 3 },
    "wip_hard_block": false
  },
  "auto_rollback": false
}
```

### Columns and counting

Limits are keyed by **board column**, not by raw status:

| Column key | Statuses that count |
|---|---|
| `backlog` | `backlog`, `new`, `reopened`, `cancelled` |
| `ready` | `ready`, `triaged` |
| `in_progress` | `in_progress` |
| `review` | `in_review`, `fixed` |
| `qa` | `in_qa`, `verified` |
| `done` | `done`, `closed` |
| `blocked` | `blocked` |

The map is defined in `api/internal/workspace/board_wip.go` and mirrors
`boardColumns` in `ui/src/lib/workspace.ts`. Counts include every work item in
the project with one of the column's statuses, including cancelled items in
`backlog`. The UI badge and the server check therefore agree.

`wip_limits` values must be whole numbers from 0 to 999; unknown column keys
are rejected with `400 VALIDATION_ERROR`. A limit of `0` (or an omitted key)
means "no limit" and is removed during normalization.

### Warn versus hard block

- `wip_hard_block: false` (default) — the board shows `current/limit` badges
  and colours a reached limit amber and an exceeded limit red. Moves are never
  blocked by WIP.
- `wip_hard_block: true` — the API rejects a move or create that would
  overfill a column. The board also pre-checks the drop and shows the
  rejection without calling the API.

The hard block is opt-in because it changes delivery behaviour. It is surfaced
in **Settings → Board policy** and in the board toolbar ("WIP moves are
enforced").

The rule is evaluated on **create** and on any **status change** that crosses
columns. Moving between two statuses in the same column (`in_review` →
`fixed`, or `backlog` → `cancelled`) is always allowed because it does not
change the column count. Updating other fields is never blocked.

`UpdateSettings` replaces the whole settings object. The UI must send `board`
alongside `merge_policy` and the delivery fields, or the limits are cleared;
`WorkspaceDelivery.vue` preserves `board` when it saves delivery policy.

### `409 WIP_LIMIT` contract

A rejected move returns HTTP `409`:

```json
{
  "error": {
    "code": "WIP_LIMIT",
    "message": "in_progress is at its WIP limit of 4"
  },
  "wip": {
    "column": "in_progress",
    "status": "in_progress",
    "current": 4,
    "limit": 4,
    "hard_block": true
  }
}
```

`current` is the count before the move. The structured `wip` object lets the
board explain the rejection without re-reading project settings. A limit is
reached when `current >= limit`; the mutation is rejected rather than allowing
a temporary overfill.

## 8. Moving work items

The board moves work through the same revisioned
`PATCH /projects/{id}/work-items/{itemId}/status` endpoint used by the drawer.
There is no separate board-move or bulk endpoint: each card is one request, so
the server can enforce the workflow, WIP and permission rules per item.

### Pointer drag

Cards use pointer events rather than HTML5 drag and drop, so the same gesture
works with a mouse, a pen and touch. A drag starts after a 6 px movement
threshold. The source card is dimmed, a preview follows the pointer and the
column under the pointer is highlighted as the drop target. Releasing over a
column performs exactly one mutation; releasing anywhere else cancels without a
request. The click that follows a drag is suppressed so the detail drawer does
not open. Cards use `touch-action: pan-y`, leaving vertical scrolling available
on touch devices while horizontal movement can start a drag.

A drop on the card's current column is a no-op. A drop on a column with no legal
transition shows a workflow warning and sends nothing. When hard WIP blocking
is enabled, the board pre-checks the target column and refuses the drop before
the request; the API repeats the check as the authority.

### Keyboard moves

Every card is focusable. With focus on a card, `Alt+Left` and `Alt+Right` move
it to the nearest column in that direction that has a legal transition. Columns
without a legal status are skipped: for example, `Done` cannot go straight to
`Review`, so `Alt+Left` reopens it into `In progress`. The move is announced in
a polite live region with the new column and position, for example
`SPM-42 moved to Ready, position 2 of 5.` Focus returns to the moved card after
the optimistic re-render. `Alt` is required so plain arrow keys keep scrolling
the board.

### Optimistic updates, rollback and retry

A move paints immediately through a pending-status overlay while the request is
in flight. The overlay is keyed by work item id and is also used by WIP counts,
quick filters and swimlane lanes, so the card never appears in two columns.
When the request succeeds the workspace snapshot is refreshed before the
overlay is removed. When it fails the overlay is removed, the card returns to
its previous column and a retry banner names the item and the target column.
Retry re-reads the current revision from the refreshed snapshot, so a stale
`If-Match` does not trap the user in a retry loop. Revision conflicts also
refresh the workspace and use the standard conflict message.

Concurrent refreshes are coalesced: awaiting `refresh()` means the latest
snapshot has been installed, not that a second caller returned while the first
request was still running. That guarantee is what lets the overlay be removed
safely after a move.

### Multi-select and bulk move

The checkbox on each card builds a selection; `Ctrl`/`Cmd`-clicking a card
toggles selection without opening it. The bulk bar offers a target column and a
`Move selected` action. Bulk moves are **per-item**, not transactional: every
selected card gets its own revisioned request, so a WIP or transition rejection
on one card does not roll back the others. The result dialog reports `moved`,
`failed` and `skipped` counts and a line per item. Failed items stay selected
and can be retried with `Retry failed`; cards already in the target column are
reported as skipped without a request.

### Audit and telemetry

Every successful status change writes the `work_item.move` audit event with
`before.status` and `after.status`, in addition to the existing
`work_item.status_changed` activity entry. Rejected or failed moves increment
`spm_board_move_failures_total` with a bounded `reason` (`conflict`, `wip`,
`invalid_transition`, `invalid_request`, `not_found` or `error`). The metric
catalogue and alert guidance are in
[`docs/OBSERVABILITY.md`](OBSERVABILITY.md#planning); the audit event catalogue
is in [`docs/AUDIT.md`](AUDIT.md#6-event-catalog).

## 9. Permissions

| Action | Rule |
|---|---|
| Read a saved view | Shared views, the caller's own private views, and all views for owners, maintainers and admins. |
| Create a private view | `work_item.write`. |
| Create or update a shared view | `planning.write`. |
| Edit or delete a view | The owner, `settings.write`, project owner or maintainer. |
| Change private → shared | `planning.write`. |
| Change board policy | `settings.write`. |
| Move a work item | `work_item.write` plus the workflow transition guard. |

Every check is repeated by the API. The UI hides actions the caller cannot
perform, but it is never the authority.

## 10. Verification

- `api/internal/workspace/board_wip_test.go` covers the column map, settings
  normalization, default policy and settings decoding.
- `api/internal/workspace/handler_planning_fields_test.go` covers swimlane
  normalization and rejection of unknown values, plus view validation.
- `api/internal/workspace/revision_test.go` covers the `If-Match` conflict
  contract and the planning-conflict counter.
- `api/internal/metrics/metrics_test.go` covers the bounded
  `spm_board_move_failures_total` reasons.
- `api/internal/migrate/schema_test.go` pins migration `022` as the latest
  migration and verifies the rollback removes the column and its constraint.
- `ui/src/lib/planning.test.ts` covers layout capture, sort ordering,
  swimlane grouping, quick-filter composition, visible fields, default-view
  selection, WIP state, keyboard target selection, announcements and bulk
  summaries (27 tests in the planning suite).
- `ui/e2e/linked-sdlc.spec.ts` covers a failed drag with rollback and retry,
  a successful retry, keyboard moves with a live-region assertion and a
  multi-item bulk move.
- `go build ./...`, `go vet ./...`, `go test ./...`, `go run ./cmd/openapi
  -check`, the Redocly lint, and the UI typecheck, unit tests and production
  build are the merge gate.

The full-stack Playwright suite requires the Docker stack and is not claimed by
the unit gate; the spec is run by CI where the stack is available.
