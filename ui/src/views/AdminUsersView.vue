<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import BaseModal from '../components/BaseModal.vue'
import AppIcon from '../components/AppIcon.vue'
import { api } from '../api/client'
import { useAuthStore } from '../stores/authStore'
import { useToastStore } from '../stores/toastStore'
import { GLOBAL_ROLES } from '../types/admin'
import type { AdminUser, OffboardingImpact } from '../types/admin'
import { canDeactivateUser } from '../lib/permissions'
import type { PermissionDecision } from '../lib/permissions'

const auth = useAuthStore()
const toast = useToastStore()

const users = ref<AdminUser[]>([])
const loading = ref(true)
const loadError = ref('')
const search = ref('')
const roleFilter = ref('')
const busyUserId = ref('')

const filteredUsers = computed(() => {
  const term = search.value.trim().toLowerCase()
  return users.value.filter((user) => {
    if (roleFilter.value && user.role !== roleFilter.value) return false
    if (!term) return true
    return [user.username, user.email, user.display_name]
      .filter(Boolean)
      .some((value) => value.toLowerCase().includes(term))
  })
})

const activeCount = computed(() => users.value.filter((user) => user.is_active).length)
const adminCount = computed(
  () => users.value.filter((user) => user.is_active && user.role === 'admin').length,
)

const showCreate = ref(false)
const createBusy = ref(false)
const createError = ref('')
const createForm = reactive({
  username: '',
  email: '',
  display_name: '',
  role: 'member',
  password: '',
})

const editing = ref<AdminUser | null>(null)
const editBusy = ref(false)
const editError = ref('')
const editForm = reactive({
  display_name: '',
  email: '',
  avatar_url: '',
  role: 'member',
  is_active: true,
  password: '',
})

function roleLabel(role: string): string {
  return GLOBAL_ROLES.find((entry) => entry.value === role)?.label ?? role
}

function roleClass(role: string): string {
  if (role === 'admin') return 'bg-indigo-50 text-indigo-700 ring-indigo-200'
  if (role === 'member') return 'bg-sky-50 text-sky-700 ring-sky-200'
  return 'bg-slate-100 text-slate-700 ring-slate-200'
}

function initials(user: AdminUser): string {
  const source = user.display_name || user.username
  return source
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? '')
    .join('')
}

function deactivationDecision(user: AdminUser): PermissionDecision {
  if (!user.is_active) return { allowed: true, reason: '' }
  return canDeactivateUser({
    actorId: auth.user?.id ?? '',
    targetId: user.id,
    isActive: user.is_active,
  })
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    users.value = await api.adminListUsers()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'Failed to load users'
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  createForm.username = ''
  createForm.email = ''
  createForm.display_name = ''
  createForm.role = 'member'
  createForm.password = ''
  createError.value = ''
  showCreate.value = true
}

async function createUser(): Promise<void> {
  createBusy.value = true
  createError.value = ''
  try {
    await api.adminCreateUser({
      username: createForm.username.trim(),
      email: createForm.email.trim(),
      display_name: createForm.display_name.trim() || createForm.username.trim(),
      role: createForm.role,
      password: createForm.password,
    })
    showCreate.value = false
    toast.success('User created.', 'Users & roles')
    await load()
  } catch (error) {
    createError.value = error instanceof Error ? error.message : 'Failed to create user'
  } finally {
    createBusy.value = false
  }
}

function openEdit(user: AdminUser): void {
  editing.value = user
  editForm.display_name = user.display_name
  editForm.email = user.email
  editForm.avatar_url = user.avatar_url
  editForm.role = user.role
  editForm.is_active = user.is_active
  editForm.password = ''
  editError.value = ''
}

