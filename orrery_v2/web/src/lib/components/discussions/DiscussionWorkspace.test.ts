import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DiscussionWorkspaceHarness from './DiscussionWorkspaceHarness.svelte'
import { setLocale } from '#lib/i18n'
import { queryClient } from '#lib/query-client'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import type { MissionControlViewDTO } from '#lib/types/api'
import type { DiscussionDTO, DiscussionHILModeUpdateDTO, DiscussionPageDTO, DiscussionStreamEventDTO } from '#lib/types/discussions'
import type { RealtimeEnvelope } from '#lib/realtime/client'
import { discussionWorkspaceContext } from '#lib/discussion-workspace-context.svelte'
import { clearConversationSwitchContext, conversationSwitchContext } from '#lib/conversation-switch-context.svelte'
import { ApiClientError } from '#lib/api/client'

const apiMocks = vi.hoisted(() => ({
  listDiscussions: vi.fn(),
  listWorkforceMembers: vi.fn(),
  getWorkforceAvatar: vi.fn(),
  getDiscussion: vi.fn(),
  getPersonalAssistant: vi.fn(),
  getMissionControl: vi.fn(),
  getMissionOps: vi.fn(),
  createMission: vi.fn(),
  getMissionDiscussion: vi.fn(),
  createScheduledTask: vi.fn(),
  listDiscussionMessages: vi.fn(),
  listApprovals: vi.fn(),
  decideApproval: vi.fn(),
  decideHILDecision: vi.fn(),
  getLatestRunDebug: vi.fn(),
  sendMessageStream: vi.fn(),
  retryMessageStream: vi.fn(),
  deleteDiscussion: vi.fn(),
  abortDiscussion: vi.fn(),
  interjectDiscussion: vi.fn(),
  prewarmDiscussion: vi.fn(),
  uploadDiscussionAttachment: vi.fn(),
  startDiscussionAttachmentDraft: vi.fn(),
  addMembers: vi.fn(),
  setWorkspace: vi.fn(),
  setHILMode: vi.fn()
}))

const realtimeMocks = vi.hoisted(() => {
  const handlers = new Map<string, (ctx: { data: RealtimeEnvelope }) => void>()
  const subscription = {
    on: vi.fn((event: string, callback: (ctx: { data: RealtimeEnvelope }) => void) => {
      handlers.set(event, callback)
      return subscription
    }),
    subscribe: vi.fn(),
    unsubscribe: vi.fn()
  }
  const client = {
    newSubscription: vi.fn(() => subscription),
    connect: vi.fn(),
    disconnect: vi.fn()
  }
  return {
    client,
    handlers,
    subscription,
    createRealtimeClient: vi.fn(() => client)
  }
})

const missionStreamMocks = vi.hoisted(() => {
  const handlers = new Map<string, EventListener>()
  const source = {
    addEventListener: vi.fn((event: string, listener: EventListener) => handlers.set(event, listener)),
    close: vi.fn()
  }
  return {
    handlers,
    source,
    connectMissionStream: vi.fn(() => source)
  }
})

vi.mock('#lib/api/discussions', () => ({
  discussionApi: {
    listDiscussions: apiMocks.listDiscussions,
    getDiscussion: apiMocks.getDiscussion,
    listDiscussionMessages: apiMocks.listDiscussionMessages,
    getLatestRunDebug: apiMocks.getLatestRunDebug,
    deleteDiscussion: apiMocks.deleteDiscussion,
    abortDiscussion: apiMocks.abortDiscussion,
    interjectDiscussion: apiMocks.interjectDiscussion,
    prewarmDiscussion: apiMocks.prewarmDiscussion,
    uploadDiscussionAttachment: apiMocks.uploadDiscussionAttachment,
    startDiscussionAttachmentDraft: apiMocks.startDiscussionAttachmentDraft,
    addMembers: apiMocks.addMembers,
    setWorkspace: apiMocks.setWorkspace,
    setHILMode: apiMocks.setHILMode,
    sendMessage: vi.fn(),
    sendMessageStream: apiMocks.sendMessageStream,
    retryMessageStream: apiMocks.retryMessageStream
  }
}))

vi.mock('#lib/api/missions', () => ({
  missionApi: {
    getMissionControl: apiMocks.getMissionControl,
    getMissionOps: apiMocks.getMissionOps,
    createMission: apiMocks.createMission,
    getMissionDiscussion: apiMocks.getMissionDiscussion
  }
}))

vi.mock('#lib/api/scheduler', () => ({
  schedulerApi: {
    createTask: apiMocks.createScheduledTask
  }
}))

vi.mock('#lib/api/profile', () => ({
  profileApi: {
    getPersonalAssistant: apiMocks.getPersonalAssistant
  }
}))

vi.mock('#lib/api/operations', () => ({
  operationsApi: {
    listWorkforceMembers: apiMocks.listWorkforceMembers,
    getWorkforceAvatar: apiMocks.getWorkforceAvatar
  }
}))

vi.mock('#lib/api/approvals', () => ({
  approvalApi: {
    listApprovals: apiMocks.listApprovals,
    decideApproval: apiMocks.decideApproval,
    decideHILDecision: apiMocks.decideHILDecision
  }
}))

vi.mock('#lib/api/websocket', () => ({
  connectMissionStream: missionStreamMocks.connectMissionStream
}))

vi.mock('#lib/realtime/client', () => ({
  createRealtimeClient: realtimeMocks.createRealtimeClient
}))

