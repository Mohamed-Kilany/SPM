<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import BaseModal from '../BaseModal.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import EmptyState from './EmptyState.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useAuthStore } from '../../stores/authStore'
import { useToastStore } from '../../stores/toastStore'
import { api } from '../../api/client'
import { dateInputValue } from '../../lib/workspace'
import { formatDateTime, shortId, timeAgo } from '../../lib/format'
import type { Build, Deployment, GitRepository, ProjectSettings, PullRequest, Release } from '../../types/workspace'

const store = useWorkspaceStore()
const auth = useAuthStore()
const toast = useToastStore()
const route = useRoute()

function applyDeepLink(): void {
  const target = String(route.query.build ?? '')
    || String(route.query.deployment ?? '')
    || String(route.query.pr ?? '')
  if (!target) return
  const prefix = route.query.build ? 'build' : route.query.deployment ? 'deployment' : 'pr'
  void nextTick(() => {
    const element = document.getElementById(`${prefix}-${target}`)
    if (!element) return
    element.scrollIntoView({ behavior: 'smooth', block: 'center' })
    element.classList.add('ring-2', 'ring-indigo-400', 'ring-offset-1')
    window.setTimeout(() => element.classList.remove('ring-2', 'ring-indigo-400', 'ring-offset-1'), 2400)
  })
}

onMounted(applyDeepLink)
watch(() => route.query, applyDeepLink)

const repositories = computed(() => store.repositories)
const branches = computed(() => store.branches)
const pullRequests = computed(() => store.pullRequests)
const builds = computed(() => store.builds)
const deployments = computed(() => store.deployments)
const environments = computed(() =>
  [...(store.workspace?.environments ?? [])].sort((a, b) => a.position - b.position),
)
const releases = computed(() => store.releases)
const workItems = computed(() => store.workItems)
const healthChecks = computed(() => store.workspace?.health_checks ?? [])

// Capability gate for every write action in the delivery room. Reads
// (builds, deployments, health) are projections and always visible.
const canManageSettings = computed(() => auth.can(store.projectId, 'settings.write'))
const canPlan = computed(() => auth.can(store.projectId, 'planning.write'))
const canWriteCode = computed(() => auth.can(store.projectId, 'code.write'))
const canMerge = computed(() => auth.can(store.projectId, 'pr.merge'))
const canDeploy = computed(() => auth.can(store.projectId, 'delivery.deploy'))

const openPrs = computed(() => pullRequests.value.filter((pr) => pr.status === 'open').length)
const successfulBuilds = computed(() => builds.value.filter((build) => build.status === 'success').length)
const healthyEnvironments = computed(
  () => environments.value.filter((environment) => latestDeployment(environment.id)?.health_status === 'healthy').length,
)

const latestDeploymentMap = computed(() => {
  const map = new Map<string, Deployment>()
  const sorted = [...deployments.value].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  )
  for (const deployment of sorted) {
    if (!map.has(deployment.environment_id)) map.set(deployment.environment_id, deployment)
  }
  return map
})

const latestHealthMap = computed(() => {
  const map = new Map<string, (typeof healthChecks.value)[number]>()
  const sorted = [...healthChecks.value].sort(
    (a, b) => new Date(b.checked_at).getTime() - new Date(a.checked_at).getTime(),
  )
  for (const check of sorted) {
    if (!map.has(check.deployment_id)) map.set(check.deployment_id, check)
  }
  return map
})

function latestDeployment(environmentId: string): Deployment | undefined {
  return latestDeploymentMap.value.get(environmentId)
}

function latestHealth(deploymentId: string) {
  return latestHealthMap.value.get(deploymentId)
}

function repositoryName(repositoryId: string | null): string {
  if (!repositoryId) return 'No repository'
  return repositories.value.find((repository) => repository.id === repositoryId)?.name ?? 'Repository'
}

function environmentName(environmentId: string): string {
  return environments.value.find((environment) => environment.id === environmentId)?.name ?? 'Environment'
}

function releaseLabel(releaseId: string | null): string {
  if (!releaseId) return 'No release'
  const release = releases.value.find((entry) => entry.id === releaseId)
  return release ? `${release.name} - ${release.version}` : 'Release'
}

function workItemKey(workItemId: string | null): string {
  if (!workItemId) return '-'
  return workItems.value.find((entry) => entry.id === workItemId)?.key ?? '-'
}

function branchName(branchId: string | null): string {
  if (!branchId) return '-'
  return branches.value.find((branch) => branch.id === branchId)?.name ?? 'Branch'
}

function buildTitle(build: Build): string {
  const workflow = String(build.metadata?.workflow ?? '').trim()
  if (workflow) return workflow
  return build.number ? `Run ${build.number}` : `Build ${shortId(build.commit_sha)}`
}

function buildRunLabel(build: Build): string {
  const runNumber = String(build.metadata?.run_number ?? build.number ?? '').trim()
  return runNumber ? `Run #${runNumber}` : shortId(build.commit_sha)
}

function deploymentSource(deployment: Deployment): string {
  return String(deployment.metadata?.observed_via ?? deployment.metadata?.source ?? 'argocd')
}

