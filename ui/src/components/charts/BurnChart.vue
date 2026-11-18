<script setup lang="ts">
import { computed } from 'vue'

interface BurnPoint {
  label: string
  scope: number
  completed: number
  ideal: number
}

const props = withDefaults(
  defineProps<{
    points: BurnPoint[]
    height?: number
  }>(),
  {
    height: 260,
  },
)

const width = 640
const padding = { top: 18, right: 18, bottom: 38, left: 38 }
const chartWidth = width - padding.left - padding.right
const chartHeight = computed(() => props.height - padding.top - padding.bottom)
const maxValue = computed(() =>
  Math.max(1, ...props.points.flatMap((point) => [point.scope, point.completed, point.ideal])),
)
const xFor = (index: number) =>
  props.points.length <= 1
    ? padding.left + chartWidth / 2
    : padding.left + (chartWidth / (props.points.length - 1)) * index
const yFor = (value: number) => padding.top + chartHeight.value - (value / maxValue.value) * chartHeight.value

function linePath(values: number[]): string {
  return values.map((value, index) => `${index === 0 ? 'M' : 'L'} ${xFor(index)} ${yFor(value)}`).join(' ')
}

const scopePath = computed(() => linePath(props.points.map((point) => point.scope)))
const completedPath = computed(() => linePath(props.points.map((point) => point.completed)))
const idealPath = computed(() => linePath(props.points.map((point) => point.ideal)))
const ticks = computed(() => {
  const count = 4
  return Array.from({ length: count + 1 }, (_, index) => ({
    value: (maxValue.value / count) * index,
    y: padding.top + chartHeight.value - (chartHeight.value / count) * index,
  }))
})
const labelStep = computed(() => Math.max(1, Math.ceil(props.points.length / 7)))
</script>

<template>
  <div class="overflow-x-auto" tabindex="0">
    <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" role="img" aria-label="Burn-up and burn-down chart">
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
          :key="`tick-${tick.value}`"
          :x="padding.left - 7"
          :y="tick.y + 3"
          text-anchor="end"
          class="fill-slate-400"
          style="font-size: 9px"
        >
          {{ Math.round(tick.value) }}
        </text>
      </g>

      <path :d="scopePath" fill="none" stroke="#94a3b8" stroke-width="2" stroke-dasharray="5 4" />
      <path :d="idealPath" fill="none" stroke="#cbd5e1" stroke-width="1.5" stroke-dasharray="3 5" />
      <path :d="completedPath" fill="none" stroke="#4f46e5" stroke-width="2.5" />

      <g v-for="(point, index) in points" :key="point.label">
        <circle :cx="xFor(index)" :cy="yFor(point.completed)" r="3" fill="#4f46e5">
          <title>{{ point.label }}: {{ point.completed }} completed / {{ point.scope }} scope</title>
        </circle>
        <text
          v-if="index % labelStep === 0 || index === points.length - 1"
          :x="xFor(index)"
          :y="height - 14"
          text-anchor="middle"
          class="fill-slate-500"
          style="font-size: 9px"
        >
          {{ point.label }}
        </text>
      </g>
    </svg>
    <div class="mt-1 flex flex-wrap gap-4 px-1 text-[11px] text-slate-600">
      <span class="inline-flex items-center gap-1.5"><span class="h-0.5 w-5 bg-indigo-600" /> Completed</span>
      <span class="inline-flex items-center gap-1.5"><span class="h-0.5 w-5 border-t-2 border-dashed border-slate-400" /> Scope</span>
      <span class="inline-flex items-center gap-1.5"><span class="h-0.5 w-5 border-t border-dashed border-slate-300" /> Ideal burn-down</span>
    </div>
  </div>
</template>
