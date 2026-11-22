<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import { api, ApiError } from '../api/client'
import { useAuthStore } from '../stores/authStore'
import { useToastStore } from '../stores/toastStore'
import type { AuthSession } from '../types/project'

const auth = useAuthStore()
const toast = useToastStore()
const router = useRouter()

const profileBusy = ref(false)
const profileError = ref('')
const profile = reactive({
  display_name: '',
  email: '',
  avatar_url: '',
})

const passwordBusy = ref(false)
const passwordError = ref('')
const password = reactive({
  current_password: '',
  new_password: '',
  confirm_password: '',
})

const sessions = ref<AuthSession[]>([])
const sessionsBusy = ref(false)
const sessionsError = ref('')
const revokingId = ref('')
const revokeOthersOpen = ref(false)
const revokeOthersBusy = ref(false)

const avatarPreview = computed(() => profile.avatar_url.trim())
const otherSessions = computed(() => sessions.value.filter((session) => !session.current))

function hydrate(): void {
  profile.display_name = auth.user?.display_name || auth.user?.username || ''
  profile.email = auth.user?.email || ''
  profile.avatar_url = auth.user?.avatar_url || ''
}

function formatWhen(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'Unknown'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}

function lastSeenLabel(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'unknown'
  const elapsed = Date.now() - date.getTime()
  if (elapsed >= 0 && elapsed < 120_000) return 'active now'
  return formatWhen(value)
}

async function loadSessions(): Promise<void> {
  sessionsBusy.value = true
  sessionsError.value = ''
  try {
    const response = await api.listSessions()
    sessions.value = response.sessions ?? []
  } catch (error) {
    sessionsError.value = error instanceof Error ? error.message : 'Failed to load sessions'
  } finally {
    sessionsBusy.value = false
  }
}

async function revokeSession(session: AuthSession): Promise<void> {
  if (session.current) return
  revokingId.value = session.id
  sessionsError.value = ''
  try {
    await api.revokeSession(session.id)
    sessions.value = sessions.value.filter((item) => item.id !== session.id)
    toast.success(`${session.device} has been signed out.`, 'Sessions')
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      await loadSessions()
      toast.show('That session was already signed out.', 'info')
    } else {
      sessionsError.value = error instanceof Error ? error.message : 'Failed to revoke session'
    }
  } finally {
    revokingId.value = ''
  }
}

async function revokeOtherSessions(): Promise<void> {
  revokeOthersBusy.value = true
  sessionsError.value = ''
  try {
    const result = await api.revokeOtherSessions()
    revokeOthersOpen.value = false
    await loadSessions()
    toast.success(
      result.revoked === 1
        ? 'Signed out 1 other session.'
        : `Signed out ${result.revoked} other sessions.`,
      'Sessions',
    )
  } catch (error) {
    sessionsError.value = error instanceof Error ? error.message : 'Failed to revoke sessions'
  } finally {
    revokeOthersBusy.value = false
  }
}

onMounted(() => {
  hydrate()
  void loadSessions()
})

async function saveProfile(): Promise<void> {
  profileBusy.value = true
  profileError.value = ''
  try {
    const updated = await api.updateProfile({
      display_name: profile.display_name.trim(),
      email: profile.email.trim(),
      avatar_url: profile.avatar_url.trim(),
    })
    auth.setUser(updated)
    toast.success('Your profile has been saved.', 'Profile')
  } catch (error) {
    profileError.value = error instanceof Error ? error.message : 'Failed to save profile'
  } finally {
    profileBusy.value = false
  }
}