function deploymentHealthMessage(deployment: Deployment): string {
  return String(deployment.metadata?.health_message ?? deployment.metadata?.operation_message ?? '').trim()
}

// ---------------------------------------------------------------------------
// Delivery policy and inbound integration
// ---------------------------------------------------------------------------

const settingsModal = ref(false)
const settingsBusy = ref(false)
const settingsError = ref('')
const settingsForm = reactive<ProjectSettings>({
  merge_policy: {
    require_linked_work_item: true,
    require_approved_review: false,
    require_passing_checks: true,
  },
  auto_rollback: false,
  auto_deploy_on_build: false,
  auto_deploy_environment: 'staging',
  require_health_check: false,
})

function openSettings(): void {
  if (!canManageSettings.value) return
  const settings = store.project?.settings
  if (settings) {
    Object.assign(settingsForm, {
      merge_policy: { ...settings.merge_policy },
      auto_rollback: settings.auto_rollback,
      auto_deploy_on_build: settings.auto_deploy_on_build,
      auto_deploy_environment: settings.auto_deploy_environment || 'staging',
      require_health_check: settings.require_health_check,
    })
  }
  settingsError.value = ''
  settingsModal.value = true
}

async function saveSettings(): Promise<void> {
  if (!canManageSettings.value) return
  settingsBusy.value = true
  settingsError.value = ''
  try {
    await api.updateSettings(store.projectId, settingsForm)
    settingsModal.value = false
    await store.refresh()
    toast.success('Merge gates updated. CI and deployment state remain automatic.', 'Delivery policy')
  } catch (error) {
    settingsError.value = error instanceof Error ? error.message : 'Failed to update settings'
  } finally {
    settingsBusy.value = false
  }
}

const webhookRepo = ref<GitRepository | null>(null)
const webhookOpen = ref(false)
const webhookCopied = ref(false)

function showWebhook(repository: GitRepository): void {
  webhookRepo.value = repository
  webhookCopied.value = false
  webhookOpen.value = true
}

