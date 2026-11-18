import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { Ref } from 'vue'
import { api } from '../api/client'
import { useWorkspaceStore } from '../stores/workspaceStore'

const WORKSPACE_EVENT_TYPES = [
  'workspace.changed',
  'project.created',
  'project.updated',
  'ticket.created',
  'ticket.updated',
  'ticket.state_changed',
  'ticket.deleted',
  'pipeline.status_changed',
  'deployment.status_changed',
  'deployment.health_degraded',
  'git.branch.created',
  'git.branch.already_exists',
  'git.code.pushed',
  'git.pr.merged',
] as const

/**
 * Listens to the existing project SSE stream and refreshes the normalized
 * workspace aggregate when any domain event arrives. Normalized artifacts do
 * not live in the legacy snapshot, so the stream is used as a change signal
 * rather than a JSON Patch source.
 */
export function useWorkspaceStream(projectId: Ref<string>): {
  isConnected: Ref<boolean>
  isReconnecting: Ref<boolean>
  refresh: () => Promise<void>
} {
  const store = useWorkspaceStore()
  const isConnected = ref(false)
  const isReconnecting = ref(false)

  let source: EventSource | null = null
  let refreshTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectAttempts = 0
  let destroyed = false
  let connecting = false

  function close(): void {
    if (source) {
      source.onopen = null
      source.onmessage = null
      source.onerror = null
      source.close()
      source = null
    }
    isConnected.value = false
  }

  function scheduleRefresh(): void {
    if (refreshTimer !== null) return
    refreshTimer = setTimeout(() => {
      refreshTimer = null
      void store.refresh()
    }, 220)
  }

  async function connect(): Promise<void> {
    if (destroyed || !projectId.value || connecting) return
    connecting = true
    try {
      const { ticket } = await api.streamTicket()
      if (destroyed) return
      const url = new URL(
        `/api/v1/projects/${encodeURIComponent(projectId.value)}/stream`,
        window.location.origin,
      )
      url.searchParams.set('ticket', ticket)
      const next = new EventSource(url.toString())
      source = next
      next.onopen = () => {
        isConnected.value = true
        isReconnecting.value = false
        reconnectAttempts = 0
        // Re-read the aggregate after every connection so events missed during
        // an outage cannot leave the workspace stale.
        if (!store.loading) void store.refresh()
      }
      next.onmessage = scheduleRefresh
      for (const type of WORKSPACE_EVENT_TYPES) {
        next.addEventListener(type, scheduleRefresh as EventListener)
      }
      next.addEventListener('resync', scheduleRefresh as EventListener)
      next.onerror = () => {
        if (destroyed) return
        close()
        scheduleReconnect()
      }
    } catch {
      isConnected.value = false
      scheduleReconnect()
    } finally {
      connecting = false
    }
  }

  function scheduleReconnect(): void {
    if (destroyed || reconnectTimer !== null) return
    isReconnecting.value = true
    const delay = Math.min(1000 * 2 ** Math.min(reconnectAttempts, 5), 30_000)
    reconnectAttempts += 1
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      void connect()
    }, delay)
  }

  async function refresh(): Promise<void> {
    await store.refresh()
  }

  function cleanup(): void {
    destroyed = true
    isReconnecting.value = false
    if (refreshTimer !== null) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    close()
  }

  onMounted(() => {
    destroyed = false
    void connect()
  })

  onUnmounted(cleanup)

  watch(projectId, async (next, previous) => {
    if (next === previous) return
    cleanup()
    destroyed = false
    reconnectAttempts = 0
    await connect()
  })

  return { isConnected, isReconnecting, refresh }
}
