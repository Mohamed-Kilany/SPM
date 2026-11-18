<script setup lang="ts">
import { computed, ref, toRef } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import BaseModal from '../components/BaseModal.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PipelineStatusIcon from '../components/PipelineStatusIcon.vue'
import TicketCard from '../components/TicketCard.vue'
import TicketDrawer from '../components/TicketDrawer.vue'
import { useProjectStream } from '../composables/useProjectStream'
import { api } from '../api/client'
import { useAuthStore } from '../stores/authStore'
import { useProjectStore } from '../stores/projectStore'
import { useToastStore } from '../stores/toastStore'
import {
  pipelineLabel,
  priorityRank,
  shortId,
  timeAgo,
} from '../lib/format'
import type {
  CreateTicketPayload,
  Notification,
  Ticket,
  UpdateTicketPayload,
} from '../types/project'

const props = defineProps<{ id: string }>()

const auth = useAuthStore()
const store = useProjectStore()
const toast = useToastStore()
const stream = useProjectStream(toRef(props, 'id'), {
  onToast: (message) => toast.error(message),
})

const { isLoading, error, reconnect } = stream

const canWrite = computed(() => auth.canWrite())

interface ColumnAccent {
  border: string
  header: string
  badge: string
  dot: string
}

const COLUMN_ACCENTS: Record<string, ColumnAccent> = {
  Backlog: {
    border: 'border-l-slate-500',
    header: 'bg-slate-500/10',
    badge: 'bg-slate-100 text-slate-700',
    dot: 'bg-slate-500',
  },
  'In Progress': {
    border: 'border-l-indigo-500',
    header: 'bg-indigo-50',
    badge: 'bg-indigo-50 text-indigo-700',
    dot: 'bg-accent-500',
  },
  'In Review': {
    border: 'border-l-blue-500',
    header: 'bg-blue-500/10',
    badge: 'bg-blue-50 text-blue-700',
    dot: 'bg-blue-500',
  },
  Done: {
    border: 'border-l-green-500',
    header: 'bg-emerald-50',
    badge: 'bg-emerald-50 text-emerald-700',
    dot: 'bg-green-500',
  },
}

function accentFor(name: string): ColumnAccent {
  return (
    COLUMN_ACCENTS[name] ?? {
      border: 'border-l-slate-400',
      header: 'bg-slate-50',
      badge: 'bg-slate-100 text-slate-700',
      dot: 'bg-navy-500',
    }
  )
}

const boardColumns = computed(() =>
  store.orderedColumns.map((column) => {
    const tickets = [...(store.ticketsByState[column.name] ?? [])].sort((a, b) => {
      const byPriority = (priorityRank[a.priority] ?? 4) - (priorityRank[b.priority] ?? 4)
      if (byPriority !== 0) return byPriority
      return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
    })
    return { column, accent: accentFor(column.name), tickets }
  }),
)

const totalTickets = computed(() => store.tickets.length)
const inProgressCount = computed(() => (store.ticketsByState['In Progress'] ?? []).length)
const inReviewCount = computed(() => (store.ticketsByState['In Review'] ?? []).length)
const doneCount = computed(() => (store.ticketsByState['Done'] ?? []).length)

const showLoadError = computed(
  () => Boolean(error.value) && !store.projectId && !isLoading.value,
)
const streamWarning = computed(
  () => Boolean(error.value) && Boolean(store.projectId),
)

const memberOptions = computed(() =>
  store.members.map((member) => ({
    id: member.user_id,
    role: member.role,
    label:
      member.user_id === auth.user?.id
        ? `You · ${auth.displayName}`
        : member.user_id === store.ownerId
          ? `Owner · ${shortId(member.user_id, 8)}`
          : `Member · ${shortId(member.user_id, 8)}`,
  })),
)

const notificationVisual = (notification: Notification) => {
  if (notification.type.includes('fail')) {
    return { icon: 'alert', cls: 'bg-red-50 text-red-600' }
  }
  if (notification.type.includes('merge')) {
    return { icon: 'rocket', cls: 'bg-emerald-50 text-emerald-700' }
  }
  return { icon: 'bell', cls: 'bg-accent-500/10 text-indigo-700' }
}

