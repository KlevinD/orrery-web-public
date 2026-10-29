import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DiscussionPanel from './DiscussionPanel.svelte'
import { setLocale } from '#lib/i18n'
import { clearSession } from '#lib/stores/session.svelte'
import { discussion } from './DiscussionPanel.fixtures'
import type { ApprovalItemViewModel } from '#lib/types/view-models'

describe('DiscussionPanel approvals', () => {
  beforeEach(() => {
    setLocale('en-US')
    clearSession()
  })

  it('renders pending approval requests as actionable timeline bubbles', async () => {
    const approval = pendingApproval()
    const onApprovalAction = vi.fn()
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [
            {
              id: 'msg-before',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: 'I need approval before editing files.',
              createdAt: 'Jun 02, 09:00'
            },
            {
              id: 'msg-after',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: 'I will continue after the approval.',
              createdAt: 'Jun 02, 09:02'
            }
          ],
          timeline: [
            {
              id: 'msg-before',
              kind: 'message',
              sortAt: '2026-06-02T09:00:00Z',
              message: {
                id: 'msg-before',
                role: 'assistant',
                authorId: 'agent-alpha',
                authorName: 'Lead Agent',
                content: 'I need approval before editing files.',
                createdAt: 'Jun 02, 09:00'
              }
            },
            {
              id: 'msg-after',
              kind: 'message',
              sortAt: '2026-06-02T09:02:00Z',
              message: {
                id: 'msg-after',
                role: 'assistant',
                authorId: 'agent-alpha',
                authorName: 'Lead Agent',
                content: 'I will continue after the approval.',
                createdAt: 'Jun 02, 09:02'
              }
            }
          ]
        }),
        pendingApprovals: [approval],
        onApprovalAction
      }
    })

    expect(screen.getByTestId('discussion-approval-card-approval-1')).toBeTruthy()
    expect(screen.getByText('Modify files')).toBeTruthy()
    expect(screen.getByTestId('discussion-approval-operation-type').textContent).toBe('File change')
    expect(screen.getByText('The agent will modify files in the workspace.')).toBeTruthy()
    expect(screen.queryByText('Codex wants to modify files in the workspace.')).toBeNull()
    expect(screen.queryByText('Approve file changes')).toBeNull()
    expect(screen.queryByText('codex:agent-alpha')).toBeNull()
    expect([...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual([
      'msg-before',
      'approval:approval-1',
      'msg-after'
    ])

    expect(screen.queryByRole('button', { name: 'Review approval' })).toBeNull()
    const inlinePanel = within(screen.getByTestId('discussion-approval-card-approval-1')).getByTestId('discussion-inline-approval-panel')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve once' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve for session' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Deny' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Other' })).toBeTruthy()
    expect(within(inlinePanel).queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'No' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'Approve' })).toBeNull()

    await fireEvent.click(within(inlinePanel).getByRole('button', { name: 'Approve once' }))

    expect(onApprovalAction).toHaveBeenCalledWith(approval, approval.actions[0], expect.objectContaining({ message: '' }))
  })

  it('does not send another message while the discussion is waiting for runtime approval', async () => {
    const approval = pendingApproval({
      discussionId: 'conv-1',
      runtimeRunId: 'run-approval',
      runtimeRequestId: 'approval-1'
    })
    const onSend = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: 'Try again',
        discussion: discussion({
          runtimeStatus: 'blocked',
          timeline: [{ id: `approval:${approval.id}`, kind: 'approval', sortAt: approval.createdAtRaw, approval }]
        }),
        onSend
      }
    })

    const composer = screen.getByRole('textbox', { name: 'Chat message' })
    const send = screen.getByRole('button', { name: 'Send message' })
    expect((send as HTMLButtonElement).disabled).toBe(true)
    await fireEvent.keyDown(composer, { key: 'Enter' })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('uses approval center actions for approvals without AI choices', async () => {
    const approval = pendingApproval({
      actions: [
        {
          ...pendingApproval().actions[0],
          id: 'approve_once',
          label: 'Approve once',
          decision: 'approved',
          effect: 'once'
        },
        {
          ...pendingApproval().actions[0],
          id: 'approve_session',
          label: 'Approve for session',
          style: 'primary',
          decision: 'approved',
          effect: 'session',
          rememberAllowed: true
        },
        {
          id: 'deny',
          label: 'Deny',
          style: 'danger',
          decision: 'denied',
          effect: 'deny',
          inputKind: 'message',
          defaultMessage: '',
          method: 'POST',
          path: '/api/approvals/approval-1/decision',
          reasonRequired: false,
          confirmationRequired: true,
          allowsModification: false,
          rememberAllowed: false
        },
        {
          id: 'other',
          label: 'Other',
          style: 'secondary',
          decision: 'approved',
          effect: 'once',
          inputKind: 'message',
          defaultMessage: '',
          method: 'POST',
          path: '/api/approvals/approval-1/decision',
          reasonRequired: false,
          confirmationRequired: true,
          allowsModification: false,
          rememberAllowed: false
        }
      ],
      payloadPreview: { kind: 'command_execution', language: 'shell', title: '', content: 'npm test', fields: [] }
    })
    const onApprovalAction = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval],
        onApprovalAction
      }
    })

    expect(screen.queryByRole('button', { name: 'Review approval' })).toBeNull()
    const inlinePanel = within(screen.getByTestId('discussion-approval-card-approval-1')).getByTestId('discussion-inline-approval-panel')

    expect(screen.queryByRole('dialog')).toBeNull()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve once' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve for session' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Deny' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Other' })).toBeTruthy()
    expect(within(inlinePanel).queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'No' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'Approve' })).toBeNull()

    await fireEvent.click(within(inlinePanel).getByRole('button', { name: 'Other' }))
    const otherInput = within(inlinePanel).getByLabelText('Approval message') as HTMLTextAreaElement
    expect(otherInput.value).toBe('')
    await fireEvent.input(otherInput, { target: { value: 'Run only unit tests' } })
    await fireEvent.click(within(inlinePanel).getByRole('button', { name: 'Other' }))

    expect(onApprovalAction).toHaveBeenCalledWith(
      approval,
      expect.objectContaining({ id: 'other' }),
      expect.objectContaining({ message: 'Run only unit tests' })
    )
  })

  it('uses an AI choice prompt with options and other without yes or no', async () => {
    const approval = pendingApproval({
      displayKind: 'user_input',
      choices: [
        { id: 'fast', label: 'Fast rollout', description: 'Ship to beta first' },
        { id: 'safe', label: 'Safe rollout', description: 'Use staged rollout' }
      ],
      actions: [
        {
          id: 'answer',
          label: 'Answer',
          style: 'primary',
          decision: 'answered',
          effect: 'answer',
          inputKind: 'choice_or_text',
          defaultMessage: '',
          method: 'POST',
          path: '/api/approvals/approval-1/decision',
          reasonRequired: false,
          confirmationRequired: true,
          allowsModification: false,
          rememberAllowed: false
        }
      ],
      payloadPreview: { kind: 'user_input', language: '', title: '', content: 'Which rollout should I use?', fields: [] }
    })
    const onApprovalAction = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval],
        onApprovalAction
      }
    })

    const inlinePanel = within(screen.getByTestId('discussion-approval-card-approval-1')).getByTestId('discussion-inline-approval-panel')

    expect(screen.queryByRole('dialog')).toBeNull()
    expect(within(inlinePanel).getByRole('button', { name: 'Fast rollout' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Safe rollout' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Other' })).toBeTruthy()
    expect(within(inlinePanel).queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'No' })).toBeNull()

    await fireEvent.click(within(inlinePanel).getByRole('button', { name: 'Fast rollout' }))

    expect(onApprovalAction).toHaveBeenCalledWith(
      approval,
      expect.objectContaining({ id: 'answer' }),
      expect.objectContaining({ choiceId: 'fast', message: '' })
    )
  })

  it('shows provider explanations for command approvals before the decision controls', () => {
    const approval = pendingApproval({
      typeLabel: 'Command',
      displayKind: 'command_execution',
      provider: 'codex',
      toolName: 'exec_command',
      summary: 'I need to run the test suite to verify the approval center changes.',
      details: 'This command reads project files and runs unit tests; it does not modify source files.',
      riskSummary: 'Runs a shell command in the workspace.',
      payloadPreview: {
        kind: 'command_execution',
        language: 'shell',
        title: '',
        content: 'npm test -- DiscussionPanel.test.ts',
        fields: [
          { key: 'sandbox_permissions', value: 'workspace-write' },
          { key: 'approval_session_id', value: 'thread-1' }
        ]
      }
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval]
      }
    })

    const card = screen.getByTestId('discussion-approval-card-approval-1')

    expect(within(card).getByText('Run command')).toBeTruthy()
    expect(within(card).getByTestId('discussion-approval-operation-type').textContent).toBe('Command')
    expect(within(card).getByText('The agent will run the command shown below in the workspace.')).toBeTruthy()
    expect(within(card).queryByText('Approve command execution')).toBeNull()
    expect(within(card).queryByText('I need to run the test suite to verify the approval center changes.')).toBeNull()
    expect(within(card).getByText('This command reads project files and runs unit tests; it does not modify source files.')).toBeTruthy()
    expect(within(card).getByText('Runs a shell command in the workspace.')).toBeTruthy()
    expect(within(card).getByText('npm test -- DiscussionPanel.test.ts')).toBeTruthy()
    expect(within(card).queryByText(/approval_session_id/i)).toBeNull()
  })

  it('explains runtime web approvals directly and hides internal identifiers', () => {
    const approval = pendingApproval({
      id: 'approval-web',
      type: 'tool_use',
      displayKind: 'tool_use',
      displayTone: 'warning',
      typeLabel: 'Tool',
      priority: 'normal',
      priorityTone: 'warning',
      sourceLabel: 'discussion:orrery-123',
      provider: 'claude',
      toolName: 'WebFetch',
      title: 'Runtime approval: WebFetch',
      summary: 'Runtime approval required for WebFetch',
      requestedBy: 'profile-108b661386c13b9b0d55cb117efff2f0',
      payloadPreview: {
        kind: 'tool_use',
        language: '',
        title: '',
        content: 'https://github.com/openai/symphony/blob/main/SPEC.md',
        fields: [
          { key: 'url', value: 'https://github.com/openai/symphony/blob/main/SPEC.md' },
          { key: 'approval_session_id', value: 'dbef6f8d-24ac-4036-84a3-964fc0bddc14' }
        ]
      }
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval]
      }
    })

    const card = screen.getByTestId('discussion-approval-card-approval-web')
    const inlinePanel = within(card).getByTestId('discussion-inline-approval-panel')

    expect(within(card).getByText('Read webpage')).toBeTruthy()
    expect(within(card).getByTestId('discussion-approval-operation-type').textContent).toBe('Network access')
    expect(within(card).getByText('The agent will read content from the specified webpage.')).toBeTruthy()
    expect(within(card).queryByText('Approve web access')).toBeNull()
    expect(within(card).queryByText('claude wants to access this URL with WebFetch.')).toBeNull()
    expect(within(card).queryByText('Approving lets it continue. Denying blocks this action. Use Other to send your own instruction.')).toBeNull()
    expect(within(card).getByText('URL')).toBeTruthy()
    expect(within(card).getAllByText('https://github.com/openai/symphony/blob/main/SPEC.md').length).toBeGreaterThan(0)
    expect(within(card).queryByText(/approval_session_id/i)).toBeNull()
    expect(within(card).queryByText('dbef6f8d-24ac-4036-84a3-964fc0bddc14')).toBeNull()
    expect(within(card).queryByText('discussion:orrery-123')).toBeNull()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve once' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Approve for session' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Deny' })).toBeTruthy()
    expect(within(inlinePanel).getByRole('button', { name: 'Other' })).toBeTruthy()
    expect(within(inlinePanel).queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'No' })).toBeNull()
    expect(within(inlinePanel).queryByRole('button', { name: 'Approve' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Review approval' })).toBeNull()
  })

  it('shows a localized mission action and its actual goal without opaque request ids', () => {
    setLocale('zh-CN')
    const approval = pendingApproval({
      id: 'approval-mission-prepare',
      type: 'tool_use',
      displayKind: 'tool_use',
      priority: 'normal',
      toolName: 'mission_prepare',
      title: 'Runtime approval: mission_prepare',
      summary: 'Runtime approval required for mission_prepare',
      payloadPreview: {
        kind: 'tool_use',
        language: '',
        title: '',
        content: 'call_vgVsQnXJV5XcuTA9PnltJJR9',
        fields: [
          { key: 'title', value: '企业级数字员工平台' },
          { key: 'final_goal', value: '完成可部署、可验收的产品版本' },
          { key: 'idempotency_key', value: 'mission_prepare:call_vgVsQnXJV5XcuTA9PnltJJR9' },
          { key: 'approval_request_id', value: 'call_vgVsQnXJV5XcuTA9PnltJJR9' }
        ]
      }
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval]
      }
    })

    const card = screen.getByTestId('discussion-approval-card-approval-mission-prepare')
    expect(within(card).getByText('准备任务')).toBeTruthy()
    expect(within(card).getByTestId('discussion-approval-operation-type').textContent).toBe('工具调用')
    expect(within(card).getByText('Agent 将创建或更新任务简报，并同步建立团队根任务。')).toBeTruthy()
    expect(within(card).getByText('任务名称')).toBeTruthy()
    expect(within(card).getByText('企业级数字员工平台')).toBeTruthy()
    expect(within(card).getByText('最终目标')).toBeTruthy()
    expect(within(card).getByText('完成可部署、可验收的产品版本')).toBeTruthy()
    expect(within(card).queryByText('mission_prepare')).toBeNull()
    expect(within(card).queryByText(/call_vgVs/)).toBeNull()
    expect(within(card).getByRole('button', { name: '仅本次批准' })).toBeTruthy()
    expect(within(card).getByRole('button', { name: '本会话批准' })).toBeTruthy()
    expect(within(card).getByRole('button', { name: '拒绝' })).toBeTruthy()
    expect(within(card).getByRole('button', { name: '其他' })).toBeTruthy()
  })

  it('renders decided approvals as compact one-line tool records', () => {
    const approval = pendingApproval({
      status: 'approved',
      actions: [],
      decisionEffect: 'session',
      details: 'Operator approved this for the current session.',
      payloadPreview: { kind: 'command_execution', language: 'shell', title: '', content: 'npm test', fields: [] }
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval]
      }
    })

    const card = screen.getByTestId('discussion-approval-card-approval-1')

    expect(card.hasAttribute('data-approval-compact')).toBe(true)
    expect(within(card).getByTestId('discussion-approval-compact-action').textContent).toBe('Will modify files in the workspace.')
    expect(within(card).getByTestId('discussion-approval-compact-status').textContent).toBe('Approved')
    expect(within(card).queryByText('Operator approved this for the current session.')).toBeNull()
    expect(within(card).queryByText('npm test')).toBeNull()
    expect(within(card).queryByTestId('discussion-inline-approval-panel')).toBeNull()
    expect(within(card).queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(within(card).queryByRole('button', { name: 'No' })).toBeNull()
    expect(within(card).queryByRole('button', { name: 'Other' })).toBeNull()
  })

  it('groups more than two consecutive decided approvals into the same collapsible tool drawer', async () => {
    setLocale('zh-CN')
    const approvals = [
      pendingApproval({
        id: 'approval-prepare',
        status: 'approved',
        actions: [],
        toolName: 'mission_prepare',
        createdAtRaw: '2026-06-02T09:01:00Z'
      }),
      pendingApproval({
        id: 'approval-custom-a',
        status: 'approved',
        actions: [],
        toolName: 'custom_tool_a',
        createdAtRaw: '2026-06-02T09:02:00Z'
      }),
      pendingApproval({
        id: 'approval-custom-b',
        status: 'approved',
        actions: [],
        toolName: 'custom_tool_b',
        createdAtRaw: '2026-06-02T09:03:00Z'
      })
    ]

    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: approvals
      }
    })

    const drawer = screen.getByTestId('discussion-tool-call-drawer')
    const toggle = within(drawer).getByRole('button', { name: 'Tool calls' })
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(within(drawer).getByText('将创建或更新任务简报，并同步建立团队根任务。')).toBeTruthy()
    expect(within(drawer).getByText('custom_tool_a')).toBeTruthy()
    expect(within(drawer).getAllByText('已批准')).toHaveLength(3)

    await fireEvent.click(toggle)

    expect(drawer.querySelector('[data-tool-call-drawer-content]')).toBeNull()
    expect(screen.queryByText('custom_tool_a')).toBeNull()
    expect(container.querySelectorAll('[data-testid^="discussion-approval-card-"]')).toHaveLength(0)
  })

  it('uses the tool-call scan effect while an approved action is executing', () => {
    setLocale('zh-CN')
    const approval = pendingApproval({
      id: 'approval-member-add',
      status: 'approved',
      actions: [],
      priority: 'normal',
      taskId: 'run-1',
      toolName: 'discussion_member_add',
      displayKind: 'tool_use',
      payloadPreview: {
        kind: 'tool_use',
        language: '',
        title: '',
        content: '',
        fields: [{ key: 'approval_request_id', value: 'call-member-1' }]
      }
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'running',
          runs: [{
            id: 'run-1',
            agentId: 'agent-alpha',
            agentName: 'Lead Agent',
            status: 'running',
            runtimeKind: 'codex',
            toolCalls: [{ id: 'call-member-1', name: 'discussion_member_add', status: 'running', createdAt: 'Jul 22, 09:00' }],
            createdAt: 'Jul 22, 09:00'
          }]
        }),
        pendingApprovals: [approval]
      }
    })

    const action = screen.getByText('将把指定成员加入当前讨论。')
    expect(action.textContent).toBe('将把指定成员加入当前讨论。')
    expect(action.classList.contains('discussion-tool-text-running')).toBe(true)
    expect(screen.getByText('已批准')).toBeTruthy()
  })

  it('falls back to the raw tool name when no localized action is available', () => {
    setLocale('zh-CN')
    const approval = pendingApproval({
      id: 'approval-custom-tool',
      status: 'approved',
      actions: [],
      priority: 'normal',
      toolName: 'custom_native_tool',
      displayKind: 'tool_use'
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval]
      }
    })

    const card = screen.getByTestId('discussion-approval-card-approval-custom-tool')
    expect(within(card).getByTestId('discussion-approval-compact-action').textContent).toBe('custom_native_tool')
    expect(within(card).getByTestId('discussion-approval-compact-status').textContent).toBe('已批准')
  })

  it('labels unavailable approvals as expired rather than completed decisions', () => {
    const approval = pendingApproval({
      status: 'unavailable',
      actions: [],
      details: 'The original runtime request is no longer active.',
      payloadPreview: { kind: 'tool_use', language: '', title: '', content: 'WebSearch', fields: [] }
    })

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingApprovals: [approval]
      }
    })

    const card = screen.getByTestId('discussion-approval-card-approval-1')
    const resultText = within(card).getByTestId('discussion-approval-compact-status').textContent ?? ''

    expect(resultText).toContain('Unavailable')
    expect(within(card).queryByTestId('discussion-inline-approval-panel')).toBeNull()
  })

  it('replaces stale pending approval cards with the decided discussion approval for the same runtime request', () => {
    const fields = [
      { key: 'approval_conversation_id', value: 'conv-1' },
      { key: 'approval_request_id', value: 'cmd-1' },
      { key: 'approval_run_id', value: 'run-1' },
      { key: 'url', value: 'https://github.com/openai/symphony/blob/main/SPEC.md' }
    ]
    const decidedApproval = pendingApproval({
      id: 'discussion-runtime-approval-conv-1-run-1-cmd-1',
      type: 'tool_use',
      displayKind: 'tool_use',
      typeLabel: 'Tool',
      status: 'approved',
      missionId: 'conv-1',
      taskId: 'run-1',
      sourceLabel: 'discussion:conv-1',
      provider: 'claude',
      toolName: 'WebFetch',
      summary: 'Claude wants to fetch the Symphony SPEC.',
      details: 'Operator approved this request.',
      payloadPreview: {
        kind: 'tool_use',
        language: '',
        title: '',
        content: 'https://github.com/openai/symphony/blob/main/SPEC.md',
        fields
      },
      actions: []
    })
    const stalePendingApproval = pendingApproval({
      id: 'command_execution:mission-1:cmd-1',
      type: 'tool_use',
      displayKind: 'tool_use',
      typeLabel: 'Tool',
      status: 'pending',
      missionId: 'mission-1',
      taskId: 'run-1',
      sourceLabel: 'runtime:mission-1',
      provider: 'claude',
      toolName: 'WebFetch',
      payloadPreview: {
        kind: 'tool_use',
        language: '',
        title: '',
        content: 'https://github.com/openai/symphony/blob/main/SPEC.md',
        fields
      },
      actions: [
        {
          ...pendingApproval().actions[0],
          path: '/api/discussions/conv-1/approvals/cmd-1/decision'
        },
        {
          ...pendingApproval().actions[1],
          path: '/api/discussions/conv-1/approvals/cmd-1/decision'
        }
      ]
    })
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [],
          runs: [],
          timeline: [{
            id: `approval:${decidedApproval.id}`,
            kind: 'approval',
            sortAt: decidedApproval.createdAtRaw,
            approval: decidedApproval
          }]
        }),
        pendingApprovals: [stalePendingApproval]
      }
    })

    expect(container.querySelectorAll('[data-testid^="discussion-approval-card-"]')).toHaveLength(1)
    expect(screen.getByTestId('discussion-approval-card-discussion-runtime-approval-conv-1-run-1-cmd-1')).toBeTruthy()
    expect(screen.queryByTestId('discussion-approval-card-command_execution:mission-1:cmd-1')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Yes' })).toBeNull()
    expect(screen.getByTestId('discussion-approval-compact-status').textContent).toContain('Approved')
  })
})

