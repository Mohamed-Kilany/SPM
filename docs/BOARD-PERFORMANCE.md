# Board performance and scale

BRD-03 makes the project board predictable at 2,000+ cards. The board is still
server-backed and URL-addressable; this document defines the reference dataset,
the budgets, the rendering model that meets them, and the evidence used to
verify each budget. Board behaviour is documented in
[`BOARD-VIEWS.md`](BOARD-VIEWS.md).

## 1. Reference dataset

The stress test synthesizes the workspace response rather than depending on a
seeded project, so the same dataset runs on every environment:

| Dimension | Value |
|---|---|
| Work items | 2,000 |
| Columns | 7 (`backlog`, `ready`, `in_progress`, `review`, `qa`, `done`, `blocked`) |
| Types | story, task, bug, spike, chore, subtask |
| Priorities | critical, high, medium, low |
| Labels | 0 or 2 per card |
| Assignees | 0 or 1 per card |
| Custom fields, branches, PRs | none, so the test isolates board rendering |
| Saved views | none, so the default layout is used |
| Swimlane | `none` (one lane) |
| Sort | `priority:asc` (highest first) |

Card 1 is fixed to `ready` / `task` / `critical` and is the only critical card
in `ready`. That gives the drag and keyboard scenarios a deterministic legal
transition while keeping the anchor inside the first rendered page.

The dataset is generated in
[`ui/e2e/board-scale.spec.ts`](../ui/e2e/board-scale.spec.ts). The workspace
response is intercepted before the SPA loads, so no database, Gitea or Argo CD
state is created or changed.

## 2. Budgets

| Budget | Target | Gate |
|---|---|---|
| Initial board render | < 1,500 ms | `data-render-ms` from the stress spec. |
| Drag or move interaction | < 100 ms perceived | `data-move-ms` for the pointer drag and the median of 60 keyboard moves. |
| Board scrolling | 60 fps on a mid-range laptop | 1.5 s `requestAnimationFrame` loop; the headless floor is 50 fps to absorb CI scheduler noise. |
| Layout shift while cards load | CLS < 0.1 | `layout-shift` entries after the board shell mounts. |
| Memory after 10 minutes of SSE updates | bounded; no per-event growth | Accelerated soak of 60 full snapshot replacements; heap growth < 48 MB after forced GC. |
| JavaScript bundle | 230,000 bytes gzip | `npm run bundle:check`. |
| CSS bundle | 16,000 bytes gzip | `npm run bundle:check`. |
| Largest chunk | 125,000 bytes gzip | `npm run bundle:check`. |

The 60 fps target is the product budget. The automated floor is 50 fps because
headless Chromium shares a CPU with the Playwright worker; the measured value
is attached to the test report so a release check on reference hardware can
confirm 60 fps.

The 10-minute memory budget is structural rather than a literal 10-minute
wait in CI. The board replaces the workspace snapshot instead of appending to
it, the snapshot cache is capped at five projects, stream events are coalesced
into one refresh, and the toast stack is capped. The accelerated soak performs
60 complete workspace replacements in under a minute and compares the heap
after forced GC; a leak of one 2,000-card snapshot per cycle would exceed
100 MB, well above the 48 MB gate. The wall-clock case is covered by a manual
release soak that runs the board against a live SSE stream for 10 minutes and
records the heap plateau; that is a release check, not a PR gate.

## 3. Rendering model

### One grouping pass

`buildBoardLanes` in `ui/src/lib/planning.ts` groups every effective item into
its column once, then sorts each bucket. The previous shape filtered the full
item list once per lane and per column, which is
`O(lanes × columns × items)`. The current shape is `O(items + items log items)`
for the common one-lane case and does not re-filter when a card moves.

`boardWipCounts` also walks the effective items once. WIP limits are evaluated
against the unfiltered column population, matching the server policy, so
filtering the board never changes a WIP badge.

### Lazy column pages

Each column renders `BOARD_COLUMN_PAGE_SIZE` (40) cards at a time. With seven
columns the initial board therefore mounts at most 280 cards, regardless of
whether the project has 200 or 20,000 work items. The column footer shows
`Show next 40 of N hidden`; `nextVisibleCount` clamps the last page so a column
with 7 cards never offers 40 more.

Pages are keyed by `<lane>:<column>` and are reset when the filter, quick
filter, sort or swimlane layout changes. This keeps a page from a previous
filter visible against a new result set. A keyboard or drag move calls
`revealItem`, which expands the target page far enough to include the moved
card. Focus never lands on a card hidden by the page boundary.

The page size is deliberately a constant rather than a measurement. It is the
main tuning knob: larger pages reduce clicks but increase DOM work, smaller
pages do the reverse. 40 keeps the initial board under the render budget while
still showing a useful amount of each column.

### O(1) card lookups

The board builds lookup maps once per workspace snapshot for:

- work items by id (parent links and drawer navigation),
- feature titles by id,
- custom-field values by work item,
- branches by work item,
- pull requests by work item.

Before these maps, every rendered card scanned the full feature, branch, PR and
custom-value collections. That cost scaled with cards × collections and was
the dominant cost after the grouping pass was fixed.

### Debounced filters

Free-text search is debounced by `BOARD_FILTER_DEBOUNCE_MS` (180 ms). The input
stays controlled and responsive; the board and URL settle once after typing
pauses. Applying a saved view or clearing filters cancels the pending timer and
updates both the input and the active query in the same tick, so a stale
debounce cannot overwrite a deliberate filter change.

### Snapshot cache

