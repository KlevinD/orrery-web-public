import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ConversationSwitchPanel from './ConversationSwitchPanel.svelte'
import type { ConversationSwitchItem } from '#lib/conversation-switch-context.svelte'
import { setLocale } from '#lib/i18n'

const conversations: ConversationSwitchItem[] = [
  {
    id: 'discussion-launch',
    title: 'Q3 product launch',
    agentName: 'Planning Agent'
  },
  {
    id: 'discussion-delivery',
    title: 'Customer delivery review',
    agentName: 'Operations Agent'
  }
]

describe('ConversationSwitchPanel', () => {
  beforeEach(() => setLocale('en-US'))

  it('shows all conversations and forwards the selected conversation', async () => {
    const onSelect = vi.fn()
    const { container } = render(ConversationSwitchPanel, {
      props: { conversations, selectedConversationId: 'discussion-launch', onSelect }
    })

    expect(screen.getByText('Q3 product launch')).toBeTruthy()
    expect(screen.getByText('Customer delivery review')).toBeTruthy()
    expect(container.querySelector('[data-conversation-switch="discussion-launch"]')?.getAttribute('aria-pressed')).toBe('true')

    await fireEvent.click(screen.getByRole('button', { name: 'Conversations: Customer delivery review' }))
    expect(onSelect).toHaveBeenCalledWith(conversations[1])
    expect(screen.queryByRole('button', { name: '停止任务' })).toBeNull()
  })
})
