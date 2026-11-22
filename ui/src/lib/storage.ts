export const TOKEN_KEY = 'spm_token'
export const USER_KEY = 'spm_user'
export const REFRESH_TOKEN_KEY = 'spm_refresh_token'
export const SESSION_ID_KEY = 'spm_session_id'

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

export function getStoredRefreshToken(): string | null {
  try {
    return localStorage.getItem(REFRESH_TOKEN_KEY)
  } catch {
    return null
  }
}

export function setStoredRefreshToken(token: string): void {
  try {
    localStorage.setItem(REFRESH_TOKEN_KEY, token)
  } catch {
    // Ignore storage failures; the in-memory flow keeps the tab signed in.
  }
}

export function getStoredSessionId(): string | null {
  try {
    return localStorage.getItem(SESSION_ID_KEY)
  } catch {
    return null
  }
}

export function setStoredSessionId(sessionId: string): void {
  try {
    localStorage.setItem(SESSION_ID_KEY, sessionId)
  } catch {
    // Ignore storage failures.
  }
}

export function clearStoredSession(): void {
  try {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
    localStorage.removeItem(REFRESH_TOKEN_KEY)
    localStorage.removeItem(SESSION_ID_KEY)
  } catch {
    // Ignore storage failures.
  }
}
