<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import BaseModal from '../BaseModal.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import AppIcon from '../AppIcon.vue'
import StatusPill from './StatusPill.vue'
import { ApiError, api } from '../../api/client'
import { useToastStore } from '../../stores/toastStore'
import { formatDateTime } from '../../lib/format'
import { statusLabel } from '../../lib/workspace'
import {
  createDebouncer,
  firstPendingIndex,
  flattenRunSteps,
  moveIndex,
  nextPendingIndex,
  parseRunnerResume,
  passRate,
  runStorageKey,
  runnerCounts,
  shortcutStatus,
} from '../../lib/testRunner'
import type { RunnerStep } from '../../lib/testRunner'
import type { TestRun, TestRunFinishResult } from '../../types/workspace'

const props = defineProps<{
  open: boolean
  run: TestRun | null
  projectId: string
  caseTitle: (caseId: string) => string
  canTest: boolean
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'refresh'): void
  (event: 'raise-bug', resultId: string, stepResultId: string): void
  (event: 'finished', outcome: string): void
}>()

const toast = useToastStore()

const currentIndex = ref(0)
const notes = reactive<Record<string, string>>({})
const evidence = reactive<Record<string, string>>({})
const localStatus = reactive<Record<string, string>>({})
const busyStep = ref<string | null>(null)
const evidenceBusy = ref(false)
const evidenceTarget = ref<RunnerStep | null>(null)
const evidenceInput = ref<HTMLInputElement | null>(null)
const errorMessage = ref('')
const finishing = ref(false)
const finishConfirm = ref(false)
const summary = ref<TestRunFinishResult | null>(null)
const showSummary = ref(false)

const statusOptions = [
  { status: 'passed', shortcut: 'P' },
  { status: 'failed', shortcut: 'F' },
  { status: 'blocked', shortcut: 'B' },
  { status: 'skipped', shortcut: 'S' },
] as const

const serverSteps = computed(() => flattenRunSteps(props.run, props.caseTitle))

const steps = computed<RunnerStep[]>(() =>
  serverSteps.value.map((step) => ({
    ...step,
    status: localStatus[step.stepResultId] ?? step.status,
    actualResult: notes[step.stepResultId] ?? step.actualResult,
    evidenceUrl: evidence[step.stepResultId] ?? step.evidenceUrl,
  })),
)

const current = computed<RunnerStep | null>(() => steps.value[currentIndex.value] ?? null)
const counts = computed(() => runnerCounts(steps.value))
const currentCaseStepCount = computed(() =>
  current.value ? steps.value.filter((step) => step.resultId === current.value?.resultId).length : 0,
)
const editable = computed(() => props.canTest && props.run?.status !== 'completed')
const incompleteCases = computed(
  () => (props.run?.results ?? []).filter((result) => result.status === 'not_run').length,
)
const finishLabel = computed(() => (props.run?.status === 'completed' ? 'View summary' : 'Finish run'))
const outcomeTone = computed(() => {
  switch (summary.value?.outcome) {
    case 'passed':
      return 'bg-emerald-50 text-emerald-700 ring-emerald-200'
    case 'failed':
      return 'bg-red-50 text-red-700 ring-red-200'
    default:
      return 'bg-amber-50 text-amber-700 ring-amber-200'
  }
})
const summaryPassRate = computed(() =>
  summary.value ? passRate(summary.value.passed, summary.value.failed) : 0,
)

// Autosave batches note edits; status clicks save immediately.
const dirty = new Set<string>()
const autosave = createDebouncer(() => {
  void flushDrafts()
}, 700)
let flushing = false

function storageKey(): string {
  return props.run ? runStorageKey(props.projectId, props.run.id) : ''
}

function initializeDrafts(): void {
  for (const step of serverSteps.value) {
    if (notes[step.stepResultId] === undefined) notes[step.stepResultId] = step.actualResult
    if (evidence[step.stepResultId] === undefined) evidence[step.stepResultId] = step.evidenceUrl
    if (localStatus[step.stepResultId] === undefined) localStatus[step.stepResultId] = step.status
  }
}