describe('DiscussionWorkspace', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/discussions')
    setLocale('en-US')
    apiMocks.listDiscussions.mockReset()
    apiMocks.listWorkforceMembers.mockReset()
    apiMocks.getWorkforceAvatar.mockReset()
    apiMocks.getDiscussion.mockReset()
    apiMocks.getPersonalAssistant.mockReset()
    apiMocks.getMissionControl.mockReset()
    apiMocks.getMissionOps.mockReset()
    apiMocks.createMission.mockReset()
    apiMocks.getMissionDiscussion.mockReset()
    apiMocks.createScheduledTask.mockReset()
    apiMocks.listDiscussionMessages.mockReset()
    apiMocks.listApprovals.mockReset()
    apiMocks.decideApproval.mockReset()
    apiMocks.decideHILDecision.mockReset()
    apiMocks.getLatestRunDebug.mockReset()
    apiMocks.sendMessageStream.mockReset()
    apiMocks.retryMessageStream.mockReset()
    apiMocks.deleteDiscussion.mockReset()
    apiMocks.abortDiscussion.mockReset()
    apiMocks.interjectDiscussion.mockReset()
    apiMocks.prewarmDiscussion.mockReset()
    apiMocks.uploadDiscussionAttachment.mockReset()
    apiMocks.startDiscussionAttachmentDraft.mockReset()
    apiMocks.addMembers.mockReset()
    apiMocks.setWorkspace.mockReset()
    apiMocks.setHILMode.mockReset()
    realtimeMocks.handlers.clear()
    realtimeMocks.client.newSubscription.mockClear()
    realtimeMocks.client.connect.mockClear()
    realtimeMocks.client.disconnect.mockClear()
    realtimeMocks.subscription.on.mockClear()
    realtimeMocks.subscription.subscribe.mockClear()
    realtimeMocks.subscription.unsubscribe.mockClear()
    realtimeMocks.createRealtimeClient.mockClear()
    missionStreamMocks.handlers.clear()
    missionStreamMocks.source.addEventListener.mockClear()
    missionStreamMocks.source.close.mockClear()
    missionStreamMocks.connectMissionStream.mockClear()
    queryClient.clear()
    clearConversationSwitchContext()
    clearSession()
    applySession({
      accessToken: 'token-1',
      expiresAt: '',
      permissionScope: ['discussion.read', 'discussion.write'],
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: 'human-operator',
        role: 'owner'
      }
    })
    apiMocks.listApprovals.mockResolvedValue(emptyApprovalPage())
    apiMocks.listWorkforceMembers.mockResolvedValue(workforcePage())
    apiMocks.getWorkforceAvatar.mockResolvedValue(new Blob())
    apiMocks.decideApproval.mockResolvedValue({})
    apiMocks.decideHILDecision.mockResolvedValue({
      decision: { id: 'hil-decision-1', status: 'resolved' }
    })
    apiMocks.prewarmDiscussion.mockResolvedValue(newDiscussion())
    apiMocks.getPersonalAssistant.mockResolvedValue({
      assistant_enabled: true,
      hosting_mode: 'orrery_managed',
      fallback_to_managed: false,
      execution_identity: 'user',
      cloud_fallback: false,
      revision: 0,
      owned_agents: []
    })
    apiMocks.getMissionOps.mockResolvedValue({
      mission_id: '', title: '', status: '', phase: '', current_focus: [], agent_lanes: [], runtime_sessions: [],
      action_cards: [], evidence_gates: [], approval_gates: [], autonomy_policy: {}, summary: {}, updated_at: '', version: 0
    })
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000099')
  })

  it('restores the figma chat layout with list filters and a bubble thread', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')

    const workspace = container.querySelector('[data-discussion-workspace]') as HTMLElement
    const listPanel = container.querySelector('[data-discussion-list-panel]') as HTMLElement
    const messagePanel = container.querySelector('[data-discussion-message-panel]') as HTMLElement
    const timelineScroller = container.querySelector('[data-discussion-timeline-scroller]') as HTMLElement

    expect(workspace.className).toContain('h-full')
    expect(workspace.className).toContain('min-h-0')
    expect(workspace.className).not.toContain('min-h-[calc(100vh-3rem)]')
    expect(listPanel.className).toContain('w-[280px]')
    expect(listPanel.className).toContain('md:w-[280px]')
    expect(within(listPanel).getByRole('searchbox', { name: 'Search contacts' })).toBeTruthy()
    expect(within(listPanel).getByRole('button', { name: 'All' })).toBeTruthy()
    expect(within(listPanel).getByRole('button', { name: 'Digital employees' })).toBeTruthy()
    expect(screen.queryByLabelText('Workspace directory')).toBeNull()
    expect(container.querySelector('[data-discussion-list-action-row]')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Start a new session with this Agent' })).toBeNull()
    expect(messagePanel.className).toContain('min-w-0')
    expect(timelineScroller.className).toContain('bg-background')
    expect(container.querySelector('[data-discussion-task-panel]')).toBeNull()
    await waitFor(() => expect(discussionWorkspaceContext.mission?.mission.description).toBe('Ship existing mission'))
  })

  it('keeps discussion history out of the right sidebar when the header switcher owns sessions', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithNext())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => existingDiscussion({
      discussion_id: discussionId,
      title: discussionId === 'conv-next' ? 'Next launch' : 'Existing launch'
    }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness)

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-existing', { limit: 20 }))
    expect(conversationSwitchContext.conversations).toEqual([])
    expect(conversationSwitchContext.available).toBe(false)
    expect(screen.queryByLabelText('Switch session')).toBeNull()
  })

  it('pins the personal assistant and opens its real chat workspace for an empty account', async () => {
    apiMocks.listDiscussions.mockResolvedValue(emptyDiscussionPage())

    const { container } = render(DiscussionWorkspaceHarness)

    const assistantEntry = await screen.findByRole('button', { name: 'My assistant' })
    expect(assistantEntry).toBeTruthy()
    await waitFor(() => expect(container.querySelector('[data-personal-assistant-status]')).toBeTruthy())
    expect(discussionWorkspaceContext.assistantMode).toBe(true)
    expect(screen.getAllByText('Cloud runtime')).toHaveLength(2)
    expect(screen.getByText('What should I handle for you today?')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Create a scheduled Mission' }))

    expect((screen.getByLabelText('Chat message') as HTMLTextAreaElement).value).toBe('Create a scheduled Mission')
    await waitFor(() => expect(apiMocks.prewarmDiscussion).toHaveBeenCalledWith(
      undefined,
      { title: 'My assistant', conversation_kind: 'personal_assistant' },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
  })

  it('keeps older personal assistant conversations visible and switchable', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithPersonalAssistantHistory())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => personalAssistantDiscussion(discussionId))

    const { container } = render(DiscussionWorkspaceHarness)

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-assistant-latest', { limit: 20 }))
    expect(container.querySelector('[data-discussion-list-item="conv-assistant-latest"]')).toBeNull()
    expect(container.querySelector('[data-discussion-list-item="conv-assistant-older"]')).toBeNull()

    const sessionSelect = screen.getByLabelText('Switch session') as HTMLSelectElement
    expect(within(sessionSelect).getAllByRole('option')).toHaveLength(2)
    expect(sessionSelect.value).toBe('conv-assistant-latest')
    expect(screen.getByRole('button', { name: 'Start a new session with this Agent' })).toBeTruthy()
    expect(container.querySelector('[data-personal-assistant-history-list]')).toBeNull()

    await fireEvent.change(sessionSelect, { target: { value: 'conv-assistant-older' } })

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-assistant-older', { limit: 20 }))
    expect(await screen.findByText('Earlier assistant answer')).toBeTruthy()
    expect(new URLSearchParams(window.location.search).get('discussion')).toBe('conv-assistant-older')
  })

  it('starts a fresh personal assistant conversation even when an earlier conversation exists', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithPersonalAssistantHistory())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => personalAssistantDiscussion(discussionId))
    apiMocks.prewarmDiscussion.mockResolvedValue(personalAssistantDiscussion('conv-assistant-new'))

    render(DiscussionWorkspaceHarness)

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-assistant-latest', { limit: 20 }))
    await fireEvent.click(screen.getByRole('button', { name: 'Start a new session with this Agent' }))

    await waitFor(() => expect(apiMocks.prewarmDiscussion).toHaveBeenCalledWith(
      undefined,
      { title: 'My assistant', conversation_kind: 'personal_assistant' },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
    expect(discussionWorkspaceContext.assistantMode).toBe(true)
  })

  it('forces a fresh personal assistant conversation when prewarming is unavailable', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithPersonalAssistantHistory())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => personalAssistantDiscussion(discussionId))
    apiMocks.prewarmDiscussion.mockResolvedValue({})
    apiMocks.sendMessageStream.mockResolvedValue(undefined)

    render(DiscussionWorkspaceHarness)

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-assistant-latest', { limit: 20 }))
    await fireEvent.click(screen.getByRole('button', { name: 'Start a new session with this Agent' }))
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Fresh assistant request' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBeUndefined()
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Fresh assistant request',
      conversation_kind: 'personal_assistant',
      force_new_conversation: true
    })
  })

  it('switches sessions and starts a fresh session for a direct Agent chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithDirectAgentHistory())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => directDiscussionSession(discussionId))

    render(DiscussionWorkspaceHarness)

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-direct-latest', { limit: 20 }))
    const sessionSelect = screen.getByLabelText('Switch session') as HTMLSelectElement
    expect(within(sessionSelect).getAllByRole('option')).toHaveLength(2)
    expect(sessionSelect.value).toBe('conv-direct-latest')

    await fireEvent.change(sessionSelect, { target: { value: 'conv-direct-older' } })

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-direct-older', { limit: 20 }))
    expect(await screen.findByText('Earlier direct Agent answer')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Start a new session with this Agent' }))

    expect((screen.getByLabelText('Switch session') as HTMLSelectElement).value).toBe('__new_session__')
  })

  it('keeps the restored conversation list compact and does not render metadata action rows', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithAttachmentCount())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion({
      attachments: [{
        attachment_id: 'att-1',
        discussion_id: 'conv-existing',
        file_name: 'brief.txt',
        content_type: 'text/plain',
        size_bytes: 12,
        uploaded_by: 'operator',
        uploaded_by_kind: 'user',
        uploaded_by_name: 'Operator',
        download_url: '/api/discussions/conv-existing/attachments/att-1/download',
        created_at: '2026-06-02T09:00:00Z'
      }]
    }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')

    const item = container.querySelector('[data-discussion-list-item="conv-existing"]') as HTMLElement
    const titleRow = item.querySelector('[data-discussion-list-title-row]') as HTMLElement
    const actionRow = item.querySelector('[data-discussion-list-action-row]') as HTMLElement

    expect(titleRow.textContent).toContain('Existing launch')
    expect(titleRow.textContent).not.toContain('Group chat')
    expect(titleRow.textContent).not.toContain('completed')
    expect(titleRow.textContent).not.toContain('1')
    expect(actionRow).toBeNull()
    expect(within(item).queryByRole('button', { name: 'Delete chat' })).toBeNull()
  })

  it('prewarms an opened group discussion once across load, focus, and typing', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness)

    const composer = await screen.findByLabelText('Chat message')
    await waitFor(() => expect(apiMocks.prewarmDiscussion).toHaveBeenCalledWith(
      'conv-existing',
      {},
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
    await fireEvent.focus(composer)
    await fireEvent.input(composer, { target: { value: 'Prepare context' } })

    expect(apiMocks.prewarmDiscussion.mock.calls.filter(([discussionId]) => discussionId === 'conv-existing')).toHaveLength(1)
  })

  it('keeps the composer in new group chat mode after clicking Start group chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: newDiscussion() })
      }
    )

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))
    await screen.findByRole('link', { name: 'Open Mission workspace: Existing mission' })
    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))

    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))
    await tick()

    expect(screen.queryByRole('link', { name: /Open Mission workspace/ })).toBeNull()

    const composer = screen.getByLabelText('Chat message') as HTMLTextAreaElement
    await fireEvent.input(composer, { target: { value: 'Start a fresh intake' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-new')
    expect(apiMocks.prewarmDiscussion).toHaveBeenCalledWith(
      undefined,
      { title: 'Start group chat', conversation_kind: 'group' },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    )
    const discussionList = container.querySelector<HTMLElement>('[data-discussion-list-panel]')
    if (!discussionList) throw new Error('discussion list panel was not rendered')
    expect(within(discussionList).getByText('Start a fresh intake')).toBeTruthy()
  })

  it('creates a real Mission and sends its goal into the canonical Mission chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-mission-created' ? createdMissionDiscussion() : existingDiscussion()
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.createMission.mockResolvedValue({
      mission_id: 'mission-created',
      title: 'Validate the next cu…',
      final_goal: 'Validate the next customer cohort',
      status: 'active',
      created_at: '2026-08-08T09:00:00Z'
    })
    apiMocks.getMissionDiscussion.mockResolvedValue({
      discussion_group_id: 'group-mission-created',
      conversation_id: 'conv-mission-created'
    })
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: createdMissionDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Toggle Mission mode' }))
    await fireEvent.input(screen.getByLabelText('Mission goal'), { target: { value: 'Validate the next customer cohort' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Start Mission' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.createMission).toHaveBeenCalledWith({
      title: 'Validate the next cu…',
      final_goal: 'Validate the next customer cohort',
      business_direction: ''
    }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(apiMocks.getMissionDiscussion).toHaveBeenCalledWith('mission-created')
    expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-mission-created', { limit: 20 })
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-mission-created')
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual(expect.objectContaining({
      content: 'Validate the next customer cohort'
    }))
    expect(apiMocks.sendMessageStream.mock.calls[0][1].content).not.toContain('Create a new Mission conversation')
  })

  it('preserves the personal assistant conversation as the Mission source', async () => {
    apiMocks.listDiscussions.mockResolvedValue(emptyDiscussionPage())
    apiMocks.prewarmDiscussion.mockResolvedValue({
      ...newDiscussion(),
      discussion_id: 'conv-assistant',
      conversation_kind: 'personal_assistant',
      title: 'My assistant'
    })
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-mission-created' ? createdMissionDiscussion() : {
        ...newDiscussion(),
        discussion_id: 'conv-assistant',
        conversation_kind: 'personal_assistant',
        title: 'My assistant'
      }
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.createMission.mockResolvedValue({
      mission_id: 'mission-created',
      title: 'Prepare a customer brief',
      final_goal: 'Prepare a customer brief',
      status: 'active',
      created_at: '2026-08-08T09:00:00Z'
    })
    apiMocks.getMissionDiscussion.mockResolvedValue({
      discussion_group_id: 'group-mission-created',
      conversation_id: 'conv-mission-created'
    })
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: createdMissionDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness, { props: { initialAssistant: true } })

    await screen.findByRole('button', { name: 'My assistant' })
    await waitFor(() => expect(apiMocks.prewarmDiscussion).toHaveBeenCalledWith(
      undefined,
      { title: 'My assistant', conversation_kind: 'personal_assistant' },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
    await fireEvent.click(screen.getByRole('button', { name: 'Toggle Mission mode' }))
    await fireEvent.input(screen.getByLabelText('Mission goal'), { target: { value: 'Prepare a customer brief' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Start Mission' }))

    await waitFor(() => expect(apiMocks.createMission).toHaveBeenCalledTimes(1))
    expect(apiMocks.createMission).toHaveBeenCalledWith(expect.objectContaining({
      final_goal: 'Prepare a customer brief',
      business_direction: '',
      source_personal_assistant_conversation_id: 'conv-assistant'
    }), expect.objectContaining({ idempotencyKey: expect.any(String) }))
  })

  it('reuses one Mission launch across transport and discussion lookup retries', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-mission-created' ? createdMissionDiscussion() : existingDiscussion()
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.createMission
      .mockRejectedValueOnce(new Error('Connection reset after create'))
      .mockResolvedValue({
        mission_id: 'mission-created',
        title: 'Validate the next customer cohort',
        final_goal: 'Validate the next customer cohort',
        status: 'active',
        created_at: '2026-08-08T09:00:00Z'
      })
    apiMocks.getMissionDiscussion
      .mockRejectedValueOnce(new Error('Mission chat is still projecting'))
      .mockResolvedValue({
        discussion_group_id: 'group-mission-created',
        conversation_id: 'conv-mission-created'
      })
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: createdMissionDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Toggle Mission mode' }))
    await fireEvent.input(screen.getByLabelText('Mission goal'), { target: { value: 'Validate the next customer cohort' } })
    const startMission = screen.getByRole('button', { name: 'Start Mission' })

    await fireEvent.click(startMission)
    await waitFor(() => expect(apiMocks.createMission).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(startMission.hasAttribute('disabled')).toBe(false))

    await fireEvent.click(startMission)
    await waitFor(() => expect(apiMocks.createMission).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(apiMocks.getMissionDiscussion).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(startMission.hasAttribute('disabled')).toBe(false))

    await fireEvent.click(startMission)
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))

    expect(apiMocks.createMission).toHaveBeenCalledTimes(2)
    expect(apiMocks.createMission.mock.calls[0][1].idempotencyKey).toBe(apiMocks.createMission.mock.calls[1][1].idempotencyKey)
    expect(apiMocks.getMissionDiscussion).toHaveBeenCalledTimes(2)
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-mission-created')
  })

  it('creates a scheduled Mission without running its Agent before the start time', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-mission-created' ? createdMissionDiscussion() : existingDiscussion()
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.createScheduledTask.mockResolvedValue({
      scheduled_task_id: 'schedule-created',
      mission_id: 'mission-created',
      agent_task_id: 'agent-task-created',
      title: 'Prepare the weekly o…',
      schedule: { type: 'once', run_at: '2099-08-12T01:30:00.000Z' },
      target: { mode: 'agent_task', agent_id: 'A000010', task_goal: 'Prepare the weekly operating review' },
      policy: { misfire: 'run_once', concurrency: 'allow_parallel' },
      status: 'enabled',
      enabled: true,
      created_by: 'operator',
      created_at: '2026-08-08T09:00:00Z',
      updated_at: '2026-08-08T09:00:00Z'
    })
    apiMocks.getMissionDiscussion.mockResolvedValue({
      discussion_group_id: 'group-mission-created',
      conversation_id: 'conv-mission-created'
    })

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Toggle Mission mode' }))
    await fireEvent.input(screen.getByLabelText('Mission goal'), { target: { value: 'Prepare the weekly operating review' } })
    await fireEvent.change(screen.getByRole('combobox', { name: 'Mission launch mode' }), { target: { value: 'scheduled' } })
    await fireEvent.input(screen.getByLabelText('Mission start time'), { target: { value: '2099-08-12T09:30' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Start Mission' }))

    await waitFor(() => expect(apiMocks.createScheduledTask).toHaveBeenCalled())
    expect(apiMocks.createScheduledTask).toHaveBeenCalledWith(expect.objectContaining({
      title: 'Prepare the weekly o…',
      description: 'Prepare the weekly operating review',
      schedule: expect.objectContaining({ type: 'once', run_at: expect.any(String) }),
      target: {
        mode: 'agent_task',
        agent_id: 'A000010',
        task_goal: 'Prepare the weekly operating review',
        prompt: 'Prepare the weekly operating review'
      },
      policy: { misfire: 'run_once', concurrency: 'allow_parallel' },
      enabled: true
    }), expect.objectContaining({ idempotencyKey: expect.any(String) }))
    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-mission-created', { limit: 20 }))
    expect(apiMocks.sendMessageStream).not.toHaveBeenCalled()
    expect((screen.getByLabelText('Chat message') as HTMLTextAreaElement).value).toBe('')
  })

  it('does not reuse the personal assistant prewarm when starting a normal group chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.prewarmDiscussion.mockImplementation(async (_discussionId: string | undefined, body: { title?: string }) => ({
      ...newDiscussion(),
      discussion_id: body.title === 'My assistant' ? 'conv-assistant' : 'conv-group',
      title: body.title ?? ''
    }))
    apiMocks.sendMessageStream.mockImplementation(
      async (discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            ...continuedDiscussion(body.content),
            discussion_id: discussionId ?? 'conv-group'
          }
        })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'My assistant' }))
    await waitFor(() => expect(apiMocks.prewarmDiscussion).toHaveBeenCalledWith(
      undefined,
      { title: 'My assistant', conversation_kind: 'personal_assistant' },
      expect.any(Object)
    ))
    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Open a normal group chat' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-group')
  })

  it('opens the conversation list as an overlay on compact viewports', async () => {
    const originalMatchMedia = window.matchMedia
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 767px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn()
      }))
    })

    try {
      apiMocks.listDiscussions.mockResolvedValue(discussionPage())
      apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
      apiMocks.getMissionControl.mockResolvedValue(missionControl())

      const { container } = render(DiscussionWorkspaceHarness)

      await fireEvent.click(await screen.findByRole('button', { name: 'Open menu' }))

      const workspace = container.querySelector('[data-discussion-workspace]') as HTMLElement
      const listPanel = container.querySelector('[data-discussion-list-panel]') as HTMLElement
      expect(workspace.classList.contains('relative')).toBe(true)
      expect(listPanel.classList.contains('absolute')).toBe(true)
      expect(listPanel.classList.contains('inset-y-0')).toBe(true)
      expect(listPanel.classList.contains('left-0')).toBe(true)
      expect(listPanel.classList.contains('md:static')).toBe(true)

      const actions = within(listPanel).getByRole('button', { name: 'Open chat actions' })
      expect(actions.classList.contains('opacity-100')).toBe(true)
      expect(actions.classList.contains('md:opacity-0')).toBe(true)
    } finally {
      Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia })
    }
  })

  it('collapses the conversation list after reselecting the active draft on a compact viewport', async () => {
    const originalMatchMedia = window.matchMedia
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 767px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn()
      }))
    })

    try {
      apiMocks.listDiscussions.mockResolvedValue(discussionPage())
      apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
      apiMocks.getMissionControl.mockResolvedValue(missionControl())

      const { container } = render(DiscussionWorkspaceHarness)

      await screen.findByRole('button', { name: 'Open menu' })
      await fireEvent.click(screen.getByRole('button', { name: 'Open menu' }))
      await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))
      await tick()

      await fireEvent.click(screen.getByRole('button', { name: 'Open menu' }))
      const draftItem = container.querySelector('[data-discussion-list-item="draft-new-discussion"]') as HTMLElement
      await fireEvent.click(within(draftItem).getByRole('button'))

      expect(container.querySelector('[data-discussion-list-panel]')).toBeNull()
      expect(screen.getByRole('button', { name: 'Open menu' })).toBeTruthy()
    } finally {
      Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia })
    }
  })

  it('saves the selected server workspace directory for a group chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion({ workspace_root_dir: '/srv/current' }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.setWorkspace.mockResolvedValue(existingDiscussion({ workspace_root_dir: '/srv/next' }))

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Open workspace settings' }))
    const input = screen.getByLabelText('Workspace directory') as HTMLInputElement
    expect(input.value).toBe('/srv/current')

    await fireEvent.input(input, { target: { value: '/srv/next' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(apiMocks.setWorkspace).toHaveBeenCalled())
    expect(apiMocks.setWorkspace).toHaveBeenCalledWith(
      'conv-existing',
      { workspace_root_dir: '/srv/next' },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    )
    expect(input.value).toBe('/srv/next')
  })

  it('uses the created discussion id for later messages in the same discussion', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: continuedDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Start a fresh intake' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-new')
    await screen.findByText('Runtime status: completed')

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Continue in the same discussion' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(2))

    expect(apiMocks.sendMessageStream.mock.calls[1][0]).toBe('conv-new')
  })

  it('keeps a new discussion plain assistant reply visible when the detail refetch is stale', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockImplementation((discussionId: string) => {
      if (discussionId === 'conv-new') {
        return Promise.resolve(continuedDiscussion('嘿 都冒个泡'))
      }
      return Promise.resolve(existingDiscussion())
    })
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: continuedDiscussion(body.content) })
        onEvent({
          kind: 'message_completed',
          event_id: 'evt-plain-runtime-reply',
          discussion_id: 'conv-new',
          turn_id: 'turn-new',
          revision: 3,
          message: {
            message_id: 'msg-plain-runtime-reply',
            discussion_id: 'conv-new',
            turn_id: 'turn-new',
            run_id: 'run-new',
            role: 'assistant',
            author_id: 'lead-agent',
            author_name: '顾明澈',
            content: '冒泡',
            created_at: '2026-06-02T10:00:02Z'
          }
        })
      }
    )

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: '嘿 都冒个泡' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(apiMocks.getDiscussion.mock.calls.some(([discussionId]) => discussionId === 'conv-new')).toBe(true))
    await tick()

    const messagePanel = container.querySelector('[data-discussion-message-panel]') as HTMLElement
    expect(within(messagePanel).getByText('冒泡')).toBeTruthy()
    expect(messagePanel.querySelector('[data-testid^="discussion-agent-work-card-"]')).toBeNull()
  })

  it('uses the created discussion id after a new discussion stream fails', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream
      .mockImplementationOnce(
        async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
          onEvent({ kind: 'snapshot', discussion: failedNewDiscussion(body.content) })
          throw new Error('stream disconnected')
        }
      )
      .mockImplementationOnce(
        async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
          onEvent({ kind: 'snapshot', discussion: continuedDiscussion(body.content) })
        }
      )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Start a fresh intake' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-new')
    await screen.findByText('stream disconnected')

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Continue after failure' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(2))

    expect(apiMocks.sendMessageStream.mock.calls[1][0]).toBe('conv-new')
  })

  it('serializes readable composer mentions before sending', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion({
      members: [
        { kind: 'agent', id: 'agent-alpha', display_name: '顾明澈', role: 'Lead' },
        { kind: 'agent', id: 'agent-beta', display_name: '秦若兰', role: 'Staffing' },
        { kind: 'user', id: 'operator', display_name: 'Operator', role: '话题发起人' }
      ]
    }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: continuedDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing mission is ready.')
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: '@秦' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Mention 秦若兰' }))
    expect((screen.getByLabelText('Chat message') as HTMLTextAreaElement).value).toBe('@秦若兰 ')
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: '@秦若兰 请确认。' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toMatchObject({
      content: '@秦若兰 请确认。',
      mentions: [{ target_kind: 'agent', target_id: 'agent-beta', display_name: '秦若兰' }]
    })
  })

  it('uploads an attachment into the active discussion and renders its download link', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.uploadDiscussionAttachment.mockResolvedValue({
      attachment_id: 'att-1',
      discussion_id: 'conv-existing',
      file_name: 'brief.txt',
      content_type: 'text/plain',
      size_bytes: 12,
      uploaded_by: 'operator',
      uploaded_by_kind: 'user',
      uploaded_by_name: 'Operator',
      download_url: '/api/discussions/conv-existing/attachments/att-1/download',
      created_at: '2026-06-02T09:05:00Z'
    })
    const file = new File(['launch brief'], 'brief.txt', { type: 'text/plain' })

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing mission is ready.')
    await fireEvent.change(screen.getByLabelText('Upload attachment file'), { target: { files: [file] } })

    await waitFor(() => expect(apiMocks.uploadDiscussionAttachment).toHaveBeenCalledWith(
      'conv-existing',
      file,
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
    expect((await screen.findByRole('link', { name: /brief\.txt/ })).getAttribute('href')).toBe('/api/discussions/conv-existing/attachments/att-1/download')
  })

  it('starts a group chat draft when the first action is uploading an attachment', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.uploadDiscussionAttachment.mockResolvedValue({
      attachment_id: 'att-draft',
      discussion_id: 'conv-new',
      file_name: 'brief.txt',
      content_type: 'text/plain',
      size_bytes: 12,
      uploaded_by: 'operator',
      uploaded_by_kind: 'user',
      uploaded_by_name: 'Operator',
      download_url: '/api/discussions/conv-new/attachments/att-draft/download',
      created_at: '2026-06-02T10:00:00Z'
    })
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: continuedDiscussion(body.content) })
      }
    )
    const file = new File(['launch brief'], 'brief.txt', { type: 'text/plain' })

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))
    expect((screen.getByRole('button', { name: 'Upload attachment' }) as HTMLButtonElement).disabled).toBe(false)
    await fireEvent.change(screen.getByLabelText('Upload attachment file'), { target: { files: [file] } })

    await waitFor(() => expect(apiMocks.uploadDiscussionAttachment).toHaveBeenCalledWith(
      'conv-new',
      file,
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
    expect(apiMocks.startDiscussionAttachmentDraft).not.toHaveBeenCalled()
    expect(await screen.findByRole('link', { name: /brief\.txt/ })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-new')
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: '',
      attachment_ids: ['att-draft']
    })
  })

  it('starts a private chat draft when the first action is uploading an attachment', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.startDiscussionAttachmentDraft.mockResolvedValue({
      discussion: directDiscussionWithAttachment(),
      attachment: {
        attachment_id: 'att-direct',
        discussion_id: 'conv-direct',
        file_name: 'brief.txt',
        content_type: 'text/plain',
        size_bytes: 12,
        uploaded_by: 'operator',
        uploaded_by_kind: 'user',
        uploaded_by_name: 'Operator',
        download_url: '/api/discussions/conv-direct/attachments/att-direct/download',
        created_at: '2026-06-02T10:00:00Z'
      }
    })
    const file = new File(['launch brief'], 'brief.txt', { type: 'text/plain' })

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findAllByText('秦若兰')
    await fireEvent.click(screen.getByRole('button', { name: /Staffing/ }))
    expect((screen.getByRole('button', { name: 'Upload attachment' }) as HTMLButtonElement).disabled).toBe(false)
    await fireEvent.change(screen.getByLabelText('Upload attachment file'), { target: { files: [file] } })

    await waitFor(() => expect(apiMocks.startDiscussionAttachmentDraft).toHaveBeenCalledWith(
      { file, forceNewConversation: false, directTargetId: 'staffing-agent', directTargetKind: 'agent', directAgentId: undefined },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    ))
    expect(await screen.findByRole('link', { name: /brief\.txt/ })).toBeTruthy()

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Use this privately' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-direct')
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Use this privately',
      attachment_ids: ['att-direct']
    })
  })

  it('adds a selected group chat draft to the left list immediately when starting a group chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')

    await fireEvent.click(screen.getByRole('button', { name: 'Start group chat' }))
    await tick()

    const discussionList = container.querySelector<HTMLElement>('[data-discussion-list-panel]')
    if (!discussionList) throw new Error('discussion list panel was not rendered')
    expect(within(discussionList).getAllByText('Start group chat').length).toBeGreaterThan(0)
    expect(within(discussionList).getByText('No chat yet. Start a private or group chat.')).toBeTruthy()
    expect(within(discussionList).queryByText('Group chat')).toBeNull()

    const draftDiscussion = within(discussionList)
      .getAllByRole('button')
      .find((button) => button.textContent?.includes('No chat yet. Start a private or group chat.'))
    expect(draftDiscussion).toBeTruthy()
    expect(draftDiscussion?.parentElement?.className).toContain('selection-wash')
  })

  it('requests retry from an eligible human message through the stream API', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(retryableDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.retryMessageStream.mockImplementation(
      async (_discussionId: string, _messageId: string, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: retryPendingDiscussion() })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Use HTML')
    await fireEvent.click(screen.getByRole('button', { name: 'Retry from this message' }))

    await waitFor(() => expect(apiMocks.retryMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.retryMessageStream.mock.calls[0][0]).toBe('conv-existing')
    expect(apiMocks.retryMessageStream.mock.calls[0][1]).toBe('msg-user')
    expect(apiMocks.retryMessageStream.mock.calls[0][2]).toEqual({ approve: false })
    expect(await screen.findByText('Retry approval pending')).toBeTruthy()
  })

  it('does not render numbered pagination controls for discussion list paging', async () => {
    apiMocks.listDiscussions
      .mockResolvedValueOnce(discussionPage({ total: 21 }))
      .mockResolvedValueOnce(secondDiscussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    expect(apiMocks.listDiscussions.mock.calls[0][0]).toEqual({ page: 1, pageSize: 20 })
    expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Previous page' })).toBeNull()
  })

  it('starts a private chat draft with an Agent and sends the target agent id', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string; direct_agent_id?: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: directDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findAllByText('秦若兰')
    expect(apiMocks.listWorkforceMembers).toHaveBeenCalledWith(
      '?enabled_only=true&include_details=false&limit=100&offset=0',
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )

    await fireEvent.click(screen.getByRole('button', { name: /Staffing/ }))
    expect(screen.getAllByText('Private chat').length).toBeGreaterThan(0)
    expect(screen.getByText('Private chat ready. Send the first message to start.')).toBeTruthy()

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Can you staff this?' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBeUndefined()
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Can you staff this?',
      direct_target_kind: 'agent',
      direct_target_id: 'staffing-agent'
    })
    await screen.findByText('Runtime status: completed')
  })

  it('restores a consultation draft and target passed from the home composer', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness, {
      props: {
        initialDraft: 'Which payment clauses are risky?',
        initialDirectTargetId: 'staffing-agent',
        initialDirectTargetKind: 'agent',
        initialDirectTargetName: '秦若兰'
      }
    })

    expect(await screen.findByDisplayValue('Which payment clauses are risky?')).toBeTruthy()
    expect(apiMocks.listWorkforceMembers).not.toHaveBeenCalled()
    expect(screen.getAllByText('Private chat').length).toBeGreaterThan(0)
    expect(screen.getAllByText('秦若兰').length).toBeGreaterThan(0)
  })

  it('sends a home composer message immediately after opening the private chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: directDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness, {
      props: {
        initialDraft: 'Please review this contract.',
        initialSendImmediately: true,
        initialDirectTargetId: 'staffing-agent',
        initialDirectTargetKind: 'agent',
        initialDirectTargetName: '秦若兰'
      }
    })

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBeUndefined()
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Please review this contract.',
      direct_target_kind: 'agent',
      direct_target_id: 'staffing-agent'
    })
  })

  it('keeps an ID-only consultation targeted when the workforce lookup has no match', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.listWorkforceMembers.mockResolvedValue({ members: [], page: { total: 0, returned: 0 } })
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockResolvedValue(undefined)

    render(DiscussionWorkspaceHarness, {
      props: {
        initialDraft: 'Which payment clauses are risky?',
        initialDirectTargetId: 'staffing-agent'
      }
    })

    expect(await screen.findByDisplayValue('Which payment clauses are risky?')).toBeTruthy()
    expect(screen.getAllByText('Private chat').length).toBeGreaterThan(0)

    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBeUndefined()
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Which payment clauses are risky?',
      direct_target_kind: 'agent',
      direct_target_id: 'staffing-agent'
    })
  })

  it('shows direct chat runtime approval from the HTTP stream without waiting for a refresh', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => {
      if (discussionId === 'conv-direct') {
        return new Promise<DiscussionDTO>(() => {})
      }
      return existingDiscussion()
    })
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string; direct_target_id?: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: directDiscussionWithRuntimeApproval(body.content) })
      }
    )

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findAllByText('秦若兰')
    await fireEvent.click(screen.getByRole('button', { name: /Staffing/ }))

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Please inspect Symphony.' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    await screen.findByTestId('discussion-approval-card-discussion-runtime-approval-conv-direct-run-direct-cmd-1')
    const timelineIds = [...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))
    expect(timelineIds).toEqual([
      'msg-direct-user',
      'run-direct',
      'approval:discussion-runtime-approval-conv-direct-run-direct-cmd-1'
    ])
  })

  it('starts a private chat draft with a person and sends the direct target id', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: directHumanDiscussion(body.content) })
      }
    )

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findByText('Reviewer')

    await fireEvent.click(screen.getByRole('button', { name: /Human reviewer/ }))
    expect(screen.getAllByText('Private chat').length).toBeGreaterThan(0)
    expect(screen.getByText('Private chat ready. Send the first message to start.')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Start a new session with this Agent' })).toBeNull()

    const listPanel = container.querySelector('[data-discussion-list-panel]') as HTMLElement
    await fireEvent.click(within(listPanel).getByRole('button', { name: 'People' }))
    expect(within(listPanel).getByText('Reviewer')).toBeTruthy()
    await fireEvent.click(within(listPanel).getByRole('button', { name: 'All' }))

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Can you review this?' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBeUndefined()
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Can you review this?',
      direct_target_kind: 'user',
      direct_target_id: 'human-reviewer'
    })
    await screen.findByText('Runtime status: completed')
  })

  it('does not offer the current user as a private chat target', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findByText('秦若兰')
    expect(screen.getByRole('button', { name: /Human reviewer/ })).toBeTruthy()

    expect(screen.queryByText('Operator')).toBeNull()
  })

  it('switches to an existing private chat with the selected Agent instead of creating a draft', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithDirect())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-direct'
        ? (apiMocks.addMembers.mock.calls.length > 0 ? convertedGroupDiscussion() : directDiscussion('Earlier private note'))
        : existingDiscussion()
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: directDiscussion(body.content) })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findAllByText('秦若兰')

    await fireEvent.click(screen.getByRole('button', { name: /Staffing/ }))

    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-direct', { limit: 20 }))
    expect(screen.queryByText('Private chat ready. Send the first message to start.')).toBeNull()
    expect(screen.getAllByText('Private chat').length).toBeGreaterThan(0)

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Continue privately' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalled())
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBe('conv-direct')
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Continue privately'
    })
  })

  it('starts a separate session from an existing Agent private chat', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithDirect())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-direct' ? directDiscussion('Earlier private note') : existingDiscussion()
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'snapshot', discussion: { ...directDiscussion(body.content), discussion_id: 'conv-direct-new' } })
      }
    )

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Start private chat' }))
    await screen.findAllByText('秦若兰')
    await fireEvent.click(screen.getByRole('button', { name: /Staffing/ }))
    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-direct', { limit: 20 }))

    const listPanel = container.querySelector('[data-discussion-list-panel]') as HTMLElement
    expect(listPanel.querySelectorAll('[data-discussion-list-item]')).toHaveLength(2)

    await fireEvent.click(screen.getByRole('button', { name: 'Start a new session with this Agent' }))
    expect(screen.queryByText('Private chat ready. Send the first message to start.')).toBeNull()
    expect(listPanel.querySelectorAll('[data-discussion-list-item]')).toHaveLength(2)
    expect(listPanel.querySelector('[data-discussion-list-item="draft-direct-discussion"]')).toBeNull()
    expect(listPanel.querySelector('[data-discussion-list-item="conv-direct"]')?.classList.contains('selection-wash')).toBe(true)
    expect((screen.getByLabelText('Switch session') as HTMLSelectElement).value).toBe('__new_session__')

    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Start separately' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    expect(apiMocks.sendMessageStream.mock.calls[0][0]).toBeUndefined()
    expect(apiMocks.sendMessageStream.mock.calls[0][1]).toEqual({
      content: 'Start separately',
      force_new_conversation: true,
      direct_target_kind: 'agent',
      direct_target_id: 'staffing-agent'
    })
    await waitFor(() => expect(listPanel.querySelector('[data-discussion-list-item="conv-direct-new"]')).toBeTruthy())
    expect(listPanel.querySelectorAll('[data-discussion-list-item]')).toHaveLength(2)
    expect(listPanel.querySelector('[data-discussion-list-item="conv-direct"]')).toBeNull()
    const sessionSelect = screen.getByLabelText('Switch session') as HTMLSelectElement
    expect(within(sessionSelect).getAllByRole('option').map((option) => (option as HTMLOptionElement).value)).toEqual(expect.arrayContaining([
      'conv-direct-new',
      'conv-direct'
    ]))

    await fireEvent.change(sessionSelect, { target: { value: 'conv-direct' } })
    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-direct', { limit: 20 }))
    await waitFor(() => {
      const directEntries = [...listPanel.querySelectorAll<HTMLElement>('[data-discussion-list-item]')]
        .filter((item) => item.dataset.discussionListItem?.startsWith('conv-direct'))
      expect(directEntries).toHaveLength(1)
      expect(directEntries[0].classList.contains('selection-wash')).toBe(true)
    })
  })

  it('converts a private chat to a group chat by adding another member', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithDirect())
    apiMocks.getDiscussion.mockImplementation(async (discussionId: string) => (
      discussionId === 'conv-direct' ? directDiscussion('Earlier private note') : existingDiscussion()
    ))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.addMembers.mockResolvedValue(convertedGroupDiscussion())

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: /秦若兰/ }))
    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))
    await screen.findByRole('button', { name: 'Convert to group chat' })

    await fireEvent.click(screen.getByRole('button', { name: 'Convert to group chat' }))
    await screen.findByText('顾明澈')
    expect(screen.queryAllByText('秦若兰').length).toBeGreaterThan(0)

    await fireEvent.click(screen.getByRole('button', { name: /顾明澈/ }))

    await waitFor(() => expect(apiMocks.addMembers).toHaveBeenCalledWith('conv-direct', { target_ids: ['lead-agent'] }, expect.objectContaining({ idempotencyKey: expect.any(String) })))
    await screen.findByText('秦若兰、顾明澈')
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Convert to group chat' })).toBeNull())
  })

  it('loads more discussions when the list reaches the bottom and shows no-more text at the end', async () => {
    apiMocks.listDiscussions
      .mockResolvedValueOnce(discussionPage({ total: 21 }))
      .mockResolvedValueOnce(secondDiscussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    expect(apiMocks.listDiscussions.mock.calls[0][0]).toEqual({ page: 1, pageSize: 20 })

    const listScroller = container.querySelector('[data-discussion-list-scroller]') as HTMLDivElement
    Object.defineProperty(listScroller, 'scrollTop', { configurable: true, value: 800 })
    Object.defineProperty(listScroller, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(listScroller, 'scrollHeight', { configurable: true, value: 960 })
    await fireEvent.scroll(listScroller)

    await screen.findByText('Second page discussion')
    expect(apiMocks.listDiscussions.mock.calls[1][0]).toEqual({ page: 2, pageSize: 20 })
    expect(within(listScroller).getByText('No more.')).toBeTruthy()
    expect(screen.queryByText('Loading more discussions...')).toBeNull()
  })

  it('loads older discussion messages from the message endpoint when the timeline scrolls upward', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion({
      message_page: { limit: 20, offset: 0, total: 21, returned: 20, has_more: true }
    }))
    apiMocks.listDiscussionMessages.mockResolvedValue({
      page: { limit: 20, offset: 20, total: 21, returned: 1, has_more: false },
      messages: [
        {
          message_id: 'msg-older',
          discussion_id: 'conv-existing',
          role: 'user',
          author_id: 'operator',
          author_name: 'Operator',
          content: 'Older context',
          created_at: '2026-06-02T08:59:00Z'
        }
      ]
    })
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing mission is ready.')
    const timeline = container.querySelector('[data-discussion-timeline-scroller]') as HTMLDivElement
    Object.defineProperty(timeline, 'scrollTop', { configurable: true, value: 0 })
    await fireEvent.scroll(timeline)

    await screen.findByText('Older context')
    expect(apiMocks.listDiscussionMessages).toHaveBeenCalledWith('conv-existing', { limit: 20, offset: 20 })
    expect(screen.getAllByText('No more.').length).toBeGreaterThan(0)
  })

  it('deletes a discussion from the list after confirmation', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    apiMocks.listDiscussions.mockResolvedValueOnce(discussionPage()).mockResolvedValueOnce(emptyDiscussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.deleteDiscussion.mockResolvedValue(undefined)

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing launch')
    await fireEvent.click(screen.getByRole('button', { name: 'Open chat actions' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Delete chat' }))

    await waitFor(() => expect(apiMocks.deleteDiscussion).toHaveBeenCalledWith('conv-existing', expect.anything()))
    expect((await screen.findAllByText('No chat yet. Start a private or group chat.')).length).toBeGreaterThan(0)
  })

  it('selects the first remaining discussion after deleting the active discussion', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const remainingDiscussion = existingDiscussion({
      discussion_id: 'conv-next',
      title: 'Next launch',
      messages: [
        {
          message_id: 'msg-next',
          discussion_id: 'conv-next',
          role: 'assistant',
          author_id: 'agent-beta',
          author_name: 'Second Agent',
          content: 'Next room is ready.',
          created_at: '2026-06-02T09:02:00Z'
        }
      ],
      linked_missions: []
    })
    const deletedFetchesAfterDelete: string[] = []
    apiMocks.listDiscussions
      .mockResolvedValueOnce(discussionPageWithNext())
      .mockResolvedValueOnce({
        page: { page: 1, page_size: 20, total: 1 },
        discussions: [
          {
            discussion_id: 'conv-next',
            title: 'Next launch',
            status: 'completed',
            last_message: 'Next summary',
            linked_missions: 0,
            updated_at: '2026-06-02T09:02:00Z',
            unread_completed: false
          }
        ]
      })
    apiMocks.getDiscussion.mockImplementation((discussionId: string) => {
      if (apiMocks.deleteDiscussion.mock.calls.length > 0 && discussionId === 'conv-existing') {
        deletedFetchesAfterDelete.push(discussionId)
      }
      return Promise.resolve(discussionId === 'conv-next' ? remainingDiscussion : existingDiscussion())
    })
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.deleteDiscussion.mockResolvedValue(undefined)

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing mission is ready.')
    await fireEvent.click(screen.getAllByRole('button', { name: 'Open chat actions' })[0])
    await fireEvent.click(screen.getByRole('button', { name: 'Delete chat' }))

    await waitFor(() => expect(apiMocks.deleteDiscussion).toHaveBeenCalledWith('conv-existing', expect.anything()))
    await screen.findByText('Next room is ready.')
    expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-next', { limit: 20 })
    expect(deletedFetchesAfterDelete).toEqual([])
  })

  it('aborts the active discussion run from the discussion panel', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(runningDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.abortDiscussion.mockResolvedValue(abortedDiscussion())

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Runtime status: 顾明澈 is responding')
    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-plan',
      type: 'discussion.plan.updated',
      version: 1,
      occurred_at: '2026-06-02T09:01:30Z',
      payload: {
        conversation_id: 'conv-existing',
        run_id: 'run-active',
        plan: {
          plan_id: 'plan-active',
          status: 'active',
          items: [{ id: 'step-1', text: 'Prepare mission work', status: 'in_progress' }]
        }
      }
    })
    await screen.findByTestId('discussion-agent-work-card-run-active')

    await fireEvent.click(screen.getAllByRole('button', { name: 'Abort chat' })[0])

    await waitFor(() => expect(apiMocks.abortDiscussion).toHaveBeenCalledWith('conv-existing', { idempotencyKey: '00000000-0000-4000-8000-000000000099' }))
    await screen.findByText('Runtime status: cancelled')
  })

  it('switches the discussion HIL mode immediately without refetching the workspace', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion({ hil_mode: 'manual' }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    let resolveHILMode: ((discussion: DiscussionHILModeUpdateDTO) => void) | undefined
    apiMocks.setHILMode.mockImplementation(() => new Promise<DiscussionHILModeUpdateDTO>((resolve) => {
      resolveHILMode = resolve
    }))

    render(DiscussionWorkspaceHarness)

    const selector = await screen.findByRole('combobox', { name: 'HIL approval mode' }) as HTMLSelectElement
    const control = selector.closest('[data-discussion-hil-mode]')
    expect(selector.value).toBe('manual')
    await waitFor(() => expect(apiMocks.prewarmDiscussion).toHaveBeenCalledTimes(1))
    const detailRequestsBeforeSwitch = apiMocks.getDiscussion.mock.calls.length
    const listRequestsBeforeSwitch = apiMocks.listDiscussions.mock.calls.length
    const prewarmRequestsBeforeSwitch = apiMocks.prewarmDiscussion.mock.calls.length
    await fireEvent.change(selector, { target: { value: 'none' } })

    await waitFor(() => expect(apiMocks.setHILMode).toHaveBeenCalledWith(
      'conv-existing',
      { mode: 'none' },
      { idempotencyKey: '00000000-0000-4000-8000-000000000099' }
    ))
    expect(selector.value).toBe('none')
    await waitFor(() => expect(control?.getAttribute('aria-busy')).toBe('true'))
    expect(control?.querySelector('.animate-spin')).toBeNull()

    resolveHILMode?.({
      discussion_id: 'conv-existing',
      revision: 2,
      hil_mode: 'none',
      updated_at: '2026-06-02T09:03:00Z'
    })
    await waitFor(() => expect(control?.getAttribute('aria-busy')).toBe('false'))
    await tick()

    expect(apiMocks.getDiscussion).toHaveBeenCalledTimes(detailRequestsBeforeSwitch)
    expect(apiMocks.listDiscussions).toHaveBeenCalledTimes(listRequestsBeforeSwitch)
    expect(apiMocks.prewarmDiscussion).toHaveBeenCalledTimes(prewarmRequestsBeforeSwitch)
  })

  it('interjects into the specifically mentioned Agent while the run stays active', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(runningDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.interjectDiscussion.mockResolvedValue({
      message_id: 'msg-interject',
      discussion_id: 'conv-existing',
      turn_id: 'turn-active',
      run_id: 'run-active',
      role: 'user',
      author_id: 'operator',
      author_name: 'Operator',
      content: '@顾明澈 改用新的验收标准',
      mentions: [{ target_kind: 'agent', target_id: 'agent-alpha', display_name: '顾明澈' }],
      metadata: { delivery: 'runtime_steer' },
      created_at: '2026-06-02T09:02:00Z'
    })

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Runtime status: 顾明澈 is responding')
    const composer = screen.getByLabelText('Chat message') as HTMLTextAreaElement
    await fireEvent.input(composer, { target: { value: '@顾' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Mention 顾明澈' }))
    await fireEvent.input(composer, { target: { value: '@顾明澈 改用新的验收标准' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Steer active Agent' }))

    await waitFor(() => expect(apiMocks.interjectDiscussion).toHaveBeenCalledWith(
      'conv-existing',
      {
        content: '@顾明澈 改用新的验收标准',
        expected_turn_id: 'turn-active',
        expected_run_id: 'run-active',
        mentions: [{ target_kind: 'agent', target_id: 'agent-alpha', display_name: '顾明澈' }]
      },
      { idempotencyKey: '00000000-0000-4000-8000-000000000099' }
    ))
    expect((await screen.findAllByText('@顾明澈 改用新的验收标准')).length).toBeGreaterThan(0)
    expect(screen.getByText('Added to this run')).toBeTruthy()
    expect(screen.getByText('Runtime status: 顾明澈 is responding')).toBeTruthy()
  })

  it('shows current mission approvals inline in the discussion and submits decisions through the approval broker', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.listApprovals.mockResolvedValueOnce(approvalPage()).mockResolvedValue(emptyApprovalPage())

    render(DiscussionWorkspaceHarness)

    await screen.findByTestId('discussion-approval-card-approval-1')
    expect(apiMocks.listApprovals).toHaveBeenCalledWith('?mission_id=mission-existing&status=pending&limit=20')

    const inlinePanel = within(screen.getByTestId('discussion-approval-card-approval-1')).getByTestId('discussion-inline-approval-panel')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve once' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve for session' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Deny' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Other' })).toBeTruthy()

    await fireEvent.click(within(inlinePanel).getByRole('button', { name: 'Approve for session' }))

    await waitFor(() => expect(apiMocks.decideApproval).toHaveBeenCalledWith(
      'approval-1',
      {
        action_id: 'approve_session',
        remember: true,
        task_id: 'task-1'
      },
      { idempotencyKey: '00000000-0000-4000-8000-000000000099' },
      '/api/discussions/conv-existing/approvals/cmd-1/decision'
    ))
    const decidedCard = screen.getByTestId('discussion-approval-card-approval-1')
    await waitFor(() => expect(within(decidedCard).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
    expect(within(decidedCard).queryByRole('button', { name: 'Approve once' })).toBeNull()
    expect(within(decidedCard).queryByRole('button', { name: 'Approve for session' })).toBeNull()
    expect(apiMocks.getDiscussion).toHaveBeenCalledTimes(1)
    expect(apiMocks.listApprovals).toHaveBeenCalledTimes(1)
  })

  it('submits HIL-backed discussion approvals through the canonical HIL API', async () => {
    const pending = existingDiscussion({
      status: 'blocked',
      events: [{
        event_id: 'approval-hil-1',
        discussion_id: 'conv-existing',
        kind: 'discussion_runtime_approval_requested',
        status: 'pending',
        summary: 'Runtime approval required for knowledge_search',
        metadata: {
          approval_conversation_id: 'conv-existing',
          approval_request_id: 'call-1',
          approval_run_id: 'run-hil-1',
          approval_type: 'tool_use',
          approval_tool_name: 'knowledge_search',
          approval_summary: 'Search the approved knowledge base.',
          hil_decision_id: 'hil-decision-1'
        },
        created_at: '2026-08-28T02:43:09Z'
      }]
    })
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(pending)
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness)

    const cardId = 'discussion-approval-card-approval-hil-1'
    const card = await screen.findByTestId(cardId)
    await fireEvent.click(within(card).getByRole('button', { name: 'Approve' }))

    await waitFor(() => expect(apiMocks.decideHILDecision).toHaveBeenCalledWith(
      'hil-decision-1',
      { decision: 'approved', action_id: 'approve' },
      { idempotencyKey: '00000000-0000-4000-8000-000000000099' }
    ))
    expect(apiMocks.decideApproval).not.toHaveBeenCalled()
    await waitFor(() => expect(within(screen.getByTestId(cardId)).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
  })

  it('applies the returned discussion approval decision immediately instead of waiting for refetch', async () => {
    const pending = existingDiscussion({
      status: 'blocked',
      runs: [{
        run_id: 'run-approval',
        discussion_id: 'conv-existing',
        agent_id: 'agent-alpha',
        status: 'blocked',
        runtime_kind: 'claude',
        created_at: '2026-06-02T09:00:30Z',
        completed_at: '2026-06-02T09:01:00Z'
      }],
      events: [{
        event_id: 'discussion-runtime-approval-conv-existing-run-approval-cmd-1',
        discussion_id: 'conv-existing',
        kind: 'discussion_runtime_approval_requested',
        status: 'pending',
        summary: 'Runtime approval required for WebFetch',
        metadata: {
          approval_conversation_id: 'conv-existing',
          approval_request_id: 'cmd-1',
          approval_run_id: 'run-approval',
          approval_type: 'tool_use',
          approval_tool_name: 'WebFetch',
          approval_source: 'claude',
          url: 'https://github.com/openai/symphony/blob/main/SPEC.md'
        },
        available_actions: runtimeApprovalActions('/api/discussions/conv-existing/approvals/discussion-runtime-approval-conv-existing-run-approval-cmd-1/decision'),
        created_at: '2026-06-02T09:01:00Z'
      }]
    })
    const approved = existingDiscussion({
      status: 'running',
      runs: [
        ...(pending.runs ?? []),
        {
          run_id: 'run-continuation',
          discussion_id: 'conv-existing',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T09:02:00Z'
        }
      ],
      events: [{
        ...(pending.events ?? [])[0],
        status: 'approved',
        metadata: {
          ...(pending.events ?? [])[0].metadata,
          approval_decision: 'approved',
          approval_action_id: 'approve_once',
          approval_continuation_run_id: 'run-continuation'
        }
      }]
    })
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(pending)
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.decideApproval.mockResolvedValue(approved)

    render(DiscussionWorkspaceHarness)

    await screen.findByTestId('discussion-approval-card-discussion-runtime-approval-conv-existing-run-approval-cmd-1')
    await fireEvent.click(screen.getByRole('button', { name: 'Approve once' }))

    await waitFor(() => expect(apiMocks.decideApproval).toHaveBeenCalled())
    const card = screen.getByTestId('discussion-approval-card-discussion-runtime-approval-conv-existing-run-approval-cmd-1')
    await waitFor(() => expect(within(card).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
    await screen.findByText('Runtime status: Agent is responding')
    expect(within(card).queryByRole('button', { name: 'Approve once' })).toBeNull()
    expect(within(card).queryByText('https://github.com/openai/symphony/blob/main/SPEC.md')).toBeNull()
    expect(apiMocks.getDiscussion).toHaveBeenCalledTimes(1)
    expect(apiMocks.listDiscussions).toHaveBeenCalledTimes(1)
    expect(apiMocks.listApprovals).toHaveBeenCalledTimes(1)
  })

  it('submits concurrent inline approvals independently without rolling back decided cards', async () => {
    const pending = discussionWithRuntimeApprovals(['cmd-a', 'cmd-b'])
    const approvedA = discussionWithRuntimeApprovals(['cmd-a', 'cmd-b'], { 'cmd-a': 'approved' })
    const approvedB = discussionWithRuntimeApprovals(['cmd-a', 'cmd-b'], { 'cmd-b': 'approved' })
    const decisions: Record<string, (value: unknown) => void> = {}
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(pending)
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.decideApproval.mockImplementation((approvalId: string) => new Promise((resolve) => {
      decisions[approvalId] = resolve
    }))

    render(DiscussionWorkspaceHarness)

    const cardAId = 'discussion-approval-card-discussion-runtime-approval-conv-existing-run-cmd-a-cmd-a'
    const cardBId = 'discussion-approval-card-discussion-runtime-approval-conv-existing-run-cmd-b-cmd-b'
    const cardA = await screen.findByTestId(cardAId)
    const cardB = await screen.findByTestId(cardBId)
    const currentCardA = () => screen.getByTestId(cardAId)
    const currentCardB = () => screen.getByTestId(cardBId)

    await fireEvent.click(within(cardA).getByRole('button', { name: 'Approve once' }))
    await fireEvent.click(within(cardB).getByRole('button', { name: 'Approve once' }))

    await waitFor(() => expect(apiMocks.decideApproval).toHaveBeenCalledTimes(2))
    expect(apiMocks.decideApproval.mock.calls.map((call) => call[0])).toEqual([
      'discussion-runtime-approval-conv-existing-run-cmd-a-cmd-a',
      'discussion-runtime-approval-conv-existing-run-cmd-b-cmd-b'
    ])
    await waitFor(() => expect(within(currentCardA()).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
    await waitFor(() => expect(within(currentCardB()).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
    expect(within(currentCardA()).queryByRole('button', { name: 'Approve once' })).toBeNull()
    expect(within(currentCardB()).queryByRole('button', { name: 'Approve once' })).toBeNull()

    decisions['discussion-runtime-approval-conv-existing-run-cmd-a-cmd-a'](approvedA)
    await waitFor(() => expect(within(currentCardA()).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
    expect(within(currentCardB()).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved')

    decisions['discussion-runtime-approval-conv-existing-run-cmd-b-cmd-b'](approvedB)
    await waitFor(() => expect(within(currentCardB()).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved'))
    expect(within(currentCardA()).getByTestId('discussion-approval-compact-status').textContent).toContain('Approved')
    expect(within(currentCardA()).queryByRole('button', { name: 'Approve once' })).toBeNull()
    expect(within(currentCardB()).queryByRole('button', { name: 'Approve once' })).toBeNull()
  })

  it('refreshes only discussion notification badges when opening a discussion', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    try {
      render(DiscussionWorkspaceHarness)
      await screen.findByText('Existing mission is ready.')
      await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['notification-badges', 'discussions'] }))
      expect(invalidate).not.toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['notification-badges'] }))
      expect(invalidate).not.toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['notification-badges', 'approvals'] }))
    } finally {
      invalidate.mockRestore()
    }
  })

  it('refreshes inline approvals when the active discussion stream advances', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.listApprovals.mockResolvedValueOnce(emptyApprovalPage()).mockResolvedValue(approvalPage())

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing mission is ready.')
    await waitFor(() => expect(apiMocks.listApprovals).toHaveBeenCalledWith('?mission_id=mission-existing&status=pending&limit=20'))
    expect(screen.queryByTestId('discussion-approval-card-approval-1')).toBeNull()

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-approval',
      type: 'discussion.run.completed',
      version: 1,
      payload: {
        run_id: 'run-approval',
        conversation_id: 'conv-existing',
        agent_id: 'agent-alpha',
        status: 'blocked'
      }
    })

    await screen.findByTestId('discussion-approval-card-approval-1')
    expect(apiMocks.listApprovals.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('refreshes inline approvals when the active Mission stream advances', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.listApprovals.mockResolvedValueOnce(emptyApprovalPage()).mockResolvedValue(approvalPage())

    render(DiscussionWorkspaceHarness)

    await screen.findByText('Existing mission is ready.')
    await waitFor(() => expect(missionStreamMocks.connectMissionStream).toHaveBeenCalledWith('mission-existing'))
    expect(screen.queryByTestId('discussion-approval-card-approval-1')).toBeNull()

    missionStreamMocks.handlers.get('patch')?.(new Event('patch'))

    await screen.findByTestId('discussion-approval-card-approval-1')
    expect(apiMocks.listApprovals.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('renders a runtime approval from a websocket snapshot invalidation without a page refresh', async () => {
    const approvalDiscussion = existingDiscussion({
      revision: 2,
      status: 'running',
      runs: [{
        run_id: 'run-live-approval',
        discussion_id: 'conv-existing',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-02T09:00:30Z'
      }],
      events: [{
        event_id: 'discussion-runtime-approval-conv-existing-run-live-approval-cmd-1',
        discussion_id: 'conv-existing',
        kind: 'discussion_runtime_approval_requested',
        status: 'pending',
        summary: 'Runtime approval required for WebFetch',
        metadata: {
          approval_conversation_id: 'conv-existing',
          approval_request_id: 'cmd-1',
          approval_run_id: 'run-live-approval',
          approval_agent_id: 'agent-alpha',
          approval_type: 'tool_use',
          approval_tool_name: 'WebFetch',
          approval_source: 'claude',
          url: 'https://github.com/openai/symphony/blob/main/SPEC.md'
        },
        available_actions: runtimeApprovalActions('/api/discussions/conv-existing/approvals/discussion-runtime-approval-conv-existing-run-live-approval-cmd-1/decision'),
        created_at: '2026-06-02T09:01:00Z'
      }]
    })
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion
      .mockResolvedValueOnce(existingDiscussion())
      .mockResolvedValue(approvalDiscussion)
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.listApprovals.mockResolvedValue(emptyApprovalPage())

    render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    expect(screen.queryByTestId('discussion-approval-card-discussion-runtime-approval-conv-existing-run-live-approval-cmd-1')).toBeNull()

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-runtime-approval-requested',
      type: 'discussion.snapshot.invalidated',
      version: 1,
      payload: {
        conversation_id: 'conv-existing',
        reason: 'runtime_approval_requested'
      }
    })

    await screen.findByTestId('discussion-approval-card-discussion-runtime-approval-conv-existing-run-live-approval-cmd-1')
  })

  it('stops automatic snapshot refreshes after a forbidden response for the active discussion', async () => {
    const nextDiscussion = existingDiscussion({
      discussion_id: 'conv-next',
      title: 'Next launch',
      messages: [{
        message_id: 'msg-next',
        discussion_id: 'conv-next',
        role: 'assistant',
        author_id: 'agent-beta',
        author_name: 'Second Agent',
        content: 'Next room is ready.',
        created_at: '2026-06-02T09:02:00Z'
      }],
      linked_missions: []
    })
    let existingCalls = 0
    apiMocks.listDiscussions.mockResolvedValue(discussionPageWithNext())
    apiMocks.getDiscussion.mockImplementation((discussionId: string) => {
      if (discussionId === 'conv-next') return Promise.resolve(nextDiscussion)
      existingCalls += 1
      if (existingCalls === 1) return Promise.resolve(existingDiscussion())
      return Promise.reject(new ApiClientError({ status: 403, code: 'forbidden', message: 'permission denied' }))
    })
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-forbidden-snapshot-1',
      type: 'discussion.snapshot.invalidated',
      version: 1,
      payload: { conversation_id: 'conv-existing', reason: 'authorization_changed' }
    })
    await waitFor(() => expect(existingCalls).toBe(2))

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-forbidden-snapshot-2',
      type: 'discussion.snapshot.invalidated',
      version: 2,
      payload: { conversation_id: 'conv-existing', reason: 'authorization_changed' }
    })
    await tick()
    expect(existingCalls).toBe(2)

    await fireEvent.click(screen.getByText('Next launch'))
    await screen.findByText('Next room is ready.')
    expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-next', { limit: 20 })
  })

  it('keeps correlated websocket progress while an HTTP send stream is rendering', async () => {
    let releaseStream: (() => void) | undefined
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion
      .mockResolvedValueOnce(existingDiscussion())
      .mockResolvedValue(existingDiscussion({
        revision: 3,
        status: 'running',
        messages: [
          ...(existingDiscussion().messages ?? []),
          {
            message_id: 'msg-refetched',
            discussion_id: 'conv-existing',
            role: 'assistant',
            author_id: 'agent-alpha',
            author_name: 'Lead Agent',
            content: 'Snapshot refresh survived the pending HTTP stream.',
            created_at: '2026-06-02T09:01:30Z'
          }
        ]
      }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      () => new Promise<void>((resolve) => {
        releaseStream = resolve
      })
    )

    render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    await waitFor(() => expect(realtimeMocks.client.connect).toHaveBeenCalledTimes(1))
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Continue with the spec' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))

    expect(realtimeMocks.subscription.unsubscribe).not.toHaveBeenCalled()
    expect(realtimeMocks.client.disconnect).not.toHaveBeenCalled()

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-live-run-started',
      type: 'discussion.run.started',
      version: 1,
      payload: {
        run_id: 'run-active',
        turn_id: 'turn-live',
        conversation_id: 'conv-existing',
        agent_id: 'agent-alpha',
        status: 'running'
      }
    })
    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-live-text',
      type: 'discussion.token.delta',
      version: 2,
      payload: {
        run_id: 'run-active',
        turn_id: 'turn-live',
        conversation_id: 'conv-existing',
        agent_id: 'agent-alpha',
        delta: 'Let me load this document another way.'
      }
    })

    await tick()
    expect(screen.getByText('Let me load this document another way.')).toBeTruthy()

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-live-snapshot-invalidated',
      type: 'discussion.snapshot.invalidated',
      version: 3,
      payload: {
        conversation_id: 'conv-existing',
        reason: 'runtime_approval_requested'
      }
    })

    await waitFor(() => expect(apiMocks.getDiscussion.mock.calls.length).toBeGreaterThanOrEqual(2))
    releaseStream?.()
  })

  it('keeps runtime setup guidance after a failed snapshot refresh drops the error detail', async () => {
    const runtimeSetupError = 'discussion runtime unavailable: runtime kind "pi": exec: "pi": executable file not found'
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion
      .mockResolvedValueOnce(existingDiscussion())
      .mockResolvedValue(existingDiscussion({
        revision: 3,
        status: 'failed',
        updated_at: '2026-06-02T09:02:00Z',
        runs: [{
          run_id: 'run-failed',
          discussion_id: 'conv-existing',
          agent_id: 'agent-alpha',
          status: 'failed',
          runtime_kind: 'pi',
          created_at: '2026-06-02T09:01:00Z',
          completed_at: '2026-06-02T09:02:00Z'
        }],
        events: []
      }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'error', event_id: 'evt-runtime-setup-error', discussion_id: 'conv-existing', turn_id: 'turn-runtime-setup', revision: 2, error: runtimeSetupError })
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Continue with the spec' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(apiMocks.getDiscussion).toHaveBeenCalledTimes(2))

    expect(screen.getByText(/Agent runtime needs setup/)).toBeTruthy()
    expect(screen.queryByText('Message send failed')).toBeNull()
    expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('/settings')
  })

  it('keeps runtime execution diagnostics when the stream later rejects with a generic failure', async () => {
    const runtimeError = 'discussion runtime execution failed: runtime kind "pi": summary cannot be generated without runtime output, deliverables, or evidence'
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion.mockResolvedValue(existingDiscussion())
    apiMocks.getMissionControl.mockResolvedValue(missionControl())
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: { content: string }, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({ kind: 'error', event_id: 'evt-runtime-error', discussion_id: 'conv-existing', turn_id: 'turn-runtime-error', revision: 2, error: runtimeError })
        throw new Error('Message send failed')
      }
    )

    render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    await fireEvent.input(screen.getByLabelText('Chat message'), { target: { value: 'Continue with the spec' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.sendMessageStream).toHaveBeenCalledTimes(1))

    expect(screen.getByText(/summary cannot be generated/)).toBeTruthy()
    expect(screen.queryByText('Message send failed')).toBeNull()
    expect(screen.queryByRole('link', { name: 'Open Settings' })).toBeNull()
  })

  it('uses websocket snapshot invalidations to replace stale local streaming state in the main pane', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion
      .mockResolvedValueOnce(existingDiscussion())
      .mockResolvedValue(existingDiscussion({
        revision: 3,
        messages: [
          ...(existingDiscussion().messages ?? []),
          {
            message_id: 'msg-live',
            discussion_id: 'conv-existing',
            role: 'assistant',
            author_id: 'agent-alpha',
            author_name: 'Lead Agent',
            content: '让我换一种方式加载这个文档。',
            created_at: '2026-06-02T09:01:00Z'
          }
        ],
        status: 'running',
        runs: [{
          run_id: 'run-active',
          discussion_id: 'conv-existing',
          turn_id: 'turn-1',
          mode: 'group',
          has_plan: true,
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T09:00:30Z'
        }]
      }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-run-before-refetch',
      type: 'discussion.run.started',
      version: 1,
      payload: {
        run_id: 'run-active',
        conversation_id: 'conv-existing',
        agent_id: 'agent-alpha',
        status: 'running'
      }
    })
    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-plan-before-refetch',
      type: 'discussion.plan.updated',
      version: 2,
      payload: {
        run_id: 'run-active',
        conversation_id: 'conv-existing',
        agent_id: 'agent-alpha',
        plan: {
          plan_id: 'plan-active',
          status: 'active',
          items: [{ id: 'step-1', text: 'Inspect runtime approval', status: 'in_progress' }]
        }
      }
    })
    await screen.findByTestId('discussion-agent-work-card-run-active')

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-snapshot-invalidated',
      type: 'discussion.snapshot.invalidated',
      version: 3,
      payload: {
        conversation_id: 'conv-existing',
        reason: 'runtime_approval_decided'
      }
    })

    const timeline = container.querySelector('[data-discussion-timeline-scroller]') as HTMLElement
    await waitFor(() => expect(timeline.textContent).toContain('让我换一种方式加载这个文档。'))
    expect(timeline.textContent).toContain('View work process')
  })

  it('keeps locally merged websocket messages in chronological order after a snapshot refetch', async () => {
    apiMocks.listDiscussions.mockResolvedValue(discussionPage())
    apiMocks.getDiscussion
      .mockResolvedValueOnce(existingDiscussion({
        status: 'running',
        runs: [{
          run_id: 'run-active',
          discussion_id: 'conv-existing',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T09:00:30Z'
        }]
      }))
      .mockResolvedValue(existingDiscussion({
        revision: 2,
        status: 'running',
        messages: [
          {
            message_id: 'msg-1',
            discussion_id: 'conv-existing',
            role: 'assistant',
            author_id: 'agent-alpha',
            author_name: 'Lead Agent',
            content: 'Existing mission is ready.',
            created_at: '2026-06-02T09:00:00Z'
          },
          {
            message_id: 'msg-2',
            discussion_id: 'conv-existing',
            role: 'assistant',
            author_id: 'agent-beta',
            author_name: 'Second Agent',
            content: 'Snapshot message after the local stream.',
            created_at: '2026-06-02T09:01:30Z'
          }
        ],
        runs: [{
          run_id: 'run-active',
          discussion_id: 'conv-existing',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T09:00:30Z'
        }]
      }))
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    const { container } = render(DiscussionWorkspaceHarness)

    await screen.findAllByText('Existing mission is ready.')
    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-token-before-snapshot',
      type: 'discussion.token.delta',
      version: 1,
      payload: {
        run_id: 'run-active',
        conversation_id: 'conv-existing',
        agent_id: 'agent-alpha',
        delta: 'Local stream before snapshot message.'
      }
    })
    await screen.findByText('Local stream before snapshot message.')

    emitDiscussionRealtimeEnvelope({
      event_id: 'evt-snapshot-invalidated-for-order',
      type: 'discussion.snapshot.invalidated',
      version: 2,
      payload: {
        conversation_id: 'conv-existing',
        reason: 'snapshot_order'
      }
    })

    const timeline = container.querySelector('[data-discussion-timeline-scroller]') as HTMLElement
    await waitFor(() => expect(timeline.textContent).toContain('Snapshot message after the local stream.'))
    expect(timeline.textContent?.indexOf('Local stream before snapshot message.')).toBeLessThan(
      timeline.textContent?.indexOf('Snapshot message after the local stream.') ?? -1
    )
  })

  it('selects a mission-linked discussion from the initial discussion id', async () => {
    apiMocks.listDiscussions.mockResolvedValue({
      page: { page: 1, page_size: 20, total: 2 },
      discussions: [
        {
          discussion_id: 'conv-first',
          title: 'First discussion',
          status: 'completed',
          linked_missions: 0,
          updated_at: '2026-06-02T08:00:00Z',
          unread_completed: false
        },
        {
          discussion_id: 'conv-mission',
          title: 'Mission discussion',
          status: 'completed',
          linked_missions: 1,
          updated_at: '2026-06-02T09:00:00Z',
          unread_completed: false
        }
      ]
    })
    apiMocks.getDiscussion.mockImplementation((discussionId: string) =>
      Promise.resolve(existingDiscussion({
        discussion_id: discussionId,
        title: discussionId === 'conv-mission' ? 'Mission discussion' : 'First discussion',
        messages: [
          {
            message_id: 'msg-selected',
            discussion_id: discussionId,
            role: 'assistant',
            author_id: 'agent-alpha',
            author_name: 'Lead Agent',
            content: discussionId === 'conv-mission' ? 'Mission room opened.' : 'Wrong room opened.',
            created_at: '2026-06-02T09:00:00Z'
          }
        ]
      }))
    )
    apiMocks.getMissionControl.mockResolvedValue(missionControl())

    render(DiscussionWorkspaceHarness, { props: { initialDiscussionId: 'conv-mission' } })

    await screen.findByText('Mission room opened.')
    expect(screen.queryByText('Wrong room opened.')).toBeNull()
    expect(apiMocks.getDiscussion).toHaveBeenCalledWith('conv-mission', { limit: 20 })
  })
})

