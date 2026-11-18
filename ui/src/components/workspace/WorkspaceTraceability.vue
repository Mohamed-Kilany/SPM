<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import EmptyState from './EmptyState.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { api } from '../../api/client'
import { percent } from '../../lib/workspace'
import type { TraceabilityFeature, TraceabilityResponse } from '../../types/workspace'

const store = useWorkspaceStore()
const router = useRouter()

const traceability = ref<TraceabilityResponse | null>(null)
const loading = ref(false)
const error = ref('')
const search = ref('')
const onlyGaps = ref(false)

const features = computed(() => traceability.value?.features ?? [])
const gaps = computed(() => traceability.value?.gaps ?? [])
const metrics = computed(() => store.metrics)

const filteredFeatures = computed(() => {
  const query = search.value.trim().toLowerCase()
  return features.value.filter((feature) => {
    if (onlyGaps.value && !feature.gaps.length) return false
    if (!query) return true
    return `${feature.feature_title} ${epicTitle(feature.epic_id)} ${prdTitle(feature.prd_id)}`
      .toLowerCase()
      .includes(query)
  })
})

const completeFeatures = computed(() => features.value.filter((feature) => !feature.gaps.length).length)
const featuresWithGaps = computed(() => features.value.filter((feature) => feature.gaps.length).length)

