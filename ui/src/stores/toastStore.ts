import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastKind = 'info' | 'success' | 'warning' | 'error'

export interface ToastItem {
  id: number
  message: string
  title?: string
  kind: ToastKind
  createdAt: number
}

let nextToastId = 1
const timers = new Map<number, ReturnType<typeof setTimeout>>()

export const useToastStore = defineStore('toast', () => {
  const toasts = ref<ToastItem[]>([])

  function dismiss(id: number): void {
    toasts.value = toasts.value.filter((toast) => toast.id !== id)
    const timer = timers.get(id)
    if (timer) {
      clearTimeout(timer)
      timers.delete(id)
    }
  }

  function show(
    message: string,
    kind: ToastKind = 'info',
    options: { title?: string; timeoutMs?: number } = {},
  ): number {
    const id = nextToastId++
    toasts.value.push({ id, message, kind, title: options.title, createdAt: Date.now() })

    // Keep the stack compact; older toasts fade out naturally.
    if (toasts.value.length > 4) {
      const oldest = toasts.value[0]
      if (oldest) dismiss(oldest.id)
    }

    const timeout = options.timeoutMs ?? (kind === 'error' ? 8000 : 5000)
    if (timeout > 0) {
      timers.set(id, setTimeout(() => dismiss(id), timeout))
    }
    return id
  }

  const success = (message: string, title?: string) => show(message, 'success', { title })
  const warning = (message: string, title?: string) => show(message, 'warning', { title })
  const error = (message: string, title?: string) => show(message, 'error', { title })

  function clear(): void {
    for (const timer of timers.values()) clearTimeout(timer)
    timers.clear()
    toasts.value = []
  }

  return { toasts, show, success, warning, error, dismiss, clear }
})
