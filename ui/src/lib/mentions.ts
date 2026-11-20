export interface MentionPart {
  text: string
  mention: boolean
}

/** Split a plain-text body into text and @mention segments for safe rendering. */
export function mentionParts(body: string): MentionPart[] {
  const parts: MentionPart[] = []
  const pattern = /(^|[^A-Za-z0-9_.-])(@[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9_])?)/g
  let cursor = 0
  let match: RegExpExecArray | null
  while ((match = pattern.exec(body)) !== null) {
    const prefix = match[1] ?? ''
    const token = match[2] ?? ''
    const start = match.index + prefix.length
    if (start > cursor) parts.push({ text: body.slice(cursor, start), mention: false })
    parts.push({ text: token, mention: true })
    cursor = start + token.length
  }
  if (cursor < body.length) parts.push({ text: body.slice(cursor), mention: false })
  return parts.length > 0 ? parts : [{ text: body, mention: false }]
}

/**
 * Return the username fragment being typed at the caret, or null when the
 * caret is not inside an @mention. Only whitespace or the start of the value
 * may precede the `@`, so e-mail addresses do not open the picker.
 */
export function mentionQueryAt(value: string, caret: number): { query: string; start: number } | null {
  const before = value.slice(0, Math.max(0, caret))
  const match = before.match(/(?:^|\s)@([A-Za-z0-9_.-]*)$/)
  if (!match) return null
  const query = match[1] ?? ''
  return { query: query.toLowerCase(), start: before.length - query.length - 1 }
}

/** Replace the active mention fragment with a chosen username. */
export function insertMention(value: string, caret: number, start: number, username: string): { value: string; caret: number } {
  const before = value.slice(0, start)
  const after = value.slice(Math.max(caret, start))
  const next = `${before}@${username} ${after}`
  return { value: next, caret: before.length + username.length + 2 }
}
