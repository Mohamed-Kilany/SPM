import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { AuthUser, ProjectAccess } from '../types/project'
import {
  clearStoredSession,
  getStoredToken,
  setStoredToken,
  USER_KEY,
} from '../lib/storage'

interface TokenClaims {
  sub?: string
  role?: string
  project_ids?: string[]
  exp?: number
  iat?: number
}

function decodeToken(token: string): TokenClaims | null {
  try {
    const parts = token.split('.')
    if (parts.length < 2) return null
    const payload = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const padded = payload.padEnd(Math.ceil(payload.length / 4) * 4, '=')
    const bytes = Uint8Array.from(atob(padded), (char) => char.charCodeAt(0))
    return JSON.parse(new TextDecoder().decode(bytes)) as TokenClaims
  } catch {
    return null
  }
}

function readStoredUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(USER_KEY)
    return raw ? (JSON.parse(raw) as AuthUser) : null
  } catch {
    return null
  }
}

function storeUser(user: AuthUser): void {
  try {
    localStorage.setItem(USER_KEY, JSON.stringify(user))
  } catch {
    // Non-fatal: the in-memory session remains usable.
  }
}

function userFromClaims(claims: TokenClaims): AuthUser {
  return {
    id: claims.sub ?? '',
    username: claims.sub ? `User ${claims.sub.slice(0, 8)}` : 'Account',
    email: '',
    display_name: '',
    avatar_url: '',
    role: claims.role ?? 'viewer',
    project_ids: claims.project_ids ?? [],
    projects: [],
  }
}

export const useAuthStore = defineStore('auth', () => {
  const initialToken = getStoredToken()
  const initialClaims = initialToken ? decodeToken(initialToken) : null
  const tokenExpired =
    !!initialClaims?.exp && initialClaims.exp * 1000 <= Date.now()

  if (tokenExpired) clearStoredSession()

  const token = ref<string | null>(tokenExpired ? null : initialToken)
  const user = ref<AuthUser | null>(
    tokenExpired ? null : (readStoredUser() ?? (initialClaims ? userFromClaims(initialClaims) : null)),
  )
  const initialized = ref(false)

  const isAuthenticated = computed(() => !!token.value)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const isViewer = computed(() => user.value?.role === 'viewer')
  const canCreateProjects = computed(
    () => user.value?.role === 'admin' || user.value?.role === 'member',
  )
  const displayName = computed(
    () => user.value?.display_name || user.value?.username || 'Account',
  )
  const avatarUrl = computed(() => user.value?.avatar_url || '')
  const initials = computed(() => {
    const name = displayName.value.trim()
    if (!name) return 'SP'
    const words = name.split(/\s+/).filter(Boolean)
    if (words.length > 1) {
      return `${words[0]![0] ?? ''}${words[1]![0] ?? ''}`.toUpperCase()
    }
    return name.slice(0, 2).toUpperCase()
  })
  const roleLabel = computed(() => {
    const role = user.value?.role ?? 'viewer'
    return role.charAt(0).toUpperCase() + role.slice(1)
  })

  function applySession(nextToken: string, nextUser: AuthUser): void {
    token.value = nextToken
    user.value = nextUser
    setStoredToken(nextToken)
    storeUser(nextUser)
  }

  async function login(username: string, password: string): Promise<AuthUser> {
    const response = await api.login(username, password)
    applySession(response.token, response.user)
    initialized.value = true
    return response.user
  }

  async function refreshUser(): Promise<AuthUser | null> {
    if (!token.value) {
      initialized.value = true
      return null
    }
    try {
      const fresh = await api.me()
      user.value = fresh
      storeUser(fresh)
      initialized.value = true
      return fresh
    } catch (error) {
      initialized.value = true
      throw error
    }
  }

  function setUser(next: AuthUser): void {
    user.value = next
    storeUser(next)
  }

  function projectAccess(projectId: string): ProjectAccess | null {
    return user.value?.projects?.find((project) => project.project_id === projectId) ?? null
  }

  function projectRole(projectId: string): string {
    return projectAccess(projectId)?.role ?? ''
  }

  /**
   * Checks a capability resolved by the API on /auth/me. Global admins keep
   * break-glass access; everyone else must have the capability for this
   * project, so a stale JWT project list cannot grant a write.
   */
  function can(projectId: string, capability: string): boolean {
    if (!user.value) return false
    if (user.value.role === 'admin') return true
    return projectAccess(projectId)?.capabilities?.includes(capability) ?? false
  }

  function hasProjectAccess(projectId: string): boolean {
    if (!user.value) return false
    if (user.value.role === 'admin') return true
    if (user.value.projects) {
      return user.value.projects.some((project) => project.project_id === projectId)
    }
    return user.value.project_ids?.includes(projectId) ?? false
  }

  function canWrite(projectId?: string): boolean {
    if (projectId) return can(projectId, 'work_item.write')
    return user.value?.role === 'admin' || user.value?.role === 'member'
  }

  function logout(): void {
    token.value = null
    user.value = null
    initialized.value = true
    clearStoredSession()
  }

  return {
    token,
    user,
    initialized,
    isAuthenticated,
    isAdmin,
    isViewer,
    canCreateProjects,
    displayName,
    avatarUrl,
    initials,
    roleLabel,
    login,
    refreshUser,
    setUser,
    projectAccess,
    projectRole,
    can,
    hasProjectAccess,
    canWrite,
    logout,
  }
})
