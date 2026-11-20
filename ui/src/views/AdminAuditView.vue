<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import BaseModal from '../components/BaseModal.vue'
import { api } from '../api/client'
import { formatDateTime } from '../lib/format'
import { useToastStore } from '../stores/toastStore'
import type { AuditEvent, AuditVerification } from '../types/audit'

const toast = useToastStore()

const events = ref<AuditEvent[]>([])
const cursor = ref('')
const loading = ref(true)
const loadingMore = ref(false)
const loadError = ref('')
const selected = ref<AuditEvent | null>(null)
const verifying = ref(false)
const verification = ref<AuditVerification | null>(null)
const exporting = ref<'csv' | 'json' | ''>('')

const filters = reactive({
  action: '',
  actor_id: '',
  project_id: '',
  result: '',
  from: '',
  to: '',
})

const activeFilterCount = computed(
  () => Object.values(filters).filter((value) => value.trim() !== '').length,
)

const failureCount = computed(() => events.value.filter((event) => event.result !== 'success').length)

const chainClass = computed(() => {
  if (!verification.value) return 'bg-slate-100 text-slate-600 ring-slate-200'
  return verification.value.ok
    ? 'bg-emerald-50 text-emerald-700 ring-emerald-200'
    : 'bg-red-50 text-red-700 ring-red-200'
})

function resultClass(result: string): string {
  switch (result) {
    case 'success':
      return 'bg-emerald-50 text-emerald-700 ring-emerald-200'
    case 'denied':
      return 'bg-amber-50 text-amber-700 ring-amber-200'
    default:
      return 'bg-red-50 text-red-700 ring-red-200'
  }
}

function actorLabel(event: AuditEvent): string {
  if (event.actor_username) return `@${event.actor_username}`
  if (event.actor_id) return event.actor_id
  return event.actor_role || 'system'
}

function pretty(value: unknown): string {
  if (value === undefined || value === null) return '—'
  return JSON.stringify(value, null, 2)
}

function queryFilters() {
  return {
    action: filters.action.trim(),
    actor_id: filters.actor_id.trim(),
    project_id: filters.project_id.trim(),
    result: filters.result,
    from: filters.from,
    to: filters.to,
  }
}

