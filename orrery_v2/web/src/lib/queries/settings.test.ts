import { describe, expect, it } from 'vitest'
import { settingsQueryKeys } from './settings'

describe('settings queries', () => {
  it('uses a stable query key for user preferences', () => {
    expect(settingsQueryKeys.detail).toEqual(['settings'])
  })
})
