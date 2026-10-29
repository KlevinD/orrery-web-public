import { fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { WorkforceChannelAccountViewModel } from '#lib/types/view-models'
import WorkforceChannelBindingsPanel from './WorkforceChannelBindingsPanel.svelte'

const accounts: WorkforceChannelAccountViewModel[] = [
  {
    key: 'feishu:jordan-belfort', channelKey: 'feishu', accountId: 'jordan-belfort', enabled: true,
    status: 'connected', statusTone: 'success', ownerAgentProfileId: 'jordan-belfort', ownerDisplayName: 'Jordan',
    inboundEnabled: true, revision: 2, createdAt: '', updatedAt: '', ownedByTarget: true
  },
  {
    key: 'feishu:main', channelKey: 'feishu', accountId: 'main', enabled: true,
    status: 'connected', statusTone: 'success', ownerAgentProfileId: 'gangbeng', ownerDisplayName: '贴饼',
    inboundEnabled: false, revision: 4, createdAt: '', updatedAt: '', ownedByTarget: false
  }
]

describe('WorkforceChannelBindingsPanel', () => {
  afterEach(() => vi.restoreAllMocks())

  it('assigns, moves, toggles, and removes Feishu accounts with current revisions', async () => {
    setLocale('en-US')
    const onassign = vi.fn()
    const ontoggle = vi.fn()
    const ondelete = vi.fn()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    render(WorkforceChannelBindingsPanel, {
      targetAgentProfileId: 'jordan-belfort', accounts, canManage: true, onassign, ontoggle, ondelete
    })

    expect(screen.getByText('Owner: Jordan')).toBeTruthy()
    expect(screen.getByText('Owner: 贴饼')).toBeTruthy()
    await fireEvent.click(screen.getByLabelText('Inbound messages for jordan-belfort'))
    expect(ontoggle).toHaveBeenCalledWith(accounts[0], false)

    await fireEvent.click(screen.getByRole('button', { name: 'Unassign jordan-belfort' }))
    expect(ondelete).toHaveBeenCalledWith(accounts[0])

    await fireEvent.change(screen.getByLabelText('Feishu account'), { target: { value: 'main' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Assign account' }))
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining('贴饼'))
    expect(onassign).toHaveBeenCalledWith(accounts[1])
  })

  it('renders stable read-only controls without exposing credentials', () => {
    setLocale('en-US')
    const { container } = render(WorkforceChannelBindingsPanel, {
      targetAgentProfileId: 'jordan-belfort', accounts, canManage: false
    })

    expect((screen.getByLabelText('Inbound messages for jordan-belfort') as HTMLInputElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Unassign jordan-belfort' }) as HTMLButtonElement).disabled).toBe(true)
    expect(container.querySelector('[data-channel-account-row]')?.className).toContain('min-w-0')
    expect(container.textContent).not.toContain('app_secret')
  })
})