async function load(append = false): Promise<void> {
  if (append) {
    if (!cursor.value || loadingMore.value) return
    loadingMore.value = true
  } else {
    loading.value = true
    cursor.value = ''
  }
  loadError.value = ''
  try {
    const page = await api.adminListAuditEvents({
      ...queryFilters(),
      limit: 50,
      cursor: append ? cursor.value : undefined,
    })
    events.value = append ? [...events.value, ...page.events] : page.events
    cursor.value = page.next_cursor ?? ''
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : 'Failed to load audit events'
    if (!append) events.value = []
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function applyFilters(): void {
  void load(false)
}

function resetFilters(): void {
  filters.action = ''
  filters.actor_id = ''
  filters.project_id = ''
  filters.result = ''
  filters.from = ''
  filters.to = ''
  void load(false)
}

async function verifyChain(): Promise<void> {
  verifying.value = true
  try {
    verification.value = await api.adminVerifyAuditChain()
    if (verification.value.ok) {
      toast.success(
        `Verified ${verification.value.checked} event(s). No tampering detected.`,
        'Audit chain',
      )
    } else {
      toast.error(
        `Chain broken at ${verification.value.first_bad_id ?? 'an unknown event'} (${verification.value.first_bad_cause ?? 'unknown cause'}).`,
        'Audit chain',
      )
    }
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Verification failed', 'Audit chain')
  } finally {
    verifying.value = false
  }
}

async function exportEvents(format: 'csv' | 'json'): Promise<void> {
  exporting.value = format
  try {
    const { blob, filename } = await api.adminExportAudit(queryFilters(), format)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    link.click()
    URL.revokeObjectURL(url)
    toast.success(`Exported audit events as ${format.toUpperCase()}.`, 'Audit log')
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Export failed', 'Audit log')
  } finally {
    exporting.value = ''
  }
}

onMounted(() => void load())
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">Administration</p>
        <h1 class="mt-2 text-2xl font-semibold tracking-tight text-slate-900">Audit log</h1>
        <p class="mt-1 text-sm text-slate-600">
          Append-only, hash-chained record of security and privileged actions.
        </p>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn-secondary" :disabled="verifying" @click="verifyChain">
          <AppIcon v-if="verifying" name="loader" :size="15" class="animate-spin" />
          <AppIcon v-else name="shield" :size="15" />
          Verify chain
        </button>
        <button type="button" class="btn-secondary" :disabled="exporting !== ''" @click="exportEvents('csv')">
          <AppIcon v-if="exporting === 'csv'" name="loader" :size="15" class="animate-spin" />
          <AppIcon v-else name="file" :size="15" />
          CSV
        </button>
        <button type="button" class="btn-secondary" :disabled="exporting !== ''" @click="exportEvents('json')">
          <AppIcon v-if="exporting === 'json'" name="loader" :size="15" class="animate-spin" />
          <AppIcon v-else name="file" :size="15" />
          JSON
        </button>
      </div>
    </header>

    <div class="mt-6 grid gap-3 sm:grid-cols-3">
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Events loaded</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ events.length }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Non-success results</p>
        <p class="mt-1 text-2xl font-semibold text-slate-900">{{ failureCount }}</p>
      </div>
      <div class="card p-4">
        <p class="text-[10px] font-semibold uppercase tracking-[0.16em] text-slate-500">Chain status</p>
        <span class="badge mt-2" :class="chainClass">
          {{ verification ? (verification.ok ? 'Intact' : 'Broken') : 'Not verified' }}
        </span>
        <p v-if="verification" class="mt-1 text-[11px] text-slate-500">
          {{ verification.checked }} event(s) · {{ formatDateTime(verification.checked_at) }}
        </p>
      </div>
    </div>

    <form class="card mt-5 space-y-3 p-4" @submit.prevent="applyFilters">
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <label class="block">
          <span class="field-label">Action prefix</span>
          <input v-model="filters.action" class="field-input" placeholder="project.member." />
        </label>
        <label class="block">
          <span class="field-label">Result</span>
          <select v-model="filters.result" class="field-input">
            <option value="">Any result</option>
            <option value="success">Success</option>
            <option value="failure">Failure</option>
            <option value="denied">Denied</option>
          </select>
        </label>
        <label class="block">
          <span class="field-label">Actor ID</span>
          <input v-model="filters.actor_id" class="field-input" placeholder="UUID" />
        </label>
        <label class="block">
          <span class="field-label">Project ID</span>
          <input v-model="filters.project_id" class="field-input" placeholder="UUID" />
        </label>
        <label class="block">
          <span class="field-label">From</span>
          <input v-model="filters.from" type="date" class="field-input" />
        </label>
        <label class="block">
          <span class="field-label">To</span>
          <input v-model="filters.to" type="date" class="field-input" />
        </label>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <p class="text-[11px] text-slate-500">
          {{ activeFilterCount }} active filter(s). Newest events first; exports honour the same filters.
        </p>
        <div class="flex gap-2">
          <button type="button" class="btn-ghost" @click="resetFilters">Reset</button>
          <button type="submit" class="btn-primary">
            <AppIcon name="filter" :size="15" />
            Apply filters
          </button>
        </div>
      </div>
    </form>

    <div v-if="loading" class="mt-5 space-y-2">
      <div v-for="index in 5" :key="index" class="skeleton h-16 rounded-xl" />
    </div>

    <div v-else-if="loadError" class="card mt-5 p-8 text-center">
      <span class="mx-auto flex h-11 w-11 items-center justify-center rounded-xl bg-red-50 text-red-600">
        <AppIcon name="alert" :size="20" />
      </span>
      <h2 class="mt-3 text-base font-semibold text-slate-900">Audit log unavailable</h2>
      <p class="mt-1 text-sm text-slate-600">{{ loadError }}</p>
      <button type="button" class="btn-secondary mt-4" @click="load(false)">Try again</button>
    </div>

    <div v-else class="table-shell mt-5">
      <table class="min-w-full divide-y divide-slate-200 text-sm">
        <thead class="table-head">
          <tr>
            <th class="px-4 py-3">When</th>
            <th class="px-4 py-3">Actor</th>
            <th class="px-4 py-3">Action</th>
            <th class="hidden px-4 py-3 lg:table-cell">Target</th>
            <th class="hidden px-4 py-3 lg:table-cell">Project</th>
            <th class="px-4 py-3">Result</th>
            <th class="px-4 py-3 text-right">Details</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100">
          <tr v-for="event in events" :key="event.id" class="table-row">
            <td class="px-4 py-3 text-slate-600">
              <span class="block font-medium text-slate-900">{{ formatDateTime(event.occurred_at) }}</span>
              <span class="font-mono text-[11px] text-slate-500">{{ event.id.slice(0, 8) }}</span>
            </td>
            <td class="px-4 py-3">
              <span class="block font-medium text-slate-900">{{ actorLabel(event) }}</span>
              <span v-if="event.ip" class="text-[11px] text-slate-500">{{ event.ip }}</span>
            </td>
            <td class="px-4 py-3">
              <code class="rounded bg-slate-100 px-1.5 py-0.5 text-[11px] font-medium text-slate-700">{{ event.action }}</code>
              <span v-if="event.reason" class="mt-1 block text-[11px] text-slate-500">{{ event.reason }}</span>
            </td>
            <td class="hidden px-4 py-3 text-slate-600 lg:table-cell">
              <span class="block">{{ event.target_type || '—' }}</span>
              <span class="font-mono text-[11px] text-slate-500">{{ event.target_id ? event.target_id.slice(0, 12) : '' }}</span>
            </td>
            <td class="hidden px-4 py-3 text-slate-600 lg:table-cell">
              {{ event.project_name || (event.project_id ? event.project_id.slice(0, 8) : '—') }}
            </td>
            <td class="px-4 py-3">
              <span class="badge" :class="resultClass(event.result)">{{ event.result }}</span>
            </td>
            <td class="px-4 py-3 text-right">
              <button type="button" class="btn-ghost" @click="selected = event">
                <AppIcon name="eye" :size="15" />
                View
              </button>
            </td>
          </tr>
          <tr v-if="!events.length">
            <td colspan="7" class="px-4 py-10 text-center text-sm text-slate-500">
              No audit events match this filter.
            </td>
          </tr>
        </tbody>
      </table>
      <div v-if="cursor" class="flex justify-center border-t border-slate-200 px-4 py-3">
        <button type="button" class="btn-secondary" :disabled="loadingMore" @click="load(true)">
          <AppIcon v-if="loadingMore" name="loader" :size="15" class="animate-spin" />
          Load more
        </button>
      </div>
    </div>

    <BaseModal
      :model-value="Boolean(selected)"
      title="Audit event"
      :description="selected ? selected.action : ''"
      max-width="lg"
      @update:model-value="(value) => { if (!value) selected = null }"
    >
      <div v-if="selected" class="space-y-4">
        <dl class="grid gap-3 sm:grid-cols-2">
          <div>
            <dt class="field-label">Occurred</dt>
            <dd class="text-sm text-slate-900">{{ formatDateTime(selected.occurred_at) }}</dd>
          </div>
          <div>
            <dt class="field-label">Result</dt>
            <dd><span class="badge" :class="resultClass(selected.result)">{{ selected.result }}</span></dd>
          </div>
          <div>
            <dt class="field-label">Actor</dt>
            <dd class="text-sm text-slate-900">{{ actorLabel(selected) }}</dd>
          </div>
          <div>
            <dt class="field-label">Target</dt>
            <dd class="break-all text-sm text-slate-900">{{ selected.target_type || '—' }} {{ selected.target_id || '' }}</dd>
          </div>
          <div>
            <dt class="field-label">Project</dt>
            <dd class="break-all text-sm text-slate-900">{{ selected.project_name || selected.project_id || '—' }}</dd>
          </div>
          <div>
            <dt class="field-label">Request ID</dt>
            <dd class="break-all font-mono text-[11px] text-slate-600">{{ selected.request_id || '—' }}</dd>
          </div>
          <div v-if="selected.reason" class="sm:col-span-2">
            <dt class="field-label">Reason</dt>
            <dd class="text-sm text-slate-900">{{ selected.reason }}</dd>
          </div>
          <div v-if="selected.user_agent" class="sm:col-span-2">
            <dt class="field-label">User agent</dt>
            <dd class="break-all text-xs text-slate-600">{{ selected.user_agent }}</dd>
          </div>
        </dl>

        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <p class="field-label">Before</p>
            <pre class="mt-1 max-h-56 overflow-auto rounded-lg bg-slate-900 p-3 text-[11px] leading-relaxed text-slate-100">{{ pretty(selected.before) }}</pre>
          </div>
          <div>
            <p class="field-label">After</p>
            <pre class="mt-1 max-h-56 overflow-auto rounded-lg bg-slate-900 p-3 text-[11px] leading-relaxed text-slate-100">{{ pretty(selected.after) }}</pre>
          </div>
        </div>

        <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
          <p class="field-label">Hash chain</p>
          <p class="mt-1 break-all font-mono text-[11px] text-slate-600">prev: {{ selected.prev_hash || '(genesis)' }}</p>
          <p class="mt-1 break-all font-mono text-[11px] text-slate-600">hash: {{ selected.hash || '—' }}</p>
        </div>

        <div class="flex justify-end">
          <button type="button" class="btn-secondary" @click="selected = null">Close</button>
        </div>
      </div>
    </BaseModal>
  </div>
</template>
