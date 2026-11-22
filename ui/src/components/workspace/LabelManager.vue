<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import AppIcon from '../AppIcon.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import EmptyState from './EmptyState.vue'
import { api } from '../../api/client'
import { useAuthStore } from '../../stores/authStore'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { LABEL_COLORS, labelDot, labelTone, normalizeLabelInput } from '../../lib/planning'
import type { ProjectLabel } from '../../types/workspace'

const auth = useAuthStore()
const store = useWorkspaceStore()
const toast = useToastStore()

const form = reactive({ name: '', color: 'slate' })
const editingId = ref('')
const busy = ref(false)
const error = ref('')
const deleteTarget = ref<ProjectLabel | null>(null)
const deleting = ref(false)

const projectId = computed(() => store.projectId)
const canManage = computed(() => auth.can(projectId.value, 'planning.write'))
const labels = computed(() => store.labels)

function usage(name: string): number {
  return store.workItems.filter((item) => (item.labels ?? []).includes(name)).length
}

function startEdit(label: ProjectLabel): void {
  editingId.value = label.id
  form.name = label.name
  form.color = label.color
  error.value = ''
}

function reset(): void {
  editingId.value = ''
  form.name = ''
  form.color = 'slate'
  error.value = ''
}

async function save(): Promise<void> {
  if (!canManage.value || busy.value) return
  const names = normalizeLabelInput(form.name)
  if (names.length !== 1) {
    error.value = 'Use 1–24 lowercase letters, digits, spaces, hyphens or underscores.'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const payload = { name: names[0]!, color: form.color }
    if (editingId.value) {
      await api.updateLabel(projectId.value, editingId.value, payload)
      toast.success('Label updated and usages renamed.', 'Labels')
    } else {
      await api.createLabel(projectId.value, payload)
      toast.success('Label created.', 'Labels')
    }
    await store.refresh()
    reset()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to save label'
  } finally {
    busy.value = false
  }
}

async function remove(): Promise<void> {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  try {
    await api.deleteLabel(projectId.value, target.id)
    await store.refresh()
    deleteTarget.value = null
    if (editingId.value === target.id) reset()
    toast.success('Label deleted and removed from every work item.', 'Labels')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to delete label', 'Labels')
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section class="card overflow-hidden">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-slate-200 p-6">
      <div>
        <h3 class="text-sm font-semibold text-slate-900">Labels</h3>
        <p class="mt-1 text-xs text-slate-500">Reusable project labels with a color token. Renaming rewrites every usage; deleting removes it from work items.</p>
      </div>
      <span class="badge bg-slate-100 text-slate-600 ring-slate-200">{{ labels.length }} labels</span>
    </div>

    <form v-if="canManage" class="grid gap-3 border-b border-slate-200 bg-slate-50/70 p-4 sm:p-5 lg:grid-cols-[minmax(200px,1fr)_170px_auto]" @submit.prevent="save">
      <label class="block">
        <span class="field-label">Label name</span>
        <input v-model="form.name" class="field-input" maxlength="24" placeholder="needs qa" required />
      </label>
      <label class="block">
        <span class="field-label">Color</span>
        <select v-model="form.color" class="field-input">
          <option v-for="color in LABEL_COLORS" :key="color" :value="color">{{ color }}</option>
        </select>
      </label>
      <div class="flex items-end gap-2">
        <button type="submit" class="btn-primary" :disabled="busy">
          <AppIcon v-if="busy" name="loader" :size="15" class="animate-spin" />
          <AppIcon v-else :name="editingId ? 'check' : 'plus'" :size="15" />
          {{ editingId ? 'Save' : 'Add label' }}
        </button>
        <button v-if="editingId" type="button" class="btn-secondary" :disabled="busy" @click="reset">Cancel</button>
      </div>
      <p v-if="error" class="text-xs text-red-600 lg:col-span-3" role="alert">{{ error }}</p>
    </form>

    <p v-else class="border-b border-slate-200 bg-slate-50/70 px-4 py-3 text-xs text-slate-500 sm:px-6">
      Read-only: creating labels needs <code class="font-mono">planning.write</code>.
    </p>

    <ul v-if="labels.length" class="divide-y divide-slate-100">
      <li v-for="label in labels" :key="label.id" class="flex flex-wrap items-center gap-3 px-4 py-3 sm:px-6">
        <span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold ring-1" :class="labelTone(label.color)">
          <span class="h-1.5 w-1.5 rounded-full" :class="labelDot(label.color)" />
          {{ label.name }}
        </span>
        <span class="text-[11px] text-slate-500">{{ usage(label.name) }} work items</span>
        <div v-if="canManage" class="ml-auto flex items-center gap-1">
          <button type="button" class="btn-ghost text-xs" :aria-label="`Edit ${label.name}`" @click="startEdit(label)">
            <AppIcon name="edit" :size="14" />
          </button>
          <button type="button" class="btn-ghost text-xs text-red-600 hover:bg-red-50" :aria-label="`Delete ${label.name}`" @click="deleteTarget = label">
            <AppIcon name="trash" :size="14" />
          </button>
        </div>
      </li>
    </ul>
    <div v-else class="p-6">
      <EmptyState icon="tag" title="No labels yet" description="Create the first reusable label to color and filter work items." />
    </div>

    <ConfirmDialog
      :open="Boolean(deleteTarget)"
      title="Delete label"
      :message="deleteTarget ? `Delete ${deleteTarget.name}? It will be removed from every work item that uses it.` : ''"
      confirm-label="Delete label"
      :busy="deleting"
      @confirm="remove"
      @cancel="deleteTarget = null"
      @update:open="(value) => { if (!value) deleteTarget = null }"
    />
  </section>
</template>
