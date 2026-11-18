<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BaseModal from '../BaseModal.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import PriorityPill from './PriorityPill.vue'
import EmptyState from './EmptyState.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { api } from '../../api/client'
import { percent, statusLabel } from '../../lib/workspace'
import { formatDateTime, timeAgo } from '../../lib/format'
import type { TestCase, TestResult, TestRun, TestStep, TestStepResult } from '../../types/workspace'

const store = useWorkspaceStore()
const toast = useToastStore()
const route = useRoute()
const router = useRouter()

const caseSearch = ref('')
const caseFeatureFilter = ref('')
const caseModal = ref(false)
const caseEditingId = ref<string | null>(null)
const caseBusy = ref(false)
const caseError = ref('')

const runModal = ref(false)
const runBusy = ref(false)
const runError = ref('')

const detailOpen = ref(false)
const selectedRunId = ref<string | null>(null)
const resultNotes = reactive<Record<string, string>>({})
const stepNotes = reactive<Record<string, string>>({})
const stepEvidence = reactive<Record<string, string>>({})
const resultBusy = ref<string | null>(null)

const bugOpen = ref(false)
const bugBusy = ref(false)
const bugError = ref('')
const bugResultId = ref<string | null>(null)
const bugStepResultId = ref<string | null>(null)
const bugForm = reactive({
  title: '',
  description: '',
  severity: 'major',
  priority: 'high',
  reproduction_steps: '',
  expected_result: '',
  actual_result: '',
})

const testCases = computed(() => store.testCases)
const testRuns = computed(() => store.testRuns)
const features = computed(() => store.features)
const members = computed(() => store.workspace?.members ?? [])

const selectedRun = computed(() =>
  selectedRunId.value ? store.testRuns.find((run) => run.id === selectedRunId.value) ?? null : null,
)

const filteredCases = computed(() => {
  const query = caseSearch.value.trim().toLowerCase()
  return testCases.value.filter((testCase) => {
    if (caseFeatureFilter.value && testCase.feature_id !== caseFeatureFilter.value) return false
    if (!query) return true
    return `${testCase.title} ${testCase.description} ${testCase.automation_ref}`
      .toLowerCase()
      .includes(query)
  })
})

const sortedRuns = computed(() =>
  [...testRuns.value].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  ),
)

const stepById = computed(() => {
  const map = new Map<string, TestStep>()
  for (const testCase of testCases.value) {
    for (const step of testCase.steps ?? []) map.set(step.id, step)
  }
  return map
})

const caseById = computed(() => {
  const map = new Map<string, TestCase>()
  for (const testCase of testCases.value) map.set(testCase.id, testCase)
  return map
})

const memberNames = computed(() => {
  const map = new Map<string, string>()
  for (const member of members.value) map.set(member.user_id, member.username || member.email)
  return map
})

function featureName(featureId: string | null): string {
  if (!featureId) return 'No feature'
  return features.value.find((feature) => feature.id === featureId)?.title ?? 'Unknown feature'
}

function environmentName(environmentId: string | null): string {
  if (!environmentId) return 'No environment'
  return store.workspace?.environments.find((environment) => environment.id === environmentId)?.name ?? 'Environment'
}

function ownerName(ownerId: string | null): string {
  if (!ownerId) return 'Unassigned'
  return memberNames.value.get(ownerId) ?? 'Team member'
}

function caseTitle(caseId: string): string {
  return caseById.value.get(caseId)?.title ?? 'Test case'
}

function caseCountLabel(count: number): string {
  return `${count} ${count === 1 ? 'case' : 'cases'}`
}

function resultCounts(run: TestRun) {
  const results = run.results ?? []
  return {
    total: results.length,
    passed: results.filter((result) => result.status === 'passed').length,
    failed: results.filter((result) => result.status === 'failed').length,
    notRun: results.filter((result) => result.status === 'not_run').length,
    blocked: results.filter((result) => result.status === 'blocked').length,
    skipped: results.filter((result) => result.status === 'skipped').length,
  }
}

function runProgress(run: TestRun): number {
  const counts = resultCounts(run)
  if (!counts.total) return 0
  return Math.round(((counts.total - counts.notRun) / counts.total) * 100)
}

function stepFor(stepResult: TestStepResult): TestStep | undefined {
  return stepById.value.get(stepResult.test_step_id)
}

// ---------------------------------------------------------------------------
// Test case form
// ---------------------------------------------------------------------------

const caseForm = reactive({
  feature_id: '',
  title: '',
  description: '',
  preconditions: '',
  type: 'manual',
  priority: 'medium',
  status: 'draft',
  automation_ref: '',
  owner_id: '',
  tags: '',
  steps: [{ action: '', expected_result: '', test_data: '' }] as Array<{
    action: string
    expected_result: string
    test_data: string
  }>,
})

