<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BaseModal from '../BaseModal.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import PriorityPill from './PriorityPill.vue'
import EmptyState from './EmptyState.vue'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useAuthStore } from '../../stores/authStore'
import { useToastStore } from '../../stores/toastStore'
import { api, isRevisionConflict, revisionConflictMessage } from '../../api/client'
import { percent, statusLabel } from '../../lib/workspace'
import { formatDateTime, timeAgo } from '../../lib/format'
import type {
  TestCase,
  TestCaseVersion,
  TestPlan,
  TestResult,
  TestRun,
  TestSharedStep,
  TestSuite,
  TestStep,
  TestStepResult,
} from '../../types/workspace'

const store = useWorkspaceStore()
const auth = useAuthStore()
const toast = useToastStore()
const route = useRoute()
const router = useRouter()

const canTest = computed(() => auth.can(store.projectId, 'test.write'))

const caseSearch = ref('')
const caseFeatureFilter = ref('')
const caseModal = ref(false)
const caseEditingId = ref<string | null>(null)
const caseBusy = ref(false)
const caseError = ref('')

const importInput = ref<HTMLInputElement | null>(null)
const ioBusy = ref(false)

const historyOpen = ref(false)
const historyBusy = ref(false)
const historyError = ref('')
const historyCaseId = ref<string | null>(null)
const historyCaseTitle = ref('')
const historyVersions = ref<TestCaseVersion[]>([])
const historyTotal = ref(0)

const sharedPick = ref('')
const sharedModal = ref(false)
const sharedEditingId = ref<string | null>(null)
const sharedEditingRevision = ref(0)
const sharedBusy = ref(false)
const sharedError = ref('')
const sharedForm = reactive({
  name: '',
  action: '',
  expected_result: '',
  test_data: '',
})

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
const testPlans = computed(() => store.testPlans)
const testSuites = computed(() => store.testSuites)
const features = computed(() => store.features)
const members = computed(() => store.workspace?.members ?? [])
const sharedSteps = computed(() => store.workspace?.test_shared_steps ?? [])

const selectedPlanId = ref('')
const selectedSuiteId = ref('')

const orderedPlans = computed(() =>
  [...testPlans.value].sort((a, b) => {
    const rank = (plan: TestPlan) => (plan.status === 'archived' ? 2 : plan.status === 'active' ? 0 : 1)
    return rank(a) - rank(b) || new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
  }),
)

const selectedPlan = computed<TestPlan | null>(
  () => testPlans.value.find((plan) => plan.id === selectedPlanId.value) ?? null,
)

const planSuites = computed<TestSuite[]>(() =>
  testSuites.value
    .filter((suite) => suite.plan_id === selectedPlanId.value)
    .sort((a, b) => a.position - b.position || a.name.localeCompare(b.name)),
)

const activeSuites = computed(() => planSuites.value.filter((suite) => !suite.archived_at))

const selectedSuite = computed<TestSuite | null>(
  () => planSuites.value.find((suite) => suite.id === selectedSuiteId.value) ?? null,
)

const selectedSuiteCases = computed(() => {
  if (!selectedSuite.value) return []
  return store.testSuiteCases
    .filter((member) => member.suite_id === selectedSuite.value?.id)
    .sort((a, b) => a.position - b.position)
    .map((member) => caseById.value.get(member.test_case_id))
    .filter((testCase): testCase is TestCase => Boolean(testCase))
})

function suiteCaseIds(suiteId: string): string[] {
  return store.testSuiteCases
    .filter((member) => member.suite_id === suiteId)
    .map((member) => member.test_case_id)
}

function planCaseIds(planId: string): string[] {
  const suiteIds = new Set(
    testSuites.value.filter((suite) => suite.plan_id === planId && !suite.archived_at).map((suite) => suite.id),
  )
  return [
    ...new Set(
      store.testSuiteCases
        .filter((member) => suiteIds.has(member.suite_id))
        .map((member) => member.test_case_id),
    ),
  ]
}

watch(
  orderedPlans,
  (plans) => {
    if (!plans.some((plan) => plan.id === selectedPlanId.value)) {
      selectedPlanId.value = plans.find((plan) => plan.status !== 'archived')?.id ?? plans[0]?.id ?? ''
    }
  },
  { immediate: true },
)

watch(
  planSuites,
  (suites) => {
    if (!suites.some((suite) => suite.id === selectedSuiteId.value)) {
      selectedSuiteId.value = suites.find((suite) => !suite.archived_at)?.id ?? suites[0]?.id ?? ''
    }
  },
  { immediate: true },
)

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
  return stepResult.test_step_id ? stepById.value.get(stepResult.test_step_id) : undefined
}

// Step results snapshot their content when the run starts, so they render
// correctly even after the case is edited and live steps are replaced.
function stepPosition(stepResult: TestStepResult): number | string {
  return stepResult.position || stepFor(stepResult)?.position || '—'
}

function stepAction(stepResult: TestStepResult): string {
  return stepResult.action || stepFor(stepResult)?.action || 'Test step'
}

function stepExpected(stepResult: TestStepResult): string {
  return stepResult.expected_result || stepFor(stepResult)?.expected_result || '—'
}

// ---------------------------------------------------------------------------
// Test case form
// ---------------------------------------------------------------------------

interface CaseStepDraft {
  key: number
  shared_step_id: string | null
  action: string
  expected_result: string
  test_data: string
}

let stepKeySeed = 0

function emptyStep(): CaseStepDraft {
  stepKeySeed += 1
  return {
    key: stepKeySeed,
    shared_step_id: null,
    action: '',
    expected_result: '',
    test_data: '',
  }
}

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
  steps: [emptyStep()] as CaseStepDraft[],
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
    steps: [emptyStep()],
  })
}

function openCreateCase(): void {
  if (!canTest.value) return
  caseEditingId.value = null
  resetCaseForm()
  sharedPick.value = ''
  caseError.value = ''
  caseModal.value = true
}

function openEditCase(testCase: TestCase): void {
  if (!canTest.value) return
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
      ...emptyStep(),
      shared_step_id: step.shared_step_id ?? null,
      action: step.action,
      expected_result: step.expected_result,
      test_data: step.test_data,
    })),
  })
  if (!caseForm.steps.length) caseForm.steps = [emptyStep()]
  sharedPick.value = ''
  caseError.value = ''
  caseModal.value = true
}

function addStep(): void {
  caseForm.steps.push(emptyStep())
}

function removeStep(index: number): void {
  if (caseForm.steps.length <= 1) return
  caseForm.steps.splice(index, 1)
}

function moveStep(index: number, delta: number): void {
  const target = index + delta
  if (target < 0 || target >= caseForm.steps.length) return
  const [step] = caseForm.steps.splice(index, 1)
  caseForm.steps.splice(target, 0, step)
}

function onStepKeydown(event: KeyboardEvent, index: number): void {
  if (!event.altKey) return
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    moveStep(index, -1)
  } else if (event.key === 'ArrowDown') {
    event.preventDefault()
    moveStep(index, 1)
  }
}

