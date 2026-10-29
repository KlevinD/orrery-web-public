import { describe, expect, it } from 'vitest'
import { countMeaningfulMemorySources, groupMemoryDiaryRecords, isMemoryDiaryRecord, memoryDiaryDate, memoryDiaryPath } from './diary'
import type { MemoryRecordDTO } from '#lib/types/memory'

describe('memory diary helpers', () => {
  it('groups memory records by OpenClaw-style daily diary file', () => {
    const records = [
      record('memory-1', '2026-06-14T10:00:00Z', ['diary_date:2026-06-14', 'diary_path:memory/2026-06-14.md']),
      record('memory-2', '2026-06-14T12:00:00Z', ['diary_date:2026-06-14', 'diary_path:memory/2026-06-14.md']),
      record('memory-3', '2026-06-13T12:00:00Z', ['agent_diary']),
      record('memory-regular', '2026-06-12T12:00:00Z', [])
    ]

    expect(memoryDiaryDate(records[0])).toBe('2026-06-14')
    expect(memoryDiaryPath(records[0])).toBe('memory/2026-06-14.md')
    expect(memoryDiaryPath(records[2])).toBe('memory/2026-06-13.md')
    expect(isMemoryDiaryRecord(records[2])).toBe(true)
    expect(isMemoryDiaryRecord(records[3])).toBe(false)
    expect(groupMemoryDiaryRecords(records).map((day) => [day.path, day.records.map((item) => item.memory_id)])).toEqual([
      ['memory/2026-06-14.md', ['memory-2', 'memory-1']],
      ['memory/2026-06-13.md', ['memory-3']]
    ])
  })

  it('does not present an internal diary flush as corroborating evidence', () => {
    const item = record('memory-1', '2026-06-14T10:00:00Z', ['agent_diary'])
    item.source_memory_refs = ['agent_run_diary:diary-flush:internal', 'memory:confirmed-fact']
    item.source_evidence_refs = ['evidence:release-note']

    expect(countMeaningfulMemorySources(item)).toBe(2)
  })
})

function record(memoryId: string, createdAt: string, tags: string[]): MemoryRecordDTO {
  return {
    memory_id: memoryId,
    profile_id: 'memory-agent',
    candidate_id: `candidate-${memoryId}`,
    layer: 'fact',
    scope: { kind: 'agent', id: 'agent-1' },
    topic: '',
    summary: memoryId,
    tags,
    confidence: 1,
    visibility: { mode: 'owner' },
    sensitivity: 'normal',
    de_id_level: 'raw',
    lifecycle_status: 'active',
    created_by: 'agent-1',
    created_at: createdAt,
    updated_at: createdAt
  }
}
