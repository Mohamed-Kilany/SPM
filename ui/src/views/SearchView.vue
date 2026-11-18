<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/workspace/EmptyState.vue'
import StatusPill from '../components/workspace/StatusPill.vue'
import { api } from '../api/client'
import { timeAgo } from '../lib/format'
import type { SearchResponse } from '../types/search'

const route = useRoute()
const router = useRouter()

const query = ref(typeof route.query.q === 'string' ? route.query.q : '')
const result = ref<SearchResponse | null>(null)
const loading = ref(false)
const error = ref('')
const activeGroup = ref<'all' | 'work_items' | 'features' | 'test_cases' | 'projects'>('all')
const savedFilters = ref<Array<{ name: string; query: string }>>([])
const savedName = ref('')
let searchTimer: ReturnType<typeof setTimeout> | null = null

const examples = [
  'status:blocked OR priority:critical',
  'type:bug assignee:me -status:done',
  'feature:"Checkout redesign"',
  'due <= 2026-12-31 status:open',
  'text:"SSE reconnect"',
]

const systemFilters = [
  { label: 'My open work', query: 'assignee:me -status:done -status:closed -status:verified' },
  { label: 'Critical bugs', query: 'type:bug priority:critical -status:closed' },
  { label: 'Blocked', query: 'status:blocked' },
  { label: 'Due this week', query: 'due <= today status:open' },
  { label: 'Recently updated', query: 'updated >= -7d' },
]

const counts = computed(() => ({
  all: result.value?.total ?? 0,
  work_items: result.value?.work_items.length ?? 0,
  features: result.value?.features.length ?? 0,
  test_cases: result.value?.test_cases.length ?? 0,
  projects: result.value?.projects.length ?? 0,
}))

const hasResults = computed(() => (result.value?.total ?? 0) > 0)

function loadSaved(): void {
  try {
    const raw = localStorage.getItem('spm.savedSearches')
    savedFilters.value = raw ? (JSON.parse(raw) as Array<{ name: string; query: string }>) : []
  } catch {
    savedFilters.value = []
  }
}

function persistSaved(): void {
  localStorage.setItem('spm.savedSearches', JSON.stringify(savedFilters.value))
}

function saveFilter(): void {
  const name = savedName.value.trim() || query.value.trim()
  if (!name || !query.value.trim()) return
  savedFilters.value = [
    { name, query: query.value.trim() },
    ...savedFilters.value.filter((entry) => entry.name !== name),
  ].slice(0, 12)
  savedName.value = ''
  persistSaved()
}

function removeFilter(name: string): void {
  savedFilters.value = savedFilters.value.filter((entry) => entry.name !== name)
  persistSaved()
}

function applyQuery(value: string): void {
  query.value = value
  void runSearch()
}

async function runSearch(): Promise<void> {
  const trimmed = query.value.trim()
  loading.value = true
  error.value = ''
  try {
    result.value = await api.search(trimmed)
    void router.replace({ query: trimmed ? { q: trimmed } : {} })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Search failed.'
  } finally {
    loading.value = false
  }
}

function scheduleSearch(): void {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => void runSearch(), 350)
}

function workItemLink(projectId: string, itemId: string) {
  return { name: 'workspace', params: { id: projectId }, query: { tab: 'board', item: itemId } }
}

function featureLink(projectId: string, featureId: string) {
  return { name: 'workspace', params: { id: projectId }, query: { tab: 'planning', feature: featureId } }
}

function testCaseLink(projectId: string, caseId: string) {
  return { name: 'workspace', params: { id: projectId }, query: { tab: 'tests', case: caseId } }
}

function projectLink(projectId: string) {
  return { name: 'workspace', params: { id: projectId } }
}

function snippet(value: string): string {
  const text = value.replace(/\s+/g, ' ').trim()
  return text.length > 160 ? `${text.slice(0, 157)}…` : text
}

