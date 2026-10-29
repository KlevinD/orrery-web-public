import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import { session } from '#lib/stores/session.svelte'
import MissionDiscussionPanel from './MissionDiscussionPanel.svelte'
import type { DiscussionViewModel, MissionControlViewModel } from '#lib/types/view-models'

const discussion: DiscussionViewModel = {
  groupId: 'dg-1',
  pendingMentions: 0,
  decisions: 0,
  blockers: 0,
  messages: [
    {
      id: 'm-1',
      groupId: 'dg-1',
      kind: 'message',
      authorId: 'operator',
      authorName: 'Operator',
      authorType: 'human',
      body: 'I will review the evidence.',
      mentions: [],
      replyToMessageId: '',
      replyToAuthor: '',
      replyPreview: '',
      toolName: '',
      oldStatus: '',
      newStatus: '',
      postedAt: 'Jun 02, 10:00 AM'
    },
    {
      id: 'm-2',
      groupId: 'dg-1',
      kind: 'message',
      authorId: 'agent-1',
      authorName: 'Planner',
      authorType: 'agent',
      body: 'Evidence pack is ready.',
      mentions: [],
      replyToMessageId: '',
      replyToAuthor: '',
      replyPreview: '',
      toolName: '',
      oldStatus: '',
      newStatus: '',
      postedAt: 'Jun 02, 10:01 AM'
    }
  ]
}

const discussionWithNewMessage: DiscussionViewModel = {
  ...discussion,
  messages: [
    ...discussion.messages,
    {
      id: 'm-3',
      groupId: 'dg-1',
      kind: 'message',
      authorId: 'agent-2',
      authorName: 'Verifier',
      authorType: 'agent',
      body: 'Final QA note arrived.',
      mentions: [],
      replyToMessageId: '',
      replyToAuthor: '',
      replyPreview: '',
      toolName: '',
      oldStatus: '',
      newStatus: '',
      postedAt: 'Jun 02, 10:03 AM'
    }
  ]
}

const discussionWithReplyPreview: DiscussionViewModel = {
  ...discussion,
  messages: [
    ...discussion.messages,
    {
      id: 'm-3',
      groupId: 'dg-1',
      kind: 'reply',
      authorId: 'agent-1',
      authorName: 'Planner',
      authorType: 'agent',
      body: 'I will continue from that evidence note.',
      mentions: [],
      replyToMessageId: 'm-1',
      replyToAuthor: 'Operator',
      replyPreview: 'I will review the evidence',
      toolName: '',
      oldStatus: '',
      newStatus: '',
      postedAt: 'Jun 02, 10:02 AM'
    }
  ]
}

const discussionWithMentionLink: DiscussionViewModel = {
  ...discussion,
  messages: [
    {
      ...discussion.messages[0],
      body: 'Ping [@Planner](mention://agent/agent-1) for review.'
    }
  ]
}

const context: MissionControlViewModel = {
  mission: {
    id: 'MSN-1',
    title: 'Evidence Review',
    description: 'Review evidence',
    status: 'active',
    statusTone: 'success',
    phase: 'execute',
    riskLevel: 'low',
    riskTone: 'success',
    teamSize: 1,
    activeTaskCount: 0,
    activeRunCount: 0,
    pendingApprovalCount: 0,
    blockerCount: 0,
    deliveryPercent: 50,
    deadline: 'Jun 03',
    goals: ['Review evidence'],
    createdBy: 'operator',
    updatedAt: 'Jun 02',
    originKind: '',
    originId: '',
    originTodoId: '',
    executionAttempt: 0,
    discussionMode: '',
    conversationId: '',
    originDevelopmentProjectId: ''
  },
  summary: {
    activeTasks: 0,
    pendingApprovals: 0,
    risks: 0,
    blockers: 0,
    teamSize: 1,
    deliveryPercent: 50,
    version: 1
  },
  team: [
    {
      id: 'agent-1',
      name: 'Planner',
      role: 'Mission planner',
      type: 'agent',
      department: 'Ops',
      position: 'Planner',
      status: 'active'
    }
  ],
  tasks: [],
  events: [],
  progressFocus: [],
  evidenceCount: 0,
  qaCount: 0,
  blockers: [],
  discussionGroupId: 'dg-1',
  discussionConversationId: 'conv-1',
  discussionRestricted: false
}

