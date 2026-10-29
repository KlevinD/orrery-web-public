import { describe, expect, it } from 'vitest'
import { formatList, formatTaskEvidenceLabel } from './workspaceDisplay'

describe('mission workspace display helpers', () => {
  it('keeps list fallback empty so callers can render richer backend summaries', () => {
    expect(formatList([])).toBe('')
    expect(formatList(['spec', 'qa-log'])).toBe('spec, qa-log')
  })

  it('falls back to backend evidence count when a task has no explicit evidence requirements', () => {
    expect(formatTaskEvidenceLabel([], 3, (count) => `${count} evidence items`)).toBe('3 evidence items')
    expect(formatTaskEvidenceLabel(['spec'], 3, (count) => `${count} evidence items`)).toBe('spec')
  })
})
