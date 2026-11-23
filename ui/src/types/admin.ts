import type { GlobalRole, ProjectRole } from './project'

export interface AdminUser {
  id: string
  username: string
  email: string
  display_name: string
  avatar_url: string
  role: GlobalRole | string
  is_active: boolean
  project_ids: string[]
  created_at: string
  updated_at: string
}

export interface DirectoryUser {
  id: string
  username: string
  email: string
  display_name: string
  avatar_url: string
}

export interface ProjectMember {
  user_id: string
  username: string
  email: string
  role: ProjectRole | string
}

export const GLOBAL_ROLES: Array<{ value: GlobalRole; label: string; description: string }> = [
  { value: 'admin', label: 'Admin', description: 'Full platform administration and all projects' },
  { value: 'member', label: 'Member', description: 'Create projects and work inside memberships' },
  { value: 'viewer', label: 'Viewer', description: 'Read-only access to memberships' },
]

export const PROJECT_ROLES: Array<{ value: ProjectRole; label: string; description: string }> = [
  { value: 'owner', label: 'Owner', description: 'Full project control and membership' },
  { value: 'maintainer', label: 'Maintainer', description: 'Settings, planning, code and delivery' },
  { value: 'developer', label: 'Developer', description: 'Planning, work items, code and non-prod deploys' },
  { value: 'tester', label: 'Tester', description: 'Tests, runs and defects' },
  { value: 'viewer', label: 'Viewer', description: 'Read-only project access' },
]

export type OwnershipTransferStatus = 'pending' | 'accepted' | 'declined' | 'confirmed' | 'cancelled'

export interface OwnershipTransfer {
  id: string
  project_id: string
  project_name: string
  from_user_id: string
  from_user: string
  to_user_id: string
  to_user: string
  requested_by: string
  requested_by_name: string
  status: OwnershipTransferStatus | string
  reason: string
  created_at: string
  resolved_at?: string | null
  resolved_by?: string | null
}

export interface OwnershipTransferInbox {
  incoming: OwnershipTransfer[]
  outgoing: OwnershipTransfer[]
}

export interface OffboardingProject {
  id: string
  name: string
  status: string
  owner_count: number
  sole_owner: boolean
}

export interface OffboardingAssignment {
  id: string
  key: string
  title: string
  status: string
  project_id: string
  project_name: string
}

export interface OffboardingApproval {
  id: string
  deployment_id: string
  project_id: string
  project_name: string
}

export interface OffboardingImpact {
  user_id: string
  username: string
  display_name: string
  role: string
  is_active: boolean
  is_last_admin: boolean
  owned_projects: OffboardingProject[]
  open_assignments: OffboardingAssignment[]
  pending_approvals: OffboardingApproval[]
  owned_artifacts: Record<string, number>
  active_sessions: number
  requires_reassignment: boolean
  warnings: string[]
}

export interface OffboardingResult {
  impact: OffboardingImpact
  reassigned_projects: number
  reassigned_items: number
  reassigned_approvals: number
  reassigned_artifacts: number
  revoked_sessions: number
}
