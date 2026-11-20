import { getStoredToken } from '../lib/storage'
import type { AdminUser, DirectoryUser, ProjectMember } from '../types/admin'
import type { AuditEvent, AuditFilters, AuditPage, AuditVerification } from '../types/audit'
import type { AiAskResponse, AiInsightResponse, AiSearchResponse } from '../types/ai'
import type { SearchResponse } from '../types/search'
import type {
  AuthUser,
  CreateProjectResponse,
  CreateTicketPayload,
  CreatedTicketResponse,
  LoginResponse,
  ProjectSnapshot,
  ProjectSummary,
  StreamTicketResponse,
  UpdateTicketPayload,
} from '../types/project'
import type {
  ProjectReport,
  ProjectSettings,
  PullRequestFile,
  ReleaseReadiness,
  RepositoryBranch,
  RepositoryCommit,
  RepositoryFile,
  RepositoryTreeResponse,
  TraceabilityResponse,
  Attachment,
  Comment,
  LinkedArtifact,
  WorkItemDetail,
  Workspace,
} from '../types/workspace'

const API_BASE = (import.meta.env.VITE_API_BASE_URL ?? '').replace(/\/$/, '')

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(message: string, status: number, code = 'REQUEST_FAILED') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  auth?: boolean
  signal?: AbortSignal
  timeoutMs?: number
  headers?: Record<string, string>
}

export interface RevisionedResult {
  id: string
  revision: number
  deleted?: boolean
}

export function isRevisionConflict(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'REVISION_CONFLICT'
}

export function revisionConflictMessage(action: string): string {
  return `This ${action} changed in another session. The workspace has been refreshed; review the latest values and try again.`
}

function ifMatch(revision: number): Record<string, string> {
  return { 'If-Match': `"${revision}"` }
}

interface RawResult<T> {
  data: T
  response: Response
}

function notifyUnauthorized(): void {
  if (typeof window !== 'undefined' && getStoredToken()) {
    window.dispatchEvent(new CustomEvent('spm:unauthorized'))
  }
}

async function requestRaw<T>(path: string, options: RequestOptions = {}): Promise<RawResult<T>> {
  const { method = 'GET', body, auth = true, signal, timeoutMs, headers: extraHeaders } = options
  const headers = new Headers({ Accept: 'application/json' })

  if (extraHeaders) {
    for (const [name, value] of Object.entries(extraHeaders)) headers.set(name, value)
  }
  if (body !== undefined && !(typeof FormData !== 'undefined' && body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }
  if (auth) {
    const token = getStoredToken()
    if (token) headers.set('Authorization', `Bearer ${token}`)
  }

  const controller = timeoutMs ? new AbortController() : null
  const timer = controller ? window.setTimeout(() => controller.abort(), timeoutMs) : null
  const abortFromCaller = () => controller?.abort()

  if (controller && signal) {
    if (signal.aborted) controller.abort()
    else signal.addEventListener('abort', abortFromCaller, { once: true })
  }

  try {
    const response = await fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body:
        body === undefined
          ? undefined
          : typeof FormData !== 'undefined' && body instanceof FormData
            ? body
            : JSON.stringify(body),
      signal: controller?.signal ?? signal,
      credentials: 'same-origin',
    })

    const contentType = response.headers.get('content-type') ?? ''
    const data = contentType.includes('application/json')
      ? await response.json().catch(() => null)
      : null

    if (!response.ok) {
      if (response.status === 401) notifyUnauthorized()
      const message =
        data?.error?.message ??
        (response.status === 401
          ? 'Your session has expired. Please sign in again.'
          : `Request failed (HTTP ${response.status})`)
      throw new ApiError(message, response.status, data?.error?.code ?? 'REQUEST_FAILED')
    }

    return { data: data as T, response }
  } catch (error) {
    if (error instanceof ApiError) throw error
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ApiError('The request timed out. Please try again.', 0, 'TIMEOUT')
    }
    throw new ApiError('Unable to reach the server. Check your connection and retry.', 0, 'NETWORK_ERROR')
  } finally {
    if (timer !== null) window.clearTimeout(timer)
    if (controller && signal) signal.removeEventListener('abort', abortFromCaller)
  }
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { data } = await requestRaw<T>(path, options)
  return data
}

