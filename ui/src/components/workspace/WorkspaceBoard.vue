<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BaseModal from '../BaseModal.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import PriorityPill from './PriorityPill.vue'
import EmptyState from './EmptyState.vue'
import WorkItemDrawer from './WorkItemDrawer.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useAuthStore } from '../../stores/authStore'
import { useToastStore } from '../../stores/toastStore'
import { api, isRevisionConflict, revisionConflictMessage } from '../../api/client'
import {
  allowedTransitions,
  boardColumnFor,
  boardColumns,
  branchNameFor,
  dateInputValue,
  defaultStatusFor,
  statusLabel,
  targetStatusForColumn,
  workItemStatusOptions,
  workItemTypeLabels,
} from '../../lib/workspace'
import { timeAgo } from '../../lib/format'
import {
  BOARD_COLUMN_PAGE_SIZE,
  BOARD_FILTER_DEBOUNCE_MS,
  BOARD_QUICK_FILTERS,
  BOARD_SORT_OPTIONS,
  BOARD_SWIMLANES,
  BOARD_VISIBLE_FIELDS,
  boardFilterCount,
  boardFiltersFromQuery,
  boardFiltersToQuery,
  boardFiltersToSaved,
  boardMoveAnnouncement,
  boardSortFromId,
  boardSortId,
  boardSortToSaved,
  boardViewFromSaved,
  boardWipCounts,
  buildBoardLanes,
  canManageSavedFilter,
  colorForLabel,
  compareBoardItems,
  customFieldDisplay,
  customFieldDraft,
  customFieldPayload,
  customValueMap,
  defaultBoardView,
  fieldVisible,
  itemMatchesQuickFilters,
  keyboardMoveTarget,
  labelDot,
  labelTone,
  missingRequiredCustomField,
  moveTargetForColumn,
  nextVisibleCount,
  normalizeLabelInput,
  quickFiltersFromValue,
  quickFiltersToSaved,
  summarizeBoardMoves,
  toggleQuickFilter,
  visibleFieldsFromValue,
  visibleFieldsToSaved,
  wipState,
  workItemMatchesFilters,
} from '../../lib/planning'
import type { BoardFilters, BoardMoveResult, BoardQuickFilter, BoardSort } from '../../lib/planning'
import type { CustomField, GitBranch, PullRequest, SavedFilter, WorkItem } from '../../types/workspace'

interface PendingMove {
  fromStatus: string
  toStatus: string
  columnKey: string
  columnLabel: string
  source: 'drag' | 'keyboard' | 'bulk'
}

interface FailedMove {
  itemId: string
  itemKey: string
  columnKey: string
  columnLabel: string
  message: string
}

const store = useWorkspaceStore()
const auth = useAuthStore()
const toast = useToastStore()
const route = useRoute()
const router = useRouter()

const searchInput = ref('')
const search = ref('')
let searchTimer: ReturnType<typeof setTimeout> | null = null
const typeFilter = ref('')
const sprintFilter = ref('')
const featureFilter = ref('')
const labelFilter = ref('')
const customFilters = reactive<Record<string, string>>({})
const selectedViewId = ref('')
const swimlane = ref('none')
const sort = ref<BoardSort>({ field: 'priority', direction: 'asc' })
const visibleFields = ref<string[]>(BOARD_VISIBLE_FIELDS.map((field) => field.value))
const quickFilters = ref<BoardQuickFilter[]>([])
const draggingId = ref<string | null>(null)
const dropTarget = ref<string | null>(null)
const dragPoint = reactive({ x: 0, y: 0 })
const pendingMoves = reactive<Record<string, PendingMove>>({})
const failedMove = ref<FailedMove | null>(null)
const announcement = ref('')
const selectedIds = ref<string[]>([])
const bulkTargetColumn = ref('')
const bulkBusy = ref(false)
const bulkResult = ref<{ columnKey: string; columnLabel: string; entries: BoardMoveResult[] } | null>(null)
const bulkResultOpen = ref(false)
const visibleCounts = reactive<Record<string, number>>({})
const boardPerf = reactive({ renderMs: 0, lastMoveMs: 0 })
const mountedAt = performance.now()
let firstRenderMeasured = false

let dragCandidate: {
  id: string
  pointerId: number
  startX: number
  startY: number
  moved: boolean
} | null = null
let suppressClickUntil = 0

const boardScope = 'board'
const boardFields = computed(() => store.customFields)
const boardLabels = computed(() => store.labels)
const boardViews = computed(() =>
  store.savedFiltersFor(boardScope).slice().sort((a, b) => {
    if (a.pinned !== b.pinned) return a.pinned ? -1 : 1
    if (a.is_default !== b.is_default) return a.is_default ? -1 : 1
    return a.name.localeCompare(b.name)
  }),
)
const boardPolicy = computed(() => store.project?.settings?.board)

// pendingMoves is the optimistic overlay. The store is only replaced after the
// API confirms a move, so a failed request can restore the exact previous card
// without touching the cached snapshot.
const effectiveItems = computed<WorkItem[]>(() =>
  store.workItems.map((item) => {
    const pending = pendingMoves[item.id]
    return pending ? { ...item, status: pending.toStatus } : item
  }),
)
const selectedItems = computed(() =>
  effectiveItems.value.filter((item) => selectedIds.value.includes(item.id)),
)
const dragItem = computed(() => (draggingId.value ? store.workItemById(draggingId.value) : undefined))
const bulkSummary = computed(() =>
  bulkResult.value
    ? summarizeBoardMoves(bulkResult.value.entries)
    : { moved: 0, failed: 0, skipped: 0 },
)

function isPending(id: string): boolean {
  return Boolean(pendingMoves[id])
}

function isSelected(id: string): boolean {
  return selectedIds.value.includes(id)
}

function toggleSelected(id: string): void {
  selectedIds.value = isSelected(id)
    ? selectedIds.value.filter((entry) => entry !== id)
    : [...selectedIds.value, id]
}

function clearSelection(): void {
  selectedIds.value = []
}

function boardColumnLabel(key: string): string {
  return boardColumns.find((column) => column.key === key)?.label ?? key
}

function announce(message: string): void {
  announcement.value = message
}

function announcePosition(item: WorkItem, columnKey: string, columnLabel: string): void {
  const column = boardColumns.find((entry) => entry.key === columnKey)
  if (!column) {
    announce(boardMoveAnnouncement(item.key, columnLabel, 0, 0))
    return
  }
  const items = effectiveItems.value
    .filter((entry) => column.statuses.includes(entry.status))
    .sort((a, b) => compareBoardItems(a, b, sort.value))
  const position = items.findIndex((entry) => entry.id === item.id) + 1
  announce(boardMoveAnnouncement(item.key, columnLabel, position, items.length))
}

async function focusItem(id: string): Promise<void> {
  await nextTick()
  document.querySelector<HTMLElement>(`[data-board-card="${id}"]`)?.focus()
}

function isInteractiveTarget(target: EventTarget | null): boolean {
  return (
    target instanceof Element &&
    Boolean(target.closest('button, input, select, textarea, a, [data-no-drag]'))
  )
}

function boardTargetAt(x: number, y: number): { key: string; columnKey: string } | null {
  const element = document.elementFromPoint(x, y)
  const section = element instanceof Element ? element.closest<HTMLElement>('[data-board-column]') : null
  const key = section?.dataset.boardColumn
  const columnKey = section?.dataset.columnKey
  return key && columnKey ? { key, columnKey } : null
}

function finishDrag(): void {
  draggingId.value = null
  dropTarget.value = null
  document.body.classList.remove('select-none')
}

function onCardPointerDown(item: WorkItem, event: PointerEvent): void {
  if (!canWriteWorkItems.value || isPending(item.id)) return
  if (event.button !== 0 || event.isPrimary === false) return
  if (isInteractiveTarget(event.target)) return
  dragCandidate = {
    id: item.id,
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    moved: false,
  }
}

function onCardPointerMove(item: WorkItem, event: PointerEvent): void {
  if (
    !dragCandidate ||
    dragCandidate.id !== item.id ||
    dragCandidate.pointerId !== event.pointerId
  ) {
    return
  }
  if (!dragCandidate.moved) {
    const distance = Math.hypot(
      event.clientX - dragCandidate.startX,
      event.clientY - dragCandidate.startY,
    )
    if (distance < 6) return
    dragCandidate.moved = true
    draggingId.value = item.id
    ;(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId)
    document.body.classList.add('select-none')
  }
  if (event.cancelable) event.preventDefault()
  dragPoint.x = event.clientX
  dragPoint.y = event.clientY
  dropTarget.value = boardTargetAt(event.clientX, event.clientY)?.key ?? null
}

function onCardPointerUp(item: WorkItem, event: PointerEvent): void {
  if (
    !dragCandidate ||
    dragCandidate.id !== item.id ||
    dragCandidate.pointerId !== event.pointerId
  ) {
    return
  }
  const candidate = dragCandidate
  dragCandidate = null
  if (!candidate.moved) return
  // A drag finishes with a click event on the card. Suppress that one click so
  // dragging never opens the drawer or selects the card twice. The timestamp
  // also covers touch click delays and expires on its own if no click arrives.
  suppressClickUntil = Date.now() + 500
  const target = boardTargetAt(event.clientX, event.clientY)
  finishDrag()
  if (target) void requestMove(item, target.columnKey, 'drag')
}

function onCardPointerCancel(): void {
  dragCandidate = null
  finishDrag()
}

function onCardClick(item: WorkItem, event: MouseEvent): void {
  if (Date.now() < suppressClickUntil) {
    suppressClickUntil = 0
    return
  }
  if (isInteractiveTarget(event.target)) return
  if (event.metaKey || event.ctrlKey) {
    toggleSelected(item.id)
    return
  }
  openItemDrawer(item)
}

function onCardActivate(item: WorkItem, event: KeyboardEvent): void {
  if (isInteractiveTarget(event.target)) return
  event.preventDefault()
  openItemDrawer(item)
}

