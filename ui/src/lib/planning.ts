import { allowedTransitions, boardColumns, targetStatusForColumn } from './workspace'
import type { BoardColumn } from './workspace'
import type {
  CustomField,
  CustomFieldOption,
  CustomFieldValue,
  LabelColor,
  ProjectLabel,
  SavedFilter,
  WorkItem,
} from '../types/workspace'

export const LABEL_COLORS: LabelColor[] = [
  'slate',
  'blue',
  'green',
  'amber',
  'red',
  'purple',
  'pink',
  'teal',
]

const LABEL_TONES: Record<string, string> = {
  slate: 'bg-slate-100 text-slate-700 ring-slate-200',
  blue: 'bg-sky-100 text-sky-700 ring-sky-200',
  green: 'bg-emerald-100 text-emerald-700 ring-emerald-200',
  amber: 'bg-amber-100 text-amber-800 ring-amber-200',
  red: 'bg-red-100 text-red-700 ring-red-200',
  purple: 'bg-violet-100 text-violet-700 ring-violet-200',
  pink: 'bg-pink-100 text-pink-700 ring-pink-200',
  teal: 'bg-teal-100 text-teal-700 ring-teal-200',
}

const LABEL_DOTS: Record<string, string> = {
  slate: 'bg-slate-400',
  blue: 'bg-sky-500',
  green: 'bg-emerald-500',
  amber: 'bg-amber-500',
  red: 'bg-red-500',
  purple: 'bg-violet-500',
  pink: 'bg-pink-500',
  teal: 'bg-teal-500',
}

export function labelTone(color: string | undefined): string {
  return LABEL_TONES[color ?? ''] ?? LABEL_TONES.slate!
}

export function labelDot(color: string | undefined): string {
  return LABEL_DOTS[color ?? ''] ?? LABEL_DOTS.slate!
}

export function colorForLabel(label: string, labels: ProjectLabel[]): string {
  return labels.find((entry) => entry.name === label)?.color ?? 'slate'
}

export function normalizeLabelInput(input: string): string[] {
  const seen = new Set<string>()
  const labels: string[] = []
  for (const part of input.split(',')) {
    const label = part
      .toLowerCase()
      .trim()
      .replace(/\s+/g, ' ')
    if (!label || label.length > 24 || !/^[a-z0-9 _-]+$/.test(label)) continue
    if (seen.has(label)) continue
    seen.add(label)
    labels.push(label)
    if (labels.length === 10) break
  }
  return labels
}

export interface BoardFilters {
  search: string
  type: string
  feature: string
  sprint: string
  label: string
  custom: Record<string, string>
}

