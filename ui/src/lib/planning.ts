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