function resetCaseForm(): void {
  Object.assign(caseForm, {
    feature_id: features.value[0]?.id ?? '',
    title: '',
    description: '',
    preconditions: '',
    type: 'manual',
    priority: 'medium',
    status: 'draft',
    automation_ref: '',
    owner_id: '',
    tags: '',
    steps: [{ action: '', expected_result: '', test_data: '' }],
  })
}

function openCreateCase(): void {
  caseEditingId.value = null
  resetCaseForm()
  caseError.value = ''
  caseModal.value = true
}

function openEditCase(testCase: TestCase): void {
  caseEditingId.value = testCase.id
  Object.assign(caseForm, {
    feature_id: testCase.feature_id ?? '',
    title: testCase.title,
    description: testCase.description,
    preconditions: testCase.preconditions,
    type: testCase.type,
    priority: testCase.priority,
    status: testCase.status,
    automation_ref: testCase.automation_ref,
    owner_id: testCase.owner_id ?? '',
    tags: (testCase.tags ?? []).join(', '),
    steps: (testCase.steps ?? []).map((step) => ({
      action: step.action,
      expected_result: step.expected_result,
      test_data: step.test_data,
    })),
  })
  if (!caseForm.steps.length) caseForm.steps = [{ action: '', expected_result: '', test_data: '' }]
  caseError.value = ''
  caseModal.value = true
}

function addStep(): void {
  caseForm.steps.push({ action: '', expected_result: '', test_data: '' })
}

function removeStep(index: number): void {
  if (caseForm.steps.length <= 1) return
  caseForm.steps.splice(index, 1)
}