function persistResume(): void {
  if (typeof window === 'undefined' || !props.run) return
  try {
    window.localStorage.setItem(
      storageKey(),
      JSON.stringify({
        stepResultId: steps.value[currentIndex.value]?.stepResultId,
        notes: { ...notes },
        evidence: { ...evidence },
      }),
    )
  } catch {
    // Storage can be unavailable in private mode; resume is best effort.
  }
}

function clearResume(): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.removeItem(storageKey())
  } catch {
    // Ignore storage failures.
  }
}

function restore(): void {
  initializeDrafts()
  summary.value = null
  showSummary.value = false
  finishConfirm.value = false
  errorMessage.value = ''
  let savedIndex = -1
  if (typeof window !== 'undefined' && props.run) {
    const saved = parseRunnerResume(window.localStorage.getItem(storageKey()))
    if (saved) {
      for (const [id, value] of Object.entries(saved.notes)) notes[id] = value
      for (const [id, value] of Object.entries(saved.evidence)) evidence[id] = value
      if (saved.stepResultId) {
        savedIndex = steps.value.findIndex((step) => step.stepResultId === saved.stepResultId)
      }
    }
  }
  currentIndex.value = savedIndex >= 0 ? savedIndex : firstPendingIndex(steps.value)
  persistResume()
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    await nextTick()
    restore()
  },
  { immediate: true },
)

watch(
  () => props.run?.id,
  () => {
    if (props.open) restore()
  },
)

watch(
  () => props.run?.updated_at,
  () => {
    initializeDrafts()
    if (currentIndex.value >= steps.value.length) {
      currentIndex.value = Math.max(0, steps.value.length - 1)
    }
  },
)

watch(currentIndex, persistResume)

function go(delta: 1 | -1): void {
  const next = moveIndex(steps.value.length, currentIndex.value, delta)
  if (next >= 0) currentIndex.value = next
}

function jump(index: number): void {
  if (index >= 0 && index < steps.value.length) currentIndex.value = index
}

function goNext(): void {
  const pending = nextPendingIndex(steps.value, currentIndex.value)
  currentIndex.value = pending >= 0 ? pending : moveIndex(steps.value.length, currentIndex.value, 1)
}

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return Boolean(target.closest('input, textarea, select, [contenteditable="true"]'))
}

function onKeydown(event: KeyboardEvent): void {
  if (!props.open || finishConfirm.value || showSummary.value) return
  if (event.ctrlKey || event.metaKey || event.altKey) return
  if (isTypingTarget(event.target)) return
  const step = current.value
  if (!step) return
  const status = shortcutStatus(event.key)
  if (status) {
    event.preventDefault()
    void setStatus(step, status)
    return
  }
  if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
    event.preventDefault()
    go(1)
    return
  }
  if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
    event.preventDefault()
    go(-1)
    return
  }
  if (event.key.toLowerCase() === 'g' && step.status === 'failed') {
    event.preventDefault()
    emit('raise-bug', step.resultId, step.stepResultId)
    return
  }
  if (event.key.toLowerCase() === 'e') {
    event.preventDefault()
    chooseEvidence(step)
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  autosave.cancel()
})

async function persistStep(step: RunnerStep, status: string): Promise<void> {
  if (!props.run) return
  await api.patchTestStepResult(props.projectId, props.run.id, step.resultId, step.stepResultId, {
    status,
    actual_result: notes[step.stepResultId] ?? step.actualResult,
    evidence_url: evidence[step.stepResultId] ?? step.evidenceUrl,
    bug_work_item_id: step.bugWorkItemId,
  })
}

