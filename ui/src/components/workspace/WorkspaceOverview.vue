<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '../AppIcon.vue'
import AiCopilot from './AiCopilot.vue'
import StatusPill from './StatusPill.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { formatDateTime, timeAgo } from '../../lib/format'
import { percent } from '../../lib/workspace'
import type { Deployment, Environment } from '../../types/workspace'

const store = useWorkspaceStore()
const router = useRouter()

const projectId = computed(() => store.projectId)
const metrics = computed(() => store.metrics)
const workspace = computed(() => store.workspace)

const memberNames = computed(() => {
  const map = new Map<string, string>()
  for (const member of workspace.value?.members ?? []) {
    map.set(member.user_id, member.username || member.email)
  }
  return map
})

const chain = computed(() => [
  { key: 'prd', label: 'PRDs', icon: 'folder', count: workspace.value?.prds.length ?? 0, tab: 'planning' },
  { key: 'epic', label: 'Epics', icon: 'flag', count: workspace.value?.epics.length ?? 0, tab: 'planning' },
  { key: 'feature', label: 'Features', icon: 'layers', count: workspace.value?.features.length ?? 0, tab: 'planning' },
  { key: 'work', label: 'Work items', icon: 'board', count: workspace.value?.work_items.length ?? 0, tab: 'board' },
  { key: 'test', label: 'Test cases', icon: 'flask', count: workspace.value?.test_cases.length ?? 0, tab: 'tests' },
  { key: 'branch', label: 'Branches', icon: 'branch', count: workspace.value?.branches.length ?? 0, tab: 'delivery' },
  { key: 'pr', label: 'Pull requests', icon: 'repo', count: workspace.value?.pull_requests.length ?? 0, tab: 'delivery' },
  { key: 'build', label: 'Builds', icon: 'server', count: workspace.value?.builds.length ?? 0, tab: 'delivery' },
  { key: 'deploy', label: 'Deployments', icon: 'rocket', count: workspace.value?.deployments.length ?? 0, tab: 'delivery' },
])

const latestDeployments = computed(() => {
  const map = new Map<string, Deployment>()
  for (const deployment of workspace.value?.deployments ?? []) {
    if (!map.has(deployment.environment_id)) map.set(deployment.environment_id, deployment)
  }
  return map
})

const environments = computed(() =>
  [...(workspace.value?.environments ?? [])].sort((a, b) => a.position - b.position),
)

const recentActivity = computed(() => (workspace.value?.activity ?? []).slice(0, 9))

const releases = computed(() =>
  [...(workspace.value?.releases ?? [])]
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, 4),
)

const activeSprint = computed(
  () => workspace.value?.sprints.find((sprint) => sprint.status === 'active') ?? null,
)

function environmentStatus(environment: Environment): string {
  return latestDeployments.value.get(environment.id)?.health_status || 'unknown'
}

function environmentDeployment(environment: Environment): Deployment | undefined {
  return latestDeployments.value.get(environment.id)
}

function open(tab: string): void {
  void router.push({ name: 'workspace', params: { id: projectId.value }, query: { tab } })
}

function entityIcon(entityType: string): string {
  switch (entityType) {
    case 'prd':
      return 'folder'
    case 'epic':
      return 'flag'
    case 'feature':
      return 'layers'
    case 'work_item':
      return 'board'
    case 'test_case':
    case 'test_result':
    case 'test_run':
    case 'test_step_result':
      return 'flask'
    case 'branch':
      return 'branch'
    case 'pull_request':
      return 'repo'
    case 'build':
      return 'server'
    case 'deployment':
    case 'health_check':
      return 'rocket'
    case 'environment':
      return 'server'
    default:
      return 'activity'
  }
}

function actorName(actorId: string | null): string {
  if (!actorId) return 'System'
  return memberNames.value.get(actorId) ?? 'Team member'
}
</script>

