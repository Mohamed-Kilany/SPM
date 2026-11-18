<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import { useAuthStore } from '../stores/authStore'
import { useToastStore } from '../stores/toastStore'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const toast = useToastStore()

const username = ref('')
const password = ref('')
const showPassword = ref(false)
const loading = ref(false)
const error = ref('')

const isDev = import.meta.env.DEV

const redirectTarget = computed(() => {
  const value = route.query.redirect
  if (typeof value === 'string' && value.startsWith('/') && !value.startsWith('//')) {
    return value
  }
  return '/projects'
})

function clearError(): void {
  if (error.value) error.value = ''
}

async function submit(): Promise<void> {
  if (loading.value) return
  error.value = ''

  if (!username.value.trim() || !password.value) {
    error.value = 'Enter both your username and password.'
    return
  }

  loading.value = true
  try {
    await auth.login(username.value.trim(), password.value)
    toast.success('Welcome back to SPM Platform.', 'Signed in')
    await router.replace(redirectTarget.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unable to sign in. Please try again.'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  if (auth.isAuthenticated) void router.replace(redirectTarget.value)
})
</script>

<template>
  <div class="relative flex min-h-screen items-center justify-center overflow-hidden bg-gradient-to-br from-slate-50 via-white to-indigo-50 px-4 py-10">
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute -left-24 top-1/4 h-96 w-96 rounded-full bg-indigo-200/40 blur-[110px]" />
      <div class="absolute -right-32 bottom-0 h-[28rem] w-[28rem] rounded-full bg-sky-200/30 blur-[120px]" />
      <div class="bg-grid absolute inset-0 opacity-40" />
    </div>

    <div class="relative w-full max-w-[26rem]">
      <div class="mb-8 flex justify-center animate-fade-scale-in">
        <div class="flex items-center gap-3 rounded-2xl border border-slate-200 bg-white px-4 py-3 shadow-sm">
          <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-indigo-600 text-white shadow-sm">
            <AppIcon name="sparkles" :size="21" />
          </span>
          <p class="text-lg font-semibold tracking-tight text-slate-900">SPM Platform</p>
        </div>
      </div>

      <div class="card animate-fade-scale-in rounded-2xl p-7 sm:p-8">
        <div class="mb-7">
          <h1 class="text-2xl font-semibold tracking-tight text-slate-900">Welcome back</h1>
        </div>

        <form class="space-y-5" @submit.prevent="submit">
          <label class="block">
            <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
              Username
            </span>
            <div class="relative">
              <span class="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-600">
                <AppIcon name="user" :size="16" />
              </span>
              <input
                v-model="username"
                type="text"
                required
                autocomplete="username"
                :disabled="loading"
                placeholder="your.username"
                class="field-input pl-10"
                @input="clearError"
              />
            </div>
          </label>

          <label class="block">
            <span class="mb-1.5 block text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">
              Password
            </span>
            <div class="relative">
              <span class="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-600">
                <AppIcon name="lock" :size="16" />
              </span>
              <input
                v-model="password"
                :type="showPassword ? 'text' : 'password'"
                required
                autocomplete="current-password"
                :disabled="loading"
                placeholder="••••••••"
                class="field-input pl-10 pr-11"
                @input="clearError"
              />
              <button
                type="button"
                class="absolute right-2.5 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-md text-slate-600 hover:bg-slate-100 hover:text-slate-700"
                :aria-label="showPassword ? 'Hide password' : 'Show password'"
                tabindex="-1"
                @click="showPassword = !showPassword"
              >
                <AppIcon :name="showPassword ? 'eyeOff' : 'eye'" :size="15" />
              </button>
            </div>
          </label>

          <p
            v-if="error"
            class="rounded-lg border border-red-200 bg-red-50 px-4 py-2.5 text-center text-xs font-medium text-red-700"
            role="alert"
          >
            {{ error }}
          </p>

          <button type="submit" :disabled="loading" class="btn-primary w-full py-3">
            <AppIcon v-if="loading" name="loader" :size="17" class="animate-spin" />
            <span>{{ loading ? 'Signing in…' : 'Sign in' }}</span>
            <AppIcon v-if="!loading" name="arrowRight" :size="16" class="opacity-70" />
          </button>
        </form>

        <div v-if="isDev" class="mt-6 rounded-lg border border-slate-200 bg-slate-50 px-3.5 py-2.5">
          <p class="text-[10px] font-medium uppercase tracking-[0.14em] text-slate-600">Development access</p>
          <p class="mt-1 font-mono text-[11px] text-slate-600">admin / Admin@1234</p>
        </div>
      </div>

      <div class="mt-6 flex flex-wrap items-center justify-center gap-x-5 gap-y-2 text-[10px] font-medium uppercase tracking-[0.12em] text-slate-600">
        <span class="flex items-center gap-1.5"><AppIcon name="lock" :size="12" /> Secure SSE tickets</span>
        <span class="flex items-center gap-1.5"><AppIcon name="activity" :size="12" /> Real-time</span>
        <span class="flex items-center gap-1.5"><AppIcon name="layers" :size="12" /> Traceable by default</span>
      </div>
    </div>
  </div>
</template>
