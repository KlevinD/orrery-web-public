import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { GatewayFeishuAccountViewModel } from '#lib/types/view-models'
import FeishuAccountsEditor from './FeishuAccountsEditor.svelte'

const mainAccount: GatewayFeishuAccountViewModel = {
  enabled: true,
  app_id: 'main-app',
  app_secret: '********',
  encrypt_key: '********',
  verify_token: '********',
  default_session_label: 'general',
  connection_mode: 'stream',
  domain_name: 'feishu'
}

describe('FeishuAccountsEditor', () => {
  it('adds a named account and selects that alias for QR registration', async () => {
    setLocale('en-US')
    const onaccountschange = vi.fn()
    const onregister = vi.fn()
    render(FeishuAccountsEditor, {
      accounts: {},
      onaccountschange,
      onregister
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Add account' }))
    await fireEvent.input(screen.getByLabelText('Account alias'), {
      target: { value: 'jordan-belfort' }
    })
    await fireEvent.input(screen.getByLabelText('App ID'), {
      target: { value: 'cli-jordan' }
    })
    await fireEvent.input(screen.getByLabelText('App Secret'), {
      target: { value: 'secret-jordan' }
    })
    await fireEvent.click(screen.getByRole('button', { name: 'Start QR setup' }))
    expect(onregister).toHaveBeenCalledWith('jordan-belfort')

    await fireEvent.click(screen.getByRole('button', { name: 'Save account' }))
    expect(onaccountschange).toHaveBeenCalledWith({
      'jordan-belfort': expect.objectContaining({
        enabled: true,
        app_id: 'cli-jordan',
        app_secret: 'secret-jordan',
        connection_mode: 'stream',
        domain_name: 'feishu'
      })
    })
  })

  it('preserves masked credentials when an existing account is edited with a blank replacement', async () => {
    setLocale('en-US')
    const onaccountschange = vi.fn()
    render(FeishuAccountsEditor, {
      accounts: { main: mainAccount },
      onaccountschange
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Edit main' }))
    expect((screen.getByLabelText('Replacement App Secret') as HTMLInputElement).value).toBe('')
    await fireEvent.input(screen.getByLabelText('App ID'), {
      target: { value: 'main-app-updated' }
    })
    await fireEvent.click(screen.getByRole('button', { name: 'Save account' }))

    expect(onaccountschange).toHaveBeenCalledWith({
      main: expect.objectContaining({
        app_id: 'main-app-updated',
        app_secret: '********',
        encrypt_key: '********',
        verify_token: '********'
      })
    })
  })

  it('deletes only the selected alias', async () => {
    setLocale('en-US')
    const onaccountschange = vi.fn()
    render(FeishuAccountsEditor, {
      accounts: {
        main: mainAccount,
        secondary: { ...mainAccount, app_id: 'secondary-app' }
      },
      onaccountschange
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Delete main' }))
    expect(onaccountschange).toHaveBeenCalledWith({
      secondary: expect.objectContaining({ app_id: 'secondary-app' })
    })
  })
})
