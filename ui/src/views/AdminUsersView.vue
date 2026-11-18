<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import BaseModal from '../components/BaseModal.vue'
import AppIcon from '../components/AppIcon.vue'
import { api } from '../api/client'
import { useAuthStore } from '../stores/authStore'
import { useToastStore } from '../stores/toastStore'
import { GLOBAL_ROLES } from '../types/admin'
import type { AdminUser } from '../types/admin'

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
  busyUserId.value = user.id
  try {
    await api.adminUpdateUser(user.id, { is_active: !user.is_active })
    toast.success(
      user.is_active ? 'User deactivated.' : 'User activated.',
      'Users & roles',
    )
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
                  :disabled="busyUserId === user.id || user.id === auth.user?.id"
                  :title="user.id === auth.user?.id ? 'You cannot disable your own account' : undefined"
                  @click="toggleActive(user)"
                >
                  <AppIcon v-if="busyUserId === user.id" name="loader" :size="15" class="animate-spin" />
                  <AppIcon v-else :name="user.is_active ? 'lock' : 'check-circle'" :size="15" />
                  {{ user.is_active ? 'Disable' : 'Enable' }}
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
          <label class="block">
            <span class="field-label">Account state</span>
            <select v-model="editForm.is_active" class="field-input">
              <option :value="true">Active</option>
              <option :value="false">Disabled</option>
            </select>
          </label>
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
  </div>
</template>
