<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import AppIcon from '../AppIcon.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import EmptyState from './EmptyState.vue'
import { api } from '../../api/client'
import { useAuthStore } from '../../stores/authStore'
import { useWorkspaceStore } from '../../stores/workspaceStore'
import { useToastStore } from '../../stores/toastStore'
import { customFieldDisplay, optionLines, parseOptionLines } from '../../lib/planning'
import type { CustomField, CustomFieldType } from '../../types/workspace'

const auth = useAuthStore()
const store = useWorkspaceStore()
const toast = useToastStore()

const FIELD_TYPES: Array<{ value: CustomFieldType; label: string }> = [
  { value: 'text', label: 'Text' },
  { value: 'number', label: 'Number' },
  { value: 'select', label: 'Select' },
  { value: 'date', label: 'Date' },
  { value: 'user', label: 'Project member' },
]

const form = reactive({
  name: '',
  type: 'text' as CustomFieldType,
  description: '',
  required: false,
  options: '',
  min: '',
  max: '',
})
const editingId = ref('')
const busy = ref(false)
const error = ref('')
const purgeTarget = ref<CustomField | null>(null)
const purging = ref(false)

const editingField = computed(() =>
  editingId.value ? store.customFieldById(editingId.value) : undefined,
)
const typeLocked = computed(() =>
  Boolean(editingField.value && valuesExist(editingField.value)),
)

const projectId = computed(() => store.projectId)
const canManage = computed(() => auth.can(projectId.value, 'planning.write'))
const canPurge = computed(() => auth.can(projectId.value, 'settings.write'))
const fields = computed(() => store.allCustomFields)

function usage(field: CustomField): number {
  return store.customFieldValues.filter((value) => value.field_id === field.id).length
}

function valuesExist(field: CustomField): boolean {
  return usage(field) > 0
}

function startEdit(field: CustomField): void {
  editingId.value = field.id
  form.name = field.name
  form.type = field.type as CustomFieldType
  form.description = field.description
  form.required = field.required
  form.options = optionLines(field.options)
  form.min = field.config.min === undefined ? '' : String(field.config.min)
  form.max = field.config.max === undefined ? '' : String(field.config.max)
  error.value = ''
}

function reset(): void {
  editingId.value = ''
  form.name = ''
  form.type = 'text'
  form.description = ''
  form.required = false
  form.options = ''
  form.min = ''
  form.max = ''
  error.value = ''
}

async function save(): Promise<void> {
  if (!canManage.value || busy.value) return
  const current = editingId.value ? store.customFieldById(editingId.value) : undefined
  if (!form.name.trim()) {
    error.value = 'A field name is required.'
    return
  }
  if (form.type === 'select' && parseOptionLines(form.options).length === 0) {
    error.value = 'A select field needs at least one option.'
    return
  }
  if (form.min !== '' && form.max !== '' && Number(form.min) > Number(form.max)) {
    error.value = 'Minimum cannot be greater than maximum.'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const config: Record<string, unknown> = {}
    if (form.type === 'number') {
      if (form.min !== '' && Number.isFinite(Number(form.min))) config.min = Number(form.min)
      if (form.max !== '' && Number.isFinite(Number(form.max))) config.max = Number(form.max)
    }
    const payload = {
      key: current?.key,
      name: form.name.trim(),
      type: form.type,
      description: form.description,
      required: form.required,
      position: current?.position ?? 0,
      options: form.type === 'select' ? parseOptionLines(form.options) : [],
      config,
      archived: false,
    }
    if (editingId.value) {
      await api.updateCustomField(projectId.value, editingId.value, payload)
      toast.success('Custom field updated.', 'Custom fields')
    } else {
      await api.createCustomField(projectId.value, payload)
      toast.success('Custom field created.', 'Custom fields')
    }
    await store.refresh()
    reset()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to save custom field'
  } finally {
    busy.value = false
  }
}

