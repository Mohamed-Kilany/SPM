<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import BaseModal from '../BaseModal.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import EmptyState from './EmptyState.vue'
import DonutChart from '../charts/DonutChart.vue'
import BarChart from '../charts/BarChart.vue'
import BurnChart from '../charts/BurnChart.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { api } from '../../api/client'
import { dateInputValue, statusLabel } from '../../lib/workspace'
import { formatDateTime } from '../../lib/format'
import type { ProjectReport, ReleaseReadiness, Sprint, WorkItem } from '../../types/workspace'

const store = useWorkspaceStore()
const toast = useToastStore()

const report = ref<ProjectReport | null>(null)
const loading = ref(false)
const reportError = ref('')
const activeReadiness = ref<string>('')

const sprints = computed(() => store.sprints)
const metrics = computed(() => report.value?.metrics ?? store.metrics)
const coverage = computed(() => report.value?.coverage ?? [])
const readiness = computed<ReleaseReadiness[]>(() => report.value?.releases ?? [])
const selectedReadiness = computed(() => {
  if (!readiness.value.length) return null
  return readiness.value.find((entry) => entry.release_id === activeReadiness.value) ?? readiness.value[0]
})

async function loadReport(): Promise<void> {
  if (!store.projectId) return
  loading.value = true
  reportError.value = ''
  try {
    report.value = await api.getReports(store.projectId)
    if (!activeReadiness.value && report.value.releases.length) {
      activeReadiness.value = report.value.releases[0].release_id
    }
  } catch (error) {
    reportError.value = error instanceof Error ? error.message : 'Failed to load reports'
  } finally {
    loading.value = false
  }
}

onMounted(loadReport)

function sprintItems(sprint: Sprint) {
  return store.workItems.filter((item) => item.sprint_id === sprint.id)
}

function sprintDone(sprint: Sprint): number {
  return sprintItems(sprint).filter((item) => ['done', 'closed', 'verified'].includes(item.status)).length
}

function sprintPoints(sprint: Sprint): number {
  return sprintItems(sprint).reduce((total, item) => total + (item.estimate_points ?? 0), 0)
}

function sprintProgress(sprint: Sprint): number {
  const items = sprintItems(sprint)
  if (!items.length) return 0
  return Math.round((sprintDone(sprint) / items.length) * 100)
}

const sprintModal = ref(false)
const sprintBusy = ref(false)
const sprintError = ref('')
const sprintEditingId = ref<string | null>(null)
const sprintForm = reactive({
  name: '',
  goal: '',
  status: 'planned',
  start_date: '',
  end_date: '',
  capacity: '',
})

function openSprint(sprint?: Sprint): void {
  sprintEditingId.value = sprint?.id ?? null
  Object.assign(sprintForm, {
    name: sprint?.name ?? '',
    goal: sprint?.goal ?? '',
    status: sprint?.status ?? 'planned',
    start_date: dateInputValue(sprint?.start_date),
    end_date: dateInputValue(sprint?.end_date),
    capacity: sprint?.capacity != null ? String(sprint.capacity) : '',
  })
  sprintError.value = ''
  sprintModal.value = true
}

async function saveSprint(): Promise<void> {
  if (!sprintForm.name.trim()) {
    sprintError.value = 'Sprint name is required.'
    return
  }
  sprintBusy.value = true
  sprintError.value = ''
  try {
    const payload = {
      name: sprintForm.name.trim(),
      goal: sprintForm.goal,
      status: sprintForm.status,
      start_date: sprintForm.start_date || null,
      end_date: sprintForm.end_date || null,
      capacity: sprintForm.capacity ? Number(sprintForm.capacity) : null,
    }
    if (sprintEditingId.value) {
      await api.updateSprint(store.projectId, sprintEditingId.value, payload)
      toast.success('Sprint updated.', 'Sprint')
    } else {
      await api.createSprint(store.projectId, payload)
      toast.success('Sprint planned. Assign work items from the board.', 'Sprint')
    }
    sprintModal.value = false
    await store.refresh()
    await loadReport()
  } catch (error) {
    sprintError.value = error instanceof Error ? error.message : 'Failed to save sprint'
  } finally {
    sprintBusy.value = false
  }
}

