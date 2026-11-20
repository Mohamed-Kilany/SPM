<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '../AppIcon.vue'
import BaseModal from '../BaseModal.vue'
import EmptyState from './EmptyState.vue'
import StatusPill from './StatusPill.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useAuthStore } from '../../stores/authStore'
import { useToastStore } from '../../stores/toastStore'
import { api } from '../../api/client'
import { formatDateTime, shortId, timeAgo } from '../../lib/format'
import type {
  GitBranch,
  PullRequest,
  PullRequestFile,
  RepositoryBranch,
  RepositoryCommit,
  RepositoryFile,
  RepositoryTreeEntry,
  RepositoryTreeResponse,
} from '../../types/workspace'

const store = useWorkspaceStore()
const auth = useAuthStore()
const toast = useToastStore()
const router = useRouter()
const route = useRoute()

const canWriteCode = computed(() => auth.can(store.projectId, 'code.write'))
const canMerge = computed(() => auth.can(store.projectId, 'pr.merge'))

type CodeView = 'source' | 'commits' | 'branches' | 'pull-requests'
const activeView = ref<CodeView>('source')

const selectedRepoId = ref('')
const selectedRef = ref('')
const currentPath = ref('')

const tree = ref<RepositoryTreeResponse | null>(null)
const treeLoading = ref(false)
const treeError = ref('')

const selectedFile = ref<RepositoryFile | null>(null)
const fileLoading = ref(false)
const fileError = ref('')

const liveBranches = ref<RepositoryBranch[]>([])
const liveCommits = ref<RepositoryCommit[]>([])
const liveLoading = ref(false)
const liveError = ref('')

const provisionBusy = ref(false)

const prFilesOpen = ref(false)
const prFilesLoading = ref(false)
const prFilesError = ref('')
const prFilesTarget = ref<PullRequest | null>(null)
const prFiles = ref<PullRequestFile[]>([])

const branchModal = ref(false)
const branchBusy = ref(false)
const branchError = ref('')
const branchForm = reactive({ repository_id: '', work_item_id: '', name: '' })

const prModal = ref(false)
const prBusy = ref(false)
const prError = ref('')
const prForm = reactive({
  repository_id: '',
  work_item_id: '',
  title: '',
  description: '',
  source_branch_id: '',
  target_branch: 'main',
})

const mergeOpen = ref(false)
const mergeBusy = ref(false)
const mergeTarget = ref<PullRequest | null>(null)
const mergeSha = ref('')

const featureFilter = computed(() => String(route.query.feature ?? ''))

const repositories = computed(() => store.repositories)
const selectedRepository = computed(
  () => repositories.value.find((repo) => repo.id === selectedRepoId.value) ?? repositories.value[0] ?? null,
)
const effectiveRef = computed(
  () => selectedRef.value || selectedRepository.value?.default_branch || 'main',
)

const branchOptions = computed(() => {
  const names = new Set<string>()
  const repository = selectedRepository.value
  if (repository?.default_branch) names.add(repository.default_branch)
  for (const branch of liveBranches.value) names.add(branch.name)
  for (const branch of store.branches) {
    if (!repository || branch.repository_id === repository.id) names.add(branch.name)
  }
  return [...names].sort((a, b) => a.localeCompare(b))
})

const sortedTreeEntries = computed(() =>
  [...(tree.value?.entries ?? [])].sort((a, b) => {
    if (a.type === b.type) return a.name.localeCompare(b.name)
    return a.type === 'dir' ? -1 : 1
  }),
)

const breadcrumbs = computed(() => {
  const parts = currentPath.value.split('/').filter(Boolean)
  return parts.map((part, index) => ({
    name: part,
    path: parts.slice(0, index + 1).join('/'),
  }))
})

const fileLines = computed(() => {
  const content = selectedFile.value?.content ?? ''
  return content ? content.split(/\r?\n/) : []
})

const workItems = computed(() => store.workItems)
const workItemByID = computed(() => {
  const map = new Map<string, (typeof workItems.value)[number]>()
  for (const item of workItems.value) map.set(item.id, item)
  return map
})

const pullRequests = computed(() =>
  [...store.pullRequests].sort((a, b) => b.number - a.number),
)
const visiblePullRequests = computed(() =>
  pullRequests.value.filter(
    (pr) =>
      (!selectedRepository.value || pr.repository_id === selectedRepository.value.id) &&
      matchesFeature(pr.work_item_id),
  ),
)
const openPullRequests = computed(
  () =>
    pullRequests.value.filter(
      (pr) =>
        (!selectedRepository.value || pr.repository_id === selectedRepository.value.id) &&
        pr.status === 'open',
    ).length,
)
const availablePrBranches = computed(() =>
  store.branches.filter(
    (branch) =>
      branch.repository_id === prForm.repository_id &&
      (!prForm.work_item_id || branch.work_item_id === prForm.work_item_id),
  ),
)

