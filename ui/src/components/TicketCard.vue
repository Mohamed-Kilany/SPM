<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import PipelineStatusIcon from './PipelineStatusIcon.vue'
import { personInitials, priorityLabel, timeAgo } from '../lib/format'
import type { BoardColumn, Ticket } from '../types/project'

const props = defineProps<{
  ticket: Ticket
  columns: BoardColumn[]
}>()

const emit = defineEmits<{
  (event: 'open', ticket: Ticket): void
  (event: 'move', ticketId: string, state: string): void
  (event: 'deleteTicket', ticketId: string): void
}>()

const showMoveMenu = ref(false)

const otherColumns = computed(() => props.columns.filter((column) => column.name !== props.ticket.state))

const priority = computed(
  () =>
    ({
      critical: {
        stripe: 'border-l-red-500',
        badge: 'bg-red-50 text-red-700 ring-red-200',
      },
      high: {
        stripe: 'border-l-orange-500',
        badge: 'bg-orange-50 text-orange-700 ring-orange-200',
      },
      medium: {
        stripe: 'border-l-amber-500',
        badge: 'bg-amber-50 text-amber-700 ring-amber-200',
      },
      low: {
        stripe: 'border-l-emerald-500',
        badge: 'bg-emerald-50 text-emerald-700 ring-emerald-200',
      },
    })[props.ticket.priority] ?? {
      stripe: 'border-l-slate-400',
      badge: 'bg-slate-100 text-slate-600 ring-slate-200',
    },
)

const label = computed(() => priorityLabel[props.ticket.priority] ?? 'Normal')

function openCard(): void {
  emit('open', props.ticket)
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.target !== event.currentTarget) return
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    openCard()
  }
}

function moveTo(state: string): void {
  showMoveMenu.value = false
  emit('move', props.ticket.id, state)
}
</script>

<template>
  <article
    class="group relative cursor-pointer rounded-xl border border-l-4 border-slate-200 bg-white p-3.5 shadow-sm outline-none transition-all duration-150 hover:-translate-y-0.5 hover:border-indigo-300 hover:shadow-md focus-visible:ring-2 focus-visible:ring-indigo-500/40"
    :class="priority.stripe"
    role="button"
    tabindex="0"
    :aria-label="`Open ticket ${ticket.title}`"
    @click="openCard"
    @keydown="handleKeydown"
  >
    <div class="flex items-start gap-2">
      <h3 class="min-w-0 flex-1 pr-10 text-[13px] font-medium leading-snug text-slate-900">
        {{ ticket.title }}
      </h3>
    </div>

    <p v-if="ticket.description" class="mt-1.5 line-clamp-2 text-[11.5px] leading-relaxed text-slate-600">
      {{ ticket.description }}
    </p>

    <div class="mt-3 flex flex-wrap items-center gap-2">
      <span
        class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider ring-1 ring-inset"
        :class="priority.badge"
      >
        {{ label }}
      </span>

      <span
        v-if="ticket.pipeline_status"
        class="inline-flex items-center gap-1 rounded-md bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600"
      >
        <PipelineStatusIcon :status="ticket.pipeline_status" :size="12" />
        CI
      </span>

      <span class="ml-auto inline-flex items-center gap-1 text-[10px] text-slate-600">
        <AppIcon name="clock" :size="11" />
        {{ timeAgo(ticket.updated_at) }}
      </span>
    </div>

    <div class="mt-2.5 flex items-center gap-2 border-t border-slate-100 pt-2.5">
      <div
        v-if="ticket.assignee_id"
        class="flex h-6 w-6 flex-none items-center justify-center rounded-full bg-gradient-to-br from-indigo-500 to-indigo-700 text-[10px] font-bold text-white ring-1 ring-white"
        :title="`Assigned: ${ticket.assignee_id}`"
      >
        {{ personInitials(ticket.assignee_id) }}
      </div>
      <div
        v-else
        class="flex h-6 w-6 flex-none items-center justify-center rounded-full border border-dashed border-slate-300 text-slate-600"
        title="Unassigned"
      >
        <AppIcon name="user" :size="12" />
      </div>

      <span
        v-if="ticket.branch_name"
        class="flex min-w-0 items-center gap-1 truncate font-mono text-[10px] text-slate-600"
        :title="ticket.branch_name"
      >
        <AppIcon name="branch" :size="11" class="flex-none text-slate-600" />
        <span class="truncate">{{ ticket.branch_name }}</span>
      </span>
      <span v-else class="text-[10px] text-slate-600">No branch yet</span>
    </div>

    <!-- Hover/focus-revealed actions (Requirement 9.4). -->
    <div
      class="absolute right-2 top-2 flex gap-1 opacity-0 transition-opacity duration-150 group-hover:opacity-100 group-focus-within:opacity-100"
    >
      <button
        type="button"
        class="flex h-7 w-7 items-center justify-center rounded-lg border border-slate-200 bg-white text-slate-600 shadow-sm hover:border-indigo-300 hover:bg-indigo-50 hover:text-indigo-700"
        :aria-expanded="showMoveMenu"
        title="Move ticket"
        aria-haspopup="listbox"
        @click.stop="showMoveMenu = !showMoveMenu"
      >
        <AppIcon name="arrowRight" :size="13" />
      </button>
      <button
        type="button"
        class="flex h-7 w-7 items-center justify-center rounded-lg border border-slate-200 bg-white text-slate-600 shadow-sm hover:border-red-300 hover:bg-red-50 hover:text-red-700"
        title="Delete ticket"
        aria-label="Delete ticket"
        @click.stop="emit('deleteTicket', ticket.id)"
      >
        <AppIcon name="trash" :size="13" />
      </button>
    </div>

    <!-- Floating move panel (Requirement 9.5). -->
    <div
      v-if="showMoveMenu"
      class="absolute right-2 top-10 z-30 w-44 overflow-hidden rounded-xl border border-slate-200 bg-white p-1 shadow-xl"
      role="listbox"
      @click.stop
    >
      <p class="px-2.5 py-1.5 text-[9px] font-semibold uppercase tracking-[0.16em] text-slate-600">
        Move to
      </p>
      <button
        v-for="column in otherColumns"
        :key="column.id"
        type="button"
        class="flex w-full items-center justify-between rounded-lg px-2.5 py-2 text-left text-xs text-slate-700 hover:bg-indigo-50 hover:text-indigo-700"
        role="option"
        @click.stop="moveTo(column.name)"
      >
        {{ column.name }}
        <AppIcon name="arrowRight" :size="12" class="opacity-0" />
      </button>
      <p v-if="otherColumns.length === 0" class="px-2.5 py-2 text-xs text-slate-600">
        No other columns
      </p>
    </div>

    <div
      v-if="showMoveMenu"
      class="fixed inset-0 z-20"
      aria-hidden="true"
      @click.stop="showMoveMenu = false"
    />
  </article>
</template>
