<script setup lang="ts">
import AppIcon from '../AppIcon.vue'

withDefaults(
  defineProps<{
    icon?: string
    title: string
    description?: string
    actionLabel?: string
    compact?: boolean
  }>(),
  {
    icon: 'sparkles',
    description: '',
    actionLabel: '',
    compact: false,
  },
)

defineEmits<{ (event: 'action'): void }>()
</script>

<template>
  <div
    class="flex flex-col items-center justify-center rounded-xl border border-dashed border-slate-300 bg-slate-50/60 px-6 text-center"
    :class="compact ? 'py-8' : 'py-14'"
  >
    <span class="flex h-11 w-11 items-center justify-center rounded-xl bg-white text-slate-600 shadow-sm ring-1 ring-slate-200">
      <AppIcon :name="icon" :size="20" />
    </span>
    <p class="mt-3 text-sm font-semibold text-slate-800">{{ title }}</p>
    <p v-if="description" class="mt-1 max-w-sm text-xs leading-relaxed text-slate-600">{{ description }}</p>
    <button v-if="actionLabel" type="button" class="btn-secondary mt-4" @click="$emit('action')">
      <AppIcon name="plus" :size="15" />
      {{ actionLabel }}
    </button>
  </div>
</template>