function onCardKeydown(item: WorkItem, event: KeyboardEvent): void {
  if (!event.altKey || (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight')) return
  event.preventDefault()
  event.stopPropagation()
  if (!canWriteWorkItems.value) {
    announce('You do not have permission to move work items.')
    return
  }
  if (isPending(item.id)) return
  const direction = event.key === 'ArrowRight' ? 1 : -1
  const target = keyboardMoveTarget(item, direction)
  if (!target) {
    const label = direction === 1 ? 'right' : 'left'
    announce(`${item.key} cannot move ${label}. No legal column in that direction.`)
    toast.warning(`${item.key} has no legal column ${label}.`, 'Workflow guard')
    return
  }
  void requestMove(item, target.column.key, 'keyboard')
}

async function requestMove(
  item: WorkItem,
  columnKey: string,
  source: PendingMove['source'],
): Promise<boolean> {
  if (!canWriteWorkItems.value || isPending(item.id)) return false
  const target = moveTargetForColumn(item, columnKey)
  if (!target || target.column.key === boardColumnFor(item.status).key) {
    if (!target) {
      const message = `${statusLabel(item.status)} cannot move to ${boardColumnLabel(columnKey)}. Open the item to choose a valid next step.`
      toast.warning(message, 'Workflow guard')
      if (source === 'keyboard') announce(`Move failed. ${item.key}. ${message}`)
    }
    return false
  }
  if (boardPolicy.value?.wip_hard_block) {
    const wip = wipFor(columnKey)
    if (wip?.reached) {
      const message = `${target.column.label} is at its WIP limit of ${wip.limit}. Clear work before pulling more in.`
      toast.warning(message, 'WIP limit')
      if (source === 'keyboard') {
        announce(`Move failed. ${item.key} stays in ${boardColumnFor(item.status).label}. ${message}`)
      }
      return false
    }
  }
  failedMove.value = null
  const moveStartedAt = performance.now()
  pendingMoves[item.id] = {
    fromStatus: item.status,
    toStatus: target.status,
    columnKey: target.column.key,
    columnLabel: target.column.label,
    source,
  }
  revealItem(item.id, target.column.key)
  await nextTick()
  boardPerf.lastMoveMs = roundMs(performance.now() - moveStartedAt)
  if (source === 'keyboard') {
    announcePosition(item, target.column.key, target.column.label)
    await focusItem(item.id)
  }
  try {
    await api.changeWorkItemStatus(store.projectId, item.id, target.status, item.revision)
    await store.refresh()
    delete pendingMoves[item.id]
    if (source !== 'bulk') {
      toast.success(`${item.key} moved to ${target.column.label}.`, 'Board updated')
    }
    return true
  } catch (error) {
    delete pendingMoves[item.id]
    await store.refresh()
    const message = isRevisionConflict(error)
      ? revisionConflictMessage('work item')
      : error instanceof Error
        ? error.message
        : 'Could not move work item'
    if (source === 'keyboard') {
      announce(
        `Move failed. ${item.key} returned to ${boardColumnFor(item.status).label}. ${message}`,
      )
    }
    if (source === 'bulk') throw error
    failedMove.value = {
      itemId: item.id,
      itemKey: item.key,
      columnKey,
      columnLabel: target.column.label,
      message,
    }
    toast.error(message, 'Board')
    return false
  }
}

async function retryFailedMove(): Promise<void> {
  const failed = failedMove.value
  if (!failed) return
  const item = store.workItemById(failed.itemId)
  failedMove.value = null
  if (!item) {
    toast.warning('That work item no longer exists. Refresh the board.', 'Board')
    return
  }
  await requestMove(item, failed.columnKey, 'drag')
}

async function bulkMove(): Promise<void> {
  const columnKey = bulkTargetColumn.value
  if (!columnKey || bulkBusy.value) return
  const columnLabel = boardColumnLabel(columnKey)
  const items = selectedItems.value.filter((item) => !isPending(item.id))
  if (!items.length) return
  bulkBusy.value = true
  failedMove.value = null
  const entries: BoardMoveResult[] = []
  try {
    for (const item of items) {
      const target = moveTargetForColumn(item, columnKey)
      if (!target || target.column.key === boardColumnFor(item.status).key) {
        entries.push({
          itemId: item.id,
          key: item.key,
          title: item.title,
          outcome: 'skipped',
          message: target ? 'Already in this column.' : `No legal transition from ${statusLabel(item.status)}.`,
        })
        continue
      }
      pendingMoves[item.id] = {
        fromStatus: item.status,
        toStatus: target.status,
        columnKey: target.column.key,
        columnLabel: target.column.label,
        source: 'bulk',
      }
      try {
        await api.changeWorkItemStatus(store.projectId, item.id, target.status, item.revision)
        entries.push({
          itemId: item.id,
          key: item.key,
          title: item.title,
          outcome: 'moved',
          message: `Moved to ${target.column.label}.`,
        })
      } catch (error) {
        delete pendingMoves[item.id]
        entries.push({
          itemId: item.id,
          key: item.key,
          title: item.title,
          outcome: 'failed',
          message: isRevisionConflict(error)
            ? revisionConflictMessage('work item')
            : error instanceof Error
              ? error.message
              : 'Could not move work item',
        })
      }
    }
    await store.refresh()
    for (const entry of entries) delete pendingMoves[entry.itemId]
    const summary = summarizeBoardMoves(entries)
    bulkResult.value = { columnKey, columnLabel, entries }
    bulkResultOpen.value = true
    selectedIds.value = entries
      .filter((entry) => entry.outcome === 'failed')
      .map((entry) => entry.itemId)
    if (summary.failed) {
      toast.warning(
        `${summary.moved} moved, ${summary.failed} failed, ${summary.skipped} skipped.`,
        'Bulk move',
      )
    } else {
      toast.success(
        `${summary.moved} work item${summary.moved === 1 ? '' : 's'} moved to ${columnLabel}.`,
        'Bulk move',
      )
    }
    announce(
      `Bulk move finished. ${summary.moved} moved, ${summary.failed} failed, ${summary.skipped} skipped.`,
    )
  } finally {
    bulkBusy.value = false
  }
}

function retryFailedBulk(): void {
  const result = bulkResult.value
  if (!result) return
  const failedIds = result.entries
    .filter((entry) => entry.outcome === 'failed')
    .map((entry) => entry.itemId)
  if (!failedIds.length) return
  selectedIds.value = failedIds
  bulkTargetColumn.value = result.columnKey
  bulkResultOpen.value = false
  void bulkMove()
}

function currentFilters(): BoardFilters {
  return {
    search: search.value,
    type: typeFilter.value,
    feature: featureFilter.value,
    sprint: sprintFilter.value,
    label: labelFilter.value,
    custom: { ...customFilters },
  }
}

function applyFilters(filters: BoardFilters): void {
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
  searchInput.value = filters.search
  search.value = filters.search
  typeFilter.value = filters.type
  featureFilter.value = filters.feature
  sprintFilter.value = filters.sprint
  labelFilter.value = filters.label
  for (const key of Object.keys(customFilters)) delete customFilters[key]
  Object.assign(customFilters, filters.custom)
}

function applyLayout(layout: { swimlane: string; sort: BoardSort; fields: string[]; quick: BoardQuickFilter[] }): void {
  swimlane.value = layout.swimlane
  sort.value = { ...layout.sort }
  visibleFields.value = [...layout.fields]
  quickFilters.value = [...layout.quick]
}

function currentSortId(): string {
  return boardSortId(sort.value)
}

function syncQuery(): void {
  const query: Record<string, string> = { tab: 'board' }
  if (typeof route.query.item === 'string') query.item = route.query.item
  if (selectedViewId.value) query.view = selectedViewId.value
  if (swimlane.value !== 'none') query.swimlane = swimlane.value
  if (currentSortId() !== 'priority:asc') query.sort = currentSortId()
  if (visibleFields.value.length !== BOARD_VISIBLE_FIELDS.length) query.fields = visibleFieldsToSaved(visibleFields.value).join(',')
  if (quickFilters.value.length) query.quick = quickFiltersToSaved(quickFilters.value).join(',')
  Object.assign(query, boardFiltersToQuery(currentFilters(), boardFields.value))
  void router.replace({ name: 'workspace', params: { id: store.projectId }, query })
}

function onFiltersChanged(): void {
  selectedViewId.value = ''
  clearSelection()
  syncQuery()
}

// Typing is debounced so a 2,000-card board does not re-filter on every
// keystroke. The input stays responsive; the board and URL settle once.
watch(searchInput, (value) => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = null
    if (search.value === value) return
    search.value = value
    onFiltersChanged()
  }, BOARD_FILTER_DEBOUNCE_MS)
})

function onLayoutChanged(): void {
  selectedViewId.value = ''
  clearSelection()
  syncQuery()
}

function applySortId(id: string): void {
  sort.value = boardSortFromId(id)
  onLayoutChanged()
}

function toggleQuick(value: BoardQuickFilter): void {
  quickFilters.value = toggleQuickFilter(quickFilters.value, value)
  onFiltersChanged()
}

function applyView(filter: SavedFilter): void {
  const state = boardViewFromSaved(filter, boardFields.value)
  applyFilters(state.filters)
  applyLayout(state)
}

function applySavedView(id: string): void {
  selectedViewId.value = id
  clearSelection()
  if (!id) {
    clearFilters()
    return
  }
  const filter = store.savedFilters.find((entry) => entry.id === id)
  if (!filter) return
  applyView(filter)
  syncQuery()
}

const initialFilters = boardFiltersFromQuery(route.query, boardFields.value)
applyFilters(initialFilters)
applyLayout({
  swimlane:
    typeof route.query.swimlane === 'string' && BOARD_SWIMLANES.some((entry) => entry.value === route.query.swimlane)
      ? route.query.swimlane
      : 'none',
  sort: boardSortFromId(typeof route.query.sort === 'string' ? route.query.sort : ''),
  fields: visibleFieldsFromValue(route.query.fields),
  quick: quickFiltersFromValue(route.query.quick),
})
const initialView = typeof route.query.view === 'string' ? route.query.view : ''
const requestedView = store.savedFilters.find(
  (filter) => filter.id === initialView && filter.scope === boardScope,
)
if (requestedView) {
  selectedViewId.value = requestedView.id
  applyView(requestedView)
} else if (!initialView) {
  // The default view is per user and is applied on every load, so a reload
  // restores the same layout without requiring the view id in the URL.
  const fallback = defaultBoardView(boardViews.value, auth.user?.id ?? '')
  if (fallback) {
    selectedViewId.value = fallback.id
    applyView(fallback)
  }
}

watch(
  () => route.query.feature,
  (value) => {
    featureFilter.value = typeof value === 'string' ? value : ''
  },
)

const members = computed(() => store.workspace?.members ?? [])
const features = computed(() => store.features)
const sprints = computed(() => store.sprints)
const activeFilterCount = computed(() => boardFilterCount(currentFilters()) + quickFilters.value.length)

