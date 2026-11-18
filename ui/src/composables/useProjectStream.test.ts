import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, nextTick, toRef } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { useProjectStream } from './useProjectStream'
import { useProjectStore } from '../stores/projectStore'
import type { ProjectSnapshot, ProjectState, Ticket } from '../types/project'

const mocks = vi.hoisted(() => ({
  getProject: vi.fn(),
  streamTicket: vi.fn(),
}))

vi.mock('../api/client', () => ({
  api: {
    getProject: mocks.getProject,
    streamTicket: mocks.streamTicket,
  },
  ApiError: class ApiError extends Error {},
}))

class MockEventSource {
  static instances: MockEventSource[] = []

  url: string
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  closed = false
  private listeners = new Map<string, EventListener>()

  constructor(url: string) {
    this.url = url
    MockEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: EventListener): void {
    this.listeners.set(type, listener)
  }

  close(): void {
    this.closed = true
  }

  open(): void {
    this.onopen?.(new Event('open'))
  }

  fail(): void {
    this.onerror?.(new Event('error'))
  }

  emit(type: string, data: string, lastEventId = ''): void {
    this.listeners.get(type)?.({ data, lastEventId } as MessageEvent)
  }
}

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: '0192f3a4-1111-7000-8000-000000000001',
    title: 'Initial ticket',
    description: '',
    state: 'Backlog',
    assignee_id: null,
    priority: 'medium',
    branch_name: '',
    pipeline_status: '',
    created_at: '2026-10-30T08:00:00.000Z',
    updated_at: '2026-10-30T08:00:00.000Z',
    ...overrides,
  }
}

function makeState(): ProjectState {
  return {
    project_id: '0192f3a4-2222-7000-8000-000000000001',
    name: 'Atlas',
    description: '',
    owner_id: '00000000-0000-0000-0000-000000000001',
    repo_name: 'atlas',
    default_branch: 'main',
    app_name: 'atlas',
    columns: [
      { id: 'backlog', name: 'Backlog', position: 0, is_default: true },
      { id: 'progress', name: 'In Progress', position: 1, is_default: true },
    ],
    tickets: [makeTicket()],
    members: [],
    pipelines: [],
    deployments: [],
    notifications: [],
    git_provisioning_failed: false,
    updated_at: '2026-10-30T08:00:00.000Z',
  }
}

function makeSnapshot(sequence: number): ProjectSnapshot {
  return { state: makeState(), sequence }
}

function mountStream(projectId = '0192f3a4-2222-7000-8000-000000000001') {
  const pinia = createPinia()
  setActivePinia(pinia)
  let stream!: ReturnType<typeof useProjectStream>
  const onToast = vi.fn()

  const Component = defineComponent({
    setup() {
      stream = useProjectStream(toRef({ value: projectId }, 'value'), { onToast })
      return () => h('div')
    },
  })

  const wrapper = mount(Component, { global: { plugins: [pinia] } })
  return { wrapper, stream, onToast, store: useProjectStore() }
}

async function settle(): Promise<void> {
  await flushPromises()
  await nextTick()
  await flushPromises()
}