function insertSharedStep(): void {
  const shared = sharedSteps.value.find((step) => step.id === sharedPick.value)
  if (!shared) return
  caseForm.steps.push({
    ...emptyStep(),
    shared_step_id: shared.id,
    action: shared.action,
    expected_result: shared.expected_result,
    test_data: shared.test_data,
  })
  sharedPick.value = ''
}

async function saveCase(): Promise<void> {
  if (!canTest.value) return
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
        shared_step_id: step.shared_step_id,
        action: step.action.trim(),
        expected_result: step.expected_result.trim(),
        test_data: step.test_data,
      })),
    }
    if (caseEditingId.value) {
      const current = store.testCases.find((testCase) => testCase.id === caseEditingId.value)
      if (!current) {
        caseError.value = 'This test case no longer exists. Close the dialog and refresh.'
        return
      }
      await api.updateTestCase(store.projectId, caseEditingId.value, payload, current.revision)
      toast.success('Test case updated.', 'Test lab')
    } else {
      await api.createTestCase(store.projectId, payload)
      toast.success('Test case created and linked to its feature.', 'Test lab')
    }
    caseModal.value = false
    await store.refresh()
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      caseError.value = revisionConflictMessage('test case')
      return
    }
    caseError.value = error instanceof Error ? error.message : 'Failed to save test case'
  } finally {
    caseBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Version history, import/export and shared steps
// ---------------------------------------------------------------------------

async function openHistory(testCase: Pick<TestCase, 'id' | 'title'>): Promise<void> {
  historyCaseId.value = testCase.id
  historyCaseTitle.value = testCase.title
  historyVersions.value = []
  historyTotal.value = 0
  historyError.value = ''
  historyOpen.value = true
  await loadHistory()
}

function openEditingHistory(): void {
  const current = caseEditingId.value
    ? store.testCases.find((testCase) => testCase.id === caseEditingId.value)
    : null
  if (current) void openHistory(current)
}

async function loadHistory(): Promise<void> {
  if (!historyCaseId.value) return
  historyBusy.value = true
  historyError.value = ''
  try {
    const result = await api.listTestCaseVersions(store.projectId, historyCaseId.value)
    historyVersions.value = result.versions ?? []
    historyTotal.value = result.total ?? historyVersions.value.length
  } catch (error) {
    historyError.value = error instanceof Error ? error.message : 'Failed to load version history'
  } finally {
    historyBusy.value = false
  }
}

function triggerImport(): void {
  if (!canTest.value) return
  importInput.value?.click()
}

async function onImportFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !canTest.value) return
  ioBusy.value = true
  try {
    const payload = JSON.parse(await file.text())
    const result = await api.importTestCases(store.projectId, payload)
    await store.refresh()
    toast.success(`Imported ${result.imported} test case(s).`, 'Test lab')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to import test cases', 'Test lab')
  } finally {
    ioBusy.value = false
  }
}

async function exportCases(): Promise<void> {
  if (!testCases.value.length) return
  ioBusy.value = true
  try {
    const exported = await api.exportTestCases(store.projectId)
    const blob = new Blob([JSON.stringify(exported, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `test-cases-${new Date().toISOString().slice(0, 10)}.json`
    anchor.click()
    URL.revokeObjectURL(url)
    toast.success('Test cases exported.', 'Test lab')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to export test cases', 'Test lab')
  } finally {
    ioBusy.value = false
  }
}

function resetSharedForm(): void {
  Object.assign(sharedForm, { name: '', action: '', expected_result: '', test_data: '' })
  sharedEditingId.value = null
  sharedEditingRevision.value = 0
  sharedError.value = ''
}

function openSharedManager(): void {
  if (!canTest.value) return
  resetSharedForm()
  sharedModal.value = true
}

function editSharedStep(step: TestSharedStep): void {
  sharedEditingId.value = step.id
  sharedEditingRevision.value = step.revision
  Object.assign(sharedForm, {
    name: step.name,
    action: step.action,
    expected_result: step.expected_result,
    test_data: step.test_data,
  })
  sharedError.value = ''
}

async function saveSharedStep(): Promise<void> {
  if (!canTest.value) return
  if (!sharedForm.name.trim() || !sharedForm.action.trim() || !sharedForm.expected_result.trim()) {
    sharedError.value = 'Name, action and expected result are required.'
    return
  }
  sharedBusy.value = true
  sharedError.value = ''
  const payload = {
    name: sharedForm.name.trim(),
    action: sharedForm.action.trim(),
    expected_result: sharedForm.expected_result.trim(),
    test_data: sharedForm.test_data,
  }
  try {
    if (sharedEditingId.value) {
      await api.updateSharedStep(
        store.projectId,
        sharedEditingId.value,
        payload,
        sharedEditingRevision.value,
      )
      toast.success('Shared step updated. Existing cases keep their copied text.', 'Test lab')
    } else {
      await api.createSharedStep(store.projectId, payload)
      toast.success('Shared step created.', 'Test lab')
    }
    resetSharedForm()
    await store.refresh()
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      sharedError.value = revisionConflictMessage('shared step')
      return
    }
    sharedError.value = error instanceof Error ? error.message : 'Failed to save shared step'
  } finally {
    sharedBusy.value = false
  }
}

async function deleteSharedStep(step: TestSharedStep): Promise<void> {
  if (!canTest.value || sharedBusy.value) return
  sharedBusy.value = true
  sharedError.value = ''
  try {
    await api.deleteSharedStep(store.projectId, step.id, step.revision)
    if (sharedEditingId.value === step.id) resetSharedForm()
    await store.refresh()
    toast.success('Shared step deleted. Cases keep the text they copied.', 'Test lab')
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      sharedError.value = revisionConflictMessage('shared step')
      return
    }
    sharedError.value = error instanceof Error ? error.message : 'Failed to delete shared step'
  } finally {
    sharedBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Test plans and suites
// ---------------------------------------------------------------------------

const planModal = ref(false)
const planBusy = ref(false)
const planError = ref('')
const planForm = reactive({
  name: '',
  description: '',
  owner_id: '',
  start_date: '',
  end_date: '',
})

function openCreatePlan(): void {
  if (!canTest.value) return
  Object.assign(planForm, {
    name: `Test plan · ${new Date().toISOString().slice(0, 10)}`,
    description: '',
    owner_id: '',
    start_date: '',
    end_date: '',
  })
  planError.value = ''
  planModal.value = true
}

async function savePlan(): Promise<void> {
  if (!canTest.value) return
  if (!planForm.name.trim()) {
    planError.value = 'A test plan name is required.'
    return
  }
  planBusy.value = true
  planError.value = ''
  try {
    const result = await api.createTestPlan(store.projectId, {
      name: planForm.name.trim(),
      description: planForm.description,
      status: 'draft',
      owner_id: planForm.owner_id || null,
      start_date: planForm.start_date || null,
      end_date: planForm.end_date || null,
    })
    planModal.value = false
    await store.refresh()
    selectedPlanId.value = result.id
    toast.success('Test plan created. Add suites and cases next.', 'Test plan')
  } catch (error) {
    planError.value = error instanceof Error ? error.message : 'Failed to create test plan'
  } finally {
    planBusy.value = false
  }
}

async function setPlanStatus(plan: TestPlan, status: 'draft' | 'active' | 'archived'): Promise<void> {
  if (!canTest.value || plan.status === status) return
  try {
    await api.updateTestPlan(
      store.projectId,
      plan.id,
      {
        name: plan.name,
        description: plan.description,
        status,
        owner_id: plan.owner_id,
        start_date: plan.start_date,
        end_date: plan.end_date,
      },
      plan.revision,
    )
    await store.refresh()
    toast.success(status === 'archived' ? 'Test plan archived. Historical runs keep their snapshot.' : `Test plan marked ${status}.`, 'Test plan')
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      toast.error(revisionConflictMessage('test plan'), 'Test plan')
      return
    }
    toast.error(error instanceof Error ? error.message : 'Failed to update test plan', 'Test plan')
  }
}

const suiteModal = ref(false)
const suiteBusy = ref(false)
const suiteError = ref('')
const suiteForm = reactive({
  name: '',
  description: '',
  assignee_id: '',
})

function openCreateSuite(): void {
  if (!canTest.value || !selectedPlan.value || selectedPlan.value.status === 'archived') return
  Object.assign(suiteForm, { name: '', description: '', assignee_id: '' })
  suiteError.value = ''
  suiteModal.value = true
}

async function saveSuite(): Promise<void> {
  if (!canTest.value || !selectedPlan.value) return
  if (!suiteForm.name.trim()) {
    suiteError.value = 'A suite name is required.'
    return
  }
  suiteBusy.value = true
  suiteError.value = ''
  try {
    const result = await api.createTestSuite(store.projectId, selectedPlan.value.id, {
      name: suiteForm.name.trim(),
      description: suiteForm.description,
      assignee_id: suiteForm.assignee_id || null,
    })
    suiteModal.value = false
    await store.refresh()
    selectedSuiteId.value = result.id
    toast.success('Suite created. Add cases by filter or CSV.', 'Test suite')
  } catch (error) {
    suiteError.value = error instanceof Error ? error.message : 'Failed to create test suite'
  } finally {
    suiteBusy.value = false
  }
}

async function setSuiteArchived(suite: TestSuite, archived: boolean): Promise<void> {
  if (!canTest.value) return
  try {
    await api.updateTestSuite(
      store.projectId,
      suite.plan_id,
      suite.id,
      {
        name: suite.name,
        description: suite.description,
        assignee_id: suite.assignee_id,
        position: suite.position,
        archived,
      },
      suite.revision,
    )
    await store.refresh()
    toast.success(archived ? 'Suite archived. Its membership stays available to history.' : 'Suite restored.', 'Test suite')
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      toast.error(revisionConflictMessage('test suite'), 'Test suite')
      return
    }
    toast.error(error instanceof Error ? error.message : 'Failed to update test suite', 'Test suite')
  }
}

async function moveSuite(suite: TestSuite, direction: -1 | 1): Promise<void> {
  if (!canTest.value || !selectedPlan.value) return
  const suites = activeSuites.value
  const index = suites.findIndex((entry) => entry.id === suite.id)
  const target = index + direction
  if (index < 0 || target < 0 || target >= suites.length) return
  const ids = suites.map((entry) => entry.id)
  ;[ids[index], ids[target]] = [ids[target], ids[index]]
  try {
    await api.reorderTestSuites(store.projectId, selectedPlan.value.id, ids, selectedPlan.value.revision)
    await store.refresh()
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      toast.error(revisionConflictMessage('test plan'), 'Test suite order')
      return
    }
    toast.error(error instanceof Error ? error.message : 'Failed to reorder suites', 'Test suite order')
  }
}

