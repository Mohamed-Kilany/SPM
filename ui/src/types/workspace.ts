export type WorkItemType = 'story' | 'task' | 'subtask' | 'bug' | 'spike' | 'chore'
export type WorkItemStatus =
  | 'backlog'
  | 'ready'
  | 'in_progress'
  | 'in_review'
  | 'in_qa'
  | 'done'
  | 'blocked'
  | 'cancelled'
  | 'new'
  | 'triaged'
  | 'fixed'
  | 'verified'
  | 'closed'
  | 'reopened'

export type Priority = 'low' | 'medium' | 'high' | 'critical'
export type TestStatus = 'not_run' | 'passed' | 'failed' | 'blocked' | 'skipped'

export interface MergePolicy {
  require_linked_work_item: boolean
  require_approved_review: boolean
  require_passing_checks: boolean
}

export interface ProjectSettings {
  merge_policy: MergePolicy
  auto_rollback: boolean
  auto_deploy_on_build: boolean
  auto_deploy_environment: string
  require_health_check: boolean
}

export interface WorkspaceProject {
  id: string
  name: string
  description: string
  key_prefix: string
  status: string
  owner_id: string
  repo_name: string
  default_branch: string
  app_name: string
  settings: ProjectSettings
  created_at: string
  updated_at: string
}

export interface Member {
  user_id: string
  username: string
  email: string
  role: string
}

export interface PRD {
  id: string
  project_id: string
  title: string
  version: string
  status: string
  summary: string
  content: string
  owner_id: string | null
  approved_by: string | null
  approved_at: string | null
  created_at: string
  updated_at: string
  revision: number
}

export interface Epic {
  id: string
  project_id: string
  prd_id: string | null
  title: string
  description: string
  status: string
  priority: Priority | string
  owner_id: string | null
  target_release_id: string | null
  start_date: string | null
  due_date: string | null
  created_at: string
  updated_at: string
  revision: number
}

export interface Feature {
  id: string
  project_id: string
  epic_id: string
  title: string
  description: string
  acceptance_criteria: string
  status: string
  priority: Priority | string
  owner_id: string | null
  estimate_points: number | null
  target_release_id: string | null
  created_at: string
  updated_at: string
  revision: number
}

export interface WorkItem {
  id: string
  project_id: string
  item_number: number
  key: string
  feature_id: string | null
  parent_id: string | null
  type: WorkItemType | string
  title: string
  description: string
  acceptance_criteria: string
  status: WorkItemStatus | string
  priority: Priority | string
  severity: string | null
  assignee_id: string | null
  reporter_id: string | null
  estimate_points: number | null
  sprint_id: string | null
  release_id: string | null
  environment_id: string | null
  due_date: string | null
  reproduction_steps: string
  expected_result: string
  actual_result: string
  created_at: string
  updated_at: string
  closed_at: string | null
  revision: number
}

export interface Sprint {
  id: string
  project_id: string
  name: string
  goal: string
  status: string
  start_date: string | null
  end_date: string | null
  capacity: number | null
  created_at: string
  updated_at: string
  revision: number
}

export interface Release {
  id: string
  project_id: string
  name: string
  version: string
  status: string
  planned_at: string | null
  released_at: string | null
  notes: string
  created_at: string
  updated_at: string
  revision: number
}

export interface Environment {
  id: string
  project_id: string
  name: string
  type: string
  url: string
  health_check_url: string
  deploy_strategy: string
  requires_approval: boolean
  position: number
  created_at: string
  updated_at: string
}

export interface TestStep {
  id: string
  test_case_id: string
  position: number
  action: string
  expected_result: string
  test_data: string
  created_at: string
  updated_at: string
}

export interface TestCase {
  id: string
  project_id: string
  feature_id: string | null
  title: string
  description: string
  preconditions: string
  type: string
  priority: Priority | string
  status: string
  automation_ref: string
  owner_id: string | null
  tags: string[]
  created_at: string
  updated_at: string
  steps: TestStep[]
  revision: number
}

export interface TestStepResult {
  id: string
  test_result_id: string
  test_step_id: string
  status: TestStatus | string
  actual_result: string
  evidence_url: string
  bug_work_item_id: string | null
  created_at: string
  updated_at: string
}

export interface TestResult {
  id: string
  test_run_id: string
  test_case_id: string
  status: TestStatus | string
  executed_by: string | null
  duration_ms: number | null
  actual_result: string
  bug_work_item_id: string | null
  started_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
  step_results: TestStepResult[]
}

export interface TestRun {
  id: string
  project_id: string
  release_id: string | null
  sprint_id: string | null
  environment_id: string | null
  build_id: string | null
  name: string
  status: string
  executed_by: string | null
  started_at: string | null
  completed_at: string | null
  import_source: string
  import_ref: string
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
  results: TestResult[]
  revision: number
}

export interface GitRepository {
  id: string
  project_id: string
  provider: string
  name: string
  url: string
  default_branch: string
  external_id: string
  webhook_token: string
  webhook_url: string
  created_at: string
  updated_at: string
}

export interface GitBranch {
  id: string
  repository_id: string
  work_item_id: string | null
  name: string
  head_commit_sha: string
  status: string
  created_by: string | null
  created_at: string
  updated_at: string
  merged_at: string | null
}

