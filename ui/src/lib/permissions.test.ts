import { describe, expect, it } from 'vitest'
import {
  canDeactivateUser,
  canRequestOwnershipTransfer,
  canResolveOwnershipTransfer,
} from './permissions'

const transfer = {
  status: 'pending',
  to_user_id: 'target',
  from_user_id: 'owner',
  requested_by: 'requester',
}

describe('canRequestOwnershipTransfer', () => {
  it('allows an owner with a candidate and no pending request', () => {
    expect(
      canRequestOwnershipTransfer({
        isOwnerOrAdmin: true,
        hasPending: false,
        candidateCount: 1,
      }),
    ).toEqual({ allowed: true, reason: '' })
  })

  it('explains the missing permission', () => {
    const decision = canRequestOwnershipTransfer({
      isOwnerOrAdmin: false,
      hasPending: false,
      candidateCount: 1,
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toContain('Requires project owner')
  })

  it('explains the pending request', () => {
    const decision = canRequestOwnershipTransfer({
      isOwnerOrAdmin: true,
      hasPending: true,
      candidateCount: 1,
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toContain('already pending')
  })

  it('explains the missing candidate', () => {
    const decision = canRequestOwnershipTransfer({
      isOwnerOrAdmin: true,
      hasPending: false,
      candidateCount: 0,
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toContain('Add another project member')
  })
})

describe('canResolveOwnershipTransfer', () => {
  it('lets the target accept or decline', () => {
    for (const action of ['accept', 'decline'] as const) {
      expect(
        canResolveOwnershipTransfer({ action, actorId: 'target', isAdmin: false, transfer }),
      ).toEqual({ allowed: true, reason: '' })
    }
  })

  it('rejects acceptance by anyone else with a reason', () => {
    const decision = canResolveOwnershipTransfer({
      action: 'accept',
      actorId: 'owner',
      isAdmin: false,
      transfer,
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toContain('Only the transfer target')
  })

  it('lets only a global admin confirm on the target behalf', () => {
    expect(
      canResolveOwnershipTransfer({
        action: 'confirm',
        actorId: 'admin',
        isAdmin: true,
        transfer,
      }).allowed,
    ).toBe(true)
    const denied = canResolveOwnershipTransfer({
      action: 'confirm',
      actorId: 'owner',
      isAdmin: false,
      transfer,
    })
    expect(denied.allowed).toBe(false)
    expect(denied.reason).toContain('global admin')
  })

  it('lets the requester, current owner or admin cancel', () => {
    for (const actorId of ['requester', 'owner', 'admin']) {
      expect(
        canResolveOwnershipTransfer({
          action: 'cancel',
          actorId,
          isAdmin: actorId === 'admin',
          transfer,
        }).allowed,
      ).toBe(true)
    }
    const denied = canResolveOwnershipTransfer({
      action: 'cancel',
      actorId: 'target',
      isAdmin: false,
      transfer,
    })
    expect(denied.allowed).toBe(false)
    expect(denied.reason).toContain('requester')
  })

  it('rejects every action once the request is resolved', () => {
    const decision = canResolveOwnershipTransfer({
      action: 'accept',
      actorId: 'target',
      isAdmin: false,
      transfer: { ...transfer, status: 'accepted' },
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toContain('no longer pending')
  })
})

describe('canDeactivateUser', () => {
  it('allows deactivating another active user', () => {
    expect(
      canDeactivateUser({ actorId: 'admin', targetId: 'user', isActive: true }),
    ).toEqual({ allowed: true, reason: '' })
  })

  it('blocks self-lockout with an accessible reason', () => {
    const decision = canDeactivateUser({
      actorId: 'admin',
      targetId: 'admin',
      isActive: true,
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toBe('You cannot deactivate your own account.')
  })

  it('blocks an already disabled account', () => {
    const decision = canDeactivateUser({
      actorId: 'admin',
      targetId: 'user',
      isActive: false,
    })
    expect(decision.allowed).toBe(false)
    expect(decision.reason).toContain('already disabled')
  })
})
