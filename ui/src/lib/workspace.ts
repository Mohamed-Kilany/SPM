import type { WorkItem } from '../types/workspace'

export const workItemTypeLabels: Record<string, string> = {
  story: 'Story',
  task: 'Task',
  subtask: 'Subtask',
  bug: 'Bug',
  spike: 'Spike',
  chore: 'Chore',
}

export const statusLabels: Record<string, string> = {
  // Planning
  draft: 'Draft',
  in_review: 'In review',
  approved: 'Approved',
  superseded: 'Superseded',
  planned: 'Planned',
  released: 'Released',
  // Work items
  backlog: 'Backlog',
  ready: 'Ready',
  in_progress: 'In progress',
  in_qa: 'In QA',
  done: 'Done',
  blocked: 'Blocked',
  cancelled: 'Cancelled',
  new: 'New',
  triaged: 'Triaged',
  fixed: 'Fixed',
  verified: 'Verified',
  closed: 'Closed',
  reopened: 'Reopened',
  // Tests
  not_run: 'Not run',
  passed: 'Passed',
  failed: 'Failed',
  skipped: 'Skipped',
  // Delivery
  queued: 'Queued',
  running: 'Running',
  success: 'Passed',
  failure: 'Failed',
  timed_out: 'Timed out',
  deploying: 'Deploying',
  healthy: 'Healthy',
  degraded: 'Degraded',
  unhealthy: 'Unhealthy',
  unknown: 'Unknown',
  rolled_back: 'Rolled back',
  open: 'Open',
  merged: 'Merged',
  pending: 'Pending',
  changes_requested: 'Changes requested',
  completed: 'Completed',
  active: 'Active',
  archived: 'Archived',
}

export function statusLabel(status: string | null | undefined): string {
  if (!status) return 'Unknown'
  return statusLabels[status] ?? status.replace(/_/g, ' ')
}

const emerald = 'bg-emerald-50 text-emerald-700 ring-emerald-600/20'
const red = 'bg-red-50 text-red-700 ring-red-600/20'
const amber = 'bg-amber-50 text-amber-700 ring-amber-600/20'
const violet = 'bg-violet-50 text-violet-700 ring-violet-600/20'
const sky = 'bg-sky-50 text-sky-700 ring-sky-600/20'
const cyan = 'bg-cyan-50 text-cyan-700 ring-cyan-600/20'
const slate = 'bg-slate-100 text-slate-600 ring-slate-500/20'
const indigo = 'bg-indigo-50 text-indigo-700 ring-indigo-600/20'

export function statusTone(status: string | null | undefined): string {
  switch (status) {
    case 'done':
    case 'closed':
    case 'verified':
    case 'passed':
    case 'success':
    case 'healthy':
    case 'merged':
    case 'released':
    case 'approved':
      return emerald
    case 'failed':
    case 'failure':
    case 'unhealthy':
    case 'blocked':
    case 'degraded':
    case 'timed_out':
      return red
    case 'in_progress':
    case 'running':
    case 'deploying':
    case 'queued':
    case 'pending':
    case 'fixed':
      return amber
    case 'in_review':
    case 'changes_requested':
      return violet
    case 'in_qa':
      return cyan
    case 'ready':
    case 'triaged':
      return sky
    case 'active':
      return indigo
    case 'draft':
    case 'backlog':
    case 'new':
    case 'not_run':
    case 'planned':
    case 'unknown':
    case 'cancelled':
    case 'rolled_back':
    case 'superseded':
    case 'skipped':
    case 'open':
      return slate
    default:
      return slate
  }
}

export const priorityLabels: Record<string, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
}

export function priorityTone(priority: string | null | undefined): string {
  switch (priority) {
    case 'critical':
      return 'bg-red-50 text-red-700 ring-red-600/20'
    case 'high':
      return 'bg-orange-50 text-orange-700 ring-orange-600/20'
    case 'medium':
      return 'bg-amber-50 text-amber-700 ring-amber-600/20'
    case 'low':
      return 'bg-slate-100 text-slate-600 ring-slate-500/20'
    default:
      return 'bg-slate-100 text-slate-600 ring-slate-500/20'
  }
}

export interface BoardColumn {
  key: string
  label: string
  statuses: string[]
  accent: string
}

