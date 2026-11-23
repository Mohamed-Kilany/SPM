<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import AppIcon from '../AppIcon.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import EmptyState from './EmptyState.vue'
import LabelManager from './LabelManager.vue'
import CustomFieldManager from './CustomFieldManager.vue'
import { api } from '../../api/client'
import { boardColumns } from '../../lib/workspace'
import {
  canRequestOwnershipTransfer,
  canResolveOwnershipTransfer,
} from '../../lib/permissions'
import type { OwnershipTransferAction, PermissionDecision } from '../../lib/permissions'
import { useAuthStore } from '../../stores/authStore'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { PROJECT_ROLES } from '../../types/admin'
import type { DirectoryUser, OwnershipTransfer, ProjectMember } from '../../types/admin'
import type { BoardSettings } from '../../types/workspace'

const auth = useAuthStore()
const store = useWorkspaceStore()
const toast = useToastStore()

const detailsBusy = ref(false)
const detailsError = ref('')
const details = reactive({
  name: '',
  description: '',
  key_prefix: '',
  default_branch: '',
  status: 'active',
})

const directory = ref<DirectoryUser[]>([])
const directoryError = ref('')
const memberSearch = ref('')
const newMemberId = ref('')
const newMemberRole = ref('developer')
const memberBusy = ref(false)
const memberError = ref('')

const removeTarget = ref<ProjectMember | null>(null)
const removing = ref(false)

const project = computed(() => store.project)
const members = computed(() => store.workspace?.members ?? [])
const projectId = computed(() => project.value?.id ?? '')
const canEditSettings = computed(() => auth.can(projectId.value, 'settings.write'))
const canManageMembers = computed(() => auth.can(projectId.value, 'member.write'))
const canManage = computed(() => canEditSettings.value || canManageMembers.value)
// Only an owner (or a global admin) may grant the owner role; the API
// enforces the same rule.
const canGrantOwner = computed(
  () => auth.isAdmin || auth.projectRole(projectId.value) === 'owner',
)
const assignableRoles = computed(() =>
  PROJECT_ROLES.filter((role) => canGrantOwner.value || role.value !== 'owner'),
)

const availableUsers = computed(() => {
  const memberIds = new Set(members.value.map((member) => member.user_id))
  const term = memberSearch.value.trim().toLowerCase()
  return directory.value.filter((user) => {
    if (memberIds.has(user.id)) return false
    if (!term) return true
    return [user.username, user.email, user.display_name]
      .filter(Boolean)
      .some((value) => value.toLowerCase().includes(term))
  })
})

const selectedUser = computed(() => directory.value.find((user) => user.id === newMemberId.value))

function hydrateDetails(): void {
  if (!project.value) return
  details.name = project.value.name
  details.description = project.value.description
  details.key_prefix = project.value.key_prefix
  details.default_branch = project.value.default_branch || 'main'
  details.status = project.value.status || 'active'
}

watch(project, hydrateDetails, { immediate: true })

async function loadDirectory(): Promise<void> {
  if (!canManageMembers.value) return
  directoryError.value = ''
  try {
    directory.value = await api.listDirectoryUsers()
  } catch (error) {
    directoryError.value = error instanceof Error ? error.message : 'Failed to load users'
  }
}

onMounted(loadDirectory)
watch(canManageMembers, (allowed) => {
  if (allowed) void loadDirectory()
})

async function saveDetails(): Promise<void> {
  if (!project.value) return
  detailsBusy.value = true
  detailsError.value = ''
  try {
    await api.updateProject(project.value.id, {
      name: details.name.trim(),
      description: details.description.trim(),
      key_prefix: details.key_prefix.trim().toUpperCase(),
      default_branch: details.default_branch.trim(),
      status: details.status,
    })
    await store.refresh()
    toast.success('Project settings saved.', 'Project settings')
  } catch (error) {
    detailsError.value = error instanceof Error ? error.message : 'Failed to save project'
  } finally {
    detailsBusy.value = false
  }
}

async function addMember(): Promise<void> {
  if (!project.value || !newMemberId.value) return
  memberBusy.value = true
  memberError.value = ''
  try {
    await api.addProjectMember(project.value.id, newMemberId.value, newMemberRole.value)
    newMemberId.value = ''
    newMemberRole.value = 'developer'
    memberSearch.value = ''
    await store.refresh()
    toast.success('Project member added.', 'Members')
  } catch (error) {
    memberError.value = error instanceof Error ? error.message : 'Failed to add member'
  } finally {
    memberBusy.value = false
  }
}

