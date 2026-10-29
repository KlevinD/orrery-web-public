import { act, cleanup, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import WorkforceAvatar from './WorkforceAvatar.svelte'

const apiMocks = vi.hoisted(() => ({
  getWorkforceAvatar: vi.fn()
}))

vi.mock('#lib/api/operations', () => ({
  operationsApi: {
    getWorkforceAvatar: apiMocks.getWorkforceAvatar
  }
}))

describe('WorkforceAvatar', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('marks a human avatar with a readable identity', () => {
    setLocale('zh-CN')
    const { container } = render(WorkforceAvatar, {
      props: { name: 'Mira Chen', human: true, class: 'h-10 w-10 rounded-full' }
    })

    const avatar = screen.getByRole('img', { name: 'Mira Chen · 真人' })
    expect(avatar.getAttribute('data-workforce-kind')).toBe('human')
    expect(avatar.classList.contains('workforce-avatar')).toBe(true)
    expect(container.querySelector('[data-workforce-identity="human"]')).toBeTruthy()
  })

  it('marks an agent avatar with a readable identity', () => {
    setLocale('zh-CN')
    const { container } = render(WorkforceAvatar, {
      props: { name: '市场研究分析师', class: 'h-10 w-10 rounded-full' }
    })

    const avatar = screen.getByRole('img', { name: '市场研究分析师 · 智能体' })
    expect(avatar.getAttribute('data-workforce-kind')).toBe('agent')
    expect(avatar.classList.contains('workforce-avatar')).toBe(true)
    expect(container.querySelector('[data-workforce-identity="agent"]')).toBeTruthy()
  })

  it('loads the member avatar only after it nears the viewport', async () => {
    let callback: IntersectionObserverCallback | undefined
    const disconnect = vi.fn()
    vi.stubGlobal('IntersectionObserver', class {
      root = null
      rootMargin = '200px'
      thresholds = [0]

      constructor(next: IntersectionObserverCallback) {
        callback = next
      }

      observe() {}
      unobserve() {}
      disconnect() {
        disconnect()
      }
      takeRecords() {
        return []
      }
    })
    apiMocks.getWorkforceAvatar.mockResolvedValue(new Blob(['avatar'], { type: 'image/png' }))
    render(WorkforceAvatar, {
      props: { profileId: 'profile-lazy', name: 'Lazy', human: true }
    })

    expect(apiMocks.getWorkforceAvatar).not.toHaveBeenCalled()
    const target = screen.getByRole('img', { name: 'Lazy · 真人' })
    await act(() => {
      callback?.([{ isIntersecting: true, target } as unknown as IntersectionObserverEntry], {} as IntersectionObserver)
    })

    await waitFor(() => expect(apiMocks.getWorkforceAvatar).toHaveBeenCalledTimes(1))
    expect(disconnect).toHaveBeenCalled()
  })
})
