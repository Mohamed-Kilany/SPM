<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from '../AppIcon.vue'
import EmptyState from './EmptyState.vue'
import PriorityPill from './PriorityPill.vue'
import StatusPill from './StatusPill.vue'
import { api } from '../../api/client'
import { useAuthStore } from '../../stores/authStore'
import { useToastStore } from '../../stores/toastStore'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { timeAgo } from '../../lib/format'
import { insertMention, mentionParts, mentionQueryAt } from '../../lib/mentions'
import { workItemTypeLabels } from '../../lib/workspace'
import type {
  Attachment,
  Comment,
  LinkedArtifact,
  WorkItem,
  WorkItemDetail,
} from '../../types/workspace'

type TabKey = 'details' | 'comments' | 'links' | 'attachments' | 'activity' | 'traceability'

const props = defineProps<{ modelValue: boolean; itemId: string | null }>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: boolean): void
  (event: 'edit', item: WorkItem): void
  (event: 'open-item', itemId: string): void
  (event: 'changed'): void
}>()

const store = useWorkspaceStore()
const auth = useAuthStore()
const toast = useToastStore()

const detail = ref<WorkItemDetail | null>(null)
const loading = ref(false)
const error = ref('')
const tab = ref<TabKey>('details')
const panel = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
const tabList = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null

const commentBody = ref('')
const commentInput = ref<HTMLTextAreaElement | null>(null)
const commentBusy = ref(false)
const commentError = ref('')
const mentionOpen = ref(false)
const mentionIndex = ref(0)
const mentionQuery = ref('')
const mentionStart = ref(0)
const editingCommentId = ref('')
const editingBody = ref('')
const deleteCommentId = ref('')
const watchBusy = ref(false)

const fileInput = ref<HTMLInputElement | null>(null)
const attachmentBusy = ref(false)

const linkTargetType = ref('work_item')
const linkTargetId = ref('')
const linkType = ref('related')
const linkBusy = ref(false)

const item = computed(() => detail.value)
const canWrite = computed(() => auth.can(store.projectId, 'work_item.write'))
const canModerate = computed(() => {
  const role = auth.projectRole(store.projectId)
  return role === 'owner' || role === 'maintainer' || auth.user?.role === 'admin'
})
const meId = computed(() => auth.user?.id ?? '')
const feature = computed(() =>
  item.value?.feature_id ? store.features.find((entry) => entry.id === item.value?.feature_id) : undefined,
)
const parentItem = computed(() =>
  item.value?.parent_id ? store.workItems.find((entry) => entry.id === item.value?.parent_id) : undefined,
)
const sprint = computed(() =>
  item.value?.sprint_id ? store.sprints.find((entry) => entry.id === item.value?.sprint_id) : undefined,
)
const release = computed(() =>
  item.value?.release_id ? store.releases.find((entry) => entry.id === item.value?.release_id) : undefined,
)

const tabs = computed(() => [
  { key: 'details' as TabKey, label: 'Details', count: 0 },
  { key: 'comments' as TabKey, label: 'Comments', count: detail.value?.comment_count ?? 0 },
  { key: 'links' as TabKey, label: 'Links', count: detail.value?.links.length ?? 0 },
  { key: 'attachments' as TabKey, label: 'Attachments', count: detail.value?.attachment_count ?? 0 },
  { key: 'activity' as TabKey, label: 'Activity', count: detail.value?.activity.length ?? 0 },
  { key: 'traceability' as TabKey, label: 'Traceability', count: detail.value?.child_count ?? 0 },
])

const mentionCandidates = computed(() => {
  const query = mentionQuery.value
  return store.members
    .filter((member) => {
      const haystack = `${member.username} ${member.email}`.toLowerCase()
      return query ? haystack.includes(query) : true
    })
    .slice(0, 6)
})

const targetOptions = computed(() => {
  switch (linkTargetType.value) {
    case 'feature':
      return store.features.map((entry) => ({ id: entry.id, label: entry.title }))
    case 'epic':
      return store.epics.map((entry) => ({ id: entry.id, label: entry.title }))
    case 'prd':
      return store.prds.map((entry) => ({ id: entry.id, label: entry.title }))
    case 'sprint':
      return store.sprints.map((entry) => ({ id: entry.id, label: entry.name }))
    case 'release':
      return store.releases.map((entry) => ({ id: entry.id, label: `${entry.name} · ${entry.version}` }))
    case 'test_case':
      return store.testCases.map((entry) => ({ id: entry.id, label: entry.title }))
    default:
      return store.workItems
        .filter((entry) => entry.id !== props.itemId)
        .map((entry) => ({ id: entry.id, label: `${entry.key} · ${entry.title}` }))
  }
})