async function updateRole(member: ProjectMember, role: string): Promise<void> {
  if (!project.value) return
  try {
    await api.updateProjectMember(project.value.id, member.user_id, role)
    await store.refresh()
    toast.success('Project role updated.', 'Members')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to update role', 'Members')
    await store.refresh()
  }
}

async function removeMember(): Promise<void> {
  if (!project.value || !removeTarget.value) return
  removing.value = true
  try {
    await api.removeProjectMember(project.value.id, removeTarget.value.user_id)
    removeTarget.value = null
    await store.refresh()
    toast.success('Project member removed.', 'Members')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to remove member', 'Members')
  } finally {
    removing.value = false
  }
}

function roleLabel(role: string): string {
  return PROJECT_ROLES.find((entry) => entry.value === role)?.label ?? role
}

function memberName(member: ProjectMember): string {
  return member.username || member.email || member.user_id
}

function initials(member: ProjectMember): string {
  const source = member.username || member.email || '?'
  return source
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? '')
    .join('')
}

// ---------------------------------------------------------------------------
// Ownership transfer
// ---------------------------------------------------------------------------

const ownership = ref<OwnershipTransfer[]>([])
const ownershipBusy = ref(false)
const ownershipError = ref('')
const transferTarget = ref('')
const transferReason = ref('')
const transferBusy = ref(false)
const transferActionBusy = ref('')
const cancelTransferTarget = ref<OwnershipTransfer | null>(null)
const confirmTransferTarget = ref<OwnershipTransfer | null>(null)

const currentUserId = computed(() => auth.user?.id ?? '')
const ownerMember = computed(() => members.value.find((member) => member.role === 'owner') ?? null)
const pendingTransfer = computed(
  () => ownership.value.find((transfer) => transfer.status === 'pending') ?? null,
)
const transferCandidates = computed(() =>
  members.value.filter(
    (member) => member.role !== 'owner' && member.user_id !== currentUserId.value,
  ),
)
const requestDecision = computed(() =>
  canRequestOwnershipTransfer({
    isOwnerOrAdmin: canGrantOwner.value,
    hasPending: Boolean(pendingTransfer.value),
    candidateCount: transferCandidates.value.length,
  }),
)
const canRequestTransfer = computed(() => requestDecision.value.allowed)
const requestDisabledReason = computed(() => requestDecision.value.reason)

function resolveTransferDecision(action: OwnershipTransferAction): PermissionDecision {
  if (!pendingTransfer.value) {
    return { allowed: false, reason: 'No pending ownership transfer.' }
  }
  return canResolveOwnershipTransfer({
    action,
    actorId: currentUserId.value,
    isAdmin: auth.isAdmin,
    transfer: pendingTransfer.value,
  })
}

const acceptDecision = computed(() => resolveTransferDecision('accept'))
const confirmDecision = computed(() => resolveTransferDecision('confirm'))
const cancelDecision = computed(() => resolveTransferDecision('cancel'))
const canAcceptTransfer = computed(() => acceptDecision.value.allowed)
const canConfirmTransfer = computed(() => confirmDecision.value.allowed)
const canCancelTransfer = computed(() => cancelDecision.value.allowed)
const acceptDisabledReason = computed(() => acceptDecision.value.reason)
const confirmDisabledReason = computed(() => confirmDecision.value.reason)
const cancelDisabledReason = computed(() => cancelDecision.value.reason)
const resolvedTransfers = computed(() =>
  ownership.value.filter((transfer) => transfer.status !== 'pending').slice(0, 5),
)

async function loadOwnership(): Promise<void> {
  if (!projectId.value) return
  ownershipBusy.value = true
  ownershipError.value = ''
  try {
    ownership.value = await api.listOwnershipTransfers(projectId.value)
  } catch (error) {
    ownershipError.value = error instanceof Error ? error.message : 'Failed to load ownership transfers'
  } finally {
    ownershipBusy.value = false
  }
}

