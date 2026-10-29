import { describe, expect, it } from 'vitest'
import { discussionRunTokenUsage } from './tokenUsage'

describe('discussion token usage coverage', () => {
  it('keeps cached and reasoning subsets out of totals', () => {
    expect(discussionRunTokenUsage({ model: {
      input_tokens: 1000, output_tokens: 100, total_tokens: 1100,
      cached_input_tokens: 800, reasoning_tokens: 40,
      cached_input_tokens_known: true, reasoning_tokens_known: true
    } })).toEqual({ input: 1000, output: 100, total: 1100, cachedInput: 800,
      reasoning: 40, cachedInputKnown: true, reasoningKnown: true })
  })

  it('distinguishes reported zero from legacy missing details', () => {
    expect(discussionRunTokenUsage({ model: {
      input_tokens: 100, cached_input_tokens_known: true, reasoning_tokens_known: true
    } })).toMatchObject({ cachedInput: 0, reasoning: 0, cachedInputKnown: true, reasoningKnown: true })
    expect(discussionRunTokenUsage({ model: { input_tokens: 100 } }))
      .toMatchObject({ cachedInput: 0, reasoning: 0, cachedInputKnown: false, reasoningKnown: false })
  })

  it('keeps partial counts but marks mixed model coverage incomplete in either order', () => {
    const known = { input_tokens: 1000, output_tokens: 100, total_tokens: 1100,
      cached_input_tokens: 800, reasoning_tokens: 40,
      cached_input_tokens_known: true, reasoning_tokens_known: true }
    const unknown = { input_tokens: 200, output_tokens: 20 }
    for (const usage of [{ known, unknown }, { unknown, known }]) {
      expect(discussionRunTokenUsage(usage)).toEqual({ input: 1200, output: 120,
        total: 1320, cachedInput: 800, reasoning: 40, cachedInputKnown: false, reasoningKnown: false })
    }
  })

  it('preserves a total-only model alongside an input/output model', () => {
    expect(discussionRunTokenUsage({ legacy: { total_tokens: 50 }, detailed: { input_tokens: 100, output_tokens: 20 } }))
      .toMatchObject({ input: 100, output: 20, total: 170 })
    expect(discussionRunTokenUsage(undefined)).toBeUndefined()
  })
})
