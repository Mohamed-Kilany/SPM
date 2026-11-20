import { describe, expect, it } from 'vitest'
import { insertMention, mentionParts, mentionQueryAt } from './mentions'

describe('mentionParts', () => {
  it('marks mentions and leaves surrounding text untouched', () => {
    expect(mentionParts('Hi @alice, please ask @bob.dev.')).toEqual([
      { text: 'Hi ', mention: false },
      { text: '@alice', mention: true },
      { text: ', please ask ', mention: false },
      { text: '@bob.dev', mention: true },
      { text: '.', mention: false },
    ])
  })

  it('does not treat e-mail addresses as mentions', () => {
    expect(mentionParts('mail a@b.com')).toEqual([{ text: 'mail a@b.com', mention: false }])
  })

  it('keeps markup literal instead of interpreting it', () => {
    expect(mentionParts('<script>@alice</script>')).toEqual([
      { text: '<script>', mention: false },
      { text: '@alice', mention: true },
      { text: '</script>', mention: false },
    ])
  })

  it('returns the whole body when there are no mentions', () => {
    expect(mentionParts('plain text')).toEqual([{ text: 'plain text', mention: false }])
  })
})

describe('mentionQueryAt', () => {
  it('returns the active fragment', () => {
    expect(mentionQueryAt('hello @Ali', 10)).toEqual({ query: 'ali', start: 6 })
    expect(mentionQueryAt('@', 1)).toEqual({ query: '', start: 0 })
  })

  it('ignores fragments in the middle of a word', () => {
    expect(mentionQueryAt('mail@example', 12)).toBeNull()
    expect(mentionQueryAt('hello world', 11)).toBeNull()
  })
})

describe('insertMention', () => {
  it('replaces the typed fragment and places the caret after the space', () => {
    const result = insertMention('ask @ali about it', 8, 4, 'alice')
    expect(result.value).toBe('ask @alice  about it')
    expect(result.caret).toBe(11)
  })
})