export function emptyBoardFilters(): BoardFilters {
  return { search: '', type: '', feature: '', sprint: '', label: '', custom: {} }
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function boardFiltersFromQuery(
  query: Record<string, unknown>,
  fields: CustomField[],
): BoardFilters {
  const filters: BoardFilters = {
    search: stringValue(query.search ?? query.q),
    type: stringValue(query.type),
    feature: stringValue(query.feature),
    sprint: stringValue(query.sprint),
    label: stringValue(query.label),
    custom: {},
  }
  for (const field of fields) {
    const value = stringValue(query[`cf_${field.key}`])
    if (value) filters.custom[field.id] = value
  }
  return filters
}

export function boardFiltersToQuery(
  filters: BoardFilters,
  fields: CustomField[],
): Record<string, string> {
  const query: Record<string, string> = {}
  if (filters.search.trim()) query.search = filters.search.trim()
  if (filters.type) query.type = filters.type
  if (filters.feature) query.feature = filters.feature
  if (filters.sprint) query.sprint = filters.sprint
  if (filters.label) query.label = filters.label
  for (const field of fields) {
    const value = filters.custom[field.id]
    if (value) query[`cf_${field.key}`] = value
  }
  return query
}

export function boardFilterCount(filters: BoardFilters): number {
  let count = 0
  if (filters.search.trim()) count += 1
  if (filters.type) count += 1
  if (filters.feature) count += 1
  if (filters.sprint) count += 1
  if (filters.label) count += 1
  count += Object.values(filters.custom).filter(Boolean).length
  return count
}

export function savedFilterToBoard(
  filter: SavedFilter,
  fields: CustomField[],
): BoardFilters {
  const raw = filter.filters ?? {}
  const result = emptyBoardFilters()
  result.search = stringValue(raw.search)
  result.type = stringValue(raw.type)
  result.feature = stringValue(raw.feature)
  result.sprint = stringValue(raw.sprint)
  result.label = stringValue(raw.label)
  const custom = raw.custom
  if (custom && typeof custom === 'object' && !Array.isArray(custom)) {
    for (const field of fields) {
      const value = stringValue((custom as Record<string, unknown>)[field.key])
      if (value) result.custom[field.id] = value
    }
  }
  return result
}

export function boardFiltersToSaved(
  filters: BoardFilters,
  fields: CustomField[],
): Record<string, unknown> {
  const custom: Record<string, string> = {}
  for (const field of fields) {
    const value = filters.custom[field.id]
    if (value) custom[field.key] = value
  }
  return {
    search: filters.search.trim(),
    type: filters.type,
    feature: filters.feature,
    sprint: filters.sprint,
    label: filters.label,
    custom,
  }
}

export function customValueMap(
  values: CustomFieldValue[],
  entityId: string,
): Record<string, string | number | null> {
  const map: Record<string, string | number | null> = {}
  for (const value of values) {
    if (value.entity_id === entityId) map[value.field_id] = value.value
  }
  return map
}

export function customFieldDisplay(field: CustomField, value: unknown): string {
  if (value === null || value === undefined || value === '') return ''
  if (field.type === 'select') {
    const option = field.options.find((entry) => entry.value === String(value))
    return option?.label ?? String(value)
  }
  if (field.type === 'number') return String(Number(value))
  if (field.type === 'user') return String(value)
  return String(value)
}

export function customFieldDraft(
  fields: CustomField[],
  values: Record<string, string | number | null>,
): Record<string, string> {
  const draft: Record<string, string> = {}
  for (const field of fields) {
    const value = values[field.id]
    draft[field.id] = value === null || value === undefined ? '' : String(value)
  }
  return draft
}

export function missingRequiredCustomField(
  fields: CustomField[],
  draft: Record<string, string>,
): string {
  for (const field of fields) {
    if (!field.required) continue
    if (!String(draft[field.id] ?? '').trim()) return field.name
  }
  return ''
}

export function customFieldPayload(
  fields: CustomField[],
  draft: Record<string, string>,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {}
  for (const field of fields) {
    const raw = String(draft[field.id] ?? '').trim()
    if (!raw) continue
    if (field.type === 'number') {
      const number = Number(raw)
      if (!Number.isFinite(number)) continue
      payload[field.id] = number
    } else {
      payload[field.id] = raw
    }
  }
  return payload
}

export function workItemMatchesFilters(
  item: WorkItem,
  filters: BoardFilters,
  fields: CustomField[],
  values: Record<string, unknown>,
  featureTitle: string,
): boolean {
  if (filters.type && item.type !== filters.type) return false
  if (filters.feature && item.feature_id !== filters.feature) return false
  if (filters.sprint === 'none' && item.sprint_id) return false
  if (filters.sprint && filters.sprint !== 'none' && item.sprint_id !== filters.sprint) return false
  if (filters.label && !(item.labels ?? []).includes(filters.label)) return false
  const query = filters.search.trim().toLowerCase()
  if (query) {
    const haystack = `${item.key} ${item.title} ${item.description} ${featureTitle}`.toLowerCase()
    if (!haystack.includes(query)) return false
  }
  for (const field of fields) {
    const wanted = filters.custom[field.id]
    if (!wanted) continue
    const current = values[field.id]
    if (current === null || current === undefined || current === '') return false
    if (field.type === 'text') {
      if (!String(current).toLowerCase().includes(wanted.toLowerCase())) return false
    } else if (field.type === 'number') {
      if (Number(current) !== Number(wanted)) return false
    } else if (String(current) !== wanted) {
      return false
    }
  }
  return true
}

export function parseOptionLines(input: string): CustomFieldOption[] {
  const options: CustomFieldOption[] = []
  const seen = new Set<string>()
  for (const line of input.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const [rawValue, rawLabel] = trimmed.includes(':')
      ? [trimmed.slice(0, trimmed.indexOf(':')), trimmed.slice(trimmed.indexOf(':') + 1)]
      : [trimmed, trimmed]
    const label = (rawLabel || rawValue).trim()
    const value = (rawValue || label)
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9_-]+/g, '-')
      .replace(/^-+|-+$/g, '')
    if (!value || seen.has(value)) continue
    seen.add(value)
    options.push({ value, label })
  }
  return options
}