export interface GitCommit {
  id: string
  repository_id: string
  sha: string
  branch_name: string
  message: string
  author_name: string
  author_email: string
  committed_at: string
  created_at: string
}

export interface RepositoryTreeEntry {
  type: 'file' | 'dir' | 'symlink' | 'submodule' | string
  name: string
  path: string
  sha: string
  size: number
}

export interface RepositoryTreeResponse {
  ref: string
  path: string
  entries: RepositoryTreeEntry[]
}

export interface RepositoryFile {
  type: string
  name: string
  path: string
  sha: string
  size: number
  content: string
  encoding: string
  binary: boolean
  truncated: boolean
}

export interface RepositoryCommit {
  sha: string
  message: string
  author_name: string
  author_email: string
  committed_at: string
}

export interface RepositoryBranch {
  name: string
  commit_sha: string
  commit_message: string
  protected: boolean
  updated_at: string
}

export interface PullRequestFile {
  filename: string
  status: string
  additions: number
  deletions: number
  changes: number
  patch: string
}

export interface PullRequest {
  id: string
  repository_id: string
  work_item_id: string | null
  number: number
  title: string
  description: string
  source_branch_id: string | null
  target_branch: string
  author: string
  status: string
  review_status: string
  merge_commit_sha: string
  created_at: string
  updated_at: string
  merged_at: string | null
}

export interface Build {
  id: string
  project_id: string
  repository_id: string | null
  commit_sha: string
  branch: string
  provider: string
  external_id: string
  number: string
  status: string
  logs_url: string
  metadata: Record<string, unknown>
  triggered_by: string | null
  started_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
}

export interface Deployment {
  id: string
  project_id: string
  environment_id: string
  build_id: string | null
  release_id: string | null
  status: string
  health_status: string
  version: string
  argo_app: string
  argo_revision: string
  metadata: Record<string, unknown>
  deployed_by: string | null
  previous_deployment_id: string | null
  rollback_of_id: string | null
  started_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
}

export interface HealthCheck {
  id: string
  deployment_id: string
  url: string
  status: string
  http_status: number | null
  latency_ms: number | null
  details: Record<string, unknown>
  checked_at: string
  created_at: string
}

export interface ArtifactLink {
  id: string
  project_id: string
  source_type: string
  source_id: string
  target_type: string
  target_id: string
  link_type: string
  created_by: string | null
  created_at: string
}

export interface ActivityEvent {
  id: string
  project_id: string
  actor_id: string | null
  entity_type: string
  entity_id: string | null
  action: string
  summary: string
  metadata: Record<string, unknown>
  created_at: string
}

export interface TestTrendPoint {
  run_id: string
  name: string
  date: string
  total: number
  passed: number
  failed: number
  pass_rate: number
}

export interface WorkspaceMetrics {
  prd_count: number
  epic_count: number
  feature_count: number
  work_item_count: number
  open_bug_count: number
  test_case_count: number
  test_run_count: number
  pass_rate: number
  coverage_percent: number
  deployment_count: number
  healthy_deployments: number
  degraded_deployments: number
  unlinked_features: number
  untested_features: number
  flaky_test_count: number
  cycle_time_hours: number
  delivery_confidence: number
  test_trend: TestTrendPoint[]
}

export interface ReportGate {
  key: string
  label: string
  status: 'pass' | 'warn' | 'fail'
  detail: string
}

export interface ReleaseReadiness {
  release_id: string
  name: string
  version: string
  status: string
  ready: boolean
  score: number
  gates: ReportGate[]
}

export interface CoverageRow {
  feature_id: string
  feature: string
  epic: string
  prd: string
  test_cases: number
  passed: number
  failed: number
  pass_rate: number
  open_bugs: number
  delivery_gaps: number
}

export interface ProjectReport {
  generated_at: string
  metrics: WorkspaceMetrics
  coverage: CoverageRow[]
  releases: ReleaseReadiness[]
}

export interface Workspace {
  project: WorkspaceProject
  members: Member[]
  prds: PRD[]
  epics: Epic[]
  features: Feature[]
  work_items: WorkItem[]
  sprints: Sprint[]
  releases: Release[]
  environments: Environment[]
  test_cases: TestCase[]
  test_runs: TestRun[]
  test_results: TestResult[]
  test_step_results: TestStepResult[]
  repositories: GitRepository[]
  branches: GitBranch[]
  commits: GitCommit[]
  pull_requests: PullRequest[]
  builds: Build[]
  deployments: Deployment[]
  health_checks: HealthCheck[]
  links: ArtifactLink[]
  activity: ActivityEvent[]
  metrics: WorkspaceMetrics
  revision: number
}

export interface TraceabilityFeature {
  feature_id: string
  feature_title: string
  epic_id: string
  prd_id: string | null
  stories: number
  tasks: number
  test_case_ids: string[]
  latest_test_run_id: string | null
  test_cases: number
  last_run_status: string
  open_bugs: number
  branch_ids: string[]
  branch_count: number
  pull_request_ids: string[]
  pr_count: number
  build_ids: string[]
  build_count: number
  deployment_ids: string[]
  deployments: number
  health_status: string
  gaps: string[]
}

export interface TraceabilityResponse {
  features: TraceabilityFeature[]
  gaps: string[]
}
