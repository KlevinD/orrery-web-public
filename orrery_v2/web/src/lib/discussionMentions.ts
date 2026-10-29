export interface DiscussionMentionLink {
  kind: string
  id: string
  label: string
  start: number
  end: number
}

const mentionScheme = 'mention://'

export function parseDiscussionMentionLinks(value: string): DiscussionMentionLink[] {
  const links: DiscussionMentionLink[] = []
  let cursor = 0
  while (cursor < value.length) {
    const start = value.indexOf('[', cursor)
    if (start < 0) break
    const labelEnd = value.indexOf('](', start + 1)
    if (labelEnd < 0) break
    const nestedStart = value.lastIndexOf('[', labelEnd)
    const linkStart = nestedStart > start ? nestedStart : start
    const targetStart = labelEnd + 2
    if (!value.startsWith(mentionScheme, targetStart)) {
      cursor = labelEnd + 1
      continue
    }
    const targetEnd = value.indexOf(')', targetStart)
    if (targetEnd < 0) break
    const target = value.slice(targetStart + mentionScheme.length, targetEnd)
    const slash = target.indexOf('/')
    const kind = slash > 0 ? target.slice(0, slash) : ''
    const id = slash > 0 ? target.slice(slash + 1).trim() : ''
    const rawLabel = value.slice(linkStart + 1, labelEnd).trim()
    const label = rawLabel.startsWith('@') ? rawLabel.slice(1) : rawLabel
    if (validMentionKind(kind) && validMentionId(id) && label) {
      links.push({ kind, id, label, start: linkStart, end: targetEnd + 1 })
    }
    cursor = targetEnd + 1
  }
  links.push(...parseBareDiscussionMentionLinks(value, links))
  return links.sort((left, right) => left.start - right.start)
}

function parseBareDiscussionMentionLinks(value: string, markdownLinks: DiscussionMentionLink[]) {
  const links: DiscussionMentionLink[] = []
  let cursor = 0
  while (cursor < value.length) {
    const start = value.indexOf(mentionScheme, cursor)
    if (start < 0) break
    if (markdownLinks.some((link) => start >= link.start && start < link.end) || value.slice(Math.max(0, start - 2), start) === '](') {
      cursor = start + mentionScheme.length
      continue
    }
    const targetStart = start + mentionScheme.length
    const slash = value.indexOf('/', targetStart)
    if (slash <= targetStart) {
      cursor = targetStart
      continue
    }
    const kind = value.slice(targetStart, slash)
    let end = slash + 1
    while (end < value.length && !bareMentionTerminator(value[end])) end += 1
    const id = value.slice(slash + 1, end).trim()
    if (validMentionKind(kind) && validMentionId(id)) {
      links.push({ kind, id, label: id, start, end })
    }
    cursor = Math.max(end, start + mentionScheme.length)
  }
  return links
}

function bareMentionTerminator(char: string) {
  return /\s/.test(char) || ',.;!?)]}>\"\'，。；！？）】》'.includes(char)
}

export function replaceDiscussionMentionLinks(
  value: string,
  replacement: (link: DiscussionMentionLink, index: number) => string
) {
  const links = parseDiscussionMentionLinks(value)
  if (links.length === 0) return value
  let output = ''
  let cursor = 0
  links.forEach((link, index) => {
    output += value.slice(cursor, link.start)
    output += replacement(link, index)
    cursor = link.end
  })
  return output + value.slice(cursor)
}

export function renderPlainDiscussionMentions(
  value: string,
  displayLabel: (link: DiscussionMentionLink) => string = (link) => link.label
) {
  return replaceDiscussionMentionLinks(value, (link) => `@${displayLabel(link)}`)
}

function validMentionKind(kind: string) {
  if (!kind) return false
  for (const char of kind) {
    const code = char.charCodeAt(0)
    const asciiLetter = (code >= 65 && code <= 90) || (code >= 97 && code <= 122)
    if (!asciiLetter && char !== '_') return false
  }
  return true
}

function validMentionId(id: string) {
  if (!id) return false
  for (const char of id) {
    if (char.trim() === '' || char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127) return false
  }
  return true
}
