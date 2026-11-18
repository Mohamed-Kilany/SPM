<script setup lang="ts">
import { computed } from 'vue'

interface BarDatum {
  label: string
  value: number
  color: string
}

const props = withDefaults(
  defineProps<{
    data: BarDatum[]
    height?: number
    valueSuffix?: string
  }>(),
  {
    height: 230,
    valueSuffix: '',
  },
)

const width = 560
const padding = { top: 18, right: 12, bottom: 38, left: 28 }
const chartWidth = width - padding.left - padding.right
const chartHeight = computed(() => props.height - padding.top - padding.bottom)
const maxValue = computed(() => Math.max(1, ...props.data.map((entry) => entry.value)))
const barStep = computed(() => (props.data.length ? chartWidth / props.data.length : chartWidth))
const barWidth = computed(() => Math.min(64, Math.max(14, barStep.value * 0.58)))
const bars = computed(() =>
  props.data.map((entry, index) => {
    const valueHeight = (entry.value / maxValue.value) * chartHeight.value
    return {
      ...entry,
      x: padding.left + barStep.value * index + (barStep.value - barWidth.value) / 2,
      y: padding.top + chartHeight.value - valueHeight,
      width: barWidth.value,
      height: Math.max(0, valueHeight),
      labelX: padding.left + barStep.value * index + barStep.value / 2,
    }
  }),
)
const ticks = computed(() => {
  const count = 4
  return Array.from({ length: count + 1 }, (_, index) => {
    const value = (maxValue.value / count) * index
    return {
      value,
      y: padding.top + chartHeight.value - (chartHeight.value / count) * index,
    }
  })
})
</script>

<template>
  <div class="overflow-x-auto" tabindex="0">
    <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" role="img" aria-label="Bar chart">
      <g>
        <line
          v-for="tick in ticks"
          :key="tick.value"
          :x1="padding.left"
          :x2="width - padding.right"
          :y1="tick.y"
          :y2="tick.y"
          stroke="#e2e8f0"
          stroke-width="1"
        />
        <text
          v-for="tick in ticks"
          :key="`label-${tick.value}`"
          :x="padding.left - 6"
          :y="tick.y + 3"
          text-anchor="end"
          class="fill-slate-400"
          style="font-size: 9px"
        >
          {{ Math.round(tick.value) }}
        </text>
      </g>
      <g v-for="bar in bars" :key="bar.label">
        <rect
          :x="bar.x"
          :y="bar.y"
          :width="bar.width"
          :height="bar.height"
          :fill="bar.color"
          rx="4"
        >
          <title>{{ bar.label }}: {{ bar.value }}{{ valueSuffix }}</title>
        </rect>
        <text
          :x="bar.labelX"
          :y="bar.y - 5"
          text-anchor="middle"
          class="fill-slate-700"
          style="font-size: 9px; font-weight: 700"
        >
          {{ bar.value }}{{ valueSuffix }}
        </text>
        <text
          :x="bar.labelX"
          :y="height - 14"
          text-anchor="middle"
          class="fill-slate-500"
          style="font-size: 9px"
        >
          {{ bar.label.length > 16 ? `${bar.label.slice(0, 15)}…` : bar.label }}
          <title>{{ bar.label }}</title>
        </text>
      </g>
    </svg>
  </div>
</template>
