<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import BaseModal from '../BaseModal.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import PriorityPill from './PriorityPill.vue'
import EmptyState from './EmptyState.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { api } from '../../api/client'
import { dateInputValue, statusLabel } from '../../lib/workspace'
import { formatDateTime, timeAgo } from '../../lib/format'
import type { Epic, Feature, PRD } from '../../types/workspace'

const store = useWorkspaceStore()
const toast = useToastStore()
const router = useRouter()

const selectedPrdId = ref<string>('all')
const collapsedEpics = ref<Set<string>>(new Set())
const showPrdContent = ref<Set<string>>(new Set())
const busy = ref(false)
const formError = ref('')

const prds = computed(() => store.prds)
const epics = computed(() => store.epics)
const features = computed(() => store.features)
const workItems = computed(() => store.workItems)
const testCases = computed(() => store.testCases)
const members = computed(() => store.workspace?.members ?? [])

const selectedPrd = computed(() => prds.value.find((prd) => prd.id === selectedPrdId.value) ?? null)
const unlinkedEpicCount = computed(
  () => epics.value.filter((epic) => !epic.prd_id || !prds.value.some((prd) => prd.id === epic.prd_id)).length,
)

const visibleEpics = computed(() => {
  if (selectedPrdId.value === 'all') return epics.value
  if (selectedPrdId.value === 'unlinked') {
    return epics.value.filter(
      (epic) => !epic.prd_id || !prds.value.some((prd) => prd.id === epic.prd_id),
    )
  }
  return epics.value.filter((epic) => epic.prd_id === selectedPrdId.value)
})

const selectedTitle = computed(() => {
  if (selectedPrdId.value === 'all') return 'Full planning hierarchy'
  if (selectedPrdId.value === 'unlinked') return 'Unlinked epics'
  return selectedPrd.value?.title ?? 'Planning'
})

const selectedEpicCount = computed(() => {
  const prd = selectedPrd.value
  if (!prd) return 0
  return epics.value.filter((epic) => epic.prd_id === prd.id).length
})

const selectedFeatureCount = computed(() => {
  const prd = selectedPrd.value
  if (!prd) return 0
  const epicIds = new Set(
    epics.value.filter((epic) => epic.prd_id === prd.id).map((epic) => epic.id),
  )
  return features.value.filter((feature) => epicIds.has(feature.epic_id)).length
})

const featureItems = computed(() => {
  const map = new Map<string, number>()
  for (const item of workItems.value) {
    if (item.feature_id) map.set(item.feature_id, (map.get(item.feature_id) ?? 0) + 1)
  }
  return map
})

const featureTests = computed(() => {
  const map = new Map<string, number>()
  for (const testCase of testCases.value) {
    if (testCase.feature_id) map.set(testCase.feature_id, (map.get(testCase.feature_id) ?? 0) + 1)
  }
  return map
})

function featuresForEpic(epicId: string): Feature[] {
  return features.value.filter((feature) => feature.epic_id === epicId)
}

function prdForEpic(epic: Epic): PRD | undefined {
  return prds.value.find((prd) => prd.id === epic.prd_id)
}

function memberName(userId: string | null): string {
  if (!userId) return 'Unassigned'
  const member = members.value.find((entry) => entry.user_id === userId)
  return member?.username || member?.email || 'Team member'
}

function releaseName(releaseId: string | null): string {
  if (!releaseId) return 'No release'
  return store.releases.find((release) => release.id === releaseId)?.name ?? 'Release'
}

function epicProgress(epicId: string): number {
  const epicFeatures = featuresForEpic(epicId)
  if (!epicFeatures.length) return 0
  const done = epicFeatures.filter((feature) => ['done', 'released'].includes(feature.status)).length
  return Math.round((done / epicFeatures.length) * 100)
}

function toggleEpic(epicId: string): void {
  const next = new Set(collapsedEpics.value)
  if (next.has(epicId)) next.delete(epicId)
  else next.add(epicId)
  collapsedEpics.value = next
}

function toggleContent(prdId: string): void {
  const next = new Set(showPrdContent.value)
  if (next.has(prdId)) next.delete(prdId)
  else next.add(prdId)
  showPrdContent.value = next
}

function openBoardForFeature(featureId: string): void {
  void router.push({
    name: 'workspace',
    params: { id: store.projectId },
    query: { tab: 'board', feature: featureId },
  })
}

