import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { applyPatch as applyJsonPatch } from 'fast-json-patch'
import type { Operation } from 'fast-json-patch'
import type {
  BoardColumn,
  DeploymentStatus,
  Notification,
  PipelineStatus,
  ProjectMember,
  ProjectState,
  Ticket,
} from '../types/project'

/**
 * Mirrors the server-side project read model. A full snapshot is loaded once,
 * then RFC 6902 patches delivered over SSE are applied in order. The snapshot
 * sequence is the durable cursor used when (re)connecting the stream.
 */
export const useProjectStore = defineStore('project', () => {
  const projectId = ref<string | null>(null)
  const name = ref('')
  const description = ref('')
  const ownerId = ref('')
  const repoName = ref('')
  const defaultBranch = ref('main')
  const appName = ref('')
  const columns = ref<BoardColumn[]>([])
  const tickets = ref<Ticket[]>([])
  const members = ref<ProjectMember[]>([])
  const pipelines = ref<PipelineStatus[]>([])
  const deployments = ref<DeploymentStatus[]>([])
  const notifications = ref<Notification[]>([])
  const gitProvisioningFailed = ref(false)
  const updatedAt = ref<string | null>(null)
  const snapshotSequence = ref(0)

  // A degraded health alert is sticky until the user dismisses it. Subsequent
  // patches should not make a persistent incident disappear silently.
  const hasDegradedAlert = ref(false)

  const ticketsByState = computed<Record<string, Ticket[]>>(() => {
    const map: Record<string, Ticket[]> = {}
    for (const column of columns.value) {
      map[column.name] = tickets.value
        .filter((ticket) => ticket.state === column.name)
        .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
    }
    return map
  })

  const orderedColumns = computed<BoardColumn[]>(() =>
    [...columns.value].sort((a, b) => a.position - b.position),
  )

  const ticketById = computed(() => (id: string | null) =>
    id ? tickets.value.find((ticket) => ticket.id === id) ?? null : null,
  )

  const latestDeployment = computed<DeploymentStatus | null>(() => {
    if (deployments.value.length === 0) return null
    return [...deployments.value].sort(
      (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
    )[0]!
  })

  const latestPipeline = computed<PipelineStatus | null>(() => {
    if (pipelines.value.length === 0) return null
    return [...pipelines.value].sort(
      (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
    )[0]!
  })

  const orderedNotifications = computed<Notification[]>(() =>
    [...notifications.value].sort(
      (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    ),
  )

  function asSnapshot(): ProjectState {
    return {
      project_id: projectId.value ?? '',
      name: name.value,
      description: description.value,
      owner_id: ownerId.value,
      repo_name: repoName.value,
      default_branch: defaultBranch.value,
      app_name: appName.value,
      columns: columns.value,
      tickets: tickets.value,
      members: members.value,
      pipelines: pipelines.value,
      deployments: deployments.value,
      notifications: notifications.value,
      git_provisioning_failed: gitProvisioningFailed.value,
      updated_at: updatedAt.value ?? '',
    }
  }

  function hydrate(snapshot: ProjectState, sequence?: number): void {
    projectId.value = snapshot.project_id || null
    name.value = snapshot.name ?? ''
    description.value = snapshot.description ?? ''
    ownerId.value = snapshot.owner_id ?? ''
    repoName.value = snapshot.repo_name ?? ''
    defaultBranch.value = snapshot.default_branch || 'main'
    appName.value = snapshot.app_name ?? ''
    columns.value = [...(snapshot.columns ?? [])].sort((a, b) => a.position - b.position)
    tickets.value = [...(snapshot.tickets ?? [])]
    members.value = [...(snapshot.members ?? [])]
    pipelines.value = [...(snapshot.pipelines ?? [])]
    deployments.value = [...(snapshot.deployments ?? [])]
    notifications.value = [...(snapshot.notifications ?? [])]
    gitProvisioningFailed.value = snapshot.git_provisioning_failed ?? false
    updatedAt.value = snapshot.updated_at || null

    if (typeof sequence === 'number' && Number.isFinite(sequence)) {
      snapshotSequence.value = sequence
    }

    if (deployments.value.some((deployment) => deployment.health_status === 'Degraded')) {
      hasDegradedAlert.value = true
    }
  }

  function applyPatch(patch: Operation[]): void {
    if (patch.length === 0) return
    const result = applyJsonPatch(asSnapshot(), patch, false, false)
    hydrate(result.newDocument as unknown as ProjectState)
  }

  function setSnapshotSequence(sequence: number): void {
    if (Number.isFinite(sequence) && sequence >= 0) snapshotSequence.value = sequence
  }

  /** Optimistically reflect a move while the event is being projected. */
  function setTicketState(ticketId: string, state: string): void {
    const ticket = tickets.value.find((item) => item.id === ticketId)
    if (!ticket) return
    ticket.state = state
    ticket.updated_at = new Date().toISOString()
  }

  /** Optimistically reflect an edit while the event is being projected. */
  function patchTicket(ticketId: string, changes: Partial<Ticket>): void {
    const ticket = tickets.value.find((item) => item.id === ticketId)
    if (!ticket) return
    Object.assign(ticket, { updated_at: new Date().toISOString() }, changes)
  }

  /** Optimistically add a newly created ticket (the SSE patch reconciles it). */
  function addTicket(ticket: Ticket): void {
    if (!tickets.value.some((item) => item.id === ticket.id)) {
      tickets.value.push(ticket)
      updatedAt.value = ticket.updated_at
    }
  }

  /** Optimistically remove a deleted ticket (the SSE patch reconciles it). */
  function removeTicket(ticketId: string): void {
    tickets.value = tickets.value.filter((ticket) => ticket.id !== ticketId)
  }

  function dismissDegradedAlert(): void {
    hasDegradedAlert.value = false
  }

  function reset(): void {
    projectId.value = null
    name.value = ''
    description.value = ''
    ownerId.value = ''
    repoName.value = ''
    defaultBranch.value = 'main'
    appName.value = ''
    columns.value = []
    tickets.value = []
    members.value = []
    pipelines.value = []
    deployments.value = []
    notifications.value = []
    gitProvisioningFailed.value = false
    updatedAt.value = null
    snapshotSequence.value = 0
    hasDegradedAlert.value = false
  }

  return {
    projectId,
    name,
    description,
    ownerId,
    repoName,
    defaultBranch,
    appName,
    columns,
    tickets,
    members,
    pipelines,
    deployments,
    notifications,
    gitProvisioningFailed,
    updatedAt,
    snapshotSequence,
    hasDegradedAlert,
    ticketsByState,
    orderedColumns,
    ticketById,
    latestDeployment,
    latestPipeline,
    orderedNotifications,
    hydrate,
    applyPatch,
    setSnapshotSequence,
    setTicketState,
    patchTicket,
    addTicket,
    removeTicket,
    dismissDegradedAlert,
    $reset: reset,
  }
})
