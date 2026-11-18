<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'

withDefaults(
  defineProps<{
    open: boolean
    title?: string
    message?: string
    confirmLabel?: string
    busy?: boolean
    danger?: boolean
  }>(),
  {
    title: 'Are you sure?',
    message: 'This action cannot be undone.',
    confirmLabel: 'Confirm',
    busy: false,
    danger: true,
  },
)

const emit = defineEmits<{
  (event: 'confirm'): void
  (event: 'cancel'): void
  (event: 'update:open', value: boolean): void
}>()

function close(): void {
  emit('update:open', false)
  emit('cancel')
}
</script>

<template>
  <BaseModal
    :model-value="open"
    :title="title"
    :persistent="busy"
    max-width="sm"
    @update:model-value="(value) => { if (!value) close() }"
  >
    <div class="flex items-start gap-4">
      <span
        class="flex h-10 w-10 flex-none items-center justify-center rounded-xl"
        :class="danger ? 'bg-red-50 text-red-600' : 'bg-indigo-50 text-indigo-600'"
      >
        <AppIcon :name="danger ? 'alert' : 'sparkles'" :size="19" />
      </span>
      <p class="pt-1 text-sm leading-relaxed text-slate-600">{{ message }}</p>
    </div>

    <template #footer>
      <button
        type="button"
        class="rounded-lg px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-100 hover:text-slate-900"
        :disabled="busy"
        @click="close"
      >
        Cancel
      </button>
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50"
        :class="danger ? 'bg-red-600 hover:bg-red-500' : 'bg-accent-600 hover:bg-accent-700'"
        :disabled="busy"
        @click="emit('confirm')"
      >
        <AppIcon v-if="busy" name="loader" :size="15" class="animate-spin" />
        {{ busy ? 'Working…' : confirmLabel }}
      </button>
    </template>
  </BaseModal>
</template>
