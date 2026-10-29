import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clearApiBaseUrlForTests,
  resolveApiBaseUrl,
  setDesktopBackendAdapterForTests,
  waitForApiBaseUrl
} from './api-base'

describe('runtime API base URL resolution', () => {
  beforeEach(() => {
    clearApiBaseUrlForTests()
    setDesktopBackendAdapterForTests(undefined)
  })

  it('uses same-origin API paths for web mode without an explicit base URL', async () => {
    await expect(resolveApiBaseUrl({ desktop: false, envBaseUrl: '' })).resolves.toBe('')
    expect(waitForApiBaseUrl()).toBe('')
  })

  it('normalizes explicit web API base URLs', async () => {
    await expect(resolveApiBaseUrl({ desktop: false, envBaseUrl: ' http://127.0.0.1:8080/ ' })).resolves.toBe(
      'http://127.0.0.1:8080'
    )
  })

  it('starts the local backend and uses its port in desktop mode', async () => {
    const startLocalBackend = vi.fn().mockResolvedValue(49152)
    setDesktopBackendAdapterForTests({ startLocalBackend })

    await expect(resolveApiBaseUrl({ desktop: true, envBaseUrl: '' })).resolves.toBe('http://127.0.0.1:49152')
    expect(startLocalBackend).toHaveBeenCalledTimes(1)
    expect(waitForApiBaseUrl()).toBe('http://127.0.0.1:49152')
  })
})