async function archive(field: CustomField): Promise<void> {
  if (!canManage.value) return
  try {
    await api.deleteCustomField(projectId.value, field.id)
    await store.refresh()
    if (editingId.value === field.id) reset()
    toast.success('Custom field archived. Stored values are preserved.', 'Custom fields')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to archive custom field', 'Custom fields')
  }
}

async function restore(field: CustomField): Promise<void> {
  if (!canManage.value) return
  try {
    await api.updateCustomField(projectId.value, field.id, {
      key: field.key,
      name: field.name,
      type: field.type,
      description: field.description,
      required: field.required,
      position: field.position,
      options: field.options,
      config: field.config,
      archived: false,
    })
    await store.refresh()
    toast.success('Custom field restored.', 'Custom fields')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to restore custom field', 'Custom fields')
  }
}

async function purge(): Promise<void> {
  const target = purgeTarget.value
  if (!target || !canPurge.value || purging.value) return
  purging.value = true
  try {
    await api.deleteCustomField(projectId.value, target.id, true)
    await store.refresh()
    if (editingId.value === target.id) reset()
    purgeTarget.value = null
    toast.success('Custom field and its values were permanently deleted.', 'Custom fields')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : 'Failed to purge custom field', 'Custom fields')
  } finally {
    purging.value = false
  }
}

function typeLabel(type: string): string {
  return FIELD_TYPES.find((entry) => entry.value === type)?.label ?? type
}

function sample(field: CustomField): string {
  const value = store.customFieldValues.find((entry) => entry.field_id === field.id)
  return value ? customFieldDisplay(field, value.value) : ''
}
</script>

