export interface AuditEvent {
  id: string
  occurred_at: string
  actor_id?: string
  actor_username?: string
  actor_role?: string
  action: string
  target_type?: string
  target_id?: string
  project_id?: string
  project_name?: string
  result: 'success' | 'failure' | 'denied'
  reason?: string
  ip?: string
  user_agent?: string
  request_id?: string
  before?: unknown
  after?: unknown
  prev_hash?: string
  hash?: string
}

export interface AuditPage {
  events: AuditEvent[]
  next_cursor?: string
}

export interface AuditFilters {
  action?: string
  actor_id?: string
  project_id?: string
  result?: string
  from?: string
  to?: string
  limit?: number
  cursor?: string
}

export interface AuditVerification {
  ok: boolean
  checked: number
  first_bad_id?: string
  first_bad_cause?: string
  checked_at: string
}
