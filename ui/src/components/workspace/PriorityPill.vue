<script setup lang="ts">
import { computed } from 'vue'
import { priorityLabels, priorityTone } from '../../lib/workspace'

const props = defineProps<{ priority: string | null | undefined }>()
const classes = computed(() => priorityTone(props.priority))
const text = computed(() => priorityLabels[props.priority ?? ''] ?? (props.priority || 'Medium'))
</script>

<template>
  <span
    class="inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-semibold capitalize ring-1 ring-inset"
    :class="classes"
  >
    {{ text }}
  </span>
</template>