function countLabel(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`
}

function epicTitle(epicId: string | null): string {
  if (!epicId) return 'No epic'
  return store.epics.find((epic) => epic.id === epicId)?.title ?? 'Unknown epic'
}

function prdTitle(prdId: string | null): string {
  if (!prdId) return 'No PRD'
  return store.prds.find((prd) => prd.id === prdId)?.title ?? 'Unknown PRD'
}

function openBoard(feature: TraceabilityFeature): void {
  void router.push({
    name: 'workspace',
    params: { id: store.projectId },
    query: { tab: 'board', feature: feature.feature_id },
  })
}

function openTestEvidence(feature: TraceabilityFeature): void {
  const query: Record<string, string> = { tab: 'tests' }
  if (feature.latest_test_run_id) query.run = feature.latest_test_run_id
  else if (feature.test_case_ids.length) query.case = feature.test_case_ids[0]
  void router.push({ name: 'workspace', params: { id: store.projectId }, query })
}

function openCodeEvidence(feature: TraceabilityFeature): void {
  const query: Record<string, string> = { tab: 'code', feature: feature.feature_id }
  if (feature.pull_request_ids.length) query.pr = feature.pull_request_ids[0]
  void router.push({ name: 'workspace', params: { id: store.projectId }, query })
}

function openBuildEvidence(feature: TraceabilityFeature): void {
  const query: Record<string, string> = { tab: 'delivery' }
  if (feature.build_ids.length) query.build = feature.build_ids[0]
  void router.push({ name: 'workspace', params: { id: store.projectId }, query })
}

function openDeploymentEvidence(feature: TraceabilityFeature): void {
  const query: Record<string, string> = { tab: 'delivery' }
  if (feature.deployment_ids.length) query.deployment = feature.deployment_ids[0]
  void router.push({ name: 'workspace', params: { id: store.projectId }, query })
}

function openTests(): void {
  void router.push({ name: 'workspace', params: { id: store.projectId }, query: { tab: 'tests' } })
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    traceability.value = await api.getTraceability(store.projectId)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load traceability matrix'
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(
  () => store.workspace?.revision,
  (next, previous) => {
    if (next !== previous && next !== undefined) void load()
  },
)
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight text-slate-900">Traceability matrix</h2>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn-secondary" @click="openTests">
          <AppIcon name="flask" :size="15" /> Test evidence
        </button>
      </div>
    </div>

    <section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <article class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Chain coverage</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ percent(metrics?.coverage_percent) }}</p>
        <p class="mt-1 text-xs text-slate-600">{{ countLabel(metrics?.feature_count ?? 0, 'feature') }} in the product graph</p>
      </article>
      <article class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Chain-complete features</p>
        <p class="mt-2 text-3xl font-semibold text-emerald-700">{{ completeFeatures }}</p>
      </article>
      <article class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Features with gaps</p>
        <p class="mt-2 text-3xl font-semibold" :class="featuresWithGaps ? 'text-amber-700' : 'text-slate-900'">{{ featuresWithGaps }}</p>
        <p class="mt-1 text-xs text-slate-600">{{ countLabel(gaps.length, 'individual gap') }}</p>
      </article>
      <article class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Unlinked / untested</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">
          {{ metrics?.unlinked_features ?? 0 }}<span class="text-base font-medium text-slate-600"> / {{ metrics?.untested_features ?? 0 }}</span>
        </p>
      </article>
    </section>

    <section class="card overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 px-5 py-4">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">End-to-end evidence</h3>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative">
            <AppIcon name="search" :size="14" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-600" />
            <input v-model="search" class="field-input w-56 py-2 pl-8 text-xs" placeholder="Search features…" />
          </div>
          <label class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-600">
            <input v-model="onlyGaps" type="checkbox" class="h-3.5 w-3.5 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" />
            Only gaps
          </label>
        </div>
      </div>

      <div v-if="loading" class="space-y-2 p-5">
        <div v-for="index in 5" :key="index" class="skeleton h-14 rounded-lg" />
      </div>

      <div v-else-if="error" class="p-5">
        <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">
          {{ error }}
          <button type="button" class="ml-2 font-semibold underline" @click="load">Retry</button>
        </div>
      </div>

      <div v-else-if="filteredFeatures.length" class="overflow-x-auto">
        <table class="w-full min-w-[1120px] text-left">
          <thead class="table-head">
            <tr>
              <th class="px-5 py-2.5">Feature / requirement</th>
              <th class="px-3 py-2.5">Work</th>
              <th class="px-3 py-2.5">Tests</th>
              <th class="px-3 py-2.5">Defects</th>
              <th class="px-3 py-2.5">Code</th>
              <th class="px-3 py-2.5">Delivery</th>
              <th class="px-3 py-2.5">Health</th>
              <th class="px-3 py-2.5">Gaps</th>
              <th class="px-5 py-2.5" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="feature in filteredFeatures" :key="feature.feature_id" class="table-row align-top">
              <td class="px-5 py-3">
                <p class="max-w-[240px] truncate text-xs font-semibold text-slate-800">{{ feature.feature_title }}</p>
                <p class="mt-1 flex flex-wrap items-center gap-1 text-[10px] text-slate-600">
                  <span class="inline-flex items-center gap-1"><AppIcon name="flag" :size="10" /> {{ epicTitle(feature.epic_id) }}</span>
                  <span class="text-slate-300">·</span>
                  <span class="inline-flex items-center gap-1" :class="feature.prd_id ? 'text-indigo-600' : 'text-amber-700'">
                    <AppIcon name="folder" :size="10" /> {{ prdTitle(feature.prd_id) }}
                  </span>
                </p>
              </td>
              <td class="px-3 py-3">
                <button type="button" class="text-left hover:text-indigo-700" @click="openBoard(feature)">
                  <p class="text-xs font-semibold text-slate-800">{{ countLabel(feature.stories, 'story', 'stories') }}</p>
                  <p class="mt-0.5 text-[10px] text-slate-600">{{ countLabel(feature.tasks, 'task / subtask') }}</p>
                </button>
              </td>
              <td class="px-3 py-3">
                <button type="button" class="text-left hover:text-indigo-700" @click="openTestEvidence(feature)">
                  <p class="text-xs font-semibold text-slate-800">{{ countLabel(feature.test_cases, 'case') }}</p>
                  <StatusPill :status="feature.last_run_status || 'not_run'" :dot="false" />
                </button>
              </td>
              <td class="px-3 py-3">
                <button
                  type="button"
                  class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ring-1 ring-inset hover:ring-red-400"
                  :class="feature.open_bugs ? 'bg-red-50 text-red-700 ring-red-600/20' : 'bg-slate-100 text-slate-600 ring-slate-500/20'"
                  @click="openBoard(feature)"
                >
                  {{ feature.open_bugs }} open
                </button>
              </td>
              <td class="px-3 py-3">
                <button type="button" class="text-left hover:text-indigo-700" @click="openCodeEvidence(feature)">
                  <p class="text-[11px] text-slate-700">{{ countLabel(feature.branch_count, 'branch', 'branches') }}</p>
                  <p class="text-[10px] text-slate-600">{{ countLabel(feature.pr_count, 'PR') }}</p>
                </button>
              </td>
              <td class="px-3 py-3">
                <button type="button" class="text-left hover:text-indigo-700" @click="openBuildEvidence(feature)">
                  <p class="text-[11px] text-slate-700">{{ countLabel(feature.build_count, 'build') }}</p>
                  <p class="text-[10px] text-slate-600" @click.stop="openDeploymentEvidence(feature)">{{ countLabel(feature.deployments, 'deployment') }}</p>
                </button>
              </td>
              <td class="px-3 py-3">
                <button type="button" class="hover:opacity-80" @click="openDeploymentEvidence(feature)">
                  <StatusPill :status="feature.health_status || 'unknown'" />
                </button>
              </td>
              <td class="px-3 py-3">
                <div v-if="feature.gaps.length" class="flex max-w-[190px] flex-wrap gap-1">
                  <span
                    v-for="gap in feature.gaps"
                    :key="gap"
                    class="rounded bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700"
                  >
                    {{ gap }}
                  </span>
                </div>
                <span v-else class="inline-flex items-center gap-1 text-[10px] font-semibold text-emerald-700">
                  <AppIcon name="check-circle" :size="12" /> Chain complete
                </span>
              </td>
              <td class="px-5 py-3 text-right">
                <button type="button" class="btn-ghost px-2 text-[11px]" @click="openBoard(feature)">
                  Board <AppIcon name="arrowRight" :size="11" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="p-5">
        <EmptyState
          icon="link"
          compact
          title="No features to trace"

        />
      </div>
    </section>

    <section class="card p-5">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Gap register</h3>
        </div>
        <span class="badge bg-amber-50 text-amber-700 ring-amber-600/20">{{ gaps.length }} gaps</span>
      </div>
      <div v-if="gaps.length" class="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
        <div v-for="gap in gaps" :key="gap" class="flex items-start gap-2 rounded-lg border border-amber-100 bg-amber-50/60 px-3 py-2">
          <AppIcon name="alert" :size="13" class="mt-0.5 flex-none text-amber-700" />
          <p class="text-[11px] leading-relaxed text-amber-800">{{ gap }}</p>
        </div>
      </div>
      <div v-else class="mt-3 flex items-center gap-2 rounded-lg border border-emerald-100 bg-emerald-50 px-3 py-2 text-xs text-emerald-700">
        <AppIcon name="check-circle" :size="14" />
        Every feature has a complete requirement-to-health chain.
      </div>
    </section>
  </div>
</template>