function emitDiscussionRealtimeEnvelope(overrides: Partial<RealtimeEnvelope> = {}) {
  const listener = realtimeMocks.handlers.get('publication')
  const payload = {
    conversation_id: overrides.aggregate_id ?? 'conv-existing',
    turn_id: 'turn-1',
    run_id: 'run-active',
    revision: 2,
    sequence: 1,
    ...(overrides.payload ?? {})
  }
  listener?.({
    data: {
      event_id: 'evt-1',
      type: 'discussion.message.created',
      channel: 'conversation:conv-existing',
      aggregate_type: 'conversation',
      aggregate_id: 'conv-existing',
      version: 1,
      occurred_at: '2026-06-02T09:01:00Z',
      ...overrides,
      payload
    }
  })
}

function discussionPage(page: { page?: number; pageSize?: number; total?: number } = {}): DiscussionPageDTO {
  return {
    page: { page: page.page ?? 1, page_size: page.pageSize ?? 20, total: page.total ?? 1 },
    discussions: [
      {
        discussion_id: 'conv-existing',
        title: 'Existing launch',
        status: 'completed',
        last_message: 'Keep going',
        linked_missions: 1,
        updated_at: '2026-06-02T09:00:00Z',
        unread_completed: false
      }
    ]
  }
}

function discussionPageWithAttachmentCount(): DiscussionPageDTO {
  const page = discussionPage()
  return {
    ...page,
    discussions: page.discussions.map((discussion) => ({ ...discussion, attachment_count: 1 }))
  }
}