watch(query, scheduleSearch)
onMounted(() => {
  loadSaved()
  if (query.value.trim()) void runSearch()
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-8">
    <header class="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="text-[11px] font-semibold uppercase tracking-[0.18em] text-indigo-600">Discovery</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-slate-900">Search &amp; filters</h1>
      </div>
      <RouterLink to="/projects" class="btn-secondary">
        <AppIcon name="projects" :size="15" /> Projects
      </RouterLink>
    </header>

    <section class="card p-4 sm:p-5">
      <form class="flex flex-col gap-3 sm:flex-row" @submit.prevent="runSearch">
        <div class="relative flex-1">
          <AppIcon name="search" :size="17" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input
            v-model="query"
            class="field-input pl-10"
            placeholder='Try status:blocked OR priority:critical, type:bug assignee:me, feature:"Checkout"'
            aria-label="Search query"
            autofocus
          />
        </div>
        <button type="submit" class="btn-primary sm:w-auto" :disabled="loading">
          <AppIcon name="search" :size="15" /> {{ loading ? 'Searching…' : 'Search' }}
        </button>
      </form>

      <div class="mt-3 flex flex-wrap items-center gap-2">
        <span class="text-[11px] font-semibold uppercase tracking-wide text-slate-500">System filters</span>
        <button
          v-for="filter in systemFilters"
          :key="filter.label"
          type="button"
          class="rounded-full border border-slate-200 bg-white px-2.5 py-1 text-[11px] font-medium text-slate-600 hover:border-indigo-200 hover:bg-indigo-50 hover:text-indigo-700"
          @click="applyQuery(filter.query)"
        >
          {{ filter.label }}
        </button>
      </div>

      <div v-if="savedFilters.length" class="mt-3 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-3">
        <span class="text-[11px] font-semibold uppercase tracking-wide text-slate-500">Saved</span>
        <span
          v-for="filter in savedFilters"
          :key="filter.name"
          class="inline-flex items-center gap-1 rounded-full border border-indigo-100 bg-indigo-50 py-1 pl-2.5 pr-1 text-[11px] font-medium text-indigo-700"
        >
          <button type="button" class="max-w-[180px] truncate" :title="filter.query" @click="applyQuery(filter.query)">{{ filter.name }}</button>
          <button type="button" class="rounded-full p-0.5 hover:bg-indigo-100" aria-label="Remove saved filter" @click="removeFilter(filter.name)">
            <AppIcon name="close" :size="10" />
          </button>
        </span>
      </div>

      <details class="mt-3 text-xs text-slate-600">
        <summary class="cursor-pointer font-medium text-slate-700">Query syntax</summary>
        <div class="mt-2 grid gap-2 sm:grid-cols-2">
          <p><span class="font-mono text-indigo-700">field:value</span> — exact filter, e.g. <span class="font-mono">status:done</span>.</p>
          <p><span class="font-mono text-indigo-700">field~text</span> — contains match, e.g. <span class="font-mono">feature~checkout</span>.</p>
          <p><span class="font-mono text-indigo-700">-field:value</span> or <span class="font-mono">NOT</span> — negate a term.</p>
          <p><span class="font-mono text-indigo-700">OR</span> — combine alternatives; spaces are AND.</p>
          <p>Dates: <span class="font-mono">created &gt;= -7d</span>, <span class="font-mono">due &lt;= 2026-12-31</span>, <span class="font-mono">updated &gt; today</span>.</p>
          <p>Fields: project, type, status, priority, assignee, reporter, feature, sprint, release, key, title, text, created, updated, due.</p>
        </div>
      </details>
    </section>

    <div v-if="error" class="mt-4 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700" role="alert">
      <div class="flex items-start justify-between gap-3">
        <span>{{ error }}</span>
        <button type="button" class="btn-secondary py-1.5" @click="runSearch">Retry</button>
      </div>
    </div>

    <div v-if="!result && !loading" class="mt-6 card p-5">
      <h2 class="text-sm font-semibold text-slate-900">Start with a system filter or an example</h2>
      <div class="mt-3 flex flex-wrap gap-2">
        <button
          v-for="example in examples"
          :key="example"
          type="button"
          class="rounded-lg border border-slate-200 bg-slate-50 px-2.5 py-1.5 font-mono text-[11px] text-slate-700 hover:border-indigo-200 hover:bg-indigo-50 hover:text-indigo-700"
          @click="applyQuery(example)"
        >
          {{ example }}
        </button>
      </div>
    </div>

    <div v-if="result" class="mt-5">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap gap-1 rounded-xl border border-slate-200 bg-white p-1">
          <button
            v-for="group in (['all', 'work_items', 'features', 'test_cases', 'projects'] as const)"
            :key="group"
            type="button"
            class="rounded-lg px-3 py-1.5 text-xs font-semibold capitalize"
            :class="activeGroup === group ? 'bg-indigo-600 text-white' : 'text-slate-600 hover:bg-slate-50'"
            @click="activeGroup = group"
          >
            {{ group.replace('_', ' ') }}
            <span class="ml-1 opacity-70">{{ counts[group] }}</span>
          </button>
        </div>
        <div class="flex items-center gap-2">
          <input v-model="savedName" class="field-input w-40 py-1.5 text-xs" placeholder="Save this filter" aria-label="Saved filter name" />
          <button type="button" class="btn-secondary py-1.5 text-xs" :disabled="!query.trim()" @click="saveFilter">Save</button>
        </div>
      </div>

      <p class="mb-3 text-xs text-slate-500">
        {{ result.total }} result{{ result.total === 1 ? '' : 's' }} for
        <span class="font-mono text-slate-700">{{ result.query || 'all accessible work' }}</span>
      </p>

      <div v-if="!hasResults" class="card">
        <EmptyState icon="search" title="No matches" />
      </div>

      <div v-else class="space-y-5">
        <section v-if="(activeGroup === 'all' || activeGroup === 'work_items') && result.work_items.length" class="card overflow-hidden">
          <div class="border-b border-slate-100 px-4 py-3">
            <h2 class="text-sm font-semibold text-slate-900">Work items <span class="text-slate-400">{{ result.work_items.length }}</span></h2>
          </div>
          <ul class="divide-y divide-slate-100">
            <li v-for="item in result.work_items" :key="item.id" class="px-4 py-3 hover:bg-slate-50">
              <div class="flex flex-wrap items-start justify-between gap-2">
                <RouterLink :to="workItemLink(item.project_id, item.id)" class="group min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-mono text-[11px] font-semibold text-indigo-600">{{ item.key }}</span>
                    <span class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-slate-600">{{ item.type }}</span>
                    <StatusPill :status="item.status" />
                  </div>
                  <p class="mt-1 truncate text-sm font-semibold text-slate-900 group-hover:text-indigo-700">{{ item.title }}</p>
                  <p v-if="item.description" class="mt-0.5 line-clamp-1 text-xs text-slate-500">{{ snippet(item.description) }}</p>
                </RouterLink>
                <div class="flex flex-none items-center gap-2 text-[11px] text-slate-500">
                  <span v-if="item.assignee">{{ item.assignee }}</span>
                  <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10px]">{{ item.project_key }}</span>
                  <span>{{ timeAgo(item.updated_at) }}</span>
                </div>
              </div>
              <p v-if="item.feature" class="mt-1.5 text-[11px] text-slate-500">Feature: <span class="text-slate-700">{{ item.feature }}</span></p>
            </li>
          </ul>
        </section>

        <section v-if="(activeGroup === 'all' || activeGroup === 'features') && result.features.length" class="card overflow-hidden">
          <div class="border-b border-slate-100 px-4 py-3">
            <h2 class="text-sm font-semibold text-slate-900">Features <span class="text-slate-400">{{ result.features.length }}</span></h2>
          </div>
          <ul class="divide-y divide-slate-100">
            <li v-for="feature in result.features" :key="feature.id" class="flex items-start justify-between gap-3 px-4 py-3 hover:bg-slate-50">
              <RouterLink :to="featureLink(feature.project_id, feature.id)" class="min-w-0 flex-1">
                <p class="truncate text-sm font-semibold text-slate-900 hover:text-indigo-700">{{ feature.title }}</p>
                <p v-if="feature.description" class="mt-0.5 line-clamp-1 text-xs text-slate-500">{{ snippet(feature.description) }}</p>
              </RouterLink>
              <div class="flex flex-none items-center gap-2 text-[11px] text-slate-500">
                <StatusPill :status="feature.status" />
                <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10px]">{{ feature.project_key }}</span>
              </div>
            </li>
          </ul>
        </section>

        <section v-if="(activeGroup === 'all' || activeGroup === 'test_cases') && result.test_cases.length" class="card overflow-hidden">
          <div class="border-b border-slate-100 px-4 py-3">
            <h2 class="text-sm font-semibold text-slate-900">Test cases <span class="text-slate-400">{{ result.test_cases.length }}</span></h2>
          </div>
          <ul class="divide-y divide-slate-100">
            <li v-for="testCase in result.test_cases" :key="testCase.id" class="flex items-start justify-between gap-3 px-4 py-3 hover:bg-slate-50">
              <RouterLink :to="testCaseLink(testCase.project_id, testCase.id)" class="min-w-0 flex-1">
                <p class="truncate text-sm font-semibold text-slate-900 hover:text-indigo-700">{{ testCase.title }}</p>
                <p class="mt-0.5 text-xs text-slate-500">
                  <span class="uppercase">{{ testCase.type }}</span>
                  <span v-if="testCase.feature"> · {{ testCase.feature }}</span>
                </p>
              </RouterLink>
              <div class="flex flex-none items-center gap-2 text-[11px] text-slate-500">
                <StatusPill :status="testCase.status" />
                <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10px]">{{ testCase.project_key }}</span>
              </div>
            </li>
          </ul>
        </section>

        <section v-if="(activeGroup === 'all' || activeGroup === 'projects') && result.projects.length" class="card overflow-hidden">
          <div class="border-b border-slate-100 px-4 py-3">
            <h2 class="text-sm font-semibold text-slate-900">Projects <span class="text-slate-400">{{ result.projects.length }}</span></h2>
          </div>
          <ul class="divide-y divide-slate-100">
            <li v-for="project in result.projects" :key="project.id" class="flex items-center justify-between gap-3 px-4 py-3 hover:bg-slate-50">
              <RouterLink :to="projectLink(project.id)" class="flex min-w-0 flex-1 items-center gap-3">
                <span class="flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-indigo-50 font-mono text-[11px] font-bold text-indigo-700">{{ project.key_prefix }}</span>
                <span class="truncate text-sm font-semibold text-slate-900 hover:text-indigo-700">{{ project.name }}</span>
              </RouterLink>
              <StatusPill :status="project.status" />
            </li>
          </ul>
        </section>
      </div>
    </div>
  </div>
</template>