export const boardColumns: BoardColumn[] = [
  { key: 'backlog', label: 'Backlog', statuses: ['backlog', 'new', 'reopened', 'cancelled'], accent: 'bg-slate-400' },
  { key: 'ready', label: 'Ready', statuses: ['ready', 'triaged'], accent: 'bg-sky-400' },
  { key: 'in_progress', label: 'In progress', statuses: ['in_progress'], accent: 'bg-amber-400' },
  { key: 'review', label: 'Review', statuses: ['in_review', 'fixed'], accent: 'bg-violet-400' },
  { key: 'qa', label: 'QA', statuses: ['in_qa', 'verified'], accent: 'bg-cyan-400' },
  { key: 'done', label: 'Done', statuses: ['done', 'closed'], accent: 'bg-emerald-400' },
  { key: 'blocked', label: 'Blocked', statuses: ['blocked'], accent: 'bg-red-400' },
]

export function boardColumnFor(status: string): BoardColumn {
  return boardColumns.find((column) => column.statuses.includes(status)) ?? boardColumns[0]!
}

export function targetStatusForColumn(type: string, columnKey: string): string {
  if (type === 'bug') {
    switch (columnKey) {
      case 'backlog':
        return 'new'
      case 'ready':
        return 'triaged'
      case 'in_progress':
        return 'in_progress'
      case 'review':
        return 'fixed'
      case 'qa':
        return 'verified'
      case 'done':
        return 'closed'
      case 'blocked':
        return 'blocked'
      default:
        return 'new'
    }
  }
  switch (columnKey) {
    case 'backlog':
      return 'backlog'
    case 'ready':
      return 'ready'
    case 'in_progress':
      return 'in_progress'
    case 'review':
      return 'in_review'
    case 'qa':
      return 'in_qa'
    case 'done':
      return 'done'
    case 'blocked':
      return 'blocked'
    default:
      return 'backlog'
  }
}

const standardTransitions: Record<string, string[]> = {
  backlog: ['ready', 'in_progress', 'blocked', 'cancelled'],
  ready: ['backlog', 'in_progress', 'blocked', 'cancelled'],
  in_progress: ['in_review', 'in_qa', 'done', 'blocked', 'cancelled'],
  in_review: ['in_progress', 'in_qa', 'done', 'blocked'],
  in_qa: ['in_progress', 'in_review', 'done', 'blocked'],
  done: ['in_progress'],
  blocked: ['ready', 'in_progress', 'in_review', 'in_qa', 'cancelled'],
  cancelled: ['backlog', 'ready'],
}

const bugTransitions: Record<string, string[]> = {
  new: ['triaged', 'cancelled'],
  triaged: ['in_progress', 'cancelled'],
  in_progress: ['fixed', 'blocked', 'cancelled'],
  fixed: ['verified', 'reopened'],
  verified: ['closed', 'reopened'],
  closed: ['reopened'],
  reopened: ['triaged', 'in_progress'],
  blocked: ['in_progress', 'cancelled'],
  cancelled: [],
}

export function allowedTransitions(type: string, status: string): string[] {
  const map = type === 'bug' ? bugTransitions : standardTransitions
  return map[status] ?? []
}

export function workItemStatusOptions(type: string): string[] {
  return type === 'bug'
    ? ['new', 'triaged', 'in_progress', 'fixed', 'verified', 'closed', 'reopened', 'blocked', 'cancelled']
    : ['backlog', 'ready', 'in_progress', 'in_review', 'in_qa', 'done', 'blocked', 'cancelled']
}

export function defaultStatusFor(type: string): string {
  return type === 'bug' ? 'new' : 'backlog'
}

export function slugify(value: string): string {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48)
}

export function branchNameFor(item: WorkItem): string {
  const prefix = item.type === 'bug' ? 'bugfix' : item.type === 'chore' ? 'chore' : 'feature'
  return `${prefix}/${item.key.toLowerCase()}-${slugify(item.title) || 'work'}`
}

export function dateInputValue(value: string | null | undefined): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 10)
}

export function percent(value: number | null | undefined): string {
  const safe = Number.isFinite(value) ? Number(value) : 0
  return `${Math.round(safe)}%`
}

export function isOpenBug(item: WorkItem): boolean {
  return item.type === 'bug' && !['closed', 'verified', 'cancelled'].includes(item.status)
}
