import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import ImageGenerationSettingsPanel from './ImageGenerationSettingsPanel.svelte'
import type { ImageGenerationSettingsDTO } from '#lib/types/api/image-generation'

const settings: ImageGenerationSettingsDTO = { enabled: false, protocol: 'openai-compatible', base_url: 'https://gateway.example/v1', model: 'image2', api_key_ref: 'secret://image/provider' }
const result = { status: 'ok' as const, reason: 'model_listed' as const, model_available: true, generation_verified: false, latency_ms: 12 }
const setup = (props = {}) => {
  const onsave = vi.fn(async (value: ImageGenerationSettingsDTO) => value)
  const ontest = vi.fn(async () => result)
  return { ...render(ImageGenerationSettingsPanel, { props: { settings, onsave, ontest, ...props } }), onsave, ontest }
}

beforeEach(() => setLocale('en-US'))
afterEach(cleanup)

describe('ImageGenerationSettingsPanel', () => {
  it('saves an independent normalized config and does not test unsaved changes', async () => {
    const { onsave, ontest } = setup()
    const gateway = await screen.findByLabelText('Gateway API URL')
    await fireEvent.input(gateway, { target: { value: 'https://new.example/v1/' } })
    expect((screen.getByRole('button', { name: 'Check connection' }) as HTMLButtonElement).disabled).toBe(true)
    await fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(onsave).toHaveBeenCalledWith({ ...settings, base_url: 'https://new.example/v1' }))
    expect(ontest).not.toHaveBeenCalled()
  })

  it('shows catalog success without claiming generation is verified, and hides stale results', async () => {
    const { ontest } = setup()
    await fireEvent.click(await screen.findByRole('button', { name: 'Check connection' }))
    expect(await screen.findByText('Gateway connected and model listed')).toBeTruthy()
    expect(screen.getByText('Actual image generation has not been verified')).toBeTruthy()
    expect(ontest).toHaveBeenCalledOnce()
    await fireEvent.input(screen.getByLabelText('Gateway API URL'), { target: { value: 'https://new.example/v1' } })
    expect(screen.queryByText('Gateway connected and model listed')).toBeNull()
  })

  it('disables mutation controls for read-only users', async () => {
    const { onsave, ontest } = setup({ readOnly: true })
    const input = await screen.findByLabelText('Gateway API URL')
    expect((input.closest('fieldset') as HTMLFieldSetElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Check connection' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /^Save$/ }) as HTMLButtonElement).disabled).toBe(true)
    expect(onsave).not.toHaveBeenCalled()
    expect(ontest).not.toHaveBeenCalled()
  })

  it('keeps unsaved changes on save failure and supports reset', async () => {
    setup({ onsave: vi.fn().mockRejectedValue(new Error('sensitive-upstream-detail')) })
    const gateway = await screen.findByLabelText('Gateway API URL') as HTMLInputElement
    await fireEvent.input(gateway, { target: { value: 'https://new.example/v1' } })
    await fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(gateway.value).toBe('https://new.example/v1')
    expect(screen.queryByText('sensitive-upstream-detail')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Discard unsaved changes' }))
    expect(gateway.value).toBe(settings.base_url)
  })

  it('renders loading and retry states without editable fields', async () => {
    const onretry = vi.fn()
    const { rerender } = setup({ loading: true, onretry })
    expect(screen.getByText('Loading image settings')).toBeTruthy()
    expect(screen.queryByLabelText('Gateway API URL')).toBeNull()
    await rerender({ settings, onsave: vi.fn(), ontest: vi.fn(), loading: false, loadFailed: true, onretry })
    expect(screen.getByRole('alert')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onretry).toHaveBeenCalledOnce()
  })

  it('requires complete settings when enabling the service', async () => {
    setup({ settings: { ...settings, model: '', base_url: '', api_key_ref: '' } })
    await fireEvent.click(await screen.findByRole('checkbox'))
    expect((screen.getByRole('button', { name: /^Save$/ }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Check connection' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('preserves drafts when settings refresh and resets to the latest saved values', async () => {
    const { rerender, onsave, ontest } = setup()
    const gateway = await screen.findByLabelText('Gateway API URL') as HTMLInputElement
    await fireEvent.input(gateway, { target: { value: 'https://draft.example/v1' } })
    await rerender({ settings: { ...settings, base_url: 'https://updated.example/v1' }, onsave, ontest })
    expect(gateway.value).toBe('https://draft.example/v1')
    await fireEvent.click(screen.getByRole('button', { name: 'Discard unsaved changes' }))
    expect(gateway.value).toBe('https://updated.example/v1')
  })
})