// ---------------------------------------------------------------------------
// PRD form
// ---------------------------------------------------------------------------

const prdModal = ref(false)
const prdEditingId = ref<string | null>(null)
const prdForm = reactive({
  title: '',
  version: '1.0',
  status: 'draft',
  summary: '',
  content: '',
  owner_id: '',
})

function openCreatePrd(): void {
  prdEditingId.value = null
  Object.assign(prdForm, {
    title: '',
    version: '1.0',
    status: 'draft',
    summary: '',
    content: '',
    owner_id: '',
  })
  formError.value = ''
  prdModal.value = true
}

function openEditPrd(prd: PRD): void {
  prdEditingId.value = prd.id
  Object.assign(prdForm, {
    title: prd.title,
    version: prd.version,
    status: prd.status,
    summary: prd.summary,
    content: prd.content,
    owner_id: prd.owner_id ?? '',
  })
  formError.value = ''
  prdModal.value = true
}

async function savePrd(): Promise<void> {
  if (!prdForm.title.trim()) {
    formError.value = 'A PRD title is required.'
    return
  }
  busy.value = true
  formError.value = ''
  try {
    const payload = {
      title: prdForm.title.trim(),
      version: prdForm.version.trim() || '1.0',
      status: prdForm.status,
      summary: prdForm.summary,
      content: prdForm.content,
      owner_id: prdForm.owner_id || null,
    }
    if (prdEditingId.value) {
      await api.updatePRD(store.projectId, prdEditingId.value, payload)
      toast.success('PRD updated.', 'Planning')
    } else {
      const result = await api.createPRD(store.projectId, payload)
      selectedPrdId.value = result.id
      toast.success('PRD created. Add epics to break it down.', 'Planning')
    }
    prdModal.value = false
    await store.refresh()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Failed to save PRD'
  } finally {
    busy.value = false
  }
}

// ---------------------------------------------------------------------------
// Epic form
// ---------------------------------------------------------------------------

const epicModal = ref(false)
const epicEditingId = ref<string | null>(null)
const epicForm = reactive({
  prd_id: '',
  title: '',
  description: '',
  status: 'draft',
  priority: 'medium',
  owner_id: '',
  target_release_id: '',
  start_date: '',
  due_date: '',
})

function openCreateEpic(prdId = ''): void {
  epicEditingId.value = null
  Object.assign(epicForm, {
    prd_id: prdId,
    title: '',
    description: '',
    status: 'draft',
    priority: 'medium',
    owner_id: '',
    target_release_id: '',
    start_date: '',
    due_date: '',
  })
  formError.value = ''
  epicModal.value = true
}

function openEditEpic(epic: Epic): void {
  epicEditingId.value = epic.id
  Object.assign(epicForm, {
    prd_id: epic.prd_id ?? '',
    title: epic.title,
    description: epic.description,
    status: epic.status,
    priority: epic.priority,
    owner_id: epic.owner_id ?? '',
    target_release_id: epic.target_release_id ?? '',
    start_date: dateInputValue(epic.start_date),
    due_date: dateInputValue(epic.due_date),
  })
  formError.value = ''
  epicModal.value = true
}

async function saveEpic(): Promise<void> {
  if (!epicForm.title.trim()) {
    formError.value = 'An epic title is required.'
    return
  }
  busy.value = true
  formError.value = ''
  try {
    const payload = {
      prd_id: epicForm.prd_id || null,
      title: epicForm.title.trim(),
      description: epicForm.description,
      status: epicForm.status,
      priority: epicForm.priority,
      owner_id: epicForm.owner_id || null,
      target_release_id: epicForm.target_release_id || null,
      start_date: epicForm.start_date || null,
      due_date: epicForm.due_date || null,
    }
    if (epicEditingId.value) {
      await api.updateEpic(store.projectId, epicEditingId.value, payload)
      toast.success('Epic updated.', 'Planning')
    } else {
      await api.createEpic(store.projectId, payload)
      toast.success('Epic created. Add features next.', 'Planning')
    }
    epicModal.value = false
    await store.refresh()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Failed to save epic'
  } finally {
    busy.value = false
  }
}

// ---------------------------------------------------------------------------
// Feature form
// ---------------------------------------------------------------------------

const featureModal = ref(false)
const featureEditingId = ref<string | null>(null)
const featureForm = reactive({
  epic_id: '',
  title: '',
  description: '',
  acceptance_criteria: '',
  status: 'draft',
  priority: 'medium',
  owner_id: '',
  estimate_points: '',
  target_release_id: '',
})

