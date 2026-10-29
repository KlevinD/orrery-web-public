export function issueNumber(id: string) {
  const normalized = (id || '').trim()
  let hash = 0
  for (let index = 0; index < normalized.length; index += 1) {
    hash = (hash * 31 + normalized.charCodeAt(index)) % 90000
  }
  return `#${String(hash + 10000).padStart(5, '0')}`
}