// Lookup maps keep per-card work O(1). Without them a 2,000-card board scans
// every feature, branch, pull request and custom value for every rendered card.
const featureTitles = computed(() => new Map(store.features.map((feature) => [feature.id, feature.title])))
const workItemsById = computed(() => new Map(store.workItems.map((item) => [item.id, item])))
const customValuesByItem = computed(() => {
  const map = new Map<string, Record<string, string | number | null>>()
  for (const value of store.customFieldValues) {
    if (value.entity_type !== 'work_item') continue
    let bucket = map.get(value.entity_id)
    if (!bucket) {
      bucket = {}
      map.set(value.entity_id, bucket)
    }
    bucket[value.field_id] = value.value
  }
  return map
})
const branchesByItem = computed(() => {
  const map = new Map<string, GitBranch[]>()
  for (const branch of store.branches) {
    if (!branch.work_item_id) continue
    const bucket = map.get(branch.work_item_id)
    if (bucket) bucket.push(branch)
    else map.set(branch.work_item_id, [branch])
  }
  return map
})
const prsByItem = computed(() => {
  const map = new Map<string, PullRequest[]>()
  for (const pr of store.pullRequests) {
    if (!pr.work_item_id) continue
    const bucket = map.get(pr.work_item_id)
    if (bucket) bucket.push(pr)
    else map.set(pr.work_item_id, [pr])
  }
  return map
})
const wipCounts = computed(() => boardWipCounts(effectiveItems.value))

function customValueSet(itemId: string): Record<string, string | number | null> {
  return customValuesByItem.value.get(itemId) ?? {}
}

function cardLabels(item: WorkItem): string[] {
  return (item.labels ?? []).slice(0, 3)
}

function cardCustomValues(item: WorkItem): Array<{ field: CustomField; label: string }> {
  const values = customValueSet(item.id)
  return store.customFields
    .filter((field) => field.type === 'select')
    .map((field) => ({ field, label: customFieldDisplay(field, values[field.id]) }))
    .filter((entry) => entry.label)
    .slice(0, 2)
}

const filteredItems = computed(() => {
  const filters = currentFilters()
  const quickContext = { userId: auth.user?.id ?? '' }
  return effectiveItems.value.filter((item) => {
    const featureTitle = item.feature_id ? featureTitles.value.get(item.feature_id) ?? '' : ''
    if (!workItemMatchesFilters(item, filters, boardFields.value, customValueSet(item.id), featureTitle)) {
      return false
    }
    return itemMatchesQuickFilters(item, quickFilters.value, quickContext)
  })
})

function wipFor(columnKey: string) {
  const limit = boardPolicy.value?.wip_limits?.[columnKey] ?? 0
  return wipState(limit, wipCounts.value[columnKey] ?? 0)
}

// lanes renders the board once for swimlane "none" and once per bucket
// otherwise. buildBoardLanes groups and sorts in one pass; columns always come
// from the shared board definition so a lane cannot drift from the header order.
const lanes = computed(() =>
  buildBoardLanes(filteredItems.value, swimlane.value, memberName, sort.value).map((lane) => ({
    ...lane,
    columns: lane.columns.map(({ column, items }) => ({
      ...column,
      wip: wipFor(column.key),
      items,
    })),
  })),
)

function visibleColumnCount(laneKey: string, columnKey: string, total: number): number {
  return Math.min(total, visibleCounts[`${laneKey}:${columnKey}`] ?? BOARD_COLUMN_PAGE_SIZE)
}

function showMore(laneKey: string, columnKey: string, total: number): void {
  const key = `${laneKey}:${columnKey}`
  visibleCounts[key] = nextVisibleCount(visibleCounts[key] ?? 0, total)
}

// A keyboard or drag move can land beyond the first page of the target column.
// Reveal enough cards to keep the moved card focusable instead of silently
// dropping focus when the page boundary hides it.
function revealItem(itemId: string, columnKey: string): void {
  for (const lane of lanes.value) {
    const column = lane.columns.find((entry) => entry.key === columnKey)
    if (!column) continue
    const position = column.items.findIndex((item) => item.id === itemId)
    if (position < 0) continue
    const key = `${lane.key}:${columnKey}`
    visibleCounts[key] = Math.max(visibleCounts[key] ?? BOARD_COLUMN_PAGE_SIZE, position + 1)
    return
  }
}

const layoutSignature = computed(() =>
  JSON.stringify({
    filters: currentFilters(),
    quick: quickFilters.value,
    sort: sort.value,
    swimlane: swimlane.value,
  }),
)

watch(layoutSignature, () => {
  for (const key of Object.keys(visibleCounts)) delete visibleCounts[key]
})

function roundMs(value: number): number {
  return Math.round(value * 10) / 10
}

watch(
  () => store.workItems.length,
  async (count) => {
    if (firstRenderMeasured || count === 0) return
    firstRenderMeasured = true
    await nextTick()
    boardPerf.renderMs = roundMs(performance.now() - mountedAt)
  },
  { immediate: true },
)

onUnmounted(() => {
  if (searchTimer) clearTimeout(searchTimer)
})

function dueLabel(item: WorkItem): string {
  if (!item.due_date) return ''
  const date = new Date(item.due_date)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function dueTone(item: WorkItem): string {
  if (!item.due_date) return ''
  const date = new Date(item.due_date)
  if (Number.isNaN(date.getTime())) return ''
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return date.getTime() < today.getTime() ? 'bg-red-50 text-red-700 ring-red-200' : 'bg-slate-100 text-slate-600 ring-slate-200'
}

const stats = computed(() => {
  let active = 0
  let bugs = 0
  let blocked = 0
  for (const item of effectiveItems.value) {
    if (['in_progress', 'in_review', 'in_qa'].includes(item.status)) active += 1
    if (item.type === 'bug' && !['closed', 'verified', 'cancelled'].includes(item.status)) bugs += 1
    if (item.status === 'blocked') blocked += 1
  }
  return { total: effectiveItems.value.length, active, bugs, blocked }
})

function featureName(featureId: string | null): string {
  if (!featureId) return 'No feature'
  return featureTitles.value.get(featureId) ?? 'Unknown feature'
}

function parentItem(parentId: string | null): WorkItem | undefined {
  if (!parentId) return undefined
  return workItemsById.value.get(parentId)
}

function openParent(parentId: string | null): void {
  if (parentId) openDrawerById(parentId)
}

function filterByFeature(featureId: string | null): void {
  if (!featureId) return
  featureFilter.value = featureId
  selectedViewId.value = ''
  itemModal.value = false
  syncQuery()
}

function labelColor(name: string): string {
  return colorForLabel(name, boardLabels.value)
}

function memberName(userId: string | null): string {
  if (!userId) return ''
  const member = members.value.find((entry) => entry.user_id === userId)
  return member?.username || member?.email || 'Team member'
}

function initials(userId: string | null): string {
  const name = memberName(userId)
  if (name) {
    const parts = name.split(/[\s._-]+/).filter(Boolean)
    return (parts.length > 1 ? `${parts[0]![0]}${parts[1]![0]}` : name.slice(0, 2)).toUpperCase()
  }
  return '—'
}

function typeIcon(type: string): string {
  switch (type) {
    case 'story':
      return 'layers'
    case 'task':
      return 'check-circle'
    case 'subtask':
      return 'check'
    case 'bug':
      return 'alert'
    case 'spike':
      return 'sparkles'
    default:
      return 'server'
  }
}

function typeTone(type: string): string {
  switch (type) {
    case 'story':
      return 'bg-indigo-50 text-indigo-600'
    case 'task':
      return 'bg-sky-50 text-sky-700'
    case 'subtask':
      return 'bg-slate-100 text-slate-600'
    case 'bug':
      return 'bg-red-50 text-red-600'
    case 'spike':
      return 'bg-violet-50 text-violet-600'
    default:
      return 'bg-amber-50 text-amber-700'
  }
}

function branchesFor(itemId: string): GitBranch[] {
  return branchesByItem.value.get(itemId) ?? []
}

function prsFor(itemId: string): PullRequest[] {
  return prsByItem.value.get(itemId) ?? []
}

function clearFilters(): void {
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
  searchInput.value = ''
  search.value = ''
  typeFilter.value = ''
  sprintFilter.value = ''
  featureFilter.value = ''
  labelFilter.value = ''
  selectedViewId.value = ''
  quickFilters.value = []
  clearSelection()
  for (const key of Object.keys(customFilters)) delete customFilters[key]
  syncQuery()
}

// ---------------------------------------------------------------------------
// Saved views
// ---------------------------------------------------------------------------

const viewModal = ref(false)
const viewName = ref('')
const viewVisibility = ref<'private' | 'shared'>('private')
const viewPinned = ref(false)
const viewDefault = ref(false)
const viewSwimlane = ref('none')
const viewSortId = ref('priority:asc')
const viewFields = ref<string[]>([])
const viewBusy = ref(false)
const viewError = ref('')
const editingViewId = ref('')
const deleteViewId = ref('')
const deletingView = ref(false)

const selectedView = computed(() =>
  boardViews.value.find((filter) => filter.id === selectedViewId.value),
)
const editingView = computed(() =>
  boardViews.value.find((filter) => filter.id === editingViewId.value),
)
const canEditSelectedView = computed(() =>
  canManageSavedFilter(
    selectedView.value,
    auth.user?.id ?? '',
    auth.projectRole(store.projectId),
    auth.can(store.projectId, 'settings.write'),
  ),
)
const canShareView = computed(() => auth.can(store.projectId, 'planning.write'))

function openSaveView(asNew = false): void {
  const current = asNew ? undefined : selectedView.value
  editingViewId.value = current?.id ?? ''
  viewName.value = current?.name ?? ''
  viewVisibility.value = current?.visibility === 'shared' ? 'shared' : 'private'
  viewPinned.value = current?.pinned ?? false
  viewDefault.value = current?.is_default ?? false
  viewSwimlane.value = swimlane.value
  viewSortId.value = currentSortId()
  viewFields.value = [...visibleFields.value]
  viewError.value = ''
  viewModal.value = true
}

async function saveView(): Promise<void> {
  if (viewBusy.value) return
  if (!viewName.value.trim()) {
    viewError.value = 'A view name is required.'
    return
  }
  if (viewVisibility.value === 'shared' && !canShareView.value) {
    viewError.value = 'Shared views need planning write access.'
    return
  }
  viewBusy.value = true
  viewError.value = ''
  try {
    swimlane.value = viewSwimlane.value
    sort.value = boardSortFromId(viewSortId.value)
    visibleFields.value = [...viewFields.value]
    const filters: Record<string, unknown> = boardFiltersToSaved(currentFilters(), boardFields.value)
    filters.quick = quickFiltersToSaved(quickFilters.value)
    const payload = {
      name: viewName.value.trim(),
      scope: boardScope,
      visibility: viewVisibility.value,
      filters,
      sort: boardSortToSaved(sort.value),
      columns: visibleFieldsToSaved(visibleFields.value),
      swimlane: swimlane.value,
      pinned: viewPinned.value,
      is_default: viewDefault.value,
    }
    let saved: SavedFilter
    if (editingView.value) {
      saved = await api.updateSavedFilter(
        store.projectId,
        editingView.value.id,
        payload,
        editingView.value.revision,
      )
      toast.success('Saved view updated.', 'Board views')
    } else {
      saved = await api.createSavedFilter(store.projectId, payload)
      toast.success('Saved view created.', 'Board views')
    }
    await store.refresh()
    selectedViewId.value = saved.id
    viewModal.value = false
    syncQuery()
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      viewError.value = revisionConflictMessage('saved view')
      return
    }
    viewError.value = error instanceof Error ? error.message : 'Failed to save view'
  } finally {
    viewBusy.value = false
  }
}