const views = computed<Array<{ key: CodeView; label: string; count: number }>>(() => [
  { key: 'source', label: 'Source', count: sortedTreeEntries.value.length },
  { key: 'commits', label: 'Commits', count: liveCommits.value.length },
  { key: 'branches', label: 'Branches', count: branchOptions.value.length },
  { key: 'pull-requests', label: 'Pull requests', count: openPullRequests.value },
])

function matchesFeature(workItemId: string | null): boolean {
  if (!featureFilter.value) return true
  if (!workItemId) return false
  return workItemByID.value.get(workItemId)?.feature_id === featureFilter.value
}

function linkedItem(id: string | null): string {
  if (!id) return ''
  return workItemByID.value.get(id)?.key ?? ''
}

function linkedBranch(name: string): GitBranch | undefined {
  return store.branches.find(
    (branch) => branch.repository_id === selectedRepository.value?.id && branch.name === name,
  )
}

function branchWorkItem(name: string): string | null {
  return linkedBranch(name)?.work_item_id ?? null
}

function branchLink(name: string): string {
  const id = branchWorkItem(name)
  return id ? linkedItem(id) : ''
}

function openWorkItem(id: string | null): void {
  if (!id) return
  void router.push({
    name: 'workspace',
    params: { id: store.projectId },
    query: { tab: 'board', item: id },
  })
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${unit === 0 ? Math.round(size) : size.toFixed(1)} ${units[unit]}`
}

function ensureRepositorySelected(): void {
  if (!repositories.value.length) return
  if (!selectedRepoId.value || !repositories.value.some((repo) => repo.id === selectedRepoId.value)) {
    selectedRepoId.value = repositories.value[0].id
  }
  if (!selectedRef.value) {
    selectedRef.value = selectedRepository.value?.default_branch || 'main'
  }
}

async function loadTree(): Promise<void> {
  const repository = selectedRepository.value
  if (!repository) return
  treeLoading.value = true
  treeError.value = ''
  try {
    tree.value = await api.getRepositoryTree(
      store.projectId,
      repository.id,
      effectiveRef.value,
      currentPath.value,
    )
  } catch (error) {
    tree.value = null
    treeError.value = error instanceof Error ? error.message : 'Failed to load repository'
  } finally {
    treeLoading.value = false
  }
}

async function loadLive(): Promise<void> {
  const repository = selectedRepository.value
  if (!repository) return
  liveLoading.value = true
  liveError.value = ''
  try {
    const [branchResponse, commitResponse] = await Promise.all([
      api.getRepositoryBranches(store.projectId, repository.id),
      api.getRepositoryCommits(store.projectId, repository.id, effectiveRef.value, 1, 50),
    ])
    liveBranches.value = branchResponse.branches
    liveCommits.value = commitResponse.commits
  } catch (error) {
    liveBranches.value = []
    liveCommits.value = []
    liveError.value = error instanceof Error ? error.message : 'Failed to load branches and commits'
  } finally {
    liveLoading.value = false
  }
}

async function loadRepository(): Promise<void> {
  if (!selectedRepository.value) return
  await Promise.all([loadTree(), loadLive()])
}

async function openFile(path: string): Promise<void> {
  const repository = selectedRepository.value
  if (!repository || !path) return
  fileLoading.value = true
  fileError.value = ''
  selectedFile.value = null
  try {
    selectedFile.value = await api.getRepositoryFile(
      store.projectId,
      repository.id,
      effectiveRef.value,
      path,
    )
  } catch (error) {
    fileError.value = error instanceof Error ? error.message : 'Failed to load file'
  } finally {
    fileLoading.value = false
  }
}

function openEntry(entry: RepositoryTreeEntry): void {
  if (entry.type === 'dir') {
    currentPath.value = entry.path
    selectedFile.value = null
    fileError.value = ''
    void loadTree()
    return
  }
  void openFile(entry.path)
}

function goToPath(path: string): void {
  currentPath.value = path
  selectedFile.value = null
  fileError.value = ''
  void loadTree()
}

function onRepositoryChange(): void {
  selectedRef.value = selectedRepository.value?.default_branch || 'main'
  currentPath.value = ''
  selectedFile.value = null
  fileError.value = ''
  treeError.value = ''
  void loadRepository()
}

async function changeRef(): Promise<void> {
  currentPath.value = ''
  selectedFile.value = null
  fileError.value = ''
  await loadRepository()
}

async function selectRef(name: string): Promise<void> {
  selectedRef.value = name
  activeView.value = 'source'
  await changeRef()
}

async function refreshRepository(): Promise<void> {
  try {
    await store.refresh()
    await loadRepository()
    toast.success('Repository refreshed.', 'Source control')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to refresh repository', 'Source control')
  }
}

async function repairRepository(): Promise<void> {
  if (!canWriteCode.value) return
  const repository = selectedRepository.value
  if (!repository || provisionBusy.value) return
  provisionBusy.value = true
  try {
    await api.provisionRepository(store.projectId, repository.id)
    await store.refresh()
    await loadRepository()
    toast.success('Repository and webhook repaired.', 'Source control')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to repair repository', 'Source control')
  } finally {
    provisionBusy.value = false
  }
}

async function openPrFiles(pr: PullRequest): Promise<void> {
  prFilesTarget.value = pr
  prFilesOpen.value = true
  prFiles.value = []
  prFilesError.value = ''
  prFilesLoading.value = true
  try {
    const result = await api.getPullRequestFiles(store.projectId, pr.id)
    prFiles.value = result.files
  } catch (error) {
    prFilesError.value = error instanceof Error ? error.message : 'Failed to load changed files'
  } finally {
    prFilesLoading.value = false
  }
}

function openBranchModal(): void {
  if (!canWriteCode.value) return
  const item = workItems.value[0]
  Object.assign(branchForm, {
    repository_id: selectedRepository.value?.id ?? repositories.value[0]?.id ?? '',
    work_item_id: item?.id ?? '',
    name: item ? `feature/${item.key.toLowerCase()}` : '',
  })
  branchError.value = ''
  branchModal.value = true
}

async function saveBranch(): Promise<void> {
  if (!canWriteCode.value) return
  if (!branchForm.repository_id || !branchForm.work_item_id || !branchForm.name.trim()) {
    branchError.value = 'Repository, work item and branch name are required.'
    return
  }
  branchBusy.value = true
  branchError.value = ''
  try {
    await api.createBranch(store.projectId, {
      repository_id: branchForm.repository_id,
      work_item_id: branchForm.work_item_id,
      name: branchForm.name.trim(),
      head_commit_sha: '',
      created_by: null,
    })
    branchModal.value = false
    activeView.value = 'branches'
    await store.refresh()
    await loadLive()
    toast.success('Branch created in the internal Git engine and linked to its work item.', 'Source control')
  } catch (error) {
    branchError.value = error instanceof Error ? error.message : 'Failed to create branch'
  } finally {
    branchBusy.value = false
  }
}

function openPrModal(): void {
  if (!canWriteCode.value) return
  const item = workItems.value[0]
  const repositoryId = selectedRepository.value?.id ?? repositories.value[0]?.id ?? ''
  const branch = store.branches.find(
    (entry) => entry.repository_id === repositoryId && entry.work_item_id === item?.id,
  )
  Object.assign(prForm, {
    repository_id: repositoryId,
    work_item_id: item?.id ?? '',
    title: item?.title ?? '',
    description: '',
    source_branch_id: branch?.id ?? '',
    target_branch: selectedRepository.value?.default_branch ?? 'main',
  })
  prError.value = ''
  prModal.value = true
}

async function savePr(): Promise<void> {
  if (!canWriteCode.value) return
  if (!prForm.repository_id || !prForm.work_item_id || !prForm.title.trim()) {
    prError.value = 'Repository, work item and title are required.'
    return
  }
  if (!prForm.source_branch_id) {
    prError.value = 'Select the linked branch that contains the change.'
    return
  }
  prBusy.value = true
  prError.value = ''
  try {
    const created = await api.createPullRequest(store.projectId, {
      repository_id: prForm.repository_id,
      work_item_id: prForm.work_item_id,
      number: 0,
      title: prForm.title.trim(),
      description: prForm.description,
      source_branch_id: prForm.source_branch_id,
      target_branch: prForm.target_branch.trim() || 'main',
      author: '',
      status: 'open',
      review_status: 'pending',
      merge_commit_sha: '',
    })
    prModal.value = false
    selectedRepoId.value = prForm.repository_id
    selectedRef.value = prForm.target_branch.trim() || selectedRepository.value?.default_branch || 'main'
    activeView.value = 'pull-requests'
    await store.refresh()
    await loadLive()
    toast.success(`Pull request #${created.number} opened.`, 'Source control')
  } catch (error) {
    prError.value = error instanceof Error ? error.message : 'Failed to open pull request'
  } finally {
    prBusy.value = false
  }
}