async function requestTransfer(): Promise<void> {
  if (!projectId.value || !transferTarget.value || !canRequestTransfer.value) return
  transferBusy.value = true
  ownershipError.value = ''
  try {
    await api.requestOwnershipTransfer(projectId.value, {
      to_user_id: transferTarget.value,
      reason: transferReason.value.trim(),
    })
    transferTarget.value = ''
    transferReason.value = ''
    await loadOwnership()
    toast.success('Ownership transfer requested. The target must accept it.', 'Ownership')
  } catch (error) {
    ownershipError.value = error instanceof Error ? error.message : 'Failed to request the transfer'
  } finally {
    transferBusy.value = false
  }
}

async function resolveTransfer(transfer: OwnershipTransfer, action: 'accept' | 'decline' | 'confirm' | 'cancel'): Promise<void> {
  if (!projectId.value) return
  transferActionBusy.value = `${transfer.id}:${action}`
  ownershipError.value = ''
  try {
    if (action === 'accept') await api.acceptOwnershipTransfer(projectId.value, transfer.id)
    else if (action === 'decline') await api.declineOwnershipTransfer(projectId.value, transfer.id)
    else if (action === 'confirm') await api.confirmOwnershipTransfer(projectId.value, transfer.id)
    else await api.cancelOwnershipTransfer(projectId.value, transfer.id)
    cancelTransferTarget.value = null
    confirmTransferTarget.value = null
    await Promise.all([loadOwnership(), store.refresh()])
    const labels: Record<typeof action, string> = {
      accept: 'Ownership accepted.',
      decline: 'Ownership transfer declined.',
      confirm: 'Ownership transfer confirmed.',
      cancel: 'Ownership transfer cancelled.',
    }
    toast.success(labels[action], 'Ownership')
  } catch (error) {
    ownershipError.value = error instanceof Error ? error.message : 'Failed to resolve the transfer'
  } finally {
    transferActionBusy.value = ''
  }
}

function transferStatusClass(status: string): string {
  if (status === 'accepted' || status === 'confirmed') {
    return 'bg-emerald-50 text-emerald-700 ring-emerald-200'
  }
  if (status === 'pending') return 'bg-amber-50 text-amber-700 ring-amber-200'
  return 'bg-slate-100 text-slate-600 ring-slate-200'
}

function formatTransferDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'Unknown'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}

watch(projectId, () => {
  ownership.value = []
  transferTarget.value = ''
  transferReason.value = ''
  void loadOwnership()
})

onMounted(loadOwnership)

// ---------------------------------------------------------------------------
// Board policy (WIP limits)
// ---------------------------------------------------------------------------

const boardBusy = ref(false)
const boardError = ref('')
const boardForm = reactive<{ wip_limits: Record<string, string>; wip_hard_block: boolean }>({
  wip_limits: {},
  wip_hard_block: false,
})

function hydrateBoard(): void {
  const board = store.project?.settings?.board
  for (const key of Object.keys(boardForm.wip_limits)) delete boardForm.wip_limits[key]
  for (const column of boardColumns) {
    const limit = board?.wip_limits?.[column.key] ?? 0
    boardForm.wip_limits[column.key] = limit > 0 ? String(limit) : ''
  }
  boardForm.wip_hard_block = board?.wip_hard_block ?? false
}

watch(project, hydrateBoard, { immediate: true })

async function saveBoardPolicy(): Promise<void> {
  if (!canEditSettings.value || !project.value) return
  boardError.value = ''
  const limits: Record<string, number> = {}
  for (const column of boardColumns) {
    const raw = String(boardForm.wip_limits[column.key] ?? '').trim()
    if (!raw) continue
    const value = Number(raw)
    if (!Number.isInteger(value) || value < 0 || value > 999) {
      boardError.value = `${column.label} must be a whole number between 0 and 999.`
      return
    }
    if (value > 0) limits[column.key] = value
  }
  boardBusy.value = true
  try {
    const board: BoardSettings = {
      wip_limits: limits,
      wip_hard_block: boardForm.wip_hard_block,
    }
    await api.updateSettings(project.value.id, { ...project.value.settings, board })
    await store.refresh()
    toast.success(
      boardForm.wip_hard_block
        ? 'Board policy saved. Moves above a limit will be blocked.'
        : 'Board policy saved. Limits warn only.',
      'Board policy',
    )
  } catch (error) {
    boardError.value = error instanceof Error ? error.message : 'Failed to save board policy'
  } finally {
    boardBusy.value = false
  }
}