function openCreateFeature(epicId = ''): void {
  featureEditingId.value = null
  Object.assign(featureForm, {
    epic_id: epicId || visibleEpics.value[0]?.id || epics.value[0]?.id || '',
    title: '',
    description: '',
    acceptance_criteria: '',
    status: 'draft',
    priority: 'medium',
    owner_id: '',
    estimate_points: '',
    target_release_id: '',
  })
  formError.value = ''
  featureModal.value = true
}

function openEditFeature(feature: Feature): void {
  featureEditingId.value = feature.id
  Object.assign(featureForm, {
    epic_id: feature.epic_id,
    title: feature.title,
    description: feature.description,
    acceptance_criteria: feature.acceptance_criteria,
    status: feature.status,
    priority: feature.priority,
    owner_id: feature.owner_id ?? '',
    estimate_points: feature.estimate_points === null ? '' : String(feature.estimate_points),
    target_release_id: feature.target_release_id ?? '',
  })
  formError.value = ''
  featureModal.value = true
}

async function saveFeature(): Promise<void> {
  if (!featureForm.title.trim()) {
    formError.value = 'A feature title is required.'
    return
  }
  if (!featureForm.epic_id) {
    formError.value = 'Choose the epic this feature belongs to.'
    return
  }
  busy.value = true
  formError.value = ''
  try {
    const estimate = String(featureForm.estimate_points ?? '').trim()
    const payload = {
      epic_id: featureForm.epic_id,
      title: featureForm.title.trim(),
      description: featureForm.description,
      acceptance_criteria: featureForm.acceptance_criteria,
      status: featureForm.status,
      priority: featureForm.priority,
      owner_id: featureForm.owner_id || null,
      estimate_points: estimate ? Number(estimate) : null,
      target_release_id: featureForm.target_release_id || null,
    }
    if (featureEditingId.value) {
      await api.updateFeature(store.projectId, featureEditingId.value, payload)
      toast.success('Feature updated.', 'Planning')
    } else {
      await api.createFeature(store.projectId, payload)
      toast.success('Feature created. It can now receive work items and tests.', 'Planning')
    }
    featureModal.value = false
    await store.refresh()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'Failed to save feature'
  } finally {
    busy.value = false
  }
}

// ---------------------------------------------------------------------------
// Delete flow
// ---------------------------------------------------------------------------

type DeleteTarget = { type: 'prd' | 'epic' | 'feature'; id: string; title: string }
const deleteTarget = ref<DeleteTarget | null>(null)
const deleteOpen = ref(false)