async function load(): Promise<void> {
  if (!props.itemId) return
  loading.value = true
  error.value = ''
  try {
    detail.value = await api.getWorkItemDetail(store.projectId, props.itemId)
    if (detail.value) {
      if (!linkTargetId.value) linkTargetId.value = targetOptions.value[0]?.id ?? ''
      if (!tabs.value.some((entry) => entry.key === tab.value)) tab.value = 'details'
    }
  } catch (err) {
    detail.value = null
    error.value = err instanceof Error ? err.message : 'Failed to load work item'
  } finally {
    loading.value = false
  }
}

function close(): void {
  emit('update:modelValue', false)
}

async function refresh(): Promise<void> {
  await load()
  emit('changed')
}

function onWindowKeydown(event: KeyboardEvent): void {
  if (!props.modelValue) return
  if (event.key === 'Escape') {
    if (mentionOpen.value) {
      mentionOpen.value = false
      return
    }
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const focusable = panel.value.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (!first || !last) return
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(
  [() => props.modelValue, () => props.itemId],
  async ([open]) => {
    if (!open) {
      if (typeof document !== 'undefined') document.body.style.overflow = previousFocus ? '' : ''
      previousFocus?.focus()
      previousFocus = null
      mentionOpen.value = false
      editingCommentId.value = ''
      deleteCommentId.value = ''
      return
    }
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (typeof document !== 'undefined') document.body.style.overflow = 'hidden'
    tab.value = 'details'
    commentBody.value = ''
    commentError.value = ''
    await load()
    await nextTick()
    closeButton.value?.focus()
  },
  { immediate: true },
)

onMounted(() => window.addEventListener('keydown', onWindowKeydown))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onWindowKeydown)
  if (typeof document !== 'undefined') document.body.style.overflow = ''
})

// Reload when realtime workspace events bump the item revision.
watch(
  () => store.workItems.find((entry) => entry.id === props.itemId)?.revision,
  (revision, previous) => {
    if (props.modelValue && revision && previous && revision !== previous) void load()
  },
)

function selectTab(next: TabKey): void {
  tab.value = next
}

function onTabKeydown(event: KeyboardEvent, index: number): void {
  if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const buttons = tabList.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
  if (!buttons || buttons.length === 0) return
  let target = index
  if (event.key === 'ArrowRight') target = (index + 1) % buttons.length
  if (event.key === 'ArrowLeft') target = (index - 1 + buttons.length) % buttons.length
  if (event.key === 'Home') target = 0
  if (event.key === 'End') target = buttons.length - 1
  const button = buttons[target]
  if (button) {
    button.focus()
    const key = button.dataset.tab as TabKey | undefined
    if (key) tab.value = key
  }
}

function memberName(userId: string | null): string {
  if (!userId) return ''
  const member = store.members.find((entry) => entry.user_id === userId)
  return member?.username || member?.email || 'Team member'
}

function formatDate(value: string | null): string {
  if (!value) return '—'
  return new Date(value).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

function canEditComment(comment: Comment): boolean {
  return canModerate.value || (!!meId.value && comment.author_id === meId.value)
}

function canDeleteAttachment(attachment: Attachment): boolean {
  return canModerate.value || (!!meId.value && attachment.uploaded_by === meId.value)
}

// ---------------------------------------------------------------------------
// Mentions
// ---------------------------------------------------------------------------

function updateMentions(): void {
  const textarea = commentInput.value
  if (!textarea) return
  const caret = textarea.selectionStart ?? textarea.value.length
  const trigger = mentionQueryAt(textarea.value, caret)
  if (!trigger) {
    mentionOpen.value = false
    return
  }
  mentionQuery.value = trigger.query
  mentionStart.value = trigger.start
  mentionOpen.value = mentionCandidates.value.length > 0
  mentionIndex.value = 0
}

function chooseMention(member: { username: string }): void {
  const textarea = commentInput.value
  const caret = textarea?.selectionStart ?? commentBody.value.length
  const result = insertMention(commentBody.value, caret, mentionStart.value, member.username)
  commentBody.value = result.value
  mentionOpen.value = false
  void nextTick(() => {
    const element = commentInput.value
    if (!element) return
    element.focus()
    element.setSelectionRange(result.caret, result.caret)
  })
}

function onComposerKeydown(event: KeyboardEvent): void {
  if (mentionOpen.value) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      mentionIndex.value = (mentionIndex.value + 1) % mentionCandidates.value.length
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      mentionIndex.value = (mentionIndex.value - 1 + mentionCandidates.value.length) % mentionCandidates.value.length
      return
    }
    if (event.key === 'Enter' || event.key === 'Tab') {
      const member = mentionCandidates.value[mentionIndex.value]
      if (member) {
        event.preventDefault()
        chooseMention(member)
        return
      }
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      mentionOpen.value = false
      return
    }
  }
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault()
    void submitComment()
  }
}