function secondDiscussionPage(): DiscussionPageDTO {
  return {
    page: { page: 2, page_size: 20, total: 21 },
    discussions: [
      {
        discussion_id: 'conv-second',
        title: 'Second page discussion',
        status: 'completed',
        last_message: 'Older work',
        linked_missions: 0,
        updated_at: '2026-06-01T09:00:00Z',
        unread_completed: false
      }
    ]
  }
}

function discussionPageWithNext(): DiscussionPageDTO {
  return {
    page: { page: 1, page_size: 20, total: 2 },
    discussions: [
      {
        discussion_id: 'conv-existing',
        title: 'Existing launch',
        status: 'completed',
        last_message: 'Keep going',
        linked_missions: 1,
        updated_at: '2026-06-02T09:00:00Z',
        unread_completed: false
      },
      {
        discussion_id: 'conv-next',
        title: 'Next launch',
        status: 'completed',
        last_message: 'Next summary',
        linked_missions: 0,
        updated_at: '2026-06-02T09:02:00Z',
        unread_completed: false
      }
    ]
  }
}

function emptyDiscussionPage(): DiscussionPageDTO {
  return {
    page: { page: 1, page_size: 20, total: 0 },
    discussions: []
  }
}

function discussionPageWithPersonalAssistantHistory(): DiscussionPageDTO {
  return {
    page: { page: 1, page_size: 20, total: 2 },
    discussions: [
      {
        discussion_id: 'conv-assistant-latest',
        conversation_kind: 'personal_assistant',
        title: 'Personal Assistant',
        status: 'completed',
        last_message: 'Latest WeChat reply',
        linked_missions: 0,
        updated_at: '2026-08-17T12:21:00Z',
        unread_completed: false
      },
      {
        discussion_id: 'conv-assistant-older',
        conversation_kind: 'personal_assistant',
        title: 'Personal Assistant',
        status: 'completed',
        last_message: 'Earlier WeChat reply',
        linked_missions: 0,
        updated_at: '2026-08-17T10:32:00Z',
        unread_completed: false
      }
    ]
  }
}

