<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import { api } from '../api/client'
import { useAuthStore } from '../stores/authStore'
import { useToastStore } from '../stores/toastStore'

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

const avatarPreview = computed(() => profile.avatar_url.trim())

function hydrate(): void {
  profile.display_name = auth.user?.display_name || auth.user?.username || ''
  profile.email = auth.user?.email || ''
  profile.avatar_url = auth.user?.avatar_url || ''
}

onMounted(hydrate)

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
  </div>
</template>