export function optionLines(options: CustomFieldOption[]): string {
  return options.map((option) => `${option.value}: ${option.label}`).join('\n')
}

export function scopeLabel(scope: string): string {
  switch (scope) {
    case 'planning':
      return 'Planning'
    case 'board':
      return 'Board'
    case 'test':
      return 'Test'
    default:
      return scope
  }
}

export function canManageSavedFilter(
  filter: SavedFilter | undefined,
  userId: string,
  projectRole: string,
  canSettingsWrite: boolean,
): boolean {
  if (!filter) return false
  if (filter.owner_id === userId) return true
  if (canSettingsWrite) return true
  return projectRole === 'owner' || projectRole === 'maintainer'
}

// ---------------------------------------------------------------------------
// Board views: swimlanes, sort, quick filters, visible fields and WIP policy
// ---------------------------------------------------------------------------

export const BOARD_COLUMNS = [
  'backlog',
  'ready',
  'in_progress',
  'review',
  'qa',
  'done',
  'blocked',
] as const

export type BoardColumnKey = (typeof BOARD_COLUMNS)[number]

export const BOARD_SWIMLANES: Array<{ value: string; label: string }> = [
  { value: 'none', label: 'No swimlanes' },
  { value: 'assignee', label: 'Assignee' },
  { value: 'priority', label: 'Priority' },
  { value: 'type', label: 'Work item type' },
]

export function boardSwimlaneFromSaved(filter: SavedFilter | undefined): string {
  const value = filter?.swimlane
  return BOARD_SWIMLANES.some((entry) => entry.value === value) ? String(value) : 'none'
}

export interface BoardVisibleField {
  value: string
  label: string
}

export const BOARD_VISIBLE_FIELDS: BoardVisibleField[] = [
  { value: 'priority', label: 'Priority' },
  { value: 'feature', label: 'Feature' },
  { value: 'labels', label: 'Labels' },
  { value: 'assignee', label: 'Assignee' },
  { value: 'due_date', label: 'Due date' },
  { value: 'delivery', label: 'Delivery links' },
  { value: 'custom', label: 'Custom fields' },
]

export const DEFAULT_BOARD_FIELDS = BOARD_VISIBLE_FIELDS.map((field) => field.value)

export function visibleFieldsFromSaved(filter: SavedFilter | undefined): string[] {
  const known = new Set(DEFAULT_BOARD_FIELDS)
  const saved = (filter?.columns ?? []).filter((column) => known.has(column))
  if (!saved.length) return [...DEFAULT_BOARD_FIELDS]
  return DEFAULT_BOARD_FIELDS.filter((field) => saved.includes(field))
}

export function visibleFieldsToSaved(fields: string[]): string[] {
  return DEFAULT_BOARD_FIELDS.filter((field) => fields.includes(field))
}

export function fieldVisible(fields: string[], field: string): boolean {
  return fields.includes(field)
}

export type BoardSortField = 'priority' | 'newest' | 'oldest' | 'due_soon' | 'due_later' | 'key'
export type BoardSortDirection = 'asc' | 'desc'

export interface BoardSort {
  field: BoardSortField
  direction: BoardSortDirection
}

