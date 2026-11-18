<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { useToastStore } from '../stores/toastStore'

const toastStore = useToastStore()

const styles: Record<string, { shell: string; icon: string; name: string }> = {
  success: {
    shell: 'border-emerald-200 bg-white',
    icon: 'bg-emerald-50 text-emerald-700',
    name: 'check-circle',
  },
  error: {
    shell: 'border-red-200 bg-white',
    icon: 'bg-red-50 text-red-600',
    name: 'alert',
  },
  warning: {
    shell: 'border-amber-200 bg-white',
    icon: 'bg-amber-50 text-amber-700',
    name: 'alert',
  },
  info: {
    shell: 'border-indigo-200 bg-white',
    icon: 'bg-indigo-50 text-indigo-600',
    name: 'sparkles',
  },
}

const toastStyle = computed(() => (kind: string) => styles[kind] ?? styles.info!)
</script>

<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-[calc(100vw-2rem)] max-w-sm flex-col gap-2.5"
      aria-live="polite"
      aria-atomic="false"
    >
      <TransitionGroup name="toast">
        <div
          v-for="toast in toastStore.toasts"
          :key="toast.id"
          class="pointer-events-auto flex items-start gap-3 rounded-xl border p-3.5 shadow-lg"
          :class="toastStyle(toast.kind).shell"
          role="status"
        >
          <span
            class="mt-0.5 flex h-8 w-8 flex-none items-center justify-center rounded-lg"
            :class="toastStyle(toast.kind).icon"
          >
            <AppIcon :name="toastStyle(toast.kind).name" :size="16" />
          </span>

          <div class="min-w-0 flex-1 pt-0.5">
            <p v-if="toast.title" class="text-[13px] font-semibold text-slate-900">{{ toast.title }}</p>
            <p class="text-[13px] leading-relaxed text-slate-600">{{ toast.message }}</p>
          </div>

          <button
            type="button"
            class="mt-0.5 flex-none rounded-md p-1 text-slate-600 hover:bg-slate-100 hover:text-slate-700"
            aria-label="Dismiss notification"
            @click="toastStore.dismiss(toast.id)"
          >
            <AppIcon name="close" :size="14" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition: opacity 180ms ease, transform 220ms ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(16px) scale(0.98);
}
.toast-move {
  transition: transform 220ms ease;
}
</style>
