// Domain types shared by the API client, stores and views. These mirror the
// Go read model in api/internal/models/models.go.

export type Priority = 'low' | 'medium' | 'high' | 'critical'
export type ProjectRole = 'owner' | 'maintainer' | 'developer' | 'tester' | 'viewer'
export type GlobalRole = 'admin' | 'member' | 'viewer'
export type PipelineState =
  | 'pending'
  | 'in_progress'
  | 'success'
  | 'failure'
  | 'cancelled'
  | 'timed_out'
  | ''

export interface BoardColumn {
  id: string
  name: string
  position: number
  is_default: boolean
}

export interface Ticket {
  id: string
  title: string
  description: string
  state: string
  assignee_id: string | null
  priority: Priority | string
  branch_name: string
  pipeline_status: PipelineState | string
  created_at: string
  updated_at: string
}

export interface ProjectMember {
  user_id: string
  role: ProjectRole | string
}

export interface PipelineStatus {
  branch: string
  commit_sha: string
  status: PipelineState | string
  updated_at: string
}

export interface DeploymentStatus {
  app_name: string
  sync_status: string
  health_status: string
  commit_sha: string
  updated_at: string
}

export interface Notification {
  id: string
  type: string
  message: string
  ticket_id?: string | null
  assignee_id?: string | null
  created_at: string
}

/** Full project read model returned by GET /projects/:id. */
export interface ProjectState {
  project_id: string
  name: string
  description: string
  owner_id: string
  repo_name: string
  default_branch: string
  app_name: string
  columns: BoardColumn[]
  tickets: Ticket[]
  members: ProjectMember[]
  pipelines: PipelineStatus[]
  deployments: DeploymentStatus[]
  notifications: Notification[]
  git_provisioning_failed: boolean
  updated_at: string
}

/** Row returned by GET /projects. */
export interface ProjectSummary {
  id: string
  name: string
  description: string
  owner_id: string
  repo_name: string
  default_branch: string
  app_name: string
  created_at: string
  updated_at: string
  tickets: Ticket[]
  ticket_count: number
  work_item_count?: number
  done_work_item_count?: number
  test_case_count?: number
  open_bug_count?: number
  last_activity_at?: string | null
}

/** Resolved project role and capability set returned by /auth/me. */
export interface ProjectAccess {
  project_id: string
  role: ProjectRole | 'admin' | string
  capabilities: string[]
}

export interface AuthUser {
  id: string
  username: string
  email: string
  display_name: string
  avatar_url: string
  role: GlobalRole | string
  project_ids: string[]
  projects?: ProjectAccess[]
}

export interface LoginResponse {
  token: string
  refresh_token: string
  session_id: string
  expires_in: number
  user: AuthUser
}

export interface AuthSession {
  id: string
  device: string
  ip: string
  user_agent: string
  created_at: string
  last_seen_at: string
  idle_expires_at: string
  expires_at: string
  current: boolean
}

export interface SessionListResponse {
  sessions: AuthSession[]
}

export interface StreamTicketResponse {
  ticket: string
  expires_in: number
}

export interface ProjectSnapshot {
  state: ProjectState
  /** Durable project event sequence the snapshot is valid through. */
  sequence: number
}

export interface CreateProjectResponse {
  id: string
  name: string
  repo_name: string
  warnings?: string[]
}

export interface CreatedTicketResponse {
  id: string
}

export interface UpdateTicketPayload {
  title: string
  description: string
  assignee_id: string | null
  priority: Priority
}

export interface CreateTicketPayload extends UpdateTicketPayload {
  state: string
}
