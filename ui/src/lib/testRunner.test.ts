import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TestRun } from '../types/workspace'
import {
  createDebouncer,
  firstPendingIndex,
  flattenRunSteps,
  moveIndex,
  nextPendingIndex,
  parseRunnerResume,
  passRate,
  runnerCounts,
  shortcutStatus,
} from './testRunner'

function fixtureRun(): TestRun {
  return {
    id: 'run-1',
    project_id: 'project-1',
    results: [
      {
        id: 'result-1',
        test_case_id: 'case-1',
        case_revision: 3,
        status: 'failed',
        actual_result: '',
        step_results: [
          {
            id: 'step-b',
            test_result_id: 'result-1',
            case_revision: 3,
            position: 2,
            action: 'Confirm payment',
            expected_result: 'Receipt is shown',
            test_data: 'card 4242',
            status: 'failed',
            actual_result: 'HTTP 500',
            evidence_url: '/evidence/b',
            bug_work_item_id: 'bug-1',
          },
          {
            id: 'step-a',
            test_result_id: 'result-1',
            case_revision: 3,
            position: 1,
            action: 'Open checkout',
            expected_result: 'Payment action is available',
            test_data: '',
            status: 'passed',
            actual_result: '',
            evidence_url: '',
            bug_work_item_id: null,
          },
        ],
      },
      {
        id: 'result-2',
        test_case_id: 'case-2',
        case_revision: 1,
        status: 'not_run',
        actual_result: '',
        step_results: [
          {
            id: 'step-c',
            test_result_id: 'result-2',
            case_revision: 1,
            position: 1,
            action: 'Sign in',
            expected_result: 'Dashboard is shown',
            test_data: '',
            status: 'not_run',
            actual_result: '',
            evidence_url: '',
            bug_work_item_id: null,
          },
        ],
      },
    ],
  } as unknown as TestRun
}

describe('flattenRunSteps', () => {
  it('orders steps per result and snapshots case titles and revisions', () => {
    const steps = flattenRunSteps(fixtureRun(), (caseId) => (caseId === 'case-1' ? 'Checkout' : 'Sign in'))
    expect(steps.map((step) => step.stepResultId)).toEqual(['step-a', 'step-b', 'step-c'])
    expect(steps[1]).toMatchObject({
      caseTitle: 'Checkout',
      caseRevision: 3,
      expectedResult: 'Receipt is shown',
      bugWorkItemId: 'bug-1',
    })
  })

  it('returns an empty list without a run', () => {
    expect(flattenRunSteps(null, () => 'unused')).toEqual([])
  })
})

describe('runnerCounts', () => {
  it('counts statuses and progress', () => {
    const steps = flattenRunSteps(fixtureRun(), (caseId) => caseId)
    const counts = runnerCounts(steps)
    expect(counts).toMatchObject({ total: 3, passed: 1, failed: 1, notRun: 1, executed: 2, progress: 67 })
  })

  it('handles an empty run', () => {
    expect(runnerCounts([]).progress).toBe(0)
  })
})

describe('shortcutStatus', () => {
  it('maps P/F/B/S case-insensitively', () => {
    expect(shortcutStatus('p')).toBe('passed')
    expect(shortcutStatus('F')).toBe('failed')
    expect(shortcutStatus('b')).toBe('blocked')
    expect(shortcutStatus('S')).toBe('skipped')
  })

  it('ignores other keys', () => {
    expect(shortcutStatus('x')).toBeNull()
    expect(shortcutStatus('ArrowRight')).toBeNull()
  })
})

describe('navigation helpers', () => {
  it('clamps movement at both ends', () => {
    expect(moveIndex(3, 0, -1)).toBe(0)
    expect(moveIndex(3, 2, 1)).toBe(2)
    expect(moveIndex(3, 1, 1)).toBe(2)
    expect(moveIndex(0, 0, 1)).toBe(-1)
  })

  it('finds the next pending step with a single wrap', () => {
    const steps = flattenRunSteps(fixtureRun(), (caseId) => caseId)
    expect(nextPendingIndex(steps, 0)).toBe(2)
    expect(nextPendingIndex(steps, 2)).toBe(2)
    expect(firstPendingIndex(steps)).toBe(2)
  })

  it('returns -1 when every step is executed', () => {
    const steps = flattenRunSteps(fixtureRun(), (caseId) => caseId).map((step) => ({
      ...step,
      status: 'passed',
    }))
    expect(nextPendingIndex(steps, 0)).toBe(-1)
  })
})

describe('passRate', () => {
  it('uses only executed pass/fail outcomes', () => {
    expect(passRate(8, 2)).toBe(80)
    expect(passRate(0, 0)).toBe(0)
    expect(passRate(1, 0)).toBe(100)
  })
})

describe('parseRunnerResume', () => {
  it('accepts the stored shape and filters non-string drafts', () => {
    const resume = parseRunnerResume(
      JSON.stringify({
        stepResultId: 'step-a',
        notes: { 'step-a': 'looks good', 'step-b': 4 },
        evidence: { 'step-a': '/evidence/a' },
      }),
    )
    expect(resume).toEqual({
      stepResultId: 'step-a',
      notes: { 'step-a': 'looks good' },
      evidence: { 'step-a': '/evidence/a' },
    })
  })

  it('tolerates corrupt or empty storage', () => {
    expect(parseRunnerResume(null)).toBeNull()
    expect(parseRunnerResume('not json')).toBeNull()
    expect(parseRunnerResume('[]')).toBeNull()
    expect(parseRunnerResume('{}')).toEqual({ stepResultId: undefined, notes: {}, evidence: {} })
  })
})

describe('createDebouncer', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('collapses rapid schedules into one call', () => {
    const fn = vi.fn()
    const debouncer = createDebouncer(fn, 500)
    debouncer.schedule()
    debouncer.schedule()
    debouncer.schedule()
    vi.advanceTimersByTime(499)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('flush runs a pending save immediately and cancel drops it', () => {
    const fn = vi.fn()
    const debouncer = createDebouncer(fn, 500)
    debouncer.schedule()
    debouncer.flush()
    expect(fn).toHaveBeenCalledTimes(1)
    debouncer.schedule()
    debouncer.cancel()
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)
  })
})
