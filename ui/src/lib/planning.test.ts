import { describe, expect, it } from 'vitest'
import {
  BOARD_COLUMN_PAGE_SIZE,
  boardFilterCount,
  boardFiltersFromQuery,
  boardFiltersToQuery,
  boardFiltersToSaved,
  boardMoveAnnouncement,
  boardSortFromId,
  boardWipCounts,
  buildBoardLanes,
  nextVisibleCount,
  boardSortFromSaved,
  boardSortId,
  boardSortToSaved,
  boardSwimlaneFromSaved,
  boardViewFromSaved,
  canManageSavedFilter,
  colorForLabel,
  compareBoardItems,
  customFieldDisplay,
  customFieldDraft,
  customFieldPayload,
  customValueMap,
  defaultBoardView,
  groupBySwimlane,
  itemMatchesQuickFilters,
  keyboardMoveTarget,
  labelDot,
  labelTone,
  missingRequiredCustomField,
  moveTargetForColumn,
  normalizeLabelInput,
  optionLines,
  parseOptionLines,
  quickFiltersFromSaved,
  quickFiltersFromValue,
  quickFiltersToSaved,
  savedFilterToBoard,
  summarizeBoardMoves,
  toggleQuickFilter,
  visibleFieldsFromSaved,
  visibleFieldsFromValue,
  visibleFieldsToSaved,
  wipState,
  workItemMatchesFilters,
} from './planning'
import type {
  CustomField,
  CustomFieldValue,
  ProjectLabel,
  SavedFilter,
  WorkItem,
} from '../types/workspace'

function field(overrides: Partial<CustomField> = {}): CustomField {
  return {
    id: 'field-1', project_id: 'project-1', key: 'story_points', name: 'Story points',
    type: 'number', description: '', options: [], config: {}, required: false, position: 0,
    archived_at: null, created_by: null, created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z', ...overrides,
  }
}

function item(overrides: Partial<WorkItem> = {}): WorkItem {
  return {
    id: 'item-1', project_id: 'project-1', item_number: 1, key: 'SPM-1',
    feature_id: 'feature-1', parent_id: null, type: 'task', title: 'Checkout flow',
    description: 'Payment capture', acceptance_criteria: '', status: 'ready', priority: 'medium',
    severity: null, assignee_id: null, reporter_id: null, estimate_points: null,
    sprint_id: 'sprint-1', release_id: null, environment_id: null, due_date: null,
    reproduction_steps: '', expected_result: '', actual_result: '', labels: ['needs qa'],
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    closed_at: null, revision: 1, ...overrides,
  }
}

function saved(overrides: Partial<SavedFilter> = {}): SavedFilter {
  return {
    id: 'view-1', project_id: 'project-1', name: 'QA', scope: 'board', visibility: 'shared',
    owner_id: 'user-1', filters: {}, sort: {}, columns: [], swimlane: 'none',
    pinned: false, is_default: false,
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', revision: 1,
    ...overrides,
  }
}

function value(overrides: Partial<CustomFieldValue> = {}): CustomFieldValue {
  return {
    id: 'value-1', project_id: 'project-1', field_id: 'field-1', entity_type: 'work_item',
    entity_id: 'item-1', value: 5, updated_at: '2026-01-01T00:00:00Z', ...overrides,
  }
}

describe('label helpers', () => {
  it('normalizes, deduplicates and rejects invalid labels', () => {
    expect(normalizeLabelInput(' Needs QA, needs qa, backend, café, , -fast ')).toEqual([
      'needs qa',
      'backend',
      '-fast',
    ])
    expect(normalizeLabelInput('a'.repeat(25))).toEqual([])
    expect(normalizeLabelInput('one,two,three,four,five,six,seven,eight,nine,ten,eleven')).toHaveLength(10)
  })

  it('maps color tokens to tones and dots', () => {
    expect(labelTone('amber')).toContain('amber')
    expect(labelTone('unknown')).toContain('slate')
    expect(labelDot(undefined)).toContain('slate')
  })

  it('resolves a label color from the project registry', () => {
    const labels = [
      { id: 'label-1', project_id: 'project-1', name: 'needs qa', color: 'green' },
    ] as ProjectLabel[]
    expect(colorForLabel('needs qa', labels)).toBe('green')
    expect(colorForLabel('other', labels)).toBe('slate')
  })
})