function discussionPageWithDirectAgentHistory(): DiscussionPageDTO {
  return {
    page: { page: 1, page_size: 20, total: 2 },
    discussions: [
      {
        discussion_id: 'conv-direct-latest',
        conversation_kind: 'direct',
        direct_agent_id: 'staffing-agent',
        direct_target_kind: 'agent',
        direct_target_id: 'staffing-agent',
        title: '秦若兰',
        status: 'completed',
        last_message: 'Latest direct Agent answer',
        linked_missions: 0,
        updated_at: '2026-08-17T12:21:00Z',
        unread_completed: false
      },
      {
        discussion_id: 'conv-direct-older',
        conversation_kind: 'direct',
        direct_agent_id: 'staffing-agent',
        direct_target_kind: 'agent',
        direct_target_id: 'staffing-agent',
        title: '秦若兰',
        status: 'completed',
        last_message: 'Earlier direct Agent answer',
        linked_missions: 0,
        updated_at: '2026-08-17T10:32:00Z',
        unread_completed: false
      }
    ]
  }
}

function personalAssistantDiscussion(discussionId: string): DiscussionDTO {
  const older = discussionId === 'conv-assistant-older'
  return existingDiscussion({
    discussion_id: discussionId,
    conversation_kind: 'personal_assistant',
    title: 'Personal Assistant',
    updated_at: older ? '2026-08-17T10:32:00Z' : '2026-08-17T12:21:00Z',
    messages: [{
      message_id: older ? 'msg-assistant-older' : 'msg-assistant-latest',
      discussion_id: discussionId,
      role: 'assistant',
      author_id: 'personal-assistant',
      author_name: 'Personal Assistant',
      content: older ? 'Earlier assistant answer' : 'Latest assistant answer',
      created_at: older ? '2026-08-17T10:32:00Z' : '2026-08-17T12:21:00Z'
    }],
    linked_missions: []
  })
}