function openMerge(pr: PullRequest): void {
  if (!canMerge.value) return
  mergeTarget.value = pr
  mergeSha.value = ''
  mergeOpen.value = true
}

async function confirmMerge(): Promise<void> {
  if (!canMerge.value) return
  const pr = mergeTarget.value
  if (!pr || mergeBusy.value) return
  mergeBusy.value = true
  try {
    await api.mergePullRequest(store.projectId, pr.id, mergeSha.value.trim())
    mergeOpen.value = false
    await store.refresh()
    await loadLive()
    toast.success(`PR #${pr.number} merged and recorded.`, 'Merge complete')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Merge failed', 'Source control')
  } finally {
    mergeBusy.value = false
  }
}

function applyDeepLink(): void {
  const repoId = String(route.query.repo ?? '')
  if (repoId && repositories.value.some((repo) => repo.id === repoId)) {
    selectedRepoId.value = repoId
  }
  const ref = String(route.query.ref ?? '')
  if (ref) selectedRef.value = ref

  if (route.query.pr) activeView.value = 'pull-requests'
  else if (route.query.branch) activeView.value = 'branches'
  else if (route.query.commit) activeView.value = 'commits'
  else if (route.query.file) activeView.value = 'source'

  const file = String(route.query.file ?? '')
  if (file) {
    currentPath.value = file.includes('/') ? file.slice(0, file.lastIndexOf('/')) : ''
    void nextTick(() => openFile(file))
  }

  const target = String(route.query.pr ?? route.query.branch ?? route.query.commit ?? '')
  if (!target) return
  const elementId = route.query.pr
    ? `code-pr-${target}`
    : route.query.branch
      ? `code-branch-${store.branches.find((branch) => branch.id === target)?.name ?? target}`
      : `code-commit-${target}`
  void nextTick(() => {
    const element = document.getElementById(elementId)
    if (!element) return
    element.scrollIntoView({ behavior: 'smooth', block: 'center' })
    element.classList.add('bg-indigo-50/60')
    window.setTimeout(() => element.classList.remove('bg-indigo-50/60'), 2400)
  })
}