async function saveEdit(): Promise<void> {
  if (!editing.value) return
  editBusy.value = true
  editError.value = ''
  try {
    await api.adminUpdateUser(editing.value.id, {
      display_name: editForm.display_name.trim(),
      email: editForm.email.trim(),
      avatar_url: editForm.avatar_url.trim(),
      role: editForm.role,
      is_active: editForm.is_active,
      ...(editForm.password ? { password: editForm.password } : {}),
    })
    if (editing.value.id === auth.user?.id) {
      await auth.refreshUser()
    }
    editing.value = null
    toast.success('User updated.', 'Users & roles')
    await load()
  } catch (error) {
    editError.value = error instanceof Error ? error.message : 'Failed to update user'
  } finally {
    editBusy.value = false
  }
}

async function toggleActive(user: AdminUser): Promise<void> {
  if (user.is_active) {
    await openOffboarding(user)
    return
  }
  busyUserId.value = user.id
  try {
    await api.adminUpdateUser(user.id, { is_active: true })
    toast.success('User activated.', 'Users & roles')
    await load()
  } catch (error) {
    toast.error(
      error instanceof Error ? error.message : 'Failed to update user',
      'Users & roles',
    )
  } finally {
    busyUserId.value = ''
  }
}

// ---------------------------------------------------------------------------
// Offboarding
// ---------------------------------------------------------------------------

const offboardingUser = ref<AdminUser | null>(null)
const offboardingImpact = ref<OffboardingImpact | null>(null)
const offboardingLoading = ref(false)
const offboardingError = ref('')
const offboardingSubmitting = ref(false)
const offboardingForm = reactive({
  reason: '',
  reassign_to: '',
  project_owners: {} as Record<string, string>,
})

const successorCandidates = computed(() =>
  users.value.filter(
    (user) => user.is_active && user.id !== offboardingUser.value?.id,
  ),
)

const ownedArtifactTotal = computed(() => {
  const counts = offboardingImpact.value?.owned_artifacts ?? {}
  return Object.values(counts).reduce((total, count) => total + count, 0)
})

const offboardingBlockedReason = computed(() => {
  const impact = offboardingImpact.value
  if (!impact) return 'Load the impact preview first.'
  if (impact.is_last_admin) return 'This is the last active admin; deactivation is blocked.'
  if (offboardingForm.reason.trim().length < 3) return 'Enter a reason between 3 and 500 characters.'
  if (impact.requires_reassignment && !offboardingForm.reassign_to) {
    return 'Choose a successor for the affected projects and open work.'
  }
  return ''
})

async function openOffboarding(user: AdminUser): Promise<void> {
  offboardingUser.value = user
  offboardingImpact.value = null
  offboardingError.value = ''
  offboardingForm.reason = ''
  offboardingForm.reassign_to = ''
  offboardingForm.project_owners = {}
  await loadOffboardingImpact()
}

async function loadOffboardingImpact(): Promise<void> {
  if (!offboardingUser.value) return
  offboardingLoading.value = true
  offboardingError.value = ''
  try {
    const impact = await api.adminOffboardingImpact(offboardingUser.value.id)
    offboardingImpact.value = impact
    const owners: Record<string, string> = {}
    for (const project of impact.owned_projects) {
      owners[project.id] = ''
    }
    offboardingForm.project_owners = owners
  } catch (error) {
    offboardingError.value = error instanceof Error ? error.message : 'Failed to load offboarding impact'
  } finally {
    offboardingLoading.value = false
  }
}

