<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { pipelineLabel } from '../lib/format'

const props = withDefaults(defineProps<{ status?: string; size?: number }>(), {
  status: '',
  size: 15,
})

const icon = computed(() => {
  switch (props.status) {
    case 'success':
      return { name: 'check-circle', cls: 'text-emerald-700', spin: false }
    case 'failure':
      return { name: 'x', cls: 'text-rose-600', spin: false }
    case 'in_progress':
      return { name: 'loader', cls: 'text-blue-600', spin: true }
    case 'pending':
      return { name: 'clock', cls: 'text-slate-600', spin: false }
    case 'cancelled':
    case 'timed_out':
      return { name: 'minus', cls: 'text-slate-600', spin: false }
    default:
      return { name: 'minus', cls: 'text-slate-600', spin: false }
  }
})

const label = computed(() => pipelineLabel[props.status] ?? 'No runs')
</script>

<template>
  <span
    class="inline-flex items-center justify-center"
    :class="[icon.cls, icon.spin ? 'animate-spin' : '']"
    :title="`CI: ${label}`"
    :aria-label="`CI: ${label}`"
  >
    <AppIcon :name="icon.name" :size="size" :stroke-width="2" />
  </span>
</template>