// ---------------------------------------------------------------------------
// Create ticket
// ---------------------------------------------------------------------------
const showCreate = ref(false)
const createTitle = ref('')
const createDescription = ref('')
const createState = ref('Backlog')
const createPriority = ref('medium')
const createAssignee = ref('')
const creating = ref(false)
const createError = ref('')

const createPriorities = [
  { value: 'critical', label: 'Critical', dot: 'bg-red-500' },
  { value: 'high', label: 'High', dot: 'bg-orange-500' },
  { value: 'medium', label: 'Medium', dot: 'bg-yellow-500' },
  { value: 'low', label: 'Low', dot: 'bg-green-500' },
] as const

function resetCreateForm(): void {
  createTitle.value = ''
  createDescription.value = ''
  createState.value = store.orderedColumns[0]?.name ?? 'Backlog'
  createPriority.value = 'medium'
  createAssignee.value = ''
  createError.value = ''
}

function openCreate(state?: string): void {
  resetCreateForm()
  if (state) createState.value = state
  showCreate.value = true
}

function closeCreate(): void {
  if (creating.value) return
  showCreate.value = false
  resetCreateForm()
}

async function createTicket(): Promise<void> {
  const title = createTitle.value.trim()
  if (!title) {
    createError.value = 'A ticket title is required.'
    return
  }

  const payload: CreateTicketPayload = {
    title,
    description: createDescription.value.trim(),
    state: createState.value,
    priority: createPriority.value as CreateTicketPayload['priority'],
    assignee_id: createAssignee.value || null,
  }

  creating.value = true
  createError.value = ''
  try {
    const result = await api.createTicket(props.id, payload)
    const now = new Date().toISOString()
    store.addTicket({
      id: result.id,
      title: payload.title,
      description: payload.description,
      state: payload.state,
      assignee_id: payload.assignee_id,
      priority: payload.priority,
      branch_name: '',
      pipeline_status: '',
      created_at: now,
      updated_at: now,
    })
    showCreate.value = false
    resetCreateForm()
    toast.success('The ticket was added to the board.', 'Ticket created')
  } catch (err) {
    createError.value = err instanceof Error ? err.message : 'Failed to create ticket'
  } finally {
    creating.value = false
  }
}

// ---------------------------------------------------------------------------
// Ticket drawer, edits and moves
// ---------------------------------------------------------------------------
const selectedTicketId = ref<string | null>(null)
const selectedTicket = computed(() => store.ticketById(selectedTicketId.value))
const saving = ref(false)

function openTicket(ticket: Ticket): void {
  selectedTicketId.value = ticket.id
}

function closeTicket(): void {
  if (saving.value) return
  selectedTicketId.value = null
}

async function handleMove(ticketId: string, state: string): Promise<void> {
  const ticket = store.tickets.find((item) => item.id === ticketId)
  if (!ticket || ticket.state === state) return

  const previous = { state: ticket.state, updated_at: ticket.updated_at }
  store.setTicketState(ticketId, state)
  try {
    await api.changeTicketState(props.id, ticketId, state)
    toast.success(`Ticket moved to ${state}.`, 'Board updated')
  } catch (err) {
    store.patchTicket(ticketId, previous)
    toast.error(err instanceof Error ? err.message : 'Failed to move ticket')
  }
}

async function handleSave(payload: UpdateTicketPayload): Promise<void> {
  const ticket = selectedTicket.value
  if (!ticket || saving.value) return

  saving.value = true
  try {
    await api.updateTicket(props.id, ticket.id, payload)
    store.patchTicket(ticket.id, payload)
    selectedTicketId.value = null
    toast.success('Ticket details saved.', 'Updated')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to update ticket')
  } finally {
    saving.value = false
  }
}

// ---------------------------------------------------------------------------
// Delete confirmation
// ---------------------------------------------------------------------------
const pendingDelete = ref<Ticket | null>(null)
const deleting = ref(false)

function requestDelete(ticket: Ticket): void {
  pendingDelete.value = ticket
}

function requestDeleteById(ticketId: string): void {
  const ticket = store.tickets.find((item) => item.id === ticketId)
  if (ticket) requestDelete(ticket)
}

async function confirmDelete(): Promise<void> {
  const ticket = pendingDelete.value
  if (!ticket || deleting.value) return

  deleting.value = true
  try {
    await api.deleteTicket(props.id, ticket.id)
    store.removeTicket(ticket.id)
    if (selectedTicketId.value === ticket.id) selectedTicketId.value = null
    pendingDelete.value = null
    toast.success(`"${ticket.title}" was deleted.`, 'Ticket removed')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to delete ticket')
  } finally {
    deleting.value = false
  }
}

