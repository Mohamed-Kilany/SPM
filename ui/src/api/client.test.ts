import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from './client'
import {
  REFRESH_TOKEN_KEY,
  SESSION_ID_KEY,
  TOKEN_KEY,
  USER_KEY,
} from '../lib/storage'

const user = {
  id: 'user-1',
  username: 'admin',
  email: 'admin@example.com',
  display_name: 'Admin',
  avatar_url: '',
  role: 'admin',
  project_ids: [],
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function unauthorized(code: string): Response {
  return jsonResponse(401, { error: { code, message: 'Unauthorized' } })
}

function loginPayload(token: string, refreshToken: string): unknown {
  return {
    token,
    refresh_token: refreshToken,
    session_id: 'session-1',
    expires_in: 900,
    user,
  }
}

describe('API client session refresh', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem(TOKEN_KEY, 'expired-token')
    localStorage.setItem(REFRESH_TOKEN_KEY, 'refresh-1')
    localStorage.setItem(SESSION_ID_KEY, 'session-1')
    localStorage.setItem(USER_KEY, JSON.stringify(user))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('rotates the refresh token once for concurrent 401s and retries', async () => {
    let refreshCalls = 0
    const refreshed = vi.fn()
    window.addEventListener('spm:token-refreshed', refreshed)

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const authorization = new Headers(init?.headers).get('Authorization')
        if (url.endsWith('/api/v1/auth/refresh')) {
          refreshCalls += 1
          expect(await new Response(init?.body as string).json()).toEqual({
            refresh_token: 'refresh-1',
          })
          return jsonResponse(200, loginPayload('fresh-token', 'refresh-2'))
        }
        if (url.endsWith('/api/v1/auth/me')) {
          return authorization === 'Bearer fresh-token'
            ? jsonResponse(200, user)
            : unauthorized('SESSION_EXPIRED')
        }
        throw new Error(`unexpected request: ${url}`)
      }),
    )

    const [first, second] = await Promise.all([api.me(), api.me()])

    expect(first.id).toBe('user-1')
    expect(second.id).toBe('user-1')
    expect(refreshCalls).toBe(1)
    expect(refreshed).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem(TOKEN_KEY)).toBe('fresh-token')
    expect(localStorage.getItem(REFRESH_TOKEN_KEY)).toBe('refresh-2')
    expect(localStorage.getItem(SESSION_ID_KEY)).toBe('session-1')

    window.removeEventListener('spm:token-refreshed', refreshed)
  })

  it('clears the session and emits unauthorized when refresh is rejected', async () => {
    const unauthorizedEvent = vi.fn()
    window.addEventListener('spm:unauthorized', unauthorizedEvent)

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/v1/auth/refresh')) return unauthorized('INVALID_REFRESH_TOKEN')
        return unauthorized('SESSION_EXPIRED')
      }),
    )

    await expect(api.me()).rejects.toBeInstanceOf(ApiError)

    expect(localStorage.getItem(TOKEN_KEY)).toBeNull()
    expect(localStorage.getItem(REFRESH_TOKEN_KEY)).toBeNull()
    expect(localStorage.getItem(SESSION_ID_KEY)).toBeNull()
    expect(localStorage.getItem(USER_KEY)).toBeNull()
    expect(unauthorizedEvent).toHaveBeenCalledTimes(1)

    window.removeEventListener('spm:unauthorized', unauthorizedEvent)
  })

  it('keeps the session when the refresh request fails transiently', async () => {
    const unauthorizedEvent = vi.fn()
    window.addEventListener('spm:unauthorized', unauthorizedEvent)

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/v1/auth/refresh')) {
          return jsonResponse(503, { error: { code: 'UNAVAILABLE', message: 'Try later' } })
        }
        return unauthorized('SESSION_EXPIRED')
      }),
    )

    const failure = await api.me().catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(ApiError)
    expect((failure as ApiError).status).toBe(503)

    expect(localStorage.getItem(REFRESH_TOKEN_KEY)).toBe('refresh-1')
    expect(unauthorizedEvent).not.toHaveBeenCalled()

    window.removeEventListener('spm:unauthorized', unauthorizedEvent)
  })
})