async function flushDrafts(): Promise<void> {
  if (flushing || !props.run || !editable.value) return
  const pending = [...dirty]
  if (!pending.length) return
  dirty.clear()
  flushing = true
  try {
    for (const id of pending) {
      const step = steps.value.find((entry) => entry.stepResultId === id)
      if (!step) continue
      await persistStep(step, localStatus[id] ?? step.status)
    }
    persistResume()
    emit('refresh')
  } catch (error) {
    for (const id of pending) dirty.add(id)
    errorMessage.value = error instanceof Error ? error.message : 'Autosave failed'
  } finally {
    flushing = false
  }
}

function markDirty(step: RunnerStep): void {
  dirty.add(step.stepResultId)
  persistResume()
  autosave.schedule()
}

async function setStatus(step: RunnerStep, status: string): Promise<void> {
  if (!editable.value || busyStep.value) return
  busyStep.value = step.stepResultId
  errorMessage.value = ''
  const previous = localStatus[step.stepResultId] ?? step.status
  localStatus[step.stepResultId] = status
  dirty.delete(step.stepResultId)
  try {
    await persistStep(step, status)
    persistResume()
    emit('refresh')
  } catch (error) {
    localStatus[step.stepResultId] = previous
    errorMessage.value = error instanceof Error ? error.message : 'Failed to record step result'
  } finally {
    busyStep.value = null
  }
}

function chooseEvidence(step: RunnerStep): void {
  if (!editable.value || evidenceBusy.value) return
  evidenceTarget.value = step
  evidenceInput.value?.click()
}

async function onEvidenceSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  const step = evidenceTarget.value
  input.value = ''
  if (!file || !step || !props.run) return
  if (file.size > 10 * 1024 * 1024) {
    toast.error('Evidence files must be 10 MiB or smaller.', 'Test runner')
    evidenceTarget.value = null
    return
  }
  evidenceBusy.value = true
  try {
    const uploaded = await api.uploadTestStepEvidence(
      props.projectId,
      props.run.id,
      step.resultId,
      step.stepResultId,
      file,
    )
    evidence[step.stepResultId] = uploaded.evidence_url
    persistResume()
    emit('refresh')
    toast.success(`${file.name} attached to step ${step.position}.`, 'Evidence uploaded')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Failed to upload evidence', 'Test runner')
  } finally {
    evidenceBusy.value = false
    evidenceTarget.value = null
  }
}

async function finish(force: boolean): Promise<void> {
  if (!props.run || finishing.value) return
  finishing.value = true
  errorMessage.value = ''
  try {
    await flushDrafts()
    autosave.cancel()
    const result = await api.finishTestRun(props.projectId, props.run.id, force)
    summary.value = result
    showSummary.value = true
    finishConfirm.value = false
    clearResume()
    emit('refresh')
    emit('finished', result.outcome)
  } catch (error) {
    if (error instanceof ApiError && error.code === 'RUN_INCOMPLETE') {
      finishConfirm.value = true
    } else {
      errorMessage.value = error instanceof Error ? error.message : 'Failed to finish the run'
    }
  } finally {
    finishing.value = false
  }
}

function close(): void {
  autosave.flush()
  persistResume()
  emit('update:open', false)
}
</script>

