import type { DiscussionModelUsageDTO } from '#lib/types/discussions'

export function discussionRunTokenUsage(usage: Record<string, DiscussionModelUsageDTO> | undefined) {
  const entries = Object.values(usage ?? {})
  if (entries.length === 0) return undefined
  return {
    input: entries.reduce((sum, item) => sum + (item.input_tokens ?? 0), 0),
    output: entries.reduce((sum, item) => sum + (item.output_tokens ?? 0), 0),
    total: entries.reduce((sum, item) => sum + (item.total_tokens ?? ((item.input_tokens ?? 0) + (item.output_tokens ?? 0))), 0),
    cachedInput: entries.reduce((sum, item) => sum + (item.cached_input_tokens ?? 0), 0),
    reasoning: entries.reduce((sum, item) => sum + (item.reasoning_tokens ?? 0), 0),
    cachedInputKnown: entries.every((item) => item.cached_input_tokens_known === true),
    reasoningKnown: entries.every((item) => item.reasoning_tokens_known === true)
  }
}
