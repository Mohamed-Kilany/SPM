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
