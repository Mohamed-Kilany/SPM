<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import PipelineStatusIcon from './PipelineStatusIcon.vue'
import { formatDateTime, personInitials, shortId } from '../lib/format'
import { useAuthStore } from '../stores/authStore'
import type { BoardColumn, ProjectMember, Ticket, UpdateTicketPayload } from '../types/project'

const props = defineProps<{
  open: boolean
  ticket: Ticket | null
  columns: BoardColumn[]
  members: ProjectMember[]
  saving?: boolean
  canWrite?: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'save', payload: UpdateTicketPayload): void
  (event: 'move', state: string): void
  (event: 'delete'): void
}>()

const auth = useAuthStore()

const title = ref('')
const description = ref('')
const priority = ref('medium')
const assignee = ref('')
const state = ref('')
const formError = ref('')
const titleInput = ref<HTMLInputElement | null>(null)

const priorities = [
  { value: 'critical', label: 'Critical', dot: 'bg-red-500', active: 'border-red-300 bg-red-50 text-red-700' },
  { value: 'high', label: 'High', dot: 'bg-orange-500', active: 'border-orange-300 bg-orange-50 text-orange-700' },
  { value: 'medium', label: 'Medium', dot: 'bg-amber-500', active: 'border-amber-300 bg-amber-50 text-amber-700' },
  { value: 'low', label: 'Low', dot: 'bg-emerald-500', active: 'border-emerald-300 bg-emerald-50 text-emerald-700' },
] as const

const canWrite = computed(() => props.canWrite ?? true)

const assigneeOptions = computed(() => {
  const options = props.members.map((member) => ({
    id: member.user_id,
    label:
      member.user_id === auth.user?.id
        ? `You · ${auth.displayName}`
        : `Member ${shortId(member.user_id, 8)}`,
    role: member.role,
  }))
  if (props.ticket?.assignee_id && !options.some((option) => option.id === props.ticket?.assignee_id)) {
    options.push({
      id: props.ticket.assignee_id,
      label: `Member ${shortId(props.ticket.assignee_id, 8)}`,
      role: 'member',
    })
  }
  return options
})

function fillForm(ticket: Ticket): void {
  title.value = ticket.title
  description.value = ticket.description ?? ''
  priority.value = ticket.priority || 'medium'
  assignee.value = ticket.assignee_id ?? ''
  state.value = ticket.state
  formError.value = ''
}

watch(
  () => [props.open, props.ticket?.id] as const,
  async ([open]) => {
    if (!open || !props.ticket) return
    fillForm(props.ticket)
    await nextTick()
    const input = titleInput.value
    if (!input) return
    input.focus()
    // Focus at the beginning so a long title is read from the start instead
    // of the browser scrolling the input to the caret at the end.
    input.setSelectionRange(0, 0)
    input.scrollLeft = 0
    // Chrome may apply the focus scroll after the selection change.
    requestAnimationFrame(() => {
      if (titleInput.value) titleInput.value.scrollLeft = 0
    })
  },
  { immediate: true },
)

watch(
  () => props.ticket?.state,
  (next) => {
    if (next && props.open) state.value = next
  },
)

function requestClose(): void {
  if (props.saving) return
  emit('close')
}

function handleKeydown(event: KeyboardEvent): void {
  if (props.open && event.key === 'Escape') {
    event.preventDefault()
    requestClose()
  }
}

function handleStateChange(event: Event): void {
  const next = (event.target as HTMLSelectElement).value
  state.value = next
  if (props.ticket && next !== props.ticket.state) emit('move', next)
}

function submit(): void {
  if (!canWrite.value) return
  const cleanTitle = title.value.trim()
  if (!cleanTitle) {
    formError.value = 'A ticket title is required.'
    titleInput.value?.focus()
    return
  }
  formError.value = ''
  emit('save', {
    title: cleanTitle,
    description: description.value.trim(),
    assignee_id: assignee.value || null,
    priority: priority.value as UpdateTicketPayload['priority'],
  })
}

watch(
  () => props.open,
  (open) => document.body.classList.toggle('drawer-open', open),
)

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleKeydown)
  document.body.classList.remove('drawer-open')
})
</script>

