import type { MemoryRecordDTO } from '#lib/types/memory'

export interface MemoryDiaryDay {
  date: string
  path: string
  records: MemoryRecordDTO[]
}

const diaryTags = new Set(['agent_diary', 'daily_note', 'daily_diary'])

export function isMemoryDiaryRecord(record: MemoryRecordDTO) {
  if (/^memory\/\d{4}-\d{2}-\d{2}\.md$/i.test(record.topic?.trim() ?? '')) return true
  return (record.tags ?? []).some((tag) => {
    const normalized = tag.trim().toLowerCase()
    return diaryTags.has(normalized) || normalized.startsWith('diary_date:') || normalized.startsWith('diary_path:')
  })
}

export function countMeaningfulMemorySources(record: MemoryRecordDTO) {
  const memorySources = (record.source_memory_refs ?? []).filter((ref) => !ref.trim().toLowerCase().startsWith('agent_run_diary:'))
  return memorySources.length
    + (record.source_evidence_refs?.length ?? 0)
    + (record.citation_evidence?.length ?? 0)
}

export function groupMemoryDiaryRecords(items: MemoryRecordDTO[]): MemoryDiaryDay[] {
  const groups = new Map<string, MemoryDiaryDay>()
  for (const record of items.filter(isMemoryDiaryRecord)) {
    const date = memoryDiaryDate(record)
    const path = memoryDiaryPath(record, date)
    const existing = groups.get(date)
    if (existing) {
      existing.records.push(record)
    } else {
      groups.set(date, { date, path, records: [record] })
    }
  }
  return Array.from(groups.values())
    .map((day) => ({
      ...day,
      records: [...day.records].sort((a, b) => b.created_at.localeCompare(a.created_at))
    }))
    .sort((a, b) => b.date.localeCompare(a.date))
}

export function memoryDiaryDate(record: MemoryRecordDTO) {
  const tagDate = record.tags?.find((tag) => tag.startsWith('diary_date:'))?.slice('diary_date:'.length)
  if (tagDate) return tagDate
  const topicDate = record.topic?.match(/memory\/(\d{4}-\d{2}-\d{2})\.md/)?.[1]
  if (topicDate) return topicDate
  const date = new Date(record.created_at)
  return Number.isNaN(date.getTime()) ? 'unknown-date' : date.toISOString().slice(0, 10)
}

export function memoryDiaryPath(record: MemoryRecordDTO, date = memoryDiaryDate(record)) {
  const tagPath = record.tags?.find((tag) => tag.startsWith('diary_path:'))?.slice('diary_path:'.length)
  if (tagPath) return tagPath
  if (record.topic?.startsWith('memory/')) return record.topic
  return `memory/${date}.md`
}