async function copyWebhook(): Promise<void> {
  if (!webhookRepo.value) return
  const value = `${webhookRepo.value.webhook_url}\nToken: ${webhookRepo.value.webhook_token}`
  try {
    await navigator.clipboard.writeText(value)
    webhookCopied.value = true
  } catch {
    webhookCopied.value = false
    toast.error('Clipboard access is unavailable. Select the values manually.', 'Webhook')
  }
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

const repoModal = ref(false)
const repoBusy = ref(false)
const repoError = ref('')
const repoForm = reactive({ provider: 'gitea', name: '', url: '', default_branch: 'main', external_id: '' })

function openRepo(): void {
  if (!canWriteCode.value) return
  Object.assign(repoForm, {
    provider: 'gitea',
    name: store.project?.repo_name ?? '',
    url: '',
    default_branch: store.project?.default_branch ?? 'main',
    external_id: '',
  })
  repoError.value = ''
  repoModal.value = true
}

async function saveRepo(): Promise<void> {
  if (!canWriteCode.value) return
  if (!repoForm.name.trim()) {
    repoError.value = 'A repository name is required.'
    return
  }
  repoBusy.value = true
  try {
    await api.createRepository(store.projectId, { ...repoForm, name: repoForm.name.trim() })
    repoModal.value = false
    await store.refresh()
    toast.success('Repository linked to the project.', 'Source control')
  } catch (error) {
    repoError.value = error instanceof Error ? error.message : 'Failed to link repository'
  } finally {
    repoBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Branch
// ---------------------------------------------------------------------------

const branchModal = ref(false)
const branchBusy = ref(false)
const branchError = ref('')
const branchForm = reactive({ repository_id: '', work_item_id: '', name: '' })

function openBranch(): void {
  if (!canWriteCode.value) return
  Object.assign(branchForm, {
    repository_id: repositories.value[0]?.id ?? '',
    work_item_id: workItems.value[0]?.id ?? '',
    name: '',
  })
  branchError.value = ''
  branchModal.value = true
}

async function saveBranch(): Promise<void> {
  if (!canWriteCode.value) return
  if (!branchForm.repository_id) {
    branchError.value = 'Link a repository first.'
    return
  }
  if (!branchForm.work_item_id) {
    branchError.value = 'A branch must link to a work item.'
    return
  }
  if (!branchForm.name.trim()) {
    branchError.value = 'A branch name is required.'
    return
  }
  branchBusy.value = true
  try {
    await api.createBranch(store.projectId, {
      repository_id: branchForm.repository_id,
      work_item_id: branchForm.work_item_id,
      name: branchForm.name.trim(),
      head_commit_sha: '',
      created_by: null,
    })
    branchModal.value = false
    await store.refresh()
    toast.success('Branch created in Gitea and linked to its work item.', 'Source control')
  } catch (error) {
    branchError.value = error instanceof Error ? error.message : 'Failed to create branch'
  } finally {
    branchBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Pull request
// ---------------------------------------------------------------------------

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

const availablePrBranches = computed(() =>
  branches.value.filter(
    (branch) =>
      branch.repository_id === prForm.repository_id &&
      (!prForm.work_item_id || branch.work_item_id === prForm.work_item_id),
  ),
)

function openPr(): void {
  if (!canWriteCode.value) return
  Object.assign(prForm, {
    repository_id: repositories.value[0]?.id ?? '',
    work_item_id: workItems.value[0]?.id ?? '',
    title: '',
    description: '',
    source_branch_id: '',
    target_branch: store.project?.default_branch ?? 'main',
  })
  prError.value = ''
  prModal.value = true
}

async function savePr(): Promise<void> {
  if (!canWriteCode.value) return
  if (!prForm.repository_id) {
    prError.value = 'Link a repository first.'
    return
  }
  if (!prForm.work_item_id) {
    prError.value = 'A pull request must link to a work item.'
    return
  }
  if (!prForm.title.trim()) {
    prError.value = 'A pull request title is required.'
    return
  }
  if (!prForm.source_branch_id) {
    prError.value = 'Select the branch that contains the change.'
    return
  }
  prBusy.value = true
  try {
    const created = await api.createPullRequest(store.projectId, {
      repository_id: prForm.repository_id,
      work_item_id: prForm.work_item_id,
      number: 0,
      title: prForm.title.trim(),
      description: prForm.description,
      source_branch_id: prForm.source_branch_id,
      target_branch: prForm.target_branch.trim() || 'main',
      author: auth.user?.username ?? '',
      status: 'open',
      review_status: 'pending',
      merge_commit_sha: '',
    })
    prModal.value = false
    await store.refresh()
    toast.success(`Pull request #${created.number} opened in Gitea. Review state is synced automatically.`, 'Source control')
  } catch (error) {
    prError.value = error instanceof Error ? error.message : 'Failed to open pull request'
  } finally {
    prBusy.value = false
  }
}

const mergeOpen = ref(false)
const mergeBusy = ref(false)
const mergePrTarget = ref<PullRequest | null>(null)

function openMerge(pr: PullRequest): void {
  if (!canMerge.value) return
  mergePrTarget.value = pr
  mergeOpen.value = true
}

async function confirmMerge(): Promise<void> {
  if (!canMerge.value) return
  const pr = mergePrTarget.value
  if (!pr || mergeBusy.value) return
  mergeBusy.value = true
  try {
    await api.mergePullRequest(store.projectId, pr.id, '')
    mergeOpen.value = false
    await store.refresh()
    toast.success(
      `PR #${pr.number} merged in Gitea. Build results will arrive from Gitea Actions automatically.`,
      'Merge complete',
    )
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Merge failed', 'Source control')
  } finally {
    mergeBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Environment
// ---------------------------------------------------------------------------

const envModal = ref(false)
const envBusy = ref(false)
const envError = ref('')
const envForm = reactive({
  name: '',
  type: 'custom',
  url: '',
  health_check_url: '',
  deploy_strategy: 'rolling',
  requires_approval: false,
  position: 0,
})

function openEnvironment(): void {
  if (!canDeploy.value) return
  Object.assign(envForm, {
    name: '',
    type: 'custom',
    url: '',
    health_check_url: '',
    deploy_strategy: 'rolling',
    requires_approval: false,
    position: environments.value.length + 1,
  })
  envError.value = ''
  envModal.value = true
}

async function saveEnvironment(): Promise<void> {
  if (!canDeploy.value) return
  if (!envForm.name.trim()) {
    envError.value = 'An environment name is required.'
    return
  }
  envBusy.value = true
  try {
    await api.createEnvironment(store.projectId, {
      ...envForm,
      name: envForm.name.trim(),
      position: Number(envForm.position) || environments.value.length + 1,
    })
    envModal.value = false
    await store.refresh()
    toast.success('Environment added. Argo CD health will populate it automatically.', 'Environments')
  } catch (error) {
    envError.value = error instanceof Error ? error.message : 'Failed to create environment'
  } finally {
    envBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Release
// ---------------------------------------------------------------------------

const releaseModal = ref(false)
const releaseBusy = ref(false)
const releaseError = ref('')
const releaseEditingId = ref<string | null>(null)
const releaseForm = reactive({
  name: '',
  version: '',
  status: 'planned',
  planned_at: '',
  notes: '',
})

function openRelease(release?: Release): void {
  if (!canPlan.value) return
  releaseEditingId.value = release?.id ?? null
  Object.assign(releaseForm, {
    name: release?.name ?? '',
    version: release?.version ?? '',
    status: release?.status ?? 'planned',
    planned_at: dateInputValue(release?.planned_at),
    notes: release?.notes ?? '',
  })
  releaseError.value = ''
  releaseModal.value = true
}

async function saveRelease(): Promise<void> {
  if (!canPlan.value) return
  if (!releaseForm.name.trim() || !releaseForm.version.trim()) {
    releaseError.value = 'Release name and version are required.'
    return
  }
  releaseBusy.value = true
  try {
    const payload = {
      name: releaseForm.name.trim(),
      version: releaseForm.version.trim(),
      status: releaseForm.status,
      planned_at: releaseForm.planned_at || null,
      notes: releaseForm.notes,
    }
    if (releaseEditingId.value) {
      await api.updateRelease(store.projectId, releaseEditingId.value, payload)
      toast.success('Release updated.', 'Release')
    } else {
      await api.createRelease(store.projectId, payload)
      toast.success('Release planned. Deployments and test runs can now reference it.', 'Release')
    }
    releaseModal.value = false
    await store.refresh()
  } catch (error) {
    releaseError.value = error instanceof Error ? error.message : 'Failed to save release'
  } finally {
    releaseBusy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight text-slate-900">Delivery control room</h2>
      </div>
      <div class="flex flex-wrap gap-2">
        <button v-if="canManageSettings" type="button" class="btn-secondary" @click="openSettings">
          <AppIcon name="lock" :size="15" /> Merge policy
        </button>
        <button v-if="canPlan" type="button" class="btn-secondary" @click="openRelease()">
          <AppIcon name="flag" :size="15" /> Release
        </button>
        <button v-if="canWriteCode" type="button" class="btn-secondary" @click="openBranch">
          <AppIcon name="branch" :size="15" /> Branch
        </button>
        <button v-if="canWriteCode" type="button" class="btn-primary" @click="openPr">
          <AppIcon name="plus" :size="15" /> Pull request
        </button>
      </div>
    </div>

    <section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <article class="card p-5">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Repositories</p>
            <p class="mt-2 text-3xl font-semibold text-slate-900">{{ repositories.length }}</p>
            <p class="mt-1 text-xs text-slate-600">{{ branches.length }} branches - {{ openPrs }} open PRs</p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-100 text-slate-600"><AppIcon name="repo" :size="19" /></span>
        </div>
      </article>
      <article class="card p-5">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Gitea Actions</p>
            <p class="mt-2 text-3xl font-semibold text-slate-900">{{ builds.length }}</p>
            <p class="mt-1 text-xs text-slate-600">{{ successfulBuilds }} passed - {{ builds.filter((build) => build.status === 'failure').length }} failed</p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-sky-50 text-sky-700"><AppIcon name="server" :size="19" /></span>
        </div>
      </article>
      <article class="card p-5">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Argo CD</p>
            <p class="mt-2 text-3xl font-semibold text-slate-900">{{ deployments.length }}</p>
            <p class="mt-1 text-xs text-slate-600">{{ healthyEnvironments }}/{{ environments.length }} environments healthy</p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-50 text-emerald-700"><AppIcon name="rocket" :size="19" /></span>
        </div>
      </article>
      <article class="card p-5">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Releases</p>
            <p class="mt-2 text-3xl font-semibold text-slate-900">{{ releases.length }}</p>
            <p class="mt-1 text-xs text-slate-600">{{ releases.filter((release) => release.status === 'released').length }} shipped</p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600"><AppIcon name="flag" :size="19" /></span>
        </div>
      </article>
    </section>

    <section class="card overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-5 py-4">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Environment pipeline</h3>
        </div>
        <button v-if="canDeploy" type="button" class="btn-ghost text-xs" @click="openEnvironment">
          <AppIcon name="plus" :size="13" /> Add environment
        </button>
      </div>
      <div v-if="environments.length" class="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-4">
        <article
          v-for="environment in environments"
          :key="environment.id"
          class="rounded-xl border border-slate-200 bg-slate-50/60 p-4"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="flex items-center gap-2">
              <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-white text-slate-600 shadow-sm ring-1 ring-slate-200">
                <AppIcon :name="environment.type === 'production' ? 'lock' : 'server'" :size="15" />
              </span>
              <div>
                <p class="text-sm font-semibold capitalize text-slate-900">{{ environment.name }}</p>
                <p class="text-[10px] capitalize text-slate-600">{{ environment.type }} - {{ environment.deploy_strategy }}</p>
              </div>
            </div>
            <StatusPill :status="latestDeployment(environment.id)?.health_status || 'unknown'" />
          </div>
          <div class="mt-3 rounded-lg bg-white p-2.5 ring-1 ring-slate-100">
            <p class="text-[10px] uppercase tracking-wide text-slate-600">Current revision</p>
            <p class="mt-0.5 truncate font-mono text-xs font-semibold text-slate-800">
              {{ latestDeployment(environment.id)?.version || 'Not observed yet' }}
            </p>
            <p class="mt-0.5 text-[10px] text-slate-600">
              {{ latestDeployment(environment.id) ? timeAgo(latestDeployment(environment.id)?.updated_at) : 'Waiting for Argo CD' }}
            </p>
          </div>
          <div class="mt-3 flex items-center gap-1.5 text-[10px] text-slate-500">
            <AppIcon name="check-circle" :size="12" />
            Read-only from Argo CD
          </div>
        </article>
      </div>
      <div v-else class="p-5">
        <EmptyState
          icon="server"
          compact
          title="No environments"
          :action-label="canDeploy ? 'Add environment' : ''"
          @action="openEnvironment"
        />
      </div>
    </section>

    <div class="grid gap-5 xl:grid-cols-3">
      <section class="card xl:col-span-2">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-5 py-4">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Source control</h3>
          </div>
          <div v-if="canWriteCode" class="flex gap-1">
            <button type="button" class="btn-ghost text-xs" @click="openRepo">
              <AppIcon name="plus" :size="13" /> Repository
            </button>
            <button type="button" class="btn-ghost text-xs" @click="openBranch">
              <AppIcon name="branch" :size="13" /> Branch
            </button>
          </div>
        </div>

        <div v-if="repositories.length" class="border-b border-slate-100 px-5 py-3">
          <div class="flex flex-wrap gap-2">
            <div v-for="repository in repositories" :key="repository.id" class="flex items-center gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
              <AppIcon name="repo" :size="14" class="text-slate-600" />
              <div>
                <p class="text-xs font-semibold text-slate-800">{{ repository.name }}</p>
                <p class="text-[10px] capitalize text-slate-600">{{ repository.provider }} - {{ repository.default_branch }}</p>
              </div>
              <button
                type="button"
                class="ml-1 rounded px-1.5 py-0.5 text-[10px] font-semibold text-slate-600 hover:bg-white hover:text-indigo-700"
                title="Show inbound webhook configuration"
                @click="showWebhook(repository)"
              >
                Webhook
              </button>
            </div>
          </div>
        </div>

        <div v-if="branches.length" class="divide-y divide-slate-100">
          <div v-for="branch in branches" :key="branch.id" class="flex flex-wrap items-center gap-2 px-5 py-3">
            <span class="flex h-7 w-7 items-center justify-center rounded bg-slate-100 text-slate-600"><AppIcon name="branch" :size="13" /></span>
            <div class="min-w-0 flex-1">
              <p class="truncate font-mono text-xs font-medium text-slate-800">{{ branch.name }}</p>
              <p class="mt-0.5 text-[10px] text-slate-600">
                {{ workItemKey(branch.work_item_id) }} - {{ repositoryName(branch.repository_id) }} - {{ timeAgo(branch.updated_at) }}
              </p>
            </div>
            <StatusPill :status="branch.status" />
          </div>
        </div>
        <div v-else class="px-5 py-8 text-center">
          <button v-if="canWriteCode" type="button" class="btn-secondary" @click="openBranch">
            <AppIcon name="branch" :size="13" /> Link branch
          </button>
          <p v-else class="text-xs text-slate-600">No branches linked yet.</p>
        </div>
      </section>

      <section class="card">
        <div class="flex items-center justify-between border-b border-slate-100 px-5 py-4">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Pull requests</h3>
          </div>
          <button v-if="canWriteCode" type="button" class="btn-ghost text-xs" @click="openPr"><AppIcon name="plus" :size="13" /> PR</button>
        </div>
        <div v-if="pullRequests.length" class="max-h-[430px] divide-y divide-slate-100 overflow-y-auto">
          <div v-for="pr in pullRequests" :key="pr.id" :id="`pr-${pr.id}`" class="scroll-mt-24 px-5 py-3 transition-shadow">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <p class="truncate text-xs font-semibold text-slate-800">#{{ pr.number }} {{ pr.title }}</p>
                <p class="mt-0.5 truncate text-[10px] text-slate-600">
                  {{ workItemKey(pr.work_item_id) }} - {{ branchName(pr.source_branch_id) }} -> {{ pr.target_branch }}
                </p>
              </div>
              <StatusPill :status="pr.status" />
            </div>
            <div class="mt-2 flex items-center justify-between">
              <span class="text-[10px] text-slate-600">{{ pr.author || 'Unassigned' }} - {{ pr.review_status }}</span>
              <button
                v-if="pr.status === 'open' && canMerge"
                type="button"
                class="rounded-lg border border-indigo-200 bg-indigo-50 px-2 py-1 text-[10px] font-semibold text-indigo-700 hover:bg-indigo-100"
                @click="openMerge(pr)"
              >
                Merge PR
              </button>
            </div>
          </div>
        </div>
        <div v-else class="p-5">
          <EmptyState compact title="No pull requests" />
        </div>
      </section>
    </div>

    <div class="grid gap-5 xl:grid-cols-3">
      <section class="card xl:col-span-2">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-5 py-4">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Builds - Gitea Actions</h3>
          </div>
          <span class="inline-flex items-center gap-1 rounded-full border border-emerald-200 bg-emerald-50 px-2.5 py-1 text-[10px] font-semibold text-emerald-700">
            <AppIcon name="check-circle" :size="12" /> Auto-synced
          </span>
        </div>
        <div v-if="builds.length" class="divide-y divide-slate-100">
          <div v-for="build in builds" :key="build.id" :id="`build-${build.id}`" data-testid="build-row" class="flex scroll-mt-24 flex-wrap items-center gap-3 px-5 py-3 transition-shadow">
            <span class="flex h-8 w-8 items-center justify-center rounded-lg" :class="build.status === 'success' ? 'bg-emerald-50 text-emerald-700' : build.status === 'failure' ? 'bg-red-50 text-red-600' : 'bg-sky-50 text-sky-700'">
              <AppIcon name="server" :size="14" />
            </span>
            <div class="min-w-0 flex-1">
              <p class="truncate text-xs font-semibold text-slate-800">
                {{ buildTitle(build) }}
                <span class="font-normal text-slate-600">- {{ build.branch || 'No branch' }}</span>
              </p>
              <p class="mt-0.5 truncate text-[10px] text-slate-600">
                {{ buildRunLabel(build) }} - {{ repositoryName(build.repository_id) }} - {{ timeAgo(build.updated_at) }}
              </p>
            </div>
            <StatusPill :status="build.status" />
          </div>
        </div>
        <div v-else class="p-5">
          <EmptyState compact title="No builds" />
        </div>
      </section>

      <section class="card">
        <div class="flex items-center justify-between border-b border-slate-100 px-5 py-4">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Releases</h3>
          </div>
          <button v-if="canPlan" type="button" class="btn-ghost text-xs" @click="openRelease()"><AppIcon name="plus" :size="13" /> Release</button>
        </div>
        <div v-if="releases.length" class="divide-y divide-slate-100">
          <div v-for="release in releases" :key="release.id" class="px-5 py-3">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <p class="truncate text-xs font-semibold text-slate-800">{{ release.name }}</p>
                <p class="mt-0.5 text-[10px] text-slate-600">
                  {{ release.version }} - {{ release.planned_at ? formatDateTime(release.planned_at) : 'No date' }}
                </p>
              </div>
              <StatusPill :status="release.status" />
            </div>
            <div class="mt-2 flex justify-end">
              <button v-if="canPlan" type="button" class="text-[10px] font-semibold text-indigo-600 hover:text-indigo-700" @click="openRelease(release)">Edit release</button>
            </div>
          </div>
        </div>
        <div v-else class="p-5">
          <EmptyState compact title="No releases" />
        </div>
      </section>
    </div>

    <section class="card">
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-5 py-4">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Deployments - Argo CD</h3>
        </div>
        <span class="inline-flex items-center gap-1 rounded-full border border-emerald-200 bg-emerald-50 px-2.5 py-1 text-[10px] font-semibold text-emerald-700">
          <AppIcon name="check-circle" :size="12" /> Auto-synced
        </span>
      </div>

      <div v-if="deployments.length" class="overflow-x-auto">
        <table class="w-full min-w-[900px] text-left">
          <thead class="table-head">
            <tr>
              <th class="px-5 py-2.5">Environment</th>
              <th class="px-3 py-2.5">Argo application</th>
              <th class="px-3 py-2.5">Revision</th>
              <th class="px-3 py-2.5">Build / release</th>
              <th class="px-3 py-2.5">Sync</th>
              <th class="px-3 py-2.5">Health</th>
              <th class="px-5 py-2.5">When</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="deployment in deployments" :key="deployment.id" :id="`deployment-${deployment.id}`" data-testid="deployment-row" class="table-row scroll-mt-24 transition-shadow">
              <td class="px-5 py-3">
                <p class="text-xs font-semibold capitalize text-slate-800">{{ environmentName(deployment.environment_id) }}</p>
                <p class="mt-0.5 text-[10px] text-slate-600">{{ deploymentSource(deployment) }}</p>
              </td>
              <td class="px-3 py-3">
                <p class="font-mono text-xs text-slate-700">{{ deployment.argo_app || '-' }}</p>
              </td>
              <td class="px-3 py-3 font-mono text-[10px] text-slate-600">{{ deployment.argo_revision || deployment.version || '-' }}</td>
              <td class="px-3 py-3">
                <p class="text-[10px] text-slate-600">{{ deployment.build_id ? shortId(deployment.build_id) : 'No build' }}</p>
                <p class="text-[10px] text-slate-600">{{ releaseLabel(deployment.release_id) }}</p>
              </td>
              <td class="px-3 py-3"><StatusPill :status="deployment.status" /></td>
              <td class="px-3 py-3">
                <StatusPill :status="deployment.health_status" />
                <p v-if="deploymentHealthMessage(deployment)" class="mt-1 max-w-[220px] truncate text-[10px] text-slate-600" :title="deploymentHealthMessage(deployment)">
                  {{ deploymentHealthMessage(deployment) }}
                </p>
                <p v-else-if="latestHealth(deployment.id)" class="mt-1 text-[10px] text-slate-600">
                  {{ latestHealth(deployment.id)?.status }}
                </p>
              </td>
              <td class="px-5 py-3 text-[10px] text-slate-600">{{ timeAgo(deployment.updated_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="p-5">
        <EmptyState icon="rocket" compact title="No deployments observed" />
      </div>
    </section>

    <!-- Repository modal -->
    <BaseModal v-model="repoModal" title="Link repository" max-width="md" :persistent="repoBusy">
      <div class="space-y-4">
        <div>
          <label class="field-label" for="repo-name">Name</label>
          <input id="repo-name" v-model="repoForm.name" data-autofocus class="field-input" placeholder="spm-platform" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="repo-provider">Provider</label>
            <select id="repo-provider" v-model="repoForm.provider" class="field-input">
              <option value="gitea">Gitea</option>
              <option value="github">GitHub</option>
              <option value="gitlab">GitLab</option>
              <option value="generic">Generic Git</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="repo-branch">Default branch</label>
            <input id="repo-branch" v-model="repoForm.default_branch" class="field-input" />
          </div>
        </div>
        <div>
          <label class="field-label" for="repo-url">Repository URL</label>
          <input id="repo-url" v-model="repoForm.url" class="field-input" placeholder="Internal clone URL or provider URL" />
        </div>
        <div>
          <label class="field-label" for="repo-external">External ID</label>
          <input id="repo-external" v-model="repoForm.external_id" class="field-input" placeholder="Optional provider ID" />
        </div>
        <p v-if="repoError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ repoError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="repoBusy" @click="repoModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="repoBusy" @click="saveRepo">
          <AppIcon v-if="repoBusy" name="loader" :size="14" class="animate-spin" /> Link repository
        </button>
      </template>
    </BaseModal>

    <!-- Branch modal -->
    <BaseModal v-model="branchModal" title="Link branch" max-width="md" :persistent="branchBusy">
      <div class="space-y-4">
        <div>
          <label class="field-label" for="new-branch-repo">Repository</label>
          <select id="new-branch-repo" v-model="branchForm.repository_id" class="field-input">
            <option value="">Select a repository</option>
            <option v-for="repository in repositories" :key="repository.id" :value="repository.id">{{ repository.name }}</option>
          </select>
        </div>
        <div>
          <label class="field-label" for="new-branch-item">Work item</label>
          <select id="new-branch-item" v-model="branchForm.work_item_id" class="field-input">
            <option value="">Select a work item</option>
            <option v-for="item in workItems" :key="item.id" :value="item.id">{{ item.key }} - {{ item.title }}</option>
          </select>
        </div>
        <div>
          <label class="field-label" for="new-branch-name">Branch name</label>
          <input id="new-branch-name" v-model="branchForm.name" class="field-input font-mono text-xs" placeholder="feature/SPM-12-checkout" />
        </div>
        <p v-if="branchError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ branchError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="branchBusy" @click="branchModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="branchBusy" @click="saveBranch">
          <AppIcon v-if="branchBusy" name="loader" :size="14" class="animate-spin" /> Link branch
        </button>
      </template>
    </BaseModal>

    <!-- Pull request modal -->
    <BaseModal v-model="prModal" title="Open pull request" max-width="lg" :persistent="prBusy">
      <div class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="pr-repo">Repository</label>
            <select id="pr-repo" v-model="prForm.repository_id" class="field-input">
              <option value="">Select a repository</option>
              <option v-for="repository in repositories" :key="repository.id" :value="repository.id">{{ repository.name }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="pr-item">Work item</label>
            <select id="pr-item" v-model="prForm.work_item_id" class="field-input">
              <option value="">Select a work item</option>
              <option v-for="item in workItems" :key="item.id" :value="item.id">{{ item.key }} - {{ item.title }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="pr-title">Title</label>
          <input id="pr-title" v-model="prForm.title" data-autofocus class="field-input" placeholder="Implement one-tap payment" />
        </div>
        <div>
          <label class="field-label" for="pr-description">Description</label>
          <textarea id="pr-description" v-model="prForm.description" rows="2" class="field-input" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="pr-source">Source branch</label>
            <select id="pr-source" v-model="prForm.source_branch_id" class="field-input">
              <option value="">Select a linked branch</option>
              <option v-for="branch in availablePrBranches" :key="branch.id" :value="branch.id">{{ branch.name }} ({{ workItemKey(branch.work_item_id) }})</option>
            </select>
            <p v-if="!availablePrBranches.length" class="mt-1 text-[11px] text-amber-700">Create a branch for the selected work item first.</p>
          </div>
          <div>
            <label class="field-label" for="pr-target">Target branch</label>
            <input id="pr-target" v-model="prForm.target_branch" class="field-input" />
          </div>
        </div>
        <p v-if="prError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ prError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="prBusy" @click="prModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="prBusy" @click="savePr">
          <AppIcon v-if="prBusy" name="loader" :size="14" class="animate-spin" /> Open PR
        </button>
      </template>
    </BaseModal>

    <!-- Merge modal -->
    <BaseModal v-model="mergeOpen" title="Merge pull request" :description="mergePrTarget ? `#${mergePrTarget.number} ${mergePrTarget.title}` : ''" max-width="md" :persistent="mergeBusy">
      <div class="space-y-4"></div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="mergeBusy" @click="mergeOpen = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="mergeBusy" @click="confirmMerge">
          <AppIcon v-if="mergeBusy" name="loader" :size="14" class="animate-spin" /> Merge in Gitea
        </button>
      </template>
    </BaseModal>

    <!-- Environment modal -->
    <BaseModal v-model="envModal" title="Add environment" max-width="md" :persistent="envBusy">
      <div class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="env-name">Name</label>
            <input id="env-name" v-model="envForm.name" data-autofocus class="field-input" placeholder="staging" />
          </div>
          <div>
            <label class="field-label" for="env-type">Type</label>
            <select id="env-type" v-model="envForm.type" class="field-input">
              <option value="development">Development</option>
              <option value="staging">Staging</option>
              <option value="production">Production</option>
              <option value="custom">Custom</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="env-url">URL</label>
          <input id="env-url" v-model="envForm.url" class="field-input" placeholder="https://staging.example.com" />
        </div>
        <div>
          <label class="field-label" for="env-health">Health check URL</label>
          <input id="env-health" v-model="envForm.health_check_url" class="field-input" placeholder="https://staging.example.com/health" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="env-strategy">Deploy strategy</label>
            <select id="env-strategy" v-model="envForm.deploy_strategy" class="field-input">
              <option value="rolling">Rolling</option>
              <option value="blue_green">Blue / green</option>
              <option value="canary">Canary</option>
              <option value="recreate">Recreate</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="env-position">Position</label>
            <input id="env-position" v-model="envForm.position" type="number" min="0" class="field-input" />
          </div>
        </div>
        <label class="flex items-center gap-2 text-xs text-slate-600">
          <input v-model="envForm.requires_approval" type="checkbox" class="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" />
          Requires approval before deployment
        </label>
        <p v-if="envError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ envError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="envBusy" @click="envModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="envBusy" @click="saveEnvironment">
          <AppIcon v-if="envBusy" name="loader" :size="14" class="animate-spin" /> Add environment
        </button>
      </template>
    </BaseModal>

    <!-- Release modal -->
    <BaseModal v-model="releaseModal" :title="releaseEditingId ? 'Edit release' : 'Plan release'" max-width="md" :persistent="releaseBusy">
      <div class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="release-name">Name</label>
            <input id="release-name" v-model="releaseForm.name" data-autofocus class="field-input" placeholder="Summer checkout" />
          </div>
          <div>
            <label class="field-label" for="release-version">Version</label>
            <input id="release-version" v-model="releaseForm.version" class="field-input" placeholder="1.4.0" />
          </div>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="release-status">Status</label>
            <select id="release-status" v-model="releaseForm.status" class="field-input">
              <option value="planned">Planned</option>
              <option value="in_progress">In progress</option>
              <option value="released">Released</option>
              <option value="cancelled">Cancelled</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="release-date">Planned date</label>
            <input id="release-date" v-model="releaseForm.planned_at" type="date" class="field-input" />
          </div>
        </div>
        <div>
          <label class="field-label" for="release-notes">Notes</label>
          <textarea id="release-notes" v-model="releaseForm.notes" rows="3" class="field-input" />
        </div>
        <p v-if="releaseError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ releaseError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="releaseBusy" @click="releaseModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="releaseBusy" @click="saveRelease">
          <AppIcon v-if="releaseBusy" name="loader" :size="14" class="animate-spin" /> {{ releaseEditingId ? 'Save release' : 'Plan release' }}
        </button>
      </template>
    </BaseModal>

    <!-- Delivery policy modal -->
    <BaseModal v-model="settingsModal" title="Merge policy" max-width="lg" :persistent="settingsBusy">
      <div class="space-y-4">
        <div class="rounded-xl border border-slate-200 p-4">
          <p class="text-sm font-semibold text-slate-800">Merge gates</p>
          <div class="mt-3 space-y-2.5">
            <label class="flex items-start gap-3 text-xs text-slate-600">
              <input v-model="settingsForm.merge_policy.require_linked_work_item" type="checkbox" class="mt-0.5 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" />
              <span><span class="font-semibold text-slate-800">Require a linked work item.</span> A pull request without a story, task or bug cannot be merged.</span>
            </label>
            <label class="flex items-start gap-3 text-xs text-slate-600">
              <input v-model="settingsForm.merge_policy.require_approved_review" type="checkbox" class="mt-0.5 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" />
              <span><span class="font-semibold text-slate-800">Require an approved review.</span> The review status must be approved before merge.</span>
            </label>
            <label class="flex items-start gap-3 text-xs text-slate-600">
              <input v-model="settingsForm.merge_policy.require_passing_checks" type="checkbox" class="mt-0.5 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" />
              <span><span class="font-semibold text-slate-800">Require passing CI.</span> A successful Gitea Actions run for the source branch is mandatory.</span>
            </label>
          </div>
        </div>
        <p v-if="settingsError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ settingsError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="settingsBusy" @click="settingsModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="settingsBusy" @click="saveSettings">
          <AppIcon v-if="settingsBusy" name="loader" :size="14" class="animate-spin" /> Save policy
        </button>
      </template>
    </BaseModal>

    <!-- Webhook integration modal -->
    <BaseModal v-model="webhookOpen" title="Inbound webhook" :description="webhookRepo?.name || ''" max-width="lg">
      <div v-if="webhookRepo" class="space-y-4">
        <div>
          <p class="field-label">Payload URL</p>
          <code class="block break-all rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-[11px] text-slate-700">{{ webhookRepo.webhook_url || 'No webhook URL recorded' }}</code>
        </div>
        <div>
          <p class="field-label">Secret token</p>
          <code class="block break-all rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-[11px] text-slate-700">{{ webhookRepo.webhook_token || 'No token recorded' }}</code>
        </div>
        <p v-if="webhookCopied" class="text-xs font-semibold text-emerald-700">Copied to clipboard.</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" @click="webhookOpen = false">Close</button>
        <button type="button" class="btn-primary" @click="copyWebhook">
          <AppIcon name="link" :size="14" /> Copy configuration
        </button>
      </template>
    </BaseModal>
  </div>
</template>
