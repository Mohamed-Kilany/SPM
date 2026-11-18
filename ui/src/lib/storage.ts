export const TOKEN_KEY = 'spm_token'
export const USER_KEY = 'spm_user'

export function getStoredToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export function setStoredToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token)
  } catch {
    // Storage may be unavailable in hardened/private browser modes. The
    // in-memory session still works for the lifetime of the tab.
  }
}

export function clearStoredSession(): void {
  try {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
  } catch {
    // Ignore storage failures.
  }
}