async function deleteView(): Promise<void> {
  if (!deleteViewId.value || deletingView.value) return
  deletingView.value = true
  try {
    await api.deleteSavedFilter(store.projectId, deleteViewId.value)
    await store.refresh()
    if (selectedViewId.value === deleteViewId.value) selectedViewId.value = ''
    deleteViewId.value = ''
    toast.success('Saved view deleted.', 'Board views')
    syncQuery()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to delete view', 'Board views')
  } finally {
    deletingView.value = false
  }
}

// ---------------------------------------------------------------------------
// Work item form
// ---------------------------------------------------------------------------

const itemModal = ref(false)
const itemEditingId = ref<string | null>(null)
const itemBusy = ref(false)
const itemError = ref('')
const branchRepoId = ref('')
const branchName = ref('')
const branchBusy = ref(false)
const deleteOpen = ref(false)

const itemForm = reactive({
  type: 'task',
  title: '',
  description: '',
  acceptance_criteria: '',
  status: 'backlog',
  priority: 'medium',
  feature_id: '',
  parent_id: '',
  sprint_id: '',
  release_id: '',
  environment_id: '',
  assignee_id: '',
  estimate_points: '',
  due_date: '',
  severity: '',
  reproduction_steps: '',
  expected_result: '',
  actual_result: '',
  labels: '',
})

const customDraft = reactive<Record<string, string>>({})

function clearCustomDraft(): void {
  for (const key of Object.keys(customDraft)) delete customDraft[key]
}

function loadCustomDraft(itemId: string): void {
  clearCustomDraft()
  const values = customValueMap(store.customValuesFor(itemId), itemId)
  Object.assign(customDraft, customFieldDraft(store.customFields, values))
}

const selectedLabelNames = computed(() => normalizeLabelInput(itemForm.labels))

const selectedItem = computed(() =>
  itemEditingId.value ? store.workItemById(itemEditingId.value) : undefined,
)

const editing = computed(() => Boolean(itemEditingId.value && selectedItem.value))

// Capability gate. Testers hold work_item.write for defects and status moves
// but cannot edit or delete existing work items; the API enforces the same
// rules, so a stale client still cannot escalate.
const projectId = computed(() => store.projectId)
const canWriteWorkItems = computed(() => auth.can(projectId.value, 'work_item.write'))
const isTester = computed(() => auth.projectRole(projectId.value) === 'tester')
const canManageWorkItems = computed(() => canWriteWorkItems.value && !isTester.value)
const canLinkBranch = computed(() => auth.can(projectId.value, 'code.write'))
const formDisabled = computed(() => editing.value && !canManageWorkItems.value)

const linkedBranches = computed(() => (selectedItem.value ? branchesFor(selectedItem.value.id) : []))
const linkedPrs = computed(() => (selectedItem.value ? prsFor(selectedItem.value.id) : []))

const featureRequired = computed(() => ['story', 'task', 'subtask'].includes(itemForm.type))
const bugNeedsLink = computed(() => itemForm.type === 'bug' && !itemForm.feature_id && !itemForm.parent_id)
const parentRequired = computed(() => itemForm.type === 'subtask')

const parentCandidates = computed(() => {
  const candidates = store.workItems.filter((item) => item.id !== itemEditingId.value)
  switch (itemForm.type) {
    case 'story':
      return []
    case 'task':
      return candidates.filter((item) => item.type === 'story')
    case 'subtask':
      return candidates.filter((item) => item.type === 'task' || item.type === 'story')
    case 'bug':
      return candidates
    default:
      return candidates
  }
})

const transitionOptions = computed(() => {
  if (!selectedItem.value) return []
  return allowedTransitions(selectedItem.value.type, selectedItem.value.status)
})

function resetItemForm(): void {
  Object.assign(itemForm, {
    type: 'task',
    title: '',
    description: '',
    acceptance_criteria: '',
    status: 'backlog',
    priority: 'medium',
    feature_id: featureFilter.value || features.value[0]?.id || '',
    parent_id: '',
    sprint_id: '',
    release_id: '',
    environment_id: '',
    assignee_id: '',
    estimate_points: '',
    due_date: '',
    severity: '',
    reproduction_steps: '',
    expected_result: '',
    actual_result: '',
    labels: '',
  })
  clearCustomDraft()
}

function openCreateItem(type = 'task', columnKey = ''): void {
  if (isTester.value) type = 'bug'
  itemEditingId.value = null
  resetItemForm()
  itemForm.type = type
  itemForm.status = columnKey ? targetStatusForColumn(type, columnKey) : defaultStatusFor(type)
  itemError.value = ''
  itemModal.value = true
}

function openEditItem(item: WorkItem): void {
  itemEditingId.value = item.id
  Object.assign(itemForm, {
    type: item.type,
    title: item.title,
    description: item.description,
    acceptance_criteria: item.acceptance_criteria,
    status: item.status,
    priority: item.priority,
    feature_id: item.feature_id ?? '',
    parent_id: item.parent_id ?? '',
    sprint_id: item.sprint_id ?? '',
    release_id: item.release_id ?? '',
    environment_id: item.environment_id ?? '',
    assignee_id: item.assignee_id ?? '',
    estimate_points: item.estimate_points === null ? '' : String(item.estimate_points),
    due_date: dateInputValue(item.due_date),
    severity: item.severity ?? '',
    reproduction_steps: item.reproduction_steps,
    expected_result: item.expected_result,
    actual_result: item.actual_result,
    labels: (item.labels ?? []).join(', '),
  })
  loadCustomDraft(item.id)
  itemError.value = ''
  branchRepoId.value = store.repositories[0]?.id ?? ''
  branchName.value = branchNameFor(item)
  itemModal.value = true
}

// ---------------------------------------------------------------------------
// Work item detail drawer
// ---------------------------------------------------------------------------

const drawerOpen = ref(false)
const drawerItemId = ref(typeof route.query.item === 'string' ? route.query.item : '')

function setItemQuery(id: string | null): void {
  const query = { ...route.query }
  if (id) query.item = id
  else delete query.item
  void router.replace({ query })
}

function openItemDrawer(item: WorkItem): void {
  drawerItemId.value = item.id
  drawerOpen.value = true
  if (route.query.item !== item.id) setItemQuery(item.id)
}

function openDrawerById(id: string): void {
  const item = store.workItemById(id)
  if (item) {
    openItemDrawer(item)
    return
  }
  drawerItemId.value = id
  drawerOpen.value = true
  setItemQuery(id)
}

function closeDrawer(): void {
  drawerOpen.value = false
  if (route.query.item) setItemQuery(null)
}

function onDrawerVisibility(value: boolean): void {
  if (value) {
    drawerOpen.value = true
    return
  }
  closeDrawer()
}

function editFromDrawer(item: WorkItem): void {
  drawerOpen.value = false
  if (route.query.item) setItemQuery(null)
  openEditItem(item)
}

watch(
  () => route.query.item,
  (value) => {
    const id = typeof value === 'string' ? value : ''
    drawerItemId.value = id
    drawerOpen.value = Boolean(id)
  },
  { immediate: true },
)

watch(
  () => store.workItems.length,
  () => {
    const id = typeof route.query.item === 'string' ? route.query.item : ''
    if (id && store.workItemById(id)) {
      drawerItemId.value = id
      drawerOpen.value = true
    }
  },
)

watch(
  () => store.workItems.map((item) => item.id).join(','),
  () => {
    const live = new Set(store.workItems.map((item) => item.id))
    const next = selectedIds.value.filter((id) => live.has(id))
    if (next.length !== selectedIds.value.length) selectedIds.value = next
  },
)

function onTypeChange(): void {
  if (!workItemStatusOptions(itemForm.type).includes(itemForm.status)) {
    itemForm.status = defaultStatusFor(itemForm.type)
  }
  if (itemForm.type === 'story') itemForm.parent_id = ''
  if (itemForm.type !== 'bug') {
    itemForm.severity = ''
  }
  if (!parentCandidates.value.some((item) => item.id === itemForm.parent_id)) {
    itemForm.parent_id = ''
  }
}

async function saveItem(): Promise<void> {
  if (formDisabled.value) return
  if (!itemForm.title.trim()) {
    itemError.value = 'A work item title is required.'
    return
  }
  if (featureRequired.value && !itemForm.feature_id) {
    itemError.value = 'This work item type must link to a feature.'
    return
  }
  if (bugNeedsLink.value) {
    itemError.value = 'A bug must link to a feature or a parent work item.'
    return
  }
  if (parentRequired.value && !itemForm.parent_id) {
    itemError.value = 'A subtask must have a parent task or story.'
    return
  }
  const missingField = missingRequiredCustomField(store.customFields, customDraft)
  if (missingField) {
    itemError.value = `${missingField} is required.`
    return
  }

  itemBusy.value = true
  itemError.value = ''
  try {
    const estimate = String(itemForm.estimate_points ?? '').trim()
    const payload = {
      feature_id: itemForm.feature_id || null,
      parent_id: itemForm.type === 'story' ? null : itemForm.parent_id || null,
      type: itemForm.type,
      title: itemForm.title.trim(),
      description: itemForm.description,
      acceptance_criteria: itemForm.acceptance_criteria,
      status: itemForm.status,
      priority: itemForm.priority,
      severity: itemForm.type === 'bug' ? itemForm.severity || null : null,
      assignee_id: itemForm.assignee_id || null,
      reporter_id: null,
      estimate_points: estimate ? Number(estimate) : null,
      sprint_id: itemForm.sprint_id || null,
      release_id: itemForm.release_id || null,
      environment_id: itemForm.environment_id || null,
      due_date: itemForm.due_date || null,
      reproduction_steps: itemForm.reproduction_steps,
      expected_result: itemForm.expected_result,
      actual_result: itemForm.actual_result,
      labels: normalizeLabelInput(itemForm.labels),
    }
    let savedId = itemEditingId.value
    if (itemEditingId.value) {
      const current = store.workItemById(itemEditingId.value)
      if (!current) {
        itemError.value = 'This work item no longer exists. Close the dialog and refresh.'
        return
      }
      await api.updateWorkItem(store.projectId, itemEditingId.value, payload, current.revision)
      toast.success('Work item updated.', 'Board')
    } else {
      const result = await api.createWorkItem(store.projectId, payload)
      savedId = result.id
      toast.success(`${result.key ?? 'Work item'} created and linked.`, 'Board')
      itemEditingId.value = result.id
    }
    if (savedId && store.customFields.length) {
      await api.putWorkItemCustomFields(
        store.projectId,
        savedId,
        customFieldPayload(store.customFields, customDraft),
      )
    }
    await store.refresh()
    itemModal.value = false
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      itemError.value = revisionConflictMessage('work item')
      return
    }
    itemError.value = error instanceof Error ? error.message : 'Failed to save work item'
  } finally {
    itemBusy.value = false
  }
}

