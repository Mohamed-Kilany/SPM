<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import StatusPill from '../components/workspace/StatusPill.vue'
import WorkspaceOverview from '../components/workspace/WorkspaceOverview.vue'
import WorkspacePlanning from '../components/workspace/WorkspacePlanning.vue'
import WorkspaceBoard from '../components/workspace/WorkspaceBoard.vue'
import WorkspaceTests from '../components/workspace/WorkspaceTests.vue'
import WorkspaceDelivery from '../components/workspace/WorkspaceDelivery.vue'
import WorkspaceReports from '../components/workspace/WorkspaceReports.vue'
import WorkspaceTraceability from '../components/workspace/WorkspaceTraceability.vue'
import WorkspaceCode from '../components/workspace/WorkspaceCode.vue'
import WorkspaceSettings from '../components/workspace/WorkspaceSettings.vue'
import { useWorkspaceStore } from '../stores/workspaceStore'
import { useWorkspaceStream } from '../composables/useWorkspaceStream'
import { useToastStore } from '../stores/toastStore'

const route = useRoute()
const router = useRouter()
const store = useWorkspaceStore()
const toast = useToastStore()

const projectId = computed(() => String(route.params.id ?? ''))
useWorkspaceStream(projectId)

const components = {
  overview: WorkspaceOverview,
  planning: WorkspacePlanning,
  board: WorkspaceBoard,
  tests: WorkspaceTests,
  code: WorkspaceCode,
  delivery: WorkspaceDelivery,
  reports: WorkspaceReports,
  traceability: WorkspaceTraceability,
  settings: WorkspaceSettings,
}

const activeTab = computed(() => {
  const tab = String(route.query.tab ?? 'overview')
  return tab in components ? (tab as keyof typeof components) : 'overview'
})

const activeComponent = computed(() => components[activeTab.value])
const project = computed(() => store.project)

async function load(): Promise<void> {
  store.reset()
  await store.load(projectId.value)
}

async function refresh(): Promise<void> {
  await store.refresh()
  toast.success('Workspace refreshed from the source of truth.', 'Up to date')
}

onMounted(load)
watch(projectId, load)
</script>

<template>
  <div class="min-h-screen bg-slate-50">
    <header class="border-b border-slate-200 bg-white">
      <div class="mx-auto max-w-[1500px] px-4 py-5 sm:px-6 lg:px-8">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <RouterLink
                to="/projects"
                class="inline-flex items-center gap-1.5 text-xs font-medium text-slate-600 hover:text-slate-800"
              >
                <AppIcon name="arrowLeft" :size="13" />
                All projects
              </RouterLink>
              <span class="text-slate-300">/</span>
              <span class="font-mono text-xs font-semibold text-indigo-600">
                {{ project?.key_prefix || 'WORKSPACE' }}
              </span>
            </div>

            <div class="mt-2 flex flex-wrap items-center gap-3">
              <h1 class="truncate text-2xl font-semibold tracking-tight text-slate-900">
                {{ project?.name || (store.loading ? 'Loading workspace…' : 'Project workspace') }}
              </h1>
              <StatusPill v-if="project" :status="project.status" />
            </div>

            <p v-if="project?.description" class="mt-1 max-w-3xl text-sm leading-relaxed text-slate-600">
              {{ project.description }}
            </p>
          </div>

        </div>
      </div>
    </header>

    <main class="mx-auto max-w-[1500px] px-4 py-6 sm:px-6 lg:px-8">
      <div v-if="store.loading" class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div v-for="index in 4" :key="index" class="skeleton h-28 rounded-xl" />
        </div>
        <div class="skeleton h-72 rounded-xl" />
        <div class="grid gap-4 lg:grid-cols-3">
          <div v-for="index in 3" :key="index" class="skeleton h-52 rounded-xl" />
        </div>
      </div>

      <div
        v-else-if="store.error && !store.workspace"
        class="mx-auto mt-16 max-w-lg rounded-2xl border border-red-200 bg-white p-8 text-center shadow-sm"
      >
        <span class="mx-auto flex h-12 w-12 items-center justify-center rounded-xl bg-red-50 text-red-600">
          <AppIcon name="alert" :size="22" />
        </span>
        <h2 class="mt-4 text-lg font-semibold text-slate-900">Workspace unavailable</h2>
        <p class="mt-2 text-sm leading-relaxed text-slate-600">{{ store.error }}</p>
        <div class="mt-5 flex justify-center gap-2">
          <button type="button" class="btn-secondary" @click="router.push('/projects')">
            Back to projects
          </button>
          <button type="button" class="btn-primary" @click="load">Try again</button>
        </div>
      </div>

      <div v-else-if="store.workspace">
        <div
          v-if="store.error"
          class="mb-4 flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 p-3.5 text-sm text-amber-800"
        >
          <AppIcon name="alert" :size="17" class="mt-0.5 flex-none" />
          <p class="flex-1">{{ store.error }}</p>
          <button type="button" class="text-xs font-semibold underline" @click="refresh">Retry</button>
        </div>
        <component :is="activeComponent" />
      </div>
    </main>
  </div>
</template>