export const BOARD_SORT_OPTIONS: Array<{ id: string; label: string; sort: BoardSort }> = [
  { id: 'priority:asc', label: 'Priority: highest first', sort: { field: 'priority', direction: 'asc' } },
  { id: 'priority:desc', label: 'Priority: lowest first', sort: { field: 'priority', direction: 'desc' } },
  { id: 'newest:desc', label: 'Newest first', sort: { field: 'newest', direction: 'desc' } },
  { id: 'oldest:asc', label: 'Oldest first', sort: { field: 'oldest', direction: 'asc' } },
  { id: 'due_soon:asc', label: 'Due soonest first', sort: { field: 'due_soon', direction: 'asc' } },
  { id: 'due_later:desc', label: 'Due latest first', sort: { field: 'due_later', direction: 'desc' } },
  { id: 'key:asc', label: 'Key: ascending', sort: { field: 'key', direction: 'asc' } },
  { id: 'key:desc', label: 'Key: descending', sort: { field: 'key', direction: 'desc' } },
]

export function emptyBoardSort(): BoardSort {
  return { field: 'priority', direction: 'asc' }
}

export function boardSortId(sort: BoardSort): string {
  return `${sort.field}:${sort.direction}`
}

export function boardSortFromId(id: string): BoardSort {
  const option = BOARD_SORT_OPTIONS.find((entry) => entry.id === id)
  return option ? { ...option.sort } : emptyBoardSort()
}

export function boardSortFromSaved(filter: SavedFilter | undefined): BoardSort {
  const raw = filter?.sort ?? {}
  const field = stringValue(raw.field)
  const direction = raw.direction === 'desc' ? 'desc' : 'asc'
  const option = BOARD_SORT_OPTIONS.find((entry) => entry.sort.field === field)
  if (!option) return emptyBoardSort()
  return { field: option.sort.field, direction: direction as BoardSortDirection }
}

export function boardSortToSaved(sort: BoardSort): Record<string, unknown> {
  return { field: sort.field, direction: sort.direction }
}

function dueTime(item: WorkItem): number | null {
  if (!item.due_date) return null
  const time = Date.parse(item.due_date)
  return Number.isNaN(time) ? null : time
}

function createdTime(item: WorkItem): number {
  const time = Date.parse(item.created_at)
  return Number.isNaN(time) ? 0 : time
}

function priorityValue(priority: string): number {
  switch (priority) {
    case 'critical':
      return 0
    case 'high':
      return 1
    case 'medium':
      return 2
    case 'low':
      return 3
    default:
      return 4
  }
}

// compareBoardItems is the single ordering used by the board. Unscheduled cards
// always sort last for date fields so a missing due date never looks urgent.
export function compareBoardItems(a: WorkItem, b: WorkItem, sort: BoardSort): number {
  let result = 0
  switch (sort.field) {
    case 'priority':
      result = priorityValue(a.priority) - priorityValue(b.priority)
      break
    case 'newest':
      result = createdTime(b) - createdTime(a)
      break
    case 'oldest':
      result = createdTime(a) - createdTime(b)
      break
    case 'due_soon':
    case 'due_later': {
      const left = dueTime(a)
      const right = dueTime(b)
      if (left === null && right === null) result = 0
      else if (left === null) return 1
      else if (right === null) return -1
      else result = sort.field === 'due_soon' ? left - right : right - left
      break
    }
    case 'key':
      result = a.item_number - b.item_number
      break
  }
  if (sort.field !== 'due_soon' && sort.field !== 'due_later' && sort.field !== 'newest' && sort.field !== 'oldest') {
    if (sort.direction === 'desc') result = -result
  }
  if (result !== 0) return result
  return b.item_number - a.item_number
}

export type BoardQuickFilter = 'mine' | 'unassigned' | 'blocked' | 'bugs' | 'due_soon'

export const BOARD_QUICK_FILTERS: Array<{ value: BoardQuickFilter; label: string }> = [
  { value: 'mine', label: 'Assigned to me' },
  { value: 'unassigned', label: 'Unassigned' },
  { value: 'blocked', label: 'Blocked' },
  { value: 'bugs', label: 'Defects' },
  { value: 'due_soon', label: 'Due in 7 days' },
]

const QUICK_FILTER_VALUES = new Set<string>(BOARD_QUICK_FILTERS.map((entry) => entry.value))

export function quickFiltersFromSaved(filter: SavedFilter | undefined): BoardQuickFilter[] {
  const raw = filter?.filters?.quick
  if (!Array.isArray(raw)) return []
  return raw.filter((entry): entry is BoardQuickFilter => typeof entry === 'string' && QUICK_FILTER_VALUES.has(entry))
}