function pendingApproval(overrides: Partial<ApprovalItemViewModel> = {}): ApprovalItemViewModel {
  const base: ApprovalItemViewModel = {
    id: 'approval-1',
    type: 'file_change',
    displayKind: 'file_change',
    displayTone: 'danger',
    typeLabel: 'file edit',
    status: 'pending',
    statusTone: 'warning',
    priority: 'high',
    priorityTone: 'danger',
    missionId: 'mission-1',
    taskId: 'task-1',
    sourceLabel: 'codex:agent-alpha',
    provider: 'codex',
    toolName: 'patch_apply',
    title: 'Allow Codex file edit',
    summary: 'Codex wants to modify files in the workspace.',
    details: '',
    riskSummary: '',
    requestedBy: 'agent-alpha',
    assignedTo: 'operator',
    organizationCode: 'Not provided',
    organizationName: 'Not provided',
    requestedByActorLabel: 'Not provided',
    targetActorLabel: 'Not provided',
    policyId: 'Not provided',
    riskLevel: 'Not provided',
    decisionEffect: 'Not provided',
    payloadPreview: { kind: 'file_change', language: 'diff', title: '', content: '', fields: [] },
    choices: [],
    evidenceRefs: [],
    createdAt: 'Jun 02, 2026',
    createdAtRaw: '2026-06-02T09:01:00Z',
    expiresAt: 'Not provided',
    expiresAtRaw: '',
    actions: [
      {
        id: 'approve_once',
        label: 'Approve once',
        style: 'primary',
        decision: 'approved',
        effect: 'once',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: false,
        allowsModification: false,
        rememberAllowed: false
      },
      {
        id: 'approve_session',
        label: 'Approve for session',
        style: 'primary',
        decision: 'approved',
        effect: 'session',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: true,
        allowsModification: false,
        rememberAllowed: true
      },
      {
        id: 'deny',
        label: 'Deny',
        style: 'danger',
        decision: 'denied',
        effect: 'deny',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: true,
        allowsModification: false,
        rememberAllowed: false
      },
      {
        id: 'other',
        label: 'Other',
        style: 'secondary',
        decision: 'approved',
        effect: 'once',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: true,
        allowsModification: false,
        rememberAllowed: false
      }
    ]
  }
  return { ...base, ...overrides } as ApprovalItemViewModel
}