describe('board filters', () => {
  const fields = [field(), field({ id: 'field-2', key: 'risk', name: 'Risk', type: 'text' })]

  it('round-trips filters through the URL query', () => {
    const filters = {
      search: ' checkout ',
      type: 'bug',
      feature: 'feature-1',
      sprint: 'none',
      label: 'needs qa',
      custom: { 'field-1': '5', 'field-2': 'high' },
    }
    const query = boardFiltersToQuery(filters, fields)
    expect(query).toEqual({
      search: 'checkout',
      type: 'bug',
      feature: 'feature-1',
      sprint: 'none',
      label: 'needs qa',
      cf_story_points: '5',
      cf_risk: 'high',
    })
    expect(boardFiltersFromQuery(query, fields)).toEqual({
      search: 'checkout',
      type: 'bug',
      feature: 'feature-1',
      sprint: 'none',
      label: 'needs qa',
      custom: { 'field-1': '5', 'field-2': 'high' },
    })
    expect(boardFilterCount(boardFiltersFromQuery(query, fields))).toBe(7)
  })

  it('serializes and applies a saved view by field key', () => {
    const filters = {
      search: '',
      type: 'task',
      feature: '',
      sprint: '',
      label: 'needs qa',
      custom: { 'field-2': 'high' },
    }
    const stored = boardFiltersToSaved(filters, fields)
    expect(stored.custom).toEqual({ risk: 'high' })
    expect(
      savedFilterToBoard(saved({ filters: stored }), fields),
    ).toEqual(filters)
  })

  it('matches label, search, sprint and typed custom filters', () => {
    const filters = {
      search: 'payment',
      type: 'task',
      feature: 'feature-1',
      sprint: 'sprint-1',
      label: 'needs qa',
      custom: { 'field-1': '5', 'field-2': 'HIGH' },
    }
    expect(
      workItemMatchesFilters(
        item(),
        filters,
        fields,
        { 'field-1': 5, 'field-2': 'high risk' },
        'Checkout',
      ),
    ).toBe(true)
    expect(workItemMatchesFilters(item({ labels: [] }), filters, fields, {}, 'Checkout')).toBe(false)
    expect(workItemMatchesFilters(item(), { ...filters, search: 'missing' }, fields, {}, 'Checkout')).toBe(false)
    expect(
      workItemMatchesFilters(item(), { ...filters, custom: { 'field-1': '6' } }, fields, { 'field-1': 5 }, 'Checkout'),
    ).toBe(false)
  })
})

describe('custom field values', () => {
  it('maps values and displays select option labels', () => {
    const map = customValueMap(
      [value(), value({ id: 'value-2', field_id: 'field-2', value: 'high' })],
      'item-1',
    )
    expect(map).toEqual({ 'field-1': 5, 'field-2': 'high' })
    const select = field({
      id: 'field-2',
      type: 'select',
      options: [{ value: 'high', label: 'High risk' }],
    })
    expect(customFieldDisplay(select, 'high')).toBe('High risk')
    expect(customFieldDisplay(select, 'missing')).toBe('missing')
    expect(customFieldDisplay(field(), 5)).toBe('5')
    expect(customFieldDisplay(field(), null)).toBe('')
  })

  it('builds a typed draft and payload while dropping blanks', () => {
    const fields = [
      field(),
      field({ id: 'field-2', key: 'notes', name: 'Notes', type: 'text' }),
      field({ id: 'field-3', key: 'due', name: 'Due', type: 'date' }),
    ]
    const draft = customFieldDraft(fields, { 'field-1': 8, 'field-2': null, 'field-3': '2026-11-21' })
    expect(draft).toEqual({ 'field-1': '8', 'field-2': '', 'field-3': '2026-11-21' })
    draft['field-2'] = '  shipped  '
    expect(customFieldPayload(fields, draft)).toEqual({
      'field-1': 8,
      'field-2': 'shipped',
      'field-3': '2026-11-21',
    })
  })

  it('reports the first missing required field', () => {
    const required = field({ id: 'req', name: 'Story points', required: true })
    expect(missingRequiredCustomField([required], {})).toBe('Story points')
    expect(missingRequiredCustomField([required], { req: '   ' })).toBe('Story points')
    expect(missingRequiredCustomField([required], { req: '3' })).toBe('')
    expect(missingRequiredCustomField([field({ required: false })], {})).toBe('')
  })
})

