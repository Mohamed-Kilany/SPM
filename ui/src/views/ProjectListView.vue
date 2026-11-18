<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import BaseModal from '../components/BaseModal.vue'
import AppIcon from '../components/AppIcon.vue'
import { api } from '../api/client'
import { useAuthStore } from '../stores/authStore'
import { useToastStore } from '../stores/toastStore'
import { formatDateTime, timeAgo } from '../lib/format'
import type { ProjectSummary } from '../types/project'

const router = useRouter()
const auth = useAuthStore()
const toast = useToastStore()

const projects = ref<ProjectSummary[]>([])
const loading = ref(true)
const loadError = ref('')

const showCreate = ref(false)
const projectName = ref('')
const projectDescription = ref('')
const creating = ref(false)
const createError = ref('')

const totalWorkItems = computed(() =>
  projects.value.reduce((sum, project) => sum + workItemCount(project), 0),
)
const activeProjects = computed(
  () =>
    projects.value.filter(
      (project) => workItemCount(project) > 0 || testCaseCount(project) > 0,
    ).length,
)

function workItemCount(project: ProjectSummary): number {
  return project.work_item_count ?? 0
}

function testCaseCount(project: ProjectSummary): number {
  return project.test_case_count ?? 0
}

function openBugCount(project: ProjectSummary): number {
  return project.open_bug_count ?? 0
}

function workItemLabel(count: number): string {
  return `${count} ${count === 1 ? 'work item' : 'work items'}`
}

function testCaseLabel(count: number): string {
  return `${count} ${count === 1 ? 'test case' : 'test cases'}`
}

function lastActivity(project: ProjectSummary): string {
  return project.last_activity_at ? timeAgo(project.last_activity_at) : 'No activity yet'
}

function lastActivityDate(project: ProjectSummary): string {
  return project.last_activity_at
    ? `Last activity ${formatDateTime(project.last_activity_at)}`
    : 'No activity yet'
}

function doneCount(project: ProjectSummary): number {
  return project.done_work_item_count ?? 0
}

function completion(project: ProjectSummary): number {
  const count = workItemCount(project)
  if (count === 0) return 0
  return Math.round((doneCount(project) / count) * 100)
}

function projectMonogram(project: ProjectSummary): string {
  const words = project.name.trim().split(/\s+/).filter(Boolean)
  if (words.length > 1) return `${words[0]![0] ?? ''}${words[1]![0] ?? ''}`.toUpperCase()
  return project.name.slice(0, 2).toUpperCase()
}

async function loadProjects(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    projects.value = await api.listProjects()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : 'Failed to load projects'
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  projectName.value = ''
  projectDescription.value = ''
  createError.value = ''
  showCreate.value = true
}

function closeCreate(): void {
  if (creating.value) return
  showCreate.value = false
  projectName.value = ''
  projectDescription.value = ''
  createError.value = ''
}

async function createProject(): Promise<void> {
  const name = projectName.value.trim()
  if (!name) {
    createError.value = 'A project name is required.'
    return
  }

  creating.value = true
  createError.value = ''
  try {
    const result = await api.createProject(name, projectDescription.value.trim())
    showCreate.value = false
    projectName.value = ''
    projectDescription.value = ''

    if (result.warnings?.length) {
      toast.warning(result.warnings.join(' '), 'Project created with warnings')
    } else {
      toast.success(`${result.name} is ready.`, 'Project created')
    }
    await router.push({ name: 'workspace', params: { id: result.id } })
  } catch (err) {
    createError.value = err instanceof Error ? err.message : 'Failed to create project'
  } finally {
    creating.value = false
  }
}

onMounted(loadProjects)
</script>

