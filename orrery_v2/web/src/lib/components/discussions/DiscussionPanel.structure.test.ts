import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('DiscussionPanel test structure', () => {
  it('keeps debug drawer tests outside the main panel behavior suite', () => {
    const directory = 'src/lib/components/discussions'
    const source = readFileSync(`${directory}/DiscussionPanel.test.ts`, 'utf-8')
    const suites = [
      'DiscussionPanel.test.ts',
      'DiscussionPanel.behavior.test.ts',
      'DiscussionPanel.composer-and-errors.test.ts'
    ]

    for (const suite of suites) {
      expect(readFileSync(`${directory}/${suite}`, 'utf-8').split(/\r?\n/).length, suite).toBeLessThanOrEqual(800)
    }
    expect(existsSync(`${directory}/DiscussionPanel.debug.test.ts`)).toBe(true)
    expect(existsSync(`${directory}/DiscussionPanel.fixtures.ts`)).toBe(true)
    expect(source).not.toContain('opens and renders the captured final prompt debug drawer')
    expect(source).not.toContain('function debugPrompt')
  })
})