onMounted(() => {
  applyDeepLink()
  ensureRepositorySelected()
  void loadRepository()
})

watch(
  repositories,
  () => {
    const hadRepository = Boolean(selectedRepoId.value)
    ensureRepositorySelected()
    if (!hadRepository) void loadRepository()
  },
)

watch(
  () => route.query,
  () => {
    applyDeepLink()
    void loadRepository()
  },
  { deep: true },
)
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">Source control</p>
        <h2 class="mt-2 text-xl font-semibold tracking-tight text-slate-900">Code</h2>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn-secondary" :disabled="!selectedRepository || treeLoading" @click="refreshRepository">
          <AppIcon name="refresh" :size="15" :class="{ 'animate-spin': treeLoading }" />
          Refresh
        </button>
        <button v-if="selectedRepository && canWriteCode" type="button" class="btn-secondary" :disabled="provisionBusy" @click="repairRepository">
          <AppIcon v-if="provisionBusy" name="loader" :size="15" class="animate-spin" />
          <AppIcon v-else name="shield" :size="15" />
          Repair
        </button>
        <button v-if="canWriteCode" type="button" class="btn-secondary" :disabled="!repositories.length || !workItems.length" @click="openBranchModal">
          <AppIcon name="branch" :size="15" />
          New branch
        </button>
        <button v-if="canWriteCode" type="button" class="btn-primary" :disabled="!repositories.length || !workItems.length" @click="openPrModal">
          <AppIcon name="pullRequest" :size="15" />
          Open pull request
        </button>
      </div>
    </header>

    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Repositories</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ repositories.length }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Branches</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ branchOptions.length }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Commits</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ liveCommits.length }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Open pull requests</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ openPullRequests }}</p>
      </div>
    </div>

    <div v-if="!repositories.length" class="card p-6">
      <EmptyState title="No repository linked" />
    </div>

    <template v-else>
      <section class="card p-4 sm:p-5">
        <div class="flex flex-wrap items-center gap-3">
          <span class="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-slate-100 text-slate-700">
            <AppIcon name="repo" :size="19" />
          </span>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <select v-model="selectedRepoId" class="field-input w-auto min-w-52 py-1.5 text-sm font-semibold" @change="onRepositoryChange">
                <option v-for="repo in repositories" :key="repo.id" :value="repo.id">{{ repo.name }}</option>
              </select>
              <span class="badge bg-emerald-50 text-emerald-700 ring-emerald-200">Internal Git</span>
            </div>
            <p class="mt-1 text-xs text-slate-500">
              {{ selectedRepository?.provider === 'gitea' ? 'Gitea engine' : selectedRepository?.provider }}
              · default branch <span class="font-mono">{{ selectedRepository?.default_branch }}</span>
              <span v-if="selectedRepository?.webhook_url"> · webhook registered</span>
            </p>
          </div>
          <label class="flex items-center gap-2 text-xs text-slate-600">
            <span class="font-medium">Ref</span>
            <select v-model="selectedRef" class="field-input w-52 py-1.5 font-mono text-xs" @change="changeRef">
              <option v-for="name in branchOptions" :key="name" :value="name">{{ name }}</option>
            </select>
          </label>
        </div>
      </section>

      <section class="card overflow-hidden">
        <div class="flex gap-1 overflow-x-auto border-b border-slate-200 px-4">
          <button
            v-for="view in views"
            :key="view.key"
            type="button"
            class="flex shrink-0 items-center gap-2 border-b-2 px-3 py-3 text-sm font-medium"
            :class="
              activeView === view.key
                ? 'border-indigo-600 text-indigo-700'
                : 'border-transparent text-slate-600 hover:border-slate-300 hover:text-slate-800'
            "
            @click="activeView = view.key"
          >
            {{ view.label }}
            <span class="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold text-slate-600">{{ view.count }}</span>
          </button>
        </div>

        <div v-if="activeView === 'source'">
          <div class="flex flex-wrap items-center gap-2 border-b border-slate-100 px-4 py-3 sm:px-6">
            <button type="button" class="rounded-md px-1.5 py-0.5 text-xs font-medium text-slate-600 hover:bg-slate-100" @click="goToPath('')">
              root
            </button>
            <template v-for="crumb in breadcrumbs" :key="crumb.path">
              <AppIcon name="chevronRight" :size="12" class="text-slate-400" />
              <button type="button" class="rounded-md px-1.5 py-0.5 text-xs font-medium text-slate-600 hover:bg-slate-100" @click="goToPath(crumb.path)">
                {{ crumb.name }}
              </button>
            </template>
            <span class="ml-auto font-mono text-[11px] text-slate-500">{{ effectiveRef }}</span>
          </div>

          <div class="grid gap-0 lg:grid-cols-[minmax(220px,320px)_1fr]">
            <div class="border-b border-slate-200 lg:border-b-0 lg:border-r">
              <div v-if="treeLoading" class="flex items-center gap-2 p-6 text-sm text-slate-500">
                <AppIcon name="loader" :size="16" class="animate-spin" />
                Loading tree…
              </div>
              <div v-else-if="treeError" class="p-4 sm:p-6">
                <div class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
                  <p class="font-semibold">Repository unavailable</p>
                  <p class="mt-1 text-xs leading-relaxed">{{ treeError }}</p>
                  <button v-if="canWriteCode" type="button" class="btn-secondary mt-3 py-1.5 text-xs" :disabled="provisionBusy" @click="repairRepository">
                    <AppIcon v-if="provisionBusy" name="loader" :size="13" class="animate-spin" />
                    <AppIcon v-else name="shield" :size="13" />
                    Repair repository
                  </button>
                </div>
              </div>
              <div v-else-if="!sortedTreeEntries.length" class="p-4 sm:p-6">
                <EmptyState compact title="Empty repository" />
              </div>
              <ul v-else class="divide-y divide-slate-100">
                <li v-for="entry in sortedTreeEntries" :key="entry.path">
                  <button
                    type="button"
                    class="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-slate-50"
                    @click="openEntry(entry)"
                  >
                    <AppIcon :name="entry.type === 'dir' ? 'folder' : 'file'" :size="16" class="flex-none text-slate-500" />
                    <span
                      class="min-w-0 flex-1 truncate font-mono text-xs"
                      :class="entry.type === 'dir' ? 'font-semibold text-slate-900' : 'text-slate-700'"
                    >
                      {{ entry.name }}
                    </span>
                    <span v-if="entry.type !== 'dir'" class="flex-none text-[10px] text-slate-400">{{ formatBytes(entry.size) }}</span>
                  </button>
                </li>
              </ul>
            </div>

            <div class="min-w-0">
              <div v-if="fileLoading" class="flex items-center gap-2 p-6 text-sm text-slate-500">
                <AppIcon name="loader" :size="16" class="animate-spin" />
                Loading file…
              </div>
              <div v-else-if="fileError" class="p-4 sm:p-6">
                <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800">{{ fileError }}</div>
              </div>
              <div v-else-if="selectedFile" class="min-w-0">
                <div class="flex flex-wrap items-center gap-2 border-b border-slate-100 px-4 py-3 sm:px-6">
                  <AppIcon name="file" :size="15" class="flex-none text-slate-500" />
                  <span class="min-w-0 flex-1 truncate font-mono text-xs text-slate-800">{{ selectedFile.path }}</span>
                  <span class="text-[10px] text-slate-400">{{ formatBytes(selectedFile.size) }}</span>
                  <span v-if="selectedFile.binary" class="badge bg-slate-100 text-slate-600 ring-slate-200">Binary</span>
                  <span v-if="selectedFile.truncated" class="badge bg-amber-50 text-amber-700 ring-amber-200">Truncated</span>
                </div>
                <div v-if="selectedFile.binary" class="p-6 text-sm text-slate-500">
                  This file is binary and cannot be displayed in the browser.
                </div>
                <div v-else class="max-h-[70vh] overflow-auto">
                  <div class="flex min-w-max">
                    <div class="select-none border-r border-slate-100 bg-slate-50 px-3 py-4 text-right font-mono text-[11px] leading-6 text-slate-400">
                      <div v-for="n in fileLines.length" :key="n">{{ n }}</div>
                    </div>
                    <pre class="flex-1 px-4 py-4 font-mono text-[12px] leading-6 text-slate-800"><code>{{ selectedFile.content }}</code></pre>
                  </div>
                </div>
              </div>
              <div v-else class="flex min-h-[280px] flex-col items-center justify-center p-8 text-center">
                <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-slate-100 text-slate-500">
                  <AppIcon name="code" :size="22" />
                </span>
                <p class="mt-3 text-sm font-semibold text-slate-900">Browse the repository</p>
              </div>
            </div>
          </div>
        </div>

        <div v-else-if="activeView === 'commits'">
          <div v-if="liveLoading" class="flex items-center gap-2 p-6 text-sm text-slate-500">
            <AppIcon name="loader" :size="16" class="animate-spin" />
            Loading commits…
          </div>
          <div v-else-if="liveError" class="p-6">
            <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800">{{ liveError }}</div>
          </div>
          <div v-else-if="liveCommits.length" class="divide-y divide-slate-100">
            <div
              v-for="commit in liveCommits"
              :id="`code-commit-${commit.sha}`"
              :key="commit.sha"
              class="flex scroll-mt-24 items-start gap-3 px-4 py-4 transition-colors sm:px-6"
            >
              <span class="mt-0.5 flex h-8 w-8 flex-none items-center justify-center rounded-full bg-slate-100 font-mono text-[10px] font-semibold text-slate-600">
                {{ shortId(commit.sha, 2) }}
              </span>
              <div class="min-w-0 flex-1">
                <p class="text-sm font-medium text-slate-900">{{ commit.message }}</p>
                <p class="mt-1 text-[11px] text-slate-500">
                  <span class="font-mono">{{ shortId(commit.sha, 10) }}</span>
                  · {{ commit.author_name || commit.author_email }}
                  · {{ timeAgo(commit.committed_at) }}
                </p>
              </div>
            </div>
          </div>
          <div v-else class="p-6">
            <EmptyState title="No commits" />
          </div>
        </div>

        <div v-else-if="activeView === 'branches'">
          <div v-if="liveLoading" class="flex items-center gap-2 p-6 text-sm text-slate-500">
            <AppIcon name="loader" :size="16" class="animate-spin" />
            Loading branches…
          </div>
          <div v-else-if="liveError" class="p-6">
            <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800">{{ liveError }}</div>
          </div>
          <div v-else-if="liveBranches.length" class="divide-y divide-slate-100">
            <div
              v-for="branch in liveBranches"
              :id="`code-branch-${branch.name}`"
              :key="branch.name"
              class="flex scroll-mt-24 flex-wrap items-center gap-3 px-4 py-3.5 transition-colors sm:px-6"
            >
              <AppIcon name="branch" :size="17" class="flex-none text-slate-500" />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <p class="truncate font-mono text-sm font-medium text-slate-900">{{ branch.name }}</p>
                  <span v-if="branch.protected" class="badge bg-amber-50 text-amber-700 ring-amber-200">Protected</span>
                  <span v-if="branch.name === selectedRepository?.default_branch" class="badge bg-slate-100 text-slate-600 ring-slate-200">Default</span>
                </div>
                <p class="mt-0.5 text-[11px] text-slate-500">
                  {{ branch.commit_message || 'No commit message' }}
                  · <span class="font-mono">{{ shortId(branch.commit_sha, 10) }}</span>
                  · updated {{ timeAgo(branch.updated_at) }}
                </p>
              </div>
              <button
                v-if="branchWorkItem(branch.name)"
                type="button"
                class="badge bg-indigo-50 font-mono text-indigo-700 ring-indigo-200 hover:bg-indigo-100"
                @click="openWorkItem(branchWorkItem(branch.name))"
              >
                {{ branchLink(branch.name) }}
              </button>
              <button type="button" class="btn-secondary py-1.5 text-xs" @click="selectRef(branch.name)">
                Browse
              </button>
            </div>
          </div>
          <div v-else class="p-6">
            <EmptyState title="No branches" />
          </div>
        </div>

        <div v-else>
          <div v-if="visiblePullRequests.length" class="divide-y divide-slate-100">
            <div
              v-for="pr in visiblePullRequests"
              :id="`code-pr-${pr.id}`"
              :key="pr.id"
              class="flex scroll-mt-24 flex-wrap items-start gap-3 px-4 py-4 transition-colors sm:px-6"
            >
              <span class="mt-0.5 flex h-9 w-9 flex-none items-center justify-center rounded-full bg-violet-50 text-violet-700">
                <AppIcon name="pullRequest" :size="17" />
              </span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <p class="text-sm font-semibold text-slate-900">#{{ pr.number }} {{ pr.title }}</p>
                  <StatusPill :status="pr.status" />
                  <span v-if="pr.review_status" class="badge bg-slate-100 text-slate-600 ring-slate-200">Review {{ pr.review_status }}</span>
                </div>
                <p class="mt-1 text-xs text-slate-500">
                  {{ pr.author || 'Unknown author' }} wants to merge
                  <span class="font-mono">{{ pr.target_branch }}</span>
                  · {{ timeAgo(pr.created_at) }}
                </p>
                <div class="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
                  <button
                    v-if="pr.work_item_id"
                    type="button"
                    class="inline-flex items-center gap-1 rounded-md bg-indigo-50 px-2 py-1 font-mono font-semibold text-indigo-700 hover:bg-indigo-100"
                    @click="openWorkItem(pr.work_item_id)"
                  >
                    <AppIcon name="link" :size="11" />
                    {{ linkedItem(pr.work_item_id) }}
                  </button>
                  <span class="text-slate-500">{{ formatDateTime(pr.created_at) }}</span>
                </div>
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <button type="button" class="btn-secondary py-1.5 text-xs" @click="openPrFiles(pr)">
                  <AppIcon name="file" :size="12" />
                  Files
                </button>
                <button
                  v-if="pr.status === 'open' && canMerge"
                  type="button"
                  class="rounded-lg border border-indigo-200 bg-indigo-50 px-2.5 py-1.5 text-[11px] font-semibold text-indigo-700 hover:bg-indigo-100"
                  @click="openMerge(pr)"
                >
                  Merge PR
                </button>
              </div>
            </div>
          </div>
          <div v-else class="p-6">
            <EmptyState title="No pull requests" />
          </div>
        </div>
      </section>
    </template>

    <BaseModal
      v-model="branchModal"
      title="Create linked branch"
      max-width="md"
      :persistent="branchBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="code-branch-repo">Repository</label>
          <select id="code-branch-repo" v-model="branchForm.repository_id" class="field-input">
            <option v-for="repo in repositories" :key="repo.id" :value="repo.id">{{ repo.name }}</option>
          </select>
        </div>
        <div>
          <label class="field-label" for="code-branch-item">Work item</label>
          <select id="code-branch-item" v-model="branchForm.work_item_id" class="field-input">
            <option v-for="item in workItems" :key="item.id" :value="item.id">{{ item.key }} · {{ item.title }}</option>
          </select>
        </div>
        <div>
          <label class="field-label" for="code-branch-name">Branch name</label>
          <input id="code-branch-name" v-model="branchForm.name" data-autofocus class="field-input font-mono text-xs" placeholder="feature/spm-123-checkout-retry" />
        </div>
        <p v-if="branchError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ branchError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="branchBusy" @click="branchModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="branchBusy" @click="saveBranch">
          <AppIcon v-if="branchBusy" name="loader" :size="14" class="animate-spin" />
          {{ branchBusy ? 'Creating…' : 'Create branch' }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      v-model="prModal"
      title="Open pull request"
      max-width="lg"
      :persistent="prBusy"
    >
      <div class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="code-pr-repo">Repository</label>
            <select id="code-pr-repo" v-model="prForm.repository_id" class="field-input">
              <option v-for="repo in repositories" :key="repo.id" :value="repo.id">{{ repo.name }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="code-pr-item">Work item</label>
            <select id="code-pr-item" v-model="prForm.work_item_id" class="field-input">
              <option v-for="item in workItems" :key="item.id" :value="item.id">{{ item.key }} · {{ item.title }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="code-pr-title">Title</label>
          <input id="code-pr-title" v-model="prForm.title" data-autofocus class="field-input" />
        </div>
        <div>
          <label class="field-label" for="code-pr-description">Description</label>
          <textarea id="code-pr-description" v-model="prForm.description" rows="2" class="field-input" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="code-pr-source">Source branch</label>
            <select id="code-pr-source" v-model="prForm.source_branch_id" class="field-input">
              <option value="">Select a linked branch</option>
              <option v-for="branch in availablePrBranches" :key="branch.id" :value="branch.id">{{ branch.name }}</option>
            </select>
            <p v-if="!availablePrBranches.length" class="mt-1 text-[11px] text-amber-700">Create a branch for this work item first.</p>
          </div>
          <div>
            <label class="field-label" for="code-pr-target">Target branch</label>
            <input id="code-pr-target" v-model="prForm.target_branch" class="field-input" />
          </div>
        </div>
        <p v-if="prError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ prError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="prBusy" @click="prModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="prBusy" @click="savePr">
          <AppIcon v-if="prBusy" name="loader" :size="14" class="animate-spin" />
          {{ prBusy ? 'Opening…' : 'Open pull request' }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      v-model="mergeOpen"
      title="Merge pull request"
      :description="mergeTarget ? `#${mergeTarget.number} ${mergeTarget.title}` : ''"
      max-width="md"
      :persistent="mergeBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="code-merge-sha">Merge commit SHA (optional)</label>
          <input id="code-merge-sha" v-model="mergeSha" data-autofocus class="field-input font-mono text-xs" placeholder="Leave blank to use the branch head" />
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="mergeBusy" @click="mergeOpen = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="mergeBusy" @click="confirmMerge">
          <AppIcon v-if="mergeBusy" name="loader" :size="14" class="animate-spin" />
          {{ mergeBusy ? 'Merging…' : 'Merge pull request' }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      v-model="prFilesOpen"
      :title="prFilesTarget ? `#${prFilesTarget.number} ${prFilesTarget.title}` : 'Pull request files'"
      max-width="lg"
    >
      <div v-if="prFilesLoading" class="flex items-center gap-2 p-4 text-sm text-slate-500">
        <AppIcon name="loader" :size="16" class="animate-spin" />
        Loading changed files…
      </div>
      <div v-else-if="prFilesError" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800">
        {{ prFilesError }}
      </div>
      <div v-else-if="prFiles.length" class="space-y-3">
        <div v-for="file in prFiles" :key="file.filename" class="overflow-hidden rounded-xl border border-slate-200">
          <div class="flex flex-wrap items-center gap-2 bg-slate-50 px-3 py-2">
            <span class="badge bg-slate-100 text-slate-600 ring-slate-200">{{ file.status }}</span>
            <span class="min-w-0 flex-1 truncate font-mono text-xs text-slate-800">{{ file.filename }}</span>
            <span class="text-[11px] font-semibold text-emerald-700">+{{ file.additions }}</span>
            <span class="text-[11px] font-semibold text-red-700">-{{ file.deletions }}</span>
          </div>
          <pre v-if="file.patch" class="max-h-72 overflow-auto bg-slate-950 px-3 py-3 font-mono text-[11px] leading-5 text-slate-100"><code>{{ file.patch }}</code></pre>
          <p v-else class="px-3 py-3 text-xs text-slate-500">No textual diff available.</p>
        </div>
      </div>
      <div v-else class="p-4">
        <EmptyState compact title="No changed files" />
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" @click="prFilesOpen = false">Close</button>
      </template>
    </BaseModal>
  </div>
</template>