const addCasesOpen = ref(false)
const addCasesBusy = ref(false)
const addCasesError = ref('')
const addCasesForm = reactive({
  mode: 'filter' as 'filter' | 'csv',
  feature_id: '',
  type: '',
  priority: '',
  status: '',
  csv: '',
})

function openAddCases(suite: TestSuite): void {
  if (!canTest.value || suite.archived_at) return
  selectedSuiteId.value = suite.id
  Object.assign(addCasesForm, {
    mode: 'filter',
    feature_id: '',
    type: '',
    priority: '',
    status: '',
    csv: '',
  })
  addCasesError.value = ''
  addCasesOpen.value = true
}

async function saveAddCases(): Promise<void> {
  if (!canTest.value || !selectedSuite.value) return
  if (addCasesForm.mode === 'csv' && !addCasesForm.csv.trim()) {
    addCasesError.value = 'Paste at least one exact test case title.'
    return
  }
  addCasesBusy.value = true
  addCasesError.value = ''
  try {
    const payload: Record<string, unknown> = {}
    if (addCasesForm.mode === 'csv') {
      payload.csv = addCasesForm.csv
    } else {
      payload.filter = {
        feature_id: addCasesForm.feature_id || null,
        type: addCasesForm.type,
        priority: addCasesForm.priority,
        status: addCasesForm.status,
      }
    }
    const result = await api.addTestSuiteCases(
      store.projectId,
      selectedSuite.value.plan_id,
      selectedSuite.value.id,
      payload,
      selectedSuite.value.revision,
    )
    addCasesOpen.value = false
    await store.refresh()
    toast.success(
      `${result.added} ${result.added === 1 ? 'case' : 'cases'} added${result.skipped ? `, ${result.skipped} already present` : ''}.`,
      'Test suite',
    )
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      addCasesError.value = revisionConflictMessage('test suite')
      return
    }
    addCasesError.value = error instanceof Error ? error.message : 'Failed to add cases'
  } finally {
    addCasesBusy.value = false
  }
}

async function removeSuiteCase(suite: TestSuite, testCase: TestCase): Promise<void> {
  if (!canTest.value || suite.archived_at) return
  try {
    await api.removeTestSuiteCase(store.projectId, suite.plan_id, suite.id, testCase.id, suite.revision)
    await store.refresh()
    toast.success(`${testCase.title} removed from the suite.`, 'Test suite')
  } catch (error) {
    if (isRevisionConflict(error)) {
      await store.refresh()
      toast.error(revisionConflictMessage('test suite'), 'Test suite')
      return
    }
    toast.error(error instanceof Error ? error.message : 'Failed to remove case', 'Test suite')
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
  scope: 'all' as 'all' | 'selection' | 'suite' | 'plan' | 'filter',
  plan_id: '',
  suite_id: '',
  filter_feature_id: '',
  filter_type: '',
  filter_priority: '',
  filter_status: '',
  test_case_ids: [] as string[],
})

function openCreateRun(options: { scope?: typeof runForm.scope; planId?: string; suiteId?: string } = {}): void {
  if (!canTest.value) return
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
    scope: options.scope ?? 'all',
    plan_id: options.planId ?? selectedPlanId.value,
    suite_id: options.suiteId ?? selectedSuiteId.value,
    filter_feature_id: '',
    filter_type: '',
    filter_priority: '',
    filter_status: '',
    test_case_ids: [],
  })
  runError.value = ''
  runModal.value = true
}