<template>
  <div class="min-h-screen">
    <div class="mx-auto max-w-7xl px-5 py-8 sm:px-8 lg:py-10">
      <header class="flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p class="flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">
            <span class="h-px w-5 bg-indigo-400" />
            Workspace
          </p>
          <h1 class="mt-2 text-3xl font-semibold tracking-tight text-slate-900">Projects</h1>
        </div>

        <button v-if="auth.canCreateProjects" type="button" class="btn-primary self-start sm:self-auto" @click="openCreate">
          <AppIcon name="plus" :size="16" />
          New project
        </button>
      </header>

      <div class="mt-8 grid grid-cols-2 gap-3 sm:flex sm:flex-wrap">
        <div class="card px-4 py-3">
          <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-600">Projects</p>
          <p class="mt-1 text-xl font-semibold text-slate-900">{{ projects.length }}</p>
        </div>
        <div class="card px-4 py-3">
          <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-600">Work items</p>
          <p class="mt-1 text-xl font-semibold text-slate-900">{{ totalWorkItems }}</p>
        </div>
        <div class="card px-4 py-3">
          <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-600">Active workspaces</p>
          <p class="mt-1 flex items-center gap-2 text-xl font-semibold text-slate-900">
            {{ activeProjects }}
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
          </p>
        </div>
      </div>

      <div v-if="loadError" class="mt-8 flex flex-col gap-3 rounded-xl border border-red-200 bg-red-50 px-4 py-3 sm:flex-row sm:items-center" role="alert">
        <span class="flex items-center gap-2 text-sm text-red-700">
          <AppIcon name="alert" :size="16" />
          {{ loadError }}
        </span>
        <button type="button" class="btn-secondary self-start border-red-200 text-red-700 hover:bg-red-50 sm:ml-auto" @click="loadProjects">
          Retry
        </button>
      </div>

      <div v-if="loading" class="mt-6 grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <div v-for="index in 6" :key="index" class="card p-5">
          <div class="flex items-center justify-between">
            <div class="skeleton h-10 w-10 rounded-xl" />
            <div class="skeleton h-5 w-14 rounded-full" />
          </div>
          <div class="skeleton mt-4 h-5 w-3/4 rounded" />
          <div class="skeleton mt-2 h-3 w-full rounded" />
          <div class="skeleton mt-1.5 h-3 w-2/3 rounded" />
          <div class="mt-6 flex gap-3">
            <div class="skeleton h-4 w-20 rounded" />
            <div class="skeleton h-4 w-24 rounded" />
          </div>
          <div class="skeleton mt-4 h-1.5 w-full rounded-full" />
        </div>
      </div>

      <div v-else-if="projects.length > 0" class="mt-6 grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <RouterLink
          v-for="project in projects"
          :key="project.id"
          :to="{ name: 'workspace', params: { id: project.id } }"
          class="card group relative flex flex-col overflow-hidden p-5 transition hover:-translate-y-0.5 hover:border-indigo-200 hover:shadow-md"
        >
          <div class="flex items-start justify-between gap-3">
            <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-indigo-50 text-xs font-bold tracking-wide text-indigo-700 ring-1 ring-inset ring-indigo-100">
              {{ projectMonogram(project) }}
            </span>
          </div>

          <h2 class="mt-4 truncate text-base font-semibold tracking-tight text-slate-900 group-hover:text-indigo-700">
            {{ project.name }}
          </h2>
          <p class="mt-1.5 line-clamp-2 min-h-[2.35rem] text-[11.5px] leading-relaxed text-slate-600">
            {{ project.description || 'No description yet. Open the workspace to start planning.' }}
          </p>

          <div class="mt-5 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[11px]">
            <span class="flex items-center gap-1.5 text-slate-600">
              <AppIcon name="layers" :size="13" class="text-slate-600" />
              {{ workItemLabel(workItemCount(project)) }}
            </span>
            <span class="flex items-center gap-1.5 text-slate-600">
              <AppIcon name="flask" :size="13" class="text-slate-600" />
              {{ testCaseLabel(testCaseCount(project)) }}
            </span>
            <span class="flex items-center gap-1.5 text-slate-600" :title="lastActivityDate(project)">
              <AppIcon name="clock" :size="13" class="text-slate-600" />
              {{ lastActivity(project) }}
            </span>
          </div>
          <div v-if="openBugCount(project) > 0" class="mt-2">
            <span class="badge bg-rose-50 text-rose-700 ring-rose-200">
              {{ openBugCount(project) }} open {{ openBugCount(project) === 1 ? 'bug' : 'bugs' }}
            </span>
          </div>

          <div class="mt-4">
            <div class="flex items-center justify-between text-[9px] font-medium uppercase tracking-wider text-slate-600">
              <span>Completion</span>
              <span>{{ completion(project) }}%</span>
            </div>
            <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-slate-100">
              <div class="h-full rounded-full bg-indigo-500 transition-all duration-300" :style="{ width: `${completion(project)}%` }" />
            </div>
          </div>

          <div class="mt-4 flex items-center gap-2 border-t border-slate-100 pt-3">
            <AppIcon name="repo" :size="12" class="text-slate-600" />
            <span class="min-w-0 flex-1 truncate font-mono text-[10px] text-slate-600">
              {{ project.repo_name || 'repository pending' }}
            </span>
            <span class="flex h-6 w-6 items-center justify-center rounded-lg border border-slate-200 text-slate-600 transition group-hover:border-indigo-200 group-hover:bg-indigo-50 group-hover:text-indigo-600">
              <AppIcon name="arrowRight" :size="13" />
            </span>
          </div>
        </RouterLink>
      </div>

      <div v-else-if="!loadError" class="mt-8 flex flex-col items-center justify-center rounded-2xl border border-dashed border-slate-300 bg-white px-6 py-20 text-center">
        <span class="flex h-14 w-14 items-center justify-center rounded-2xl bg-indigo-50 text-indigo-600 ring-1 ring-inset ring-indigo-100">
          <AppIcon name="projects" :size="25" />
        </span>
        <h2 class="mt-5 text-lg font-semibold text-slate-900">No projects yet</h2>
        <button v-if="auth.canCreateProjects" type="button" class="btn-primary mt-5" @click="openCreate">
          <AppIcon name="plus" :size="15" />
          Create project
        </button>
      </div>
    </div>

    <BaseModal
      :model-value="showCreate"
      title="New project"

      max-width="md"
      :persistent="creating"
      @update:model-value="(value) => { if (!value) closeCreate() }"
    >
      <form class="space-y-5" @submit.prevent="createProject">
        <label class="block">
          <span class="field-label">Project name</span>
          <input v-model="projectName" data-autofocus type="text" maxlength="255" :disabled="creating" class="field-input" placeholder="e.g. Atlas Commerce" @input="createError = ''" />
        </label>

        <label class="block">
          <span class="field-label">Description <span class="font-normal normal-case tracking-normal text-slate-600">optional</span></span>
          <textarea v-model="projectDescription" rows="4" maxlength="4000" :disabled="creating" class="field-input resize-none" placeholder="What is this project delivering?" />
        </label>

        <p v-if="createError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
          {{ createError }}
        </p>

        <div class="flex items-center justify-end gap-3 pt-1">
          <button type="button" class="btn-secondary" :disabled="creating" @click="closeCreate">Cancel</button>
          <button type="submit" class="btn-primary" :disabled="creating || !projectName.trim()">
            <AppIcon v-if="creating" name="loader" :size="15" class="animate-spin" />
            {{ creating ? 'Provisioning…' : 'Create project' }}
          </button>
        </div>
      </form>
    </BaseModal>
  </div>
</template>
