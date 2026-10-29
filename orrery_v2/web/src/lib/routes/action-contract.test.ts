import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const routeFiles = [
  'src/routes/settings/+page.svelte',
  'src/routes/squads/+page.svelte'
]

describe('route action contract', () => {
  it('does not render enabled buttons without an action in migrated pages', () => {
    for (const file of routeFiles) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).not.toMatch(/<Button(?![^>]*(onclick|disabled=|disabled|href=))[^>]*>/)
      expect(source, file).not.toMatch(/<button(?![^>]*(onclick|disabled|aria-label|type="submit"))[^>]*>/)
    }
  })
})
