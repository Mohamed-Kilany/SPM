import type { OwnershipTransfer } from '../types/admin'

export type OwnershipTransferAction = 'accept' | 'decline' | 'confirm' | 'cancel'

export interface PermissionDecision {
  allowed: boolean
  reason: string
}

const ALLOWED: PermissionDecision = { allowed: true, reason: '' }

/**
 * Ownership can be requested by a project owner or a global admin, only when no
 * other request is pending and at least one eligible member exists.
 */
export function canRequestOwnershipTransfer(input: {
  isOwnerOrAdmin: boolean
  hasPending: boolean
  candidateCount: number
}): PermissionDecision {
  if (!input.isOwnerOrAdmin) {
    return { allowed: false, reason: 'Requires project owner or global admin.' }
  }
  if (input.hasPending) {
    return { allowed: false, reason: 'A transfer is already pending for this project.' }
  }
  if (input.candidateCount === 0) {
    return {
      allowed: false,
      reason: 'Add another project member before transferring ownership.',
    }
  }
  return ALLOWED
}

/**
 * Mirrors the API permission matrix so the UI can explain every disabled
 * action instead of hiding it.
 */
export function canResolveOwnershipTransfer(input: {
  action: OwnershipTransferAction
  actorId: string
  isAdmin: boolean
  transfer: Pick<
    OwnershipTransfer,
    'status' | 'to_user_id' | 'from_user_id' | 'requested_by'
  >
}): PermissionDecision {
  if (input.transfer.status !== 'pending') {
    return { allowed: false, reason: 'This request is no longer pending.' }
  }
  switch (input.action) {
    case 'accept':
    case 'decline':
      return input.actorId === input.transfer.to_user_id
        ? ALLOWED
        : {
            allowed: false,
            reason: 'Only the transfer target can accept or decline this request.',
          }
    case 'confirm':
      return input.isAdmin
        ? ALLOWED
        : {
            allowed: false,
            reason: 'Confirming on the target’s behalf requires a global admin.',
          }
    case 'cancel':
      return input.isAdmin ||
        input.actorId === input.transfer.requested_by ||
        input.actorId === input.transfer.from_user_id
        ? ALLOWED
        : {
            allowed: false,
            reason:
              'Only the requester, the current owner or a global admin can cancel this request.',
          }
  }
}

/** Self-lockout and already-disabled accounts cannot be deactivated again. */
export function canDeactivateUser(input: {
  actorId: string
  targetId: string
  isActive: boolean
}): PermissionDecision {
  if (!input.isActive) {
    return { allowed: false, reason: 'This account is already disabled.' }
  }
  if (input.actorId === input.targetId) {
    return { allowed: false, reason: 'You cannot deactivate your own account.' }
  }
  return ALLOWED
}