describe('custom field options', () => {
  it('parses value: label lines and generates stable values', () => {
    expect(parseOptionLines('High Priority\nlow: Low\nmedium: Medium')).toEqual([
      { value: 'high-priority', label: 'High Priority' },
      { value: 'low', label: 'Low' },
      { value: 'medium', label: 'Medium' },
    ])
    expect(optionLines([{ value: 'high', label: 'High' }])).toBe('high: High')
  })
})

describe('saved view permissions', () => {
  it('lets owners manage their own views and privileged roles manage all', () => {
    expect(canManageSavedFilter(saved(), 'user-1', 'developer', false)).toBe(true)
    expect(canManageSavedFilter(saved(), 'user-2', 'developer', false)).toBe(false)
    expect(canManageSavedFilter(saved(), 'user-2', 'maintainer', false)).toBe(true)
    expect(canManageSavedFilter(saved(), 'user-2', 'developer', true)).toBe(true)
    expect(canManageSavedFilter(undefined, 'user-1', 'owner', true)).toBe(false)
  })
})

describe('board views', () => {
  it('captures filters, swimlane, sort, visible fields and quick filters', () => {
    const view = saved({
      filters: { search: 'checkout', quick: ['mine', 'bugs'] },
      sort: { field: 'due_soon', direction: 'asc' },
      columns: ['priority', 'labels'],
      swimlane: 'assignee',
    })
    expect(boardSwimlaneFromSaved(view)).toBe('assignee')
    expect(boardSortFromSaved(view)).toEqual({ field: 'due_soon', direction: 'asc' })
    expect(visibleFieldsFromSaved(view)).toEqual(['priority', 'labels'])
    expect(quickFiltersFromSaved(view)).toEqual(['mine', 'bugs'])

    const state = boardViewFromSaved(view, [])
    expect(state.filters.search).toBe('checkout')
    expect(state.swimlane).toBe('assignee')
    expect(state.fields).toEqual(['priority', 'labels'])
    expect(state.quick).toEqual(['mine', 'bugs'])
  })

  it('falls back safely for unknown layout values', () => {
    expect(boardSwimlaneFromSaved(saved({ swimlane: 'epic' }))).toBe('none')
    expect(boardSwimlaneFromSaved(undefined)).toBe('none')
    expect(boardSortFromSaved(saved({ sort: { field: 'chaos' } }))).toEqual({
      field: 'priority',
      direction: 'asc',
    })
    expect(visibleFieldsFromSaved(saved())).toContain('assignee')
    expect(visibleFieldsFromValue('labels,unknown,due_date')).toEqual([
      'labels',
      'due_date',
    ])
    expect(visibleFieldsToSaved(['labels', 'priority', 'nope'])).toEqual(['priority', 'labels'])
  })

  it('round-trips sort ids and quick filter values', () => {
    for (const option of [
      { field: 'priority', direction: 'asc' },
      { field: 'newest', direction: 'desc' },
      { field: 'key', direction: 'desc' },
    ] as const) {
      expect(boardSortFromId(boardSortId(option))).toEqual(option)
    }
    expect(boardSortToSaved({ field: 'oldest', direction: 'asc' })).toEqual({
      field: 'oldest',
      direction: 'asc',
    })
    expect(quickFiltersFromValue('mine, blocked,nope,mine')).toEqual(['mine', 'blocked'])
    expect(quickFiltersToSaved(['bugs', 'bugs', 'mine'])).toEqual(['bugs', 'mine'])
    expect(toggleQuickFilter(['bugs'], 'mine')).toEqual(['bugs', 'mine'])
    expect(toggleQuickFilter(['bugs', 'mine'], 'bugs')).toEqual(['mine'])
  })

  it('chooses the current user default before any shared default', () => {
    const shared = saved({ id: 'shared', owner_id: 'user-2', is_default: true })
    const mine = saved({ id: 'mine', owner_id: 'user-1', is_default: true })
    expect(defaultBoardView([shared, mine], 'user-1')?.id).toBe('mine')
    expect(defaultBoardView([shared], 'user-1')?.id).toBe('shared')
    expect(defaultBoardView([saved()], 'user-1')).toBeUndefined()
  })
})