async function transition(status: string): Promise<void> {
  const item = selectedItem.value
  if (!item || itemBusy.value) return
  itemBusy.value = true
  try {
    await api.changeWorkItemStatus(store.projectId, item.id, status, item.revision)
    itemForm.status = status
    await store.refresh()
    toast.success(`${item.key} is now ${statusLabel(status)}.`, 'Workflow')
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      toast.warning(revisionConflictMessage('work item'), 'Workflow')
      return
    }
    toast.error(error instanceof Error ? error.message : 'Status transition failed', 'Workflow')
  } finally {
    itemBusy.value = false
  }
}

async function createBranch(): Promise<void> {
  const item = selectedItem.value
  if (!item || branchBusy.value) return
  if (!branchRepoId.value) {
    toast.warning('Link a repository in Delivery before creating a branch.', 'No repository')
    return
  }
  if (!branchName.value.trim()) {
    toast.warning('A branch name is required.', 'Branch')
    return
  }
  branchBusy.value = true
  try {
    await api.createBranch(store.projectId, {
      repository_id: branchRepoId.value,
      work_item_id: item.id,
      name: branchName.value.trim(),
      head_commit_sha: '',
      created_by: null,
    })
    await store.refresh()
    toast.success(`Branch ${branchName.value.trim()} linked to ${item.key}.`, 'Git')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Could not create branch', 'Git')
  } finally {
    branchBusy.value = false
  }
}

async function deleteItem(): Promise<void> {
  const item = selectedItem.value
  if (!item) return
  itemBusy.value = true
  try {
    await api.deleteWorkItem(store.projectId, item.id, item.revision)
    await store.refresh()
    deleteOpen.value = false
    itemModal.value = false
    toast.success(`${item.key} deleted.`, 'Board')
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      toast.warning(revisionConflictMessage('work item'), 'Board')
      return
    }
    toast.error(error instanceof Error ? error.message : 'Could not delete work item', 'Board')
  } finally {
    itemBusy.value = false
  }
}

