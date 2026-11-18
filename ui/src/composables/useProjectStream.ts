import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { Ref } from 'vue'
import type { Operation } from 'fast-json-patch'
import { api, ApiError } from '../api/client'
import { useProjectStore } from '../stores/projectStore'

export interface UseProjectStreamOptions {
  /** Called when a non-blocking toast should be displayed. */
  onToast?: (message: string) => void
  /** Optional override retained for embedders; the API client owns the base URL. */
  baseURL?: string
  /** Optional token ref retained for embedders; the API client reads the session. */
  token?: Ref<string | null> | Ref<string>
}

export interface UseProjectStreamReturn {
  isLoading: Ref<boolean>
  isConnected: Ref<boolean>
  isReconnecting: Ref<boolean>
  error: Ref<string | null>
  resync: () => Promise<void>
  reconnect: () => Promise<void>
  cleanup: () => void
}

const BASE_RECONNECT_DELAY_MS = 1000
const MAX_RECONNECT_DELAY_MS = 30_000

// EventSource matches named events exactly, so every event type produced by
// the reducer must be registered. Keep this in sync with
// api/internal/snapshot/reducer.go.
const SSE_EVENT_TYPES = [
  'project.created',
  'project.updated',
  'ticket.created',
  'ticket.updated',
  'ticket.state_changed',
  'ticket.deleted',
  'pipeline.status_changed',
  'deployment.status_changed',
  'deployment.health_degraded',
  'deployment.manifest_commit_failed',
  'git.branch.created',
  'git.branch.already_exists',
  'git.branch.creation_failed',
  'git.repo.provisioned',
  'git.repo.provision_failed',
  'git.code.pushed',
  'git.pr.merged',
  'notification.branch_creation_failed',
] as const

/**
 * Owns the real-time project lifecycle:
 *   1. fetch the snapshot and its durable sequence,
 *   2. obtain a single-use stream ticket,
 *   3. connect with only `?ticket=` (plus a cursor),
 *   4. apply named JSON Patch events in order,
 *   5. replay/resync safely across reconnects.
 */
