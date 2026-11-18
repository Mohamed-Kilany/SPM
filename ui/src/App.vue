<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppSidebar from './components/AppSidebar.vue'
import AppIcon from './components/AppIcon.vue'
import ToastHost from './components/ToastHost.vue'
import { useAuthStore } from './stores/authStore'
import { useToastStore } from './stores/toastStore'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const toast = useToastStore()

const showNavigation = ref(false)
let handlingUnauthorized = false

const showShell = computed(() => Boolean(route.meta.requiresAuth) && auth.isAuthenticated)

watch(
  () => route.fullPath,
  () => {
    showNavigation.value = false
  },
)

function handleUnauthorized(): void {
  if (handlingUnauthorized) return
  handlingUnauthorized = true
  auth.logout()

  const wasOnLogin = route.name === 'login'
  if (!wasOnLogin) {
    toast.warning('Your session expired. Please sign in again.', 'Session ended')
  }

  const redirect = route.fullPath
  void router
    .replace({
      name: 'login',
      query: !wasOnLogin && redirect ? { redirect } : {},
    })
    .finally(() => {
      handlingUnauthorized = false
    })
}

onMounted(async () => {
  window.addEventListener('spm:unauthorized', handleUnauthorized)
  if (auth.isAuthenticated) {
    try {
      await auth.refreshUser()
    } catch {
      // A 401 is handled by the global unauthorized listener; transient network
      // failures leave the cached session usable and surface on the next call.
    }
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('spm:unauthorized', handleUnauthorized)
})
</script>

<template>
  <div v-if="showShell" class="min-h-screen bg-slate-50">
    <AppSidebar :open="showNavigation" @close="showNavigation = false" />

    <div class="lg:pl-64">
      <header
        class="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-slate-200 bg-white/95 px-4 backdrop-blur lg:hidden"
      >
        <button
          type="button"
          class="flex h-9 w-9 items-center justify-center rounded-lg border border-slate-200 bg-white text-slate-600 hover:bg-slate-50 hover:text-slate-900"
          aria-label="Open navigation"
          @click="showNavigation = true"
        >
          <AppIcon name="menu" :size="18" />
        </button>
        <RouterLink to="/projects" class="flex items-center gap-2">
          <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-indigo-600 text-white">
            <AppIcon name="sparkles" :size="15" />
          </span>
          <span class="text-sm font-semibold text-slate-900">SPM Platform</span>
        </RouterLink>
        <span class="flex-1" />
        <RouterLink
          to="/profile"
          class="flex h-8 w-8 items-center justify-center rounded-full bg-indigo-100 text-[10px] font-bold text-indigo-700"
          title="Open your profile"
        >
          {{ auth.initials }}
        </RouterLink>
      </header>

      <main class="min-h-[calc(100vh-3.5rem)] lg:min-h-screen">
        <RouterView />
      </main>
    </div>
  </div>

  <RouterView v-else />

  <ToastHost />
</template>
