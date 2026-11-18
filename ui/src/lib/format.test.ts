import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  formatDateTime,
  personInitials,
  pipelineLabel,
  priorityLabel,
  shortId,
  ticketCountLabel,
  timeAgo,
} from './format'

describe('format helpers', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-30T12:00:00.000Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('produces stable relative labels', () => {
    expect(timeAgo('2026-10-30T11:59:50.000Z')).toBe('just now')
    expect(timeAgo('2026-10-30T09:00:00.000Z')).toBe('3 hours ago')
    expect(timeAgo('2026-10-27T12:00:00.000Z')).toBe('3 days ago')
    expect(timeAgo(null)).toBe('just now')
    expect(timeAgo('not-a-date')).toBe('just now')
  })

  it('uses the first two characters of the assignee UUID', () => {
    expect(personInitials('ab12cd34-0000-0000-0000-000000000000')).toBe('AB')
    expect(personInitials(null)).toBe('')
  })

  it('formats identifiers, labels and counts', () => {
    expect(shortId('0192f3a4-1111-7000-8000-000000000001')).toBe('0192f3a4')
    expect(shortId(null)).toBe('—')
    expect(ticketCountLabel(1)).toBe('1 ticket')
    expect(ticketCountLabel(3)).toBe('3 tickets')
    expect(priorityLabel.critical).toBe('Critical')
    expect(pipelineLabel.in_progress).toBe('Running')
  })

  it('formats dates defensively', () => {
    expect(formatDateTime(null)).toBe('—')
    expect(formatDateTime('not-a-date')).toBe('—')
    expect(formatDateTime('2026-10-30T12:00:00.000Z')).toContain('2026')
  })
})
