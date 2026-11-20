<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import { useAuthStore } from '../stores/authStore'
import { useWorkspaceStore } from '../stores/workspaceStore'
import { useToastStore } from '../stores/toastStore'
import { displayVersion } from '../lib/version'

defineProps<{ open: boolean }>()
const emit = defineEmits<{ (event: 'close'): void }>()

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const workspace = useWorkspaceStore()
const toast = useToastStore()

const projectId = computed(() => (route.params.id as string | undefined) ?? '')
const isProject = computed(() => Boolean(projectId.value) && route.name === 'workspace')
const projectName = computed(() => workspace.project?.name || 'Project workspace')
const projectKey = computed(() => workspace.project?.key_prefix || 'Workspace')
const currentTab = computed(() => (route.query.tab as string | undefined) ?? 'overview')
const isProjects = computed(() => route.name === 'projects')
const isSearch = computed(() => route.name === 'search')
const isAdminUsers = computed(() => route.name === 'admin-users')
const isAdminAudit = computed(() => route.name === 'admin-audit')
const isProfile = computed(() => route.name === 'profile')
const appVersion = displayVersion(import.meta.env.VITE_APP_VERSION)

const projectTabs = [
  { key: 'overview', label: 'Overview', icon: 'activity' },
  { key: 'planning', label: 'Planning', icon: 'layers' },
  { key: 'board', label: 'Board', icon: 'board' },
  { key: 'tests', label: 'Test lab', icon: 'flask' },
  { key: 'code', label: 'Code', icon: 'code' },
  { key: 'delivery', label: 'Delivery', icon: 'rocket' },
  { key: 'reports', label: 'Reports', icon: 'chart' },
  { key: 'traceability', label: 'Traceability', icon: 'link' },
  { key: 'settings', label: 'Settings', icon: 'settings' },
]

function close(): void {
  emit('close')
}

function signOut(): void {
  auth.logout()
  toast.show('You have been signed out.', 'info')
  close()
  void router.replace({ name: 'login' })
}