async function changePassword(): Promise<void> {
  passwordError.value = ''
  if (password.new_password.length < 10) {
    passwordError.value = 'The new password must be at least 10 characters.'
    return
  }
  if (password.new_password !== password.confirm_password) {
    passwordError.value = 'The new passwords do not match.'
    return
  }
  passwordBusy.value = true
  try {
    await api.changePassword(password.current_password, password.new_password)
    toast.success('Password changed. Sign in again with the new password.', 'Security')
    auth.logout()
    await router.replace({ name: 'login' })
  } catch (error) {
    passwordError.value = error instanceof Error ? error.message : 'Failed to change password'
  } finally {
    passwordBusy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-5xl px-4 py-8 sm:px-6 lg:px-8">
    <header>
      <p class="text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-600">Account</p>
      <h1 class="mt-2 text-2xl font-semibold tracking-tight text-slate-900">Profile &amp; security</h1>
    </header>

    <div class="mt-7 grid gap-5 lg:grid-cols-3">
      <section class="card p-6 lg:col-span-2">
        <div class="flex items-center gap-4">
          <img
            v-if="avatarPreview"
            :src="avatarPreview"
            alt="Profile avatar preview"
            class="h-16 w-16 rounded-2xl object-cover ring-2 ring-white"
          />
          <span
            v-else
            class="flex h-16 w-16 items-center justify-center rounded-2xl bg-indigo-100 text-lg font-bold text-indigo-700"
          >
            {{ auth.initials }}
          </span>
          <div>
            <p class="text-lg font-semibold text-slate-900">{{ profile.display_name || auth.displayName }}</p>
            <p class="text-sm text-slate-600">@{{ auth.user?.username }}</p>
            <span class="badge mt-2 bg-indigo-50 text-indigo-700 ring-indigo-200">{{ auth.roleLabel }}</span>
          </div>
        </div>

        <form class="mt-6 space-y-4" @submit.prevent="saveProfile">
          <label class="block">
            <span class="field-label">Display name</span>
            <input v-model="profile.display_name" class="field-input" maxlength="255" autocomplete="name" />
          </label>
          <label class="block">
            <span class="field-label">Email</span>
            <input v-model="profile.email" type="email" class="field-input" maxlength="255" autocomplete="email" />
          </label>
          <label class="block">
            <span class="field-label">Avatar URL</span>
            <input
              v-model="profile.avatar_url"
              class="field-input"
              maxlength="2048"
              placeholder="https://… or /assets/avatar.png"
              autocomplete="photo"
            />
            <span class="mt-1 block text-[11px] text-slate-500">Use a local or private-network image URL for offline installs.</span>
          </label>
          <p v-if="profileError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
            {{ profileError }}
          </p>
          <div class="flex justify-end">
            <button type="submit" class="btn-primary" :disabled="profileBusy">
              <AppIcon v-if="profileBusy" name="loader" :size="15" class="animate-spin" />
              Save profile
            </button>
          </div>
        </form>
      </section>

      <section class="card p-6">
        <h2 class="text-sm font-semibold text-slate-900">Change password</h2>
        <form class="mt-4 space-y-3" @submit.prevent="changePassword">
          <label class="block">
            <span class="field-label">Current password</span>
            <input v-model="password.current_password" type="password" class="field-input" autocomplete="current-password" required />
          </label>
          <label class="block">
            <span class="field-label">New password</span>
            <input v-model="password.new_password" type="password" class="field-input" autocomplete="new-password" required />
          </label>
          <label class="block">
            <span class="field-label">Confirm new password</span>
            <input v-model="password.confirm_password" type="password" class="field-input" autocomplete="new-password" required />
          </label>
          <p v-if="passwordError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
            {{ passwordError }}
          </p>
          <button type="submit" class="btn-secondary w-full justify-center" :disabled="passwordBusy">
            <AppIcon v-if="passwordBusy" name="loader" :size="15" class="animate-spin" />
            Update password
          </button>
        </form>
      </section>
    </div>

    <section class="card mt-5 p-6">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="flex items-center gap-2 text-sm font-semibold text-slate-900">
            <AppIcon name="shield" :size="16" class="text-indigo-600" />
            Active sessions
          </h2>
          <p class="mt-1 text-xs text-slate-500">
            Every device signed in to your account. Revoke anything you do not recognise.
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn-secondary" :disabled="sessionsBusy" @click="loadSessions">
            <AppIcon name="refresh" :size="15" :class="{ 'animate-spin': sessionsBusy }" />
            Refresh
          </button>
          <button
            type="button"
            class="btn-secondary text-red-600 hover:text-red-700"
            :disabled="sessionsBusy || revokeOthersBusy || otherSessions.length === 0"
            @click="revokeOthersOpen = true"
          >
            Revoke other sessions
          </button>
        </div>
      </div>

      <p v-if="sessionsError" class="mt-4 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700" role="alert">
        {{ sessionsError }}
      </p>

      <p v-if="sessionsBusy && sessions.length === 0" class="mt-4 text-sm text-slate-500">Loading sessions…</p>
      <p v-else-if="sessions.length === 0" class="mt-4 text-sm text-slate-500">No active sessions.</p>

      <ul v-else class="mt-4 divide-y divide-slate-100">
        <li
          v-for="session in sessions"
          :key="session.id"
          class="flex flex-wrap items-start justify-between gap-3 py-4"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <p class="text-sm font-medium text-slate-900">{{ session.device }}</p>
              <span
                v-if="session.current"
                class="badge bg-emerald-50 text-emerald-700 ring-emerald-200"
              >
                This device
              </span>
            </div>
            <p class="mt-1 text-xs text-slate-500">
              {{ session.ip || 'Unknown IP' }} · Last seen {{ lastSeenLabel(session.last_seen_at) }}
            </p>
            <p class="mt-0.5 text-[11px] text-slate-400" :title="session.user_agent">
              Signed in {{ formatWhen(session.created_at) }}
            </p>
          </div>
          <button
            v-if="!session.current"
            type="button"
            class="btn-secondary text-red-600 hover:text-red-700"
            :disabled="revokingId === session.id || revokeOthersBusy"
            @click="revokeSession(session)"
          >
            <AppIcon v-if="revokingId === session.id" name="loader" :size="15" class="animate-spin" />
            Revoke
          </button>
        </li>
      </ul>
    </section>

    <ConfirmDialog
      v-model:open="revokeOthersOpen"
      title="Revoke other sessions"
      :message="`This signs out ${otherSessions.length} other device${otherSessions.length === 1 ? '' : 's'}. Your current session stays signed in.`"
      confirm-label="Revoke others"
      :busy="revokeOthersBusy"
      @confirm="revokeOtherSessions"
    />
  </div>
</template>