function directDiscussionSession(discussionId: string): DiscussionDTO {
  const older = discussionId === 'conv-direct-older'
  const content = older ? 'Earlier direct Agent answer' : 'Latest direct Agent answer'
  const discussion = directDiscussion(content)
  return {
    ...discussion,
    discussion_id: discussionId,
    updated_at: older ? '2026-08-17T10:32:00Z' : '2026-08-17T12:21:00Z',
    messages: [{
      message_id: older ? 'msg-direct-older' : 'msg-direct-latest',
      discussion_id: discussionId,
      role: 'assistant',
      author_id: 'staffing-agent',
      author_name: '秦若兰',
      content,
      created_at: older ? '2026-08-17T10:32:00Z' : '2026-08-17T12:21:00Z'
    }],
    runs: []
  }
}

function existingDiscussion(overrides: Partial<DiscussionDTO> = {}): DiscussionDTO {
  const base = {
    discussion_id: 'conv-existing',
    revision: 1,
    title: 'Existing launch',
    status: 'completed',
    created_by: 'operator',
    created_by_name: 'Operator',
    created_at: '2026-06-02T09:00:00Z',
    updated_at: '2026-06-02T09:00:00Z',
    unread_completed: false,
    messages: [
      {
        message_id: 'msg-1',
        discussion_id: 'conv-existing',
        role: 'assistant',
        author_id: 'agent-alpha',
        author_name: 'Lead Agent',
        content: 'Existing mission is ready.',
        created_at: '2026-06-02T09:00:00Z'
      }
    ],
    runs: [],
    linked_missions: [
      {
        mission_id: 'mission-existing',
        title: 'Existing mission',
        status: 'active',
        created_at: '2026-06-02T09:00:00Z'
      }
    ]
  }
  return { ...base, ...overrides }
}