export const api = {
  async login(username: string, password: string): Promise<LoginResponse> {
    return request<LoginResponse>('/api/v1/auth/login', {
      method: 'POST',
      body: { username, password },
      auth: false,
      timeoutMs: 15_000,
    })
  },

  async me(): Promise<AuthUser> {
    return request<AuthUser>('/api/v1/auth/me', { timeoutMs: 15_000 })
  },

  async updateProfile(payload: {
    display_name?: string
    email?: string
    avatar_url?: string
  }): Promise<AuthUser> {
    return request<AuthUser>('/api/v1/auth/me', {
      method: 'PATCH',
      body: payload,
      timeoutMs: 15_000,
    })
  },

  async changePassword(currentPassword: string, newPassword: string): Promise<void> {
    await requestRaw<void>('/api/v1/auth/password', {
      method: 'POST',
      body: { current_password: currentPassword, new_password: newPassword },
      timeoutMs: 15_000,
    })
  },

  async listDirectoryUsers(query = ''): Promise<DirectoryUser[]> {
    const params = new URLSearchParams()
    if (query) params.set('q', query)
    const suffix = params.toString() ? `?${params.toString()}` : ''
    return request<DirectoryUser[]>(`/api/v1/users${suffix}`)
  },

  async search(query: string, limit = 40): Promise<SearchResponse> {
    const params = new URLSearchParams({ q: query, limit: String(limit) })
    return request<SearchResponse>(`/api/v1/search?${params.toString()}`, { timeoutMs: 20_000 })
  },

  async adminListUsers(): Promise<AdminUser[]> {
    return request<AdminUser[]>('/api/v1/admin/users')
  },

  async adminCreateUser(payload: {
    username: string
    email: string
    display_name: string
    role: string
    password: string
  }): Promise<{ id: string }> {
    return request<{ id: string }>('/api/v1/admin/users', {
      method: 'POST',
      body: payload,
      timeoutMs: 20_000,
    })
  },

  async adminUpdateUser(
    userId: string,
    payload: {
      email?: string
      display_name?: string
      avatar_url?: string
      role?: string
      is_active?: boolean
      password?: string
    },
  ): Promise<{ id: string }> {
    return request<{ id: string }>(`/api/v1/admin/users/${encodeURIComponent(userId)}`, {
      method: 'PATCH',
      body: payload,
      timeoutMs: 20_000,
    })
  },

  async adminListAuditEvents(filters: AuditFilters = {}): Promise<AuditPage> {
    const params = new URLSearchParams()
    for (const [key, value] of Object.entries(filters)) {
      if (value !== undefined && value !== null && String(value).trim() !== '') {
        params.set(key, String(value))
      }
    }
    const suffix = params.toString() ? `?${params.toString()}` : ''
    return request<AuditPage>(`/api/v1/admin/audit/events${suffix}`, { timeoutMs: 20_000 })
  },

  async adminGetAuditEvent(id: string): Promise<AuditEvent> {
    return request<AuditEvent>(`/api/v1/admin/audit/events/${encodeURIComponent(id)}`, {
      timeoutMs: 20_000,
    })
  },

  async adminVerifyAuditChain(): Promise<AuditVerification> {
    return request<AuditVerification>('/api/v1/admin/audit/verify', { timeoutMs: 60_000 })
  },

  async adminExportAudit(
    filters: AuditFilters,
    format: 'csv' | 'json',
  ): Promise<{ blob: Blob; filename: string }> {
    const params = new URLSearchParams({ format })
    for (const [key, value] of Object.entries(filters)) {
      if (value !== undefined && value !== null && String(value).trim() !== '') {
        params.set(key, String(value))
      }
    }
    const token = getStoredToken()
    const response = await fetch(`${API_BASE}/api/v1/admin/audit/export?${params.toString()}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!response.ok) {
      throw new ApiError(`Export failed (HTTP ${response.status})`, response.status, 'EXPORT_FAILED')
    }
    const disposition = response.headers.get('content-disposition') ?? ''
    const match = disposition.match(/filename="?([^"]+)"?/)
    return {
      blob: await response.blob(),
      filename: match?.[1] ?? `audit.${format}`,
    }
  },

  /** Short-lived, single-use SSE credential; never put the JWT in the URL. */
  async streamTicket(): Promise<StreamTicketResponse> {    return request<StreamTicketResponse>('/api/v1/auth/stream-ticket', {
      method: 'POST',
      timeoutMs: 10_000,
    })
  },

  async listProjects(): Promise<ProjectSummary[]> {
    return request<ProjectSummary[]>('/api/v1/projects')
  },

  async createProject(name: string, description: string): Promise<CreateProjectResponse> {
    return request<CreateProjectResponse>('/api/v1/projects', {
      method: 'POST',
      body: { name, description },
      timeoutMs: 30_000,
    })
  },

  async getProject(id: string): Promise<ProjectSnapshot> {
    const { data, response } = await requestRaw<ProjectSnapshot['state']>(
      `/api/v1/projects/${encodeURIComponent(id)}`,
    )
    const sequence = Number(response.headers.get('X-Snapshot-Sequence') ?? 0)
    return { state: data, sequence: Number.isFinite(sequence) ? sequence : 0 }
  },

  async createTicket(projectId: string, payload: CreateTicketPayload): Promise<CreatedTicketResponse> {
    return request<CreatedTicketResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/tickets`,
      { method: 'POST', body: payload, timeoutMs: 20_000 },
    )
  },

  async updateTicket(
    projectId: string,
    ticketId: string,
    payload: UpdateTicketPayload,
  ): Promise<CreatedTicketResponse> {
    return request<CreatedTicketResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/tickets/${encodeURIComponent(ticketId)}`,
      { method: 'PUT', body: payload, timeoutMs: 20_000 },
    )
  },

  async changeTicketState(
    projectId: string,
    ticketId: string,
    state: string,
  ): Promise<CreatedTicketResponse> {
    return request<CreatedTicketResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/tickets/${encodeURIComponent(ticketId)}/state`,
      { method: 'PATCH', body: { state }, timeoutMs: 20_000 },
    )
  },

  async deleteTicket(projectId: string, ticketId: string): Promise<CreatedTicketResponse> {
    return request<CreatedTicketResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/tickets/${encodeURIComponent(ticketId)}`,
      { method: 'DELETE', timeoutMs: 20_000 },
    )
  },

  // --- SDLC / STLC workspace -------------------------------------------------

  async getWorkspace(projectId: string): Promise<Workspace> {
    return request<Workspace>(`/api/v1/projects/${encodeURIComponent(projectId)}/workspace`)
  },

  async aiSearch(projectId: string, query: string): Promise<AiSearchResponse> {
    const params = new URLSearchParams({ q: query })
    return request<AiSearchResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/ai/search?${params.toString()}`,
      { timeoutMs: 30_000 },
    )
  },

  async aiAsk(projectId: string, question: string): Promise<AiAskResponse> {
    return request<AiAskResponse>(`/api/v1/projects/${encodeURIComponent(projectId)}/ai/ask`, {
      method: 'POST',
      body: { question },
      timeoutMs: 60_000,
    })
  },

  async aiInsights(projectId: string): Promise<AiInsightResponse> {
    return request<AiInsightResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/ai/insights`,
      { timeoutMs: 30_000 },
    )
  },

  async getTraceability(projectId: string): Promise<TraceabilityResponse> {
    return request<TraceabilityResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/traceability`,
    )
  },

  async getReports(projectId: string): Promise<ProjectReport> {
    return request<ProjectReport>(`/api/v1/projects/${encodeURIComponent(projectId)}/reports`)
  },

  async getReleaseReadiness(projectId: string): Promise<{ releases: ReleaseReadiness[] }> {
    return request<{ releases: ReleaseReadiness[] }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/release-readiness`,
    )
  },

  async getSettings(projectId: string): Promise<ProjectSettings> {
    return request<ProjectSettings>(`/api/v1/projects/${encodeURIComponent(projectId)}/settings`)
  },

  async updateSettings(projectId: string, payload: ProjectSettings): Promise<ProjectSettings> {
    return request<ProjectSettings>(`/api/v1/projects/${encodeURIComponent(projectId)}/settings`, {
      method: 'PUT',
      body: payload,
    })
  },

  async updateProject(
    projectId: string,
    payload: {
      name?: string
      description?: string
      key_prefix?: string
      default_branch?: string
      status?: string
    },
  ): Promise<{ id: string }> {
    return request<{ id: string }>(`/api/v1/projects/${encodeURIComponent(projectId)}`, {
      method: 'PATCH',
      body: payload,
    })
  },

  async listProjectMembers(projectId: string): Promise<ProjectMember[]> {
    return request<ProjectMember[]>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/members`,
    )
  },

  async addProjectMember(
    projectId: string,
    userId: string,
    role: string,
  ): Promise<{ user_id: string; role: string }> {
    return request<{ user_id: string; role: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/members`,
      { method: 'POST', body: { user_id: userId, role } },
    )
  },

  async updateProjectMember(
    projectId: string,
    userId: string,
    role: string,
  ): Promise<{ user_id: string; role: string }> {
    return request<{ user_id: string; role: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(userId)}`,
      { method: 'PATCH', body: { role } },
    )
  },

  async removeProjectMember(projectId: string, userId: string): Promise<void> {
    await requestRaw<void>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(userId)}`,
      { method: 'DELETE' },
    )
  },

  async importTestRun(
    projectId: string,
    payload: string | unknown,
    format: 'junit' | 'json' = 'junit',
    environmentId = '',
    featureId = '',
  ): Promise<{ id: string; total: number; matched: number; created: number; unmatched: number }> {
    const token = getStoredToken()
    const query = new URLSearchParams({ format })
    if (environmentId) query.set('environment_id', environmentId)
    if (featureId) query.set('feature_id', featureId)
    const response = await fetch(
      `${API_BASE}/api/v1/projects/${encodeURIComponent(projectId)}/test-runs/import?${query.toString()}`,
      {
        method: 'POST',
        headers: {
          Accept: 'application/json',
          'Content-Type': format === 'junit' ? 'application/xml' : 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: typeof payload === 'string' ? payload : JSON.stringify(payload),
      },
    )
    const data = await response.json().catch(() => null)
    if (!response.ok) {
      throw new ApiError(
        data?.error?.message ?? `Import failed (HTTP ${response.status})`,
        response.status,
        data?.error?.code ?? 'IMPORT_FAILED',
      )
    }
    return data
  },

  async downloadReport(
    projectId: string,
    type: string,
    format: 'csv' | 'json',
  ): Promise<{ blob: Blob; filename: string }> {
    const token = getStoredToken()
    const response = await fetch(
      `${API_BASE}/api/v1/projects/${encodeURIComponent(projectId)}/reports/export?type=${encodeURIComponent(type)}&format=${format}`,
      { headers: token ? { Authorization: `Bearer ${token}` } : {} },
    )
    if (!response.ok) {
      throw new ApiError(`Export failed (HTTP ${response.status})`, response.status, 'EXPORT_FAILED')
    }
    const disposition = response.headers.get('content-disposition') ?? ''
    const match = disposition.match(/filename="?([^"]+)"?/)
    return { blob: await response.blob(), filename: match?.[1] ?? `report.${format}` }
  },

  async createPRD(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/prds`, {
      method: 'POST',
      body: payload,
    })
  },

  async updatePRD(
    projectId: string,
    prdId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/prds/${encodeURIComponent(prdId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async deletePRD(projectId: string, prdId: string, revision: number): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/prds/${encodeURIComponent(prdId)}`,
      { method: 'DELETE', headers: ifMatch(revision) },
    )
  },

  async createEpic(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/epics`, {
      method: 'POST',
      body: payload,
    })
  },

  async updateEpic(
    projectId: string,
    epicId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async createFeature(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/features`, {
      method: 'POST',
      body: payload,
    })
  },

  async deleteEpic(projectId: string, epicId: string, revision: number): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/epics/${encodeURIComponent(epicId)}`,
      { method: 'DELETE', headers: ifMatch(revision) },
    )
  },

  async deleteFeature(
    projectId: string,
    featureId: string,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/features/${encodeURIComponent(featureId)}`,
      { method: 'DELETE', headers: ifMatch(revision) },
    )
  },

  async updateFeature(
    projectId: string,
    featureId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/features/${encodeURIComponent(featureId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async createWorkItem(
    projectId: string,
    payload: unknown,
  ): Promise<RevisionedResult & { key?: string }> {
    return request<RevisionedResult & { key?: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items`,
      { method: 'POST', body: payload },
    )
  },

  async updateWorkItem(
    projectId: string,
    itemId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async changeWorkItemStatus(
    projectId: string,
    itemId: string,
    status: string,
    revision: number,
  ): Promise<RevisionedResult & { status: string }> {
    return request<RevisionedResult & { status: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}/status`,
      { method: 'PATCH', body: { status }, headers: ifMatch(revision) },
    )
  },

  async deleteWorkItem(
    projectId: string,
    itemId: string,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}`,
      { method: 'DELETE', headers: ifMatch(revision) },
    )
  },

  async getWorkItemDetail(projectId: string, itemId: string): Promise<WorkItemDetail> {
    return request<WorkItemDetail>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}`,
    )
  },

  async createComment(projectId: string, itemId: string, body: string): Promise<Comment> {
    return request<Comment>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}/comments`,
      { method: 'POST', body: { body } },
    )
  },

  async updateComment(projectId: string, commentId: string, body: string): Promise<Comment> {
    return request<Comment>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/comments/${encodeURIComponent(commentId)}`,
      { method: 'PATCH', body: { body } },
    )
  },

  async deleteComment(projectId: string, commentId: string): Promise<void> {
    await request<null>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/comments/${encodeURIComponent(commentId)}`,
      { method: 'DELETE' },
    )
  },

  async setWorkItemWatching(
    projectId: string,
    itemId: string,
    watching: boolean,
  ): Promise<{ watching: boolean }> {
    return request<{ watching: boolean }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}/watch`,
      { method: watching ? 'POST' : 'DELETE', body: watching ? {} : undefined },
    )
  },

  async uploadAttachment(projectId: string, itemId: string, file: File): Promise<Attachment> {
    const form = new FormData()
    form.append('file', file)
    return request<Attachment>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}/attachments`,
      { method: 'POST', body: form },
    )
  },

  async downloadAttachment(projectId: string, attachmentId: string): Promise<Blob> {
    const { response } = await requestRaw<unknown>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/attachments/${encodeURIComponent(attachmentId)}/download`,
    )
    return response.blob()
  },

  async deleteAttachment(projectId: string, attachmentId: string): Promise<void> {
    await request<null>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/attachments/${encodeURIComponent(attachmentId)}`,
      { method: 'DELETE' },
    )
  },

  async createWorkItemLink(
    projectId: string,
    itemId: string,
    payload: { target_type: string; target_id: string; link_type?: string },
  ): Promise<LinkedArtifact> {
    return request<LinkedArtifact>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}/links`,
      { method: 'POST', body: payload },
    )
  },

  async deleteWorkItemLink(projectId: string, itemId: string, linkId: string): Promise<void> {
    await request<null>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/work-items/${encodeURIComponent(itemId)}/links/${encodeURIComponent(linkId)}`,
      { method: 'DELETE' },
    )
  },

  async createSprint(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/sprints`, {
      method: 'POST',
      body: payload,
    })
  },

  async updateSprint(
    projectId: string,
    sprintId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/sprints/${encodeURIComponent(sprintId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async createRelease(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/releases`, {
      method: 'POST',
      body: payload,
    })
  },

  async updateRelease(
    projectId: string,
    releaseId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/releases/${encodeURIComponent(releaseId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async createTestCase(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/test-cases`, {
      method: 'POST',
      body: payload,
    })
  },

  async updateTestCase(
    projectId: string,
    caseId: string,
    payload: unknown,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/test-cases/${encodeURIComponent(caseId)}`,
      { method: 'PUT', body: payload, headers: ifMatch(revision) },
    )
  },

  async deleteTestCase(
    projectId: string,
    caseId: string,
    revision: number,
  ): Promise<RevisionedResult> {
    return request<RevisionedResult>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/test-cases/${encodeURIComponent(caseId)}`,
      { method: 'DELETE', headers: ifMatch(revision) },
    )
  },

  async createTestRun(projectId: string, payload: unknown): Promise<RevisionedResult> {
    return request<RevisionedResult>(`/api/v1/projects/${encodeURIComponent(projectId)}/test-runs`, {
      method: 'POST',
      body: payload,
    })
  },

  async patchTestResult(
    projectId: string,
    runId: string,
    resultId: string,
    payload: unknown,
  ): Promise<{ id: string; status: string }> {
    return request<{ id: string; status: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/test-runs/${encodeURIComponent(runId)}/results/${encodeURIComponent(resultId)}`,
      { method: 'PATCH', body: payload },
    )
  },

  async patchTestStepResult(
    projectId: string,
    runId: string,
    resultId: string,
    stepResultId: string,
    payload: unknown,
  ): Promise<{ id: string; status: string }> {
    return request<{ id: string; status: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/test-runs/${encodeURIComponent(runId)}/results/${encodeURIComponent(resultId)}/steps/${encodeURIComponent(stepResultId)}`,
      { method: 'PATCH', body: payload },
    )
  },

  async createBugFromResult(
    projectId: string,
    runId: string,
    resultId: string,
    payload: unknown,
  ): Promise<{ id: string; key?: string }> {
    return request<{ id: string; key?: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/test-runs/${encodeURIComponent(runId)}/results/${encodeURIComponent(resultId)}/bug`,
      { method: 'POST', body: payload },
    )
  },

  async createRepository(projectId: string, payload: unknown): Promise<{ id: string }> {
    return request<{ id: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/repositories`,
      { method: 'POST', body: payload },
    )
  },

  async getRepositoryTree(
    projectId: string,
    repoId: string,
    ref = '',
    path = '',
  ): Promise<RepositoryTreeResponse> {
    const params = new URLSearchParams()
    if (ref) params.set('ref', ref)
    if (path) params.set('path', path)
    const query = params.toString()
    return request<RepositoryTreeResponse>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(repoId)}/tree${query ? `?${query}` : ''}`,
    )
  },

  async getRepositoryFile(
    projectId: string,
    repoId: string,
    ref: string,
    path: string,
  ): Promise<RepositoryFile> {
    const params = new URLSearchParams({ path })
    if (ref) params.set('ref', ref)
    return request<RepositoryFile>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(repoId)}/blob?${params.toString()}`,
    )
  },

  async getRepositoryCommits(
    projectId: string,
    repoId: string,
    ref = '',
    page = 1,
    limit = 30,
  ): Promise<{ ref: string; commits: RepositoryCommit[] }> {
    const params = new URLSearchParams({ page: String(page), limit: String(limit) })
    if (ref) params.set('ref', ref)
    return request<{ ref: string; commits: RepositoryCommit[] }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(repoId)}/commits?${params.toString()}`,
    )
  },

  async getRepositoryBranches(
    projectId: string,
    repoId: string,
  ): Promise<{ branches: RepositoryBranch[] }> {
    return request<{ branches: RepositoryBranch[] }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(repoId)}/branches`,
    )
  },

  async getPullRequestFiles(
    projectId: string,
    prId: string,
  ): Promise<{ number: number; files: PullRequestFile[] }> {
    return request<{ number: number; files: PullRequestFile[] }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/pull-requests/${encodeURIComponent(prId)}/files`,
    )
  },

  async provisionRepository(
    projectId: string,
    repoId: string,
  ): Promise<{ id: string; name: string; status: string }> {
    return request<{ id: string; name: string; status: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(repoId)}/provision`,
      { method: 'POST', timeoutMs: 30_000 },
    )
  },

  async createBranch(projectId: string, payload: unknown): Promise<{ id: string }> {
    return request<{ id: string }>(`/api/v1/projects/${encodeURIComponent(projectId)}/branches`, {
      method: 'POST',
      body: payload,
    })
  },

  async createPullRequest(projectId: string, payload: unknown): Promise<{ id: string; number: number; html_url: string }> {
    return request<{ id: string; number: number; html_url: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/pull-requests`,
      { method: 'POST', body: payload, timeoutMs: 30_000 },
    )
  },

  async updatePullRequest(
    projectId: string,
    prId: string,
    payload: unknown,
  ): Promise<{ id: string; status: string; build_id?: string | null }> {
    return request<{ id: string; status: string; build_id?: string | null }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/pull-requests/${encodeURIComponent(prId)}`,
      { method: 'PUT', body: payload },
    )
  },

  async mergePullRequest(projectId: string, prId: string, mergeSHA = ''): Promise<{ id: string; status: string; number: number; build_id?: string | null }> {
    return request<{ id: string; status: string; number: number; build_id?: string | null }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/pull-requests/${encodeURIComponent(prId)}/merge`,
      { method: 'POST', body: { merge_commit_sha: mergeSHA }, timeoutMs: 30_000 },
    )
  },

  async createEnvironment(projectId: string, payload: unknown): Promise<{ id: string }> {
    return request<{ id: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/environments`,
      { method: 'POST', body: payload },
    )
  },

  async createLink(projectId: string, payload: unknown): Promise<{ id: string }> {
    return request<{ id: string }>(`/api/v1/projects/${encodeURIComponent(projectId)}/links`, {
      method: 'POST',
      body: payload,
    })
  },

  async deleteLink(projectId: string, linkId: string): Promise<{ id: string }> {
    return request<{ id: string }>(
      `/api/v1/projects/${encodeURIComponent(projectId)}/links/${encodeURIComponent(linkId)}`,
      { method: 'DELETE' },
    )
  },
}