describe('board ordering', () => {
  it('orders by priority, due date and key with stable tie-breaks', () => {
    const critical = item({ id: 'a', item_number: 3, priority: 'critical', due_date: '2026-12-01T00:00:00Z' })
    const low = item({ id: 'b', item_number: 2, priority: 'low', due_date: null })
    const high = item({ id: 'c', item_number: 1, priority: 'high', due_date: '2026-11-25T00:00:00Z' })

    const priority = [low, critical, high].sort((a, b) =>
      compareBoardItems(a, b, { field: 'priority', direction: 'asc' }),
    )
    expect(priority.map((entry) => entry.id)).toEqual(['a', 'c', 'b'])
    const reversed = [low, critical, high].sort((a, b) =>
      compareBoardItems(a, b, { field: 'priority', direction: 'desc' }),
    )
    expect(reversed.map((entry) => entry.id)).toEqual(['b', 'c', 'a'])

    const due = [low, critical, high].sort((a, b) =>
      compareBoardItems(a, b, { field: 'due_soon', direction: 'asc' }),
    )
    expect(due.map((entry) => entry.id)).toEqual(['c', 'a', 'b'])
    const dueLatest = [low, critical, high].sort((a, b) =>
      compareBoardItems(a, b, { field: 'due_later', direction: 'desc' }),
    )
    expect(dueLatest.map((entry) => entry.id)).toEqual(['a', 'c', 'b'])

    const keyed = [critical, low, high].sort((a, b) =>
      compareBoardItems(a, b, { field: 'key', direction: 'asc' }),
    )
    expect(keyed.map((entry) => entry.id)).toEqual(['c', 'b', 'a'])
  })

  it('keeps unscheduled cards last regardless of direction', () => {
    const scheduled = item({ id: 'a', due_date: '2026-12-01T00:00:00Z' })
    const unscheduled = item({ id: 'b', due_date: null })
    const ordered = [unscheduled, scheduled].sort((a, b) =>
      compareBoardItems(a, b, { field: 'due_later', direction: 'desc' }),
    )
    expect(ordered.map((entry) => entry.id)).toEqual(['a', 'b'])
  })
})

