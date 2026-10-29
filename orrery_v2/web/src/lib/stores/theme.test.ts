import { beforeEach, describe, expect, it, vi } from 'vitest'

describe('theme store', () => {
  beforeEach(() => {
    vi.resetModules()
    localStorage.clear()
    document.documentElement.className = ''
    document.documentElement.removeAttribute('style')
  })

  it('defaults first-time sessions to the light workbench theme', async () => {
    const { theme } = await import('./theme.svelte')

    expect(theme.mode).toBe('light')
    expect(document.documentElement.classList.contains('light')).toBe(true)
    expect(document.documentElement.style.colorScheme).toBe('light')
  })
})
