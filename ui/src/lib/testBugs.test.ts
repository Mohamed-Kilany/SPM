import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import {
  autoBugTitle,
  autoReproduction,
  bugContextSummary,
  bulkItemLabel,
  bulkSummaryLabel,
  defaultBulkSelection,
  deploymentForRun,
  deploymentLabel,
  duplicateMatchLabel,
  duplicatesFromError,
  isFailedResult,
  normalizeBugTitle,
} from './testBugs'
import type { BugDuplicate, BulkBugItem, Deployment, TestResult, TestStepResult } from '../types/workspace'

function step(overrides: Partial<TestStepResult> = {}): TestStepResult {
  return {
    id: 'step-1', test_result_id: 'result-1', test_step_id: 'case-step-1', case_revision: 2,
    position: 1, action: 'Open checkout', expected_result: 'Checkout opens', test_data: '',
    status: 'passed', actual_result: '', evidence_url: '', bug_work_item_id: null,
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...overrides,
  }
}

function result(overrides: Partial<TestResult> = {}): TestResult {
  return {
    id: 'result-1', test_run_id: 'run-1', test_case_id: 'case-1', case_revision: 2,
    status: 'failed', executed_by: null, duration_ms: null, actual_result: 'Hung',
    bug_work_item_id: null, started_at: null, completed_at: null,
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    step_results: [step()], ...overrides,
  }
}

function deployment(overrides: Partial<Deployment> = {}): Deployment {
  return {
    id: 'deployment-1', project_id: 'project-1', environment_id: 'env-1', build_id: 'build-1',
    release_id: null, status: 'healthy', health_status: 'healthy', version: '', argo_app: '',
    argo_revision: '', metadata: {}, deployed_by: null, previous_deployment_id: null,
    rollback_of_id: null, started_at: null, completed_at: null,
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...overrides,
  }
}

function duplicate(overrides: Partial<BugDuplicate> = {}): BugDuplicate {
  return { id: 'bug-1', key: 'SPM-7', title: 'Checkout hangs', status: 'new', match_on: 'title', ...overrides }
}

