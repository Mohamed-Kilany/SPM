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
