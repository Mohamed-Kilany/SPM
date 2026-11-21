import type { TestRun, TestStatus } from '../types/workspace'

// Pure helpers for the focused test runner. Keeping step flattening, progress,
// shortcuts, resume parsing and autosave debouncing outside the component makes
// them unit-testable without mounting Vue or a browser.

export interface RunnerStep {
  key: string
  resultId: string
  stepResultId: string
  caseId: string
  caseTitle: string
  caseRevision: number
  position: number
  action: string
  expectedResult: string
  testData: string
  status: TestStatus | string
  actualResult: string
  evidenceUrl: string
  bugWorkItemId: string | null
}

export interface RunnerCounts {
  total: number
  passed: number
  failed: number
  blocked: number
  skipped: number
  notRun: number
  executed: number
  progress: number
}

export type RunnerStatus = Exclude<TestStatus, 'not_run'>

export function flattenRunSteps(run: TestRun | null, caseTitle: (caseId: string) => string): RunnerStep[] {
  if (!run) return []
  const steps: RunnerStep[] = []
  for (const result of run.results ?? []) {
    const ordered = [...(result.step_results ?? [])].sort((a, b) => a.position - b.position)
    for (const step of ordered) {
      steps.push({
        key: step.id,
        resultId: result.id,
        stepResultId: step.id,
        caseId: result.test_case_id,
        caseTitle: caseTitle(result.test_case_id),
        caseRevision: step.case_revision || result.case_revision || 1,
        position: step.position,
        action: step.action || 'Test step',
        expectedResult: step.expected_result || '—',
        testData: step.test_data,
        status: step.status,
        actualResult: step.actual_result,
        evidenceUrl: step.evidence_url,
        bugWorkItemId: step.bug_work_item_id,
      })
    }
  }
  return steps
}

export function runnerCounts(steps: RunnerStep[]): RunnerCounts {
  const counts: RunnerCounts = {
    total: steps.length,
    passed: 0,
    failed: 0,
    blocked: 0,
    skipped: 0,
    notRun: 0,
    executed: 0,
    progress: 0,
  }
  for (const step of steps) {
    switch (step.status) {
      case 'passed':
        counts.passed += 1
        break
      case 'failed':
        counts.failed += 1
        break
      case 'blocked':
        counts.blocked += 1
        break
      case 'skipped':
        counts.skipped += 1
        break
      default:
        counts.notRun += 1
        break
    }
  }
  counts.executed = counts.total - counts.notRun
  counts.progress = counts.total ? Math.round((counts.executed / counts.total) * 100) : 0
  return counts
}

// p/f/b/s mirror the required keyboard shortcuts.
export function shortcutStatus(key: string): RunnerStatus | null {
  switch (key.toLowerCase()) {
    case 'p':
      return 'passed'
    case 'f':
      return 'failed'
    case 'b':
      return 'blocked'
    case 's':
      return 'skipped'
    default:
      return null
  }
}

export function moveIndex(total: number, current: number, direction: 1 | -1): number {
  if (total <= 0) return -1
  return Math.min(total - 1, Math.max(0, current + direction))
}

// nextPendingIndex skips executed steps, wraps once and returns -1 when every
// step has a result.
export function nextPendingIndex(steps: RunnerStep[], from: number): number {
  if (!steps.length) return -1
  for (let offset = 1; offset <= steps.length; offset += 1) {
    const index = (from + offset) % steps.length
    if (steps[index].status === 'not_run') return index
  }
  return -1
}

export function firstPendingIndex(steps: RunnerStep[]): number {
  const index = steps.findIndex((step) => step.status === 'not_run')
  return index >= 0 ? index : 0
}

export function passRate(passed: number, failed: number): number {
  const executed = passed + failed
  return executed ? Math.round((passed / executed) * 100) : 0
}

export function runStorageKey(projectId: string, runId: string): string {
  return `spm.runner.${projectId}.${runId}`
}

export interface RunnerResume {
  stepResultId?: string
  notes: Record<string, string>
  evidence: Record<string, string>
}

function stringRecord(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const result: Record<string, string> = {}
  for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
    if (typeof entry === 'string') result[key] = entry
  }
  return result
}

// parseRunnerResume is deliberately tolerant: corrupt or stale local storage
// must never block opening a run.
export function parseRunnerResume(raw: string | null): RunnerResume | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    const record = parsed as Record<string, unknown>
    return {
      stepResultId: typeof record.stepResultId === 'string' ? record.stepResultId : undefined,
      notes: stringRecord(record.notes),
      evidence: stringRecord(record.evidence),
    }
  } catch {
    return null
  }
}

export interface Debouncer {
  schedule: () => void
  flush: () => void
  cancel: () => void
}

// createDebouncer batches autosaves. flush makes finish/close deterministic
// instead of racing a pending timer.
export function createDebouncer(fn: () => void, delay = 700): Debouncer {
  let timer: ReturnType<typeof setTimeout> | null = null
  const cancel = (): void => {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }
  return {
    schedule: () => {
      cancel()
      timer = setTimeout(() => {
        timer = null
        fn()
      }, delay)
    },
    flush: () => {
      if (timer !== null) {
        cancel()
        fn()
      }
    },
    cancel,
  }
}
