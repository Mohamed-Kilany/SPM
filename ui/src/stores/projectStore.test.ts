import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useProjectStore } from './projectStore'
import type { ProjectState, Ticket } from '../types/project'

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: '0192f3a4-1111-7000-8000-000000000001',
    title: 'Ship the board',
    description: 'Realtime board',
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

function makeSnapshot(): ProjectState {
  return {
    project_id: '0192f3a4-2222-7000-8000-000000000001',
    name: 'Atlas',
    description: 'Delivery project',
    owner_id: '00000000-0000-0000-0000-000000000001',
    repo_name: 'atlas',
    default_branch: 'main',
    app_name: 'atlas',
    columns: [
      { id: 'col-done', name: 'Done', position: 3, is_default: false },
      { id: 'col-backlog', name: 'Backlog', position: 0, is_default: true },
      { id: 'col-progress', name: 'In Progress', position: 1, is_default: false },
    ],
    tickets: [makeTicket()],
    members: [{ user_id: '00000000-0000-0000-0000-000000000001', role: 'admin' }],
    pipelines: [],
    deployments: [],
    notifications: [],
    git_provisioning_failed: false,
    updated_at: '2026-10-30T08:00:00.000Z',
  }
}

describe('project read-model store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('hydrates a snapshot, sorts columns and records the sequence', () => {
    const store = useProjectStore()
    store.hydrate(makeSnapshot(), 34)

    expect(store.projectId).toBe('0192f3a4-2222-7000-8000-000000000001')
    expect(store.orderedColumns.map((column) => column.name)).toEqual([
      'Backlog',
      'In Progress',
      'Done',
    ])
    expect(store.snapshotSequence).toBe(34)
    expect(store.ticketById('0192f3a4-1111-7000-8000-000000000001')?.title).toBe('Ship the board')
  })

  it('applies RFC 6902 patches and groups tickets by state', () => {
    const store = useProjectStore()
    store.hydrate(makeSnapshot(), 34)

    const replacement = makeTicket({
      title: 'Patched ticket',
      state: 'In Progress',
      updated_at: '2026-10-30T09:00:00.000Z',
    })
    store.applyPatch([{ op: 'replace', path: '/tickets', value: [replacement] }])

    expect(store.tickets).toHaveLength(1)
    expect(store.tickets[0]?.title).toBe('Patched ticket')
    expect(store.ticketsByState['In Progress']).toHaveLength(1)
    expect(store.ticketsByState['Backlog']).toHaveLength(0)
  })

  it('keeps degraded deployment alerts sticky until dismissed', () => {
    const store = useProjectStore()
    const snapshot = makeSnapshot()
    snapshot.deployments = [
      {
        app_name: 'atlas',
        sync_status: 'Synced',
        health_status: 'Degraded',
        commit_sha: 'abc1234',
        updated_at: '2026-10-30T08:30:00.000Z',
      },
    ]
    store.hydrate(snapshot, 5)
    expect(store.hasDegradedAlert).toBe(true)

    // A later healthy snapshot should not silently clear an active incident.
    store.hydrate(makeSnapshot(), 6)
    expect(store.hasDegradedAlert).toBe(true)

    store.dismissDegradedAlert()
    expect(store.hasDegradedAlert).toBe(false)
  })

  it('supports optimistic ticket mutations and a full reset', () => {
    const store = useProjectStore()
    store.hydrate(makeSnapshot(), 10)

    const created = makeTicket({
      id: '0192f3a4-3333-7000-8000-000000000003',
      title: 'New ticket',
    })
    store.addTicket(created)
    expect(store.tickets).toHaveLength(2)

    store.patchTicket(created.id, { title: 'Renamed' })
    expect(store.ticketById(created.id)?.title).toBe('Renamed')

    store.setTicketState(created.id, 'Done')
    expect(store.ticketById(created.id)?.state).toBe('Done')

    store.removeTicket(created.id)
    expect(store.ticketById(created.id)).toBeNull()

    store.$reset()
    expect(store.projectId).toBeNull()
    expect(store.tickets).toHaveLength(0)
    expect(store.snapshotSequence).toBe(0)
  })
})