<template>
  <div class="space-y-5">
    <section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <article class="card relative overflow-hidden p-5">
        <div class="flex items-start justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Traceability coverage</p>
            <p class="mt-2 text-3xl font-semibold tracking-tight text-slate-900">
              {{ percent(metrics?.coverage_percent) }}
            </p>
            <p class="mt-1 text-xs text-slate-600">
              {{ metrics?.unlinked_features ?? 0 }} unlinked · {{ metrics?.untested_features ?? 0 }} untested
            </p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600">
            <AppIcon name="link" :size="19" />
          </span>
        </div>
        <button class="mt-4 inline-flex items-center gap-1 text-xs font-semibold text-indigo-600 hover:text-indigo-700" @click="open('traceability')">
          Inspect gaps <AppIcon name="arrowRight" :size="13" />
        </button>
      </article>

      <article class="card relative overflow-hidden p-5">
        <div class="flex items-start justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Test pass rate</p>
            <p class="mt-2 text-3xl font-semibold tracking-tight text-slate-900">
              {{ percent(metrics?.pass_rate) }}
            </p>
            <p class="mt-1 text-xs text-slate-600">
              {{ metrics?.test_run_count ?? 0 }} runs · {{ metrics?.test_case_count ?? 0 }} cases
            </p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-cyan-50 text-cyan-600">
            <AppIcon name="flask" :size="19" />
          </span>
        </div>
        <button class="mt-4 inline-flex items-center gap-1 text-xs font-semibold text-cyan-700 hover:text-cyan-800" @click="open('tests')">
          Open test lab <AppIcon name="arrowRight" :size="13" />
        </button>
      </article>

      <article class="card relative overflow-hidden p-5">
        <div class="flex items-start justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Open defects</p>
            <p class="mt-2 text-3xl font-semibold tracking-tight" :class="(metrics?.open_bug_count ?? 0) > 0 ? 'text-red-600' : 'text-slate-900'">
              {{ metrics?.open_bug_count ?? 0 }}
            </p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-red-50 text-red-600">
            <AppIcon name="alert" :size="19" />
          </span>
        </div>
        <button class="mt-4 inline-flex items-center gap-1 text-xs font-semibold text-red-600 hover:text-red-700" @click="open('board')">
          Triage defects <AppIcon name="arrowRight" :size="13" />
        </button>
      </article>

      <article class="card relative overflow-hidden p-5">
        <div class="flex items-start justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Environment health</p>
            <p class="mt-2 text-3xl font-semibold tracking-tight text-slate-900">
              {{ metrics?.healthy_deployments ?? 0 }}
              <span class="text-base font-medium text-slate-600">/ {{ environments.length }}</span>
            </p>
            <p class="mt-1 text-xs text-slate-600">
              {{ metrics?.degraded_deployments ?? 0 }} degraded · {{ metrics?.deployment_count ?? 0 }} deployments
            </p>
          </div>
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-50 text-emerald-700">
            <AppIcon name="rocket" :size="19" />
          </span>
        </div>
        <button class="mt-4 inline-flex items-center gap-1 text-xs font-semibold text-emerald-700 hover:text-emerald-800" @click="open('delivery')">
          Review delivery <AppIcon name="arrowRight" :size="13" />
        </button>
      </article>
    </section>

    <AiCopilot />

    <section class="card overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-5 py-4">
        <div>
          <h2 class="text-sm font-semibold text-slate-900">Connected delivery chain</h2>
        </div>
        <button class="btn-ghost text-xs" @click="open('traceability')">
          <AppIcon name="link" :size="14" />
          Traceability matrix
        </button>
      </div>
      <div class="overflow-x-auto px-5 py-5">
        <div class="flex min-w-[840px] items-stretch gap-1.5">
          <template v-for="(step, index) in chain" :key="step.key">
            <button
              type="button"
              class="group flex min-w-[92px] flex-1 flex-col items-center rounded-xl border border-transparent px-2 py-3 text-center hover:border-slate-200 hover:bg-slate-50"
              @click="open(step.tab)"
            >
              <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-slate-100 text-slate-600 group-hover:bg-indigo-50 group-hover:text-indigo-600">
                <AppIcon :name="step.icon" :size="17" />
              </span>
              <span class="mt-2 text-lg font-semibold leading-none text-slate-900">{{ step.count }}</span>
              <span class="mt-1 text-[11px] font-medium text-slate-600">{{ step.label }}</span>
            </button>
            <span v-if="index < chain.length - 1" class="flex items-center text-slate-300">
              <AppIcon name="chevronRight" :size="14" />
            </span>
          </template>
        </div>
      </div>
    </section>

    <div class="grid gap-5 xl:grid-cols-3">
      <section class="card xl:col-span-2">
        <div class="flex items-center justify-between border-b border-slate-100 px-5 py-4">
          <div>
            <h2 class="text-sm font-semibold text-slate-900">Recent activity</h2>
          </div>
        </div>

        <div v-if="recentActivity.length" class="divide-y divide-slate-100">
          <div v-for="event in recentActivity" :key="event.id" class="flex gap-3 px-5 py-3.5">
            <span class="mt-0.5 flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-slate-100 text-slate-600">
              <AppIcon :name="entityIcon(event.entity_type)" :size="15" />
            </span>
            <div class="min-w-0 flex-1">
              <p class="text-sm leading-relaxed text-slate-700">{{ event.summary }}</p>
              <p class="mt-0.5 text-[11px] text-slate-600">
                {{ actorName(event.actor_id) }} · {{ timeAgo(event.created_at) }}
              </p>
            </div>
            <span class="hidden flex-none self-center rounded-md bg-slate-50 px-2 py-1 font-mono text-[10px] text-slate-600 sm:block">
              {{ event.entity_type.replace(/_/g, ' ') }}
            </span>
          </div>
        </div>
        <div v-else class="px-5 py-12 text-center text-sm text-slate-600">
          No activity yet. Create a PRD or work item to start the audit trail.
        </div>
      </section>

      <div class="space-y-5">
        <section class="card p-5">
          <div class="flex items-center justify-between">
            <h2 class="text-sm font-semibold text-slate-900">Environment health</h2>
            <button class="text-xs font-semibold text-indigo-600 hover:text-indigo-700" @click="open('delivery')">Manage</button>
          </div>
          <div v-if="environments.length" class="mt-3 space-y-2.5">
            <div
              v-for="environment in environments"
              :key="environment.id"
              class="flex items-center gap-3 rounded-lg border border-slate-100 px-3 py-2.5"
            >
              <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-slate-50 text-slate-600">
                <AppIcon :name="environment.type === 'production' ? 'lock' : 'server'" :size="15" />
              </span>
              <div class="min-w-0 flex-1">
                <p class="truncate text-xs font-semibold capitalize text-slate-800">{{ environment.name }}</p>
                <p class="mt-0.5 truncate text-[10px] text-slate-600">
                  {{ environmentDeployment(environment)?.version || 'No deployment yet' }}
                </p>
              </div>
              <StatusPill :status="environmentStatus(environment)" />
            </div>
          </div>
          <p v-else class="mt-3 text-xs text-slate-600">No environments configured.</p>
        </section>

        <section class="card p-5">
          <div class="flex items-center justify-between">
            <h2 class="text-sm font-semibold text-slate-900">Release readiness</h2>
            <button class="text-xs font-semibold text-indigo-600 hover:text-indigo-700" @click="open('delivery')">Plan</button>
          </div>
          <div v-if="releases.length" class="mt-3 space-y-2.5">
            <div v-for="release in releases" :key="release.id" class="flex items-center gap-3 rounded-lg border border-slate-100 px-3 py-2.5">
              <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                <AppIcon name="flag" :size="15" />
              </span>
              <div class="min-w-0 flex-1">
                <p class="truncate text-xs font-semibold text-slate-800">{{ release.name }}</p>
                <p class="mt-0.5 text-[10px] text-slate-600">
                  {{ release.version }} · {{ release.planned_at ? formatDateTime(release.planned_at) : 'No date' }}
                </p>
              </div>
              <StatusPill :status="release.status" />
            </div>
          </div>
          <p v-else class="mt-3 text-xs text-slate-600">No release has been planned yet.</p>

          <div v-if="activeSprint" class="mt-4 rounded-lg border border-indigo-100 bg-indigo-50/60 p-3">
            <p class="text-[10px] font-semibold uppercase tracking-wide text-indigo-600">Active sprint</p>
            <p class="mt-1 text-xs font-semibold text-slate-800">{{ activeSprint.name }}</p>
            <p class="mt-0.5 text-[11px] text-slate-600">{{ activeSprint.goal || 'No sprint goal set.' }}</p>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>