function openDelivery(): void {
  void router.push({ name: 'workspace', params: { id: store.projectId }, query: { tab: 'delivery' } })
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight text-slate-900">Delivery board</h2>
      </div>
      <div class="flex flex-wrap gap-2">
        <button v-if="canWriteWorkItems" type="button" class="btn-secondary" @click="openCreateItem('bug')">
          <AppIcon name="alert" :size="15" />
          New bug
        </button>
        <button v-if="canManageWorkItems" type="button" class="btn-primary" @click="openCreateItem('task')">
          <AppIcon name="plus" :size="15" />
          New work item
        </button>
      </div>
    </div>

    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <div class="card flex items-center gap-3 px-4 py-3">
        <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-slate-100 text-slate-600"><AppIcon name="board" :size="16" /></span>
        <div><p class="text-lg font-semibold leading-none text-slate-900">{{ stats.total }}</p><p class="mt-1 text-[11px] text-slate-600">Total work items</p></div>
      </div>
      <div class="card flex items-center gap-3 px-4 py-3">
        <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-amber-50 text-amber-700"><AppIcon name="activity" :size="16" /></span>
        <div><p class="text-lg font-semibold leading-none text-slate-900">{{ stats.active }}</p><p class="mt-1 text-[11px] text-slate-600">In delivery</p></div>
      </div>
      <div class="card flex items-center gap-3 px-4 py-3">
        <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-red-50 text-red-600"><AppIcon name="alert" :size="16" /></span>
        <div><p class="text-lg font-semibold leading-none text-slate-900">{{ stats.bugs }}</p><p class="mt-1 text-[11px] text-slate-600">Open defects</p></div>
      </div>
      <div class="card flex items-center gap-3 px-4 py-3">
        <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-violet-50 text-violet-600"><AppIcon name="lock" :size="16" /></span>
        <div><p class="text-lg font-semibold leading-none text-slate-900">{{ stats.blocked }}</p><p class="mt-1 text-[11px] text-slate-600">Blocked</p></div>
      </div>
    </div>

    <div class="card flex flex-wrap items-center gap-2 p-3">
      <select
        :value="selectedViewId"
        class="field-input w-auto min-w-[170px]"
        aria-label="Saved board view"
        @change="applySavedView(($event.target as HTMLSelectElement).value)"
      >
        <option value="">All work items</option>
        <option v-for="view in boardViews" :key="view.id" :value="view.id">
          {{ view.pinned ? '★ ' : '' }}{{ view.name }}{{ view.visibility === 'shared' ? ' · shared' : '' }}
        </option>
      </select>
      <button type="button" class="btn-secondary" @click="openSaveView()">
        <AppIcon name="filter" :size="14" /> {{ selectedView ? 'Save view' : 'Save current view' }}
      </button>
      <button
        v-if="selectedView"
        type="button"
        class="btn-ghost"
        title="Save the current filters as a new view"
        @click="openSaveView(true)"
      >
        <AppIcon name="plus" :size="14" />
      </button>
      <button
        v-if="selectedView && canEditSelectedView"
        type="button"
        class="btn-ghost text-red-600 hover:bg-red-50"
        aria-label="Delete saved view"
        @click="deleteViewId = selectedView.id"
      >
        <AppIcon name="trash" :size="14" />
      </button>
      <select
        v-model="swimlane"
        class="field-input w-auto min-w-[150px]"
        aria-label="Swimlane grouping"
        @change="onLayoutChanged"
      >
        <option v-for="lane in BOARD_SWIMLANES" :key="lane.value" :value="lane.value">{{ lane.label }}</option>
      </select>
      <select
        :value="currentSortId()"
        class="field-input w-auto min-w-[170px]"
        aria-label="Work item sort"
        @change="applySortId(($event.target as HTMLSelectElement).value)"
      >
        <option v-for="option in BOARD_SORT_OPTIONS" :key="option.id" :value="option.id">{{ option.label }}</option>
      </select>
      <div class="relative min-w-[190px] flex-1">
        <AppIcon name="search" :size="15" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-600" />
        <input
          v-model="searchInput"
          class="field-input pl-9"
          placeholder="Search key, title or feature…"
          aria-label="Search work items"
          data-testid="board-search"
        />
      </div>
      <select v-model="typeFilter" class="field-input w-auto min-w-[130px]" aria-label="Filter by work item type" @change="onFiltersChanged">
        <option value="">All types</option>
        <option v-for="(label, type) in workItemTypeLabels" :key="type" :value="type">{{ label }}</option>
      </select>
      <select v-model="featureFilter" class="field-input w-auto min-w-[170px]" aria-label="Filter by feature" @change="onFiltersChanged">
        <option value="">All features</option>
        <option v-for="feature in features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
      </select>
      <select v-model="sprintFilter" class="field-input w-auto min-w-[140px]" aria-label="Filter by sprint" @change="onFiltersChanged">
        <option value="">All sprints</option>
        <option value="none">No sprint</option>
        <option v-for="sprint in sprints" :key="sprint.id" :value="sprint.id">{{ sprint.name }}</option>
      </select>
      <select v-model="labelFilter" class="field-input w-auto min-w-[130px]" aria-label="Filter by label" @change="onFiltersChanged">
        <option value="">All labels</option>
        <option v-for="label in boardLabels" :key="label.id" :value="label.name">{{ label.name }}</option>
      </select>
      <template v-for="field in boardFields" :key="field.id">
        <select
          v-if="field.type === 'select'"
          v-model="customFilters[field.id]"
          class="field-input w-auto min-w-[150px]"
          :aria-label="`Filter by ${field.name}`"
          @change="onFiltersChanged"
        >
          <option value="">Any {{ field.name }}</option>
          <option v-for="option in field.options" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
        <select
          v-else-if="field.type === 'user'"
          v-model="customFilters[field.id]"
          class="field-input w-auto min-w-[150px]"
          :aria-label="`Filter by ${field.name}`"
          @change="onFiltersChanged"
        >
          <option value="">Any {{ field.name }}</option>
          <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
        </select>
        <input
          v-else
          v-model="customFilters[field.id]"
          :type="field.type === 'number' ? 'number' : field.type === 'date' ? 'date' : 'text'"
          class="field-input w-auto min-w-[140px]"
          :aria-label="`Filter by ${field.name}`"
          :placeholder="field.name"
          @change="onFiltersChanged"
        />
      </template>
      <button
        v-if="activeFilterCount"
        type="button"
        class="btn-ghost"
        @click="clearFilters"
      >
        <AppIcon name="close" :size="14" /> Clear ({{ activeFilterCount }})
      </button>
      <div class="flex w-full flex-wrap items-center gap-1.5 border-t border-slate-100 pt-2">
        <span class="mr-1 text-[10px] font-semibold uppercase tracking-wide text-slate-500">Quick filters</span>
        <button
          v-for="entry in BOARD_QUICK_FILTERS"
          :key="entry.value"
          type="button"
          class="rounded-full px-2.5 py-1 text-[11px] font-medium ring-1 transition"
          :class="quickFilters.includes(entry.value)
            ? 'bg-indigo-600 text-white ring-indigo-600'
            : 'bg-white text-slate-600 ring-slate-200 hover:border-indigo-200 hover:text-indigo-700'"
          :aria-pressed="quickFilters.includes(entry.value)"
          @click="toggleQuick(entry.value)"
        >
          {{ entry.label }}
        </button>
        <span v-if="boardPolicy?.wip_hard_block" class="badge bg-amber-50 text-amber-800 ring-amber-200">
          <AppIcon name="lock" :size="11" class="mr-1" /> WIP moves are enforced
        </span>
      </div>
    </div>

    <p class="sr-only" role="status" aria-live="polite" data-testid="board-announcer">{{ announcement }}</p>

    <div
      v-if="failedMove"
      class="card flex flex-wrap items-center gap-3 border-red-200 bg-red-50 p-3"
      role="alert"
      data-testid="board-move-failure"
    >
      <span class="flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-red-100 text-red-700">
        <AppIcon name="alert" :size="15" />
      </span>
      <div class="min-w-0 flex-1">
        <p class="text-xs font-semibold text-red-800">
          Could not move {{ failedMove.itemKey }} to {{ failedMove.columnLabel }}
        </p>
        <p class="mt-0.5 text-[11px] text-red-700">{{ failedMove.message }}</p>
      </div>
      <button type="button" class="btn-secondary" @click="retryFailedMove">
        <AppIcon name="refresh" :size="14" /> Retry
      </button>
      <button type="button" class="btn-ghost" @click="failedMove = null">Dismiss</button>
    </div>

    <div
      v-if="canWriteWorkItems && selectedIds.length"
      class="card flex flex-wrap items-center gap-2 border-indigo-200 bg-indigo-50/60 p-3"
      role="region"
      aria-label="Bulk move selected work items"
      data-testid="board-bulk-bar"
    >
      <span class="text-xs font-semibold text-indigo-900">{{ selectedIds.length }} selected</span>
      <select
        v-model="bulkTargetColumn"
        class="field-input w-auto min-w-[170px]"
        aria-label="Bulk move target column"
      >
        <option value="">Move to…</option>
        <option v-for="column in boardColumns" :key="column.key" :value="column.key">
          {{ column.label }}
        </option>
      </select>
      <button
        type="button"
        class="btn-primary"
        :disabled="!bulkTargetColumn || bulkBusy"
        @click="bulkMove"
      >
        <AppIcon name="arrowRight" :size="14" />
        {{ bulkBusy ? 'Moving…' : 'Move selected' }}
      </button>
      <button type="button" class="btn-ghost" @click="clearSelection">Clear</button>
      <span class="ml-auto text-[10px] text-indigo-700">
        Each item is checked by the server; results are reported per item.
      </span>
    </div>

    <span
      class="sr-only"
      data-testid="board-perf"
      :data-render-ms="boardPerf.renderMs"
      :data-move-ms="boardPerf.lastMoveMs"
      aria-hidden="true"
    ></span>

    <EmptyState
      v-if="!store.workItems.length"
      icon="board"
      title="No work items yet"
      :action-label="canManageWorkItems ? 'Create work item' : canWriteWorkItems ? 'Raise a defect' : ''"
      @action="openCreateItem(canManageWorkItems ? 'task' : 'bug')"
    />

    <div v-else class="space-y-4 overflow-x-auto pb-2" data-testid="board-scroll">
      <div v-for="lane in lanes" :key="lane.key" class="space-y-2">
        <div v-if="lane.label" class="flex items-center gap-2 pl-1">
          <h3 class="text-[11px] font-semibold uppercase tracking-wide text-slate-500">{{ lane.label }}</h3>
          <span class="rounded-full bg-white px-1.5 py-0.5 text-[10px] font-medium text-slate-500 ring-1 ring-slate-200">
            {{ lane.items.length }}
          </span>
        </div>
        <div class="flex min-w-[1180px] gap-3">
          <section
            v-for="column in lane.columns"
            :key="`${lane.key}:${column.key}`"
            class="flex w-[230px] flex-none flex-col rounded-xl border border-slate-200 bg-slate-100/70 transition"
            :class="dropTarget === `${lane.key}:${column.key}`
              ? 'border-indigo-300 bg-indigo-50/60 ring-2 ring-indigo-300'
              : ''"
            :data-board-column="`${lane.key}:${column.key}`"
            :data-column-key="column.key"
            :aria-label="`${column.label} column`"
          >
            <div class="flex items-center gap-2 px-3 pb-2 pt-3">
              <span class="h-2 w-2 rounded-full" :class="column.accent" />
              <h3 class="text-xs font-semibold text-slate-700">{{ column.label }}</h3>
              <span
                v-if="column.wip"
                class="rounded-full px-1.5 py-0.5 text-[10px] font-semibold ring-1"
                :class="column.wip.exceeded
                  ? 'bg-red-50 text-red-700 ring-red-200'
                  : column.wip.reached
                    ? 'bg-amber-50 text-amber-800 ring-amber-200'
                    : 'bg-white text-slate-500 ring-slate-200'"
                :title="`WIP ${column.wip.current} of ${column.wip.limit}`"
              >
                {{ column.wip.current }}/{{ column.wip.limit }}
              </span>
              <span class="ml-auto rounded-full bg-white px-1.5 py-0.5 text-[10px] font-semibold text-slate-600 ring-1 ring-slate-200">{{ column.items.length }}</span>
            </div>

            <div class="flex-1 space-y-2 px-2 pb-2">
              <article
                v-for="item in column.items.slice(0, visibleColumnCount(lane.key, column.key, column.items.length))"
                :key="item.id"
                :data-board-card="item.id"
                class="group cursor-pointer touch-pan-y rounded-lg border border-slate-200 bg-white p-3 shadow-sm transition hover:border-indigo-200 hover:shadow focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500"
                :class="[
                  isSelected(item.id) ? 'ring-2 ring-indigo-400' : '',
                  draggingId === item.id ? 'opacity-40' : '',
                  isPending(item.id) ? 'opacity-60' : '',
                ]"
                tabindex="0"
                role="group"
                :aria-label="`${item.key}: ${item.title}. Status ${statusLabel(item.status)}. Drag, or press Alt+Left or Alt+Right to move.`"
                :title="canWriteWorkItems ? 'Drag to move, or press Alt+Left / Alt+Right' : item.title"
                @pointerdown="onCardPointerDown(item, $event)"
                @pointermove="onCardPointerMove(item, $event)"
                @pointerup="onCardPointerUp(item, $event)"
                @pointercancel="onCardPointerCancel"
                @click="onCardClick(item, $event)"
                @keydown="onCardKeydown(item, $event)"
                @keydown.enter="onCardActivate(item, $event)"
                @keydown.space="onCardActivate(item, $event)"
              >
                <div class="flex items-center justify-between gap-2">
                  <div class="flex items-center gap-1.5">
                    <input
                      v-if="canWriteWorkItems"
                      type="checkbox"
                      class="h-3.5 w-3.5 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                      :checked="isSelected(item.id)"
                      :aria-label="`Select ${item.key}`"
                      data-no-drag
                      @click.stop
                      @pointerdown.stop
                      @change="toggleSelected(item.id)"
                    />
                    <span class="flex h-5 w-5 items-center justify-center rounded" :class="typeTone(item.type)">
                      <AppIcon :name="typeIcon(item.type)" :size="12" />
                    </span>
                    <span class="font-mono text-[10px] font-semibold text-slate-600">{{ item.key }}</span>
                  </div>
                  <PriorityPill v-if="fieldVisible(visibleFields, 'priority')" :priority="item.priority" />
                </div>
                <p class="mt-2 line-clamp-2 text-[13px] font-medium leading-snug text-slate-800">{{ item.title }}</p>
                <template v-if="fieldVisible(visibleFields, 'feature')">
                  <button
                    v-if="item.parent_id && parentItem(item.parent_id)"
                    type="button"
                    class="mt-1.5 flex max-w-full items-center gap-1 truncate text-left text-[10px] font-medium text-indigo-600 hover:text-indigo-800"
                    :title="`Open parent ${parentItem(item.parent_id)?.key}`"
                    @click.stop="openParent(item.parent_id)"
                  >
                    <AppIcon name="arrowLeft" :size="10" />
                    <span class="font-mono">{{ parentItem(item.parent_id)?.key }}</span>
                    <span class="truncate">{{ parentItem(item.parent_id)?.title }}</span>
                  </button>
                  <p v-else class="mt-1.5 truncate text-[10px] text-slate-600">{{ featureName(item.feature_id) }}</p>
                </template>
                <div
                  v-if="(fieldVisible(visibleFields, 'labels') && cardLabels(item).length) || (fieldVisible(visibleFields, 'custom') && cardCustomValues(item).length)"
                  class="mt-1.5 flex flex-wrap gap-1"
                >
                  <span
                    v-for="label in fieldVisible(visibleFields, 'labels') ? cardLabels(item) : []"
                    :key="label"
                    class="inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[9px] font-semibold ring-1"
                    :class="labelTone(labelColor(label))"
                  >
                    <span class="h-1 w-1 rounded-full" :class="labelDot(labelColor(label))" />
                    {{ label }}
                  </span>
                  <span
                    v-for="entry in fieldVisible(visibleFields, 'custom') ? cardCustomValues(item) : []"
                    :key="entry.field.id"
                    class="inline-flex items-center rounded-full bg-slate-100 px-1.5 py-0.5 text-[9px] font-medium text-slate-600 ring-1 ring-slate-200"
                    :title="entry.field.name"
                  >
                    {{ entry.field.name }}: {{ entry.label }}
                  </span>
                </div>
                <span
                  v-if="fieldVisible(visibleFields, 'due_date') && dueLabel(item)"
                  class="mt-1.5 inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[9px] font-medium ring-1"
                  :class="dueTone(item)"
                >
                  <AppIcon name="clock" :size="10" /> {{ dueLabel(item) }}
                </span>
                <div
                  v-if="fieldVisible(visibleFields, 'delivery') || item.type === 'bug' || fieldVisible(visibleFields, 'assignee')"
                  class="mt-2.5 flex items-center justify-between gap-2 border-t border-slate-100 pt-2"
                >
                  <div class="flex items-center gap-2 text-[10px] text-slate-600">
                    <template v-if="fieldVisible(visibleFields, 'delivery')">
                      <span v-if="branchesFor(item.id).length" class="inline-flex items-center gap-1" title="Linked branches">
                        <AppIcon name="branch" :size="11" /> {{ branchesFor(item.id).length }}
                      </span>
                      <span v-if="prsFor(item.id).length" class="inline-flex items-center gap-1" title="Linked pull requests">
                        <AppIcon name="repo" :size="11" /> {{ prsFor(item.id).length }}
                      </span>
                    </template>
                    <span v-if="item.type === 'bug'" class="inline-flex items-center gap-1">
                      <StatusPill :status="item.status" :dot="false" />
                    </span>
                  </div>
                  <template v-if="fieldVisible(visibleFields, 'assignee')">
                    <span
                      v-if="item.assignee_id"
                      class="flex h-5 w-5 items-center justify-center rounded-full bg-indigo-100 text-[8px] font-bold text-indigo-700"
                      :title="memberName(item.assignee_id)"
                    >
                      {{ initials(item.assignee_id) }}
                    </span>
                    <span v-else class="text-[10px] text-slate-600">Unassigned</span>
                  </template>
                </div>
              </article>

              <button
                v-if="column.items.length > visibleColumnCount(lane.key, column.key, column.items.length)"
                type="button"
                class="flex w-full items-center justify-center gap-1 rounded-lg border border-slate-200 bg-white py-2 text-[10px] font-semibold text-slate-600 hover:border-indigo-300 hover:text-indigo-600"
                :data-testid="`board-show-more-${lane.key}-${column.key}`"
                @click="showMore(lane.key, column.key, column.items.length)"
              >
                Show next
                {{ Math.min(BOARD_COLUMN_PAGE_SIZE, column.items.length - visibleColumnCount(lane.key, column.key, column.items.length)) }}
                of {{ column.items.length - visibleColumnCount(lane.key, column.key, column.items.length) }} hidden
              </button>

              <button
                v-if="!column.items.length && canManageWorkItems"
                type="button"
                class="flex w-full items-center justify-center gap-1 rounded-lg border border-dashed border-slate-300 py-5 text-[11px] font-medium text-slate-600 hover:border-indigo-300 hover:text-indigo-600"
                @click="openCreateItem('task', column.key)"
              >
                <AppIcon name="plus" :size="12" /> Add item
              </button>
            </div>
          </section>
        </div>
      </div>
    </div>

    <div
      v-if="dragItem"
      class="pointer-events-none fixed z-50 w-[210px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-indigo-300 bg-white p-2 shadow-xl"
      :style="{ left: `${dragPoint.x}px`, top: `${dragPoint.y}px` }"
      aria-hidden="true"
      data-testid="board-drag-preview"
    >
      <div class="flex items-center gap-1.5">
        <span class="font-mono text-[10px] font-semibold text-slate-600">{{ dragItem.key }}</span>
        <PriorityPill :priority="dragItem.priority" />
      </div>
      <p class="mt-1 line-clamp-2 text-[12px] font-medium leading-snug text-slate-800">
        {{ dragItem.title }}
      </p>
    </div>

    <BaseModal
      v-model="bulkResultOpen"
      title="Bulk move results"
      :description="bulkResult
        ? `${bulkResult.columnLabel} · ${bulkSummary.moved} moved, ${bulkSummary.failed} failed, ${bulkSummary.skipped} skipped`
        : ''"
      max-width="md"
    >
      <div v-if="bulkResult" class="space-y-3">
        <div class="grid grid-cols-3 gap-2 text-center">
          <div class="rounded-lg bg-emerald-50 p-2 ring-1 ring-emerald-200">
            <p class="text-lg font-semibold leading-none text-emerald-700">{{ bulkSummary.moved }}</p>
            <p class="mt-1 text-[10px] font-medium text-emerald-800">Moved</p>
          </div>
          <div class="rounded-lg bg-red-50 p-2 ring-1 ring-red-200">
            <p class="text-lg font-semibold leading-none text-red-700">{{ bulkSummary.failed }}</p>
            <p class="mt-1 text-[10px] font-medium text-red-800">Failed</p>
          </div>
          <div class="rounded-lg bg-slate-100 p-2 ring-1 ring-slate-200">
            <p class="text-lg font-semibold leading-none text-slate-700">{{ bulkSummary.skipped }}</p>
            <p class="mt-1 text-[10px] font-medium text-slate-700">Skipped</p>
          </div>
        </div>
        <ul class="max-h-72 space-y-1.5 overflow-y-auto" data-testid="bulk-move-results">
          <li
            v-for="entry in bulkResult.entries"
            :key="entry.itemId"
            class="flex items-start gap-2 rounded-lg border border-slate-200 p-2"
          >
            <span class="mt-0.5 font-mono text-[10px] font-semibold text-slate-600">{{ entry.key }}</span>
            <div class="min-w-0 flex-1">
              <p class="truncate text-[11px] font-medium text-slate-800">{{ entry.title }}</p>
              <p
                class="mt-0.5 text-[10px]"
                :class="entry.outcome === 'failed' ? 'text-red-700' : entry.outcome === 'moved' ? 'text-emerald-700' : 'text-slate-600'"
              >
                {{ entry.message }}
              </p>
            </div>
            <span
              class="rounded-full px-1.5 py-0.5 text-[9px] font-semibold uppercase ring-1"
              :class="entry.outcome === 'failed'
                ? 'bg-red-50 text-red-700 ring-red-200'
                : entry.outcome === 'moved'
                  ? 'bg-emerald-50 text-emerald-700 ring-emerald-200'
                  : 'bg-slate-100 text-slate-600 ring-slate-200'"
            >
              {{ entry.outcome }}
            </span>
          </li>
        </ul>
      </div>
      <template #footer>
        <button
          v-if="bulkSummary.failed"
          type="button"
          class="btn-secondary"
          @click="retryFailedBulk"
        >
          <AppIcon name="refresh" :size="14" /> Retry failed
        </button>
        <button type="button" class="btn-primary" @click="bulkResultOpen = false">Close</button>
      </template>
    </BaseModal>

    <!-- Work item modal -->
    <BaseModal
      v-model="itemModal"
      :title="editing ? `${selectedItem?.key ?? 'Work item'} · ${workItemTypeLabels[itemForm.type]}` : 'New work item'"
      :description="editing ? `Created ${timeAgo(selectedItem?.created_at)} · Updated ${timeAgo(selectedItem?.updated_at)}` : ''"
      max-width="lg"
      :persistent="itemBusy"
    >
      <div class="space-y-5">
        <div v-if="editing" class="rounded-xl border border-slate-200 bg-slate-50 p-3.5">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex items-center gap-2">
              <span class="text-xs font-semibold text-slate-600">Current status</span>
              <StatusPill :status="selectedItem?.status" />
            </div>
            <span class="text-[10px] text-slate-600">Only valid transitions are offered</span>
          </div>
          <div v-if="transitionOptions.length" class="mt-2.5 flex flex-wrap gap-1.5">
            <button
              v-for="status in transitionOptions"
              :key="status"
              type="button"
              class="rounded-lg border border-slate-300 bg-white px-2.5 py-1.5 text-[11px] font-semibold text-slate-700 hover:border-indigo-300 hover:text-indigo-700 disabled:opacity-50"
              :disabled="itemBusy"
              @click="transition(status)"
            >
              → {{ statusLabel(status) }}
            </button>
          </div>
          <p v-else class="mt-2 text-[11px] text-slate-600">This item is in a terminal state for its workflow.</p>
        </div>

        <div
          v-if="editing && (selectedItem?.parent_id || selectedItem?.feature_id)"
          class="flex flex-wrap items-center gap-2 rounded-lg bg-indigo-50/60 px-3 py-2"
        >
          <span class="text-[10px] font-semibold uppercase tracking-wide text-indigo-700">Trace links</span>
          <button
            v-if="selectedItem?.parent_id"
            type="button"
            class="inline-flex items-center gap-1 rounded-md bg-white px-2 py-1 text-[11px] font-semibold text-indigo-700 ring-1 ring-indigo-200 hover:bg-indigo-100"
            @click="openParent(selectedItem?.parent_id ?? null)"
          >
            <AppIcon name="arrowLeft" :size="11" />
            Parent <span class="font-mono">{{ parentItem(selectedItem?.parent_id ?? null)?.key }}</span>
          </button>
          <button
            v-if="selectedItem?.feature_id"
            type="button"
            class="inline-flex items-center gap-1 rounded-md bg-white px-2 py-1 text-[11px] font-semibold text-indigo-700 ring-1 ring-indigo-200 hover:bg-indigo-100"
            @click="filterByFeature(selectedItem?.feature_id ?? null)"
          >
            <AppIcon name="layers" :size="11" />
            {{ featureName(selectedItem?.feature_id ?? null) }}
          </button>
        </div>

        <div
          v-if="editing && isTester"
          class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800"
        >
          Testers can change status and raise defects, but cannot edit work item details.
        </div>

        <fieldset :disabled="formDisabled" class="space-y-5">
        <div>
          <label class="field-label" for="item-title">Title</label>
          <input id="item-title" v-model="itemForm.title" data-autofocus class="field-input" placeholder="Describe the outcome, not the activity" />
        </div>

        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="item-type">Type</label>
            <select id="item-type" v-model="itemForm.type" class="field-input" @change="onTypeChange">
              <option v-for="(label, type) in workItemTypeLabels" :key="type" :value="type">{{ label }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="item-status">Status</label>
            <select id="item-status" v-model="itemForm.status" class="field-input">
              <option v-for="status in workItemStatusOptions(itemForm.type)" :key="status" :value="status">{{ statusLabel(status) }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="item-priority">Priority</label>
            <select id="item-priority" v-model="itemForm.priority" class="field-input">
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="item-feature">
              Feature
              <span v-if="featureRequired || itemForm.type === 'bug'" class="text-red-500">*</span>
            </label>
            <select id="item-feature" v-model="itemForm.feature_id" class="field-input">
              <option value="">No feature</option>
              <option v-for="feature in features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
            </select>
          </div>
          <div v-if="itemForm.type !== 'story'">
            <label class="field-label" for="item-parent">
              Parent
              <span v-if="parentRequired" class="text-red-500">*</span>
            </label>
            <select id="item-parent" v-model="itemForm.parent_id" class="field-input">
              <option value="">No parent</option>
              <option v-for="candidate in parentCandidates" :key="candidate.id" :value="candidate.id">
                {{ candidate.key }} · {{ candidate.title }}
              </option>
            </select>
          </div>
        </div>

        <div>
          <label class="field-label" for="item-description">Description</label>
          <textarea id="item-description" v-model="itemForm.description" rows="3" class="field-input" placeholder="Context, constraints and implementation notes" />
        </div>
        <div>
          <label class="field-label" for="item-ac">Acceptance criteria</label>
          <textarea id="item-ac" v-model="itemForm.acceptance_criteria" rows="2" class="field-input" placeholder="What must be true for this to be done?" />
        </div>
        <div>
          <label class="field-label" for="item-labels">Labels</label>
          <input
            id="item-labels"
            v-model="itemForm.labels"
            class="field-input"
            maxlength="240"
            list="item-label-options"
            placeholder="Comma separated, for example: backend, needs qa"
          />
          <datalist id="item-label-options">
            <option v-for="label in boardLabels" :key="label.id" :value="label.name" />
          </datalist>
          <div v-if="selectedLabelNames.length" class="mt-2 flex flex-wrap gap-1.5">
            <span
              v-for="label in selectedLabelNames"
              :key="label"
              class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-semibold ring-1"
              :class="labelTone(labelColor(label))"
            >
              <span class="h-1.5 w-1.5 rounded-full" :class="labelDot(labelColor(label))" />
              {{ label }}
            </span>
          </div>
          <p class="mt-1 text-[11px] text-slate-500">Up to 10 labels, each 24 characters or fewer. Unknown labels become reusable automatically.</p>
        </div>

        <div v-if="store.customFields.length" class="rounded-xl border border-slate-200 bg-slate-50/60 p-4">
          <p class="flex items-center gap-2 text-xs font-semibold text-slate-700">
            <AppIcon name="filter" :size="14" /> Custom fields
          </p>
          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <label v-for="field in store.customFields" :key="field.id" class="block">
              <span class="field-label">
                {{ field.name }}
                <span v-if="field.required" class="text-red-500">*</span>
              </span>
              <select v-if="field.type === 'select'" v-model="customDraft[field.id]" class="field-input" :disabled="formDisabled">
                <option value="">Not set</option>
                <option v-for="option in field.options" :key="option.value" :value="option.value">{{ option.label }}</option>
              </select>
              <select v-else-if="field.type === 'user'" v-model="customDraft[field.id]" class="field-input" :disabled="formDisabled">
                <option value="">Not set</option>
                <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
              </select>
              <input
                v-else
                v-model="customDraft[field.id]"
                class="field-input"
                :type="field.type === 'number' ? 'number' : field.type === 'date' ? 'date' : 'text'"
                :min="field.type === 'number' && field.config.min !== undefined ? String(field.config.min) : undefined"
                :max="field.type === 'number' && field.config.max !== undefined ? String(field.config.max) : undefined"
                :step="field.type === 'number' ? 'any' : undefined"
                :disabled="formDisabled"
                :placeholder="field.description || field.name"
              />
              <span v-if="field.description" class="mt-1 block text-[10px] text-slate-500">{{ field.description }}</span>
            </label>
          </div>
          <p class="mt-2 text-[10px] text-slate-500">Values are typed and validated by the API; adding a field never changes the schema.</p>
        </div>

        <div v-if="itemForm.type === 'bug'" class="rounded-xl border border-red-100 bg-red-50/50 p-4">
          <p class="flex items-center gap-2 text-xs font-semibold text-red-700">
            <AppIcon name="alert" :size="14" /> Defect evidence
          </p>
          <div class="mt-3 grid gap-3 sm:grid-cols-3">
            <div>
              <label class="field-label" for="bug-severity">Severity</label>
              <select id="bug-severity" v-model="itemForm.severity" class="field-input">
                <option value="">Not set</option>
                <option value="blocker">Blocker</option>
                <option value="critical">Critical</option>
                <option value="major">Major</option>
                <option value="minor">Minor</option>
                <option value="trivial">Trivial</option>
              </select>
            </div>
            <div class="sm:col-span-2">
              <label class="field-label" for="bug-repro">Reproduction steps</label>
              <textarea id="bug-repro" v-model="itemForm.reproduction_steps" rows="2" class="field-input" placeholder="1. Open… 2. Click… 3. Observe…" />
            </div>
          </div>
          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <div>
              <label class="field-label" for="bug-expected">Expected result</label>
              <textarea id="bug-expected" v-model="itemForm.expected_result" rows="2" class="field-input" />
            </div>
            <div>
              <label class="field-label" for="bug-actual">Actual result</label>
              <textarea id="bug-actual" v-model="itemForm.actual_result" rows="2" class="field-input" />
            </div>
          </div>
        </div>

        <div class="grid gap-4 sm:grid-cols-4">
          <div>
            <label class="field-label" for="item-assignee">Assignee</label>
            <select id="item-assignee" v-model="itemForm.assignee_id" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="item-sprint">Sprint</label>
            <select id="item-sprint" v-model="itemForm.sprint_id" class="field-input">
              <option value="">No sprint</option>
              <option v-for="sprint in sprints" :key="sprint.id" :value="sprint.id">{{ sprint.name }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="item-release">Release</label>
            <select id="item-release" v-model="itemForm.release_id" class="field-input">
              <option value="">No release</option>
              <option v-for="release in store.releases" :key="release.id" :value="release.id">{{ release.version }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="item-due">Due date</label>
            <input id="item-due" v-model="itemForm.due_date" type="date" class="field-input" />
          </div>
        </div>

        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="item-estimate">Estimate</label>
            <input id="item-estimate" v-model="itemForm.estimate_points" type="number" min="0" step="0.5" class="field-input" placeholder="Points" />
          </div>
          <div>
            <label class="field-label" for="item-env">Environment</label>
            <select id="item-env" v-model="itemForm.environment_id" class="field-input">
              <option value="">No environment</option>
              <option v-for="environment in store.workspace?.environments ?? []" :key="environment.id" :value="environment.id">{{ environment.name }}</option>
            </select>
          </div>
        </div>
        </fieldset>

        <div v-if="editing" class="rounded-xl border border-slate-200 p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p class="flex items-center gap-2 text-xs font-semibold text-slate-700">
                <AppIcon name="branch" :size="14" /> Code trace
              </p>
              <p class="mt-0.5 text-[11px] text-slate-600">Branches and pull requests attached to {{ selectedItem?.key }}.</p>
            </div>
            <button type="button" class="btn-ghost text-xs" @click="openDelivery">
              Delivery <AppIcon name="arrowRight" :size="12" />
            </button>
          </div>

          <div v-if="linkedBranches.length || linkedPrs.length" class="mt-3 space-y-2">
            <div v-for="branch in linkedBranches" :key="branch.id" class="flex items-center gap-2 rounded-lg bg-slate-50 px-2.5 py-2">
              <AppIcon name="branch" :size="13" class="text-slate-600" />
              <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-slate-700">{{ branch.name }}</span>
              <StatusPill :status="branch.status" />
            </div>
            <div v-for="pr in linkedPrs" :key="pr.id" class="flex items-center gap-2 rounded-lg bg-slate-50 px-2.5 py-2">
              <AppIcon name="repo" :size="13" class="text-slate-600" />
              <span class="min-w-0 flex-1 truncate text-[11px] text-slate-700">#{{ pr.number }} {{ pr.title }}</span>
              <StatusPill :status="pr.status" />
            </div>
          </div>
          <p v-else class="mt-3 text-[11px] text-slate-600">No code linked yet.</p>

          <div v-if="canLinkBranch && store.repositories.length" class="mt-3 flex flex-wrap items-end gap-2">
            <div class="min-w-[150px] flex-1">
              <label class="field-label" for="branch-repo">Repository</label>
              <select id="branch-repo" v-model="branchRepoId" class="field-input">
                <option v-for="repository in store.repositories" :key="repository.id" :value="repository.id">{{ repository.name }}</option>
              </select>
            </div>
            <div class="min-w-[200px] flex-[2]">
              <label class="field-label" for="branch-name">Branch name</label>
              <input id="branch-name" v-model="branchName" class="field-input font-mono text-xs" />
            </div>
            <button type="button" class="btn-secondary" :disabled="branchBusy" @click="createBranch">
              <AppIcon v-if="branchBusy" name="loader" :size="13" class="animate-spin" />
              <AppIcon v-else name="branch" :size="13" />
              Link branch
            </button>
          </div>
          <p v-else-if="canLinkBranch" class="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-[11px] text-amber-700">
            Link a repository in Delivery to create branches from work items.
          </p>
        </div>

        <p v-if="itemError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ itemError }}</p>
      </div>

      <template #footer>
        <button
          v-if="editing && canManageWorkItems"
          type="button"
          class="mr-auto inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm font-medium text-red-600 hover:bg-red-50"
          :disabled="itemBusy"
          @click="deleteOpen = true"
        >
          <AppIcon name="trash" :size="14" /> Delete
        </button>
        <button type="button" class="btn-secondary" :disabled="itemBusy" @click="itemModal = false">Cancel</button>
        <button v-if="!formDisabled" type="button" class="btn-primary" :disabled="itemBusy" @click="saveItem">
          <AppIcon v-if="itemBusy" name="loader" :size="14" class="animate-spin" />
          {{ itemBusy ? 'Saving…' : editing ? 'Save work item' : 'Create work item' }}
        </button>
      </template>
    </BaseModal>

    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete work item?"
      :message="`${selectedItem?.key ?? 'This item'} and its links will be removed. Test evidence and build history are kept.`"
      :busy="itemBusy"
      confirm-label="Delete work item"
      @confirm="deleteItem"
    />

    <BaseModal
      v-model="viewModal"
      :title="editingView ? 'Update saved view' : 'Save board view'"
      description="A saved view keeps filters in the URL, so sharing the link reproduces the same board."
      max-width="sm"
      :persistent="viewBusy"
    >
      <div class="space-y-4">
        <label class="block">
          <span class="field-label">Name</span>
          <input v-model="viewName" class="field-input" maxlength="80" placeholder="My blocked defects" data-autofocus />
        </label>
        <label class="block">
          <span class="field-label">Visibility</span>
          <select v-model="viewVisibility" class="field-input">
            <option value="private">Private to me</option>
            <option value="shared" :disabled="!canShareView">Shared with the project</option>
          </select>
          <span v-if="!canShareView" class="mt-1 block text-[11px] text-slate-500">Shared views need <code class="font-mono">planning.write</code>.</span>
        </label>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="field-label">Swimlanes</span>
            <select v-model="viewSwimlane" class="field-input">
              <option v-for="lane in BOARD_SWIMLANES" :key="lane.value" :value="lane.value">{{ lane.label }}</option>
            </select>
          </label>
          <label class="block">
            <span class="field-label">Sort</span>
            <select v-model="viewSortId" class="field-input">
              <option v-for="option in BOARD_SORT_OPTIONS" :key="option.id" :value="option.id">{{ option.label }}</option>
            </select>
          </label>
        </div>
        <fieldset>
          <legend class="field-label">Visible card fields</legend>
          <div class="mt-1 grid gap-1.5 sm:grid-cols-2">
            <label
              v-for="field in BOARD_VISIBLE_FIELDS"
              :key="field.value"
              class="flex items-center gap-2 text-xs font-medium text-slate-700"
            >
              <input v-model="viewFields" type="checkbox" :value="field.value" class="h-4 w-4 rounded border-slate-300" />
              {{ field.label }}
            </label>
          </div>
          <p class="mt-1 text-[11px] text-slate-500">Unchecked fields are hidden on cards in this view.</p>
        </fieldset>
        <div class="flex flex-wrap gap-4">
          <label class="flex items-center gap-2 text-xs font-medium text-slate-700">
            <input v-model="viewPinned" type="checkbox" class="h-4 w-4 rounded border-slate-300" />
            Pin to the top
          </label>
          <label class="flex items-center gap-2 text-xs font-medium text-slate-700">
            <input v-model="viewDefault" type="checkbox" class="h-4 w-4 rounded border-slate-300" />
            Default for my board
          </label>
        </div>
        <p v-if="viewError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">{{ viewError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="viewBusy" @click="viewModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="viewBusy" @click="saveView">
          <AppIcon v-if="viewBusy" name="loader" :size="14" class="animate-spin" />
          {{ viewBusy ? 'Saving…' : editingView ? 'Update view' : 'Save view' }}
        </button>
      </template>
    </BaseModal>

    <ConfirmDialog
      :open="Boolean(deleteViewId)"
      title="Delete saved view"
      :message="selectedView ? `Delete ${selectedView.name}? Work items are not affected.` : ''"
      confirm-label="Delete view"
      :busy="deletingView"
      @confirm="deleteView"
      @cancel="deleteViewId = ''"
      @update:open="(value) => { if (!value) deleteViewId = '' }"
    />

    <WorkItemDrawer
      :model-value="drawerOpen"
      :item-id="drawerItemId || null"
      @update:model-value="onDrawerVisibility"
      @edit="editFromDrawer"
      @open-item="openDrawerById"
      @changed="store.refresh()"
    />
  </div>
</template>
