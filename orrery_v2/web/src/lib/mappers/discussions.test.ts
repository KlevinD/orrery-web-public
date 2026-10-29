import { describe, expect, it } from 'vitest'
import { appendPendingDiscussionMessage, applyDiscussionStreamEvent, mapDiscussionListView, mapDiscussionMessagePageView, mapDiscussionMessageRequest, mapDiscussionRunDebugView, mapDiscussionView, prependDiscussionMessagePage } from './discussions'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import type { DiscussionDTO } from '#lib/types/discussions'

describe('discussion mapper', () => {
  it('renders human todo cards only from system handoff notices', () => {
    const view = mapDiscussionView({
      discussion_id: 'source', title: 'Source', status: 'completed', created_by: 'human', created_at: '', updated_at: '', unread_completed: false,
      messages: ['system', 'user'].map((role, index) => ({ message_id: String(index), discussion_id: 'source', role, author_id: 'orrery', content: 'Human action', created_at: '', metadata: { kind: 'human_todo', todo_id: 'todo-1' } }))
    } as DiscussionDTO)
    expect(view.messages[0].humanTodoId).toBe('todo-1')
    expect(view.messages[1].humanTodoId).toBeUndefined()
  })
  it('preserves current-user actions when persisted messages use the People profile ID', () => {
    applySession({
      expiresAt: '',
      permissionScope: [],
      user: {
        userId: 'user-ding',
        username: 'ding',
        displayName: 'Ding',
        nickname: 'Ding',
        email: '',
        agentProfileId: 'human-ding',
        role: 'member'
      }
    })

    try {
      const view = mapDiscussionView({
        discussion_id: 'conv-profile-author',
        title: 'Profile author',
        status: 'completed',
        created_by: 'human-ding',
        created_at: '2026-08-12T00:00:00Z',
        updated_at: '2026-08-12T00:01:00Z',
        unread_completed: false,
        messages: [{
          message_id: 'msg-profile-author',
          discussion_id: 'conv-profile-author',
          role: 'user',
          author_id: 'human-ding',
          is_current_viewer: true,
          content: 'Persisted message',
          created_at: '2026-08-12T00:00:00Z'
        }]
      })

      expect(view.messages[0].canRequestRetry).toBe(true)
      expect(view.messages[0].isCurrentViewer).toBe(true)
    } finally {
      clearSession()
    }
  })

  it('maps HIL mode and defaults older discussions to manual', () => {
    const automatic = mapDiscussionView({
      discussion_id: 'conv-auto',
      hil_mode: 'auto',
      title: 'Auto',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false
    })
    const legacy = mapDiscussionView({
      discussion_id: 'conv-manual',
      title: 'Manual',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false
    })
    const none = mapDiscussionView({
      discussion_id: 'conv-none',
      hil_mode: 'none',
      title: 'None',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false
    })

    expect(automatic.hilMode).toBe('auto')
    expect(legacy.hilMode).toBe('manual')
    expect(none.hilMode).toBe('none')
  })

  it('maps backend unread completed flags for discussion badges', () => {
    const view = mapDiscussionListView({
      discussions: [
        {
          discussion_id: 'conv-1',
          title: 'Launch',
          status: 'completed',
          linked_missions: 1,
          updated_at: '2026-06-02T00:01:00Z',
          unread_completed: true
        },
        {
          discussion_id: 'conv-2',
          title: 'Viewed',
          status: 'completed',
          linked_missions: 0,
          updated_at: '2026-06-02T00:02:00Z',
          unread_completed: false
        }
      ]
    })

    expect(view.discussions.map((discussion) => discussion.unreadCompleted)).toEqual([true, false])
  })

  it('renders mention links in discussion list previews as readable names', () => {
    const view = mapDiscussionListView({
      discussions: [
        {
          discussion_id: 'conv-1',
          title: 'Launch',
          status: 'completed',
          linked_missions: 0,
          last_message: '[@秦若兰](mention://agent/agent-beta) 请确认一下。',
          updated_at: '2026-06-02T00:01:00Z',
          unread_completed: false
        }
      ]
    })

    expect(view.discussions[0].lastMessage).toBe('@秦若兰 请确认一下。')
  })

  it('keeps real mention links in message bodies for rich rendering', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-1',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: '[@本地用户](mention://user/user-1) [@秦若兰](mention://agent/agent-beta) 请看下。',
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      runs: [],
      linked_missions: []
    })

    expect(view.messages[0].content).toBe('[@本地用户](mention://user/user-1) [@秦若兰](mention://agent/agent-beta) 请看下。')
    expect(view.messages[0].content).toContain('mention://agent/agent-beta')
  })

  it('maps discussion DTOs into chat-first view models with linked mission routes', () => {
    const dto: DiscussionDTO = {
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_by_name: '本地用户',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-1',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          author_name: '顾明澈',
          content: 'Which deadline?',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-alpha',
          display_name: '顾明澈',
          role: 'Lead'
        },
        {
          kind: 'agent',
          id: 'agent-beta',
          display_name: '秦若兰',
          role: 'Staffing'
        },
        {
          kind: 'user',
          id: 'user-1',
          display_name: '本地用户',
          role: '话题发起人'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'openclaw',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      linked_missions: [
        {
          mission_id: 'mission-1',
          title: 'Launch plan',
          status: 'active',
          created_at: '2026-06-02T00:01:00Z'
        }
      ]
    }

    const view = mapDiscussionView(dto)

    expect(view.id).toBe('conv-1')
    expect(view.createdBy).toBe('本地用户')
    expect(view.messages[0].role).toBe('assistant')
    expect(view.runtimeStatus).toBe('running')
    expect(view.activeResponders).toEqual(['顾明澈'])
    expect(view.linkedMissions[0].href).toBe('/missions/mission-1')
  })

  it('does not show an orphaned running run as active after the discussion failed', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-failed-with-stale-run',
      title: 'Interrupted run',
      status: 'failed',
      created_by: 'user-1',
      created_at: '2026-09-01T04:08:00Z',
      updated_at: '2026-09-01T04:13:00Z',
      unread_completed: false,
      runs: [{
        run_id: 'run-orphaned',
        discussion_id: 'conv-failed-with-stale-run',
        agent_id: 'personal-assistant',
        agent_name: 'Assistant',
        status: 'running',
        runtime_kind: 'pi',
        created_at: '2026-09-01T04:08:30Z'
      }]
    })

    expect(view.runtimeStatus).toBe('failed')
    expect(view.activeResponders).toEqual([])
  })

  it('maps runtime approval events into discussion approval timeline items', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      direct_agent_id: 'staffing-agent',
      title: 'Staffing',
      status: 'blocked',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-direct',
          discussion_id: 'conv-1',
          agent_id: 'staffing-agent',
          status: 'blocked',
          runtime_kind: 'codex',
          session_id: 'thread-1',
          created_at: '2026-06-02T00:00:30Z',
          completed_at: '2026-06-02T00:01:00Z'
        }
      ],
      events: [
        {
          event_id: 'discussion-runtime-approval-conv-1-cmd-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_approval_requested',
          status: 'pending',
          summary: 'Runtime approval required for exec_command',
          metadata: {
            approval_request_id: 'cmd-1',
            approval_type: 'command_execution',
            approval_tool_name: 'exec_command',
            approval_source: 'codex',
            approval_run_id: 'run-direct',
            approval_summary: 'Codex needs to run this command to inspect the repository.',
            approval_details: 'The command only prints a diagnostic string.',
            approval_decision_source: 'allowlist',
            approval_rule_id: 'pa-git-read',
            command: 'python -c "print(\\"needs approval\\")"'
          },
          available_actions: runtimeApprovalActions('/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-cmd-1/decision'),
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    const approvalItem = view.timeline.find((item) => item.kind === 'approval')

    expect(approvalItem?.approval?.id).toBe('discussion-runtime-approval-conv-1-cmd-1')
    expect(approvalItem?.approval?.displayKind).toBe('command_execution')
    expect(approvalItem?.approval?.toolName).toBe('exec_command')
    expect(approvalItem?.approval?.summary).toBe('Codex needs to run this command to inspect the repository.')
    expect(approvalItem?.approval?.details).toBe('The command only prints a diagnostic string.')
    expect(approvalItem?.approval?.riskSummary).toBe('Allowlist | rule pa-git-read')
    expect(approvalItem?.approval?.payloadPreview.content).toContain('needs approval')
    expect(approvalItem?.approval?.actions.map((action) => `${action.id}:${action.label}:${action.inputKind}`)).toEqual(['approve_once:Approve once:message', 'approve_session:Approve for session:message', 'deny:Deny:message', 'other:Other:message'])
    expect(approvalItem?.approval?.actions[0].path).toContain('/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-cmd-1/decision')
  })

  it('routes HIL-backed runtime approvals without exposing the HIL decision id', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-hil',
      title: 'HIL discussion',
      status: 'blocked',
      created_by: 'user-1',
      created_at: '2026-08-28T02:43:00Z',
      updated_at: '2026-08-28T02:43:09Z',
      unread_completed: false,
      messages: [],
      runs: [],
      events: [{
        event_id: 'approval-hil-1',
        discussion_id: 'conv-hil',
        kind: 'discussion_runtime_approval_requested',
        status: 'pending',
        summary: 'Runtime approval required for knowledge_search',
        metadata: {
          approval_request_id: 'call-1',
          approval_type: 'tool_use',
          approval_tool_name: 'knowledge_search',
          approval_summary: 'Search the approved knowledge base.',
          hil_decision_id: 'hil-decision-1'
        },
        created_at: '2026-08-28T02:43:09Z'
      }],
      linked_missions: []
    })

    const approval = view.timeline.find((item) => item.kind === 'approval')?.approval
    expect(approval?.hilDecisionId).toBe('hil-decision-1')
    expect(approval?.actions.map((action) => action.id)).toEqual(['approve', 'reject'])
    expect(approval?.actions.every((action) => action.path === '/api/hil/decisions/hil-decision-1/decision')).toBe(true)
    expect(JSON.stringify(approval?.payloadPreview)).not.toContain('hil-decision-1')
  })

  it('uses matching tool arguments to explain approvals without exposing call ids or secrets', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-mission',
      conversation_kind: 'direct',
      direct_agent_id: 'lead-agent',
      title: 'Mission planning',
      status: 'blocked',
      created_by: 'user-1',
      created_at: '2026-07-21T09:00:00Z',
      updated_at: '2026-07-21T09:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [{
        run_id: 'run-mission',
        discussion_id: 'conv-mission',
        agent_id: 'lead-agent',
        status: 'blocked',
        runtime_kind: 'codex',
        created_at: '2026-07-21T09:00:00Z',
        tool_calls: [{
          tool_call_id: 'call_vgVsQnXJV5XcuTA9PnltJJR9',
          name: 'mission_prepare',
          status: 'blocked',
          arguments: {
            title: '企业级数字员工平台',
            final_goal: '完成可部署、可验收的产品版本',
            root_goal: '协调产品、设计和工程团队完成交付',
            idempotency_key: 'mission_prepare:call_vgVsQnXJV5XcuTA9PnltJJR9',
            api_token: 'must-not-be-visible'
          }
        }]
      }],
      events: [{
        event_id: 'approval-mission-prepare',
        discussion_id: 'conv-mission',
        kind: 'discussion_runtime_approval_requested',
        status: 'pending',
        summary: 'Runtime approval required for mission_prepare',
        metadata: {
          approval_request_id: 'call_vgVsQnXJV5XcuTA9PnltJJR9',
          approval_run_id: 'run-mission',
          approval_type: 'tool_use',
          approval_tool_name: 'mission_prepare'
        },
        created_at: '2026-07-21T09:01:00Z'
      }],
      linked_missions: []
    })

    const approval = view.timeline.find((item) => item.kind === 'approval')?.approval
    expect(approval?.toolName).toBe('mission_prepare')
    expect(approval?.payloadPreview.fields).toEqual(expect.arrayContaining([
      { key: 'title', value: '企业级数字员工平台' },
      { key: 'final_goal', value: '完成可部署、可验收的产品版本' },
      { key: 'root_goal', value: '协调产品、设计和工程团队完成交付' }
    ]))
    const visiblePayload = JSON.stringify(approval?.payloadPreview)
    expect(visiblePayload).not.toContain('call_vgVsQnXJV5XcuTA9PnltJJR9')
    expect(visiblePayload).not.toContain('idempotency_key')
    expect(visiblePayload).not.toContain('must-not-be-visible')
  })

  it('falls back to runtime approval actions when older discussion events omit backend actions', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Staffing',
      status: 'blocked',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [],
      events: [
        {
          event_id: 'discussion-runtime-approval-conv-1-cmd-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_approval_requested',
          status: 'pending',
          summary: 'Runtime approval required for exec_command',
          metadata: {
            approval_request_id: 'cmd-1',
            approval_type: 'command_execution',
            approval_tool_name: 'exec_command',
            command: 'npm test'
          },
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    const approvalItem = view.timeline.find((item) => item.kind === 'approval')

    expect(approvalItem?.approval?.actions.map((action) => `${action.id}:${action.label}:${action.inputKind}`)).toEqual(['approve_once:Approve once:message', 'approve_session:Approve for session:message', 'deny:Deny:message', 'other:Other:message'])
    expect(approvalItem?.approval?.actions[0].path).toBe('/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-cmd-1/decision')
  })

  it('uses provider-native runtime approval copy when available', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-claude',
      title: 'Claude',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-web',
          discussion_id: 'conv-claude',
          agent_id: 'claude-agent',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:30Z'
        }
      ],
      events: [
        {
          event_id: 'discussion-runtime-approval-conv-claude-run-web-approval-1',
          discussion_id: 'conv-claude',
          kind: 'discussion_runtime_approval_requested',
          status: 'pending',
          summary: 'Runtime approval required for WebFetch',
          metadata: {
            approval_request_id: 'approval-1',
            approval_run_id: 'run-web',
            approval_agent_id: 'claude-agent',
            approval_type: 'tool_use',
            approval_tool_name: 'WebFetch',
            approval_native_tool_name: 'WebFetch',
            approval_title: '确认是否访问网页',
            approval_display_name: '访问网页',
            approval_description: 'claude 请求用 WebFetch 访问这个 URL。',
            approval_reason: 'Runtime approval required for WebFetch',
            payload_preview: '请完整提取这份 SPEC 文档的全部内容。',
            url: 'https://github.com/openai/symphony/blob/main/SPEC.md'
          },
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    const approvalItem = view.timeline.find((item) => item.kind === 'approval')

    expect(approvalItem?.approval?.title).toBe('确认是否访问网页')
    expect(approvalItem?.approval?.summary).toBe('claude 请求用 WebFetch 访问这个 URL。')
    expect(approvalItem?.approval?.details).toBe('Runtime approval required for WebFetch')
    expect(approvalItem?.approval?.payloadPreview.title).toBe('访问网页')
    expect(approvalItem?.approval?.payloadPreview.content).toBe('请完整提取这份 SPEC 文档的全部内容。')
    expect(approvalItem?.approval?.payloadPreview.fields).toEqual(
      expect.arrayContaining([
        {
          key: 'url',
          value: 'https://github.com/openai/symphony/blob/main/SPEC.md'
        }
      ])
    )
  })

  it('keeps decided runtime approvals in the timeline as read-only approval results', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Direct',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:02:00Z',
      unread_completed: false,
      messages: [],
      runs: [],
      events: [
        {
          event_id: 'discussion-runtime-approval-conv-1-cmd-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_approval_requested',
          status: 'approved',
          summary: 'Runtime approval required for exec_command',
          metadata: {
            approval_request_id: 'cmd-1',
            approval_type: 'command_execution',
            approval_tool_name: 'exec_command',
            approval_source: 'codex',
            approval_decision: 'approved',
            approval_action_id: 'approve_session',
            decision_message: 'Approved for this session',
            command: 'npm test'
          },
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    const approvalItem = view.timeline.find((item) => item.kind === 'approval')

    expect(approvalItem?.approval?.status).toBe('approved')
    expect(approvalItem?.approval?.actions).toEqual([])
    expect(approvalItem?.approval?.details).toBe('Approved for this session')
  })

  it('keeps concurrent runtime approvals distinct when native request ids repeat', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Group',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-a',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:10Z'
        },
        {
          run_id: 'run-b',
          discussion_id: 'conv-1',
          agent_id: 'agent-b',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:11Z'
        }
      ],
      events: [
        {
          event_id: 'discussion-runtime-approval-conv-1-run-a-approval-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_approval_requested',
          status: 'pending',
          metadata: {
            approval_request_id: 'approval-1',
            approval_run_id: 'run-a',
            approval_agent_id: 'agent-a',
            approval_type: 'command_execution',
            approval_tool_name: 'exec_command',
            command: 'npm test'
          },
          available_actions: runtimeApprovalActions('/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-run-a-approval-1/decision'),
          created_at: '2026-06-02T00:00:12Z'
        },
        {
          event_id: 'discussion-runtime-approval-conv-1-run-b-approval-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_approval_requested',
          status: 'pending',
          metadata: {
            approval_request_id: 'approval-1',
            approval_run_id: 'run-b',
            approval_agent_id: 'agent-b',
            approval_type: 'command_execution',
            approval_tool_name: 'exec_command',
            command: 'go test'
          },
          available_actions: runtimeApprovalActions('/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-run-b-approval-1/decision'),
          created_at: '2026-06-02T00:00:13Z'
        }
      ],
      linked_missions: []
    })

    const approvals = view.timeline.flatMap((item) => (item.kind === 'approval' && item.approval ? [item.approval] : []))

    expect(approvals.map((approval) => approval.id)).toEqual(['discussion-runtime-approval-conv-1-run-a-approval-1', 'discussion-runtime-approval-conv-1-run-b-approval-1'])
    expect(approvals.map((approval) => approval.actions[0].path)).toEqual(['/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-run-a-approval-1/decision', '/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-run-b-approval-1/decision'])
  })

  it('maps discussion attachment summaries and download links', () => {
    const list = mapDiscussionListView({
      discussions: [
        {
          discussion_id: 'conv-1',
          title: 'Launch',
          status: 'completed',
          linked_missions: 0,
          attachment_count: 2,
          updated_at: '2026-06-02T00:01:00Z',
          unread_completed: false
        }
      ]
    })
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [],
      linked_missions: [],
      attachments: [
        {
          attachment_id: 'att-1',
          discussion_id: 'conv-1',
          file_name: 'brief.txt',
          content_type: 'text/plain',
          size_bytes: 12,
          uploaded_by: 'user-1',
          uploaded_by_kind: 'user',
          uploaded_by_name: 'Operator',
          download_url: '/api/discussions/conv-1/attachments/att-1/download',
          created_at: '2026-06-02T00:01:00Z'
        }
      ]
    })

    expect(list.discussions[0].attachmentCount).toBe(2)
    expect(view.attachments).toEqual([
      {
        id: 'att-1',
        discussionId: 'conv-1',
        messageId: '',
        fileName: 'brief.txt',
        contentType: 'text/plain',
        sizeBytes: 12,
        uploadedBy: 'user-1',
        uploadedByKind: 'user',
        uploadedByName: 'Operator',
        storagePath: '',
        downloadUrl: '/api/discussions/conv-1/attachments/att-1/download',
        createdAt: expect.any(String)
      }
    ])
  })

  it('maps message attachments and sends selected attachment ids', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-1',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: 'Please use this brief',
          created_at: '2026-06-02T00:01:00Z',
          attachments: [
            {
              attachment_id: 'att-1',
              discussion_id: 'conv-1',
              message_id: 'msg-1',
              file_name: 'brief.txt',
              content_type: 'text/plain',
              size_bytes: 12,
              uploaded_by: 'user-1',
              uploaded_by_kind: 'user',
              uploaded_by_name: 'Operator',
              download_url: '/api/discussions/conv-1/attachments/att-1/download',
              created_at: '2026-06-02T00:01:00Z'
            }
          ]
        }
      ],
      runs: [],
      linked_missions: []
    } as any)

    expect(view.messages[0].attachments).toEqual([
      expect.objectContaining({
        id: 'att-1',
        messageId: 'msg-1',
        fileName: 'brief.txt',
        downloadUrl: '/api/discussions/conv-1/attachments/att-1/download'
      })
    ])
    expect(
      mapDiscussionMessageRequest({
        content: 'Use it',
        attachmentIds: ['att-1', ' att-2 ', '']
      } as any)
    ).toEqual({
      content: 'Use it',
      attachment_ids: ['att-1', 'att-2']
    })
  })

  it('maps orchestration warnings attached to preserved Agent answers', () => {
    const warning = '已创建 Mission，但 Lead Agent 直接给出了答复。'
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:01:00Z',
      messages: [{
        message_id: 'msg-1',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'lead-agent',
        content: '这是 Lead 直接给出的答案。',
        metadata: { orchestration_warning: `  ${warning}  ` },
        created_at: '2026-06-02T00:01:00Z'
      }],
      runs: [],
      linked_missions: []
    } as any)

    expect(view.messages[0].content).toBe('这是 Lead 直接给出的答案。')
    expect(view.messages[0].orchestrationWarning).toBe(warning)
  })

  it('maps message page metadata and prepends older messages without duplicates', () => {
    const current = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:03:00Z',
      unread_completed: false,
      message_page: {
        limit: 20,
        offset: 0,
        total: 3,
        returned: 2,
        has_more: true
      },
      messages: [
        {
          message_id: 'msg-2',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'newer',
          created_at: '2026-06-02T00:02:00Z'
        },
        {
          message_id: 'msg-3',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'latest',
          created_at: '2026-06-02T00:03:00Z'
        }
      ],
      runs: [],
      linked_missions: []
    })
    const older = mapDiscussionMessagePageView({
      page: { limit: 20, offset: 2, total: 3, returned: 1, has_more: false },
      messages: [
        {
          message_id: 'msg-1',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: 'oldest',
          created_at: '2026-06-02T00:01:00Z'
        },
        {
          message_id: 'msg-2',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'newer duplicate',
          created_at: '2026-06-02T00:02:00Z'
        }
      ]
    })

    const merged = prependDiscussionMessagePage(current, older)

    expect(older.page.hasMore).toBe(false)
    expect(merged.messagePage).toEqual({
      limit: 20,
      offset: 2,
      total: 3,
      returned: 1,
      hasMore: false
    })
    expect(merged.messages.map((message) => message.id)).toEqual(['msg-1', 'msg-2', 'msg-3'])
    expect(merged.timeline.map((item) => item.id)).toEqual(['msg-1', 'msg-2', 'msg-3'])
  })

  it('orders discussion messages by backend timestamps even when the payload is not ordered', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:03:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-3',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'latest',
          created_at: '2026-06-02T00:03:00Z'
        },
        {
          message_id: 'msg-1',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: 'oldest',
          created_at: '2026-06-02T00:01:00Z'
        },
        {
          message_id: 'msg-2',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'middle',
          created_at: '2026-06-02T00:02:00Z'
        }
      ],
      runs: [],
      linked_missions: []
    })

    expect(view.messages.map((message) => message.id)).toEqual(['msg-1', 'msg-2', 'msg-3'])
    expect(view.timeline.map((item) => item.id)).toEqual(['msg-1', 'msg-2', 'msg-3'])
  })

  it('orders prepended discussion message pages by timestamps instead of API array order', () => {
    const current = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:04:00Z',
      unread_completed: false,
      message_page: {
        limit: 20,
        offset: 0,
        total: 4,
        returned: 2,
        has_more: true
      },
      messages: [
        {
          message_id: 'msg-4',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'latest',
          created_at: '2026-06-02T00:04:00Z'
        },
        {
          message_id: 'msg-3',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'newer',
          created_at: '2026-06-02T00:03:00Z'
        }
      ],
      runs: [],
      linked_missions: []
    })
    const older = mapDiscussionMessagePageView({
      page: { limit: 20, offset: 2, total: 4, returned: 2, has_more: false },
      messages: [
        {
          message_id: 'msg-2',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'middle',
          created_at: '2026-06-02T00:02:00Z'
        },
        {
          message_id: 'msg-1',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: 'oldest',
          created_at: '2026-06-02T00:01:00Z'
        }
      ]
    })

    const merged = prependDiscussionMessagePage(current, older)

    expect(merged.messages.map((message) => message.id)).toEqual(['msg-1', 'msg-2', 'msg-3', 'msg-4'])
    expect(merged.timeline.map((item) => item.id)).toEqual(['msg-1', 'msg-2', 'msg-3', 'msg-4'])
  })

  it('maps default discussion members from the backend', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '讨论组',
      status: 'completed',
      created_by: 'local-user',
      created_by_name: '本地用户',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      members: [
        {
          kind: 'agent',
          id: 'agent-alpha',
          display_name: '顾明澈',
          role: 'Lead'
        },
        {
          kind: 'agent',
          id: 'agent-beta',
          display_name: '秦若兰',
          role: 'Staffing'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '话题发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    expect(view.members).toEqual([
      { kind: 'agent', id: 'agent-alpha', name: '顾明澈', role: 'Lead' },
      { kind: 'agent', id: 'agent-beta', name: '秦若兰', role: 'Staffing' },
      { kind: 'user', id: 'local-user', name: '本地用户', role: '话题发起人' }
    ])
  })

  it('maps direct discussions as private chats with the target agent', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-direct',
      conversation_kind: 'direct',
      direct_agent_id: 'staffing-agent',
      direct_target_kind: 'agent',
      direct_target_id: 'staffing-agent',
      title: '秦若兰',
      status: 'completed',
      created_by: 'local-user',
      created_by_name: '本地用户',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:02:00Z',
      unread_completed: false,
      messages: [],
      members: [
        {
          kind: 'agent',
          id: 'staffing-agent',
          display_name: '秦若兰',
          role: '私聊对象'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '话题发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    expect(view.kind).toBe('direct')
    expect(view.directTargetKind).toBe('agent')
    expect(view.directTargetId).toBe('staffing-agent')
    expect(view.directTargetName).toBe('秦若兰')
    expect(view.directAgentId).toBe('staffing-agent')
    expect(view.directAgentName).toBe('秦若兰')
    expect(view.title).toBe('秦若兰')
  })

  it('maps direct discussions with a human target without treating them as agents', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-direct-human',
      conversation_kind: 'direct',
      direct_target_kind: 'user',
      direct_target_id: 'human-reviewer',
      title: '陆清岚',
      status: 'completed',
      created_by: 'local-user',
      created_by_name: '本地用户',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:02:00Z',
      unread_completed: false,
      messages: [],
      members: [
        {
          kind: 'user',
          id: 'human-reviewer',
          display_name: '陆清岚',
          role: '私聊对象'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '话题发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    expect(view.kind).toBe('direct')
    expect(view.directTargetKind).toBe('user')
    expect(view.directTargetId).toBe('human-reviewer')
    expect(view.directTargetName).toBe('陆清岚')
    expect(view.directAgentId).toBe('')
    expect(view.directAgentName).toBe('')
  })

  it('keeps timeline order with backend timestamps when formatted minute labels match', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '讨论组',
      status: 'completed',
      created_by: 'local-user',
      created_at: '2026-06-05T07:33:00Z',
      updated_at: '2026-06-05T07:34:29Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          author_name: '本地用户',
          content: '@一下协作员',
          created_at: '2026-06-05T07:33:53.0794752Z'
        },
        {
          message_id: 'msg-lead',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          author_name: '顾明澈',
          content: '[@秦若兰](mention://agent/agent-beta) 麻烦处理。',
          created_at: '2026-06-05T07:34:04.7352614Z'
        },
        {
          message_id: 'msg-staffing',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-beta',
          author_name: '秦若兰',
          content: '[@顾明澈](mention://agent/agent-alpha) 收到。',
          created_at: '2026-06-05T07:34:29.42927Z'
        }
      ],
      runs: [
        {
          run_id: 'run-lead',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-05T07:33:53.1009021Z',
          tool_calls: [{
            tool_call_id: 'tool-lead',
            name: 'discussion_ack',
            status: 'completed',
            created_at: '2026-06-05T07:33:53.1009021Z'
          }]
        },
        {
          run_id: 'run-staffing',
          discussion_id: 'conv-1',
          agent_id: 'agent-beta',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-05T07:34:04.782488Z',
          tool_calls: [{
            tool_call_id: 'tool-staffing',
            name: 'discussion_ack',
            status: 'completed',
            created_at: '2026-06-05T07:34:04.782488Z'
          }]
        }
      ],
      linked_missions: []
    })

    expect(view.messages[1].createdAt).toBe(view.messages[2].createdAt)
    expect(view.timeline.map((item) => item.id)).toEqual(['msg-user', 'msg-lead', 'msg-staffing'])
    expect(view.runs.map((run) => run.toolCalls.map((tool) => tool.id))).toEqual([['tool-lead'], ['tool-staffing']])
  })

  it('deduplicates linked missions by mission id', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:05:00Z',
      unread_completed: false,
      messages: [],
      runs: [],
      linked_missions: [
        {
          mission_id: 'mission-1',
          title: 'Launch plan',
          status: 'active',
          created_at: '2026-06-02T00:01:00Z'
        },
        {
          mission_id: 'mission-1',
          title: 'Launch plan duplicate',
          status: 'active',
          created_at: '2026-06-02T00:02:00Z'
        },
        {
          mission_id: 'mission-2',
          title: 'Follow-up',
          status: 'active',
          created_at: '2026-06-02T00:03:00Z'
        }
      ]
    })

    expect(view.linkedMissions.map((mission) => mission.id)).toEqual(['mission-1', 'mission-2'])
  })

  it('attaches agent acknowledgement events to the source message', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      members: [
        {
          kind: 'agent',
          id: 'agent-beta',
          display_name: '秦若兰',
          role: 'Staffing'
        }
      ],
      messages: [
        {
          message_id: 'msg-lead',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          author_name: '顾明澈',
          content: '[@秦若兰](mention://agent/agent-beta) 请确认一下。',
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      runs: [],
      events: [
        {
          event_id: 'event-ack',
          discussion_id: 'conv-1',
          kind: 'discussion_agent_acknowledged',
          status: 'completed',
          metadata: {
            source_message_id: 'msg-lead',
            target_agent_id: 'agent-beta',
            emoji: '👍'
          },
          created_at: '2026-06-02T00:01:02Z'
        }
      ],
      linked_missions: []
    })

    expect(view.messages[0].acknowledgements).toEqual([
      {
        id: 'event-ack',
        agentId: 'agent-beta',
        agentName: '秦若兰',
        emoji: '👍',
        createdAt: 'Jun 02, 08:01 AM'
      }
    ])
    expect(view.timeline[0].message?.acknowledgements?.[0].emoji).toBe('👍')
  })

  it('builds a chronological message timeline while keeping tool-only runs out of the main timeline', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user-1',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: 'Plan launch',
          created_at: '2026-06-02T00:00:00Z'
        },
        {
          message_id: 'msg-assistant-1',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          content: 'Mission created.',
          created_at: '2026-06-02T00:01:00Z'
        },
        {
          message_id: 'msg-user-2',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: 'Continue in the same mission',
          created_at: '2026-06-02T00:02:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'codex',
          created_at: '2026-06-02T00:00:00Z',
          tool_calls: [
            {
              tool_call_id: 'tool-1',
              name: 'mission_brief_create',
              status: 'completed',
              created_at: '2026-06-02T00:00:30Z'
            }
          ]
        },
        {
          run_id: 'run-2',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'codex',
          created_at: '2026-06-02T00:02:00Z',
          tool_calls: [
            {
              tool_call_id: 'tool-2',
              name: 'mission_task_create_root',
              status: 'completed',
              created_at: '2026-06-02T00:02:30Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user-1', 'message:msg-assistant-1', 'message:msg-user-2'])
    expect(view.runs[0].toolCalls).toMatchObject([{ name: 'mission_brief_create', status: 'completed' }])
    expect(view.runs[1].toolCalls).toMatchObject([{ name: 'mission_task_create_root', status: 'completed' }])
  })

  it('keeps a personal-assistant run in the timeline even when legacy run mode is group', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-assistant',
      conversation_kind: 'personal_assistant',
      title: 'My assistant',
      status: 'completed',
      created_by: 'human-ding',
      created_at: '2026-08-12T00:00:00Z',
      updated_at: '2026-08-12T00:03:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-assistant',
          role: 'user',
          author_id: 'human-ding',
          content: 'Check this',
          created_at: '2026-08-12T00:00:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-assistant',
          run_id: 'run-assistant',
          role: 'assistant',
          author_id: 'assistant-ding',
          content: 'Done.',
          created_at: '2026-08-12T00:03:00Z'
        }
      ],
      runs: [{
        run_id: 'run-assistant',
        discussion_id: 'conv-assistant',
        mode: 'group',
        agent_id: 'assistant-ding',
        status: 'completed',
        runtime_kind: 'codex',
        created_at: '2026-08-12T00:01:00Z',
        completed_at: '2026-08-12T00:02:00Z'
      }]
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual([
      'message:msg-user',
      'run:run-assistant',
      'message:msg-agent'
    ])
  })

  it('preserves tool call arguments and results for chat trace rendering', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'codex',
          created_at: '2026-06-02T00:00:00Z',
          tool_calls: [
            {
              tool_call_id: 'tool-1',
              name: 'mission_prepare',
              status: 'completed',
              arguments: { objective: 'Launch plan', assignee: 'agent-alpha' },
              result: { mission_id: 'mission-1', operation: 'created' },
              created_at: '2026-06-02T00:00:30Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect((view.runs[0].toolCalls[0] as any).arguments).toEqual({
      objective: 'Launch plan',
      assignee: 'agent-alpha'
    })
    expect(view.runs[0].toolCalls[0].result).toEqual({
      mission_id: 'mission-1',
      operation: 'created'
    })
  })

  it('keeps persisted tool calls on the run without adding a fallback work card', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Recruiting',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-13T11:14:00Z',
      updated_at: '2026-06-13T11:14:06Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: '发布保洁岗位',
          created_at: '2026-06-13T11:14:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          content: '当前情况汇报。',
          created_at: '2026-06-13T11:14:06Z'
        }
      ],
      runs: [
        {
          run_id: 'run-agent-a',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-13T11:14:01Z',
          completed_at: '2026-06-13T11:14:06Z',
          tool_calls: [
            {
              tool_call_id: 'company-list',
              name: 'mcp_company_list',
              status: 'completed',
              created_at: '2026-06-13T11:14:02Z'
            },
            {
              tool_call_id: 'job-update',
              name: 'mcp_job_update',
              status: 'completed',
              created_at: '2026-06-13T11:14:04Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:msg-agent'])
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mcp_company_list', 'mcp_job_update'])
  })

  it('projects persisted direct-run tools into one work card before the complete answer', () => {
    const segments = JSON.stringify([
      {
        kind: 'text',
        text: '好问题，让我看看咱们当前有谁可以拉进来！先查一下人才库 👀'
      },
      { kind: 'tool', run_id: 'run-agent-a', tool_call_id: 'talent-list' },
      {
        kind: 'text',
        text: '目前咱们群里只有你我二人，以下是可以拉进来的候选成员：'
      }
    ])
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: 'Recruiting',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-13T12:04:00Z',
      updated_at: '2026-06-13T12:04:06Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: '看看能拉谁进来',
          created_at: '2026-06-13T12:04:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          content: '好问题，让我看看咱们当前有谁可以拉进来！先查一下人才库 👀\n目前咱们群里只有你我二人，以下是可以拉进来的候选成员：',
          metadata: { display_timeline_json: segments },
          created_at: '2026-06-13T12:04:06Z'
        }
      ],
      runs: [
        {
          run_id: 'run-agent-a',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-13T12:04:01Z',
          completed_at: '2026-06-13T12:04:06Z',
          tool_calls: [
            {
              tool_call_id: 'talent-list',
              name: 'discussion_talent_list',
              status: 'completed',
              created_at: '2026-06-13T12:04:02Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'run:run-agent-a', 'message:msg-agent'])
    expect(view.timeline.filter((item) => item.kind === 'run' && item.run?.id === 'run-agent-a')).toHaveLength(1)
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['discussion_talent_list'])
    expect(view.timeline[2].message?.content).toContain('先查一下人才库')
    expect(view.timeline[2].message?.content).toContain('目前咱们群里')
  })

  it('keeps completed assistant text without adding same-run tool calls to the main timeline', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Balance sheet visualizer',
      status: 'completed',
      created_by: 'superadmin',
      created_at: '2026-06-15T07:23:00Z',
      updated_at: '2026-06-15T07:24:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'superadmin',
          content: '基于上传的 Excel 做资产负债表可视化',
          created_at: '2026-06-15T07:23:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          author_name: '顾明澈',
          content: '好的 @superadmin，那我先看看你上传的 Excel 数据结构，然后安排开发同学来做。\n\n让我先看看我的技能情况。好的，我来梳理一下，准备开始编排。\n\n先创建 Mission Brief：Mission Brief 和根任务已创建成功！',
          created_at: '2026-06-15T07:24:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-agent-a',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-15T07:23:30Z',
          completed_at: '2026-06-15T07:24:00Z',
          tool_calls: [
            {
              tool_call_id: 'skills',
              name: 'list_skills',
              status: 'completed',
              created_at: '2026-06-15T07:23:40Z'
            },
            {
              tool_call_id: 'brief',
              name: 'mission_prepare',
              status: 'completed',
              created_at: '2026-06-15T07:23:50Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:msg-agent'])
    expect(view.runs[0].toolCalls.map((tool) => tool.id)).toEqual(['skills', 'brief'])
  })

  it('keeps linked direct-run progress above the answer when display metadata is missing', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-direct',
      conversation_kind: 'direct',
      direct_agent_id: 'agent-a',
      title: 'Claude Agent',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-15T07:23:00Z',
      updated_at: '2026-06-15T07:24:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-direct',
          role: 'user',
          author_id: 'user-1',
          content: 'Inspect the project',
          created_at: '2026-06-15T07:23:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-direct',
          run_id: 'run-agent-a',
          role: 'assistant',
          author_id: 'agent-a',
          content: 'Inspection complete.',
          created_at: '2026-06-15T07:24:00Z'
        }
      ],
      runs: [{
        run_id: 'run-agent-a',
        discussion_id: 'conv-direct',
        mode: 'direct',
        agent_id: 'agent-a',
        status: 'completed',
        runtime_kind: 'claude',
        created_at: '2026-06-15T07:23:30Z',
        completed_at: '2026-06-15T07:24:00Z',
        tool_calls: [{
          tool_call_id: 'read-1',
          name: 'Read',
          status: 'completed',
          created_at: '2026-06-15T07:23:40Z'
        }]
      }],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual([
      'message:msg-user',
      'run:run-agent-a',
      'message:msg-agent'
    ])
    expect(view.timeline.filter((item) => item.kind === 'run' && item.run?.id === 'run-agent-a')).toHaveLength(1)
  })

  it('places leading tool-only display timeline work before the complete answer', () => {
    const content = '好的 @superadmin，那我先看看你上传的 Excel 数据结构，然后安排开发同学来做。\n\n让我先看看我的技能情况。好的，我来梳理一下，准备开始编排。\n\n先创建 Mission Brief：Mission Brief 和根任务已创建成功！'
    const segments = JSON.stringify([
      { kind: 'tool', run_id: 'run-agent-a', tool_call_id: 'skills' },
      { kind: 'tool', run_id: 'run-agent-a', tool_call_id: 'brief' },
      { kind: 'text', text: content }
    ])
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: 'Balance sheet visualizer',
      status: 'completed',
      created_by: 'superadmin',
      created_at: '2026-06-15T07:23:00Z',
      updated_at: '2026-06-15T07:24:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'superadmin',
          content: '基于上传的 Excel 做资产负债表可视化',
          created_at: '2026-06-15T07:23:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          author_name: '顾明澈',
          content,
          metadata: { display_timeline_json: segments },
          created_at: '2026-06-15T07:24:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-agent-a',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-15T07:23:30Z',
          completed_at: '2026-06-15T07:24:00Z',
          tool_calls: [
            {
              tool_call_id: 'skills',
              name: 'list_skills',
              status: 'completed',
              created_at: '2026-06-15T07:23:40Z'
            },
            {
              tool_call_id: 'brief',
              name: 'mission_prepare',
              status: 'completed',
              created_at: '2026-06-15T07:23:50Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'run:run-agent-a', 'message:msg-agent'])
    expect(view.runs[0].toolCalls.map((tool) => tool.id)).toEqual(['skills', 'brief'])
    expect(view.timeline[2].message?.content).toBe(content)
  })

  it('keeps mission brief update statuses while hiding unmarked duplicate creates', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'codex',
          created_at: '2026-06-02T00:00:00Z',
          tool_calls: [
            {
              tool_call_id: 'brief-1',
              name: 'mission_brief_create',
              status: 'completed',
              created_at: '2026-06-02T00:00:30Z'
            },
            {
              tool_call_id: 'task-1',
              name: 'mission_task_create_root',
              status: 'completed',
              created_at: '2026-06-02T00:00:45Z'
            }
          ]
        },
        {
          run_id: 'run-2',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'codex',
          created_at: '2026-06-02T00:02:00Z',
          tool_calls: [
            {
              tool_call_id: 'brief-2',
              name: 'mission_brief_create',
              status: 'completed',
              result: { operation: 'updated', mission_id: 'mission-1' },
              created_at: '2026-06-02T00:02:30Z'
            },
            {
              tool_call_id: 'task-2',
              name: 'mission_task_create_root',
              status: 'completed',
              created_at: '2026-06-02T00:02:45Z'
            }
          ]
        }
      ],
      linked_missions: [
        {
          mission_id: 'mission-1',
          title: 'Launch plan',
          status: 'active',
          created_at: '2026-06-02T00:01:00Z'
        }
      ]
    })

    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mission_brief_create', 'mission_task_create_root'])
    expect(view.runs[1].toolCalls.map((tool) => tool.name)).toEqual(['mission_brief_create', 'mission_task_create_root'])
    expect(view.runs[1].toolCalls[0].result?.operation).toBe('updated')
    expect(view.timeline.some((item) => item.kind === 'run')).toBe(false)
  })

  it('maps persisted discussion orchestration failures into runtime errors', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Snake',
      status: 'failed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'failed',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      events: [
        {
          event_id: 'discussion-orchestration-incomplete-run-1',
          discussion_id: 'conv-1',
          kind: 'discussion_orchestration_incomplete',
          status: 'failed',
          summary: 'discussion orchestration incomplete: agent stopped before task completion',
          metadata: { run_id: 'run-1' },
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    expect(view.runtimeStatus).toBe('failed')
    expect(view.runtimeError).toBe('discussion orchestration incomplete: agent stopped before task completion')
    expect(view.runs[0].error).toBe('discussion orchestration incomplete: agent stopped before task completion')
  })

  it('maps persisted discussion runtime failures into runtime errors', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Snake',
      status: 'failed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'failed',
          runtime_kind: 'pi',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      events: [
        {
          event_id: 'discussion-runtime-failed-run-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_failed',
          status: 'failed',
          summary: '[NodeRunError] tool run_terminal not found in toolsNode',
          metadata: { run_id: 'run-1' },
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    expect(view.runtimeStatus).toBe('failed')
    expect(view.runtimeError).toBe('[NodeRunError] tool run_terminal not found in toolsNode')
    expect(view.runs[0].error).toBe('[NodeRunError] tool run_terminal not found in toolsNode')
  })

  it('does not render a work card for runs without plan steps or tool calls', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Snake',
      status: 'failed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:06:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          content: '嘿 都冒个泡',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'failed',
          runtime_kind: 'pi',
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      events: [
        {
          event_id: 'discussion-runtime-failed-run-1',
          discussion_id: 'conv-1',
          kind: 'discussion_runtime_failed',
          status: 'failed',
          summary: 'discussion runtime execution failed',
          metadata: { run_id: 'run-1' },
          created_at: '2026-06-02T00:01:00Z'
        }
      ],
      linked_missions: []
    })

    expect(view.runs[0].toolCalls).toEqual([])
    expect(view.runs[0].planSteps).toBeUndefined()
    expect(view.timeline.some((item) => item.kind === 'run')).toBe(false)
  })

  it('maps discussion message forms to API requests behind the page boundary', () => {
    expect(mapDiscussionMessageRequest({ content: 'Plan launch' })).toEqual({
      content: 'Plan launch'
    })
    expect(mapDiscussionMessageRequest({
      content: 'Use the new acceptance criteria',
      expectedTurnId: ' turn-active ',
      expectedRunId: ' run-active '
    })).toEqual({
      content: 'Use the new acceptance criteria',
      expected_turn_id: 'turn-active',
      expected_run_id: 'run-active'
    })
    expect(mapDiscussionMessageRequest({ content: 'Handle this', conversationKind: 'personal_assistant' })).toEqual({
      content: 'Handle this',
      conversation_kind: 'personal_assistant'
    })
    expect(
      mapDiscussionMessageRequest({
        content: '[@秦若兰](mention://agent/agent-beta) 请看下。'
      })
    ).toEqual({ content: '[@秦若兰](mention://agent/agent-beta) 请看下。' })
    expect(
      mapDiscussionMessageRequest({
        content: 'Can you staff this?',
        forceNewConversation: true,
        directTargetId: 'staffing-agent'
      })
    ).toEqual({
      content: 'Can you staff this?',
      force_new_conversation: true,
      direct_target_id: 'staffing-agent'
    })
    expect(
      mapDiscussionMessageRequest({
        content: 'Can you review this?',
        directTargetId: 'human-reviewer'
      })
    ).toEqual({
      content: 'Can you review this?',
      direct_target_id: 'human-reviewer'
    })
  })

  it('keeps pending mention messages structured for rich rendering', () => {
    const view = appendPendingDiscussionMessage(undefined, '[@秦若兰](mention://agent/agent-beta) 请看下。')

    expect(view.messages[0].content).toBe('[@秦若兰](mention://agent/agent-beta) 请看下。')
    expect(view.title).toBe('@秦若兰 请看下。')
  })

  it('keeps pending message attachments on the optimistic message', () => {
    const view = appendPendingDiscussionMessage(undefined, 'Use this', [
      {
        id: 'att-1',
        discussionId: 'conv-1',
        messageId: '',
        fileName: 'brief.txt',
        contentType: 'text/plain',
        sizeBytes: 12,
        uploadedBy: 'user-1',
        uploadedByKind: 'user',
        uploadedByName: 'Operator',
        storagePath: '',
        downloadUrl: '/api/discussions/conv-1/attachments/att-1/download',
        createdAt: 'Jun 02, 08:01 AM'
      }
    ])

    expect(view.messages[0].attachments?.map((item) => item.fileName)).toEqual(['brief.txt'])
  })

  it('maps discussion debug prompt DTOs into a raw debug view', () => {
    const debug = mapDiscussionRunDebugView({
      discussion_id: 'conv-1',
      run_id: 'run-1',
      outbound_prompt: 'system token=secret-system\n\nuser: Plan launch',
      runtime_response: 'response token=secret-response',
      provider_metadata: {
        pi_debug_runtime_input_path: 'D:\\Orrery\\pi-debug\\run-1\\runtime-input.json',
        pi_debug_provider_requests_path: 'D:\\Orrery\\pi-debug\\run-1\\provider-requests.jsonl'
      },
      selected: {
        agent_id: 'agent-alpha',
        runtime_kind: 'codex',
        model: 'gpt-5-codex',
        config_ref: 'runtime-ref'
      },
      created_at: '2026-06-02T00:01:00Z',
      redacted: false
    })

    expect(debug.runId).toBe('run-1')
    expect(debug.outboundPrompt).toContain('secret-system')
    expect(debug.runtimeResponse).toContain('secret-response')
    expect(debug.providerMetadata.pi_debug_runtime_input_path).toContain('runtime-input.json')
    expect(debug.providerMetadata.pi_debug_provider_requests_path).toContain('provider-requests.jsonl')
    expect(debug.selected).toMatchObject({
      agentId: 'agent-alpha',
      runtimeKind: 'codex',
      model: 'gpt-5-codex'
    })
  })

  it('normalizes zero-valued tool created_at (0001-01-01) to run.created_at fallback', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Test',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-15T00:00:00Z',
      updated_at: '2026-06-15T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-15T07:00:00Z',
          tool_calls: [
            {
              tool_call_id: 'tool-1',
              name: 'search',
              status: 'completed',
              // Zero timestamp (0001-01-01T00:00:00Z) serialized from backend
              created_at: '0001-01-01T00:00:00Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    const tool = view.runs[0].toolCalls[0]
    // Zero timestamp is normalized to run.created_at fallback
    expect(tool.createdAt).not.toContain('0001')
    expect(tool.sortAt).not.toContain('0001')
  })

  it('keeps running tool snapshots off the main timeline when completed_at is zero-valued', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Test',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-18T19:15:20Z',
      updated_at: '2026-06-18T19:16:40Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          author_name: 'User',
          content: '找个开发来给我写个设计文档',
          created_at: '2026-06-18T19:15:27Z'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'running',
          runtime_kind: 'claude',
          started_at: '2026-06-18T19:15:37Z',
          created_at: '2026-06-18T19:15:37Z',
          completed_at: '0001-01-01T00:00:00Z',
          tool_calls: [
            {
              tool_call_id: 'tool-1',
              name: 'webReader',
              status: 'running',
              created_at: '2026-06-18T19:16:38Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => item.id)).toEqual(['msg-user'])
    expect(view.runs[0].toolCalls.map((tool) => tool.id)).toEqual(['tool-1'])
    expect(view.timeline.map((item) => item.sortAt).join('\n')).not.toContain('0001')
  })

  it('keeps one full answer after the work card instead of splitting around tools', () => {
    const content = '使用技能和准备的任务简报'
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: 'Test',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-15T00:00:00Z',
      updated_at: '2026-06-15T00:01:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          author_name: 'User',
          content: 'start',
          created_at: '2026-06-15T07:23:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          author_name: 'Agent',
          content,
          metadata: {
            display_timeline_json: JSON.stringify([
              { kind: 'tool', run_id: 'run-1', tool_call_id: 'skills' },
              { kind: 'tool', run_id: 'run-1', tool_call_id: 'brief' },
              { kind: 'text', text: '中间文字' },
              { kind: 'tool', run_id: 'run-1', tool_call_id: 'search' }
            ])
          },
          created_at: '2026-06-15T07:24:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-15T07:23:30Z',
          completed_at: '2026-06-15T07:24:00Z',
          tool_calls: [
            {
              tool_call_id: 'skills',
              name: 'list_skills',
              status: 'completed',
              created_at: '2026-06-15T07:23:40Z'
            },
            {
              tool_call_id: 'brief',
              name: 'mission_prepare',
              status: 'completed',
              created_at: '2026-06-15T07:23:50Z'
            },
            {
              tool_call_id: 'search',
              name: 'web_search',
              status: 'completed',
              created_at: '2026-06-15T07:23:55Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'run:run-1', 'message:msg-agent'])
    expect(view.timeline[2].message?.content).toBe(content)
    expect(view.runs[0].toolCalls.map((tool) => tool.id)).toEqual(['skills', 'brief', 'search'])
  })

  it('maps structured plan display timeline segments into run progress steps', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: 'Test',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-15T00:00:00Z',
      updated_at: '2026-06-15T00:01:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          author_name: 'Agent',
          content: 'Completed 2 plan step(s).',
          metadata: {
            display_timeline_json: JSON.stringify([
              {
                kind: 'plan',
                run_id: 'run-1',
                plan: {
                  plan_id: 'plan-1',
                  status: 'active',
                  items: [
                    { id: 'step-1', text: 'Inspect runtime bridge', status: 'completed' },
                    { id: 'step-2', text: 'Persist progress', status: 'in_progress' }
                  ]
                }
              }
            ])
          },
          created_at: '2026-06-15T07:24:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'running',
          runtime_kind: 'pi',
          created_at: '2026-06-15T07:23:30Z'
        }
      ],
      linked_missions: []
    })

    const runItem = view.timeline.find((item) => item.kind === 'run' && item.displaySegment)
    expect(runItem?.run?.planSteps).toEqual([
      { id: 'step-1', text: 'Inspect runtime bridge', status: 'completed' },
      { id: 'step-2', text: 'Persist progress', status: 'in_progress' }
    ])
    expect(runItem?.run?.planStatus).toBe('active')
  })

  it('maps durable Claude tasks from an aborted run without relying on message metadata', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: 'Test',
      status: 'cancelled',
      created_by: 'user-1',
      created_at: '2026-06-15T00:00:00Z',
      updated_at: '2026-06-15T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [{
        run_id: 'run-claude',
        discussion_id: 'conv-1',
        has_plan: true,
        plan: {
          plan_id: 'claude-tasks',
          status: 'cancelled',
          items: [{ id: '1', text: 'Build the game', status: 'cancelled' }]
        },
        agent_id: 'agent-a',
        status: 'cancelled',
        runtime_kind: 'claude',
        created_at: '2026-06-15T07:23:30Z'
      }],
      linked_missions: []
    })

    expect(view.runs[0].planStatus).toBe('cancelled')
    expect(view.runs[0].planSteps).toEqual([{ id: '1', text: 'Build the game', status: 'cancelled' }])
    expect(view.timeline.some((item) => item.kind === 'run' && item.run?.id === 'run-claude')).toBe(true)
  })

  it('keeps late plan stream updates on completed runs', () => {
    const current = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Test',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-15T00:00:00Z',
      updated_at: '2026-06-15T00:01:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'pi',
          created_at: '2026-06-15T07:23:30Z',
          completed_at: '2026-06-15T07:24:00Z'
        }
      ],
      linked_missions: []
    })

    const next = applyDiscussionStreamEvent(current, {
      kind: 'plan',
      run_id: 'run-1',
      plan: {
        plan_id: 'plan-1',
        status: 'completed',
        items: [
          { id: 'step-1', text: 'Inspect runtime bridge', status: 'completed' },
          { id: 'step-2', text: 'Persist progress', status: 'completed' }
        ]
      }
    })

    expect(next?.runs[0].planStatus).toBe('completed')
    expect(next?.runs[0].planSteps?.map((step) => step.status)).toEqual(['completed', 'completed'])
    expect(next?.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['run:run-1'])
  })

  it('keeps a single direct-run tool in the work card before the answer', () => {
    const content = '搜索一下'
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: 'Test',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-15T00:00:00Z',
      updated_at: '2026-06-15T00:01:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'user-1',
          author_name: 'User',
          content: 'search',
          created_at: '2026-06-15T07:23:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-a',
          author_name: 'Agent',
          content,
          metadata: {
            display_timeline_json: JSON.stringify([
              { kind: 'text', text: content },
              { kind: 'tool', run_id: 'run-1', tool_call_id: 'search' }
            ])
          },
          created_at: '2026-06-15T07:24:00Z'
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-a',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-15T07:23:30Z',
          completed_at: '2026-06-15T07:24:00Z',
          tool_calls: [
            {
              tool_call_id: 'search',
              name: 'web_search',
              status: 'completed',
              created_at: '2026-06-15T07:23:40Z'
            }
          ]
        }
      ],
      linked_missions: []
    })

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'run:run-1', 'message:msg-agent'])
    expect(view.runs[0].toolCalls).toHaveLength(1)
  })
  it('shows result authors and PA execution hosts from persisted provenance', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-mission',
      conversation_kind: 'group',
      personal_assistant_runtime: true,
      title: 'Plane game',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-08-31T01:00:00Z',
      updated_at: '2026-08-31T01:02:00Z',
      unread_completed: false,
      members: [
        { kind: 'agent', id: 'A000010', display_name: 'Gangbeng' },
        { kind: 'agent', id: 'A000003', display_name: 'Dafu' }
      ],
      messages: [
        {
          message_id: 'result-1', discussion_id: 'conv-mission', role: 'system', author_id: 'orrery', author_name: 'Orrery',
          content: 'Validation completed', metadata: { kind: 'agent_result', agent_id: 'A000003', agent_name: 'Dafu' },
          created_at: '2026-08-31T01:01:00Z'
        },
        {
          message_id: 'reply-1', discussion_id: 'conv-mission', run_id: 'run-pa', role: 'assistant',
          author_id: 'personal-assistant-user-1', author_name: 'Personal Assistant', content: 'Done',
          created_at: '2026-08-31T01:02:00Z'
        }
      ],
      runs: [{
        run_id: 'run-pa', discussion_id: 'conv-mission', agent_id: 'A000010', logical_agent_id: 'personal-assistant-user-1',
        agent_name: 'Personal Assistant', status: 'completed', runtime_kind: 'pi', created_at: '2026-08-31T01:01:30Z'
      }],
      linked_missions: [],
      attachments: []
    })

    expect(view.messages[0]).toMatchObject({ authorId: 'A000003', authorName: 'Dafu' })
    expect(view.messages[1]).toMatchObject({ authorId: 'personal-assistant-user-1', authorName: 'Personal Assistant / Gangbeng' })
    expect(view.runs[0]).toMatchObject({ agentId: 'A000010', logicalAgentId: 'personal-assistant-user-1', agentName: 'Gangbeng' })
  })

  it('maps HTML attachments to durable preview URLs without exposing storage paths', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1', title: 'Game', status: 'completed', created_by: 'user-1',
      created_at: '2026-08-31T01:00:00Z', updated_at: '2026-08-31T01:00:00Z', unread_completed: false,
      messages: [], runs: [], linked_missions: [],
      attachments: [{
        attachment_id: 'att-game', discussion_id: 'conv-1', file_name: 'index.html', content_type: 'text/html', size_bytes: 128,
        uploaded_by: 'A000001', download_url: '/api/discussions/conv-1/attachments/att-game/download',
        preview_url: '/api/discussions/conv-1/attachments/att-game/preview', created_at: '2026-08-31T01:00:00Z'
      }]
    })

    expect(view.attachments?.[0]).toMatchObject({
      storagePath: '',
      downloadUrl: '/api/discussions/conv-1/attachments/att-game/download',
      previewUrl: '/api/discussions/conv-1/attachments/att-game/preview'
    })
  })
})

function runtimeApprovalActions(path: string) {
  return [
    {
      action_id: 'approve_once',
      label: 'Approve once',
      style: 'primary',
      decision: 'approved',
      effect: 'once',
      input_kind: 'message',
      method: 'POST',
      path,
      requires_body: true,
      confirmation_required: true,
      reason_required: false
    },
    {
      action_id: 'approve_session',
      label: 'Approve for session',
      style: 'primary',
      decision: 'approved',
      effect: 'session',
      input_kind: 'message',
      method: 'POST',
      path,
      requires_body: true,
      confirmation_required: true,
      reason_required: false,
      remember_allowed: true
    },
    {
      action_id: 'deny',
      label: 'Deny',
      style: 'danger',
      decision: 'denied',
      effect: 'deny',
      input_kind: 'message',
      method: 'POST',
      path,
      requires_body: true,
      confirmation_required: true,
      reason_required: false
    },
    {
      action_id: 'other',
      label: 'Other',
      style: 'secondary',
      decision: 'approved',
      effect: 'once',
      input_kind: 'message',
      method: 'POST',
      path,
      requires_body: true,
      confirmation_required: true,
      reason_required: false
    }
  ]
}