function createdMissionDiscussion(content = ''): DiscussionDTO {
  return {
    discussion_id: 'conv-mission-created',
    revision: content ? 2 : 1,
    title: 'Validate the next customer cohort',
    status: content ? 'completed' : 'active',
    created_by: 'operator',
    created_by_name: 'Operator',
    created_at: '2026-08-08T09:00:00Z',
    updated_at: '2026-08-08T09:00:00Z',
    unread_completed: false,
    messages: content ? [{
      message_id: 'msg-mission-created',
      discussion_id: 'conv-mission-created',
      role: 'user',
      author_id: 'operator',
      author_name: 'Operator',
      content,
      created_at: '2026-08-08T09:00:00Z'
    }] : [],
    members: [
      { kind: 'agent', id: 'lead-agent', display_name: 'Lead', role: 'Lead' },
      { kind: 'user', id: 'operator', display_name: 'Operator', role: 'Owner' }
    ],
    runs: [],
    linked_missions: [{
      mission_id: 'mission-created',
      title: 'Validate the next customer cohort',
      status: 'active',
      created_at: '2026-08-08T09:00:00Z'
    }]
  }
}

function runningDiscussion(): DiscussionDTO {
  return {
    ...existingDiscussion(),
    status: 'running',
    runs: [
      {
        run_id: 'run-active',
        discussion_id: 'conv-existing',
        turn_id: 'turn-active',
        mode: 'group',
        has_plan: true,
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'managed',
        tool_calls: [{ tool_call_id: 'tool-active', name: 'run_terminal', status: 'running', created_at: '2026-06-02T09:01:00Z' }],
        created_at: '2026-06-02T09:01:00Z'
      }
    ],
    members: [
      { kind: 'agent', id: 'agent-alpha', display_name: '顾明澈', role: 'Lead' }
    ]
  }
}

function discussionWithRuntimeApprovals(requestIds: string[], statuses: Record<string, string> = {}): DiscussionDTO {
  return existingDiscussion({
    revision: 1 + Object.values(statuses).filter((status) => status === 'approved').length,
    status: Object.values(statuses).some((status) => status === 'approved') ? 'running' : 'blocked',
    runs: requestIds.map((requestId) => ({
      run_id: `run-${requestId}`,
      discussion_id: 'conv-existing',
      agent_id: 'agent-alpha',
      status: statuses[requestId] === 'approved' ? 'running' : 'blocked',
      runtime_kind: 'claude',
      created_at: '2026-06-02T09:00:30Z'
    })),
    events: requestIds.map((requestId, index) => {
      const eventID = `discussion-runtime-approval-conv-existing-run-${requestId}-${requestId}`
      const status = statuses[requestId] ?? 'pending'
      return {
        event_id: eventID,
        discussion_id: 'conv-existing',
        kind: 'discussion_runtime_approval_requested',
        status,
        summary: `Runtime approval required for ${requestId}`,
        metadata: {
          approval_conversation_id: 'conv-existing',
          approval_request_id: requestId,
          approval_run_id: `run-${requestId}`,
          approval_type: 'tool_use',
          approval_tool_name: 'WebFetch',
          approval_source: 'claude',
          approval_decision: status === 'approved' ? 'approved' : '',
          approval_action_id: status === 'approved' ? 'approve_once' : '',
          url: `https://example.test/${requestId}`
        },
        available_actions: runtimeApprovalActions(`/api/discussions/conv-existing/approvals/${eventID}/decision`),
        created_at: `2026-06-02T09:01:0${index}Z`
      }
    })
  })
}

function abortedDiscussion(): DiscussionDTO {
  return {
    ...runningDiscussion(),
    revision: 2,
    status: 'cancelled',
    runs: [
      {
        run_id: 'run-active',
        discussion_id: 'conv-existing',
        agent_id: 'agent-alpha',
        status: 'cancelled',
        runtime_kind: 'managed',
        created_at: '2026-06-02T09:01:00Z',
        completed_at: '2026-06-02T09:02:00Z'
      }
    ],
    events: [
      {
        event_id: 'event-abort',
        discussion_id: 'conv-existing',
        kind: 'discussion_aborted',
        status: 'cancelled',
        summary: '用户中止了讨论流程',
        metadata: { run_id: 'run-active', aborted_by: 'operator' },
        created_at: '2026-06-02T09:02:00Z'
      }
    ]
  }
}

function retryableDiscussion(): DiscussionDTO {
  return {
    ...existingDiscussion(),
    messages: [
      {
        message_id: 'msg-user',
        discussion_id: 'conv-existing',
        role: 'user',
        author_id: 'operator',
        author_name: 'Local User',
        content: 'Use HTML',
        created_at: '2026-06-02T09:00:00Z'
      },
      {
        message_id: 'msg-assistant',
        discussion_id: 'conv-existing',
        role: 'assistant',
        author_id: 'agent-alpha',
        author_name: 'Lead Agent',
        content: 'Existing mission is ready.',
        created_at: '2026-06-02T09:01:00Z'
      }
    ]
  }
}

function retryPendingDiscussion(): DiscussionDTO {
  return {
    ...retryableDiscussion(),
    revision: 2,
    status: 'retry_pending',
    events: [
      {
        event_id: 'retry-1',
        discussion_id: 'conv-existing',
        kind: 'message_retry_requested',
        status: 'pending',
        metadata: { message_id: 'msg-user' },
        created_at: '2026-06-02T09:02:00Z'
      }
    ]
  }
}

function newDiscussion(): DiscussionDTO {
  return {
    discussion_id: 'conv-new',
    revision: 1,
    title: 'Start a fresh intake',
    status: 'running',
    created_by: 'operator',
    created_at: '2026-06-02T10:00:00Z',
    updated_at: '2026-06-02T10:00:00Z',
    unread_completed: false,
    messages: [],
    runs: [],
    linked_missions: []
  }
}

