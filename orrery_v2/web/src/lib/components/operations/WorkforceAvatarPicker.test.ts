import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import WorkforceAvatarPicker from './WorkforceAvatarPicker.svelte'

const mocks = vi.hoisted(() => ({
  getWorkforceAvatar: vi.fn(),
  getWorkforceAvatarDefault: vi.fn(),
  listWorkforceAvatarDefaults: vi.fn()
}))

vi.mock('#lib/api/operations', () => ({
  operationsApi: {
    getWorkforceAvatar: mocks.getWorkforceAvatar,
    getWorkforceAvatarDefault: mocks.getWorkforceAvatarDefault,
    listWorkforceAvatarDefaults: mocks.listWorkforceAvatarDefaults
  }
}))

describe('WorkforceAvatarPicker', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    setLocale('en-US')
    mocks.getWorkforceAvatar.mockRejectedValue(new Error('No custom avatar'))
    mocks.getWorkforceAvatarDefault.mockResolvedValue(new Blob(['avatar'], { type: 'image/jpeg' }))
    mocks.listWorkforceAvatarDefaults.mockResolvedValue([
      {
        id: 'warm-dawn',
        avatar_url: '/api/workforce/avatar-defaults/warm-dawn',
        content_type: 'image/jpeg',
        size: 6,
        etag: 'avatar-etag'
      }
    ])
  })

  afterEach(() => cleanup())

  it('shows image-only defaults with accessible names', async () => {
    const ondefaultselect = vi.fn()
    render(WorkforceAvatarPicker, {
      props: {
        profileId: 'human-alice',
        name: 'Alice',
        human: true,
        ondefaultselect
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Change avatar' }))

    const choice = await screen.findByRole('button', { name: 'Warm Dawn' })
    expect(choice.getAttribute('title')).toBe('Warm Dawn')
    const image = within(choice).getByRole('img', { name: 'Warm Dawn' })
    expect(image.getAttribute('src')).toBe('/api/workforce/avatar-defaults/warm-dawn')
    expect(mocks.getWorkforceAvatarDefault).not.toHaveBeenCalled()
    expect(choice.textContent).not.toContain('Warm Dawn')

    await fireEvent.click(choice)
    expect(ondefaultselect).toHaveBeenCalledWith('warm-dawn')
  })
})