<template>
  <BaseModal
    :model-value="open"
    :title="run?.name || 'Test runner'"
    :description="
      run
        ? `${run.plan_name || 'Ad hoc'}${run.suite_name ? ` · ${run.suite_name}` : ''} · case revision snapshots preserved`
        : ''
    "
    max-width="lg"
    :persistent="finishing"
    @update:model-value="(value) => { if (!value) close() }"
  >
    <div v-if="showSummary && summary" data-testid="runner-summary" class="max-h-[68vh] space-y-5 overflow-y-auto pr-1">
      <div class="rounded-xl border border-slate-200 p-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Run outcome</p>
            <span
              class="mt-1 inline-flex items-center rounded-full px-2.5 py-1 text-xs font-semibold ring-1 ring-inset"
              :class="outcomeTone"
            >
              {{ summary.outcome }}
            </span>
          </div>
          <div class="text-right">
            <p class="text-2xl font-semibold text-slate-900">{{ summaryPassRate }}%</p>
            <p class="text-[10px] text-slate-500">pass rate of executed cases</p>
          </div>
        </div>
        <div class="mt-4 grid grid-cols-3 gap-2 text-center sm:grid-cols-6">
          <div class="rounded-lg bg-slate-50 p-2">
            <p class="text-lg font-semibold text-slate-900">{{ summary.total }}</p>
            <p class="text-[10px] text-slate-600">Cases</p>
          </div>
          <div class="rounded-lg bg-emerald-50 p-2">
            <p class="text-lg font-semibold text-emerald-700">{{ summary.passed }}</p>
            <p class="text-[10px] text-emerald-700">Passed</p>
          </div>
          <div class="rounded-lg bg-red-50 p-2">
            <p class="text-lg font-semibold text-red-700">{{ summary.failed }}</p>
            <p class="text-[10px] text-red-600">Failed</p>
          </div>
          <div class="rounded-lg bg-amber-50 p-2">
            <p class="text-lg font-semibold text-amber-700">{{ summary.blocked }}</p>
            <p class="text-[10px] text-amber-700">Blocked</p>
          </div>
          <div class="rounded-lg bg-slate-50 p-2">
            <p class="text-lg font-semibold text-slate-700">{{ summary.skipped }}</p>
            <p class="text-[10px] text-slate-600">Skipped</p>
          </div>
          <div class="rounded-lg bg-slate-50 p-2">
            <p class="text-lg font-semibold text-slate-700">{{ summary.not_run }}</p>
            <p class="text-[10px] text-slate-600">Not run</p>
          </div>
        </div>
        <p v-if="summary.not_run" class="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700">
          Finished with {{ summary.not_run }} unexecuted
          {{ summary.not_run === 1 ? 'case' : 'cases' }}. The run is recorded as incomplete.
        </p>
        <p v-else class="mt-3 text-xs text-slate-600">
          All cases have a result. The pass rate counts passed and failed cases; blocked and skipped
          cases are reported separately.
        </p>
        <p v-if="summary.completed_at" class="mt-2 text-[11px] text-slate-500">
          Completed {{ formatDateTime(summary.completed_at) }}
        </p>
      </div>
      <p class="text-xs text-slate-600">
        Evidence, step notes and case results remain linked to this run and its case revision
        snapshots.
      </p>
    </div>

    <div v-else-if="run && steps.length" data-testid="test-runner" class="max-h-[68vh] space-y-4 overflow-y-auto pr-1">
      <div class="rounded-xl bg-slate-50 p-3">
        <div class="flex items-center gap-3">
          <div class="h-2 flex-1 overflow-hidden rounded-full bg-white">
            <div
              class="h-full rounded-full transition-all"
              :class="counts.failed ? 'bg-rose-500' : 'bg-emerald-500'"
              :style="{ width: `${counts.progress}%` }"
            />
          </div>
          <span class="text-xs font-semibold text-slate-700">
            {{ counts.executed }} / {{ counts.total }} steps
          </span>
        </div>
        <div class="mt-2 flex flex-wrap gap-1.5 text-[10px] font-medium">
          <span class="rounded bg-emerald-100 px-1.5 py-0.5 text-emerald-700">{{ counts.passed }} passed</span>
          <span class="rounded bg-red-100 px-1.5 py-0.5 text-red-700">{{ counts.failed }} failed</span>
          <span v-if="counts.blocked" class="rounded bg-amber-100 px-1.5 py-0.5 text-amber-700">{{ counts.blocked }} blocked</span>
          <span v-if="counts.skipped" class="rounded bg-slate-200 px-1.5 py-0.5 text-slate-700">{{ counts.skipped }} skipped</span>
          <span class="rounded bg-white px-1.5 py-0.5 text-slate-600">{{ counts.notRun }} not run</span>
          <span v-if="incompleteCases" class="ml-auto rounded bg-amber-50 px-1.5 py-0.5 text-amber-700">
            {{ incompleteCases }} {{ incompleteCases === 1 ? 'case' : 'cases' }} without a result
          </span>
        </div>
      </div>

      <div class="grid gap-3 sm:grid-cols-[200px_1fr]">
        <ol class="max-h-[42vh] space-y-1 overflow-y-auto rounded-xl border border-slate-200 p-1.5" aria-label="Run steps">
          <li v-for="(step, index) in steps" :key="step.key">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[11px]"
              :class="index === currentIndex ? 'bg-indigo-50 text-indigo-800' : 'text-slate-600 hover:bg-slate-50'"
              :aria-current="index === currentIndex ? 'step' : undefined"
              @click="jump(index)"
            >
              <span
                class="h-2 w-2 flex-none rounded-full"
                :class="{
                  'bg-emerald-500': step.status === 'passed',
                  'bg-red-500': step.status === 'failed',
                  'bg-amber-500': step.status === 'blocked',
                  'bg-slate-400': step.status === 'skipped',
                  'bg-slate-200': step.status === 'not_run',
                }"
              />
              <span class="min-w-0 flex-1 truncate">{{ step.caseTitle }}</span>
              <span class="flex-none text-[10px] text-slate-400">#{{ step.position }}</span>
            </button>
          </li>
        </ol>

        <section v-if="current" data-testid="runner-step" class="space-y-3 rounded-xl border border-slate-200 p-4" aria-live="polite">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="min-w-0">
              <p class="text-sm font-semibold text-slate-900">{{ current.caseTitle }}</p>
              <p class="mt-0.5 text-[10px] text-slate-500">
                Case revision {{ current.caseRevision }} · step {{ current.position }} of
                {{ currentCaseStepCount }}
              </p>
            </div>
            <StatusPill :status="current.status" />
          </div>

          <div>
            <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Action</p>
            <p class="mt-1 text-sm leading-relaxed text-slate-800">{{ current.action }}</p>
          </div>
          <div>
            <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Expected result</p>
            <p class="mt-1 text-sm leading-relaxed text-slate-800">{{ current.expectedResult }}</p>
          </div>
          <div v-if="current.testData">
            <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Test data</p>
            <p class="mt-1 whitespace-pre-wrap rounded-lg bg-slate-50 px-2 py-1.5 font-mono text-[11px] text-slate-700">{{ current.testData }}</p>
          </div>

          <div v-if="editable" class="flex flex-wrap gap-1.5">
            <button
              v-for="option in statusOptions"
              :key="option.status"
              type="button"
              :data-testid="`runner-status-${option.status}`"
              class="inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[11px] font-semibold"
              :class="
                current.status === option.status
                  ? 'border-indigo-300 bg-indigo-50 text-indigo-700'
                  : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300'
              "
              :disabled="busyStep === current.stepResultId"
              @click="setStatus(current, option.status)"
            >
              {{ statusLabel(option.status) }}
              <kbd class="rounded bg-white/70 px-1 text-[9px] font-semibold text-slate-500 ring-1 ring-inset ring-slate-200">{{ option.shortcut }}</kbd>
            </button>
          </div>

          <div>
            <label class="field-label" :for="`runner-notes-${current.stepResultId}`">Actual result / notes</label>
            <textarea
              :id="`runner-notes-${current.stepResultId}`"
              v-model="notes[current.stepResultId]"
              data-testid="runner-notes"
              rows="3"
              class="field-input text-xs"
              :readonly="!editable"
              placeholder="Observed behavior, error text, timing or context. Saves automatically."
              @input="markDirty(current)"
            />
          </div>

          <div class="rounded-lg border border-slate-100 bg-slate-50/70 p-2.5">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Evidence</p>
              <button
                v-if="editable"
                type="button"
                class="btn-ghost text-[11px]"
                data-testid="runner-evidence"
                :disabled="evidenceBusy"
                @click="chooseEvidence(current)"
              >
                <AppIcon v-if="evidenceBusy" name="loader" :size="13" class="animate-spin" />
                <AppIcon v-else name="upload" :size="13" />
                {{ evidenceBusy ? 'Uploading…' : current.evidenceUrl ? 'Replace file' : 'Attach file' }}
              </button>
            </div>
            <a
              v-if="current.evidenceUrl"
              :href="current.evidenceUrl"
              target="_blank"
              rel="noopener"
              class="mt-1 inline-block text-[11px] font-semibold text-indigo-700 hover:text-indigo-800"
            >
              View attached evidence
            </a>
            <p v-else class="mt-1 text-[11px] text-slate-500">No file attached yet.</p>
            <p class="mt-1 text-[10px] text-slate-500">
              Screenshot, log, PDF or short recording · validated and malware-scanned · max 10 MiB.
            </p>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-2">
            <button
              v-if="editable && current.status === 'failed'"
              type="button"
              class="rounded-lg border border-red-200 bg-red-50 px-2.5 py-1.5 text-[11px] font-semibold text-red-700 hover:bg-red-100"
              @click="emit('raise-bug', current.resultId, current.stepResultId)"
            >
              Raise linked bug <kbd class="ml-1 rounded bg-white px-1 text-[9px] ring-1 ring-inset ring-red-200">G</kbd>
            </button>
            <span v-else />
            <div class="flex items-center gap-1.5">
              <button type="button" class="btn-secondary px-2.5 py-1.5 text-[11px]" :disabled="currentIndex === 0" @click="go(-1)">
                Previous
              </button>
              <button type="button" class="btn-secondary px-2.5 py-1.5 text-[11px]" :disabled="currentIndex >= steps.length - 1" @click="go(1)">
                Next
              </button>
              <button
                v-if="counts.notRun"
                type="button"
                class="btn-secondary px-2.5 py-1.5 text-[11px]"
                @click="goNext"
              >
                Next pending
              </button>
            </div>
          </div>

          <p v-if="editable" class="text-[10px] text-slate-500">
            Shortcuts: P pass · F fail · B blocked · S skip · ←/→ move · G raise bug · E evidence
          </p>
        </section>
      </div>
    </div>

    <div v-else class="rounded-xl border border-dashed border-slate-300 py-10 text-center text-sm text-slate-600">
      This run has no step results to execute.
    </div>

    <input
      ref="evidenceInput"
      type="file"
      class="hidden"
      accept=".png,.jpg,.jpeg,.gif,.webp,.txt,.log,.csv,.json,.pdf,.zip,.mp4,.webm"
      @change="onEvidenceSelected"
    />

    <p v-if="errorMessage" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700">{{ errorMessage }}</p>

    <template #footer>
      <template v-if="showSummary">
        <button type="button" class="btn-primary" @click="close">Close runner</button>
      </template>
      <template v-else>
        <button type="button" class="btn-secondary" @click="close">Close</button>
        <button
          type="button"
          class="btn-primary"
          data-testid="runner-finish"
          :disabled="finishing"
          @click="finish(false)"
        >
          <AppIcon v-if="finishing" name="loader" :size="14" class="animate-spin" />
          {{ finishing ? 'Finishing…' : finishLabel }}
        </button>
      </template>
    </template>
  </BaseModal>

  <ConfirmDialog
    :open="finishConfirm"
    title="Finish with unexecuted cases?"
    :message="`${incompleteCases} ${incompleteCases === 1 ? 'case has' : 'cases have'} no result yet. Finishing now records the run as incomplete; you can still open it later to complete the remaining cases.`"
    confirm-label="Finish anyway"
    :busy="finishing"
    :danger="false"
    @confirm="finish(true)"
    @cancel="finishConfirm = false"
    @update:open="(value) => { if (!value) finishConfirm = false }"
  />
</template>
