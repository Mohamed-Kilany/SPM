import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const { getWorkspace } = vi.hoisted(() => ({ getWorkspace: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { getWorkspace },
}))

import { useWorkspaceStore } from './workspaceStore'
import type { Workspace } from '../types/workspace'

describe('workspace refresh', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getWorkspace.mockReset()
    useWorkspaceStore().clearSnapshotCache()
  })

  it('coalesces concurrent refreshes and resolves both callers after the snapshot lands', async () => {
    let resolve!: (value: Workspace) => void
    getWorkspace.mockReturnValue(
      new Promise<Workspace>((done) => {
        resolve = done
      }),
    )
    const store = useWorkspaceStore()
    store.projectId = 'project-1'

    const first = store.refresh()
    const second = store.refresh()
    expect(getWorkspace).toHaveBeenCalledTimes(1)

    const snapshot = { work_items: [] } as unknown as Workspace
    resolve(snapshot)
    await Promise.all([first, second])

    expect(store.workspace).toStrictEqual(snapshot)
    expect(store.refreshing).toBe(false)
    expect(store.error).toBe('')
  })
})

describe('workspace snapshot cache', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getWorkspace.mockReset()
    useWorkspaceStore().clearSnapshotCache()
  })

  it('paints the cached snapshot while a fresh one loads', async () => {
    const first = { work_items: [{ id: 'first' }] } as unknown as Workspace
    const second = { work_items: [{ id: 'second' }] } as unknown as Workspace
    getWorkspace.mockResolvedValueOnce(first)
    const store = useWorkspaceStore()
    await store.load('project-1')
    expect(store.workspace).toStrictEqual(first)

    let resolve!: (value: Workspace) => void
    getWorkspace.mockReturnValueOnce(
      new Promise<Workspace>((done) => {
        resolve = done
      }),
    )
    store.reset()
    const loading = store.load('project-1')
    expect(store.workspace).toStrictEqual(first)
    expect(store.loading).toBe(false)

    resolve(second)
    await loading
    expect(store.workspace).toStrictEqual(second)
    expect(store.loading).toBe(false)
  })

  it('evicts the oldest snapshot after five projects', async () => {
    const store = useWorkspaceStore()
    for (let index = 1; index <= 6; index += 1) {
      getWorkspace.mockResolvedValueOnce({ work_items: [{ id: `item-${index}` }] } as unknown as Workspace)
      store.reset()
      await store.load(`project-${index}`)
    }

    let resolve!: (value: Workspace) => void
    getWorkspace.mockReturnValueOnce(
      new Promise<Workspace>((done) => {
        resolve = done
      }),
    )
    store.reset()
    const loading = store.load('project-1')
    expect(store.workspace).toBeNull()
    expect(store.loading).toBe(true)

    resolve({ work_items: [] } as unknown as Workspace)
    await loading
  })
})
