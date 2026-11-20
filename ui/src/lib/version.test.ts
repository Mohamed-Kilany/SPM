import { describe, expect, it } from 'vitest'
import { displayVersion } from './version'

describe('displayVersion', () => {
  it('falls back to dev when no build version is present', () => {
    expect(displayVersion(undefined)).toBe('dev')
    expect(displayVersion('')).toBe('dev')
    expect(displayVersion('   ')).toBe('dev')
  })

  it('strips a leading v and trims whitespace', () => {
    expect(displayVersion('v0.3.1')).toBe('0.3.1')
    expect(displayVersion(' 1.2.3 ')).toBe('1.2.3')
  })

  it('keeps prerelease suffixes', () => {
    expect(displayVersion('v0.3.0-rc.1')).toBe('0.3.0-rc.1')
  })
})
