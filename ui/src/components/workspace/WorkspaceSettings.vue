<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import AppIcon from '../AppIcon.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import EmptyState from './EmptyState.vue'
import { api } from '../../api/client'
import { useAuthStore } from '../../stores/authStore'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { PROJECT_ROLES } from '../../types/admin'
import type { DirectoryUser, ProjectMember } from '../../types/admin'

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
        <div class="flex justify-end">
          <button type="submit" class="btn-primary" :disabled="!canEditSettings || detailsBusy">
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
  </div>
</template>