async function submitOffboarding(): Promise<void> {
  if (!offboardingUser.value || offboardingBlockedReason.value) return
  offboardingSubmitting.value = true
  offboardingError.value = ''
  const projectOwners: Record<string, string> = {}
  for (const [projectId, userId] of Object.entries(offboardingForm.project_owners)) {
    if (userId) projectOwners[projectId] = userId
  }
  try {
    const result = await api.adminOffboardUser(offboardingUser.value.id, {
      reason: offboardingForm.reason.trim(),
      ...(offboardingForm.reassign_to ? { reassign_to: offboardingForm.reassign_to } : {}),
      ...(Object.keys(projectOwners).length ? { project_owners: projectOwners } : {}),
    })
    toast.success(
      `User deactivated. Reassigned ${result.reassigned_projects} project(s), ${result.reassigned_items} item(s) and revoked ${result.revoked_sessions} session(s).`,
      'Offboarding',
    )
    offboardingUser.value = null
    offboardingImpact.value = null
    await load()
  } catch (error) {
    offboardingError.value = error instanceof Error ? error.message : 'Failed to deactivate the user'
  } finally {
    offboardingSubmitting.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">Administration</p>
        <h1 class="mt-2 text-2xl font-semibold tracking-tight text-slate-900">Users &amp; roles</h1>
      </div>
      <button type="button" class="btn-primary" @click="openCreate">
        <AppIcon name="plus" :size="16" />
        New user
      </button>
    </header>

    <div class="mt-6 grid gap-3 sm:grid-cols-3">
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Total users</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ users.length }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Active</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ activeCount }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Platform admins</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ adminCount }}</p>
      </div>
    </div>

    <div class="mt-5 flex flex-wrap items-center gap-3">
      <label class="relative min-w-[240px] flex-1">
        <span class="sr-only">Search users</span>
        <AppIcon name="search" :size="16" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
        <input v-model="search" class="field-input pl-9" placeholder="Search name, username or email" />
      </label>
      <select v-model="roleFilter" class="field-input w-auto min-w-[160px]" aria-label="Filter by global role">
        <option value="">All global roles</option>
        <option v-for="role in GLOBAL_ROLES" :key="role.value" :value="role.value">{{ role.label }}</option>
      </select>
    </div>

    <div v-if="loading" class="mt-5 space-y-2">
      <div v-for="index in 5" :key="index" class="skeleton h-16 rounded-xl" />
    </div>

    <div v-else-if="loadError" class="card mt-5 p-8 text-center">
      <span class="mx-auto flex h-11 w-11 items-center justify-center rounded-xl bg-red-50 text-red-600">
        <AppIcon name="alert" :size="20" />
      </span>
      <h2 class="mt-3 text-base font-semibold text-slate-900">Users unavailable</h2>
      <p class="mt-1 text-sm text-slate-600">{{ loadError }}</p>
      <button type="button" class="btn-secondary mt-4" @click="load">Try again</button>
    </div>

    <div v-else class="table-shell mt-5">
      <p id="self-lockout-reason" class="sr-only">You cannot deactivate your own account.</p>
      <table class="min-w-full divide-y divide-slate-200 text-sm">
        <thead class="table-head">
          <tr>
            <th class="px-4 py-3">User</th>
            <th class="px-4 py-3">Global role</th>
            <th class="hidden px-4 py-3 sm:table-cell">Projects</th>
            <th class="px-4 py-3">Status</th>
            <th class="px-4 py-3 text-right">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100">
          <tr v-for="user in filteredUsers" :key="user.id" class="table-row">
            <td class="px-4 py-3">
              <div class="flex items-center gap-3">
                <img
                  v-if="user.avatar_url"
                  :src="user.avatar_url"
                  alt=""
                  class="h-9 w-9 rounded-full object-cover"
                />
                <span
                  v-else
                  class="flex h-9 w-9 flex-none items-center justify-center rounded-full bg-slate-100 text-[11px] font-bold text-slate-600"
                >
                  {{ initials(user) }}
                </span>
                <div class="min-w-0">
                  <p class="truncate font-medium text-slate-900">{{ user.display_name || user.username }}</p>
                  <p class="truncate text-xs text-slate-500">@{{ user.username }} · {{ user.email }}</p>
                </div>
              </div>
            </td>
            <td class="px-4 py-3">
              <span class="badge" :class="roleClass(user.role)">{{ roleLabel(user.role) }}</span>
            </td>
            <td class="hidden px-4 py-3 text-slate-600 sm:table-cell">{{ user.project_ids.length }}</td>
            <td class="px-4 py-3">
              <span
                class="badge"
                :class="user.is_active ? 'bg-emerald-50 text-emerald-700 ring-emerald-200' : 'bg-slate-100 text-slate-600 ring-slate-200'"
              >
                {{ user.is_active ? 'Active' : 'Disabled' }}
              </span>
            </td>
            <td class="px-4 py-3">
              <div class="flex justify-end gap-1">
                <button type="button" class="btn-ghost" @click="openEdit(user)">
                  <AppIcon name="edit" :size="15" />
                  Edit
                </button>
                <button
                  type="button"
                  class="btn-ghost"
                  :class="user.is_active ? 'hover:text-red-600' : 'hover:text-emerald-700'"
                  :disabled="busyUserId === user.id || !deactivationDecision(user).allowed"
                  :title="deactivationDecision(user).reason || (user.is_active ? 'Deactivate and reassign work' : 'Enable user')"
                  :aria-describedby="!deactivationDecision(user).allowed ? 'self-lockout-reason' : undefined"
                  @click="toggleActive(user)"
                >
                  <AppIcon v-if="busyUserId === user.id" name="loader" :size="15" class="animate-spin" />
                  <AppIcon v-else :name="user.is_active ? 'lock' : 'check-circle'" :size="15" />
                  {{ user.is_active ? 'Deactivate' : 'Enable' }}
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="!filteredUsers.length">
            <td colspan="5" class="px-4 py-10 text-center text-sm text-slate-500">
              No users match this filter.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <BaseModal v-model="showCreate" title="Create user" max-width="md">
      <form class="space-y-4" @submit.prevent="createUser">
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="field-label">Username</span>
            <input v-model="createForm.username" class="field-input" autocomplete="off" required />
          </label>
          <label class="block">
            <span class="field-label">Display name</span>
            <input v-model="createForm.display_name" class="field-input" autocomplete="off" />
          </label>
        </div>
        <label class="block">
          <span class="field-label">Email</span>
          <input v-model="createForm.email" type="email" class="field-input" autocomplete="off" required />
        </label>
        <label class="block">
          <span class="field-label">Global role</span>
          <select v-model="createForm.role" class="field-input">
            <option v-for="role in GLOBAL_ROLES" :key="role.value" :value="role.value">{{ role.label }}</option>
          </select>
        </label>
        <label class="block">
          <span class="field-label">Initial password</span>
          <input v-model="createForm.password" type="password" class="field-input" autocomplete="new-password" minlength="10" required />
          <span class="mt-1 block text-[11px] text-slate-500">At least 10 characters. The user can change it after signing in.</span>
        </label>
        <p v-if="createError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
          {{ createError }}
        </p>
        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="showCreate = false">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="createBusy">
            <AppIcon v-if="createBusy" name="loader" :size="15" class="animate-spin" />
            Create user
          </button>
        </div>
      </form>
    </BaseModal>

    <BaseModal
      :model-value="Boolean(editing)"
      title="Edit user"
      :description="editing ? `@${editing.username}` : ''"
      max-width="md"
      @update:model-value="(value) => { if (!value) editing = null }"
    >
      <form v-if="editing" class="space-y-4" @submit.prevent="saveEdit">
        <label class="block">
          <span class="field-label">Display name</span>
          <input v-model="editForm.display_name" class="field-input" required />
        </label>
        <label class="block">
          <span class="field-label">Email</span>
          <input v-model="editForm.email" type="email" class="field-input" required />
        </label>
        <label class="block">
          <span class="field-label">Avatar URL</span>
          <input v-model="editForm.avatar_url" class="field-input" placeholder="Optional image URL" />
        </label>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="field-label">Global role</span>
            <select v-model="editForm.role" class="field-input">
              <option v-for="role in GLOBAL_ROLES" :key="role.value" :value="role.value">{{ role.label }}</option>
            </select>
          </label>
          <div class="block">
            <span class="field-label">Account state</span>
            <p class="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-xs text-slate-600">
              {{ editing.is_active ? 'Active. Use Deactivate in the table to reassign work and revoke sessions.' : 'Disabled. Use Enable in the table to reactivate.' }}
            </p>
          </div>
        </div>
        <label class="block">
          <span class="field-label">Reset password</span>
          <input v-model="editForm.password" type="password" class="field-input" autocomplete="new-password" minlength="10" placeholder="Leave blank to keep current password" />
        </label>
        <p v-if="editError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
          {{ editError }}
        </p>
        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn-secondary" @click="editing = null">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="editBusy">
            <AppIcon v-if="editBusy" name="loader" :size="15" class="animate-spin" />
            Save changes
          </button>
        </div>
      </form>
    </BaseModal>

    <BaseModal
      :model-value="Boolean(offboardingUser)"
      :title="offboardingUser ? `Deactivate @${offboardingUser.username}` : 'Deactivate user'"
      description="Review the impact, choose successors and record a reason. Sessions are revoked immediately."
      max-width="lg"
      @update:model-value="(value) => { if (!value) { offboardingUser = null; offboardingImpact = null } }"
    >
      <div v-if="offboardingUser" class="space-y-5">
        <div v-if="offboardingLoading" class="space-y-2">
          <div class="skeleton h-16 rounded-lg" />
          <div class="skeleton h-24 rounded-lg" />
        </div>

        <p v-else-if="offboardingError && !offboardingImpact" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
          {{ offboardingError }}
        </p>

        <template v-else-if="offboardingImpact">
          <div class="grid gap-3 sm:grid-cols-4">
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-500">Projects</p>
              <p class="mt-1 text-xl font-semibold text-slate-900">{{ offboardingImpact.owned_projects.length }}</p>
            </div>
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-500">Open items</p>
              <p class="mt-1 text-xl font-semibold text-slate-900">{{ offboardingImpact.open_assignments.length }}</p>
            </div>
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-500">Approvals</p>
              <p class="mt-1 text-xl font-semibold text-slate-900">{{ offboardingImpact.pending_approvals.length }}</p>
            </div>
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-500">Artifacts</p>
              <p class="mt-1 text-xl font-semibold text-slate-900">{{ ownedArtifactTotal }}</p>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2 text-xs text-slate-600">
            <span class="badge bg-slate-100 text-slate-700 ring-slate-200">{{ offboardingImpact.role }}</span>
            <span class="badge bg-slate-100 text-slate-700 ring-slate-200">
              {{ offboardingImpact.active_sessions }} active session(s)
            </span>
            <span v-if="offboardingImpact.requires_reassignment" class="badge bg-amber-50 text-amber-700 ring-amber-200">
              Reassignment required
            </span>
          </div>

          <ul v-if="offboardingImpact.warnings.length" class="space-y-1">
            <li
              v-for="warning in offboardingImpact.warnings"
              :key="warning"
              class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800"
            >
              <AppIcon name="alert" :size="14" class="mt-0.5 flex-none" />
              <span>{{ warning }}</span>
            </li>
          </ul>

          <p v-if="offboardingImpact.is_last_admin" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
            Deactivation is blocked because this is the last active global admin.
          </p>

          <div v-if="offboardingImpact.owned_projects.length" class="space-y-2">
            <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Owned projects</p>
            <div
              v-for="project in offboardingImpact.owned_projects"
              :key="project.id"
              class="grid gap-2 rounded-lg border border-slate-200 p-3 sm:grid-cols-[minmax(0,1fr)_minmax(180px,240px)]"
            >
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-slate-900">{{ project.name }}</p>
                <p class="mt-0.5 text-[11px] text-slate-500">
                  {{ project.status }} · {{ project.owner_count }} owner(s){{ project.sole_owner ? ' · sole owner' : '' }}
                </p>
              </div>
              <label class="block">
                <span class="field-label">Project successor</span>
                <select v-model="offboardingForm.project_owners[project.id]" class="field-input">
                  <option value="">Use default successor</option>
                  <option v-for="candidate in successorCandidates" :key="candidate.id" :value="candidate.id">
                    {{ candidate.display_name || candidate.username }}
                  </option>
                </select>
              </label>
            </div>
          </div>

          <div v-if="offboardingImpact.open_assignments.length" class="space-y-2">
            <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Open assignments</p>
            <ul class="divide-y divide-slate-100 rounded-lg border border-slate-200">
              <li v-for="item in offboardingImpact.open_assignments.slice(0, 8)" :key="item.id" class="flex items-center justify-between gap-3 px-3 py-2 text-xs">
                <span class="min-w-0 truncate text-slate-700">
                  <code class="font-mono text-slate-500">{{ item.key }}</code>
                  {{ item.title }}
                </span>
                <span class="badge bg-slate-100 text-slate-600 ring-slate-200">{{ item.status }}</span>
              </li>
              <li v-if="offboardingImpact.open_assignments.length > 8" class="px-3 py-2 text-[11px] text-slate-500">
                + {{ offboardingImpact.open_assignments.length - 8 }} more
              </li>
            </ul>
          </div>

          <div v-if="offboardingImpact.pending_approvals.length" class="space-y-2">
            <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Pending deployment approvals</p>
            <ul class="divide-y divide-slate-100 rounded-lg border border-slate-200">
              <li v-for="approval in offboardingImpact.pending_approvals.slice(0, 5)" :key="approval.id" class="flex items-center justify-between gap-3 px-3 py-2 text-xs">
                <span class="min-w-0 truncate text-slate-700">{{ approval.project_name }}</span>
                <code class="font-mono text-[10px] text-slate-500">{{ approval.deployment_id.slice(0, 8) }}</code>
              </li>
            </ul>
          </div>

          <div class="space-y-3">
            <label class="block">
              <span class="field-label">Successor</span>
              <select
                v-model="offboardingForm.reassign_to"
                class="field-input"
                :required="offboardingImpact.requires_reassignment"
                :aria-describedby="offboardingImpact.requires_reassignment ? 'offboarding-successor-reason' : undefined"
              >
                <option value="">Select an active user…</option>
                <option v-for="candidate in successorCandidates" :key="candidate.id" :value="candidate.id">
                  {{ candidate.display_name || candidate.username }} (@{{ candidate.username }})
                </option>
              </select>
              <span
                v-if="offboardingImpact.requires_reassignment"
                id="offboarding-successor-reason"
                class="mt-1 block text-[11px] text-slate-500"
              >
                Required because this user owns projects, open work, approvals or planning artifacts.
              </span>
            </label>

            <label class="block">
              <span class="field-label">Reason</span>
              <textarea
                v-model="offboardingForm.reason"
                class="field-input min-h-[80px]"
                maxlength="500"
                minlength="3"
                required
                placeholder="Why is this account being deactivated?"
              />
              <span class="mt-1 block text-[11px] text-slate-500">{{ offboardingForm.reason.trim().length }}/500 · stored in the audit log</span>
            </label>
          </div>

          <p v-if="offboardingError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
            {{ offboardingError }}
          </p>

          <div class="flex flex-wrap items-center justify-end gap-3 border-t border-slate-200 pt-4">
            <p v-if="offboardingBlockedReason" id="offboarding-blocked-reason" class="mr-auto text-[11px] text-slate-500">
              {{ offboardingBlockedReason }}
            </p>
            <button type="button" class="btn-secondary" :disabled="offboardingSubmitting" @click="offboardingUser = null; offboardingImpact = null">
              Cancel
            </button>
            <button
              type="button"
              class="btn-primary bg-red-600 hover:bg-red-700"
              :disabled="offboardingSubmitting || Boolean(offboardingBlockedReason)"
              :aria-describedby="offboardingBlockedReason ? 'offboarding-blocked-reason' : undefined"
              @click="submitOffboarding"
            >
              <AppIcon v-if="offboardingSubmitting" name="loader" :size="15" class="animate-spin" />
              Deactivate and reassign
            </button>
          </div>
        </template>
      </div>
    </BaseModal>
  </div>
</template>