describe('MissionDiscussionPanel', () => {
  beforeEach(() => {
    setLocale('en-US')
    session.actorId = 'operator'
  })

  it('matches the React demo chat alignment for the current operator', () => {
    render(MissionDiscussionPanel, { props: { discussion } })

    const ownBubble = screen.getByText('I will review the evidence.').closest('[data-message-bubble]')
    const ownRow = screen.getByText('I will review the evidence.').closest('[data-message-row]')
    const peerBubble = screen.getByText('Evidence pack is ready.').closest('[data-message-bubble]')
    const peerRow = screen.getByText('Evidence pack is ready.').closest('[data-message-row]')

    expect(ownRow?.className).toContain('flex-row-reverse')
    expect(ownBubble?.className).toContain('bg-brand')
    expect(peerRow?.className).not.toContain('flex-row-reverse')
    expect(peerBubble?.className).toContain('bg-muted')
  })

  it('scrolls to the newest message when discussion messages change', async () => {
    const view = render(MissionDiscussionPanel, { props: { discussion } })
    const scrollRegion = view.container.querySelector('.subtle-scrollbar.flex-1.overflow-auto') as HTMLDivElement

    Object.defineProperty(scrollRegion, 'scrollHeight', { configurable: true, value: 1200 })
    scrollRegion.scrollTop = 0

    view.rerender({ discussion: discussionWithNewMessage })
    await tick()

    expect(scrollRegion.scrollTop).toBe(1200)
  })

  it('inserts backend-parsable mention links and keeps focus in the composer', async () => {
    render(MissionDiscussionPanel, { props: { discussion, context } })

    const composer = screen.getByLabelText('Message') as HTMLInputElement
    await fireEvent.input(composer, { target: { value: '@Plan' } })
    await fireEvent.click(screen.getByRole('button', { name: /Planner/ }))

    expect(composer.value).toBe('[@Planner](mention://agent/agent-1) ')
    expect(document.activeElement).toBe(composer)
  })

  it('matches the React demo reply reference preview with author and ellipsis', () => {
    render(MissionDiscussionPanel, { props: { discussion: discussionWithReplyPreview } })

    expect(screen.getByText('Operator: I will review the evidence...')).toBeTruthy()
  })

  it('renders backend mention links as readable @mentions', () => {
    render(MissionDiscussionPanel, { props: { discussion: discussionWithMentionLink } })

    expect(screen.getByText('@Planner')).toBeTruthy()
    expect(screen.queryByText(/mention:\/\/agent\/agent-1/)).toBeNull()
  })

  it('passes the matching inbox item id when replying to a pending mention', async () => {
    const onreply = vi.fn()
    render(MissionDiscussionPanel, {
      props: {
        discussion,
        inboxItems: [
          {
            inbox_item_id: 'inbox-1',
            owner_id: 'operator',
            message_id: 'm-2',
            reason: 'Planner mentioned you.',
            status: 'open',
            created_at: '2026-06-02T10:01:00Z'
          }
        ],
        onreply
      }
    })

    const targetRow = screen.getByText('Evidence pack is ready.').closest('[data-message-row]')
    if (!targetRow) throw new Error('target row not found')
    await fireEvent.click(within(targetRow as HTMLElement).getByRole('button', { name: 'Reply' }))

    const composer = screen.getByLabelText('Message') as HTMLInputElement
    await fireEvent.input(composer, { target: { value: 'Looks good.' } })
    await fireEvent.submit(composer.closest('form') as HTMLFormElement)

    expect(onreply).toHaveBeenCalledWith('m-2', 'Looks good.', 'inbox-1')
  })
})