// ---------------------------------------------------------------------------
// Comments
// ---------------------------------------------------------------------------

async function submitComment(): Promise<void> {
  if (!item.value || commentBusy.value) return
  const body = commentBody.value.trim()
  if (!body) {
    commentError.value = 'Write a comment before sending.'
    return
  }
  commentBusy.value = true
  commentError.value = ''
  try {
    await api.createComment(store.projectId, item.value.id, body)
    commentBody.value = ''
    mentionOpen.value = false
    await refresh()
    toast.success('Comment added.', item.value.key)
  } catch (err) {
    commentError.value = err instanceof Error ? err.message : 'Failed to add comment'
  } finally {
    commentBusy.value = false
  }
}

function startEditComment(comment: Comment): void {
  editingCommentId.value = comment.id
  editingBody.value = comment.body
  deleteCommentId.value = ''
}

async function saveComment(comment: Comment): Promise<void> {
  if (!item.value || commentBusy.value) return
  const body = editingBody.value.trim()
  if (!body) {
    toast.warning('A comment cannot be empty.', 'Comments')
    return
  }
  commentBusy.value = true
  try {
    await api.updateComment(store.projectId, comment.id, body)
    editingCommentId.value = ''
    await refresh()
    toast.success('Comment updated.', item.value.key)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to update comment', 'Comments')
  } finally {
    commentBusy.value = false
  }
}

async function deleteComment(comment: Comment): Promise<void> {
  if (!item.value || commentBusy.value) return
  if (deleteCommentId.value !== comment.id) {
    deleteCommentId.value = comment.id
    return
  }
  commentBusy.value = true
  try {
    await api.deleteComment(store.projectId, comment.id)
    deleteCommentId.value = ''
    await refresh()
    toast.success('Comment deleted.', item.value.key)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to delete comment', 'Comments')
  } finally {
    commentBusy.value = false
  }
}

// ---------------------------------------------------------------------------
// Watchers, attachments and links
// ---------------------------------------------------------------------------

async function toggleWatch(): Promise<void> {
  if (!item.value || watchBusy.value) return
  watchBusy.value = true
  try {
    const result = await api.setWorkItemWatching(store.projectId, item.value.id, !item.value.watching)
    detail.value = { ...item.value, watching: result.watching, watcher_count: Math.max(0, item.value.watcher_count + (result.watching ? 1 : -1)) }
    await refresh()
    toast.success(result.watching ? 'Watching this work item.' : 'Stopped watching.', item.value.key)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to update watch state', 'Work item')
  } finally {
    watchBusy.value = false
  }
}

function triggerUpload(): void {
  fileInput.value?.click()
}

async function onFileSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !item.value || attachmentBusy.value) return
  attachmentBusy.value = true
  try {
    await api.uploadAttachment(store.projectId, item.value.id, file)
    await refresh()
    toast.success(`${file.name} uploaded and scanned.`, 'Attachments')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Upload failed', 'Attachments')
  } finally {
    attachmentBusy.value = false
  }
}

async function downloadAttachment(attachment: Attachment): Promise<void> {
  try {
    const blob = await api.downloadAttachment(store.projectId, attachment.id)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = attachment.filename
    anchor.rel = 'noopener'
    anchor.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Download failed', 'Attachments')
  }
}

async function removeAttachment(attachment: Attachment): Promise<void> {
  if (!item.value || attachmentBusy.value) return
  attachmentBusy.value = true
  try {
    await api.deleteAttachment(store.projectId, attachment.id)
    await refresh()
    toast.success(`${attachment.filename} removed.`, 'Attachments')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to delete attachment', 'Attachments')
  } finally {
    attachmentBusy.value = false
  }
}

function onTargetTypeChange(): void {
  linkTargetId.value = targetOptions.value[0]?.id ?? ''
}

async function createLink(): Promise<void> {
  if (!item.value || linkBusy.value || !linkTargetId.value) return
  linkBusy.value = true
  try {
    await api.createWorkItemLink(store.projectId, item.value.id, {
      target_type: linkTargetType.value,
      target_id: linkTargetId.value,
      link_type: linkType.value.trim() || 'related',
    })
    await refresh()
    toast.success('Artifact linked.', item.value.key)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to create link', 'Links')
  } finally {
    linkBusy.value = false
  }
}

async function removeLink(link: LinkedArtifact): Promise<void> {
  if (!item.value || linkBusy.value) return
  linkBusy.value = true
  try {
    await api.deleteWorkItemLink(store.projectId, item.value.id, link.link_id)
    await refresh()
    toast.success('Link removed.', item.value.key)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to remove link', 'Links')
  } finally {
    linkBusy.value = false
  }
}