// ---------------------------------------------------------------------------
// Header utilities
// ---------------------------------------------------------------------------
const notificationsOpen = ref(false)

async function refreshBoard(): Promise<void> {
  await reconnect().catch(() => undefined)
  if (error.value) toast.error(error.value)
  else toast.success('Board synchronised with the event store.', 'Up to date')
}

function subscriptionLabel(status: string): string {
  return pipelineLabel[status] ?? 'No runs'
}
</script>

<template>
  <div class="min-h-screen">
    <div class="pointer-events-none fixed inset-x-0 top-0 z-0 h-80 bg-[radial-gradient(ellipse_at_top,rgba(124,58,237,0.10),transparent_55%)]" />

    <div
      v-if="store.hasDegradedAlert"
      class="relative z-10 flex flex-wrap items-center gap-3 border-b border-red-200 bg-red-50 px-5 py-3 sm:px-8"
      role="alert"
    >
      <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-red-100 text-red-600">
        <AppIcon name="alert" :size="15" />
      </span>
      <p class="min-w-0 flex-1 text-xs text-red-700">
        Deployment health is degraded. Check the Kubernetes workload before promoting further changes.
      </p>
      <button
        type="button"
        class="rounded-lg px-2.5 py-1.5 text-[11px] font-medium text-red-700 hover:bg-red-50"
        @click="store.dismissDegradedAlert()"
      >
        Dismiss
      </button>
    </div>

    <header class="relative border-b border-slate-200 bg-white/80 backdrop-blur-sm">
      <div class="mx-auto max-w-[110rem] px-5 pb-5 pt-6 sm:px-8">
        <div class="flex items-center gap-2 text-[11px] text-slate-600">
          <RouterLink to="/projects" class="flex items-center gap-1.5 hover:text-slate-700">
            <AppIcon name="chevronLeft" :size="12" />
            Projects
          </RouterLink>
          <span class="text-slate-700">/</span>
          <span class="truncate text-slate-600">{{ store.name || 'Project board' }}</span>
        </div>

        <div class="mt-4 flex flex-col gap-5 xl:flex-row xl:items-start xl:justify-between">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2.5">
              <h1 class="truncate text-2xl font-semibold tracking-tight text-slate-900 sm:text-3xl">
                {{ store.name || (isLoading ? 'Loading board…' : 'Project board') }}
              </h1>
            </div>

            <p v-if="store.description" class="mt-2 max-w-3xl text-sm leading-relaxed text-slate-600">
              {{ store.description }}
            </p>

            <div v-if="store.projectId" class="mt-4 flex flex-wrap items-center gap-2">
              <span class="inline-flex max-w-[18rem] items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1.5 font-mono text-[10px] text-slate-600">
                <AppIcon name="repo" :size="12" class="text-slate-600" />
                <span class="truncate">{{ store.repoName || 'repository pending' }}</span>
              </span>
              <span class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1.5 font-mono text-[10px] text-slate-600">
                <AppIcon name="branch" :size="12" class="text-slate-600" />
                {{ store.defaultBranch }}
              </span>
              <span
                v-if="store.latestDeployment"
                class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1.5 text-[10px] text-slate-600"
              >
                <span
                  class="h-1.5 w-1.5 rounded-full"
                  :class="store.latestDeployment.health_status === 'Healthy' ? 'bg-green-500' : store.latestDeployment.health_status === 'Degraded' ? 'bg-red-500' : 'bg-amber-500'"
                />
                {{ store.latestDeployment.health_status }}
              </span>
              <span
                v-if="store.latestPipeline"
                class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1.5 text-[10px] text-slate-600"
              >
                <PipelineStatusIcon :status="store.latestPipeline.status" :size="12" />
                {{ subscriptionLabel(store.latestPipeline.status) }}
              </span>
              <span class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2 py-1.5 text-[10px] text-slate-600">
                <AppIcon name="users" :size="12" class="text-slate-600" />
                {{ store.members.length }} {{ store.members.length === 1 ? 'member' : 'members' }}
              </span>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="relative flex h-9 w-9 items-center justify-center rounded-lg border border-slate-200 bg-slate-50 text-slate-600 hover:bg-slate-100 hover:text-slate-900"
              :aria-expanded="notificationsOpen"
              aria-label="Notifications"
              @click="notificationsOpen = !notificationsOpen"
            >
              <AppIcon name="bell" :size="16" />
              <span
                v-if="store.notifications.length > 0"
                class="absolute -right-1 -top-1 flex h-4 min-w-[1rem] items-center justify-center rounded-full bg-accent-600 px-1 text-[9px] font-bold text-white ring-2 ring-white"
              >
                {{ store.notifications.length > 9 ? '9+' : store.notifications.length }}
              </span>
            </button>

            <button
              v-if="canWrite && store.projectId"
              type="button"
              class="inline-flex h-9 items-center gap-2 rounded-lg bg-accent-600 px-3.5 text-xs font-semibold text-white shadow-sm hover:bg-accent-700"
              @click="openCreate()"
            >
              <AppIcon name="plus" :size="14" />
              New ticket
            </button>

            <div v-if="notificationsOpen" class="fixed inset-0 z-40" @click="notificationsOpen = false" />
            <div
              v-if="notificationsOpen"
              class="absolute right-5 top-[8.75rem] z-50 w-[calc(100vw-2.5rem)] max-w-sm overflow-hidden rounded-xl border border-slate-200 bg-white shadow-2xl backdrop-blur-xl sm:right-8 xl:top-[7.25rem]"
            >
              <div class="flex items-center justify-between border-b border-slate-200 px-4 py-3">
                <p class="text-sm font-semibold text-slate-900">Notifications</p>
                <button
                  type="button"
                  class="rounded-md p-1 text-slate-600 hover:text-slate-900"
                  aria-label="Close notifications"
                  @click="notificationsOpen = false"
                >
                  <AppIcon name="close" :size="15" />
                </button>
              </div>
              <div class="max-h-80 overflow-y-auto">
                <div
                  v-for="notification in store.orderedNotifications"
                  :key="notification.id"
                  class="flex gap-3 border-b border-slate-100 px-4 py-3 last:border-b-0"
                >
                  <span
                    class="mt-0.5 flex h-7 w-7 flex-none items-center justify-center rounded-lg"
                    :class="notificationVisual(notification).cls"
                  >
                    <AppIcon :name="notificationVisual(notification).icon" :size="14" />
                  </span>
                  <div class="min-w-0">
                    <p class="text-xs leading-relaxed text-slate-700">{{ notification.message }}</p>
                    <p class="mt-1 text-[10px] text-slate-600">{{ timeAgo(notification.created_at) }}</p>
                  </div>
                </div>
                <div v-if="store.notifications.length === 0" class="px-4 py-10 text-center">
                  <AppIcon name="inbox" :size="22" class="mx-auto text-slate-600" />
                  <p class="mt-2 text-xs text-slate-600">No notifications yet.</p>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-if="store.projectId" class="mt-5 flex flex-wrap items-center gap-2.5">
          <div class="flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
            <AppIcon name="layers" :size="14" class="text-slate-600" />
            <span class="text-xs text-slate-600"><span class="font-semibold text-slate-900">{{ totalTickets }}</span> {{ totalTickets === 1 ? 'ticket' : 'tickets' }}</span>
          </div>
          <div class="flex items-center gap-3 rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-2">
            <span class="h-1.5 w-1.5 rounded-full bg-accent-500" />
            <span class="text-xs text-slate-600"><span class="font-semibold text-slate-900">{{ inProgressCount }}</span> in progress</span>
          </div>
          <div class="flex items-center gap-3 rounded-lg border border-blue-200 bg-blue-50 px-3 py-2">
            <span class="h-1.5 w-1.5 rounded-full bg-blue-500" />
            <span class="text-xs text-slate-600"><span class="font-semibold text-slate-900">{{ inReviewCount }}</span> in review</span>
          </div>
          <div class="flex items-center gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2">
            <span class="h-1.5 w-1.5 rounded-full bg-green-500" />
            <span class="text-xs text-slate-600"><span class="font-semibold text-slate-900">{{ doneCount }}</span> done</span>
          </div>
        </div>

        <div v-else-if="isLoading" class="mt-5 flex flex-wrap gap-2.5">
          <div v-for="index in 4" :key="index" class="skeleton h-9 w-32 rounded-lg" />
        </div>
      </div>
    </header>

    <div
      v-if="streamWarning"
      class="relative z-10 mx-5 mt-4 flex flex-wrap items-center gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 sm:mx-8"
      role="status"
    >
      <AppIcon name="alert" :size="15" class="text-amber-400" />
      <p class="min-w-0 flex-1 text-xs text-amber-800">{{ error }}</p>
      <button
        type="button"
        class="rounded-lg border border-amber-200 px-2.5 py-1.5 text-[11px] font-medium text-amber-800 hover:bg-amber-500/10"
        @click="refreshBoard"
      >
        Retry now
      </button>
    </div>

    <!-- Initial loading skeleton. -->
    <div
      v-if="isLoading && !store.projectId"
      class="mx-auto flex max-w-[110rem] gap-4 overflow-hidden px-5 py-6 sm:px-8"
    >
      <div v-for="index in 4" :key="index" class="w-[19rem] flex-none rounded-xl border border-slate-200 bg-slate-50 p-3">
        <div class="skeleton h-8 w-full rounded-lg" />
        <div class="mt-3 space-y-2.5">
          <div v-for="card in 3" :key="card" class="skeleton h-28 w-full rounded-xl" />
        </div>
      </div>
    </div>

    <!-- Load failure with no snapshot. -->
    <div v-else-if="showLoadError" class="mx-auto max-w-xl px-5 py-20 text-center">
      <span class="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl border border-red-200 bg-red-500/[0.07] text-red-600">
        <AppIcon name="alert" :size="24" />
      </span>
      <h2 class="mt-5 text-lg font-semibold text-slate-900">Unable to load this board</h2>
      <p class="mt-2 text-sm leading-relaxed text-slate-600">{{ error }}</p>
      <div class="mt-5 flex justify-center gap-3">
        <RouterLink
          to="/projects"
          class="rounded-xl border border-slate-200 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-100"
        >
          Back to projects
        </RouterLink>
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-xl bg-accent-600 px-4 py-2 text-sm font-semibold text-white hover:bg-accent-700"
          @click="refreshBoard"
        >
          Retry
        </button>
      </div>
    </div>

    <!-- Kanban board. -->
    <div v-else class="mx-auto max-w-[110rem] px-5 py-6 sm:px-8">
      <div class="flex gap-4 overflow-x-auto pb-6">
        <section
          v-for="bucket in boardColumns"
          :key="bucket.column.id"
          class="flex min-w-[16.5rem] flex-1 flex-col overflow-hidden rounded-xl border border-l-4 border-slate-200 bg-white"
          :class="bucket.accent.border"
        >
          <header class="flex items-center gap-2 px-3.5 py-3" :class="bucket.accent.header">
            <span class="h-1.5 w-1.5 rounded-full" :class="bucket.accent.dot" />
            <h2 class="text-xs font-semibold uppercase tracking-[0.08em] text-slate-800">
              {{ bucket.column.name }}
            </h2>
            <span class="rounded-full px-2 py-0.5 text-[10px] font-semibold" :class="bucket.accent.badge">
              {{ bucket.tickets.length }}
            </span>
            <span class="flex-1" />
            <button
              v-if="canWrite"
              type="button"
              class="flex h-6 w-6 items-center justify-center rounded-md text-slate-600 hover:bg-slate-100 hover:text-slate-900"
              :aria-label="`Create ticket in ${bucket.column.name}`"
              :title="`Add ticket to ${bucket.column.name}`"
              @click="openCreate(bucket.column.name)"
            >
              <AppIcon name="plus" :size="14" />
            </button>
          </header>

          <div class="flex min-h-[20rem] flex-1 flex-col gap-2.5 p-2.5">
            <TicketCard
              v-for="ticket in bucket.tickets"
              :key="ticket.id"
              :ticket="ticket"
              :columns="store.columns"
              @open="openTicket"
              @move="handleMove"
              @delete-ticket="requestDeleteById"
            />

            <button
              v-if="bucket.tickets.length === 0"
              type="button"
              class="flex flex-1 flex-col items-center justify-center rounded-lg border border-dashed border-slate-200 px-4 py-10 text-center transition-colors hover:border-slate-300 hover:bg-slate-50"
              :disabled="!canWrite"
              @click="canWrite && openCreate(bucket.column.name)"
            >
              <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-slate-50 text-slate-600">
                <AppIcon name="inbox" :size="16" />
              </span>
              <span class="mt-2 text-[11px] text-slate-600">No tickets</span>
              <span v-if="canWrite" class="mt-1 text-[10px] text-slate-700">Click to add one</span>
            </button>
          </div>
        </section>
      </div>

      <p v-if="boardColumns.length === 0" class="py-16 text-center text-sm text-slate-600">
        This board has no workflow columns yet.
      </p>
    </div>

    <!-- Create ticket modal. -->
    <BaseModal
      :model-value="showCreate"
      title="New ticket"

      max-width="lg"
      :persistent="creating"
      @update:model-value="(value) => { if (!value) closeCreate() }"
    >
      <form class="space-y-5" @submit.prevent="createTicket">
        <label class="block">
          <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
            Title
          </span>
          <input
            v-model="createTitle"
            data-autofocus
            type="text"
            maxlength="255"
            :disabled="creating"
            class="field-input"
            placeholder="What needs to be done?"
            @input="createError = ''"
          />
        </label>

        <label class="block">
          <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
            Description <span class="font-normal normal-case tracking-normal text-slate-600">optional</span>
          </span>
          <textarea
            v-model="createDescription"
            rows="4"
            maxlength="10000"
            :disabled="creating"
            class="field-input resize-none"
            placeholder="Acceptance criteria, context or implementation notes…"
          />
        </label>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
              Column
            </span>
            <select v-model="createState" :disabled="creating" class="field-input">
              <option v-for="column in store.orderedColumns" :key="column.id" :value="column.name">
                {{ column.name }}
              </option>
            </select>
          </label>

          <label class="block">
            <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
              Assignee
            </span>
            <select v-model="createAssignee" :disabled="creating" class="field-input">
              <option value="">Unassigned</option>
              <option v-for="member in memberOptions" :key="member.id" :value="member.id">
                {{ member.label }}
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
              v-for="option in createPriorities"
              :key="option.value"
              type="button"
              :disabled="creating"
              class="flex items-center justify-center gap-1.5 rounded-lg border px-2 py-2 text-[11px] font-medium disabled:opacity-50"
              :class="createPriority === option.value
                ? 'border-accent-500/40 bg-accent-500/10 text-indigo-700'
                : 'border-slate-200 bg-slate-50 text-slate-600 hover:border-slate-300 hover:text-slate-900'"
              @click="createPriority = option.value"
            >
              <span class="h-1.5 w-1.5 rounded-full" :class="option.dot" />
              {{ option.label }}
            </button>
          </div>
        </div>

        <p
          v-if="createError"
          class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700"
          role="alert"
        >
          {{ createError }}
        </p>

        <div class="flex items-center justify-end gap-3 pt-1">
          <button
            type="button"
            class="rounded-lg px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
            :disabled="creating"
            @click="closeCreate"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="inline-flex items-center gap-2 rounded-lg bg-accent-600 px-4 py-2 text-sm font-semibold text-white hover:bg-accent-700 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="creating || !createTitle.trim()"
          >
            <AppIcon v-if="creating" name="loader" :size="15" class="animate-spin" />
            {{ creating ? 'Creating…' : 'Create ticket' }}
          </button>
        </div>
      </form>
    </BaseModal>

    <TicketDrawer
      :open="Boolean(selectedTicketId) && Boolean(selectedTicket)"
      :ticket="selectedTicket"
      :columns="store.columns"
      :members="store.members"
      :saving="saving"
      :can-write="canWrite"
      @close="closeTicket"
      @save="handleSave"
      @move="selectedTicket && handleMove(selectedTicket.id, $event)"
      @delete="selectedTicket && requestDelete(selectedTicket)"
    />

    <ConfirmDialog
      :open="Boolean(pendingDelete)"
      title="Delete ticket"
      :message="`Delete “${pendingDelete?.title ?? ''}”? The event history is retained, but the ticket will be removed from the board.`"
      confirm-label="Delete ticket"
      :busy="deleting"
      @update:open="(value) => { if (!value) pendingDelete = null }"
      @confirm="confirmDelete"
      @cancel="pendingDelete = null"
    />
  </div>
</template>