export function useProjectStream(
  projectId: Ref<string>,
  options: UseProjectStreamOptions = {},
): UseProjectStreamReturn {
  const store = useProjectStore()
  const { onToast } = options

  const isLoading = ref(false)
  const isConnected = ref(false)
  const isReconnecting = ref(false)
  const error = ref<string | null>(null)

  let eventSource: EventSource | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectAttempts = 0
  let lastEventId: string | null = null
  let destroyed = false
  let connecting = false
  let resyncing = false

  async function fetchSnapshot(): Promise<void> {
    isLoading.value = true
    error.value = null

    try {
      const { state, sequence } = await api.getProject(projectId.value)
      store.hydrate(state, sequence)
      store.setSnapshotSequence(sequence)
      lastEventId = sequence > 0 ? String(sequence) : null
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to load project'
      error.value = message
      onToast?.(message)
      throw err
    } finally {
      isLoading.value = false
    }
  }

  function closeEventSource(): void {
    if (eventSource) {
      eventSource.onopen = null
      eventSource.onmessage = null
      eventSource.onerror = null
      eventSource.close()
      eventSource = null
    }
    isConnected.value = false
  }

  async function connect(): Promise<boolean> {
    if (destroyed || !projectId.value || connecting) return false
    connecting = true

    try {
      // JWT stays in the Authorization header. Only the opaque, single-use,
      // 30-second ticket is allowed in the EventSource URL.
      const { ticket } = await api.streamTicket()
      if (destroyed) return false

      const url = new URL(
        `/api/v1/projects/${encodeURIComponent(projectId.value)}/stream`,
        window.location.origin,
      )
      url.searchParams.set('ticket', ticket)
      if (lastEventId) url.searchParams.set('lastEventId', lastEventId)

      const source = new EventSource(url.toString())
      eventSource = source

      source.onopen = () => {
        isConnected.value = true
        isReconnecting.value = false
        reconnectAttempts = 0
        error.value = null
      }

      source.onmessage = handleMessage
      for (const type of SSE_EVENT_TYPES) {
        source.addEventListener(type, handleMessage as EventListener)
      }
      source.addEventListener('resync', handleResyncEvent)

      source.onerror = () => {
        if (destroyed) return
        isConnected.value = false
        closeEventSource()
        scheduleReconnect()
      }

      return true
    } catch (err) {
      if (destroyed) return false
      const message =
        err instanceof ApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : 'Failed to obtain stream ticket'
      error.value = message.startsWith('Failed to obtain stream ticket')
        ? message
        : `Failed to obtain stream ticket: ${message}`
      isConnected.value = false
      return false
    } finally {
      connecting = false
    }
  }

  function handleMessage(event: MessageEvent): void {
    if (!event.data) return

    let patch: Operation[]
    try {
      patch = JSON.parse(event.data) as Operation[]
      if (!Array.isArray(patch)) throw new Error('Patch payload is not an array')
    } catch (err) {
      console.warn('[useProjectStream] Ignoring malformed patch:', err)
      onToast?.('Received an invalid board update. Resynchronising…')
      void resync()
      return
    }

    try {
      store.applyPatch(patch)
      if (event.lastEventId) {
        lastEventId = event.lastEventId
        const sequence = Number(event.lastEventId)
        if (Number.isFinite(sequence)) store.setSnapshotSequence(sequence)
      }
    } catch (err) {
      console.warn('[useProjectStream] Failed to apply patch:', err)
      onToast?.('The board view may be out of date. Resynchronising…')
      void resync()
    }
  }

  async function handleResyncEvent(): Promise<void> {
    if (resyncing || destroyed) return
    closeEventSource()
    try {
      await resync()
    } catch {
      // resync/fetchSnapshot already published a useful error.
    }
  }

  function scheduleReconnect(): void {
    if (destroyed || reconnectTimer !== null) return

    isReconnecting.value = true
    const delay = Math.min(
      BASE_RECONNECT_DELAY_MS * 2 ** Math.min(reconnectAttempts, 5),
      MAX_RECONNECT_DELAY_MS,
    )
    reconnectAttempts += 1

    reconnectTimer = setTimeout(async () => {
      reconnectTimer = null
      if (destroyed) return
      const connected = await connect()
      if (!connected && !destroyed) scheduleReconnect()
    }, delay)
  }

  async function resync(): Promise<void> {
    if (destroyed) return
    if (resyncing) return
    resyncing = true
    closeEventSource()
    reconnectAttempts = 0
    isReconnecting.value = false
    lastEventId = null

    try {
      await fetchSnapshot()
    } catch (err) {
      resyncing = false
      // Do not open an event stream against a stale/empty read model.
      throw err
    }

    try {
      const connected = await connect()
      if (!connected) scheduleReconnect()
    } finally {
      resyncing = false
    }
  }

  async function reconnect(): Promise<void> {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    reconnectAttempts = 0
    isReconnecting.value = false
    await resync()
  }

  function cleanup(): void {
    destroyed = true
    isReconnecting.value = false
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    closeEventSource()
    store.$reset()
    lastEventId = null
    reconnectAttempts = 0
  }

  async function start(): Promise<void> {
    try {
      await fetchSnapshot()
    } catch {
      return
    }
    if (destroyed) return
    const connected = await connect()
    if (!connected && !destroyed) scheduleReconnect()
  }

  function handleOnline(): void {
    if (destroyed || isConnected.value || connecting) return
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    reconnectAttempts = 0
    void connect().then((connected) => {
      if (!connected && !destroyed) scheduleReconnect()
    })
  }

  onMounted(() => {
    window.addEventListener('online', handleOnline)
    void start()
  })

  onUnmounted(() => {
    window.removeEventListener('online', handleOnline)
    cleanup()
  })

  watch(projectId, async (newId, oldId) => {
    if (newId === oldId) return
    cleanup()
    destroyed = false
    await start()
  })

  return {
    isLoading,
    isConnected,
    isReconnecting,
    error,
    resync,
    reconnect,
    cleanup,
  }
}