</script>

<template>
  <div class="space-y-6">
    <header>
      <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">Project governance</p>
      <h2 class="mt-2 text-xl font-semibold tracking-tight text-slate-900">Settings</h2>
    </header>

    <div
      v-if="!canManage"
      class="flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800"
    >
      <AppIcon name="lock" :size="17" class="mt-0.5 flex-none" />
      <p>
        You have read-only access. Editing details needs
        <code class="font-mono">settings.write</code>; managing members needs
        <code class="font-mono">member.write</code>. Ask a project owner or maintainer.
      </p>
    </div>

    <section class="card p-6">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Project details</h3>
        </div>
        <span class="badge bg-slate-100 text-slate-600 ring-slate-200">
          {{ project?.key_prefix || '—' }}
        </span>
      </div>

      <form class="mt-5 space-y-4" @submit.prevent="saveDetails">
        <div class="grid gap-4 lg:grid-cols-2">
          <label class="block">
            <span class="field-label">Project name</span>
            <input v-model="details.name" class="field-input" maxlength="255" :disabled="!canEditSettings" required />
          </label>
          <label class="block">
            <span class="field-label">Key prefix</span>
            <input
              v-model="details.key_prefix"
              class="field-input font-mono uppercase"
              maxlength="12"
              :disabled="!canEditSettings"
              required
            />
            <span class="mt-1 block text-[11px] text-slate-500">2–12 uppercase letters or digits. Used for artifact keys.</span>
          </label>
        </div>
        <label class="block">
          <span class="field-label">Description</span>
          <textarea v-model="details.description" class="field-input min-h-[92px]" maxlength="4000" :disabled="!canEditSettings" />
        </label>
        <div class="grid gap-4 lg:grid-cols-2">
          <label class="block">
            <span class="field-label">Default branch</span>
            <input v-model="details.default_branch" class="field-input font-mono" maxlength="255" :disabled="!canEditSettings" required />
          </label>
          <label class="block">
            <span class="field-label">Project status</span>
            <select v-model="details.status" class="field-input" :disabled="!canEditSettings">
              <option value="active">Active</option>
              <option value="archived">Archived</option>
            </select>
          </label>
        </div>
        <p v-if="detailsError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
          {{ detailsError }}
        </p>
        <div class="flex flex-wrap items-center justify-end gap-3">
          <p v-if="!canEditSettings" id="settings-write-reason" class="text-[11px] text-slate-500">
            Requires project owner or maintainer (<code class="font-mono">settings.write</code>).
          </p>
          <button
            type="submit"
            class="btn-primary"
            :disabled="!canEditSettings || detailsBusy"
            :title="!canEditSettings ? 'Requires project owner or maintainer' : undefined"
            :aria-describedby="!canEditSettings ? 'settings-write-reason' : undefined"
          >
            <AppIcon v-if="detailsBusy" name="loader" :size="15" class="animate-spin" />
            Save project
          </button>
        </div>
      </form>
    </section>

    <section class="card overflow-hidden">
      <div class="flex flex-wrap items-start justify-between gap-3 border-b border-slate-200 p-6">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Members and project roles</h3>
        </div>
        <span class="badge bg-indigo-50 text-indigo-700 ring-indigo-200">{{ members.length }} members</span>
      </div>

      <div v-if="canManageMembers" class="border-b border-slate-200 bg-slate-50/70 p-4 sm:p-5">
        <form class="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_auto]" @submit.prevent="addMember">
          <div class="relative">
            <label class="sr-only" for="member-search">Search users</label>
            <AppIcon name="search" :size="15" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
            <input
              id="member-search"
              v-model="memberSearch"
              class="field-input pl-9"
              placeholder="Search directory to add a member"
              autocomplete="off"
            />
          </div>
          <select v-model="newMemberId" class="field-input" aria-label="User to add" required>
            <option value="">Select user…</option>
            <option v-for="user in availableUsers" :key="user.id" :value="user.id">
              {{ user.display_name || user.username }} (@{{ user.username }})
            </option>
          </select>
          <div class="flex gap-2">
            <select v-model="newMemberRole" class="field-input" aria-label="New member role">
              <option v-for="role in assignableRoles" :key="role.value" :value="role.value">{{ role.label }}</option>
            </select>
            <button type="submit" class="btn-primary flex-none" :disabled="memberBusy || !newMemberId">
              <AppIcon v-if="memberBusy" name="loader" :size="15" class="animate-spin" />
              Add
            </button>
          </div>
        </form>
        <p v-if="selectedUser" class="mt-2 text-[11px] text-slate-500">
          Adding {{ selectedUser.display_name || selectedUser.username }} as {{ roleLabel(newMemberRole) }}.
        </p>
        <p v-if="directoryError" class="mt-2 text-xs text-red-600">{{ directoryError }}</p>
        <p v-if="memberError" class="mt-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
          {{ memberError }}
        </p>
      </div>

      <div v-if="members.length" class="divide-y divide-slate-100">
        <div v-for="member in members" :key="member.user_id" class="flex flex-wrap items-center gap-3 px-4 py-3 sm:px-6">
          <span class="flex h-9 w-9 flex-none items-center justify-center rounded-full bg-slate-100 text-[11px] font-bold text-slate-600">
            {{ initials(member) }}
          </span>
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium text-slate-900">{{ memberName(member) }}</p>
            <p class="truncate text-xs text-slate-500">{{ member.email }}</p>
          </div>
          <div class="flex items-center gap-2">
            <select
              v-if="canManageMembers && member.role !== 'owner'"
              :value="member.role"
              class="field-input w-auto min-w-[130px] py-1.5 text-xs"
              :aria-label="`Role for ${memberName(member)}`"
              @change="updateRole(member, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="role in assignableRoles" :key="role.value" :value="role.value">{{ role.label }}</option>
            </select>
            <span v-else class="badge bg-slate-100 text-slate-700 ring-slate-200">{{ roleLabel(member.role) }}</span>
            <button
              v-if="canManageMembers && member.role !== 'owner'"
              type="button"
              class="btn-ghost text-red-600 hover:bg-red-50"
              :aria-label="`Remove ${memberName(member)}`"
              @click="removeTarget = member"
            >
              <AppIcon name="trash" :size="15" />
            </button>
          </div>
        </div>
      </div>
      <div v-else class="p-6">
        <EmptyState title="No members" />
      </div>
    </section>

    <section class="card p-6">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Project ownership</h3>
          <p class="mt-1 text-xs text-slate-500">
            Ownership moves only when the target accepts or a global admin confirms. Every change is audited.
          </p>
        </div>
        <span v-if="ownerMember" class="badge bg-indigo-50 text-indigo-700 ring-indigo-200">
          Owner: {{ memberName(ownerMember) }}
        </span>
        <span v-else class="badge bg-amber-50 text-amber-700 ring-amber-200">No owner membership</span>
      </div>

      <p v-if="ownershipError" class="mt-4 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
        {{ ownershipError }}
      </p>

      <div v-if="ownershipBusy && !ownership.length" class="mt-4 space-y-2">
        <div class="skeleton h-14 rounded-lg" />
      </div>

      <div v-else-if="pendingTransfer" class="mt-4 rounded-xl border border-amber-200 bg-amber-50/60 p-4">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div>
            <p class="text-sm font-semibold text-amber-900">Pending transfer</p>
            <p class="mt-1 text-xs text-amber-800">
              {{ pendingTransfer.from_user }} → {{ pendingTransfer.to_user }} · requested by
              {{ pendingTransfer.requested_by_name }} on {{ formatTransferDate(pendingTransfer.created_at) }}
            </p>
            <p v-if="pendingTransfer.reason" class="mt-1 text-xs text-amber-800">
              Reason: {{ pendingTransfer.reason }}
            </p>
          </div>
          <span class="badge" :class="transferStatusClass(pendingTransfer.status)">Pending</span>
        </div>

        <div class="mt-3 flex flex-wrap gap-2">
          <span :title="acceptDisabledReason || 'Accept ownership of this project'">
            <button
              type="button"
              class="btn-primary"
              :disabled="!canAcceptTransfer || Boolean(transferActionBusy)"
              :aria-describedby="!canAcceptTransfer ? 'transfer-accept-reason' : undefined"
              @click="resolveTransfer(pendingTransfer, 'accept')"
            >
              <AppIcon v-if="transferActionBusy === `${pendingTransfer.id}:accept`" name="loader" :size="14" class="animate-spin" />
              Accept
            </button>
          </span>
          <span :title="acceptDisabledReason || 'Decline ownership of this project'">
            <button
              type="button"
              class="btn-secondary"
              :disabled="!canAcceptTransfer || Boolean(transferActionBusy)"
              :aria-describedby="!canAcceptTransfer ? 'transfer-accept-reason' : undefined"
              @click="resolveTransfer(pendingTransfer, 'decline')"
            >
              Decline
            </button>
          </span>
          <span :title="confirmDisabledReason || 'Confirm on the target’s behalf'">
            <button
              type="button"
              class="btn-secondary"
              :disabled="!canConfirmTransfer || Boolean(transferActionBusy)"
              :aria-describedby="!canConfirmTransfer ? 'transfer-confirm-reason' : undefined"
              @click="confirmTransferTarget = pendingTransfer"
            >
              Confirm on target’s behalf
            </button>
          </span>
          <span :title="cancelDisabledReason || 'Cancel this transfer'">
            <button
              type="button"
              class="btn-ghost text-red-600 hover:bg-red-50"
              :disabled="!canCancelTransfer || Boolean(transferActionBusy)"
              :aria-describedby="!canCancelTransfer ? 'transfer-cancel-reason' : undefined"
              @click="cancelTransferTarget = pendingTransfer"
            >
              Cancel transfer
            </button>
          </span>
        </div>
        <p v-if="!canAcceptTransfer" id="transfer-accept-reason" class="mt-2 text-[11px] text-amber-800">
          Accept and decline require the transfer target.
        </p>
        <p v-if="!canConfirmTransfer" id="transfer-confirm-reason" class="mt-1 text-[11px] text-amber-800">
          Confirming on the target’s behalf requires a global admin.
        </p>
        <p v-if="!canCancelTransfer" id="transfer-cancel-reason" class="mt-1 text-[11px] text-amber-800">
          Cancelling requires the requester, the current owner or a global admin.
        </p>
      </div>

      <form v-else class="mt-4 space-y-3" @submit.prevent="requestTransfer">
        <div class="grid gap-3 sm:grid-cols-[minmax(200px,1fr)_minmax(220px,1.4fr)_auto]">
          <label class="block">
            <span class="field-label">New owner</span>
            <select
              v-model="transferTarget"
              class="field-input"
              :disabled="!canRequestTransfer"
              :title="requestDisabledReason || undefined"
              :aria-describedby="requestDisabledReason ? 'transfer-request-reason' : undefined"
              required
            >
              <option value="">Select a project member…</option>
              <option v-for="member in transferCandidates" :key="member.user_id" :value="member.user_id">
                {{ memberName(member) }} ({{ roleLabel(member.role) }})
              </option>
            </select>
          </label>
          <label class="block">
            <span class="field-label">Reason (optional)</span>
            <input
              v-model="transferReason"
              class="field-input"
              maxlength="500"
              placeholder="Handover context for the audit log"
              :disabled="!canRequestTransfer"
              :title="requestDisabledReason || undefined"
            />
          </label>
          <div class="flex items-end">
            <button
              type="submit"
              class="btn-primary w-full justify-center sm:w-auto"
              :disabled="!canRequestTransfer || transferBusy || !transferTarget"
              :title="requestDisabledReason || undefined"
              :aria-describedby="requestDisabledReason ? 'transfer-request-reason' : undefined"
            >
              <AppIcon v-if="transferBusy" name="loader" :size="14" class="animate-spin" />
              Request transfer
            </button>
          </div>
        </div>
        <p v-if="requestDisabledReason" id="transfer-request-reason" class="text-[11px] text-slate-500">
          {{ requestDisabledReason }}
        </p>
        <p v-else class="text-[11px] text-slate-500">
          The target must already be a project member. They will receive the request in their profile.
        </p>
      </form>

      <div v-if="resolvedTransfers.length" class="mt-5 border-t border-slate-200 pt-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Recent transfers</p>
        <ul class="mt-2 divide-y divide-slate-100">
          <li v-for="transfer in resolvedTransfers" :key="transfer.id" class="flex flex-wrap items-center justify-between gap-2 py-2">
            <p class="text-xs text-slate-600">
              {{ transfer.from_user }} → {{ transfer.to_user }} · {{ formatTransferDate(transfer.created_at) }}
            </p>
            <span class="badge" :class="transferStatusClass(transfer.status)">{{ transfer.status }}</span>
          </li>
        </ul>
      </div>
    </section>

    <section class="card p-6">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Board policy</h3>
          <p class="mt-1 text-xs text-slate-500">
            Work-in-progress limits are per column. Limits warn by default; the hard block is explicit and enforced by the API.
          </p>
        </div>
        <span class="badge bg-slate-100 text-slate-600 ring-slate-200">
          {{ boardForm.wip_hard_block ? 'Hard block' : 'Warn only' }}
        </span>
      </div>

      <form class="mt-5 space-y-4" @submit.prevent="saveBoardPolicy">
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <label v-for="column in boardColumns" :key="column.key" class="block">
            <span class="field-label">{{ column.label }}</span>
            <input
              v-model="boardForm.wip_limits[column.key]"
              type="number"
              min="0"
              max="999"
              step="1"
              class="field-input"
              :placeholder="'No limit'"
              :disabled="!canEditSettings"
              :aria-label="`WIP limit for ${column.label}`"
            />
          </label>
        </div>
        <label class="flex items-start gap-2.5 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2.5">
          <input v-model="boardForm.wip_hard_block" type="checkbox" class="mt-0.5 h-4 w-4 rounded border-slate-300" :disabled="!canEditSettings" />
          <span>
            <span class="block text-xs font-semibold text-slate-800">Reject moves that exceed a limit</span>
            <span class="mt-0.5 block text-[11px] text-slate-500">
              When enabled, the API returns <code class="font-mono">409 WIP_LIMIT</code> for a move or create that would overfill a column.
              Leave it off to let the board warn without blocking delivery.
            </span>
          </span>
        </label>
        <p v-if="boardError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">{{ boardError }}</p>
        <div class="flex flex-wrap items-center justify-end gap-3">
          <p v-if="!canEditSettings" id="board-write-reason" class="text-[11px] text-slate-500">
            Requires project owner or maintainer (<code class="font-mono">settings.write</code>).
          </p>
          <button
            type="submit"
            class="btn-primary"
            :disabled="boardBusy || !canEditSettings"
            :title="!canEditSettings ? 'Requires project owner or maintainer' : undefined"
            :aria-describedby="!canEditSettings ? 'board-write-reason' : undefined"
          >
            <AppIcon v-if="boardBusy" name="loader" :size="14" class="animate-spin" />
            {{ boardBusy ? 'Saving…' : 'Save board policy' }}
          </button>
        </div>
      </form>
    </section>

    <LabelManager />
    <CustomFieldManager />

    <ConfirmDialog
      :open="Boolean(removeTarget)"
      title="Remove project member"
      :message="removeTarget ? `Remove ${memberName(removeTarget)} from this project? Their work items remain, but they will lose project access.` : ''"
      confirm-label="Remove member"
      :busy="removing"
      @confirm="removeMember"
      @cancel="removeTarget = null"
      @update:open="(value) => { if (!value) removeTarget = null }"
    />

    <ConfirmDialog
      :open="Boolean(confirmTransferTarget)"
      title="Confirm ownership transfer"
      :message="confirmTransferTarget ? `Move ownership from ${confirmTransferTarget.from_user} to ${confirmTransferTarget.to_user} without the target accepting first? The change is audited and takes effect immediately.` : ''"
      confirm-label="Confirm transfer"
      :busy="Boolean(transferActionBusy)"
      @confirm="confirmTransferTarget && resolveTransfer(confirmTransferTarget, 'confirm')"
      @cancel="confirmTransferTarget = null"
      @update:open="(value) => { if (!value) confirmTransferTarget = null }"
    />

    <ConfirmDialog
      :open="Boolean(cancelTransferTarget)"
      title="Cancel ownership transfer"
      :message="cancelTransferTarget ? `Cancel the pending transfer from ${cancelTransferTarget.from_user} to ${cancelTransferTarget.to_user}? The project owner stays unchanged.` : ''"
      confirm-label="Cancel transfer"
      :busy="Boolean(transferActionBusy)"
      @confirm="cancelTransferTarget && resolveTransfer(cancelTransferTarget, 'cancel')"
      @cancel="cancelTransferTarget = null"
      @update:open="(value) => { if (!value) cancelTransferTarget = null }"
    />
  </div>
</template>