<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div
        v-if="open && ticket"
        class="fixed inset-0 z-[70] bg-slate-900/30 backdrop-blur-[2px]"
        @mousedown.self="requestClose"
      >
        <section
          class="absolute inset-y-0 right-0 flex w-full max-w-lg flex-col border-l border-slate-200 bg-white shadow-2xl"
          role="dialog"
          aria-modal="true"
          aria-labelledby="ticket-drawer-title"
        >
          <div class="relative border-b border-slate-200 px-6 pb-5 pt-6">
            <div class="pointer-events-none absolute inset-x-0 top-0 h-40 bg-[radial-gradient(circle_at_20%_0%,rgba(99,102,241,0.10),transparent_65%)]" />
            <div class="relative flex items-start justify-between gap-4">
              <div class="min-w-0">
                <div class="flex items-center gap-2 text-[10px] font-medium uppercase tracking-[0.16em] text-slate-600">
                  <span class="font-mono normal-case tracking-normal">TICKET-{{ shortId(ticket.id, 8) }}</span>
                  <span class="h-1 w-1 rounded-full bg-slate-300" />
                  <span>{{ state || ticket.state }}</span>
                </div>
                <input
                  id="ticket-drawer-title"
                  ref="titleInput"
                  v-model="title"
                  type="text"
                  maxlength="255"
                  :disabled="!canWrite || saving"
                  class="mt-2 w-full border-none bg-transparent p-0 text-xl font-semibold tracking-tight text-slate-900 outline-none placeholder:text-slate-600 disabled:opacity-70"
                  placeholder="Ticket title"
                  aria-label="Ticket title"
                />
              </div>
              <button
                type="button"
                class="flex h-8 w-8 flex-none items-center justify-center rounded-lg text-slate-600 hover:bg-slate-100 hover:text-slate-900"
                aria-label="Close ticket"
                @click="requestClose"
              >
                <AppIcon name="close" :size="18" />
              </button>
            </div>

            <div class="relative mt-4 flex flex-wrap items-center gap-2">
              <span
                v-if="ticket.branch_name"
                class="inline-flex max-w-full items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1 font-mono text-[10px] text-slate-600"
              >
                <AppIcon name="branch" :size="12" class="text-indigo-500" />
                <span class="truncate">{{ ticket.branch_name }}</span>
              </span>
              <span
                v-if="ticket.pipeline_status"
                class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1 text-[10px] text-slate-600"
              >
                <PipelineStatusIcon :status="ticket.pipeline_status" :size="12" />
                Pipeline
              </span>
              <span class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1 text-[10px] text-slate-600">
                <AppIcon name="clock" :size="12" />
                Updated {{ formatDateTime(ticket.updated_at) }}
              </span>
            </div>
          </div>

          <div class="flex-1 space-y-6 overflow-y-auto px-6 py-6">
            <div
              v-if="!canWrite"
              class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs text-amber-800"
            >
              You have read-only access to this project.
            </div>

            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <label class="block">
                <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
                  Workflow state
                </span>
                <select
                  :value="state"
                  :disabled="!canWrite || saving"
                  class="field-input"
                  @change="handleStateChange"
                >
                  <option v-for="column in columns" :key="column.id" :value="column.name">
                    {{ column.name }}
                  </option>
                </select>
              </label>

              <label class="block">
                <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
                  Assignee
                </span>
                <select v-model="assignee" :disabled="!canWrite || saving" class="field-input">
                  <option value="">Unassigned</option>
                  <option v-for="option in assigneeOptions" :key="option.id" :value="option.id">
                    {{ option.label }}
                  </option>
                </select>
              </label>
            </div>

            <div>
              <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
                Priority
              </span>
              <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <button
                  v-for="option in priorities"
                  :key="option.value"
                  type="button"
                  :disabled="!canWrite || saving"
                  class="flex items-center justify-center gap-1.5 rounded-lg border px-2 py-2 text-[11px] font-medium transition-colors disabled:opacity-50"
                  :class="priority === option.value
                    ? option.active
                    : 'border-slate-200 bg-white text-slate-600 hover:border-indigo-300 hover:text-indigo-700'"
                  @click="priority = option.value"
                >
                  <span class="h-1.5 w-1.5 rounded-full" :class="option.dot" />
                  {{ option.label }}
                </button>
              </div>
            </div>

            <label class="block">
              <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
                Description
              </span>
              <textarea
                v-model="description"
                rows="9"
                maxlength="10000"
                :disabled="!canWrite || saving"
                class="field-input resize-none leading-relaxed"
                placeholder="Add context, acceptance criteria or implementation notes…"
              />
              <span class="mt-1 block text-right text-[10px] text-slate-600">
                {{ description.length }} / 10,000
              </span>
            </label>

            <p
              v-if="formError"
              class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700"
              role="alert"
            >
              {{ formError }}
            </p>

            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-[10px] font-semibold uppercase tracking-[0.14em] text-slate-600">
                Assignment
              </p>
              <div class="mt-3 flex items-center gap-3">
                <span
                  v-if="assignee"
                  class="flex h-9 w-9 items-center justify-center rounded-full bg-gradient-to-br from-indigo-500 to-indigo-700 text-xs font-bold text-white"
                >
                  {{ personInitials(assignee) }}
                </span>
                <span
                  v-else
                  class="flex h-9 w-9 items-center justify-center rounded-full border border-dashed border-slate-300 text-slate-600"
                >
                  <AppIcon name="user" :size="16" />
                </span>
                <div class="min-w-0">
                  <p class="truncate text-sm text-slate-800">
                    {{ assignee ? assigneeOptions.find((option) => option.id === assignee)?.label ?? 'Assigned member' : 'Unassigned' }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div class="flex items-center gap-3 border-t border-slate-200 bg-slate-50 px-6 py-4">
            <button
              v-if="canWrite"
              type="button"
              class="flex items-center gap-1.5 rounded-lg px-2.5 py-2 text-xs font-medium text-red-600 hover:bg-red-50 hover:text-red-700 disabled:opacity-50"
              :disabled="saving"
              @click="emit('delete')"
            >
              <AppIcon name="trash" :size="14" />
              Delete
            </button>
            <span class="flex-1" />
            <button
              type="button"
              class="rounded-lg px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
              :disabled="saving"
              @click="requestClose"
            >
              Cancel
            </button>
            <button
              v-if="canWrite"
              type="button"
              class="inline-flex items-center gap-2 rounded-lg bg-accent-600 px-4 py-2 text-sm font-semibold text-white hover:bg-accent-700 disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="saving || !title.trim()"
              @click="submit"
            >
              <AppIcon v-if="saving" name="loader" :size="15" class="animate-spin" />
              {{ saving ? 'Saving…' : 'Save changes' }}
            </button>
          </div>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 180ms ease;
}
.drawer-enter-active section,
.drawer-leave-active section {
  transition: transform 260ms cubic-bezier(0.22, 1, 0.36, 1);
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
.drawer-enter-from section,
.drawer-leave-to section {
  transform: translateX(32px);
}
</style>
