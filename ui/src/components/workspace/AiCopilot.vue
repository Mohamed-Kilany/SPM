<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from '../AppIcon.vue'
import EmptyState from './EmptyState.vue'
import { api } from '../../api/client'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import type { AiAskResponse, AiInsightResponse, AiSearchResponse } from '../../types/ai'

const store = useWorkspaceStore()
const projectId = computed(() => store.projectId)

type Mode = 'ask' | 'search' | 'insights'
const mode = ref<Mode>('ask')
const question = ref('What are the biggest risks for the next release?')
const searchQuery = ref('')
const askResult = ref<AiAskResponse | null>(null)
const searchResult = ref<AiSearchResponse | null>(null)
const insights = ref<AiInsightResponse | null>(null)
const loading = ref(false)
const error = ref('')
let lastAction: (() => Promise<void>) | null = null

const modes: Array<{ key: Mode; label: string; icon: string }> = [
  { key: 'ask', label: 'Ask', icon: 'sparkles' },
  { key: 'search', label: 'Semantic search', icon: 'search' },
  { key: 'insights', label: 'Recommendations', icon: 'chart' },
]

async function run(action: () => Promise<void>): Promise<void> {
  lastAction = action
  loading.value = true
  error.value = ''
  try {
    await action()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'The AI service is unavailable.'
  } finally {
    loading.value = false
  }
}

function ask(): void {
  const text = question.value.trim()
  if (!text) return
  void run(async () => {
    askResult.value = await api.aiAsk(projectId.value, text)
  })
}

function search(): void {
  const text = searchQuery.value.trim()
  if (!text) return
  void run(async () => {
    searchResult.value = await api.aiSearch(projectId.value, text)
  })
}

function loadInsights(): void {
  void run(async () => {
    insights.value = await api.aiInsights(projectId.value)
  })
}

function selectMode(next: Mode): void {
  mode.value = next
  error.value = ''
  if (next === 'insights' && !insights.value) loadInsights()
}

function retry(): void {
  if (lastAction) void run(lastAction)
}

function severityClass(severity: string): string {
  if (severity === 'high') return 'bg-red-50 text-red-700 ring-red-200'
  if (severity === 'medium') return 'bg-amber-50 text-amber-800 ring-amber-200'
  return 'bg-emerald-50 text-emerald-700 ring-emerald-200'
}

function isInternal(url: string): boolean {
  return url.startsWith('/')
}
</script>

