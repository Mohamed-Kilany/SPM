import { describe, expect, it } from 'vitest'
import {
  boardFilterCount,
  boardFiltersFromQuery,
  boardFiltersToQuery,
  boardFiltersToSaved,
  canManageSavedFilter,
  colorForLabel,
  customFieldDisplay,
  customFieldDraft,
  customFieldPayload,
  customValueMap,
  labelDot,
  labelTone,
  missingRequiredCustomField,
  normalizeLabelInput,
  optionLines,
  parseOptionLines,
  savedFilterToBoard,
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
    owner_id: 'user-1', filters: {}, sort: {}, columns: [], pinned: false, is_default: false,
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
