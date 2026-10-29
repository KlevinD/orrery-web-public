import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('agent memory sheet', () => {
  it('renders Agent memories as OpenClaw-style daily diary files', () => {
    const source = readFileSync('src/routes/evaluation/AgentMemorySheet.svelte', 'utf-8')

    expect(source).toContain('每日记忆日记')
    expect(source).toContain('groupMemoryDiaryRecords')
    expect(source).toContain('#lib/memory/diary')
    expect(source).toContain('日记文件')
    expect(source).toContain('MemoryCitationEvidenceList')
    expect(source).toContain('selectedRecord.citation_evidence')
  })
})