function tabLink(tab: string) {
  return { name: 'workspace', params: { id: projectId.value }, query: { tab } }
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-40 bg-slate-900/30 backdrop-blur-sm lg:hidden"
    aria-hidden="true"
    @click="close"
  />

  <aside
    class="fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r border-slate-200 bg-white transition-transform duration-300 ease-out lg:translate-x-0"
    :class="open ? 'translate-x-0 shadow-2xl' : '-translate-x-full'"
    aria-label="Primary navigation"
  >
    <div class="flex h-[76px] items-center justify-between px-5">
      <RouterLink
        to="/projects"
        class="group flex items-center gap-3"
        title="SPM Platform home"
        @click="close"
      >
        <span
          class="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-600 text-white shadow-sm transition-transform duration-200 group-hover:scale-105"
        >
          <AppIcon name="sparkles" :size="19" />
        </span>
        <span class="flex flex-col leading-none">
          <span class="text-[15px] font-semibold tracking-tight text-slate-900">SPM Platform</span>
          <span class="mt-1 text-[10px] font-medium uppercase tracking-[0.18em] text-slate-600">
            Enterprise delivery
          </span>
        </span>
      </RouterLink>

      <button
        type="button"
        class="rounded-lg p-1.5 text-slate-600 hover:bg-slate-100 hover:text-slate-900 lg:hidden"
        aria-label="Close navigation"
        @click="close"
      >
        <AppIcon name="close" :size="18" />
      </button>
    </div>

    <div class="mx-5 h-px bg-slate-200" />

    <nav class="flex-1 overflow-y-auto px-3 py-5">
      <p class="mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">
        Workspace
      </p>

      <RouterLink
        to="/projects"
        class="group mb-0.5 flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
        :class="isProjects ? 'bg-indigo-50 text-indigo-700' : ''"
        @click="close"
      >
        <AppIcon name="projects" :size="18" :class="isProjects ? 'text-indigo-600' : 'text-slate-600 group-hover:text-indigo-600'" />
        <span class="flex-1">Projects</span>
      </RouterLink>

      <RouterLink
        to="/search"
        class="group mb-0.5 flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
        :class="isSearch ? 'bg-indigo-50 text-indigo-700' : ''"
        @click="close"
      >
        <AppIcon name="search" :size="18" :class="isSearch ? 'text-indigo-600' : 'text-slate-600 group-hover:text-indigo-600'" />
        <span class="flex-1">Search &amp; filters</span>
      </RouterLink>

      <RouterLink
        to="/profile"
        class="group mb-0.5 flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
        :class="isProfile ? 'bg-indigo-50 text-indigo-700' : ''"
        @click="close"
      >
        <AppIcon name="user" :size="18" :class="isProfile ? 'text-indigo-600' : 'text-slate-600 group-hover:text-indigo-600'" />
        <span class="flex-1">My profile</span>
      </RouterLink>

      <div v-if="auth.isAdmin" class="mt-6">
        <p class="mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">
          Administration
        </p>
        <RouterLink
          to="/admin/users"
          class="group flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
          :class="isAdminUsers ? 'bg-indigo-50 text-indigo-700' : ''"
          @click="close"
        >
          <AppIcon name="users" :size="18" :class="isAdminUsers ? 'text-indigo-600' : 'text-slate-600 group-hover:text-indigo-600'" />
          <span class="flex-1">Users &amp; roles</span>
        </RouterLink>
        <RouterLink
          to="/admin/audit"
          class="group flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
          :class="isAdminAudit ? 'bg-indigo-50 text-indigo-700' : ''"
          @click="close"
        >
          <AppIcon name="shield" :size="18" :class="isAdminAudit ? 'text-indigo-600' : 'text-slate-600 group-hover:text-indigo-600'" />
          <span class="flex-1">Audit log</span>
        </RouterLink>
      </div>

      <div v-if="isProject" class="mt-6">
        <p class="mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-slate-600">
          Current project
        </p>
        <RouterLink
          to="/projects"
          class="mb-3 flex items-center gap-2.5 rounded-xl border border-indigo-100 bg-indigo-50/60 p-3.5 transition-colors hover:bg-indigo-50"
          @click="close"
        >
          <span class="flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-indigo-600 text-white">
            <AppIcon name="layers" :size="16" />
          </span>
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold text-slate-900">{{ projectName }}</p>
            <p class="mt-0.5 font-mono text-[11px] text-slate-600">{{ projectKey }}</p>
          </div>
        </RouterLink>

        <RouterLink
          v-for="tab in projectTabs"
          :key="tab.key"
          :to="tabLink(tab.key)"
          :data-testid="`workspace-tab-${tab.key}`"
          class="group mb-0.5 flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
          :class="currentTab === tab.key ? 'bg-indigo-50 text-indigo-700' : ''"
          @click="close"
        >
          <AppIcon
            :name="tab.icon"
            :size="17"
            :class="currentTab === tab.key ? 'text-indigo-600' : 'text-slate-600 group-hover:text-slate-600'"
          />
          <span class="flex-1">{{ tab.label }}</span>
          <span
            v-if="tab.key === 'tests'"
            class="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold text-slate-600"
          >
            {{ workspace.workspace?.test_cases.length ?? 0 }}
          </span>
          <span
            v-else-if="tab.key === 'board'"
            class="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold text-slate-600"
          >
            {{ workspace.workspace?.work_items.length ?? 0 }}
          </span>
        </RouterLink>
      </div>
    </nav>

    <div class="border-t border-slate-200 p-3">
      <div class="flex items-center gap-2 rounded-xl p-2 hover:bg-slate-50">
        <RouterLink
          to="/profile"
          class="flex min-w-0 flex-1 items-center gap-3"
          title="Open your profile"
          @click="close"
        >
          <img
            v-if="auth.avatarUrl"
            :src="auth.avatarUrl"
            alt=""
            class="h-9 w-9 flex-none rounded-full object-cover ring-2 ring-white"
          />
          <span
            v-else
            class="flex h-9 w-9 flex-none items-center justify-center rounded-full bg-indigo-100 text-xs font-bold text-indigo-700 ring-2 ring-white"
          >
            {{ auth.initials }}
          </span>
          <span class="min-w-0 flex-1">
            <span class="block truncate text-sm font-medium text-slate-900">{{ auth.displayName }}</span>
            <span class="mt-0.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wider text-slate-600">
              <span
                class="inline-block h-1.5 w-1.5 rounded-full"
                :class="auth.isAdmin ? 'bg-indigo-500' : auth.isViewer ? 'bg-slate-400' : 'bg-sky-500'"
              />
              {{ auth.roleLabel }}
            </span>
          </span>
        </RouterLink>
        <button
          type="button"
          class="rounded-lg p-2 text-slate-600 hover:bg-red-50 hover:text-red-600"
          title="Sign out"
          aria-label="Sign out"
          @click="signOut"
        >
          <AppIcon name="logout" :size="17" />
        </button>
      </div>
      <p
        class="mt-2 text-center text-[10px] font-medium uppercase tracking-[0.16em] text-slate-400"
        data-testid="app-version"
      >
        Version {{ appVersion }}
      </p>
    </div>
  </aside>
</template>