describe('testBugs helpers', () => {
  it('normalizes titles for duplicate comparison', () => {
    expect(normalizeBugTitle('  Defect:   Checkout\tHangs ')).toBe('defect: checkout hangs')
    expect(normalizeBugTitle('')).toBe('')
  })

  it('builds an auto title from the failed step', () => {
    expect(autoBugTitle('Checkout', step({ position: 2, action: 'Confirm payment' }))).toBe(
      'Defect: Checkout — step 2: Confirm payment',
    )
    expect(autoBugTitle('Checkout', null)).toBe('Defect: Checkout')
    expect(autoBugTitle('Checkout', step({ position: 3, action: '  ' }))).toBe('Defect: Checkout — step 3')
  })

  it('builds reproduction steps with positional fallback', () => {
    expect(autoReproduction([step({ position: 0, action: 'Open' }), step({ position: 4, action: 'Pay' })])).toBe(
      '1. Open\n4. Pay',
    )
    expect(autoReproduction([])).toBe('')
  })

  it('treats a result with a failed step as failed', () => {
    expect(isFailedResult(result())).toBe(true)
    expect(isFailedResult(result({ status: 'passed', step_results: [step({ status: 'failed' })] }))).toBe(true)
    expect(isFailedResult(result({ status: 'passed', step_results: [step()] }))).toBe(false)
  })

  it('summarizes the captured test context', () => {
    const lines = bugContextSummary({
      runName: 'Checkout regression', runStatus: 'in_progress', caseTitle: 'Checkout', caseRevision: 4,
      planName: 'Release plan', suiteName: 'Smoke',
      step: { position: 2, action: 'Confirm payment', status: 'failed' },
      environmentName: 'staging', releaseName: '2026.11', sprintName: 'Sprint 12',
      buildLabel: '#1841 (0123456)', deploymentLabel: 'healthy/healthy on staging',
    })
    expect(lines).toContain('Run: Checkout regression (in_progress)')
    expect(lines).toContain('Case: Checkout · revision 4')
    expect(lines).toContain('Step 2: Confirm payment (failed)')
    expect(lines).toContain('Build: #1841 (0123456)')
    expect(lines).toContain('Deployment: healthy/healthy on staging')
    expect(
      bugContextSummary({ runName: 'Run', runStatus: 'planned', caseTitle: 'Case', caseRevision: 1 }),
    ).toEqual(['Run: Run (planned)', 'Case: Case · revision 1'])
  })

  it('selects the newest deployment for the run build, then environment', () => {
    const older = deployment({ id: 'old', created_at: '2026-01-01T00:00:00Z' })
    const newer = deployment({ id: 'new', created_at: '2026-01-02T00:00:00Z' })
    const otherEnvironment = deployment({ id: 'other', build_id: 'build-2', environment_id: 'env-2' })

    expect(deploymentForRun({ build_id: 'build-1', environment_id: 'env-1' }, [older, newer, otherEnvironment])?.id).toBe('new')
    expect(
      deploymentForRun(
        { build_id: 'missing', environment_id: 'env-2' },
        [older, otherEnvironment],
      )?.id,
    ).toBe('other')
    expect(deploymentForRun({ build_id: null, environment_id: null }, [older])).toBeNull()
  })

  it('labels a deployment with its environment', () => {
    expect(deploymentLabel(deployment(), [{ id: 'env-1', name: 'staging' }])).toBe('healthy/healthy on staging')
    expect(deploymentLabel(deployment({ health_status: 'unknown' }), [{ id: 'env-1', name: 'staging' }])).toBe(
      'healthy on staging',
    )
    expect(deploymentLabel(null, [])).toBe('')
  })

  it('extracts duplicates from a DUPLICATE_BUG error only', () => {
    const error = new ApiError('duplicate', 409, 'DUPLICATE_BUG', { duplicates: [duplicate()] })
    expect(duplicatesFromError(error)).toEqual([duplicate()])
    expect(duplicatesFromError(new ApiError('conflict', 409, 'REVISION_CONFLICT', { duplicates: [duplicate()] }))).toEqual([])
    expect(duplicatesFromError(new ApiError('bad', 409, 'DUPLICATE_BUG', { duplicates: [{ id: 'x' }] }))).toEqual([])
    expect(duplicatesFromError(new Error('network'))).toEqual([])
    expect(duplicateMatchLabel(duplicate({ match_on: 'test_result' }))).toBe('same test result')
    expect(duplicateMatchLabel(duplicate())).toBe('same title')
  })

  it('labels bulk items and summaries', () => {
    const items: BulkBugItem[] = [
      { result_id: 'r1', status: 'created', key: 'SPM-1' },
      { result_id: 'r2', status: 'duplicate', duplicates: [duplicate()] },
      { result_id: 'r3', status: 'skipped', reason: 'already_linked' },
      { result_id: 'r4', status: 'failed', reason: 'create_failed' },
    ]
    expect(bulkItemLabel(items[0])).toBe('Created SPM-1')
    expect(bulkItemLabel(items[1])).toBe('Duplicate of SPM-7')
    expect(bulkItemLabel(items[2])).toBe('Already linked to a bug')
    expect(bulkItemLabel(items[3])).toBe('Bug creation failed')
    expect(
      bulkSummaryLabel({ run_id: 'run-1', requested: 4, created: 1, duplicates: 1, skipped: 1, failed: 1, items }),
    ).toBe('1 created · 1 duplicate · 1 skipped · 1 failed')
  })

  it('preselects unlinked failures for bulk creation', () => {
    const selected = defaultBulkSelection([
      result({ id: 'r1' }),
      result({ id: 'r2', bug_work_item_id: 'bug-1' }),
      result({ id: 'r3', status: 'passed', step_results: [step()] }),
      result({ id: 'r4', status: 'blocked', step_results: [step({ status: 'failed' })] }),
    ])
    expect(selected).toEqual(['r1', 'r4'])
  })
})
