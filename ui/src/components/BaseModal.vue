<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    title?: string
    description?: string
    maxWidth?: 'sm' | 'md' | 'lg'
    persistent?: boolean
  }>(),
  {
    title: '',
    description: '',
    maxWidth: 'md',
    persistent: false,
  },
)

const emit = defineEmits<{ (event: 'update:modelValue', value: boolean): void }>()

const panel = ref<HTMLElement | null>(null)
const titleId = `modal-title-${Math.random().toString(36).slice(2, 9)}`
let previousFocus: HTMLElement | null = null

const widthClass = computed(
  () =>
    ({
      sm: 'max-w-sm',
      md: 'max-w-md',
      lg: 'max-w-2xl',
    })[props.maxWidth],
)

function requestClose(): void {
  if (props.persistent) return
  emit('update:modelValue', false)
}

function handleKeydown(event: KeyboardEvent): void {
  if (!props.modelValue) return
  if (event.key === 'Escape') {
    event.preventDefault()
    requestClose()
  }
}

function lockScroll(locked: boolean): void {
  document.body.classList.toggle('modal-open', locked)
}

watch(
  () => props.modelValue,
  async (open) => {
    if (open) {
      previousFocus = document.activeElement as HTMLElement | null
      lockScroll(true)
      await nextTick()
      const target =
        panel.value?.querySelector<HTMLElement>('[data-autofocus]') ??
        panel.value?.querySelector<HTMLElement>('input, textarea, select, button')
      target?.focus()
    } else {
      lockScroll(false)
      previousFocus?.focus?.()
      previousFocus = null
    }
  },
)

onMounted(() => document.addEventListener('keydown', handleKeydown))
onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleKeydown)
  lockScroll(false)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-[80] overflow-y-auto bg-slate-900/30 backdrop-blur-sm"
        @mousedown.self="requestClose"
      >
        <div class="flex min-h-full items-center justify-center p-4 sm:p-6">
          <section
            ref="panel"
            class="modal-panel relative w-full overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl"
            :class="widthClass"
            role="dialog"
            aria-modal="true"
            :aria-labelledby="title ? titleId : undefined"
          >
            <div class="flex items-start justify-between gap-4 px-6 pb-1 pt-6">
              <div class="min-w-0">
                <h2 v-if="title" :id="titleId" class="text-lg font-semibold tracking-tight text-slate-900">
                  {{ title }}
                </h2>
                <p v-if="description" class="mt-1 text-sm leading-relaxed text-slate-600">
                  {{ description }}
                </p>
              </div>
              <button
                type="button"
                class="flex h-8 w-8 flex-none items-center justify-center rounded-lg text-slate-600 hover:bg-slate-100 hover:text-slate-700"
                aria-label="Close dialog"
                @click="requestClose"
              >
                <AppIcon name="close" :size="17" />
              </button>
            </div>

            <div class="px-6 py-5">
              <slot />
            </div>

            <div
              v-if="$slots.footer"
              class="flex items-center justify-end gap-3 border-t border-slate-200 bg-slate-50 px-6 py-4"
            >
              <slot name="footer" />
            </div>
          </section>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity 180ms ease;
}
.modal-enter-active .modal-panel,
.modal-leave-active .modal-panel {
  transition: transform 220ms ease, opacity 180ms ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from .modal-panel,
.modal-leave-to .modal-panel {
  opacity: 0;
  transform: translateY(12px) scale(0.985);
}
</style>