describe('useProjectStream', () => {
  beforeEach(() => {
    vi.useRealTimers()
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    MockEventSource.instances = []
    vi.stubGlobal('EventSource', MockEventSource)
    mocks.getProject.mockReset()
    mocks.streamTicket.mockReset()
    mocks.getProject.mockResolvedValue(makeSnapshot(7))
    mocks.streamTicket.mockResolvedValue({ ticket: '11111111-1111-4111-8111-111111111111' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('hydrates a snapshot, requests a ticket and connects with a cursor', async () => {
    const { wrapper, stream, store } = mountStream()
    await settle()

    expect(mocks.getProject).toHaveBeenCalledWith('0192f3a4-2222-7000-8000-000000000001')
    expect(mocks.streamTicket).toHaveBeenCalledTimes(1)
    expect(MockEventSource.instances).toHaveLength(1)

    const url = new URL(MockEventSource.instances[0]!.url)
    expect(url.pathname).toBe('/api/v1/projects/0192f3a4-2222-7000-8000-000000000001/stream')
    expect(url.searchParams.get('ticket')).toBe('11111111-1111-4111-8111-111111111111')
    expect(url.searchParams.get('lastEventId')).toBe('7')
    expect(store.name).toBe('Atlas')

    MockEventSource.instances[0]!.open()
    expect(stream.isConnected.value).toBe(true)
    expect(stream.isReconnecting.value).toBe(false)

    wrapper.unmount()
    expect(MockEventSource.instances[0]!.closed).toBe(true)
  })

  it('applies named RFC 6902 patches and advances the durable cursor', async () => {
    const { wrapper, store } = mountStream()
    await settle()
    const source = MockEventSource.instances[0]!

    source.emit(
      'ticket.created',
      JSON.stringify([
        {
          op: 'replace',
          path: '/tickets',
          value: [makeTicket({ id: 'new-ticket', title: 'Patched ticket', state: 'In Progress' })],
        },
      ]),
      '9',
    )

    expect(store.tickets).toHaveLength(1)
    expect(store.tickets[0]?.title).toBe('Patched ticket')
    expect(store.snapshotSequence).toBe(9)

    wrapper.unmount()
  })

  it('reconnects with a fresh single-use ticket after a transport error', async () => {
    vi.useFakeTimers()
    const { wrapper, stream } = mountStream()
    await settle()
    expect(MockEventSource.instances).toHaveLength(1)

    MockEventSource.instances[0]!.fail()
    expect(stream.isConnected.value).toBe(false)
    expect(stream.isReconnecting.value).toBe(true)
    expect(MockEventSource.instances[0]!.closed).toBe(true)

    await vi.advanceTimersByTimeAsync(1000)
    await settle()
    expect(mocks.streamTicket).toHaveBeenCalledTimes(2)
    expect(MockEventSource.instances).toHaveLength(2)

    MockEventSource.instances[1]!.open()
    expect(stream.isConnected.value).toBe(true)
    wrapper.unmount()
  })

  it('resynchronises the snapshot when the server signals a replay gap', async () => {
    const { wrapper, stream } = mountStream()
    await settle()
    mocks.getProject.mockResolvedValue(makeSnapshot(12))
    mocks.streamTicket.mockResolvedValue({ ticket: '22222222-2222-4222-8222-222222222222' })

    MockEventSource.instances[0]!.open()
    MockEventSource.instances[0]!.emit('resync', '{}')
    await settle()

    expect(MockEventSource.instances[0]!.closed).toBe(true)
    expect(mocks.getProject).toHaveBeenCalledTimes(2)
    expect(MockEventSource.instances).toHaveLength(2)
    const url = new URL(MockEventSource.instances[1]!.url)
    expect(url.searchParams.get('lastEventId')).toBe('12')

    MockEventSource.instances[1]!.open()
    expect(stream.isConnected.value).toBe(true)
    wrapper.unmount()
  })

  it('resynchronises when a patch cannot be applied and reports the problem', async () => {
    const { wrapper, store, onToast } = mountStream()
    await settle()
    mocks.getProject.mockResolvedValue(makeSnapshot(13))
    mocks.streamTicket.mockResolvedValue({ ticket: '33333333-3333-4333-8333-333333333333' })

    MockEventSource.instances[0]!.emit(
      'ticket.updated',
      JSON.stringify([{ op: 'replace', path: '/tickets/0/title', value: 'Title' }]),
      '12',
    )
    await settle()

    expect(store.tickets[0]?.title).toBe('Title')
    expect(store.snapshotSequence).toBe(12)
    expect(onToast).not.toHaveBeenCalled()

    // Replace a required field with an invalid shape; fast-json-patch applies
    // it, but a malformed JSON payload must force a snapshot resync.
    MockEventSource.instances[0]!.emit('ticket.updated', '{not-json', '13')
    await settle()
    expect(mocks.getProject).toHaveBeenCalledTimes(2)
    expect(onToast).toHaveBeenCalledWith(expect.stringContaining('invalid board update'))
    expect(MockEventSource.instances).toHaveLength(2)

    wrapper.unmount()
  })
})