const importModal = ref(false)
const importBusy = ref(false)
const importError = ref('')
const importFormat = ref<'junit' | 'json'>('junit')
const importEnvironment = ref('')
const importFeature = ref('')
const importFileName = ref('')
const importFileContent = ref('')
const importSummary = ref('')

function openImport(): void {
  importEnvironment.value = store.workspace?.environments[0]?.id ?? ''
  importFeature.value = ''
  importFileName.value = ''
  importFileContent.value = ''
  importSummary.value = ''
  importError.value = ''
  importModal.value = true
}

async function chooseImportFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  importFileName.value = file.name
  importFileContent.value = await file.text()
  if (file.name.toLowerCase().endsWith('.xml')) importFormat.value = 'junit'
  if (file.name.toLowerCase().endsWith('.json')) importFormat.value = 'json'
}

async function submitImport(): Promise<void> {
  if (!importFileContent.value.trim()) {
    importError.value = 'Choose a JUnit XML or SPM JSON result file first.'
    return
  }
  if (!importEnvironment.value) {
    importError.value = 'Select the environment where the results were produced.'
    return
  }
  importBusy.value = true
  importError.value = ''
  try {
    const result = await api.importTestRun(
      store.projectId,
      importFileContent.value,
      importFormat.value,
      importEnvironment.value,
      importFeature.value,
    )
    importSummary.value = `${result.total} result(s): ${result.matched} matched, ${result.created} created, ${result.unmatched} unmatched.`
    toast.success('Test results imported into a linked run.', 'Test lab')
    await store.refresh()
    await loadReport()
  } catch (error) {
    importError.value = error instanceof Error ? error.message : 'Failed to import test results'
  } finally {
    importBusy.value = false
  }
}

async function exportReport(type: string, format: 'csv' | 'json'): Promise<void> {
  try {
    const { blob, filename } = await api.downloadReport(store.projectId, type, format)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = filename
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
    toast.success(`Exported ${filename}.`, 'Report')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Export failed', 'Report')
  }
}

function metric(value: number | undefined, suffix = ''): string {
  if (value == null) return '—'
  return `${value.toFixed(1)}${suffix}`
}

function trendWidth(point: { total: number; passed: number }): string {
  if (!point.total) return '0%'
  return `${Math.max(4, Math.round((point.passed / point.total) * 100))}%`
}

function gateIcon(status: string): string {
  if (status === 'pass') return 'check-circle'
  if (status === 'warn') return 'alert'
  return 'x'
}

type ChartDimension = 'status' | 'type' | 'priority' | 'assignee' | 'feature'
const dimension = ref<ChartDimension>('status')
const chartKind = ref<'donut' | 'bar'>('donut')
const burnSprintId = ref('')

const chartDimensions: Array<{ value: ChartDimension; label: string }> = [
  { value: 'status', label: 'Status' },
  { value: 'type', label: 'Work item type' },
  { value: 'priority', label: 'Priority' },
  { value: 'assignee', label: 'Assignee' },
  { value: 'feature', label: 'Feature' },
]

const palette = ['#4f46e5', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#64748b', '#ec4899']

const members = computed(() => store.workspace?.members ?? [])
const features = computed(() => store.features)

function memberName(userId: string | null): string {
  if (!userId) return 'Unassigned'
  const member = members.value.find((entry) => entry.user_id === userId)
  return member?.username || member?.email || 'Team member'
}

function featureName(featureId: string | null): string {
  if (!featureId) return 'No feature'
  return features.value.find((feature) => feature.id === featureId)?.title ?? 'Unknown feature'
}

function dimensionLabel(item: WorkItem, dim: ChartDimension): string {
  switch (dim) {
    case 'status':
      return statusLabel(item.status)
    case 'type':
      return item.type.charAt(0).toUpperCase() + item.type.slice(1)
    case 'priority':
      return item.priority.charAt(0).toUpperCase() + item.priority.slice(1)
    case 'assignee':
      return memberName(item.assignee_id)
    case 'feature':
      return featureName(item.feature_id)
    default:
      return 'Other'
  }
}

const groupedStats = computed(() => {
  const counts = new Map<string, number>()
  for (const item of store.workItems) {
    const label = dimensionLabel(item, dimension.value)
    counts.set(label, (counts.get(label) ?? 0) + 1)
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1])
})

