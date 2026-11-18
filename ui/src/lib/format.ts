const relativeTime = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

/** Human readable relative time, e.g. "3 hours ago" or "in 2 days". */
export function timeAgo(value: string | Date | null | undefined): string {
  if (!value) return 'just now'
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return 'just now'

  const diffSeconds = Math.round((date.getTime() - Date.now()) / 1000)
  const absSeconds = Math.abs(diffSeconds)

  if (absSeconds < 45) return 'just now'
  if (absSeconds < 3600) {
    const minutes = Math.round(diffSeconds / 60)
    return relativeTime.format(minutes === 0 ? 1 : minutes, 'minute')
  }
  if (absSeconds < 86_400) return relativeTime.format(Math.round(diffSeconds / 3600), 'hour')
  if (absSeconds < 2_592_000) return relativeTime.format(Math.round(diffSeconds / 86_400), 'day')
  if (absSeconds < 31_536_000) return relativeTime.format(Math.round(diffSeconds / 2_592_000), 'month')
  return relativeTime.format(Math.round(diffSeconds / 31_536_000), 'year')
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('en', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

/** Short display identifier used for people and Git objects. */
export function shortId(value: string | null | undefined, length = 8): string {
  if (!value) return '—'
  return value.slice(0, length)
}

/** Requirement 9.2: first two characters of the assignee UUID. */
export function personInitials(userId: string | null | undefined): string {
  if (!userId) return ''
  return userId.slice(0, 2).toUpperCase()
}

export function ticketCountLabel(count: number): string {
  return `${count} ${count === 1 ? 'ticket' : 'tickets'}`
}

export const priorityRank: Record<string, number> = {
  critical: 0,
  high: 1,
  medium: 2,
  low: 3,
}

export const priorityLabel: Record<string, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
}

export const pipelineLabel: Record<string, string> = {
  pending: 'Queued',
  in_progress: 'Running',
  success: 'Passed',
  failure: 'Failed',
  cancelled: 'Cancelled',
  timed_out: 'Timed out',
  '': 'No runs',
}