async function saveCase(): Promise<void> {
  if (!caseForm.title.trim()) {
    caseError.value = 'A test case title is required.'
    return
  }
  if (!caseForm.feature_id) {
    caseError.value = 'A test case must link to a feature.'
    return
  }
  if (!caseForm.steps.length) {
    caseError.value = 'A test case needs at least one step.'
    return
  }
  if (caseForm.steps.some((step) => !step.action.trim() || !step.expected_result.trim())) {
    caseError.value = 'Every test step needs an action and an expected result.'
    return
  }
  caseBusy.value = true
  caseError.value = ''
  try {
    const payload = {
      feature_id: caseForm.feature_id,
      title: caseForm.title.trim(),
      description: caseForm.description,
      preconditions: caseForm.preconditions,
      type: caseForm.type,
      priority: caseForm.priority,
      status: caseForm.status,
      automation_ref: caseForm.automation_ref,
      owner_id: caseForm.owner_id || null,
      tags: caseForm.tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      steps: caseForm.steps.map((step) => ({
        action: step.action.trim(),
        expected_result: step.expected_result.trim(),
        test_data: step.test_data,
      })),
    }
    if (caseEditingId.value) {
      await api.updateTestCase(store.projectId, caseEditingId.value, payload)
      toast.success('Test case updated.', 'Test lab')
    } else {
      await api.createTestCase(store.projectId, payload)
      toast.success('Test case created and linked to its feature.', 'Test lab')
    }
    caseModal.value = false
    await store.refresh()
  } catch (error) {
    caseError.value = error instanceof Error ? error.message : 'Failed to save test case'
  } finally {
    caseBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Test run form
// ---------------------------------------------------------------------------

const runForm = reactive({
  name: '',
  environment_id: '',
  release_id: '',
  sprint_id: '',
  build_id: '',
  test_case_ids: [] as string[],
})

function openCreateRun(): void {
  const environments = store.workspace?.environments ?? []
  Object.assign(runForm, {
    name: `Regression · ${new Date().toISOString().slice(0, 10)}`,
    environment_id:
      environments.find((environment) => environment.type === 'staging')?.id ??
      environments.find((environment) => environment.type === 'production')?.id ??
      environments[0]?.id ??
      '',
    release_id: '',
    sprint_id: store.sprints.find((sprint) => sprint.status === 'active')?.id ?? '',
    build_id: '',
    test_case_ids: [],
  })
  runError.value = ''
  runModal.value = true
}

function toggleCaseSelection(caseId: string): void {
  if (runForm.test_case_ids.includes(caseId)) {
    runForm.test_case_ids = runForm.test_case_ids.filter((id) => id !== caseId)
  } else {
    runForm.test_case_ids = [...runForm.test_case_ids, caseId]
  }
}

async function saveRun(): Promise<void> {
  if (!runForm.environment_id) {
    runError.value = 'A test run must link to an environment.'
    return
  }
  if (!testCases.value.length) {
    runError.value = 'Create at least one test case before starting a run.'
    return
  }
  runBusy.value = true
  runError.value = ''
  try {
    const result = await api.createTestRun(store.projectId, {
      name: runForm.name.trim(),
      environment_id: runForm.environment_id,
      release_id: runForm.release_id || null,
      sprint_id: runForm.sprint_id || null,
      build_id: runForm.build_id || null,
      test_case_ids: runForm.test_case_ids,
    })
    runModal.value = false
    await store.refresh()
    openRun(result.id)
    toast.success('Test run created. Record each step to build evidence.', 'Test lab')
  } catch (error) {
    runError.value = error instanceof Error ? error.message : 'Failed to create test run'
  } finally {
    runBusy.value = false
  }
}

function openRun(runId: string): void {
  selectedRunId.value = runId
  const run = store.testRuns.find((entry) => entry.id === runId)
  for (const result of run?.results ?? []) {
    if (resultNotes[result.id] === undefined) resultNotes[result.id] = result.actual_result
    for (const stepResult of result.step_results ?? []) {
      if (stepNotes[stepResult.id] === undefined) stepNotes[stepResult.id] = stepResult.actual_result
      if (stepEvidence[stepResult.id] === undefined) stepEvidence[stepResult.id] = stepResult.evidence_url
    }
  }
  detailOpen.value = true
}

function applyDeepLink(): void {
  const runId = String(route.query.run ?? '')
  if (runId && store.testRuns.some((run) => run.id === runId)) {
    openRun(runId)
    return
  }
  const caseId = String(route.query.case ?? '')
  if (caseId) {
    const testCase = store.testCases.find((entry) => entry.id === caseId)
    if (testCase) openEditCase(testCase)
  }
}

function openRunBuild(run: TestRun): void {
  if (!run.build_id) return
  void router.push({
    name: 'workspace',
    params: { id: store.projectId },
    query: { tab: 'delivery', build: run.build_id },
  })
}

onMounted(applyDeepLink)
watch(() => route.query, applyDeepLink)

async function patchResult(result: TestResult, status: string): Promise<void> {
  if (!selectedRun.value || resultBusy.value) return
  resultBusy.value = result.id
  try {
    await api.patchTestResult(store.projectId, selectedRun.value.id, result.id, {
      status,
      actual_result: resultNotes[result.id] ?? result.actual_result,
      duration_ms: result.duration_ms,
      bug_work_item_id: result.bug_work_item_id,
    })
    await store.refresh()
    toast.success(`${caseTitle(result.test_case_id)} marked ${statusLabel(status)}.`, 'Test result')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to record result', 'Test lab')
  } finally {
    resultBusy.value = null
  }
}

async function patchStep(
  result: TestResult,
  stepResult: TestStepResult,
  status: string,
): Promise<void> {
  if (!selectedRun.value || resultBusy.value) return
  resultBusy.value = stepResult.id
  try {
    await api.patchTestStepResult(
      store.projectId,
      selectedRun.value.id,
      result.id,
      stepResult.id,
      {
        status,
        actual_result: stepNotes[stepResult.id] ?? stepResult.actual_result,
        evidence_url: stepEvidence[stepResult.id] ?? stepResult.evidence_url,
        bug_work_item_id: stepResult.bug_work_item_id,
      },
    )
    await store.refresh()
    toast.success(`Step marked ${statusLabel(status)}.`, 'Step result')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to record step', 'Test lab')
  } finally {
    resultBusy.value = null
  }
}

// ---------------------------------------------------------------------------
// Bug creation from a failed result / step
// ---------------------------------------------------------------------------

function openBug(result: TestResult, stepResult?: TestStepResult): void {
  const testCase = caseById.value.get(result.test_case_id)
  const step = stepResult ? stepFor(stepResult) : undefined
  bugResultId.value = result.id
  bugStepResultId.value = stepResult?.id ?? null
  Object.assign(bugForm, {
    title: step
      ? `Defect: ${testCase?.title ?? 'Test case'} — step ${step.position}`
      : `Defect: ${testCase?.title ?? 'Test case'}`,
    description: stepResult?.actual_result || result.actual_result || '',
    severity: 'major',
    priority: 'high',
    reproduction_steps: (testCase?.steps ?? [])
      .map((entry, index) => `${index + 1}. ${entry.action}`)
      .join('\n'),
    expected_result: step?.expected_result ?? '',
    actual_result: stepResult?.actual_result || result.actual_result || '',
  })
  bugError.value = ''
  bugOpen.value = true
}

async function saveBug(): Promise<void> {
  if (!selectedRun.value || !bugResultId.value) return
  if (!bugForm.title.trim()) {
    bugError.value = 'A bug title is required.'
    return
  }
  bugBusy.value = true
  bugError.value = ''
  try {
    const result = await api.createBugFromResult(
      store.projectId,
      selectedRun.value.id,
      bugResultId.value,
      {
        title: bugForm.title.trim(),
        description: bugForm.description,
        severity: bugForm.severity || null,
        priority: bugForm.priority,
        assignee_id: null,
        reproduction_steps: bugForm.reproduction_steps,
        expected_result: bugForm.expected_result,
        actual_result: bugForm.actual_result,
        step_result_id: bugStepResultId.value,
      },
    )
    bugOpen.value = false
    await store.refresh()
    toast.success(`${result.key ?? 'Bug'} created and linked to the failed test.`, 'Defect raised')
  } catch (error) {
    bugError.value = error instanceof Error ? error.message : 'Failed to create bug'
  } finally {
    bugBusy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight text-slate-900">Test lab</h2>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn-secondary" :disabled="!features.length" @click="openCreateCase">
          <AppIcon name="flask" :size="15" />
          New test case
        </button>
        <button type="button" class="btn-primary" :disabled="!testCases.length" @click="openCreateRun">
          <AppIcon name="plus" :size="15" />
          New test run
        </button>
      </div>
    </div>

    <div class="grid gap-5 xl:grid-cols-12">
      <section class="xl:col-span-5">
        <div class="card overflow-hidden">
          <div class="border-b border-slate-100 px-5 py-4">
            <div class="flex items-center justify-between gap-2">
              <div>
                <h3 class="text-sm font-semibold text-slate-900">Test cases</h3>
                <p class="mt-0.5 text-xs text-slate-600">{{ testCases.length }} reusable definitions</p>
              </div>
              <span class="badge bg-cyan-50 text-cyan-700 ring-cyan-600/20">{{ filteredCases.length }} shown</span>
            </div>
            <div class="mt-3 flex gap-2">
              <div class="relative flex-1">
                <AppIcon name="search" :size="14" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-600" />
                <input v-model="caseSearch" class="field-input py-2 pl-8 text-xs" placeholder="Search test cases…" />
              </div>
              <select v-model="caseFeatureFilter" class="field-input w-auto max-w-[160px] py-2 text-xs" aria-label="Filter test cases by feature">
                <option value="">All features</option>
                <option v-for="feature in features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
              </select>
            </div>
          </div>

          <div v-if="filteredCases.length" class="max-h-[640px] divide-y divide-slate-100 overflow-y-auto">
            <button
              v-for="testCase in filteredCases"
              :key="testCase.id"
              type="button"
              class="block w-full px-5 py-3.5 text-left hover:bg-slate-50"
              @click="openEditCase(testCase)"
            >
              <div class="flex items-start justify-between gap-2">
                <p class="min-w-0 flex-1 text-sm font-medium leading-snug text-slate-800">{{ testCase.title }}</p>
                <StatusPill :status="testCase.status" />
              </div>
              <p class="mt-1 truncate text-[11px] text-indigo-600">{{ featureName(testCase.feature_id) }}</p>
              <div class="mt-2 flex flex-wrap items-center gap-2 text-[10px] text-slate-600">
                <span class="inline-flex items-center gap-1 rounded bg-slate-100 px-1.5 py-0.5 font-medium capitalize text-slate-600">
                  {{ testCase.type }}
                </span>
                <PriorityPill :priority="testCase.priority" />
                <span>{{ (testCase.steps ?? []).length }} steps</span>
                <span>{{ ownerName(testCase.owner_id) }}</span>
                <span class="ml-auto">{{ timeAgo(testCase.updated_at) }}</span>
              </div>
            </button>
          </div>
          <div v-else class="px-5 py-10">
            <EmptyState
              icon="flask"
              compact
              title="No test cases"
              :action-label="features.length ? 'Create test case' : ''"
              @action="openCreateCase"
            />
          </div>
        </div>
      </section>

      <section class="xl:col-span-7">
        <div class="card overflow-hidden">
          <div class="border-b border-slate-100 px-5 py-4">
            <div class="flex items-center justify-between gap-2">
              <div>
                <h3 class="text-sm font-semibold text-slate-900">Test runs</h3>
              </div>
              <button type="button" class="btn-ghost text-xs" :disabled="!testCases.length" @click="openCreateRun">
                <AppIcon name="plus" :size="13" /> New run
              </button>
            </div>
          </div>

          <div v-if="sortedRuns.length" class="divide-y divide-slate-100">
            <button
              v-for="run in sortedRuns"
              :key="run.id"
              type="button"
              class="block w-full px-5 py-4 text-left hover:bg-slate-50"
              @click="openRun(run.id)"
            >
              <div class="flex flex-wrap items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="truncate text-sm font-semibold text-slate-800">{{ run.name }}</p>
                  <p class="mt-1 text-[11px] text-slate-600">
                    {{ environmentName(run.environment_id) }} ·
                    {{ formatDateTime(run.created_at) }}
                  </p>
                </div>
                <StatusPill :status="run.status" />
              </div>
              <div class="mt-3 flex items-center gap-3">
                <div class="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-100">
                  <div
                    class="h-full rounded-full"
                    :class="resultCounts(run).failed ? 'bg-rose-500' : 'bg-emerald-500'"
                    :style="{ width: `${runProgress(run)}%` }"
                  />
                </div>
                <span class="text-[10px] font-semibold text-slate-600">{{ runProgress(run) }}% executed</span>
              </div>
              <div class="mt-2 flex flex-wrap gap-2 text-[10px]">
                <span class="rounded bg-emerald-50 px-1.5 py-0.5 font-medium text-emerald-700">{{ resultCounts(run).passed }} passed</span>
                <span class="rounded bg-red-50 px-1.5 py-0.5 font-medium text-red-700">{{ resultCounts(run).failed }} failed</span>
                <span class="rounded bg-slate-100 px-1.5 py-0.5 font-medium text-slate-600">{{ resultCounts(run).notRun }} not run</span>
                <span v-if="resultCounts(run).blocked" class="rounded bg-amber-50 px-1.5 py-0.5 font-medium text-amber-700">{{ resultCounts(run).blocked }} blocked</span>
                <span
                  v-if="run.build_id"
                  class="cursor-pointer rounded bg-indigo-50 px-1.5 py-0.5 font-semibold text-indigo-700 hover:bg-indigo-100"
                  role="link"
                  tabindex="0"
                  @click.stop="openRunBuild(run)"
                  @keydown.enter.stop="openRunBuild(run)"
                >CI build</span>
                <span class="ml-auto font-medium text-slate-600">{{ resultCounts(run).total }} results</span>
              </div>
            </button>
          </div>
          <div v-else class="px-5 py-10">
            <EmptyState
              icon="activity"
              compact
              title="No test runs yet"
              :action-label="testCases.length ? 'Start a test run' : ''"
              @action="openCreateRun"
            />
          </div>
        </div>

        <div class="card mt-5 p-5">
          <h3 class="text-sm font-semibold text-slate-900">Coverage by feature</h3>
          <div v-if="features.length" class="mt-3 space-y-2.5">
            <div v-for="feature in features" :key="feature.id" class="flex items-center gap-3">
              <span class="min-w-0 flex-1 truncate text-xs text-slate-600">{{ feature.title }}</span>
              <div class="h-1.5 w-24 overflow-hidden rounded-full bg-slate-100">
                <div
                  class="h-full rounded-full"
                  :class="testCases.some((testCase) => testCase.feature_id === feature.id) ? 'bg-cyan-500' : 'bg-slate-200'"
                  :style="{ width: `${Math.min(100, testCases.filter((testCase) => testCase.feature_id === feature.id).length * 25)}%` }"
                />
              </div>
              <span class="w-14 text-right text-[10px] font-medium text-slate-600">
                {{ caseCountLabel(testCases.filter((testCase) => testCase.feature_id === feature.id).length) }}
              </span>
            </div>
          </div>
          <p v-else class="mt-3 text-xs text-slate-600">No features yet.</p>
        </div>
      </section>
    </div>

    <!-- Test case modal -->
    <BaseModal
      v-model="caseModal"
      :title="caseEditingId ? 'Edit test case' : 'New test case'"
      max-width="lg"
      :persistent="caseBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="case-title">Title</label>
          <input id="case-title" v-model="caseForm.title" data-autofocus class="field-input" placeholder="Guest checkout with a saved card" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="case-feature">Feature</label>
            <select id="case-feature" v-model="caseForm.feature_id" class="field-input">
              <option value="">Select a feature</option>
              <option v-for="feature in features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="case-owner">Owner</label>
            <select id="case-owner" v-model="caseForm.owner_id" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in members" :key="member.user_id" :value="member.user_id">{{ member.username || member.email }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="field-label" for="case-description">Description</label>
          <textarea id="case-description" v-model="caseForm.description" rows="2" class="field-input" placeholder="What behavior is verified?" />
        </div>
        <div>
          <label class="field-label" for="case-preconditions">Preconditions</label>
          <textarea id="case-preconditions" v-model="caseForm.preconditions" rows="2" class="field-input" placeholder="Signed in as… Cart contains… Feature flag enabled…" />
        </div>
        <div class="grid gap-4 sm:grid-cols-4">
          <div>
            <label class="field-label" for="case-type">Type</label>
            <select id="case-type" v-model="caseForm.type" class="field-input">
              <option value="manual">Manual</option>
              <option value="automated">Automated</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="case-priority">Priority</label>
            <select id="case-priority" v-model="caseForm.priority" class="field-input">
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="case-status">Status</label>
            <select id="case-status" v-model="caseForm.status" class="field-input">
              <option value="draft">Draft</option>
              <option value="ready">Ready</option>
              <option value="deprecated">Deprecated</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="case-automation">Automation ref</label>
            <input id="case-automation" v-model="caseForm.automation_ref" class="field-input" placeholder="e2e/checkout.spec.ts" />
          </div>
        </div>
        <div>
          <label class="field-label" for="case-tags">Tags</label>
          <input id="case-tags" v-model="caseForm.tags" class="field-input" placeholder="checkout, payment, regression" />
        </div>

        <div>
          <div class="mb-2 flex items-center justify-between">
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Test steps</p>
            <button type="button" class="btn-ghost text-xs" @click="addStep">
              <AppIcon name="plus" :size="13" /> Add step
            </button>
          </div>
          <div class="space-y-2">
            <div v-for="(step, index) in caseForm.steps" :key="index" class="rounded-xl border border-slate-200 bg-slate-50/60 p-3">
              <div class="flex items-center justify-between">
                <span class="text-[11px] font-semibold text-slate-600">Step {{ index + 1 }}</span>
                <button
                  type="button"
                  class="rounded p-1 text-slate-600 hover:bg-red-50 hover:text-red-600 disabled:opacity-30"
                  :disabled="caseForm.steps.length <= 1"
                  aria-label="Remove step"
                  @click="removeStep(index)"
                >
                  <AppIcon name="trash" :size="13" />
                </button>
              </div>
              <div class="mt-2 grid gap-2 sm:grid-cols-2">
                <textarea v-model="step.action" rows="2" class="field-input text-xs" placeholder="Action: what does the tester do?" />
                <textarea v-model="step.expected_result" rows="2" class="field-input text-xs" placeholder="Expected: what should happen?" />
              </div>
              <input v-model="step.test_data" class="field-input mt-2 py-1.5 text-xs" placeholder="Test data (optional)" />
            </div>
          </div>
        </div>
        <p v-if="caseError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ caseError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="caseBusy" @click="caseModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="caseBusy" @click="saveCase">
          <AppIcon v-if="caseBusy" name="loader" :size="14" class="animate-spin" />
          {{ caseBusy ? 'Saving…' : caseEditingId ? 'Save test case' : 'Create test case' }}
        </button>
      </template>
    </BaseModal>

    <!-- Test run modal -->
    <BaseModal
      v-model="runModal"
      title="New test run"
      max-width="lg"
      :persistent="runBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="run-name">Run name</label>
          <input id="run-name" v-model="runForm.name" data-autofocus class="field-input" placeholder="Sprint 12 regression" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="run-env">Environment</label>
            <select id="run-env" v-model="runForm.environment_id" class="field-input">
              <option value="">Select an environment</option>
              <option v-for="environment in store.workspace?.environments ?? []" :key="environment.id" :value="environment.id">{{ environment.name }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="run-release">Release</label>
            <select id="run-release" v-model="runForm.release_id" class="field-input">
              <option value="">No release</option>
              <option v-for="release in store.releases" :key="release.id" :value="release.id">{{ release.name }} · {{ release.version }}</option>
            </select>
          </div>
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="run-sprint">Sprint</label>
            <select id="run-sprint" v-model="runForm.sprint_id" class="field-input">
              <option value="">No sprint</option>
              <option v-for="sprint in store.sprints" :key="sprint.id" :value="sprint.id">{{ sprint.name }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="run-build">Build</label>
            <select id="run-build" v-model="runForm.build_id" class="field-input">
              <option value="">No build</option>
              <option v-for="build in store.builds" :key="build.id" :value="build.id">
                {{ build.number || build.commit_sha.slice(0, 8) }} · {{ statusLabel(build.status) }}
              </option>
            </select>
          </div>
          <div class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
            {{ caseCountLabel(runForm.test_case_ids.length || testCases.length) }} will be added to this run.
          </div>
        </div>

        <div>
          <div class="mb-2 flex items-center justify-between">
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Test cases</p>
            <button
              type="button"
              class="text-[11px] font-semibold text-indigo-600"
              @click="runForm.test_case_ids = runForm.test_case_ids.length === testCases.length ? [] : testCases.map((testCase) => testCase.id)"
            >
              {{ runForm.test_case_ids.length === testCases.length ? 'Clear all' : 'Select all' }}
            </button>
          </div>
          <div class="max-h-64 space-y-1.5 overflow-y-auto rounded-xl border border-slate-200 p-2">
            <label
              v-for="testCase in testCases"
              :key="testCase.id"
              class="flex cursor-pointer items-start gap-2.5 rounded-lg px-2 py-2 hover:bg-slate-50"
            >
              <input
                type="checkbox"
                class="mt-0.5 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                :checked="runForm.test_case_ids.includes(testCase.id)"
                @change="toggleCaseSelection(testCase.id)"
              />
              <span class="min-w-0 flex-1">
                <span class="block text-xs font-medium text-slate-800">{{ testCase.title }}</span>
                <span class="mt-0.5 block text-[10px] text-slate-600">{{ featureName(testCase.feature_id) }} · {{ (testCase.steps ?? []).length }} steps</span>
              </span>
            </label>
          </div>
        </div>
        <p v-if="runError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ runError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="runBusy" @click="runModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="runBusy" @click="saveRun">
          <AppIcon v-if="runBusy" name="loader" :size="14" class="animate-spin" />
          {{ runBusy ? 'Starting…' : 'Start test run' }}
        </button>
      </template>
    </BaseModal>

    <!-- Run detail modal -->
    <BaseModal
      v-model="detailOpen"
      :title="selectedRun?.name || 'Test run'"
      :description="selectedRun ? `${environmentName(selectedRun.environment_id)} · ${formatDateTime(selectedRun.created_at)}` : ''"
      max-width="lg"
      :persistent="Boolean(resultBusy)"
    >
      <div v-if="selectedRun" class="max-h-[68vh] space-y-3 overflow-y-auto pr-1">
        <div class="grid grid-cols-2 gap-2 sm:grid-cols-5">
          <div class="rounded-lg bg-slate-50 p-2.5 text-center">
            <p class="text-lg font-semibold text-slate-900">{{ resultCounts(selectedRun).total }}</p>
            <p class="text-[10px] text-slate-600">Cases</p>
          </div>
          <div class="rounded-lg bg-emerald-50 p-2.5 text-center">
            <p class="text-lg font-semibold text-emerald-700">{{ resultCounts(selectedRun).passed }}</p>
            <p class="text-[10px] text-emerald-700">Passed</p>
          </div>
          <div class="rounded-lg bg-red-50 p-2.5 text-center">
            <p class="text-lg font-semibold text-red-700">{{ resultCounts(selectedRun).failed }}</p>
            <p class="text-[10px] text-red-600">Failed</p>
          </div>
          <div class="rounded-lg bg-amber-50 p-2.5 text-center">
            <p class="text-lg font-semibold text-amber-700">{{ resultCounts(selectedRun).blocked }}</p>
            <p class="text-[10px] text-amber-700">Blocked</p>
          </div>
          <div class="rounded-lg bg-slate-50 p-2.5 text-center">
            <p class="text-lg font-semibold text-slate-700">{{ resultCounts(selectedRun).notRun }}</p>
            <p class="text-[10px] text-slate-600">Not run</p>
          </div>
        </div>

        <div class="flex items-center gap-3">
          <div class="h-2 flex-1 overflow-hidden rounded-full bg-slate-100">
            <div
              class="h-full rounded-full"
              :class="resultCounts(selectedRun).failed ? 'bg-rose-500' : 'bg-emerald-500'"
              :style="{ width: `${runProgress(selectedRun)}%` }"
            />
          </div>
          <span class="text-xs font-semibold text-slate-600">{{ percent(runProgress(selectedRun)) }} executed</span>
        </div>

        <div v-for="result in selectedRun.results ?? []" :key="result.id" data-testid="test-result" class="rounded-xl border border-slate-200">
          <div class="flex flex-wrap items-start justify-between gap-2 border-b border-slate-100 px-4 py-3">
            <div class="min-w-0">
              <p class="text-sm font-semibold text-slate-800">{{ caseTitle(result.test_case_id) }}</p>
              <p class="mt-0.5 text-[10px] text-slate-600">
                {{ result.completed_at ? `Completed ${timeAgo(result.completed_at)}` : 'Not executed' }}
                <span v-if="result.bug_work_item_id" class="ml-2 font-semibold text-red-600">Bug linked</span>
              </p>
            </div>
            <div class="flex items-center gap-2">
              <StatusPill :status="result.status" />
              <button
                v-if="result.status === 'failed' || (result.step_results ?? []).some((step) => step.status === 'failed')"
                type="button"
                class="rounded-lg border border-red-200 bg-red-50 px-2 py-1 text-[11px] font-semibold text-red-700 hover:bg-red-100"
                @click="openBug(result)"
              >
                Raise bug
              </button>
            </div>
          </div>

          <div class="space-y-3 px-4 py-3">
            <div class="flex flex-wrap gap-1.5">
              <button
                v-for="status in ['passed', 'failed', 'blocked', 'skipped']"
                :key="status"
                type="button"
                class="rounded-lg border px-2.5 py-1 text-[11px] font-semibold"
                :class="
                  result.status === status
                    ? 'border-indigo-300 bg-indigo-50 text-indigo-700'
                    : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300'
                "
                :disabled="resultBusy === result.id"
                @click="patchResult(result, status)"
              >
                {{ statusLabel(status) }}
              </button>
            </div>

            <textarea
              v-model="resultNotes[result.id]"
              rows="2"
              class="field-input text-xs"
              placeholder="Actual result or execution notes for this case…"
            />

            <div v-for="stepResult in result.step_results ?? []" :key="stepResult.id" data-testid="test-step-result" class="rounded-lg border border-slate-100 bg-slate-50/70 p-3">
              <div class="flex flex-wrap items-start justify-between gap-2">
                <div class="min-w-0 flex-1">
                  <p class="text-[11px] font-semibold text-slate-700">
                    Step {{ stepFor(stepResult)?.position ?? '—' }} · {{ stepFor(stepResult)?.action || 'Test step' }}
                  </p>
                  <p class="mt-0.5 text-[10px] text-slate-600">
                    Expected: {{ stepFor(stepResult)?.expected_result || '—' }}
                  </p>
                </div>
                <div class="flex items-center gap-1.5">
                  <StatusPill :status="stepResult.status" :dot="false" />
                  <button
                    v-if="stepResult.status === 'failed'"
                    type="button"
                    class="text-[10px] font-semibold text-red-600 hover:text-red-700"
                    @click="openBug(result, stepResult)"
                  >
                    Raise bug
                  </button>
                </div>
              </div>
              <div class="mt-2 flex flex-wrap gap-1">
                <button
                  v-for="status in ['passed', 'failed', 'blocked', 'skipped']"
                  :key="status"
                  type="button"
                  class="rounded border px-2 py-0.5 text-[10px] font-medium"
                  :class="
                    stepResult.status === status
                      ? 'border-indigo-300 bg-indigo-50 text-indigo-700'
                      : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300'
                  "
                  :disabled="resultBusy === stepResult.id"
                  @click="patchStep(result, stepResult, status)"
                >
                  {{ statusLabel(status) }}
                </button>
              </div>
              <div class="mt-2 grid gap-2 sm:grid-cols-2">
                <input v-model="stepNotes[stepResult.id]" class="field-input py-1.5 text-[11px]" placeholder="Actual result" />
                <input v-model="stepEvidence[stepResult.id]" class="field-input py-1.5 text-[11px]" placeholder="Evidence URL (screenshot, log, recording)" />
              </div>
            </div>
          </div>
        </div>

        <div v-if="!(selectedRun.results ?? []).length" class="rounded-xl border border-dashed border-slate-300 py-8 text-center text-sm text-slate-600">
          No test results in this run.
        </div>
      </div>
      <template #footer>

        <button type="button" class="btn-secondary" @click="detailOpen = false">Close</button>
      </template>
    </BaseModal>

    <!-- Bug modal -->
    <BaseModal
      v-model="bugOpen"
      title="Raise a linked defect"
      max-width="lg"
      :persistent="bugBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="bug-title">Title</label>
          <input id="bug-title" v-model="bugForm.title" data-autofocus class="field-input" />
        </div>
        <div>
          <label class="field-label" for="bug-description">Description</label>
          <textarea id="bug-description" v-model="bugForm.description" rows="2" class="field-input" placeholder="Observed behavior and impact" />
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="bug-severity">Severity</label>
            <select id="bug-severity" v-model="bugForm.severity" class="field-input">
              <option value="blocker">Blocker</option>
              <option value="critical">Critical</option>
              <option value="major">Major</option>
              <option value="minor">Minor</option>
              <option value="trivial">Trivial</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="bug-priority">Priority</label>
            <select id="bug-priority" v-model="bugForm.priority" class="field-input">
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
            Linked to the failed result and its feature automatically.
          </div>
        </div>
        <div>
          <label class="field-label" for="bug-repro">Reproduction steps</label>
          <textarea id="bug-repro" v-model="bugForm.reproduction_steps" rows="4" class="field-input" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="bug-expected">Expected result</label>
            <textarea id="bug-expected" v-model="bugForm.expected_result" rows="2" class="field-input" />
          </div>
          <div>
            <label class="field-label" for="bug-actual">Actual result</label>
            <textarea id="bug-actual" v-model="bugForm.actual_result" rows="2" class="field-input" />
          </div>
        </div>
        <p v-if="bugError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ bugError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="bugBusy" @click="bugOpen = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="bugBusy" @click="saveBug">
          <AppIcon v-if="bugBusy" name="loader" :size="14" class="animate-spin" />
          {{ bugBusy ? 'Creating…' : 'Create linked bug' }}
        </button>
      </template>
    </BaseModal>
  </div>
</template>