describe('swimlanes and quick filters', () => {
  it('groups by assignee, priority and type in a stable order', () => {
    const items = [
      item({ id: 'a', assignee_id: 'user-2', priority: 'low', type: 'task' }),
      item({ id: 'b', assignee_id: null, priority: 'critical', type: 'bug' }),
      item({ id: 'c', assignee_id: 'user-1', priority: 'high', type: 'story' }),
    ]
    const byAssignee = groupBySwimlane(items, 'assignee', (id) => id)
    expect(byAssignee.map((lane) => lane.key)).toEqual(['user-1', 'user-2', 'unassigned'])
    expect(byAssignee[2]?.label).toBe('Unassigned')

    const byPriority = groupBySwimlane(items, 'priority')
    expect(byPriority.map((lane) => lane.key)).toEqual(['critical', 'high', 'low'])
    expect(byPriority[0]?.label).toBe('Critical')

    const byType = groupBySwimlane(items, 'type')
    expect(byType.map((lane) => lane.key)).toEqual(['story', 'task', 'bug'])

    const none = groupBySwimlane(items, 'none')
    expect(none).toHaveLength(1)
    expect(none[0]?.items).toHaveLength(3)
  })

  it('combines quick filters without weakening the facets', () => {
    const now = new Date('2026-11-22T12:00:00Z')
    const mine = item({ assignee_id: 'user-1' })
    const other = item({ id: 'item-2', assignee_id: 'user-2' })
    const bug = item({ id: 'item-3', type: 'bug', assignee_id: 'user-1' })
    expect(itemMatchesQuickFilters(mine, ['mine'], { userId: 'user-1', now })).toBe(true)
    expect(itemMatchesQuickFilters(other, ['mine'], { userId: 'user-1', now })).toBe(false)
    expect(itemMatchesQuickFilters(mine, ['mine', 'bugs'], { userId: 'user-1', now })).toBe(false)
    expect(itemMatchesQuickFilters(bug, ['mine', 'bugs'], { userId: 'user-1', now })).toBe(true)
    expect(itemMatchesQuickFilters(mine, ['unassigned'], { userId: 'user-1', now })).toBe(false)
    expect(itemMatchesQuickFilters(item({ assignee_id: null }), ['unassigned'], { userId: 'user-1', now })).toBe(true)
    expect(itemMatchesQuickFilters(item({ status: 'blocked' }), ['blocked'], { userId: 'user-1', now })).toBe(true)
  })

  it('treats due_soon as a seven-day horizon and ignores unscheduled cards', () => {
    const now = new Date('2026-11-22T12:00:00Z')
    expect(
      itemMatchesQuickFilters(item({ due_date: '2026-11-25T00:00:00Z' }), ['due_soon'], { userId: 'user-1', now }),
    ).toBe(true)
    expect(
      itemMatchesQuickFilters(item({ due_date: '2026-12-10T00:00:00Z' }), ['due_soon'], { userId: 'user-1', now }),
    ).toBe(false)
    expect(itemMatchesQuickFilters(item({ due_date: null }), ['due_soon'], { userId: 'user-1', now })).toBe(false)
  })
})

describe('WIP policy', () => {
  it('reports no policy for a zero or missing limit', () => {
    expect(wipState(0, 5)).toBeNull()
    expect(wipState(undefined, 5)).toBeNull()
  })

  it('marks reach and exceed separately so a lowered limit is visible', () => {
    expect(wipState(5, 4)).toEqual({ current: 4, limit: 5, reached: false, exceeded: false })
    expect(wipState(5, 5)).toEqual({ current: 5, limit: 5, reached: true, exceeded: false })
    expect(wipState(5, 6)).toEqual({ current: 6, limit: 5, reached: true, exceeded: true })
  })
})

describe('board moves', () => {
  it('resolves the canonical status for a column', () => {
    expect(moveTargetForColumn(item({ status: 'ready' }), 'in_progress')).toMatchObject({
      status: 'in_progress',
      column: { key: 'in_progress' },
    })
    expect(moveTargetForColumn(item({ type: 'bug', status: 'new' }), 'ready')?.status).toBe('triaged')
    expect(moveTargetForColumn(item({ status: 'ready' }), 'missing')).toBeNull()
  })

  it('returns null instead of a status the workflow forbids', () => {
    expect(moveTargetForColumn(item({ status: 'done' }), 'backlog')).toBeNull()
    expect(moveTargetForColumn(item({ status: 'done' }), 'blocked')).toBeNull()
    expect(moveTargetForColumn(item({ status: 'blocked' }), 'ready')?.status).toBe('ready')
  })

  it('walks to the nearest legal column in each direction', () => {
    expect(keyboardMoveTarget(item({ status: 'ready' }), 1)?.column.key).toBe('in_progress')
    expect(keyboardMoveTarget(item({ status: 'blocked' }), -1)?.column.key).toBe('qa')
    expect(keyboardMoveTarget(item({ status: 'done' }), -1)?.column.key).toBe('in_progress')
    expect(keyboardMoveTarget(item({ status: 'done' }), 1)).toBeNull()
    expect(keyboardMoveTarget(item({ status: 'backlog' }), -1)).toBeNull()
  })

  it('announces the new column and position', () => {
    expect(boardMoveAnnouncement('SPM-7', 'Ready', 2, 5)).toBe(
      'SPM-7 moved to Ready, position 2 of 5.',
    )
    expect(boardMoveAnnouncement('SPM-7', 'Ready', 0, 0)).toBe('SPM-7 moved to Ready.')
  })

  it('summarizes per-item bulk outcomes', () => {
    const results = [
      { itemId: 'a', key: 'SPM-1', title: 'A', outcome: 'moved' as const, message: '' },
      { itemId: 'b', key: 'SPM-2', title: 'B', outcome: 'moved' as const, message: '' },
      { itemId: 'c', key: 'SPM-3', title: 'C', outcome: 'failed' as const, message: 'WIP limit' },
      { itemId: 'd', key: 'SPM-4', title: 'D', outcome: 'skipped' as const, message: 'Already here' },
    ]
    expect(summarizeBoardMoves(results)).toEqual({ moved: 2, failed: 1, skipped: 1 })
  })
})