`workspaceStore` keeps a module-scoped cache of the five most recently loaded
project snapshots. Reopening a project paints the cached board immediately
while the authoritative snapshot is fetched in the background. The cache is
bounded (least-recently-used eviction), survives `reset()` so route changes do
not thrash it, and is cleared by `clearSnapshotCache()` in tests. A full page
reload clears it. Loading a project that is not cached still shows the normal
loading state.

### SSE refresh coalescing

The workspace stream is a change signal, not a JSON Patch source. Events call
`store.refresh()` after a 220 ms timer, and concurrent callers share one
in-flight request. A burst of events therefore produces one snapshot
replacement, not one per event. The snapshot is replaced, not merged, so there
is no unbounded event history in memory. The toast stack is capped at four
items for the same reason.

## 4. Measurement hooks

The board exposes stable hooks so the stress test does not depend on visual
inspection:

| Hook | Meaning |
|---|---|
| `[data-testid="board-perf"]` | Hidden element with `data-render-ms` and `data-move-ms`. |
| `[data-testid="board-scroll"]` | Horizontal board scroll container. |
| `[data-testid="board-show-more-<lane>-<column>"]` | Lazy-page control for a column. |
| `[data-board-column="<lane>:<column>"]` | Column drop target. |
| `[data-board-card="<item-id>"]` | Focusable card. |

`data-render-ms` is measured from board component setup to the first non-empty
render. `data-move-ms` is measured from the optimistic overlay commit through
the next Vue tick, before the status request is sent. Both are rounded to
0.1 ms. They are diagnostics, not user-facing content, and the element is
`aria-hidden`.

## 5. Playwright stress test

`ui/e2e/board-scale.spec.ts` covers:

1. initial render against 2,000 cards and the render budget;
2. CLS after the board shell mounts;
3. the 40-card lazy page and the next-page control;
4. a pointer drag from `ready` to `in_progress` and its move budget;
5. horizontal board scrolling and its frame rate;
6. 60 accelerated full-snapshot refreshes with a forced-GC heap comparison and
   a median keyboard-move budget;
7. a Chrome DevTools performance trace attached to the Playwright report as
   `board-scale-trace.json`;
8. a metrics attachment with every measured value.

The spec requires the full stack, like the rest of `ui/e2e`. It is not part of
the PR gate yet (QAT-03 tracks that); CI is the authority when the stack is
available. To run it:

```bash
cd ui
npm run build
npx playwright test board-scale.spec.ts
```

The suite uses the admin credentials from the standard E2E environment and
`E2E_BASE_URL`. No project is created; the workspace route, status route,
stream ticket and stream are intercepted for the synthetic project id.

## 6. Bundle analysis

The production bundle is measured on gzip bytes because that is what the
browser downloads. `ui/scripts/bundle-budget.mjs` reads
`ui/performance-budget.json`, sums every JavaScript and CSS asset in
`dist/assets`, and fails when any budget is exceeded. The UI job in
`.github/workflows/pr-gate.yml` runs `npm run bundle:check` immediately after
`npm run build`.

Current measured values on the reference branch:

| Asset | Measured | Budget | Headroom |
|---|---|---|---|
| JavaScript, all chunks | 199.6 KiB | 224.6 KiB (230,000 B) | 25.0 KiB |
| CSS, all chunks | 11.3 KiB | 15.6 KiB (16,000 B) | 4.3 KiB |
| Largest chunk (`ProjectWorkspaceView`) | 103.3 KiB | 122.1 KiB (125,000 B) | 18.8 KiB |

The largest chunk is the workspace view, which contains the board, planning,
test lab, delivery and reports tabs in one lazy route chunk. Splitting it
further is a future option if the budget tightens; the board itself is not a
separate route.

Run the analysis locally with:

```bash
cd ui
npm run build
npm run bundle:check
```

The script prints the five largest assets so a regression can be traced to a
chunk without opening the report.

## 7. Known limits

- **No windowing inside a page.** The board lazy-loads by column page, not by
  viewport. A 40-card column still mounts 40 cards even if only 8 are visible.
  This is simpler and predictable; page size is the tuning knob.
- **Horizontal scroll at narrow widths.** Columns are fixed at 230 px and the
  board has a 1,180 px minimum width. At viewports below that the board scrolls
  horizontally rather than shrinking cards to unreadable widths.
- **Synthetic dataset.** The reference dataset has no custom fields, branches
  or PRs. A project with all optional card fields enabled renders more per
  card; the lookup maps keep that cost linear, but the bundle and render
  budgets should be re-measured if the card layout grows substantially.
- **SSE soak is accelerated.** The automated test replaces the snapshot 60
  times; it does not wait 10 minutes. The manual release soak covers the
  wall-clock case.
- **Playwright is not in the PR gate.** The unit suite, typecheck, production
  build and bundle budget are the merge gate. The stress spec runs where the
  Docker stack is available.

## 8. Verification checklist

- `ui/src/lib/planning.test.ts` covers the 2,000-card grouping pass, WIP
  counts, page clamping and the existing board behaviour (31 tests).
- `ui/src/stores/workspaceStore.test.ts` covers cache paint, background
  refresh and five-project eviction (3 tests).
- `ui/e2e/board-scale.spec.ts` covers the render, move, scroll, CLS, lazy-page
  and accelerated-memory budgets.
- `npm run bundle:check` enforces the gzip budgets in CI.
- The performance trace and metrics are attached to the Playwright report for
  every stress run.

The full-stack Playwright suite is not claimed by the local unit gate. It is
run by CI where the Docker stack is available; this document records the
budgets and the evidence the suite produces, not a claim that it ran in the
unit-only environment.