const runFilterCases = computed(() =>
  testCases.value.filter((testCase) => {
    if (runForm.filter_feature_id && testCase.feature_id !== runForm.filter_feature_id) return false
    if (runForm.filter_type && testCase.type !== runForm.filter_type) return false
    if (runForm.filter_priority && testCase.priority !== runForm.filter_priority) return false
    if (runForm.filter_status && testCase.status !== runForm.filter_status) return false
    return true
  }),
)

const runScopeCount = computed(() => {
  switch (runForm.scope) {
    case 'suite':
      return runForm.suite_id ? suiteCaseIds(runForm.suite_id).length : 0
    case 'plan':
      return runForm.plan_id ? planCaseIds(runForm.plan_id).length : 0
    case 'filter':
      return runFilterCases.value.length
    case 'selection':
      return runForm.test_case_ids.length
    default:
      return testCases.value.length
  }
})

function toggleCaseSelection(caseId: string): void {
  if (runForm.test_case_ids.includes(caseId)) {
    runForm.test_case_ids = runForm.test_case_ids.filter((id) => id !== caseId)
  } else {
    runForm.test_case_ids = [...runForm.test_case_ids, caseId]
  }
}

async function saveRun(): Promise<void> {
  if (!canTest.value) return
  if (!runForm.environment_id) {
    runError.value = 'A test run must link to an environment.'
    return
  }
  if (!testCases.value.length) {
    runError.value = 'Create at least one test case before starting a run.'
    return
  }
  if (runForm.scope === 'selection' && !runForm.test_case_ids.length) {
    runError.value = 'Select at least one test case, or change the run scope.'
    return
  }
  if (runForm.scope === 'suite' && !runForm.suite_id) {
    runError.value = 'Select a suite for this run.'
    return
  }
  if (runForm.scope === 'plan' && !runForm.plan_id) {
    runError.value = 'Select a plan for this run.'
    return
  }
  if (!runScopeCount.value) {
    runError.value = 'The selected scope contains no test cases.'
    return
  }
  runBusy.value = true
  runError.value = ''
  try {
    const payload: Record<string, unknown> = {
      name: runForm.name.trim(),
      environment_id: runForm.environment_id,
      release_id: runForm.release_id || null,
      sprint_id: runForm.sprint_id || null,
      build_id: runForm.build_id || null,
    }
    if (runForm.scope === 'selection') payload.test_case_ids = runForm.test_case_ids
    if (runForm.scope === 'suite') payload.suite_id = runForm.suite_id
    if (runForm.scope === 'plan') payload.plan_id = runForm.plan_id
    if (runForm.scope === 'filter') {
      payload.filter = {
        feature_id: runForm.filter_feature_id || null,
        type: runForm.filter_type,
        priority: runForm.filter_priority,
        status: runForm.filter_status,
      }
    }
    const result = await api.createTestRun(store.projectId, payload)
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
  if (!canTest.value) return
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
  if (!canTest.value) return
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
  if (!canTest.value) return
  const testCase = caseById.value.get(result.test_case_id)
  const step = stepResult ? stepFor(stepResult) : undefined
  const position = stepResult?.position || step?.position || 1
  const expected = stepResult?.expected_result || step?.expected_result || ''
  bugResultId.value = result.id
  bugStepResultId.value = stepResult?.id ?? null
  Object.assign(bugForm, {
    title: stepResult
      ? `Defect: ${testCase?.title ?? 'Test case'} — step ${position}`
      : `Defect: ${testCase?.title ?? 'Test case'}`,
    description: stepResult?.actual_result || result.actual_result || '',
    severity: 'major',
    priority: 'high',
    reproduction_steps: (testCase?.steps ?? [])
      .map((entry, index) => `${index + 1}. ${entry.action}`)
      .join('\n'),
    expected_result: expected,
    actual_result: stepResult?.actual_result || result.actual_result || '',
  })
  bugError.value = ''
  bugOpen.value = true
}

async function saveBug(): Promise<void> {
  if (!canTest.value) return
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
        <button v-if="canTest" type="button" class="btn-secondary" @click="openCreatePlan">
          <AppIcon name="layers" :size="15" />
          New test plan
        </button>
        <button v-if="canTest" type="button" class="btn-secondary" :disabled="!features.length" @click="openCreateCase">
          <AppIcon name="flask" :size="15" />
          New test case
        </button>
        <button v-if="canTest" type="button" class="btn-primary" :disabled="!testCases.length" @click="openCreateRun()">
          <AppIcon name="plus" :size="15" />
          New test run
        </button>
      </div>
    </div>

    <!-- Plans and suites -->
    <section class="card p-5" data-testid="test-plan-board">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-slate-900">Plans &amp; suites</h3>
          <p class="mt-0.5 text-xs text-slate-600">
            Plans schedule a cycle. Suites keep an ordered case selection that a run snapshots.
          </p>
        </div>
        <button
          v-if="canTest"
          type="button"
          class="btn-ghost text-xs"
          :disabled="!selectedPlan || selectedPlan.status === 'archived'"
          @click="openCreateSuite"
        >
          <AppIcon name="plus" :size="13" /> New suite
        </button>
      </div>

      <div v-if="testPlans.length" class="mt-4 grid gap-5 lg:grid-cols-12">
        <div class="space-y-3 lg:col-span-4">
          <div>
            <label class="field-label" for="test-plan-select">Plan</label>
            <select id="test-plan-select" v-model="selectedPlanId" class="field-input text-sm">
              <option v-for="plan in orderedPlans" :key="plan.id" :value="plan.id">
                {{ plan.name }} · {{ statusLabel(plan.status) }}
              </option>
            </select>
          </div>
          <div v-if="selectedPlan" class="rounded-xl border border-slate-200 bg-slate-50/60 p-3">
            <div class="flex flex-wrap items-center gap-2">
              <StatusPill :status="selectedPlan.status" />
              <span class="text-[11px] text-slate-600">{{ ownerName(selectedPlan.owner_id) }}</span>
            </div>
            <p v-if="selectedPlan.description" class="mt-2 text-xs text-slate-600">{{ selectedPlan.description }}</p>
            <p class="mt-2 text-[11px] text-slate-600">
              {{ selectedPlan.start_date ? formatDateTime(selectedPlan.start_date) : 'No start' }}
              →
              {{ selectedPlan.end_date ? formatDateTime(selectedPlan.end_date) : 'No end' }}
            </p>
            <div v-if="canTest && selectedPlan.status !== 'archived'" class="mt-3 flex flex-wrap gap-1.5">
              <button
                v-if="selectedPlan.status === 'draft'"
                type="button"
                class="btn-ghost text-[11px]"
                @click="setPlanStatus(selectedPlan, 'active')"
              >
                Activate plan
              </button>
              <button
                v-if="selectedPlan.status === 'active'"
                type="button"
                class="btn-ghost text-[11px]"
                @click="setPlanStatus(selectedPlan, 'draft')"
              >
                Move to draft
              </button>
              <button type="button" class="btn-ghost text-[11px]" @click="setPlanStatus(selectedPlan, 'archived')">
                Archive plan
              </button>
            </div>
            <p v-else-if="selectedPlan.status === 'archived'" class="mt-3 text-[11px] text-slate-600">
              Archived plans stay readable and keep the context of historical runs.
            </p>
          </div>
        </div>

        <div class="lg:col-span-8">
          <div v-if="planSuites.length" class="space-y-2">
            <div
              v-for="(suite, index) in planSuites"
              :key="suite.id"
              data-testid="suite-row"
              class="rounded-xl border px-3 py-2.5 transition"
              :class="selectedSuiteId === suite.id ? 'border-cyan-300 bg-cyan-50/50' : 'border-slate-200 bg-white'"
            >
              <div class="flex flex-wrap items-start justify-between gap-2">
                <button type="button" class="min-w-0 text-left" @click="selectedSuiteId = suite.id">
                  <p class="truncate text-sm font-medium text-slate-800">
                    {{ suite.name }}
                    <span v-if="suite.archived_at" class="ml-1 text-[10px] font-normal text-slate-500">(archived)</span>
                  </p>
                  <p class="mt-0.5 text-[11px] text-slate-600">
                    {{ caseCountLabel(suiteCaseIds(suite.id).length) }} · {{ ownerName(suite.assignee_id) }}
                  </p>
                </button>
                <div class="flex flex-wrap items-center gap-1">
                  <button
                    v-if="canTest && !suite.archived_at && selectedPlan?.status !== 'archived'"
                    type="button"
                    class="btn-ghost px-2 py-1 text-[11px]"
                    aria-label="Move suite up"
                    :disabled="index === 0"
                    @click="moveSuite(suite, -1)"
                  >
                    ↑
                  </button>
                  <button
                    v-if="canTest && !suite.archived_at && selectedPlan?.status !== 'archived'"
                    type="button"
                    class="btn-ghost px-2 py-1 text-[11px]"
                    aria-label="Move suite down"
                    :disabled="index === planSuites.length - 1"
                    @click="moveSuite(suite, 1)"
                  >
                    ↓
                  </button>
                  <button
                    v-if="canTest && !suite.archived_at"
                    type="button"
                    class="btn-ghost text-[11px]"
                    @click="openAddCases(suite)"
                  >
                    Add cases
                  </button>
                  <button
                    v-if="canTest && !suite.archived_at"
                    type="button"
                    class="btn-ghost text-[11px]"
                    :disabled="!suiteCaseIds(suite.id).length"
                    @click="openCreateRun({ scope: 'suite', suiteId: suite.id, planId: suite.plan_id })"
                  >
                    Run suite
                  </button>
                  <button
                    v-if="canTest"
                    type="button"
                    class="btn-ghost text-[11px] text-slate-500"
                    @click="setSuiteArchived(suite, !suite.archived_at)"
                  >
                    {{ suite.archived_at ? 'Restore' : 'Archive' }}
                  </button>
                </div>
              </div>
            </div>
          </div>
          <div v-else class="rounded-xl border border-dashed border-slate-300 px-4 py-8 text-center">
            <p class="text-sm text-slate-600">No suites in this plan yet.</p>
            <button v-if="canTest && selectedPlan?.status !== 'archived'" type="button" class="btn-secondary mt-3" @click="openCreateSuite">
              <AppIcon name="plus" :size="14" /> Create the first suite
            </button>
          </div>

          <div v-if="selectedSuite" class="mt-3 rounded-xl border border-slate-200">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-3 py-2">
              <p class="text-xs font-semibold text-slate-700">
                {{ selectedSuite.name }} · {{ selectedSuiteCases.length }} cases
              </p>
              <button
                v-if="canTest && !selectedSuite.archived_at"
                type="button"
                class="btn-ghost text-[11px]"
                @click="openCreateRun({ scope: 'suite', suiteId: selectedSuite.id, planId: selectedSuite.plan_id })"
              >
                Run this suite
              </button>
            </div>
            <div v-if="selectedSuiteCases.length" class="max-h-48 divide-y divide-slate-100 overflow-y-auto">
              <div
                v-for="testCase in selectedSuiteCases"
                :key="testCase.id"
                data-testid="suite-case"
                class="flex items-center justify-between gap-2 px-3 py-2"
              >
                <div class="min-w-0">
                  <p class="truncate text-xs font-medium text-slate-800">{{ testCase.title }}</p>
                  <p class="text-[10px] text-slate-600">{{ featureName(testCase.feature_id) }}</p>
                </div>
                <button
                  v-if="canTest && !selectedSuite.archived_at"
                  type="button"
                  class="rounded p-1 text-slate-500 hover:bg-red-50 hover:text-red-600"
                  aria-label="Remove case from suite"
                  @click="removeSuiteCase(selectedSuite, testCase)"
                >
                  <AppIcon name="trash" :size="13" />
                </button>
              </div>
            </div>
            <p v-else class="px-3 py-4 text-xs text-slate-600">
              This suite has no cases. Add by filter or paste exact titles.
            </p>
          </div>
        </div>
      </div>

      <div v-else class="mt-4 rounded-xl border border-dashed border-slate-300 px-4 py-8 text-center">
        <p class="text-sm text-slate-600">No test plans yet. A plan groups suites and schedules a test cycle.</p>
        <button v-if="canTest" type="button" class="btn-secondary mt-3" @click="openCreatePlan">
          <AppIcon name="plus" :size="14" /> Create a test plan
        </button>
      </div>
    </section>

    <div class="grid gap-5 xl:grid-cols-12">
      <section class="xl:col-span-5">
        <div class="card overflow-hidden">
          <div class="border-b border-slate-100 px-5 py-4">
            <div class="flex items-center justify-between gap-2">
              <div>
                <h3 class="text-sm font-semibold text-slate-900">Test cases</h3>
                <p class="mt-0.5 text-xs text-slate-600">{{ testCases.length }} reusable definitions</p>
              </div>
              <div class="flex items-center gap-1.5">
                <button
                  v-if="testCases.length"
                  type="button"
                  class="btn-ghost px-2 text-[11px]"
                  :disabled="ioBusy"
                  title="Export every test case as JSON"
                  @click="exportCases"
                >
                  <AppIcon name="download" :size="13" /> Export
                </button>
                <button
                  v-if="canTest"
                  type="button"
                  class="btn-ghost px-2 text-[11px]"
                  :disabled="ioBusy"
                  title="Import test cases from JSON"
                  @click="triggerImport"
                >
                  <AppIcon name="upload" :size="13" /> Import
                </button>
                <button
                  v-if="canTest"
                  type="button"
                  class="btn-ghost px-2 text-[11px]"
                  title="Manage reusable step templates"
                  @click="openSharedManager"
                >
                  <AppIcon name="layers" :size="13" /> Templates
                </button>
                <span class="badge bg-cyan-50 text-cyan-700 ring-cyan-600/20">{{ filteredCases.length }} shown</span>
              </div>
            </div>
            <input
              ref="importInput"
              type="file"
              accept="application/json,.json"
              class="hidden"
              @change="onImportFile"
            />
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
              :action-label="canTest && features.length ? 'Create test case' : ''"
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
              <button v-if="canTest" type="button" class="btn-ghost text-xs" :disabled="!testCases.length" @click="openCreateRun()">
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
                  <p v-if="run.plan_name || run.suite_name" class="mt-0.5 text-[10px] text-cyan-700">
                    {{ [run.plan_name, run.suite_name].filter(Boolean).join(' · ') }} · snapshot
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
              :action-label="canTest && testCases.length ? 'Start a test run' : ''"
              @action="openCreateRun()"
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
          <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-600">Test steps</p>
            <div class="flex flex-wrap items-center gap-2">
              <template v-if="sharedSteps.length">
                <select v-model="sharedPick" class="field-input w-auto max-w-[190px] py-1 text-[11px]" aria-label="Shared step template">
                  <option value="">Insert shared step…</option>
                  <option v-for="shared in sharedSteps" :key="shared.id" :value="shared.id">{{ shared.name }}</option>
                </select>
                <button type="button" class="btn-ghost text-xs" :disabled="!sharedPick" @click="insertSharedStep">
                  <AppIcon name="layers" :size="13" /> Insert
                </button>
              </template>
              <button type="button" class="btn-ghost text-xs" @click="addStep">
                <AppIcon name="plus" :size="13" /> Add step
              </button>
            </div>
          </div>
          <div class="space-y-2">
            <div
              v-for="(step, index) in caseForm.steps"
              :key="step.key"
              data-testid="case-step-editor"
              class="rounded-xl border border-slate-200 bg-slate-50/60 p-3 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
              tabindex="0"
              @keydown="onStepKeydown($event, index)"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="flex items-center gap-2 text-[11px] font-semibold text-slate-600">
                  Step {{ index + 1 }}
                  <span v-if="step.shared_step_id" class="rounded bg-indigo-50 px-1.5 py-0.5 text-[9px] font-medium text-indigo-700">
                    copied from template
                  </span>
                </span>
                <div class="flex items-center gap-0.5">
                  <button
                    type="button"
                    class="rounded p-1 text-slate-600 hover:bg-slate-200 disabled:opacity-30"
                    :disabled="index === 0"
                    aria-label="Move step up"
                    title="Move step up (Alt+Up)"
                    @click="moveStep(index, -1)"
                  >
                    <AppIcon name="arrowUp" :size="13" />
                  </button>
                  <button
                    type="button"
                    class="rounded p-1 text-slate-600 hover:bg-slate-200 disabled:opacity-30"
                    :disabled="index === caseForm.steps.length - 1"
                    aria-label="Move step down"
                    title="Move step down (Alt+Down)"
                    @click="moveStep(index, 1)"
                  >
                    <AppIcon name="arrowDown" :size="13" />
                  </button>
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
              </div>
              <div class="mt-2 grid gap-2 sm:grid-cols-2">
                <textarea v-model="step.action" rows="2" class="field-input text-xs" placeholder="Action: what does the tester do?" />
                <textarea v-model="step.expected_result" rows="2" class="field-input text-xs" placeholder="Expected: what should happen?" />
              </div>
              <input v-model="step.test_data" class="field-input mt-2 py-1.5 text-xs" placeholder="Test data (optional)" />
              <p class="mt-1 text-[10px] text-slate-500">Select this step and press Alt+↑ or Alt+↓ to reorder without leaving the keyboard.</p>
            </div>
          </div>
        </div>
        <p v-if="caseError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ caseError }}</p>
      </div>
      <template #footer>
        <button
          v-if="caseEditingId"
          type="button"
          class="btn-ghost mr-auto text-xs"
          :disabled="caseBusy"
          @click="openEditingHistory"
        >
          <AppIcon name="history" :size="13" /> History
        </button>
        <button type="button" class="btn-secondary" :disabled="caseBusy" @click="caseModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="caseBusy" @click="saveCase">
          <AppIcon v-if="caseBusy" name="loader" :size="14" class="animate-spin" />
          {{ caseBusy ? 'Saving…' : caseEditingId ? 'Save test case' : 'Create test case' }}
        </button>
      </template>
    </BaseModal>

    <!-- Version history modal -->
    <BaseModal
      v-model="historyOpen"
      :title="`Version history · ${historyCaseTitle}`"
      max-width="lg"
    >
      <div class="space-y-3">
        <div class="flex items-center justify-between gap-2 text-xs text-slate-600">
          <span>{{ historyTotal }} snapshot{{ historyTotal === 1 ? '' : 's' }} · runs keep the revision they executed</span>
          <button type="button" class="btn-ghost px-2 text-xs" :disabled="historyBusy" @click="loadHistory">
            <AppIcon name="refresh" :size="13" /> Refresh
          </button>
        </div>
        <p v-if="historyError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ historyError }}</p>
        <div v-if="historyBusy" class="py-8 text-center text-xs text-slate-600">
          <AppIcon name="loader" :size="16" class="mx-auto animate-spin" />
        </div>
        <div v-else-if="!historyVersions.length" class="rounded-xl border border-dashed border-slate-300 py-8 text-center text-xs text-slate-600">
          No snapshots yet. The next edit creates one.
        </div>
        <div v-else class="max-h-[520px] space-y-3 overflow-y-auto pr-1">
          <article
            v-for="version in historyVersions"
            :key="version.id"
            data-testid="case-version"
            class="rounded-xl border border-slate-200 bg-white p-3"
          >
            <header class="flex flex-wrap items-start justify-between gap-2">
              <div class="min-w-0">
                <p class="text-xs font-semibold text-slate-800">Revision {{ version.revision }} · {{ version.title }}</p>
                <p class="mt-0.5 text-[10px] text-slate-600">
                  {{ version.changed_by_name || 'System' }} changed this case · {{ formatDateTime(version.created_at) }}
                </p>
              </div>
              <StatusPill :status="version.status" />
            </header>
            <p class="mt-1.5 text-[10px] text-slate-600">
              {{ version.feature_title || 'No feature' }} · {{ version.priority }} priority · {{ version.type }} ·
              {{ version.steps.length }} step{{ version.steps.length === 1 ? '' : 's' }}
            </p>
            <ol class="mt-2 space-y-1">
              <li
                v-for="step in version.steps"
                :key="step.position"
                class="rounded-lg bg-slate-50 px-2 py-1.5 text-[11px] text-slate-700"
              >
                <span class="font-semibold">{{ step.position }}.</span> {{ step.action }}
                <span class="mt-0.5 block text-[10px] text-slate-600">Expected: {{ step.expected_result }}</span>
                <span v-if="step.test_data" class="mt-0.5 block text-[10px] text-slate-500">Data: {{ step.test_data }}</span>
              </li>
            </ol>
          </article>
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" @click="historyOpen = false">Close</button>
      </template>
    </BaseModal>

    <!-- Shared step templates modal -->
    <BaseModal
      v-model="sharedModal"
      title="Shared step templates"
      max-width="lg"
      :persistent="sharedBusy"
    >
      <div class="space-y-4">
        <p class="rounded-lg bg-cyan-50 px-3 py-2 text-[11px] leading-relaxed text-cyan-800">
          Inserting a template copies its action, expected result and data into the case. Editing or deleting a template never changes a case that already used it.
        </p>
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="field-label" for="shared-name">Name</label>
            <input id="shared-name" v-model="sharedForm.name" class="field-input" placeholder="Sign in as a returning customer" />
          </div>
          <div>
            <label class="field-label" for="shared-data">Test data</label>
            <input id="shared-data" v-model="sharedForm.test_data" class="field-input" placeholder="qa+checkout@example.com" />
          </div>
          <div>
            <label class="field-label" for="shared-action">Action</label>
            <textarea id="shared-action" v-model="sharedForm.action" rows="2" class="field-input" placeholder="Open the sign-in page and submit valid credentials" />
          </div>
          <div>
            <label class="field-label" for="shared-expected">Expected result</label>
            <textarea id="shared-expected" v-model="sharedForm.expected_result" rows="2" class="field-input" placeholder="The account dashboard is shown" />
          </div>
        </div>
        <p v-if="sharedError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ sharedError }}</p>
        <div class="flex gap-2">
          <button type="button" class="btn-primary text-xs" :disabled="sharedBusy" @click="saveSharedStep">
            <AppIcon v-if="sharedBusy" name="loader" :size="13" class="animate-spin" />
            {{ sharedEditingId ? 'Save template' : 'Add template' }}
          </button>
          <button v-if="sharedEditingId" type="button" class="btn-secondary text-xs" :disabled="sharedBusy" @click="resetSharedForm">
            Cancel edit
          </button>
        </div>
        <div v-if="sharedSteps.length" class="max-h-[300px] divide-y divide-slate-100 overflow-y-auto rounded-xl border border-slate-200">
          <div v-for="step in sharedSteps" :key="step.id" class="flex items-start gap-3 px-3 py-2.5">
            <div class="min-w-0 flex-1">
              <p class="truncate text-xs font-semibold text-slate-800">{{ step.name }}</p>
              <p class="mt-0.5 truncate text-[11px] text-slate-600">{{ step.action }}</p>
              <p class="mt-0.5 truncate text-[10px] text-slate-500">Expected: {{ step.expected_result }}</p>
            </div>
            <div class="flex items-center gap-0.5">
              <button type="button" class="rounded p-1 text-slate-600 hover:bg-slate-100" aria-label="Edit shared step" @click="editSharedStep(step)">
                <AppIcon name="edit" :size="13" />
              </button>
              <button type="button" class="rounded p-1 text-slate-600 hover:bg-red-50 hover:text-red-600" aria-label="Delete shared step" :disabled="sharedBusy" @click="deleteSharedStep(step)">
                <AppIcon name="trash" :size="13" />
              </button>
            </div>
          </div>
        </div>
        <p v-else class="text-xs text-slate-600">No templates yet.</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" @click="sharedModal = false">Close</button>
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
          <div>
            <label class="field-label" for="run-scope">Run scope</label>
            <select id="run-scope" v-model="runForm.scope" class="field-input">
              <option value="all">All project cases</option>
              <option value="selection">Selected cases</option>
              <option value="suite">A suite</option>
              <option value="plan">A plan</option>
              <option value="filter">A filter</option>
            </select>
          </div>
        </div>

        <div v-if="runForm.scope === 'suite'" class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="run-suite">Suite</label>
            <select id="run-suite" v-model="runForm.suite_id" class="field-input">
              <option value="">Select a suite</option>
              <option v-for="suite in testSuites.filter((entry) => !entry.archived_at)" :key="suite.id" :value="suite.id">
                {{ suite.name }} · {{ caseCountLabel(suiteCaseIds(suite.id).length) }}
              </option>
            </select>
          </div>
          <div class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
            The run stores the suite name and the revision of every case it executes.
          </div>
        </div>

        <div v-else-if="runForm.scope === 'plan'" class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="field-label" for="run-plan">Plan</label>
            <select id="run-plan" v-model="runForm.plan_id" class="field-input">
              <option value="">Select a plan</option>
              <option v-for="plan in orderedPlans.filter((entry) => entry.status !== 'archived')" :key="plan.id" :value="plan.id">
                {{ plan.name }}
              </option>
            </select>
          </div>
          <div class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
            {{ caseCountLabel(runForm.plan_id ? planCaseIds(runForm.plan_id).length : 0) }} across active suites.
          </div>
        </div>

        <div v-else-if="runForm.scope === 'filter'" class="grid gap-4 sm:grid-cols-4">
          <div>
            <label class="field-label" for="run-filter-feature">Feature</label>
            <select id="run-filter-feature" v-model="runForm.filter_feature_id" class="field-input">
              <option value="">Any feature</option>
              <option v-for="feature in features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="run-filter-type">Type</label>
            <select id="run-filter-type" v-model="runForm.filter_type" class="field-input">
              <option value="">Any type</option>
              <option value="manual">Manual</option>
              <option value="automated">Automated</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="run-filter-priority">Priority</label>
            <select id="run-filter-priority" v-model="runForm.filter_priority" class="field-input">
              <option value="">Any priority</option>
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="run-filter-status">Status</label>
            <select id="run-filter-status" v-model="runForm.filter_status" class="field-input">
              <option value="">Any status</option>
              <option value="draft">Draft</option>
              <option value="ready">Ready</option>
              <option value="deprecated">Deprecated</option>
            </select>
          </div>
        </div>

        <div v-else-if="runForm.scope === 'selection'">
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

        <div class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
          {{ caseCountLabel(runScopeCount) }} will be added to this run.
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

    <!-- Test plan modal -->
    <BaseModal
      v-model="planModal"
      title="New test plan"
      max-width="lg"
      :persistent="planBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="plan-name">Plan name</label>
          <input id="plan-name" v-model="planForm.name" data-autofocus class="field-input" placeholder="Release 2.0 regression" />
        </div>
        <div>
          <label class="field-label" for="plan-description">Description</label>
          <textarea id="plan-description" v-model="planForm.description" rows="2" class="field-input" placeholder="Scope, entry criteria and test cycle notes" />
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label class="field-label" for="plan-owner">Owner</label>
            <select id="plan-owner" v-model="planForm.owner_id" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in members" :key="member.user_id" :value="member.user_id">
                {{ member.username || member.email }}
              </option>
            </select>
          </div>
          <div>
            <label class="field-label" for="plan-start">Start date</label>
            <input id="plan-start" v-model="planForm.start_date" type="date" class="field-input" />
          </div>
          <div>
            <label class="field-label" for="plan-end">End date</label>
            <input id="plan-end" v-model="planForm.end_date" type="date" class="field-input" />
          </div>
        </div>
        <p class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
          Plans start as draft. Archived plans are read-only and cannot start new runs; deleting a plan is not
          offered so historical runs keep their snapshot.
        </p>
        <p v-if="planError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ planError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="planBusy" @click="planModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="planBusy" @click="savePlan">
          <AppIcon v-if="planBusy" name="loader" :size="14" class="animate-spin" />
          {{ planBusy ? 'Creating…' : 'Create test plan' }}
        </button>
      </template>
    </BaseModal>

    <!-- Test suite modal -->
    <BaseModal
      v-model="suiteModal"
      title="New test suite"
      max-width="lg"
      :persistent="suiteBusy"
    >
      <div class="space-y-4">
        <div>
          <label class="field-label" for="suite-name">Suite name</label>
          <input id="suite-name" v-model="suiteForm.name" data-autofocus class="field-input" placeholder="Checkout smoke" />
        </div>
        <div>
          <label class="field-label" for="suite-description">Description</label>
          <textarea id="suite-description" v-model="suiteForm.description" rows="2" class="field-input" placeholder="What this ordered selection verifies" />
        </div>
        <div>
          <label class="field-label" for="suite-assignee">Assignee</label>
          <select id="suite-assignee" v-model="suiteForm.assignee_id" class="field-input">
            <option value="">Unassigned</option>
            <option v-for="member in members" :key="member.user_id" :value="member.user_id">
              {{ member.username || member.email }}
            </option>
          </select>
        </div>
        <p class="rounded-lg bg-slate-50 px-3 py-2 text-[11px] leading-relaxed text-slate-600">
          Suites are ordered inside the plan. The run stores the suite name and each case revision it executed.
        </p>
        <p v-if="suiteError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ suiteError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="suiteBusy" @click="suiteModal = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="suiteBusy" @click="saveSuite">
          <AppIcon v-if="suiteBusy" name="loader" :size="14" class="animate-spin" />
          {{ suiteBusy ? 'Creating…' : 'Create test suite' }}
        </button>
      </template>
    </BaseModal>

    <!-- Add cases to suite -->
    <BaseModal
      v-model="addCasesOpen"
      title="Add cases to suite"
      max-width="lg"
      :persistent="addCasesBusy"
    >
      <div class="space-y-4">
        <p class="text-xs text-slate-600">
          Adding to <span class="font-semibold text-slate-800">{{ selectedSuite?.name }}</span>. Cases already in the
          suite are skipped; an unknown CSV title rejects the whole request.
        </p>
        <div>
          <label class="field-label" for="add-cases-mode">Selection method</label>
          <select id="add-cases-mode" v-model="addCasesForm.mode" class="field-input">
            <option value="filter">Filter project cases</option>
            <option value="csv">Paste exact titles</option>
          </select>
        </div>
        <div v-if="addCasesForm.mode === 'filter'" class="grid gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2">
            <label class="field-label" for="add-cases-feature">Feature</label>
            <select id="add-cases-feature" v-model="addCasesForm.feature_id" class="field-input">
              <option value="">Any feature</option>
              <option v-for="feature in features" :key="feature.id" :value="feature.id">{{ feature.title }}</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="add-cases-type">Type</label>
            <select id="add-cases-type" v-model="addCasesForm.type" class="field-input">
              <option value="">Any type</option>
              <option value="manual">Manual</option>
              <option value="automated">Automated</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="add-cases-priority">Priority</label>
            <select id="add-cases-priority" v-model="addCasesForm.priority" class="field-input">
              <option value="">Any priority</option>
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div>
            <label class="field-label" for="add-cases-status">Status</label>
            <select id="add-cases-status" v-model="addCasesForm.status" class="field-input">
              <option value="">Any status</option>
              <option value="draft">Draft</option>
              <option value="ready">Ready</option>
              <option value="deprecated">Deprecated</option>
            </select>
          </div>
        </div>
        <div v-else>
          <label class="field-label" for="add-cases-csv">Exact case titles</label>
          <textarea
            id="add-cases-csv"
            v-model="addCasesForm.csv"
            rows="6"
            class="field-input font-mono text-xs"
            placeholder="Guest checkout with a saved card&#10;One-tap payment succeeds"
          />
        </div>
        <p v-if="addCasesError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ addCasesError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn-secondary" :disabled="addCasesBusy" @click="addCasesOpen = false">Cancel</button>
        <button type="button" class="btn-primary" :disabled="addCasesBusy" @click="saveAddCases">
          <AppIcon v-if="addCasesBusy" name="loader" :size="14" class="animate-spin" />
          {{ addCasesBusy ? 'Adding…' : 'Add cases' }}
        </button>
      </template>
    </BaseModal>

    <!-- Run detail modal -->
    <BaseModal
      v-model="detailOpen"
      :title="selectedRun?.name || 'Test run'"
      :description="selectedRun ? `${environmentName(selectedRun.environment_id)} · ${formatDateTime(selectedRun.created_at)}${selectedRun.plan_name ? ` · ${selectedRun.plan_name}` : ''}${selectedRun.suite_name ? ` · ${selectedRun.suite_name}` : ''}` : ''"
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
                v-if="canTest && (result.status === 'failed' || (result.step_results ?? []).some((step) => step.status === 'failed'))"
                type="button"
                class="rounded-lg border border-red-200 bg-red-50 px-2 py-1 text-[11px] font-semibold text-red-700 hover:bg-red-100"
                @click="openBug(result)"
              >
                Raise bug
              </button>
            </div>
          </div>

          <div class="space-y-3 px-4 py-3">
            <div v-if="canTest" class="flex flex-wrap gap-1.5">
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
              v-if="canTest"
              v-model="resultNotes[result.id]"
              rows="2"
              class="field-input text-xs"
              placeholder="Actual result or execution notes for this case…"
            />

            <div v-for="stepResult in result.step_results ?? []" :key="stepResult.id" data-testid="test-step-result" class="rounded-lg border border-slate-100 bg-slate-50/70 p-3">
              <div class="flex flex-wrap items-start justify-between gap-2">
                <div class="min-w-0 flex-1">
                  <p class="text-[11px] font-semibold text-slate-700">
                    Step {{ stepPosition(stepResult) }} · {{ stepAction(stepResult) }}
                  </p>
                  <p class="mt-0.5 text-[10px] text-slate-600">
                    Expected: {{ stepExpected(stepResult) }}
                  </p>
                  <p class="mt-0.5 text-[10px] text-slate-500">
                    Executed against case revision {{ stepResult.case_revision || result.case_revision || 1 }}
                  </p>
                </div>
                <div class="flex items-center gap-1.5">
                  <StatusPill :status="stepResult.status" :dot="false" />
                  <button
                    v-if="canTest && stepResult.status === 'failed'"
                    type="button"
                    class="text-[10px] font-semibold text-red-600 hover:text-red-700"
                    @click="openBug(result, stepResult)"
                  >
                    Raise bug
                  </button>
                </div>
              </div>
              <div v-if="canTest" class="mt-2 flex flex-wrap gap-1">
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
              <div v-if="canTest" class="mt-2 grid gap-2 sm:grid-cols-2">
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