describe('board scale', () => {
  it('groups each lane into columns in one ordered pass', () => {
    const items = [
      item({ id: 'a', key: 'SPM-1', item_number: 1, status: 'ready', priority: 'low' }),
      item({ id: 'b', key: 'SPM-2', item_number: 2, status: 'in_progress', priority: 'critical' }),
      item({ id: 'c', key: 'SPM-3', item_number: 3, status: 'triaged', priority: 'high' }),
      item({ id: 'd', key: 'SPM-4', item_number: 4, status: 'blocked', priority: 'medium' }),
    ]
    const lanes = buildBoardLanes(items, 'none', (id) => id, { field: 'priority', direction: 'asc' })
    expect(lanes).toHaveLength(1)
    expect(lanes[0]!.columns.map((column) => column.items.map((entry) => entry.id))).toEqual([
      [],
      ['c', 'a'],
      ['b'],
      [],
      [],
      [],
      ['d'],
    ])
  })

  it('places 2,000 cards without losing or duplicating any', () => {
    const statuses = ['backlog', 'ready', 'in_progress', 'in_review', 'in_qa', 'done', 'blocked']
    const priorities = ['critical', 'high', 'medium', 'low']
    const types = ['story', 'task', 'bug', 'spike', 'chore', 'subtask']
    const items = Array.from({ length: 2000 }, (_, index) =>
      item({
        id: `item-${index}`,
        key: `SPM-${index + 1}`,
        item_number: index + 1,
        status: statuses[index % statuses.length]!,
        priority: priorities[index % priorities.length]!,
        type: types[index % types.length]!,
      }),
    )
    const started = performance.now()
    const lanes = buildBoardLanes(items, 'type', (id) => id, { field: 'priority', direction: 'asc' })
    const elapsed = performance.now() - started
    const rendered = lanes.flatMap((lane) => lane.columns.flatMap((column) => column.items))
    expect(rendered).toHaveLength(2000)
    expect(new Set(rendered.map((entry) => entry.id)).size).toBe(2000)
    // Generous headroom for shared CI runners; the local pass is well under 50 ms.
    expect(elapsed).toBeLessThan(250)
  })

  it('counts WIP once per effective item, including shared statuses', () => {
    const items = [
      item({ id: 'a', status: 'backlog' }),
      item({ id: 'b', status: 'new' }),
      item({ id: 'c', status: 'cancelled' }),
      item({ id: 'd', status: 'in_progress' }),
      item({ id: 'e', status: 'done' }),
      item({ id: 'f', status: 'closed' }),
    ]
    expect(boardWipCounts(items)).toMatchObject({
      backlog: 3,
      in_progress: 1,
      done: 2,
    })
  })

  it('pages columns and clamps the final page', () => {
    expect(nextVisibleCount(0, 285)).toBe(BOARD_COLUMN_PAGE_SIZE)
    expect(nextVisibleCount(BOARD_COLUMN_PAGE_SIZE, 285)).toBe(BOARD_COLUMN_PAGE_SIZE * 2)
    expect(nextVisibleCount(280, 285)).toBe(285)
    expect(nextVisibleCount(0, 7)).toBe(7)
  })
})