function openLinkedWorkItem(link: LinkedArtifact): void {
  if (link.artifact_type !== 'work_item') return
  emit('open-item', link.artifact_id)
}

function openChild(child: WorkItem): void {
  emit('open-item', child.id)
}

function artifactIcon(type: string): string {
  switch (type) {
    case 'work_item':
      return 'check-circle'
    case 'feature':
      return 'layers'
    case 'epic':
      return 'flag'
    case 'prd':
      return 'file'
    case 'test_case':
      return 'flask'
    case 'sprint':
      return 'clock'
    case 'release':
      return 'rocket'
    default:
      return 'link'
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="modelValue" class="fixed inset-0 z-40">
        <div class="absolute inset-0 bg-slate-900/40 backdrop-blur-sm" @click="close" />
        <aside
          ref="panel"
          class="absolute right-0 top-0 flex h-full w-full max-w-3xl flex-col bg-white shadow-2xl outline-none"
          role="dialog"
          aria-modal="true"
          aria-labelledby="work-item-drawer-title"
          tabindex="-1"
        >
          <header class="border-b border-slate-200 px-5 py-4">
            <div class="flex items-start justify-between gap-4">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-700">
                    {{ item?.key ?? 'Work item' }}
                  </span>
                  <span class="text-[11px] font-medium text-slate-500">
                    {{ workItemTypeLabels[item?.type ?? 'task'] ?? item?.type }}
                  </span>
                  <StatusPill v-if="item" :status="item.status" />
                  <PriorityPill v-if="item" :priority="item.priority" />
                </div>
                <h2 id="work-item-drawer-title" class="mt-2 truncate text-lg font-semibold text-slate-900">
                  {{ item?.title ?? (loading ? 'Loading work item…' : 'Work item') }}
                </h2>
              </div>
              <div class="flex shrink-0 items-center gap-1.5">
                <button
                  v-if="item && canWrite"
                  type="button"
                  class="btn-secondary text-xs"
                  :disabled="watchBusy"
                  :aria-pressed="item.watching"
                  @click="toggleWatch"
                >
                  <AppIcon :name="item.watching ? 'eyeOff' : 'eye'" :size="14" />
                  {{ item.watching ? 'Unwatch' : 'Watch' }}
                </button>
                <button
                  v-if="item && canWrite"
                  type="button"
                  class="btn-secondary text-xs"
                  @click="item && emit('edit', item)"
                >
                  <AppIcon name="edit" :size="14" />
                  Edit
                </button>
                <button ref="closeButton" type="button" class="btn-ghost p-2" aria-label="Close work item" @click="close">
                  <AppIcon name="close" :size="18" />
                </button>
              </div>
            </div>

            <div
              ref="tabList"
              class="mt-4 flex gap-1 overflow-x-auto"
              role="tablist"
              aria-label="Work item sections"
            >
              <button
                v-for="(entry, index) in tabs"
                :key="entry.key"
                type="button"
                role="tab"
                class="flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition"
                :class="tab === entry.key ? 'bg-slate-900 text-white' : 'text-slate-600 hover:bg-slate-100'"
                :data-tab="entry.key"
                :aria-selected="tab === entry.key"
                :tabindex="tab === entry.key ? 0 : -1"
                @click="selectTab(entry.key)"
                @keydown="onTabKeydown($event, index)"
              >
                {{ entry.label }}
                <span
                  v-if="entry.count"
                  class="rounded-full px-1.5 text-[10px]"
                  :class="tab === entry.key ? 'bg-white/20' : 'bg-slate-200 text-slate-700'"
                >
                  {{ entry.count }}
                </span>
              </button>
            </div>
          </header>

          <div class="flex-1 overflow-y-auto px-5 py-5">
            <div v-if="loading" class="flex items-center justify-center py-16 text-sm text-slate-500">
              <AppIcon name="loader" :size="18" class="mr-2 animate-spin" />
              Loading work item…
            </div>
            <div v-else-if="error" class="space-y-3">
              <p class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
              <button type="button" class="btn-secondary" @click="load">Retry</button>
            </div>

            <template v-else-if="item">
              <!-- Details -->
              <section v-if="tab === 'details'" class="space-y-5" aria-label="Work item details">
                <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <div class="rounded-lg border border-slate-200 p-3">
                    <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Assignee</p>
                    <p class="mt-1 truncate text-sm text-slate-800">{{ memberName(item.assignee_id) || 'Unassigned' }}</p>
                  </div>
                  <div class="rounded-lg border border-slate-200 p-3">
                    <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Sprint</p>
                    <p class="mt-1 truncate text-sm text-slate-800">{{ sprint?.name ?? 'None' }}</p>
                  </div>
                  <div class="rounded-lg border border-slate-200 p-3">
                    <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Release</p>
                    <p class="mt-1 truncate text-sm text-slate-800">{{ release?.name ?? 'None' }}</p>
                  </div>
                  <div class="rounded-lg border border-slate-200 p-3">
                    <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Due</p>
                    <p class="mt-1 truncate text-sm text-slate-800">{{ formatDate(item.due_date) }}</p>
                  </div>
                </div>

                <div v-if="item.labels.length" class="flex flex-wrap items-center gap-1.5">
                  <span class="text-[11px] font-semibold text-slate-500">Labels</span>
                  <span
                    v-for="label in item.labels"
                    :key="label"
                    class="rounded-full bg-slate-100 px-2 py-0.5 text-[11px] font-medium text-slate-700"
                  >
                    {{ label }}
                  </span>
                </div>

                <div>
                  <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Description</h3>
                  <p class="mt-1.5 whitespace-pre-wrap break-words text-sm leading-relaxed text-slate-700">
                    {{ item.description || 'No description yet.' }}
                  </p>
                </div>
                <div>
                  <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Acceptance criteria</h3>
                  <p class="mt-1.5 whitespace-pre-wrap break-words text-sm leading-relaxed text-slate-700">
                    {{ item.acceptance_criteria || 'No acceptance criteria recorded.' }}
                  </p>
                </div>

                <div v-if="item.type === 'bug'" class="grid gap-3 sm:grid-cols-3">
                  <div>
                    <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Reproduction</h3>
                    <p class="mt-1 whitespace-pre-wrap text-sm text-slate-700">{{ item.reproduction_steps || '—' }}</p>
                  </div>
                  <div>
                    <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Expected</h3>
                    <p class="mt-1 whitespace-pre-wrap text-sm text-slate-700">{{ item.expected_result || '—' }}</p>
                  </div>
                  <div>
                    <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Actual</h3>
                    <p class="mt-1 whitespace-pre-wrap text-sm text-slate-700">{{ item.actual_result || '—' }}</p>
                  </div>
                </div>

                <div class="grid gap-3 sm:grid-cols-2">
                  <div class="rounded-lg border border-slate-200 p-3">
                    <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Feature</p>
                    <button
                      v-if="feature"
                      type="button"
                      class="mt-1 block text-left text-sm font-medium text-indigo-700 hover:underline"
                      @click="emit('changed')"
                    >
                      {{ feature.title }}
                    </button>
                    <p v-else class="mt-1 text-sm text-slate-600">None</p>
                  </div>
                  <div class="rounded-lg border border-slate-200 p-3">
                    <p class="text-[10px] font-semibold uppercase tracking-wide text-slate-500">Parent</p>
                    <button
                      v-if="parentItem"
                      type="button"
                      class="mt-1 block text-left text-sm font-medium text-indigo-700 hover:underline"
                      @click="openChild(parentItem)"
                    >
                      {{ parentItem.key }} · {{ parentItem.title }}
                    </button>
                    <p v-else class="mt-1 text-sm text-slate-600">None</p>
                  </div>
                </div>

                <p class="text-[11px] text-slate-500">
                  Created {{ timeAgo(item.created_at) }} · Updated {{ timeAgo(item.updated_at) }} · Revision {{ item.revision }}
                </p>
              </section>

              <!-- Comments -->
              <section v-else-if="tab === 'comments'" class="space-y-4" aria-label="Comments">
                <div v-if="item.comments.length === 0">
                  <EmptyState icon="bell" title="No comments yet" description="Start the discussion with a question, decision or review note." compact />
                </div>
                <article
                  v-for="comment in item.comments"
                  :key="comment.id"
                  class="rounded-xl border border-slate-200 p-3.5"
                >
                  <div class="flex items-start justify-between gap-2">
                    <div class="min-w-0">
                      <p class="text-sm font-semibold text-slate-800">
                        {{ comment.author_name || 'Unknown author' }}
                        <span class="ml-1 text-[11px] font-normal text-slate-500">{{ timeAgo(comment.created_at) }}</span>
                        <span v-if="comment.edited_at" class="ml-1 text-[11px] font-normal italic text-slate-400">edited</span>
                      </p>
                    </div>
                    <div v-if="canEditComment(comment)" class="flex shrink-0 items-center gap-1">
                      <button type="button" class="btn-ghost p-1.5 text-xs" aria-label="Edit comment" @click="startEditComment(comment)">
                        <AppIcon name="edit" :size="13" />
                      </button>
                      <button
                        type="button"
                        class="btn-ghost p-1.5 text-xs"
                        :class="deleteCommentId === comment.id ? 'text-red-700' : ''"
                        :aria-label="deleteCommentId === comment.id ? 'Confirm delete comment' : 'Delete comment'"
                        @click="deleteComment(comment)"
                      >
                        <AppIcon name="trash" :size="13" />
                      </button>
                      <button v-if="deleteCommentId === comment.id" type="button" class="btn-ghost px-1.5 py-1 text-[11px]" @click="deleteCommentId = ''">
                        Cancel
                      </button>
                    </div>
                  </div>
                  <div v-if="editingCommentId === comment.id" class="mt-2 space-y-2">
                    <textarea v-model="editingBody" rows="4" class="input w-full text-sm" maxlength="10000" aria-label="Edit comment" />
                    <div class="flex justify-end gap-2">
                      <button type="button" class="btn-secondary text-xs" @click="editingCommentId = ''">Cancel</button>
                      <button type="button" class="btn-primary text-xs" :disabled="commentBusy" @click="saveComment(comment)">Save</button>
                    </div>
                  </div>
                  <p v-else class="mt-1.5 whitespace-pre-wrap break-words text-sm leading-relaxed text-slate-700">
                    <template v-for="(part, index) in mentionParts(comment.body)" :key="index">
                      <span v-if="part.mention" class="rounded bg-indigo-50 px-1 font-medium text-indigo-700">{{ part.text }}</span>
                      <template v-else>{{ part.text }}</template>
                    </template>
                  </p>
                  <div v-if="comment.mentions.length" class="mt-2 flex flex-wrap gap-1">
                    <span v-for="mention in comment.mentions" :key="mention.user_id" class="rounded-full bg-slate-100 px-2 py-0.5 text-[10px] text-slate-600">
                      @{{ mention.username }}
                    </span>
                  </div>
                </article>

                <div v-if="canWrite" class="rounded-xl border border-slate-200 bg-slate-50 p-3.5">
                  <label for="comment-body" class="text-xs font-semibold text-slate-700">Add a comment</label>
                  <div class="relative mt-1.5">
                    <textarea
                      id="comment-body"
                      ref="commentInput"
                      v-model="commentBody"
                      rows="4"
                      maxlength="10000"
                      class="input w-full text-sm"
                      placeholder="Write a comment. Use @username to mention a project member."
                      aria-describedby="comment-hint"
                      @input="updateMentions"
                      @keydown="onComposerKeydown"
                    />
                    <div
                      v-if="mentionOpen"
                      class="absolute bottom-full left-0 z-10 mb-1 w-64 overflow-hidden rounded-lg border border-slate-200 bg-white shadow-lg"
                      role="listbox"
                      aria-label="Mention suggestions"
                    >
                      <button
                        v-for="(member, index) in mentionCandidates"
                        :key="member.user_id"
                        type="button"
                        role="option"
                        :aria-selected="index === mentionIndex"
                        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs"
                        :class="index === mentionIndex ? 'bg-indigo-50 text-indigo-800' : 'text-slate-700 hover:bg-slate-50'"
                        @mousedown.prevent="chooseMention(member)"
                      >
                        <AppIcon name="user" :size="12" />
                        <span class="font-medium">{{ member.username }}</span>
                        <span class="truncate text-slate-500">{{ member.email }}</span>
                      </button>
                    </div>
                  </div>
                  <div class="mt-2 flex items-center justify-between gap-3">
                    <p id="comment-hint" class="text-[11px] text-slate-500">
                      {{ commentBody.length }}/10000 · Ctrl+Enter to send · mentions notify project members
                    </p>
                    <button type="button" class="btn-primary text-xs" :disabled="commentBusy || !commentBody.trim()" @click="submitComment">
                      <AppIcon name="arrowRight" :size="14" />
                      Comment
                    </button>
                  </div>
                  <p v-if="commentError" class="mt-2 text-xs text-red-700">{{ commentError }}</p>
                </div>
                <p v-else class="text-xs text-slate-500">You need work item write access to comment.</p>
              </section>

              <!-- Links -->
              <section v-else-if="tab === 'links'" class="space-y-4" aria-label="Links">
                <div v-if="item.links.length === 0">
                  <EmptyState icon="link" title="No linked artifacts" description="Connect this work item to a feature, test case, sprint, release or another work item." compact />
                </div>
                <ul class="space-y-2">
                  <li v-for="link in item.links" :key="link.link_id" class="flex items-center gap-3 rounded-lg border border-slate-200 px-3 py-2.5">
                    <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-slate-100 text-slate-600">
                      <AppIcon :name="artifactIcon(link.artifact_type)" :size="15" />
                    </span>
                    <div class="min-w-0 flex-1">
                      <p class="truncate text-sm font-medium text-slate-800">
                        <button
                          v-if="link.artifact_type === 'work_item'"
                          type="button"
                          class="hover:underline"
                          @click="openLinkedWorkItem(link)"
                        >
                          {{ link.artifact_key ? `${link.artifact_key} · ` : '' }}{{ link.title || link.artifact_id }}
                        </button>
                        <template v-else>
                          {{ link.artifact_key ? `${link.artifact_key} · ` : '' }}{{ link.title || link.artifact_id }}
                        </template>
                      </p>
                      <p class="text-[11px] text-slate-500">
                        {{ link.artifact_type.replace('_', ' ') }} · {{ link.direction }} · {{ link.link_type }}
                      </p>
                    </div>
                    <StatusPill v-if="link.status" :status="link.status" />
                    <button
                      v-if="canWrite"
                      type="button"
                      class="btn-ghost p-1.5 text-red-600"
                      aria-label="Remove link"
                      :disabled="linkBusy"
                      @click="removeLink(link)"
                    >
                      <AppIcon name="close" :size="14" />
                    </button>
                  </li>
                </ul>

                <div v-if="canWrite" class="rounded-xl border border-slate-200 bg-slate-50 p-3.5">
                  <p class="text-xs font-semibold text-slate-700">Link another artifact</p>
                  <div class="mt-2 grid gap-2 sm:grid-cols-4">
                    <select v-model="linkTargetType" class="input text-sm" aria-label="Linked artifact type" @change="onTargetTypeChange">
                      <option value="work_item">Work item</option>
                      <option value="feature">Feature</option>
                      <option value="epic">Epic</option>
                      <option value="prd">PRD</option>
                      <option value="test_case">Test case</option>
                      <option value="sprint">Sprint</option>
                      <option value="release">Release</option>
                    </select>
                    <select v-model="linkTargetId" class="input text-sm sm:col-span-2" aria-label="Linked artifact">
                      <option v-if="targetOptions.length === 0" value="">No artifacts available</option>
                      <option v-for="option in targetOptions" :key="option.id" :value="option.id">{{ option.label }}</option>
                    </select>
                    <input v-model="linkType" class="input text-sm" maxlength="40" aria-label="Link type" placeholder="related" />
                  </div>
                  <div class="mt-2 flex justify-end">
                    <button type="button" class="btn-primary text-xs" :disabled="linkBusy || !linkTargetId" @click="createLink">
                      <AppIcon name="link" :size="14" />
                      Link
                    </button>
                  </div>
                </div>
              </section>

              <!-- Attachments -->
              <section v-else-if="tab === 'attachments'" class="space-y-4" aria-label="Attachments">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <p class="text-xs text-slate-500">
                    PNG, JPEG, GIF, WebP, PDF, text, Markdown, CSV, JSON or ZIP · up to 10 MB · scanned before download.
                  </p>
                  <div v-if="canWrite">
                    <input
                      ref="fileInput"
                      type="file"
                      class="sr-only"
                      accept=".png,.jpg,.jpeg,.gif,.webp,.pdf,.txt,.log,.md,.csv,.json,.zip"
                      @change="onFileSelected"
                    />
                    <button type="button" class="btn-primary text-xs" :disabled="attachmentBusy" @click="triggerUpload">
                      <AppIcon :name="attachmentBusy ? 'loader' : 'plus'" :size="14" :class="attachmentBusy ? 'animate-spin' : ''" />
                      {{ attachmentBusy ? 'Uploading…' : 'Upload file' }}
                    </button>
                  </div>
                </div>
                <div v-if="item.attachments.length === 0">
                  <EmptyState icon="file" title="No attachments" description="Screenshots, logs and evidence are validated, scanned and permission-checked." compact />
                </div>
                <ul class="space-y-2">
                  <li v-for="attachment in item.attachments" :key="attachment.id" class="flex items-center gap-3 rounded-lg border border-slate-200 px-3 py-2.5">
                    <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-slate-100 text-slate-600">
                      <AppIcon name="file" :size="15" />
                    </span>
                    <div class="min-w-0 flex-1">
                      <p class="truncate text-sm font-medium text-slate-800">{{ attachment.filename }}</p>
                      <p class="text-[11px] text-slate-500">
                        {{ formatBytes(attachment.size_bytes) }} · {{ attachment.uploader_name || 'Unknown' }} · {{ timeAgo(attachment.created_at) }}
                      </p>
                    </div>
                    <span class="rounded-full bg-emerald-50 px-2 py-0.5 text-[10px] font-semibold text-emerald-700 ring-1 ring-inset ring-emerald-200">
                      {{ attachment.scan_status }}
                    </span>
                    <button type="button" class="btn-ghost p-1.5" aria-label="Download attachment" @click="downloadAttachment(attachment)">
                      <AppIcon name="external" :size="14" />
                    </button>
                    <button
                      v-if="canDeleteAttachment(attachment)"
                      type="button"
                      class="btn-ghost p-1.5 text-red-600"
                      aria-label="Delete attachment"
                      :disabled="attachmentBusy"
                      @click="removeAttachment(attachment)"
                    >
                      <AppIcon name="trash" :size="14" />
                    </button>
                  </li>
                </ul>
              </section>

              <!-- Activity -->
              <section v-else-if="tab === 'activity'" class="space-y-3" aria-label="Activity">
                <div v-if="item.activity.length === 0">
                  <EmptyState icon="activity" title="No activity yet" description="Status moves, comments, links and attachments appear here." compact />
                </div>
                <ol class="space-y-0">
                  <li v-for="event in item.activity" :key="event.id" class="flex gap-3 border-l border-slate-200 pb-4 pl-4 last:pb-0">
                    <span class="relative -ml-[25px] mt-0.5 flex h-4 w-4 items-center justify-center rounded-full bg-white ring-2 ring-slate-300">
                      <span class="h-1.5 w-1.5 rounded-full bg-slate-500" />
                    </span>
                    <div class="min-w-0">
                      <p class="text-sm text-slate-800">{{ event.summary || event.action }}</p>
                      <p class="text-[11px] text-slate-500">
                        {{ event.action }} · {{ timeAgo(event.created_at) }}
                        <span v-if="event.actor_id"> · {{ memberName(event.actor_id) || 'Team member' }}</span>
                      </p>
                    </div>
                  </li>
                </ol>
              </section>

              <!-- Traceability -->
              <section v-else class="space-y-5" aria-label="Traceability">
                <div class="grid gap-3 sm:grid-cols-2">
                  <div class="rounded-xl border border-slate-200 p-3.5">
                    <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Branches</h3>
                    <p v-if="item.branches.length === 0" class="mt-1 text-sm text-slate-600">No linked branches.</p>
                    <ul v-else class="mt-2 space-y-1.5">
                      <li v-for="branch in item.branches" :key="branch.id" class="flex items-center gap-2 text-sm text-slate-700">
                        <AppIcon name="branch" :size="14" />
                        <span class="truncate">{{ branch.name }}</span>
                        <StatusPill :status="branch.status" />
                      </li>
                    </ul>
                  </div>
                  <div class="rounded-xl border border-slate-200 p-3.5">
                    <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Pull requests</h3>
                    <p v-if="item.pull_requests.length === 0" class="mt-1 text-sm text-slate-600">No linked pull requests.</p>
                    <ul v-else class="mt-2 space-y-1.5">
                      <li v-for="pr in item.pull_requests" :key="pr.id" class="flex items-center gap-2 text-sm text-slate-700">
                        <AppIcon name="pullRequest" :size="14" />
                        <span class="truncate">#{{ pr.number }} {{ pr.title }}</span>
                        <StatusPill :status="pr.status" />
                      </li>
                    </ul>
                  </div>
                </div>
                <div>
                  <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Children</h3>
                  <p v-if="item.children.length === 0" class="mt-1 text-sm text-slate-600">No child work items.</p>
                  <ul v-else class="mt-2 space-y-2">
                    <li v-for="child in item.children" :key="child.id">
                      <button
                        type="button"
                        class="flex w-full items-center gap-3 rounded-lg border border-slate-200 px-3 py-2 text-left hover:border-indigo-300 hover:bg-indigo-50/40"
                        @click="openChild(child)"
                      >
                        <span class="font-mono text-[11px] font-semibold text-slate-500">{{ child.key }}</span>
                        <span class="min-w-0 flex-1 truncate text-sm font-medium text-slate-800">{{ child.title }}</span>
                        <StatusPill :status="child.status" />
                      </button>
                    </li>
                  </ul>
                </div>
                <div>
                  <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500">Watchers</h3>
                  <p v-if="item.watchers.length === 0" class="mt-1 text-sm text-slate-600">No watchers yet.</p>
                  <div v-else class="mt-2 flex flex-wrap gap-1.5">
                    <span v-for="watcher in item.watchers" :key="watcher.user_id" class="rounded-full bg-slate-100 px-2 py-0.5 text-[11px] text-slate-700">
                      {{ watcher.display_name || watcher.username }}
                    </span>
                  </div>
                </div>
              </section>
            </template>
          </div>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.18s ease;
}
.drawer-enter-active aside,
.drawer-leave-active aside {
  transition: transform 0.2s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
.drawer-enter-from aside,
.drawer-leave-to aside {
  transform: translateX(100%);
}
</style>