function askDelete(type: DeleteTarget['type'], id: string, title: string): void {
  deleteTarget.value = { type, id, title }
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  busy.value = true
  try {
    if (target.type === 'prd') await api.deletePRD(store.projectId, target.id)
    if (target.type === 'epic') await api.deleteEpic(store.projectId, target.id)
    if (target.type === 'feature') await api.deleteFeature(store.projectId, target.id)
    toast.success(`${target.title} deleted.`, 'Planning')
    deleteOpen.value = false
    deleteTarget.value = null
    if (selectedPrdId.value === target.id) selectedPrdId.value = 'all'
    await store.refresh()
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Delete failed', 'Planning')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight text-slate-900">Product planning</h2>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn-secondary" @click="openCreatePrd">
          <AppIcon name="folder" :size="15" />
          New PRD
        </button>
        <button type="button" class="btn-secondary" :disabled="!prds.length" @click="openCreateEpic(selectedPrdId === 'all' ? '' : selectedPrdId)">
          <AppIcon name="flag" :size="15" />
          New epic
        </button>
        <button type="button" class="btn-primary" :disabled="!epics.length" @click="openCreateFeature()">
          <AppIcon name="layers" :size="15" />
          New feature
        </button>
      </div>
    </div>

    <div class="grid gap-5 lg:grid-cols-12">
      <aside class="space-y-3 lg:col-span-4">
        <button
          type="button"
          class="w-full rounded-xl border p-4 text-left"
          :class="selectedPrdId === 'all' ? 'border-indigo-300 bg-indigo-50/70 ring-1 ring-indigo-200' : 'border-slate-200 bg-white hover:bg-slate-50'"
          @click="selectedPrdId = 'all'"
        >
          <div class="flex items-center gap-3">
            <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-indigo-600 text-white">
              <AppIcon name="layers" :size="17" />
            </span>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-semibold text-slate-900">Full planning hierarchy</p>
              <p class="mt-0.5 text-xs text-slate-600">{{ prds.length }} PRDs · {{ epics.length }} epics · {{ features.length }} features</p>
            </div>
            <AppIcon name="chevronRight" :size="15" class="text-slate-600" />
          </div>
        </button>

        <div class="flex items-center justify-between px-1 pt-2">
          <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-600">Requirements</p>
          <span class="text-[11px] text-slate-600">{{ prds.length }}</span>
        </div>

        <div v-if="!prds.length" class="rounded-xl border border-dashed border-slate-300 bg-white p-5 text-center">
          <p class="text-xs font-medium text-slate-600">No PRD yet</p>
          <button type="button" class="mt-2 text-xs font-semibold text-indigo-600 hover:text-indigo-700" @click="openCreatePrd">
            Create the first requirement
          </button>
        </div>

        <button
          v-for="prd in prds"
          :key="prd.id"
          type="button"
          class="w-full rounded-xl border bg-white p-3.5 text-left hover:border-slate-300 hover:bg-slate-50"
          :class="selectedPrdId === prd.id ? 'border-indigo-300 ring-1 ring-indigo-200' : 'border-slate-200'"
          @click="selectedPrdId = prd.id"
        >
          <div class="flex items-start justify-between gap-2">
            <p class="min-w-0 flex-1 truncate text-sm font-semibold text-slate-900">{{ prd.title }}</p>
            <StatusPill :status="prd.status" />
          </div>
          <p class="mt-1 line-clamp-2 text-xs leading-relaxed text-slate-600">
            {{ prd.summary || 'No summary yet.' }}
          </p>
          <div class="mt-2.5 flex items-center justify-between text-[10px] text-slate-600">
            <span>v{{ prd.version }} · {{ epics.filter((epic) => epic.prd_id === prd.id).length }} epics</span>
            <span>{{ timeAgo(prd.updated_at) }}</span>
          </div>
        </button>

        <button
          v-if="unlinkedEpicCount"
          type="button"
          class="w-full rounded-xl border border-amber-200 bg-amber-50/70 p-3.5 text-left"
          :class="selectedPrdId === 'unlinked' ? 'ring-1 ring-amber-300' : ''"
          @click="selectedPrdId = 'unlinked'"
        >
          <div class="flex items-center gap-2 text-xs font-semibold text-amber-800">
            <AppIcon name="alert" :size="14" />
            Unlinked epics
            <span class="ml-auto rounded-full bg-amber-100 px-1.5 py-0.5 text-[10px]">{{ unlinkedEpicCount }}</span>
          </div>
          <p class="mt-1 text-[11px] leading-relaxed text-amber-700">These epics are missing a PRD link.</p>
        </button>
      </aside>

      <section class="space-y-4 lg:col-span-8">
        <div v-if="selectedPrd" class="card p-5">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <h3 class="text-base font-semibold text-slate-900">{{ selectedPrd.title }}</h3>
                <StatusPill :status="selectedPrd.status" />
                <span class="rounded-full bg-slate-100 px-2 py-0.5 text-[11px] font-medium text-slate-600">v{{ selectedPrd.version }}</span>
              </div>
              <p class="mt-1 text-xs text-slate-600">
                Owner {{ memberName(selectedPrd.owner_id) }} · Updated {{ formatDateTime(selectedPrd.updated_at) }}
              </p>
            </div>
            <div class="flex gap-1">
              <button type="button" class="btn-ghost" @click="openEditPrd(selectedPrd)">
                <AppIcon name="edit" :size="14" /> Edit
              </button>
              <button type="button" class="btn-ghost text-red-600 hover:bg-red-50" @click="askDelete('prd', selectedPrd.id, selectedPrd.title)">
                <AppIcon name="trash" :size="14" /> Delete
              </button>
            </div>
          </div>
          <p class="mt-3 text-sm leading-relaxed text-slate-600">{{ selectedPrd.summary || 'No summary provided.' }}</p>
          <div v-if="selectedPrd.content" class="mt-3">
            <button type="button" class="text-xs font-semibold text-indigo-600 hover:text-indigo-700" @click="toggleContent(selectedPrd.id)">
              {{ showPrdContent.has(selectedPrd.id) ? 'Hide requirement content' : 'Read requirement content' }}
            </button>
            <pre
              v-if="showPrdContent.has(selectedPrd.id)"
              class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs leading-relaxed text-slate-700"
            >{{ selectedPrd.content }}</pre>
          </div>
          <div class="mt-4 flex flex-wrap gap-2">
            <button type="button" class="btn-secondary" @click="openCreateEpic(selectedPrd.id)">
              <AppIcon name="plus" :size="14" /> Add epic
            </button>
            <span class="inline-flex items-center rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-600">
              {{ selectedEpicCount }} epics · {{ selectedFeatureCount }} features
            </span>
          </div>
        </div>

        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-sm font-semibold text-slate-900">{{ selectedTitle }}</h3>
            <p class="mt-0.5 text-xs text-slate-600">
              {{ visibleEpics.length }} epics · {{ features.filter((feature) => visibleEpics.some((epic) => epic.id === feature.epic_id)).length }} features
            </p>
          </div>
          <button type="button" class="btn-ghost" @click="openCreateEpic(selectedPrdId === 'all' || selectedPrdId === 'unlinked' ? '' : selectedPrdId)">
            <AppIcon name="plus" :size="14" /> Epic
          </button>
        </div>

        <EmptyState
          v-if="!visibleEpics.length"
          icon="flag"
          title="No epics in this view"
          :action-label="prds.length ? 'Create epic' : 'Create PRD'"
          @action="prds.length ? openCreateEpic(selectedPrdId === 'all' ? '' : selectedPrdId) : openCreatePrd()"
        />

        <div v-for="epic in visibleEpics" :key="epic.id" class="card overflow-hidden">
          <div class="flex flex-wrap items-start gap-3 border-b border-slate-100 px-5 py-4">
            <button
              type="button"
              class="mt-0.5 flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-slate-100 text-slate-600 hover:bg-slate-200"
              :aria-label="collapsedEpics.has(epic.id) ? 'Expand epic' : 'Collapse epic'"
              @click="toggleEpic(epic.id)"
            >
              <AppIcon :name="collapsedEpics.has(epic.id) ? 'chevronRight' : 'chevronDown'" :size="15" />
            </button>
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <h4 class="text-sm font-semibold text-slate-900">{{ epic.title }}</h4>
                <StatusPill :status="epic.status" />
                <PriorityPill :priority="epic.priority" />
              </div>
              <p class="mt-1 line-clamp-2 text-xs leading-relaxed text-slate-600">{{ epic.description || 'No epic description.' }}</p>
              <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
                <span v-if="prdForEpic(epic)" class="inline-flex items-center gap-1 text-indigo-600">
                  <AppIcon name="folder" :size="11" />
                  {{ prdForEpic(epic)?.title }}
                </span>
                <span v-else class="inline-flex items-center gap-1 text-amber-700">
                  <AppIcon name="alert" :size="11" /> No PRD
                </span>
                <span>{{ featuresForEpic(epic.id).length }} features</span>
                <span>{{ memberName(epic.owner_id) }}</span>
                <span>{{ releaseName(epic.target_release_id) }}</span>
              </div>
            </div>
            <div class="flex items-center gap-3">
              <div class="hidden w-24 sm:block">
                <div class="flex items-center justify-between text-[10px] font-medium text-slate-600">
                  <span>Features done</span>
                  <span>{{ epicProgress(epic.id) }}%</span>
                </div>
                <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-slate-100">
                  <div class="h-full rounded-full bg-emerald-500" :style="{ width: `${epicProgress(epic.id)}%` }" />
                </div>
              </div>
              <div class="flex gap-0.5">
                <button type="button" class="btn-ghost px-2" title="Add feature" @click="openCreateFeature(epic.id)">
                  <AppIcon name="plus" :size="14" />
                </button>
                <button type="button" class="btn-ghost px-2" title="Edit epic" @click="openEditEpic(epic)">
                  <AppIcon name="edit" :size="14" />
                </button>
                <button type="button" class="btn-ghost px-2 text-red-500 hover:bg-red-50" title="Delete epic" @click="askDelete('epic', epic.id, epic.title)">
                  <AppIcon name="trash" :size="14" />
                </button>
              </div>
            </div>
          </div>

          <div v-if="!collapsedEpics.has(epic.id)" class="divide-y divide-slate-100">
            <div
              v-for="feature in featuresForEpic(epic.id)"
              :key="feature.id"
              class="group flex flex-wrap items-center gap-3 px-5 py-3 hover:bg-slate-50/70"
            >
              <span class="flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                <AppIcon name="layers" :size="15" />
              </span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <button type="button" class="truncate text-sm font-medium text-slate-800 hover:text-indigo-700" @click="openEditFeature(feature)">
                    {{ feature.title }}
                  </button>
                  <StatusPill :status="feature.status" />
                  <PriorityPill :priority="feature.priority" />
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
                  <span>{{ featureItems.get(feature.id) ?? 0 }} work items</span>
                  <span>{{ featureTests.get(feature.id) ?? 0 }} tests</span>
                  <span v-if="feature.estimate_points !== null">{{ feature.estimate_points }} pts</span>
                  <span>{{ memberName(feature.owner_id) }}</span>
                  <span>{{ releaseName(feature.target_release_id) }}</span>
                </div>
              </div>
              <div class="flex items-center gap-0.5">
                <button type="button" class="btn-ghost px-2 text-xs" @click="openBoardForFeature(feature.id)">
                  Board <AppIcon name="arrowRight" :size="12" />
                </button>
                <button type="button" class="btn-ghost px-2" title="Edit feature" @click="openEditFeature(feature)">
                  <AppIcon name="edit" :size="14" />
                </button>
                <button type="button" class="btn-ghost px-2 text-red-500 hover:bg-red-50" title="Delete feature" @click="askDelete('feature', feature.id, feature.title)">
                  <AppIcon name="trash" :size="14" />
                </button>
              </div>
            </div>
            <div v-if="!featuresForEpic(epic.id).length" class="px-5 py-5 text-center">
              <button type="button" class="btn-secondary" @click="openCreateFeature(epic.id)">
                <AppIcon name="plus" :size="13" /> Add feature
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- PRD modal -->
    <BaseModal
      v-model="prdModal"
      :title="prdEditingId ? 'Edit PRD' : 'New product requirement'"
      max-width="lg"
      :persistent="busy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="prd-title">Title</label>
          <input id="prd-title" v-model="prdForm.title" data-autofocus class="field-input" placeholder="Checkout experience v2" />
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="prd-version">Version</label>
            <input id="prd-version" v-model="prdForm.version" class="field-input" placeholder="1.0" />
          </div>
          <div>
            <label class="field-label" for="prd-status">Status</label>
            <select id="prd-status" v-model="prdForm.status" class="field-input">
              <option value="draft">Draft</option>
              <option value="in_review">In review</option>
              <option value="approved">Approved</option>
              <option value="superseded">Superseded</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="prd-owner">Owner</label>
            <select id="prd-owner" v-model="prdForm.owner_id" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="prd-summary">Summary</label>
          <textarea id="prd-summary" v-model="prdForm.summary" rows="2" class="field-input" placeholder="What outcome does this requirement create?" />
        </div>
        <div>
          <label class="field-label" for="prd-content">Requirement content</label>
          <textarea id="prd-content" v-model="prdForm.content" rows="7" class="field-input" placeholder="Problem, users, scope, non-goals, acceptance criteria…" />
        </div>
        <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ formError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="busy" @click="prdModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="busy" @click="savePrd">
          <AppIcon v-if="busy" name="loader" :size="14" class="animate-spin" />
          {{ busy ? 'Saving…' : prdEditingId ? 'Save PRD' : 'Create PRD' }}
        </button>
      </template>
    </BaseModal>

    <!-- Epic modal -->
    <BaseModal
      v-model="epicModal"
      :title="epicEditingId ? 'Edit epic' : 'New epic'"
      max-width="lg"
      :persistent="busy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="epic-title">Title</label>
          <input id="epic-title" v-model="epicForm.title" data-autofocus class="field-input" placeholder="Frictionless checkout" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="epic-prd">PRD</label>
            <select id="epic-prd" v-model="epicForm.prd_id" class="field-input">
              <option value="">No PRD</option>
              <option v-for="prd in prds" :key="prd.id" :value="prd.id">{{ prd.title }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="epic-release">Target release</label>
            <select id="epic-release" v-model="epicForm.target_release_id" class="field-input">
              <option value="">No release</option>
              <option v-for="release in store.releases" :key="release.id" :value="release.id">{{ release.name }} · {{ release.version }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="epic-description">Description</label>
          <textarea id="epic-description" v-model="epicForm.description" rows="3" class="field-input" placeholder="Outcome, scope and success measures" />
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="epic-status">Status</label>
            <select id="epic-status" v-model="epicForm.status" class="field-input">
              <option v-for="status in ['draft', 'planned', 'in_progress', 'done', 'cancelled']" :key="status" :value="status">{{ statusLabel(status) }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="epic-priority">Priority</label>
            <select id="epic-priority" v-model="epicForm.priority" class="field-input">
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="epic-owner">Owner</label>
            <select id="epic-owner" v-model="epicForm.owner_id" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
            </select>
          </div>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="epic-start">Start date</label>
            <input id="epic-start" v-model="epicForm.start_date" type="date" class="field-input" />
          </div>
          <div>
            <label class="field-label" for="epic-due">Due date</label>
            <input id="epic-due" v-model="epicForm.due_date" type="date" class="field-input" />
          </div>
        </div>
        <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ formError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="busy" @click="epicModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="busy" @click="saveEpic">
          <AppIcon v-if="busy" name="loader" :size="14" class="animate-spin" />
          {{ busy ? 'Saving…' : epicEditingId ? 'Save epic' : 'Create epic' }}
        </button>
      </template>
    </BaseModal>

    <!-- Feature modal -->
    <BaseModal
      v-model="featureModal"
      :title="featureEditingId ? 'Edit feature' : 'New feature'"
      max-width="lg"
      :persistent="busy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="feature-title">Title</label>
          <input id="feature-title" v-model="featureForm.title" data-autofocus class="field-input" placeholder="One-tap payment" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="feature-epic">Epic</label>
            <select id="feature-epic" v-model="featureForm.epic_id" class="field-input">
              <option value="">Select an epic</option>
              <option v-for="epic in epics" :key="epic.id" :value="epic.id">{{ epic.title }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="feature-release">Target release</label>
            <select id="feature-release" v-model="featureForm.target_release_id" class="field-input">
              <option value="">No release</option>
              <option v-for="release in store.releases" :key="release.id" :value="release.id">{{ release.name }} · {{ release.version }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="feature-description">Description</label>
          <textarea id="feature-description" v-model="featureForm.description" rows="3" class="field-input" placeholder="What the user can do and why it matters" />
        </div>
        <div>
          <label class="field-label" for="feature-ac">Acceptance criteria</label>
          <textarea id="feature-ac" v-model="featureForm.acceptance_criteria" rows="3" class="field-input" placeholder="Given / when / then, or a checklist of outcomes" />
        </div>
        <div class="grid gap-4 sm:grid-cols-4">
          <div>
            <label class="field-label" for="feature-status">Status</label>
            <select id="feature-status" v-model="featureForm.status" class="field-input">
              <option v-for="status in ['draft', 'ready', 'in_progress', 'in_review', 'in_qa', 'done', 'released', 'cancelled']" :key="status" :value="status">{{ statusLabel(status) }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="feature-priority">Priority</label>
            <select id="feature-priority" v-model="featureForm.priority" class="field-input">
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="feature-estimate">Estimate</label>
            <input id="feature-estimate" v-model="featureForm.estimate_points" type="number" min="0" step="0.5" class="field-input" placeholder="Points" />
          </div>
          <div>
            <label class="field-label" for="feature-owner">Owner</label>
            <select id="feature-owner" v-model="featureForm.owner_id" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
            </select>
          </div>
        </div>
        <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ formError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="busy" @click="featureModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="busy" @click="saveFeature">
          <AppIcon v-if="busy" name="loader" :size="14" class="animate-spin" />
          {{ busy ? 'Saving…' : featureEditingId ? 'Save feature' : 'Create feature' }}
        </button>
      </template>
    </BaseModal>

    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete planning artifact?"
      :message="
        deleteTarget?.type === 'prd'
          ? `Deleting ${deleteTarget?.title} will unlink its epics. The epics themselves are kept.`
          : deleteTarget?.type === 'epic'
            ? `Deleting ${deleteTarget?.title} will also delete its features. Work items and tests will be unlinked, not deleted.`
            : `Deleting ${deleteTarget?.title} will unlink its work items and test cases. They will not be deleted.`
      "
      :busy="busy"
      confirm-label="Delete"
      @confirm="confirmDelete"
    />
  </div>
</template>