const chartData = computed(() =>
  groupedStats.value.map(([label, value], index) => ({
    label,
    value,
    color: palette[index % palette.length]!,
  })),
)

const burnItems = computed(() => {
  if (!burnSprintId.value) return store.workItems
  return store.workItems.filter((item) => item.sprint_id === burnSprintId.value)
})

const burnPoints = computed(() => {
  const items = burnItems.value
  const sprint = store.sprints.find((entry) => entry.id === burnSprintId.value)
  const end = sprint?.end_date ? new Date(sprint.end_date) : new Date()
  const start = sprint?.start_date
    ? new Date(sprint.start_date)
    : new Date(end.getTime() - 13 * 24 * 60 * 60 * 1000)
  const span = Math.max(1, end.getTime() - start.getTime())
  const days = 14
  const totalScope = items.length
  return Array.from({ length: days }, (_, index) => {
    const date = new Date(start.getTime() + (span * index) / (days - 1))
    const dayEnd = new Date(date)
    dayEnd.setHours(23, 59, 59, 999)
    const scope = items.filter((item) => new Date(item.created_at).getTime() <= dayEnd.getTime()).length
    const completed = items.filter((item) => {
      if (!item.closed_at || !['done', 'closed', 'verified'].includes(item.status)) return false
      return new Date(item.closed_at).getTime() <= dayEnd.getTime()
    }).length
    return {
      label: `${date.getMonth() + 1}/${date.getDate()}`,
      scope,
      completed,
      ideal: Math.round(totalScope * (1 - index / (days - 1))),
    }
  })
})
</script>