export function quickFiltersFromValue(value: unknown): BoardQuickFilter[] {
  if (typeof value !== 'string') return []
  const seen = new Set<string>()
  const parsed: BoardQuickFilter[] = []
  for (const entry of value.split(',')) {
    const trimmed = entry.trim()
    if (!QUICK_FILTER_VALUES.has(trimmed) || seen.has(trimmed)) continue
    seen.add(trimmed)
    parsed.push(trimmed as BoardQuickFilter)
  }
  return parsed
}

export function visibleFieldsFromValue(value: unknown): string[] {
  if (typeof value !== 'string' || !value.trim()) return [...DEFAULT_BOARD_FIELDS]
  const known = new Set(DEFAULT_BOARD_FIELDS)
  const parsed = value
    .split(',')
    .map((entry) => entry.trim())
    .filter((entry) => known.has(entry))
  return parsed.length ? DEFAULT_BOARD_FIELDS.filter((field) => parsed.includes(field)) : [...DEFAULT_BOARD_FIELDS]
}

export function quickFiltersToSaved(quick: BoardQuickFilter[]): string[] {
  return [...new Set(quick)]
}

export function toggleQuickFilter(
  quick: BoardQuickFilter[],
  value: BoardQuickFilter,
): BoardQuickFilter[] {
  return quick.includes(value) ? quick.filter((entry) => entry !== value) : [...quick, value]
}

const DUE_SOON_WINDOW_MS = 7 * 24 * 60 * 60 * 1000

export function itemMatchesQuickFilters(
  item: WorkItem,
  quick: BoardQuickFilter[],
  context: { userId: string; now?: Date },
): boolean {
  const now = context.now ?? new Date()
  for (const filter of quick) {
    switch (filter) {
      case 'mine':
        if (!context.userId || item.assignee_id !== context.userId) return false
        break
      case 'unassigned':
        if (item.assignee_id) return false
        break
      case 'blocked':
        if (item.status !== 'blocked') return false
        break
      case 'bugs':
        if (item.type !== 'bug') return false
        break
      case 'due_soon': {
        const due = dueTime(item)
        if (due === null) return false
        if (due > now.getTime() + DUE_SOON_WINDOW_MS) return false
        break
      }
    }
  }
  return true
}

export interface BoardViewState {
  filters: BoardFilters
  sort: BoardSort
  swimlane: string
  fields: string[]
  quick: BoardQuickFilter[]
}

export function boardViewFromSaved(
  filter: SavedFilter,
  customFields: CustomField[],
): BoardViewState {
  return {
    filters: savedFilterToBoard(filter, customFields),
    sort: boardSortFromSaved(filter),
    swimlane: boardSwimlaneFromSaved(filter),
    fields: visibleFieldsFromSaved(filter),
    quick: quickFiltersFromSaved(filter),
  }
}

export function defaultBoardView(
  views: SavedFilter[],
  userId: string,
): SavedFilter | undefined {
  return (
    views.find((view) => view.is_default && view.owner_id === userId) ??
    views.find((view) => view.is_default)
  )
}

export interface SwimlaneGroup {
  key: string
  label: string
  items: WorkItem[]
}

const PRIORITY_LANE_ORDER = ['critical', 'high', 'medium', 'low', 'none']
const TYPE_LANE_ORDER = ['story', 'task', 'bug', 'spike', 'chore', 'subtask']

function laneTitle(value: string): string {
  if (!value) return 'None'
  return value.charAt(0).toUpperCase() + value.slice(1).replace(/_/g, ' ')
}

