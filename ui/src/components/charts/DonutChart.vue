<script setup lang="ts">
import { computed } from 'vue'

interface DonutDatum {
  label: string
  value: number
  color: string
}

const props = withDefaults(
  defineProps<{
    data: DonutDatum[]
    centerLabel?: string
    centerValue?: string | number
    size?: number
  }>(),
  {
    centerLabel: 'Total',
    centerValue: '',
    size: 180,
  },
)

const radius = 42
const circumference = 2 * Math.PI * radius
const total = computed(() => props.data.reduce((sum, entry) => sum + Math.max(0, entry.value), 0))
const segments = computed(() => {
  let offset = 0
  return props.data
    .filter((entry) => entry.value > 0)
    .map((entry) => {
      const length = total.value ? (entry.value / total.value) * circumference : 0
      const segment = {
        ...entry,
        dasharray: `${Math.max(0, length - 1.5)} ${circumference}`,
        dashoffset: -offset,
      }
      offset += length
      return segment
    })
})
const displayTotal = computed(() => props.centerValue === '' ? total.value : props.centerValue)
</script>

<template>
  <div class="flex flex-wrap items-center gap-5">
    <svg :width="size" :height="size" viewBox="0 0 100 100" role="img" :aria-label="`${centerLabel}: ${displayTotal}`">
      <circle cx="50" cy="50" :r="radius" fill="none" stroke="#e2e8f0" stroke-width="12" />
      <g transform="rotate(-90 50 50)">
        <circle
          v-for="segment in segments"
          :key="segment.label"
          cx="50"
          cy="50"
          :r="radius"
          fill="none"
          :stroke="segment.color"
          stroke-width="12"
          :stroke-dasharray="segment.dasharray"
          :stroke-dashoffset="segment.dashoffset"
          stroke-linecap="butt"
        >
          <title>{{ segment.label }}: {{ segment.value }}</title>
        </circle>
      </g>
      <text x="50" y="47" text-anchor="middle" class="fill-slate-900" style="font-size: 13px; font-weight: 700">
        {{ displayTotal }}
      </text>
      <text x="50" y="59" text-anchor="middle" class="fill-slate-500" style="font-size: 5.5px; text-transform: uppercase; letter-spacing: 0.08em">
        {{ centerLabel }}
      </text>
    </svg>
    <ul class="min-w-[140px] flex-1 space-y-2">
      <li v-for="entry in data" :key="entry.label" class="flex items-center gap-2 text-xs">
        <span class="h-2.5 w-2.5 flex-none rounded-full" :style="{ backgroundColor: entry.color }" />
        <span class="min-w-0 flex-1 truncate text-slate-600">{{ entry.label }}</span>
        <span class="font-semibold text-slate-900">{{ entry.value }}</span>
      </li>
    </ul>
  </div>
</template>
