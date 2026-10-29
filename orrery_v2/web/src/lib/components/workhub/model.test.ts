import { describe, expect, it } from 'vitest'
import { allowedIdeaStatuses, canConvertIdea } from './model'

describe('work hub idea lifecycle model', () => {
  it('reserves converted for the dedicated conversion command', () => {
    for (const status of ['submitted', 'under_review', 'accepted', 'experiment'] as const) {
      expect(allowedIdeaStatuses(status)).not.toContain('converted')
    }
  })

  it('only converts approved ideas and keeps converted ideas terminal', () => {
    expect(canConvertIdea('accepted')).toBe(true)
    expect(canConvertIdea('experiment')).toBe(true)
    expect(canConvertIdea('submitted')).toBe(false)
    expect(allowedIdeaStatuses('converted')).toEqual(['converted'])
  })
})