<template>
  <div class="space-y-5">
    <section class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight text-slate-900">Reports &amp; release control</h2>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn-secondary" @click="openImport">
          <AppIcon name="inbox" :size="15" /> Import results
        </button>
        <button type="button" class="btn-secondary" @click="exportReport('features', 'csv')">
          <AppIcon name="external" :size="15" /> Coverage CSV
        </button>
        <button type="button" class="btn-secondary" @click="exportReport('traceability', 'json')">
          <AppIcon name="link" :size="15" /> Trace JSON
        </button>
      </div>
    </section>

    <div
      v-if="reportError"
      class="flex items-center gap-3 rounded-xl border border-amber-200 bg-amber-50 p-3.5 text-sm text-amber-800"
    >
      <AppIcon name="alert" :size="17" />
      <span class="flex-1">{{ reportError }}</span>
      <button type="button" class="text-xs font-semibold underline" @click="loadReport">Retry</button>
    </div>

    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-6">
      <div class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Delivery confidence</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ metric(metrics?.delivery_confidence, '%') }}</p>
      </div>
      <div class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Pass rate</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ metric(metrics?.pass_rate, '%') }}</p>
        <p class="mt-1 text-xs text-slate-600">{{ metrics?.test_run_count ?? 0 }} runs recorded</p>
      </div>
      <div class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Coverage</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ metric(metrics?.coverage_percent, '%') }}</p>
        <p class="mt-1 text-xs text-slate-600">{{ metrics?.untested_features ?? 0 }} untested features</p>
      </div>
      <div class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Open bugs</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ metrics?.open_bug_count ?? 0 }}</p>
      </div>
      <div class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Flaky tests</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ metrics?.flaky_test_count ?? 0 }}</p>
      </div>
      <div class="card p-5">
        <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Cycle time</p>
        <p class="mt-2 text-3xl font-semibold text-slate-900">{{ metric(metrics?.cycle_time_hours, 'h') }}</p>
      </div>
    </div>

    <div class="grid gap-5 xl:grid-cols-2">
      <section class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Work distribution</h3>
          </div>
          <div class="flex flex-wrap gap-2">
            <select v-model="dimension" class="field-input w-auto min-w-[150px] py-1.5 text-xs" aria-label="Chart dimension">
              <option v-for="option in chartDimensions" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
            <div class="inline-flex rounded-lg border border-slate-300 bg-white p-0.5" role="group" aria-label="Chart type">
              <button
                type="button"
                class="rounded-md px-2.5 py-1 text-[11px] font-semibold"
                :class="chartKind === 'donut' ? 'bg-indigo-600 text-white' : 'text-slate-600 hover:bg-slate-50'"
                @click="chartKind = 'donut'"
              >Donut</button>
              <button
                type="button"
                class="rounded-md px-2.5 py-1 text-[11px] font-semibold"
                :class="chartKind === 'bar' ? 'bg-indigo-600 text-white' : 'text-slate-600 hover:bg-slate-50'"
                @click="chartKind = 'bar'"
              >Bar</button>
            </div>
          </div>
        </div>
        <div v-if="chartData.length" class="mt-5">
          <DonutChart
            v-if="chartKind === 'donut'"
            :data="chartData"
            center-label="Work items"
            :center-value="store.workItems.length"
          />
          <BarChart v-else :data="chartData" :height="230" />
        </div>
        <EmptyState v-else icon="chart" title="No data to chart" />
      </section>

      <section class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Burn-up / burn-down</h3>
          </div>
          <select v-model="burnSprintId" class="field-input w-auto min-w-[160px] py-1.5 text-xs" aria-label="Burn chart sprint">
            <option value="">All work</option>
            <option v-for="sprint in sprints" :key="sprint.id" :value="sprint.id">{{ sprint.name }}</option>
          </select>
        </div>
        <div class="mt-4">
          <BurnChart :points="burnPoints" :height="230" />
        </div>
      </section>
    </div>

    <div class="grid gap-5 xl:grid-cols-5">
      <section class="card xl:col-span-3">
        <div class="flex items-center justify-between border-b border-slate-200 px-5 py-4">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">Release readiness</h3>
          </div>
          <AppIcon name="flag" :size="18" class="text-slate-600" />
        </div>
        <div v-if="!readiness.length" class="p-5">
          <EmptyState icon="flag" title="No releases yet" />
        </div>
        <div v-else class="divide-y divide-slate-100">
          <button
            v-for="release in readiness"
            :key="release.release_id"
            type="button"
            class="flex w-full items-center gap-3 px-5 py-3.5 text-left hover:bg-slate-50"
            :class="selectedReadiness?.release_id === release.release_id ? 'bg-indigo-50/60' : ''"
            @click="activeReadiness = release.release_id"
          >
            <span
              class="flex h-9 w-9 flex-none items-center justify-center rounded-lg"
              :class="release.ready ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'"
            >
              <AppIcon :name="release.ready ? 'check-circle' : 'alert'" :size="18" />
            </span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-semibold text-slate-800">{{ release.name }}</span>
              <span class="block text-xs text-slate-600">{{ release.version }} · {{ release.score }}% evidence</span>
            </span>
            <span
              class="rounded-full px-2 py-0.5 text-[11px] font-semibold"
              :class="release.ready ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'"
            >
              {{ release.ready ? 'Ready' : 'Blocked' }}
            </span>
          </button>
        </div>
        <div v-if="selectedReadiness" class="border-t border-slate-100 bg-slate-50/60 px-5 py-4">
          <div class="grid gap-2 sm:grid-cols-2">
            <div
              v-for="gate in selectedReadiness.gates"
              :key="gate.key"
              class="rounded-lg border bg-white px-3 py-2.5"
              :class="gate.status === 'pass' ? 'border-emerald-100' : gate.status === 'warn' ? 'border-amber-100' : 'border-red-100'"
            >
              <div class="flex items-center gap-2">
                <AppIcon
                  :name="gateIcon(gate.status)"
                  :size="14"
                  :class="gate.status === 'pass' ? 'text-emerald-700' : gate.status === 'warn' ? 'text-amber-700' : 'text-red-600'"
                />
                <p class="text-xs font-semibold text-slate-800">{{ gate.label }}</p>
              </div>
              <p class="mt-1 text-[11px] leading-relaxed text-slate-600">{{ gate.detail }}</p>
            </div>
          </div>
        </div>
      </section>

      <section class="card xl:col-span-2">
        <div class="border-b border-slate-200 px-5 py-4">
          <h3 class="text-sm font-semibold text-slate-900">Test trend</h3>
        </div>
        <div v-if="!metrics?.test_trend?.length" class="p-5">
          <EmptyState icon="flask" title="No completed runs" />
        </div>
        <div v-else class="space-y-3 px-5 py-4">
          <div v-for="point in metrics.test_trend" :key="point.run_id">
            <div class="flex items-center justify-between gap-2 text-[11px]">
              <span class="truncate font-medium text-slate-600">{{ point.name }}</span>
              <span class="flex-none text-slate-600">{{ point.passed }}/{{ point.total }}</span>
            </div>
            <div class="mt-1 h-2 overflow-hidden rounded-full bg-slate-100">
              <div
                class="h-full rounded-full"
                :class="point.failed ? 'bg-amber-500' : 'bg-emerald-500'"
                :style="{ width: trendWidth(point) }"
              />
            </div>
          </div>
        </div>
      </section>
    </div>

    <section class="card overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-200 px-5 py-4">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Sprint execution</h3>
        </div>
        <button type="button" class="btn-secondary text-xs" @click="openSprint()">
          <AppIcon name="plus" :size="13" /> Sprint
        </button>
      </div>
      <div v-if="!sprints.length" class="p-5">
        <EmptyState icon="clock" title="No sprints planned" />
      </div>
      <div v-else class="grid gap-3 p-5 sm:grid-cols-2 xl:grid-cols-3">
        <article v-for="sprint in sprints" :key="sprint.id" class="rounded-xl border border-slate-200 p-4">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <p class="truncate text-sm font-semibold text-slate-800">{{ sprint.name }}</p>
              <p class="mt-0.5 text-xs text-slate-600">
                {{ sprint.start_date ? formatDateTime(sprint.start_date) : 'No start' }} →
                {{ sprint.end_date ? formatDateTime(sprint.end_date) : 'No end' }}
              </p>
            </div>
            <StatusPill :status="sprint.status" />
          </div>
          <p v-if="sprint.goal" class="mt-2 line-clamp-2 text-xs leading-relaxed text-slate-600">{{ sprint.goal }}</p>
          <div class="mt-3 h-2 overflow-hidden rounded-full bg-slate-100">
            <div class="h-full rounded-full bg-indigo-500" :style="{ width: `${sprintProgress(sprint)}%` }" />
          </div>
          <div class="mt-2 flex items-center justify-between text-[11px] text-slate-600">
            <span>{{ sprintDone(sprint) }}/{{ sprintItems(sprint).length }} items · {{ sprintPoints(sprint) }} pts</span>
            <span v-if="sprint.capacity">{{ sprint.capacity }} capacity</span>
          </div>
          <button type="button" class="btn-ghost mt-2 px-0 text-xs" @click="openSprint(sprint)">
            <AppIcon name="edit" :size="12" /> Edit sprint
          </button>
        </article>
      </div>
    </section>

    <section class="card overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-200 px-5 py-4">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Feature coverage</h3>
        </div>
        <button type="button" class="btn-ghost text-xs" @click="exportReport('summary', 'csv')">
          <AppIcon name="external" :size="13" /> Summary CSV
        </button>
      </div>
      <div v-if="!coverage.length" class="p-5">
        <EmptyState icon="layers" title="No features to report" />
      </div>
      <div v-else class="overflow-x-auto" tabindex="0" role="region" aria-label="Feature coverage">
        <table class="min-w-full text-sm">
          <thead class="bg-slate-50 text-left text-[11px] uppercase tracking-wide text-slate-600">
            <tr>
              <th class="px-5 py-3 font-semibold">Feature</th>
              <th class="px-5 py-3 font-semibold">Epic / PRD</th>
              <th class="px-5 py-3 font-semibold">Cases</th>
              <th class="px-5 py-3 font-semibold">Pass rate</th>
              <th class="px-5 py-3 font-semibold">Open bugs</th>
              <th class="px-5 py-3 font-semibold">Delivery gaps</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            <tr v-for="row in coverage" :key="row.feature_id">
              <td class="px-5 py-3 font-medium text-slate-800">{{ row.feature }}</td>
              <td class="px-5 py-3 text-xs text-slate-600">{{ row.epic || '—' }}<span v-if="row.prd"> / {{ row.prd }}</span></td>
              <td class="px-5 py-3 text-slate-600">{{ row.test_cases }}</td>
              <td class="px-5 py-3">
                <span :class="row.pass_rate >= 90 ? 'text-emerald-700' : row.pass_rate >= 70 ? 'text-amber-700' : 'text-red-700'">
                  {{ row.pass_rate.toFixed(1) }}%
                </span>
              </td>
              <td class="px-5 py-3" :class="row.open_bugs ? 'font-semibold text-red-700' : 'text-slate-600'">{{ row.open_bugs }}</td>
              <td class="px-5 py-3" :class="row.delivery_gaps ? 'font-semibold text-amber-700' : 'text-emerald-700'">
                {{ row.delivery_gaps ? `${row.delivery_gaps} gap(s)` : 'Complete' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <BaseModal
      v-model="sprintModal"
      :title="sprintEditingId ? 'Edit sprint' : 'Plan a sprint'"
    >
      <form class="space-y-4" @submit.prevent="saveSprint">
        <div>
          <label class="field-label" for="sprint-name">Name</label>
          <input id="sprint-name" v-model="sprintForm.name" class="field-input" data-autofocus placeholder="Sprint 12" />
        </div>
        <div>
          <label class="field-label" for="sprint-goal">Goal</label>
          <textarea id="sprint-goal" v-model="sprintForm.goal" class="field-input" rows="2" placeholder="Checkout conversion and release hardening" />
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="field-label" for="sprint-status">Status</label>
            <select id="sprint-status" v-model="sprintForm.status" class="field-input">
              <option value="planned">Planned</option>
              <option value="active">Active</option>
              <option value="completed">Completed</option>
              <option value="cancelled">Cancelled</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="sprint-capacity">Capacity (points)</label>
            <input id="sprint-capacity" v-model="sprintForm.capacity" type="number" min="0" class="field-input" placeholder="40" />
          </div>
          <div>
            <label class="field-label" for="sprint-start">Start</label>
            <input id="sprint-start" v-model="sprintForm.start_date" type="date" class="field-input" />
          </div>
          <div>
            <label class="field-label" for="sprint-end">End</label>
            <input id="sprint-end" v-model="sprintForm.end_date" type="date" class="field-input" />
          </div>
        </div>
        <p v-if="sprintError" class="text-sm text-red-600">{{ sprintError }}</p>
      </form>
      <template #footer>
        <button type="button" class="btn-secondary" @click="sprintModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="sprintBusy" @click="saveSprint">
          <AppIcon v-if="sprintBusy" name="loader" :size="14" class="animate-spin" />
          {{ sprintEditingId ? 'Save sprint' : 'Create sprint' }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      v-model="importModal"
      title="Import automated test results"
      max-width="lg"
    >
      <form class="space-y-4" @submit.prevent="submitImport">
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="field-label" for="import-environment">Environment</label>
            <select id="import-environment" v-model="importEnvironment" class="field-input" data-autofocus>
              <option value="">Select environment</option>
              <option v-for="environment in store.workspace?.environments ?? []" :key="environment.id" :value="environment.id">
                {{ environment.name }}
              </option>
            </select>
          </div>
          <div>
            <label class="field-label" for="import-feature">Create unmatched cases under</label>
            <select id="import-feature" v-model="importFeature" class="field-input">
              <option value="">Do not create cases</option>
              <option v-for="feature in store.features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="import-file">Result file</label>
          <input id="import-file" type="file" accept=".xml,.json,application/xml,application/json" class="field-input py-2" @change="chooseImportFile" />
          <p v-if="importFileName" class="mt-1 text-xs text-slate-600">Selected: {{ importFileName }}</p>
        </div>
        <div class="flex gap-4 text-sm text-slate-600">
          <label class="flex items-center gap-2"><input v-model="importFormat" type="radio" value="junit" /> JUnit XML</label>
          <label class="flex items-center gap-2"><input v-model="importFormat" type="radio" value="json" /> SPM JSON</label>
        </div>
        <p v-if="importSummary" class="rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800">{{ importSummary }}</p>
        <p v-if="importError" class="text-sm text-red-600">{{ importError }}</p>
      </form>
      <template #footer>
        <button type="button" class="btn-secondary" @click="importModal = false">Close</button>
        <button type="button" class="btn-primary" :disabled="importBusy" @click="submitImport">
          <AppIcon v-if="importBusy" name="loader" :size="14" class="animate-spin" />
          Import results
        </button>
      </template>
    </BaseModal>
  </div>
</template>