// groupBySwimlane preserves the board 1000-item order inside each lane. Two
// callers use it: the visible board and the tests, so lane ordering is explicit
// rather than incidental DOM order.
export function groupBySwimlane(
  items: WorkItem[],
  swimlane: string,
  memberLabel: (userId: string) => string = (userId) => userId,
): SwimlaneGroup[] {
  if (swimlane === 'none') return [{ key: 'all', label: '', items: [...items] }]
  const groups = new Map<string, WorkItem[]>()
  for (const item of items) {
    let key = ''
    if (swimlane === 'assignee') key = item.assignee_id || 'unassigned'
    else if (swimlane === 'priority') key = item.priority || 'none'
    else if (swimlane === 'type') key = item.type || 'none'
    else key = 'all'
    const bucket = groups.get(key)
    if (bucket) bucket.push(item)
    else groups.set(key, [item])
  }
  let keys = [...groups.keys()]
  if (swimlane === 'assignee') {
    keys.sort((a, b) => {
      if (a === 'unassigned') return 1
      if (b === 'unassigned') return -1
      return memberLabel(a).localeCompare(memberLabel(b))
    })
  } else if (swimlane === 'priority') {
    keys.sort((a, b) => {
      const left = PRIORITY_LANE_ORDER.indexOf(a)
      const right = PRIORITY_LANE_ORDER.indexOf(b)
      return (left === -1 ? 99 : left) - (right === -1 ? 99 : right)
    })
  } else if (swimlane === 'type') {
    keys.sort((a, b) => {
      const left = TYPE_LANE_ORDER.indexOf(a)
      const right = TYPE_LANE_ORDER.indexOf(b)
      return (left === -1 ? 99 : left) - (right === -1 ? 99 : right)
    })
  }
  return keys.map((key) => ({
    key,
    label: key === 'unassigned' ? 'Unassigned' : laneTitle(key),
    items: groups.get(key) ?? [],
  }))
}

export interface WipState {
  current: number
  limit: number
  reached: boolean
  exceeded: boolean
}

export function wipState(limit: number | undefined, current: number): WipState | null {
  if (!limit || limit <= 0) return null
  return { current, limit, reached: current >= limit, exceeded: current > limit }
}

export interface BoardMoveTarget {
  column: BoardColumn
  status: string
}

// moveTargetForColumn resolves a board column to the workflow status a card of
// this type can actually enter. The canonical status is preferred; when the
// column accepts several statuses, the first allowed one is used instead. That
// keeps a drag honest: a move either finds a legal transition or returns null,
// and the UI never sends a status the API will reject.
export function moveTargetForColumn(item: WorkItem, columnKey: string): BoardMoveTarget | null {
  const column = boardColumns.find((entry) => entry.key === columnKey)
  if (!column) return null
  if (column.statuses.includes(item.status)) return { column, status: item.status }
  const allowed = allowedTransitions(item.type, item.status)
  const preferred = targetStatusForColumn(item.type, columnKey)
  if (column.statuses.includes(preferred) && allowed.includes(preferred)) {
    return { column, status: preferred }
  }
  const fallback = column.statuses.find((status) => allowed.includes(status))
  return fallback ? { column, status: fallback } : null
}

// keyboardMoveTarget walks outward from the current column until it finds the
// nearest column with a legal transition. Skipping columns is intentional:
// "Done" cannot go straight to "Review", but it can reopen into "In progress".
export function keyboardMoveTarget(item: WorkItem, direction: -1 | 1): BoardMoveTarget | null {
  const current = boardColumns.findIndex((column) => column.statuses.includes(item.status))
  if (current === -1) return null
  for (let index = current + direction; index >= 0 && index < boardColumns.length; index += direction) {
    const target = moveTargetForColumn(item, boardColumns[index]!.key)
    if (target && target.status !== item.status) return target
  }
  return null
}

export function boardMoveAnnouncement(
  itemKey: string,
  columnLabel: string,
  position: number,
  total: number,
): string {
  if (position > 0 && total > 0) {
    return `${itemKey} moved to ${columnLabel}, position ${position} of ${total}.`
  }
  return `${itemKey} moved to ${columnLabel}.`
}

export type BoardMoveOutcome = 'moved' | 'failed' | 'skipped'

export interface BoardMoveResult {
  itemId: string
  key: string
  title: string
  outcome: BoardMoveOutcome
  message: string
}

export function summarizeBoardMoves(results: BoardMoveResult[]): {
  moved: number
  failed: number
  skipped: number
} {
  const summary = { moved: 0, failed: 0, skipped: 0 }
  for (const result of results) summary[result.outcome] += 1
  return summary
}