function newDiscussionWithAttachment(): DiscussionDTO {
  return {
    ...newDiscussion(),
    title: 'brief.txt',
    status: 'completed',
    members: [
      { kind: 'agent', id: 'lead-agent', display_name: '顾明澈', role: 'Lead' },
      { kind: 'agent', id: 'staffing-agent', display_name: '秦若兰', role: 'Staffing' },
      { kind: 'user', id: 'operator', display_name: 'Operator', role: '话题发起人' }
    ],
    attachments: [{
      attachment_id: 'att-draft',
      discussion_id: 'conv-new',
      file_name: 'brief.txt',
      content_type: 'text/plain',
      size_bytes: 12,
      uploaded_by: 'operator',
      uploaded_by_kind: 'user',
      uploaded_by_name: 'Operator',
      download_url: '/api/discussions/conv-new/attachments/att-draft/download',
      created_at: '2026-06-02T10:00:00Z'
    }]
  }
}

function continuedDiscussion(lastMessage: string): DiscussionDTO {
  return {
    discussion_id: 'conv-new',
    revision: 2,
    title: 'Start a fresh intake',
    status: 'completed',
    created_by: 'operator',
    created_at: '2026-06-02T10:00:00Z',
    updated_at: '2026-06-02T10:00:00Z',
    unread_completed: false,
    messages: [
      {
        message_id: `msg-${lastMessage.replace(/\W+/g, '-').toLowerCase()}`,
        discussion_id: 'conv-new',
        role: 'user',
        author_id: 'operator',
        author_name: 'Operator',
        content: lastMessage,
        created_at: '2026-06-02T10:00:00Z'
      }
    ],
    runs: [
      {
        run_id: `run-${lastMessage.replace(/\W+/g, '-').toLowerCase()}`,
        discussion_id: 'conv-new',
        agent_id: 'agent-alpha',
        status: 'completed',
        runtime_kind: 'codex',
        tool_calls: [],
        created_at: '2026-06-02T10:00:01Z'
      }
    ],
    linked_missions: []
  }
}

function failedNewDiscussion(lastMessage: string): DiscussionDTO {
  return {
    ...continuedDiscussion(lastMessage),
    status: 'failed',
    runs: [
      {
        run_id: `run-${lastMessage.replace(/\W+/g, '-').toLowerCase()}`,
        discussion_id: 'conv-new',
        agent_id: 'agent-alpha',
        status: 'failed',
        runtime_kind: 'managed',
        tool_calls: [],
        created_at: '2026-06-02T10:00:01Z',
        completed_at: '2026-06-02T10:00:02Z'
      }
    ]
  }
}

function directDiscussion(lastMessage: string): DiscussionDTO {
  return {
    discussion_id: 'conv-direct',
    revision: 1,
    conversation_kind: 'direct',
    direct_agent_id: 'staffing-agent',
    direct_target_kind: 'agent',
    direct_target_id: 'staffing-agent',
    title: '秦若兰',
    status: 'completed',
    created_by: 'operator',
    created_at: '2026-06-02T10:00:00Z',
    updated_at: '2026-06-02T10:00:00Z',
    unread_completed: false,
    members: [
      { kind: 'agent', id: 'staffing-agent', display_name: '秦若兰', role: '私聊对象' },
      { kind: 'user', id: 'operator', display_name: 'Operator', role: '话题发起人' }
    ],
    messages: [
      {
        message_id: 'msg-direct-user',
        discussion_id: 'conv-direct',
        role: 'user',
        author_id: 'operator',
        author_name: 'Operator',
        content: lastMessage,
        created_at: '2026-06-02T10:00:00Z'
      },
      {
        message_id: 'msg-direct-agent',
        discussion_id: 'conv-direct',
        role: 'assistant',
        author_id: 'staffing-agent',
        author_name: '秦若兰',
        content: '我来处理。',
        created_at: '2026-06-02T10:00:01Z'
      }
    ],
    runs: [
      {
        run_id: 'run-direct',
        discussion_id: 'conv-direct',
        agent_id: 'staffing-agent',
        status: 'completed',
        runtime_kind: 'codex',
        created_at: '2026-06-02T10:00:01Z'
      }
    ],
    linked_missions: []
  }
}

function directDiscussionWithRuntimeApproval(lastMessage: string): DiscussionDTO {
  const eventID = 'discussion-runtime-approval-conv-direct-run-direct-cmd-1'
  return {
    ...directDiscussion(lastMessage),
    status: 'blocked',
    messages: [
      {
        message_id: 'msg-direct-user',
        discussion_id: 'conv-direct',
        role: 'user',
        author_id: 'operator',
        author_name: 'Operator',
        content: lastMessage,
        created_at: '2026-06-02T10:00:00Z'
      }
    ],
    runs: [
      {
        run_id: 'run-direct',
        discussion_id: 'conv-direct',
        agent_id: 'staffing-agent',
        status: 'blocked',
        runtime_kind: 'claude',
        session_id: 'thread-1',
        tool_calls: [
          {
            tool_call_id: 'cmd-1',
            name: 'WebSearch',
            status: 'pending',
            arguments: { query: 'openai/symphony SPEC.md' },
            created_at: '2026-06-02T10:00:01Z'
          }
        ],
        created_at: '2026-06-02T10:00:01Z'
      }
    ],
    events: [
      {
        event_id: eventID,
        discussion_id: 'conv-direct',
        kind: 'discussion_runtime_approval_requested',
        status: 'pending',
        summary: 'claude requests permission to use WebSearch.',
        metadata: {
          approval_conversation_id: 'conv-direct',
          approval_request_id: 'cmd-1',
          approval_run_id: 'run-direct',
          approval_agent_id: 'staffing-agent',
          approval_session_id: 'thread-1',
          approval_type: 'tool_use',
          approval_tool_name: 'WebSearch',
          approval_source: 'claude',
          query: 'openai/symphony SPEC.md'
        },
        available_actions: runtimeApprovalActions(`/api/discussions/conv-direct/approvals/${eventID}/decision`),
        created_at: '2026-06-02T10:00:02Z'
      }
    ]
  }
}

function directDiscussionWithAttachment(): DiscussionDTO {
  return {
    ...directDiscussion(''),
    messages: [],
    runs: [],
    attachments: [{
      attachment_id: 'att-direct',
      discussion_id: 'conv-direct',
      file_name: 'brief.txt',
      content_type: 'text/plain',
      size_bytes: 12,
      uploaded_by: 'operator',
      uploaded_by_kind: 'user',
      uploaded_by_name: 'Operator',
      download_url: '/api/discussions/conv-direct/attachments/att-direct/download',
      created_at: '2026-06-02T10:00:00Z'
    }]
  }
}

function directHumanDiscussion(lastMessage: string): DiscussionDTO {
  return {
    discussion_id: 'conv-direct-human',
    conversation_kind: 'direct',
    direct_target_kind: 'user',
    direct_target_id: 'human-reviewer',
    title: 'Reviewer',
    status: 'completed',
    created_by: 'operator',
    created_at: '2026-06-02T10:00:00Z',
    updated_at: '2026-06-02T10:00:00Z',
    unread_completed: false,
    members: [
      { kind: 'user', id: 'human-reviewer', display_name: 'Reviewer', role: '私聊对象' },
      { kind: 'user', id: 'operator', display_name: 'Operator', role: '话题发起人' }
    ],
    messages: [
      {
        message_id: 'msg-direct-human-user',
        discussion_id: 'conv-direct-human',
        role: 'user',
        author_id: 'operator',
        author_name: 'Operator',
        content: lastMessage,
        created_at: '2026-06-02T10:00:00Z'
      }
    ],
    runs: [],
    linked_missions: []
  }
}

function convertedGroupDiscussion(): DiscussionDTO {
  return {
    ...directDiscussion('Earlier private note'),
    conversation_kind: 'group',
    direct_agent_id: '',
    direct_target_kind: '',
    direct_target_id: '',
    title: '秦若兰、顾明澈',
    members: [
      { kind: 'agent', id: 'staffing-agent', display_name: '秦若兰', role: '私聊对象' },
      { kind: 'user', id: 'operator', display_name: 'Operator', role: '话题发起人' },
      { kind: 'agent', id: 'lead-agent', display_name: '顾明澈', role: 'Lead' }
    ]
  }
}

function discussionPageWithDirect(): DiscussionPageDTO {
  return {
    page: { page: 1, page_size: 20, total: 2 },
    discussions: [
      {
        discussion_id: 'conv-existing',
        conversation_kind: 'group',
        title: 'Existing launch',
        status: 'completed',
        last_message: 'Keep going',
        linked_missions: 1,
        updated_at: '2026-06-02T09:00:00Z',
        unread_completed: false
      },
      {
        discussion_id: 'conv-direct',
        conversation_kind: 'direct',
        direct_agent_id: 'staffing-agent',
        direct_target_kind: 'agent',
        direct_target_id: 'staffing-agent',
        title: '秦若兰',
        status: 'completed',
        last_message: 'Earlier private note',
        linked_missions: 0,
        updated_at: '2026-06-02T08:00:00Z',
        unread_completed: false
      }
    ]
  }
}

function missionControl(): MissionControlViewDTO {
  return {
    mission: {
      mission_id: 'mission-existing',
      title: 'Existing mission',
      final_goal: 'Ship existing mission',
      status: 'active',
      phase: 'planning',
      risk_level: 'low',
      created_at: '2026-06-02T09:00:00Z'
    },
    summary: {
      active_task_count: 0,
      pending_approval_count: 0,
      risk_count: 0,
      blocker_count: 0,
      team_size: 0,
      delivery_rate: 0,
      updated_at: '2026-06-02T09:00:00Z'
    },
    team: { members: [] },
    task_board: { tasks: [] },
    evidence: { evidence_items: [] },
    qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
    discussion: { discussion_group_id: 'dg-1', messages: [] },
    inbox_summary: { open_count: 0, items: [] },
    progress_graph: { nodes: [], edges: [], current_focus: [] },
    latest_decisions: [],
    blockers: [],
    updated_at: '2026-06-02T09:00:00Z',
    version: 1
  }
}

function emptyApprovalPage() {
  return { items: [], page: { total: 0, returned: 0 } }
}

function workforcePage() {
  return {
    members: [
      {
        agent_id: 'staffing-agent-instance',
        agent_profile_id: 'staffing-agent',
        real_name: '秦若兰',
        display_name: '秦若兰',
        agent_kind: 'ai',
        employment_type: 'employee',
        role_ids: ['staffing'],
        role_name: 'Staffing',
        runtime_kind: 'codex',
        enabled: true
      },
      {
        agent_id: 'lead-agent-instance',
        agent_profile_id: 'lead-agent',
        real_name: '顾明澈',
        display_name: '顾明澈',
        agent_kind: 'ai',
        employment_type: 'employee',
        role_ids: ['lead'],
        role_name: 'Lead',
        runtime_kind: 'managed',
        enabled: true
      },
      {
        agent_id: 'human-operator',
        agent_profile_id: 'human-operator',
        real_name: 'Operator',
        display_name: 'Operator',
        agent_kind: 'human',
        employment_type: 'employee',
        role_name: 'Current user',
        enabled: true
      },
      {
        agent_id: 'human-reviewer',
        agent_profile_id: 'human-reviewer',
        real_name: 'Reviewer',
        display_name: 'Reviewer',
        agent_kind: 'human',
        employment_type: 'employee',
        role_name: 'Human reviewer',
        enabled: true
      }
    ],
    page: { total: 3, returned: 3 }
  }
}

function approvalPage() {
  return {
    items: [
      {
        approval_id: 'approval-1',
        approval_type: 'file_edit',
        status: 'pending',
        priority: 'high',
        mission_id: 'mission-existing',
        task_id: 'task-1',
        source_type: 'codex',
        source_id: 'agent-alpha',
        title: 'Allow Codex file edit',
        summary: 'Codex wants to modify files in the workspace.',
        requested_by: 'agent-alpha',
        assigned_to: 'operator',
        available_actions: [
          {
            action_id: 'approve',
            label: 'Approve',
            method: 'POST',
            path: '/api/discussions/conv-existing/approvals/cmd-1/decision',
            requires_body: true,
            confirmation_required: false,
            reason_required: false
          }
        ],
        evidence_refs: [],
        created_at: '2026-06-02T09:01:00Z'
      }
    ],
    page: { total: 1, returned: 1 }
  }
}

function runtimeApprovalActions(path: string) {
  return [
    { action_id: 'approve_once', label: 'Approve once', style: 'primary', decision: 'approved', effect: 'once', input_kind: 'message', method: 'POST', path, requires_body: true, confirmation_required: true, reason_required: false },
    { action_id: 'approve_session', label: 'Approve for session', style: 'primary', decision: 'approved', effect: 'session', input_kind: 'message', method: 'POST', path, requires_body: true, confirmation_required: true, reason_required: false, remember_allowed: true },
    { action_id: 'deny', label: 'Deny', style: 'danger', decision: 'denied', effect: 'deny', input_kind: 'message', method: 'POST', path, requires_body: true, confirmation_required: true, reason_required: false },
    { action_id: 'other', label: 'Other', style: 'secondary', decision: 'approved', effect: 'once', input_kind: 'message', method: 'POST', path, requires_body: true, confirmation_required: true, reason_required: false }
  ]
}