<template>
  <section class="card overflow-hidden">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 px-5 py-4">
      <div class="flex items-center gap-3">
        <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-600 to-violet-600 text-white shadow-sm">
          <AppIcon name="sparkles" :size="17" />
        </span>
        <div>
          <h2 class="text-sm font-semibold text-slate-900">Delivery copilot</h2>

        </div>
      </div>
      <span class="badge bg-slate-100 text-slate-600 ring-slate-200">CPU · offline · RAG</span>
    </div>

    <div class="flex flex-wrap gap-1 border-b border-slate-100 px-4 py-2">
      <button
        v-for="item in modes"
        :key="item.key"
        type="button"
        class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-semibold"
        :class="mode === item.key ? 'bg-indigo-50 text-indigo-700' : 'text-slate-600 hover:bg-slate-50'"
        @click="selectMode(item.key)"
      >
        <AppIcon :name="item.icon" :size="13" /> {{ item.label }}
      </button>
    </div>

    <div class="p-4 sm:p-5">
      <form v-if="mode === 'ask'" class="flex flex-col gap-2 sm:flex-row" @submit.prevent="ask">
        <input v-model="question" class="field-input flex-1" placeholder="Ask about risks, gaps, ownership or delivery readiness…" aria-label="Ask the delivery copilot" />
        <button type="submit" class="btn-primary" :disabled="loading || !question.trim()">
          <AppIcon name="sparkles" :size="14" /> {{ loading ? 'Thinking…' : 'Ask' }}
        </button>
      </form>

      <form v-else-if="mode === 'search'" class="flex flex-col gap-2 sm:flex-row" @submit.prevent="search">
        <input v-model="searchQuery" class="field-input flex-1" placeholder="Describe what you need, e.g. checkout retry evidence" aria-label="Semantic search" />
        <button type="submit" class="btn-primary" :disabled="loading || !searchQuery.trim()">
          <AppIcon name="search" :size="14" /> {{ loading ? 'Searching…' : 'Search' }}
        </button>
      </form>

      <div v-else class="flex items-center justify-between gap-3">

        <button type="button" class="btn-secondary py-1.5 text-xs" :disabled="loading" @click="loadInsights">
          <AppIcon name="refresh" :size="13" /> Refresh
        </button>
      </div>

      <div v-if="error" class="mt-3 flex items-start justify-between gap-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2.5 text-xs text-red-700" role="alert">
        <span>{{ error }}</span>
        <button type="button" class="font-semibold underline" @click="retry">Retry</button>
      </div>

      <div v-if="loading" class="mt-4 flex items-center gap-2 text-xs text-slate-500">
        <span class="h-3 w-3 animate-spin rounded-full border-2 border-indigo-200 border-t-indigo-600" />
        Retrieving authorized context and ranking evidence…
      </div>

      <div v-if="mode === 'ask' && askResult && !loading" class="mt-4">
        <div class="rounded-xl border border-indigo-100 bg-indigo-50/40 p-3.5">
          <p class="whitespace-pre-line text-sm leading-relaxed text-slate-800">{{ askResult.answer }}</p>
          <div class="mt-2 flex flex-wrap items-center gap-2 text-[10px] text-slate-500">
            <span>Confidence {{ Math.round(askResult.confidence * 100) }}%</span>
            <span>·</span>
            <span>{{ askResult.engine }}</span>
            <span>·</span>
            <span>{{ askResult.elapsed_ms }} ms</span>
          </div>
        </div>
        <div v-if="askResult.citations.length" class="mt-2 flex flex-wrap gap-1.5">
          <template v-for="citation in askResult.citations" :key="citation.id">
            <RouterLink v-if="isInternal(citation.url)" :to="citation.url" class="badge bg-white font-medium text-indigo-700 ring-indigo-200 hover:bg-indigo-50">
              {{ citation.type }} · {{ citation.title }}
            </RouterLink>
            <a v-else :href="citation.url" target="_blank" rel="noopener noreferrer" class="badge bg-white font-medium text-indigo-700 ring-indigo-200 hover:bg-indigo-50">
              {{ citation.type }} · {{ citation.title }}
            </a>
          </template>
        </div>
      </div>

      <div v-if="mode === 'search' && searchResult && !loading" class="mt-4">
        <div v-if="searchResult.hits.length" class="space-y-2">
          <RouterLink
            v-for="hit in searchResult.hits"
            :key="hit.id"
            :to="hit.url"
            class="block rounded-lg border border-slate-200 bg-white px-3 py-2.5 transition-colors hover:border-indigo-200 hover:bg-indigo-50/30"
          >
            <div class="flex items-center justify-between gap-2">
              <p class="truncate text-xs font-semibold text-slate-900">{{ hit.title }}</p>
              <span class="flex-none rounded bg-indigo-50 px-1.5 py-0.5 text-[10px] font-semibold text-indigo-700">{{ Math.round(hit.score * 100) }}%</span>
            </div>
            <p class="mt-0.5 line-clamp-2 text-[11px] text-slate-500">{{ hit.text }}</p>
            <p class="mt-1 text-[10px] uppercase tracking-wide text-slate-400">{{ hit.type }} · {{ hit.status || 'no status' }}</p>
          </RouterLink>
        </div>
        <EmptyState v-else icon="search" title="No semantic matches" />
      </div>

      <div v-if="mode === 'insights' && insights && !loading" class="mt-4 space-y-2">
        <article v-for="insight in insights.insights" :key="insight.title" class="rounded-lg border border-slate-200 px-3 py-2.5">
          <div class="flex flex-wrap items-center gap-2">
            <span class="badge ring-1" :class="severityClass(insight.severity)">{{ insight.severity }}</span>
            <span class="text-[10px] font-semibold uppercase tracking-wide text-slate-400">{{ insight.category }}</span>
            <p class="min-w-0 flex-1 text-xs font-semibold text-slate-900">{{ insight.title }}</p>
          </div>
          <p class="mt-1 text-[11px] text-slate-600">{{ insight.detail }}</p>
          <p v-if="insight.action" class="mt-1 text-[11px] font-medium text-indigo-700">{{ insight.action }}</p>
          <p v-if="insight.evidence.length" class="mt-1 truncate text-[10px] text-slate-400">{{ insight.evidence.join(' · ') }}</p>
        </article>
      </div>
    </div>
  </section>
</template>