<template>
  <section class="card overflow-hidden">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-slate-200 p-6">
      <div>
        <h3 class="text-sm font-semibold text-slate-900">Custom fields</h3>
        <p class="mt-1 text-xs text-slate-500">Typed project attributes. Values live in a side table, so adding a field never changes the schema.</p>
      </div>
      <span class="badge bg-slate-100 text-slate-600 ring-slate-200">{{ fields.length }} fields</span>
    </div>

    <form v-if="canManage" class="grid gap-3 border-b border-slate-200 bg-slate-50/70 p-4 sm:p-5 lg:grid-cols-2" @submit.prevent="save">
      <label class="block">
        <span class="field-label">Name</span>
        <input v-model="form.name" class="field-input" maxlength="80" placeholder="Story points" required />
      </label>
      <label class="block">
        <span class="field-label">Type</span>
        <select
          v-model="form.type"
          class="field-input"
          :disabled="typeLocked"
        >
          <option v-for="entry in FIELD_TYPES" :key="entry.value" :value="entry.value">{{ entry.label }}</option>
        </select>
        <span v-if="typeLocked" class="mt-1 block text-[11px] text-slate-500">
          Type is locked while stored values exist. Archive the field and create a new one instead.
        </span>
      </label>
      <label class="block lg:col-span-2">
        <span class="field-label">Description</span>
        <input v-model="form.description" class="field-input" maxlength="500" placeholder="Shown beside the field when editing a work item" />
      </label>
      <label v-if="form.type === 'select'" class="block lg:col-span-2">
        <span class="field-label">Options</span>
        <textarea v-model="form.options" rows="4" class="field-input font-mono text-xs" placeholder="high: High&#10;medium: Medium&#10;low: Low" />
        <span class="mt-1 block text-[11px] text-slate-500">One option per line as <code class="font-mono">value: Label</code>. Values use lowercase letters, digits, hyphens or underscores.</span>
      </label>
      <div v-if="form.type === 'number'" class="grid grid-cols-2 gap-3 lg:col-span-2">
        <label class="block">
          <span class="field-label">Minimum</span>
          <input v-model="form.min" type="number" step="any" class="field-input" />
        </label>
        <label class="block">
          <span class="field-label">Maximum</span>
          <input v-model="form.max" type="number" step="any" class="field-input" />
        </label>
      </div>
      <label class="flex items-center gap-2 text-xs font-medium text-slate-700">
        <input v-model="form.required" type="checkbox" class="h-4 w-4 rounded border-slate-300" />
        Required on every work item
      </label>
      <div class="flex items-center justify-end gap-2">
        <button v-if="editingId" type="button" class="btn-secondary" :disabled="busy" @click="reset">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy">
          <AppIcon v-if="busy" name="loader" :size="15" class="animate-spin" />
          <AppIcon v-else :name="editingId ? 'check' : 'plus'" :size="15" />
          {{ editingId ? 'Save field' : 'Add field' }}
        </button>
      </div>
      <p v-if="error" class="text-xs text-red-600 lg:col-span-2" role="alert">{{ error }}</p>
    </form>

    <p v-else class="border-b border-slate-200 bg-slate-50/70 px-4 py-3 text-xs text-slate-500 sm:px-6">
      Read-only: defining fields needs <code class="font-mono">planning.write</code>; purging values needs <code class="font-mono">settings.write</code>.
    </p>

    <ul v-if="fields.length" class="divide-y divide-slate-100">
      <li v-for="field in fields" :key="field.id" class="flex flex-wrap items-center gap-3 px-4 py-3 sm:px-6">
        <span class="flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-slate-100 text-slate-600">
          <AppIcon name="filter" :size="14" />
        </span>
        <div class="min-w-0 flex-1">
          <p class="flex flex-wrap items-center gap-2 text-sm font-medium text-slate-900">
            {{ field.name }}
            <span class="badge bg-slate-100 text-slate-600 ring-slate-200">{{ typeLabel(field.type) }}</span>
            <span v-if="field.required" class="badge bg-amber-50 text-amber-700 ring-amber-200">Required</span>
            <span v-if="field.archived_at" class="badge bg-slate-200 text-slate-600 ring-slate-300">Archived</span>
          </p>
          <p class="truncate text-xs text-slate-500">
            <code class="font-mono">{{ field.key }}</code>
            <span v-if="field.description"> · {{ field.description }}</span>
            · {{ usage(field) }} values
            <span v-if="sample(field)"> · e.g. {{ sample(field) }}</span>
          </p>
        </div>
        <div v-if="canManage" class="flex flex-wrap items-center gap-1">
          <button type="button" class="btn-ghost text-xs" :aria-label="`Edit ${field.name}`" @click="startEdit(field)">
            <AppIcon name="edit" :size="14" />
          </button>
          <button
            v-if="field.archived_at"
            type="button"
            class="btn-ghost text-xs"
            :aria-label="`Restore ${field.name}`"
            @click="restore(field)"
          >
            <AppIcon name="refresh" :size="14" />
          </button>
          <button
            v-else
            type="button"
            class="btn-ghost text-xs"
            :aria-label="`Archive ${field.name}`"
            @click="archive(field)"
          >
            <AppIcon name="inbox" :size="14" />
          </button>
          <button
            v-if="canPurge"
            type="button"
            class="btn-ghost text-xs text-red-600 hover:bg-red-50"
            :aria-label="`Purge ${field.name}`"
            @click="purgeTarget = field"
          >
            <AppIcon name="trash" :size="14" />
          </button>
        </div>
      </li>
    </ul>
    <div v-else class="p-6">
      <EmptyState icon="filter" title="No custom fields yet" description="Add a typed field to capture data that does not belong in the core work item schema." />
    </div>

    <ConfirmDialog
      :open="Boolean(purgeTarget)"
      title="Permanently delete custom field"
      :message="purgeTarget ? `Purge ${purgeTarget.name} and every stored value? This cannot be undone. Archiving is the safe default.` : ''"
      confirm-label="Purge field and values"
      :busy="purging"
      @confirm="purge"
      @cancel="purgeTarget = null"
      @update:open="(value) => { if (!value) purgeTarget = null }"
    />
  </section>
</template>
