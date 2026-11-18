<script setup lang="ts">
import { computed } from 'vue'
import { statusLabel, statusTone } from '../../lib/workspace'

const props = withDefaults(
  defineProps<{
    status: string | null | undefined
    label?: string
    dot?: boolean
    size?: 'sm' | 'md'
  }>(),
  {
    label: '',
    dot: true,
    size: 'sm',
  },
)

const classes = computed(() => statusTone(props.status))
const text = computed(() => props.label || statusLabel(props.status))
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-full font-semibold ring-1 ring-inset"
    :class="[classes, size === 'sm' ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs']"
  >
    <span v-if="dot" class="h-1.5 w-1.5 rounded-full bg-current opacity-70" />
    {{ text }}
  </span>
</template>
