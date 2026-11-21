import { ApiError } from '../api/client'
import type { BugDuplicate, BulkBugItem, BulkBugResult, Deployment, TestResult, TestRun, TestStepResult } from '../types/workspace'

export interface BugContextInput {
  runName: string
  runStatus: string
  caseTitle: string
  caseRevision: number
  planName?: string
  suiteName?: string
  step?: { position: number; action: string; status: string }
  environmentName?: string
  releaseName?: string
  sprintName?: string
  buildLabel?: string
  deploymentLabel?: string
}

export function normalizeBugTitle(title: string): string {
  return title.trim().replace(/\s+/g, ' ').toLowerCase()
}

export function autoBugTitle(caseTitle: string, step?: TestStepResult | null): string {
  if (!step) return `Defect: ${caseTitle}`
  const action = step.action.trim()
  return `Defect: ${caseTitle} — step ${step.position}${action ? `: ${action}` : ''}`
}

export function autoReproduction(steps: TestStepResult[]): string {
  return steps
    .map((step, index) => `${step.position > 0 ? step.position : index + 1}. ${step.action}`)
    .join('\n')
    .trim()
}

export function isFailedResult(result: Pick<TestResult, 'status' | 'step_results'>): boolean {
  return result.status === 'failed' || (result.step_results ?? []).some((step) => step.status === 'failed')
}

export function failedStep(steps: TestStepResult[]): TestStepResult | undefined {
  return steps.find((step) => step.status === 'failed')
}

export function bugContextSummary(input: BugContextInput): string[] {
  const lines = [`Run: ${input.runName} (${input.runStatus})`, `Case: ${input.caseTitle} · revision ${input.caseRevision}`]
  if (input.planName) lines.push(`Plan: ${input.planName}`)
  if (input.suiteName) lines.push(`Suite: ${input.suiteName}`)
  if (input.step) lines.push(`Step ${input.step.position}: ${input.step.action} (${input.step.status})`)
  if (input.environmentName) lines.push(`Environment: ${input.environmentName}`)
  if (input.releaseName) lines.push(`Release: ${input.releaseName}`)
  if (input.sprintName) lines.push(`Sprint: ${input.sprintName}`)
  if (input.buildLabel) lines.push(`Build: ${input.buildLabel}`)
  if (input.deploymentLabel) lines.push(`Deployment: ${input.deploymentLabel}`)
  return lines
}

export function deploymentForRun(run: Pick<TestRun, 'build_id' | 'environment_id'>, deployments: Deployment[]): Deployment | null {
  const byBuild = run.build_id ? deployments.filter((deployment) => deployment.build_id === run.build_id) : []
  const candidates = byBuild.length
    ? byBuild
    : run.environment_id
      ? deployments.filter((deployment) => deployment.environment_id === run.environment_id)
      : []
  if (!candidates.length) return null
  return [...candidates].sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))[0] ?? null
}

export function deploymentLabel(deployment: Deployment | null, environments: Array<{ id: string; name: string }>): string {
  if (!deployment) return ''
  const environment = environments.find((entry) => entry.id === deployment.environment_id)?.name
  const state = deployment.health_status && deployment.health_status !== 'unknown'
    ? `${deployment.status}/${deployment.health_status}`
    : deployment.status
  return environment ? `${state} on ${environment}` : state
}

export function duplicatesFromError(error: unknown): BugDuplicate[] {
  if (!(error instanceof ApiError) || error.code !== 'DUPLICATE_BUG') return []
  const details = error.details as { duplicates?: unknown } | null | undefined
  if (!details || !Array.isArray(details.duplicates)) return []
  return details.duplicates.filter((duplicate): duplicate is BugDuplicate => {
    if (!duplicate || typeof duplicate !== 'object') return false
    const candidate = duplicate as Partial<BugDuplicate>
    return typeof candidate.id === 'string' && typeof candidate.key === 'string' && typeof candidate.title === 'string'
  })
}

export function duplicateMatchLabel(duplicate: BugDuplicate): string {
  return duplicate.match_on === 'test_result' ? 'same test result' : 'same title'
}

const bulkReasons: Record<string, string> = {
  already_linked: 'Already linked to a bug',
  not_failed: 'No failed step remains',
  open_bug_exists: 'Open duplicate exists',
  load_failed: 'Could not load the result',
  duplicate_check_failed: 'Duplicate check failed',
  create_failed: 'Bug creation failed',
}

export function bulkItemReason(reason: string | undefined): string {
  if (!reason) return ''
  return bulkReasons[reason] ?? reason.split('_').join(' ')
}

export function bulkItemLabel(item: BulkBugItem): string {
  switch (item.status) {
    case 'created':
      return item.key ? `Created ${item.key}` : 'Bug created'
    case 'duplicate':
      return item.duplicates?.length
        ? `Duplicate of ${item.duplicates.map((duplicate) => duplicate.key).join(', ')}`
        : 'Duplicate bug'
    case 'skipped':
      return bulkItemReason(item.reason) || 'Skipped'
    default:
      return bulkItemReason(item.reason) || 'Failed'
  }
}

export function bulkSummaryLabel(result: BulkBugResult): string {
  const parts = [`${result.created} created`]
  if (result.duplicates) parts.push(`${result.duplicates} duplicate`)
  if (result.skipped) parts.push(`${result.skipped} skipped`)
  if (result.failed) parts.push(`${result.failed} failed`)
  return parts.join(' · ')
}

export function defaultBulkSelection(results: TestResult[]): string[] {
  return results.filter((result) => isFailedResult(result) && !result.bug_work_item_id).map((result) => result.id)
}
