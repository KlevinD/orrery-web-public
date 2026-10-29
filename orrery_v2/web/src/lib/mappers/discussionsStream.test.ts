import { describe, expect, it } from 'vitest'
import { applyDiscussionStreamEvent, mapDiscussionView } from './discussions'

describe('discussion stream mapper', () => {
  it('renders token deltas as a temporary streaming assistant message', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
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
          id: 'user-1',
          display_name: '本地用户',
          role: '话题发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    const afterRun = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-06-02T00:00:00Z'
      }
    })
    const afterFirstToken = applyDiscussionStreamEvent(afterRun, {
      kind: 'token_delta',
      delta: 'What '
    })
    const afterSecondToken = applyDiscussionStreamEvent(afterFirstToken, {
      kind: 'token_delta',
      delta: 'deadline?'
    })

    expect(afterSecondToken).toBeDefined()
    if (!afterSecondToken) throw new Error('expected token delta to keep the discussion view')
    expect(afterSecondToken.runtimeStatus).toBe('running')
    expect(afterSecondToken.activeResponders).toEqual(['顾明澈'])
    expect(afterSecondToken.messages.at(-1)).toMatchObject({
      id: 'streaming-run-1',
      role: 'assistant',
      authorId: 'agent-alpha',
      authorName: '顾明澈',
      content: 'What deadline?'
    })
    expect(afterSecondToken.timeline.at(-1)?.id).toBe('streaming-run-1')
  })

  it('retains streamed Claude text across authoritative snapshots while the run is active', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      revision: 1,
      title: 'Claude runtime',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [{
        run_id: 'run-claude',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        agent_name: 'Claude Agent',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-02T00:00:00Z'
      }],
      linked_missions: []
    })
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-claude',
      sequence: 1,
      delta: 'Inspecting the runtime.'
    })!

    view = applyDiscussionStreamEvent(view, {
      kind: 'snapshot',
      revision: 2,
      discussion: {
        discussion_id: 'conv-1',
        revision: 2,
        title: 'Claude runtime',
        status: 'running',
        created_by: 'user-1',
        created_at: '2026-06-02T00:00:00Z',
        updated_at: '2026-06-02T00:00:01Z',
        unread_completed: false,
        messages: [],
        runs: [{
          run_id: 'run-claude',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          agent_name: 'Claude Agent',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:00Z'
        }],
        linked_missions: []
      }
    })!

    expect(view.messages).toHaveLength(1)
    expect(view.messages[0]).toMatchObject({ id: 'streaming-run-claude', content: 'Inspecting the runtime.' })
    expect(view.timeline.some((item) => item.id === 'streaming-run-claude')).toBe(true)
  })

  it('retains live Claude plan progress across snapshots without restoring message bubbles', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      revision: 1,
      title: 'Claude plan',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [{
        run_id: 'run-claude',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        agent_name: 'Claude Agent',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-02T00:00:00Z'
      }],
      linked_missions: []
    })
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-claude',
      sequence: 1,
      delta: 'I will start with the parser.'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'plan',
      run_id: 'run-claude',
      plan: {
        plan_id: 'claude-tasks',
        status: 'active',
        items: [
          { id: '1', text: 'Inspect parser', status: 'completed' },
          { id: '2', text: 'Fix rendering', status: 'in_progress' }
        ]
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-claude',
      sequence: 2,
      delta: 'Applying the rendering fix.'
    })!

    view = applyDiscussionStreamEvent(view, {
      kind: 'snapshot',
      revision: 2,
      discussion: {
        discussion_id: 'conv-1',
        revision: 2,
        title: 'Claude plan',
        status: 'running',
        created_by: 'user-1',
        created_at: '2026-06-02T00:00:00Z',
        updated_at: '2026-06-02T00:00:01Z',
        unread_completed: false,
        messages: [],
        runs: [{
          run_id: 'run-claude',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          agent_name: 'Claude Agent',
          status: 'running',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:00Z'
        }],
        linked_missions: []
      }
    })!

    expect(view.messages.some((message) => message.id.startsWith('streaming-'))).toBe(false)
    expect(view.runs[0]).toMatchObject({
      hasPlan: true,
      planStatus: 'active',
      preview: 'I will start with the parser.Applying the rendering fix.'
    })
    expect(view.runs[0].planSteps?.map((step) => step.status)).toEqual(['completed', 'in_progress'])
    expect(view.timeline.filter((item) => item.id === 'run-claude')).toHaveLength(1)
  })

  it('keeps reasoning summaries on the run instead of adding them to answer text', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Runtime check',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [],
      linked_missions: []
    })
    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-06-02T00:00:00Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'thinking_delta',
      run_id: 'run-1',
      sequence: 1,
      delta: 'Checking '
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'thinking_delta',
      run_id: 'run-1',
      sequence: 2,
      delta: 'the runtime bridge.'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'thinking_delta',
      run_id: 'run-1',
      sequence: 1,
      delta: 'duplicate'
    })!

    expect(view.runs[0].thinking).toBe('Checking the runtime bridge.')
    expect(view.messages).toHaveLength(0)
    expect(view.timeline).toHaveLength(1)
    expect(view.timeline[0]).toMatchObject({ kind: 'run', run: { id: 'run-1' } })
  })

  it('restores a persisted reasoning summary from an authoritative snapshot', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      revision: 1,
      title: 'Runtime check',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [{
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-06-02T00:00:00Z'
      }],
      linked_missions: []
    })
    view = applyDiscussionStreamEvent(view, {
      kind: 'thinking_delta',
      run_id: 'run-1',
      sequence: 1,
      delta: 'Transient summary.'
    })!

    const refreshed = applyDiscussionStreamEvent(view, {
      kind: 'snapshot',
      revision: 2,
      discussion: {
        discussion_id: 'conv-1',
        revision: 2,
        title: 'Runtime check',
        status: 'running',
        created_by: 'user-1',
        created_at: '2026-06-02T00:00:00Z',
        updated_at: '2026-06-02T00:00:01Z',
        unread_completed: false,
        messages: [],
        runs: [{
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'codex',
          thinking_summary: 'Persisted summary after approval.',
          created_at: '2026-06-02T00:00:00Z'
        }],
        linked_missions: []
      }
    })

    expect(refreshed?.runs[0].thinking).toBe('Persisted summary after approval.')
    expect(refreshed?.timeline[0]).toMatchObject({ kind: 'run', run: { thinking: 'Persisted summary after approval.' } })
  })

  it('ignores late websocket token deltas for a completed run', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:03Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          author_name: '顾明澈',
          content: 'Done.',
          created_at: '2026-06-02T00:00:02Z'
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
          status: 'completed',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:01Z',
          completed_at: '2026-06-02T00:00:03Z'
        }
      ],
      linked_missions: []
    })

    const afterLateToken = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-1',
      sequence: 1,
      delta: 'Done.'
    })

    expect(afterLateToken?.messages.map((message) => message.content)).toEqual(['Done.'])
    expect(afterLateToken?.messages.some((message) => message.id.startsWith('streaming-'))).toBe(false)
    expect(afterLateToken?.timeline.map((item) => item.id)).toEqual(view.timeline.map((item) => item.id))
  })

  it('ignores late websocket run_started events for a completed run', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:03Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-1',
          role: 'assistant',
          author_id: 'agent-alpha',
          author_name: '顾明澈',
          content: 'Done.',
          created_at: '2026-06-02T00:00:02Z'
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
          status: 'completed',
          runtime_kind: 'claude',
          created_at: '2026-06-02T00:00:01Z',
          completed_at: '2026-06-02T00:00:03Z'
        }
      ],
      linked_missions: []
    })

    const afterLateStarted = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-02T00:00:01Z'
      }
    })

    expect(afterLateStarted?.status).toBe('completed')
    expect(afterLateStarted?.runtimeStatus).toBe('completed')
    expect(afterLateStarted?.activeResponders).toEqual([])
    expect(afterLateStarted?.runs[0].status).toBe('completed')
    expect(afterLateStarted?.timeline.map((item) => item.id)).toEqual(view.timeline.map((item) => item.id))
  })

  it('derives active responders from multiple running Agent runs', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
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
          kind: 'agent',
          id: 'coordinator-agent',
          display_name: '陆知行',
          role: 'Coordinator'
        }
      ],
      runs: [
        {
          run_id: 'run-lead',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'completed',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:00:00Z'
        },
        {
          run_id: 'run-staffing',
          discussion_id: 'conv-1',
          agent_id: 'agent-beta',
          status: 'running',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:01:00Z'
        },
        {
          run_id: 'run-coordinator',
          discussion_id: 'conv-1',
          agent_id: 'coordinator-agent',
          status: 'started',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:01:01Z'
        }
      ],
      linked_missions: []
    })

    expect(view.activeResponders).toEqual(['秦若兰', '陆知行'])
  })

  it('appends agent mention and follow-up reply message_completed events in timeline order', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '讨论组',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-05T07:33:00Z',
      updated_at: '2026-06-05T07:33:53Z',
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
          id: 'local-user',
          display_name: '本地用户',
          role: '话题发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-lead',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-alpha',
        author_name: '顾明澈',
        content: '[@秦若兰](mention://agent/agent-beta) 麻烦处理。',
        created_at: '2026-06-05T07:34:04.7352614Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-staffing',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-beta',
        author_name: '秦若兰',
        content: '[@顾明澈](mention://agent/agent-alpha) 收到。',
        created_at: '2026-06-05T07:34:29.42927Z'
      }
    })!

    expect(view.messages.map((message) => message.id)).toEqual(['msg-user', 'msg-lead', 'msg-staffing'])
    expect(view.timeline.map((item) => item.id)).toEqual(['msg-user', 'msg-lead', 'msg-staffing'])
  })

  it('orders completed stream messages by backend timestamps when events arrive out of order', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '讨论组',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-05T07:33:00Z',
      updated_at: '2026-06-05T07:33:53Z',
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
        }
      ],
      members: [],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-latest',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-beta',
        author_name: '秦若兰',
        content: '后完成但先到达。',
        created_at: '2026-06-05T07:34:29.42927Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-middle',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-alpha',
        author_name: '顾明澈',
        content: '先完成但后到达。',
        created_at: '2026-06-05T07:34:04.7352614Z'
      }
    })!

    expect(view.messages.map((message) => message.id)).toEqual(['msg-user', 'msg-middle', 'msg-latest'])
    expect(view.timeline.map((item) => item.id)).toEqual(['msg-user', 'msg-middle', 'msg-latest'])
  })

  it('keeps streamed tool status before the owning agent message', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '讨论组',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-05T07:33:00Z',
      updated_at: '2026-06-05T07:33:53Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          author_name: '本地用户',
          content: '[@秦若兰](mention://agent/staffing-agent) 拉人进来',
          created_at: '2026-06-05T07:33:53.0794752Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'staffing-agent',
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

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-staffing',
        discussion_id: 'conv-1',
        agent_id: 'staffing-agent',
        status: 'running',
        runtime_kind: 'managed',
        created_at: '2026-06-05T07:34:00Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_result',
      tool_call: {
        tool_call_id: 'member-add-1',
        name: 'discussion_member_add',
        status: 'completed',
        created_at: '2026-06-05T07:34:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-staffing',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'staffing-agent',
        author_name: '秦若兰',
        content: '收到，我已经拉人进来。',
        created_at: '2026-06-05T07:34:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'run_completed',
      run: {
        run_id: 'run-staffing',
        discussion_id: 'conv-1',
        agent_id: 'staffing-agent',
        status: 'completed',
        runtime_kind: 'managed',
        created_at: '2026-06-05T07:34:00Z',
        completed_at: '2026-06-05T07:34:01.500Z',
        tool_calls: [
          {
            tool_call_id: 'member-add-1',
            name: 'discussion_member_add',
            status: 'completed',
            created_at: '2026-06-05T07:34:01Z'
          }
        ]
      }
    })!

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:msg-staffing'])
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['discussion_member_add'])
  })

  it('keeps streamed tool calls on the run without placing them before assistant text deltas', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '招聘',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-13T11:14:00Z',
      updated_at: '2026-06-13T11:14:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '创建北京东城保洁岗位',
          created_at: '2026-06-13T11:14:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '智员a',
          role: 'Recruiter'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'managed',
        created_at: '2026-06-13T11:14:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'company-list',
        name: 'mcp_company_list',
        status: 'completed',
        created_at: '2026-06-13T11:14:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: '好的，岗位已经有了。'
    })!

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:streaming-run-agent-a'])
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mcp_company_list'])
  })

  it('keeps streamed text segments visible while storing tool calls on the run', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '招聘',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-13T11:14:00Z',
      updated_at: '2026-06-13T11:14:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '创建北京东城保洁岗位',
          created_at: '2026-06-13T11:14:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '智员a',
          role: 'Recruiter'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'managed',
        created_at: '2026-06-13T11:14:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: '好的，岗位已经有了。'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'job-update',
        name: 'mcp_job_update',
        status: 'completed',
        created_at: '2026-06-13T11:14:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: '地点已经修正为北京东城。'
    })!

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:streaming-run-agent-a'])
    expect(view.timeline[1].message?.content).toBe('好的，岗位已经有了。地点已经修正为北京东城。')
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mcp_job_update'])
  })

  it('replaces streamed segments with completed display timeline without leaving a duplicate run item', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: '招聘',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-13T11:14:00Z',
      updated_at: '2026-06-13T11:14:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '创建北京东城保洁岗位',
          created_at: '2026-06-13T11:14:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '智员a',
          role: 'Recruiter'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'managed',
        created_at: '2026-06-13T11:14:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: '我先查一下公司信息。'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'company-list',
        name: 'mcp_company_list',
        status: 'completed',
        created_at: '2026-06-13T11:14:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: '已经确认公司可用。'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-agent',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-a',
        author_name: '智员a',
        content: '我先查一下公司信息。\n已经确认公司可用。',
        metadata: {
          display_timeline_json: JSON.stringify([
            { kind: 'text', text: '我先查一下公司信息。' },
            {
              kind: 'tool',
              run_id: 'run-agent-a',
              tool_call_id: 'company-list'
            },
            { kind: 'text', text: '已经确认公司可用。' }
          ])
        },
        created_at: '2026-06-13T11:14:03Z'
      }
    })!

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'run:run-agent-a', 'message:msg-agent'])
    expect(view.timeline.filter((item) => item.kind === 'run' && item.run?.id === 'run-agent-a')).toHaveLength(1)
    expect(view.timeline[2].message?.content).toBe('我先查一下公司信息。\n已经确认公司可用。')
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mcp_company_list'])
  })

  it('removes all streaming text fragments when completed display timeline arrives', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      conversation_kind: 'direct',
      title: '设计',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-18T07:10:00Z',
      updated_at: '2026-06-18T07:10:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '读 spec',
          created_at: '2026-06-18T07:10:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '写代码的cc',
          role: 'Coder'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-18T07:10:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: "Now let me fetch Symphony's SPEC.md."
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'web-reader-1',
        name: 'webReader',
        status: 'running',
        created_at: '2026-06-18T07:10:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: 'Now I will inspect the local architecture.'
    })!
    expect(view.messages.map((message) => message.id).filter((id) => id.startsWith('streaming-'))).toEqual(['streaming-run-agent-a', 'streaming-run-agent-a:2'])

    view = applyDiscussionStreamEvent(view, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-agent',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-a',
        author_name: '写代码的cc',
        content: "Now let me fetch Symphony's SPEC.md.\nNow I will inspect the local architecture.",
        metadata: {
          display_timeline_json: JSON.stringify([
            { kind: 'text', text: "Now let me fetch Symphony's SPEC.md." },
            {
              kind: 'tool',
              run_id: 'run-agent-a',
              tool_call_id: 'web-reader-1'
            },
            {
              kind: 'text',
              text: 'Now I will inspect the local architecture.'
            }
          ])
        },
        created_at: '2026-06-18T07:10:04Z'
      }
    })!

    expect(view.messages.map((message) => message.id)).toEqual(['msg-user', 'msg-agent'])
    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'run:run-agent-a', 'message:msg-agent'])
    expect(view.timeline.some((item) => item.id.startsWith('streaming-'))).toBe(false)
  })

  it('keeps streamed tool calls in arrival order even when tool timestamps are earlier than text', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '招聘',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-13T11:14:00Z',
      updated_at: '2026-06-13T11:14:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '创建北京东城保洁岗位',
          created_at: '2026-06-13T11:14:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '智员a',
          role: 'Recruiter'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'managed',
        created_at: '2026-06-13T11:14:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      delta: '先解释一下计划。'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'job-update',
        name: 'mcp_job_update',
        status: 'completed',
        created_at: '2026-06-13T11:14:00.500Z'
      }
    })!

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:streaming-run-agent-a'])
    expect(view.timeline[1].message?.content).toBe('先解释一下计划。')
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mcp_job_update'])
  })

  it('keeps multiple streamed tool calls in one run item in arrival order', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '招聘',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-13T11:14:00Z',
      updated_at: '2026-06-13T11:14:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '创建北京东城保洁岗位',
          created_at: '2026-06-13T11:14:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '智员a',
          role: 'Recruiter'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'managed',
        created_at: '2026-06-13T11:14:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'z-first',
        name: 'mcp_company_list',
        status: 'completed',
        created_at: '2026-06-13T11:14:03Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'a-second',
        name: 'mcp_job_update',
        status: 'completed',
        created_at: '2026-06-13T11:14:02Z'
      }
    })!

    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user'])
    expect(view.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mcp_company_list', 'mcp_job_update'])
  })

  it('keeps later streamed tool calls after the text that preceded them', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '写代码的cc',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-18T07:10:00Z',
      updated_at: '2026-06-18T07:10:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '参考 Symphony SPEC 改造系统体系',
          created_at: '2026-06-18T07:10:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '写代码的cc',
          role: 'Developer'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-18T07:10:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-agent-a',
      delta: '先获取 SPEC。'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'web-reader-1',
        name: 'webReader',
        status: 'running',
        arguments: {
          url: 'https://github.com/openai/symphony/blob/main/SPEC.md'
        },
        created_at: '2026-06-18T07:10:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_result',
      tool_call: {
        tool_call_id: 'web-reader-1',
        name: '',
        status: 'completed',
        result: {
          output: 'webReader_result_summary: [{"text":{"title":"SPEC"}}]'
        },
        created_at: '2026-06-18T07:10:03Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-agent-a',
      delta: '再检查本地架构。'
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'glob-1',
        name: 'Glob',
        status: 'running',
        arguments: { pattern: '**/*.go' },
        created_at: '2026-06-18T07:10:04Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'token_delta',
      run_id: 'run-agent-a',
      delta: '最后整理方案。'
    })!

    expect(view.messages.map((message) => message.content)).toEqual(['参考 Symphony SPEC 改造系统体系', '先获取 SPEC。再检查本地架构。最后整理方案。'])
    expect(view.messages.some((message) => message.content.includes('webReader_result_summary'))).toBe(false)
    expect(view.timeline.map((item) => `${item.kind}:${item.id}`)).toEqual(['message:msg-user', 'message:streaming-run-agent-a'])
    expect(view.runs[0].toolCalls.map((tool) => tool.id)).toEqual(['web-reader-1', 'glob-1'])
    expect(view.runs[0].toolCalls[0].name).toBe('webReader')
  })

  it('routes streamed tool events to the run id carried by the event', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: '多代理',
      status: 'running',
      created_by: 'local-user',
      created_at: '2026-06-18T07:10:00Z',
      updated_at: '2026-06-18T07:10:00Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-1',
          role: 'user',
          author_id: 'local-user',
          content: '并行处理',
          created_at: '2026-06-18T07:10:00Z'
        }
      ],
      members: [
        {
          kind: 'agent',
          id: 'agent-a',
          display_name: '智员a',
          role: 'Developer'
        },
        {
          kind: 'agent',
          id: 'agent-b',
          display_name: '智员b',
          role: 'Reviewer'
        },
        {
          kind: 'user',
          id: 'local-user',
          display_name: '本地用户',
          role: '发起人'
        }
      ],
      runs: [],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-a',
        discussion_id: 'conv-1',
        agent_id: 'agent-a',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-18T07:10:01Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-agent-b',
        discussion_id: 'conv-1',
        agent_id: 'agent-b',
        status: 'running',
        runtime_kind: 'claude',
        created_at: '2026-06-18T07:10:02Z'
      }
    })!
    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      run_id: 'run-agent-a',
      tool_call: {
        tool_call_id: 'web-reader-1',
        name: 'webReader',
        status: 'running',
        created_at: '2026-06-18T07:10:03Z'
      }
    })!

    expect(view.runs.find((run) => run.id === 'run-agent-a')?.toolCalls.map((tool) => tool.id)).toEqual(['web-reader-1'])
    expect(view.runs.find((run) => run.id === 'run-agent-b')?.toolCalls).toEqual([])
  })

  it('applies stream errors as visible runtime errors', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [],
      linked_missions: []
    })

    const failed = applyDiscussionStreamEvent(view, {
      kind: 'error',
      error: 'discussion orchestration incomplete: agent created a task but stopped'
    })

    expect(failed?.runtimeStatus).toBe('failed')
    expect(failed?.runtimeError).toBe('discussion orchestration incomplete: agent created a task but stopped')
    expect(failed?.runs.at(-1)?.error).toBeUndefined()
  })

  it('attaches stream errors to the latest run when a run is active', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      linked_missions: []
    })

    const failed = applyDiscussionStreamEvent(view, {
      kind: 'error',
      error: 'discussion orchestration incomplete: agent stopped before task completion'
    })

    expect(failed?.runs[0]).toMatchObject({
      id: 'run-1',
      status: 'failed',
      error: 'discussion orchestration incomplete: agent stopped before task completion'
    })
    expect(failed?.timeline.some((item) => item.kind === 'run' && item.id === 'run-1')).toBe(false)
  })

  it('keeps tool calls on the failed run without rendering a work card', () => {
    let view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      linked_missions: []
    })

    view = applyDiscussionStreamEvent(view, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'tool-1',
        name: 'mission_prepare',
        status: 'completed',
        created_at: '2026-06-02T00:00:01Z'
      }
    })!
    const failed = applyDiscussionStreamEvent(view, {
      kind: 'error',
      error: 'mission_task_create_root returned no result payload'
    })

    expect(failed?.timeline.some((item) => item.kind === 'run' && item.id === 'run-1')).toBe(false)
    expect(failed?.runs[0].status).toBe('failed')
    expect(failed?.runs[0].toolCalls.map((tool) => tool.name)).toEqual(['mission_prepare'])
    expect(failed?.runs[0].error).toBe('mission_task_create_root returned no result payload')
  })

  it('removes transient streaming assistant text when a stream fails', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
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
        }
      ],
      runs: [
        {
          run_id: 'run-1',
          discussion_id: 'conv-1',
          agent_id: 'agent-alpha',
          status: 'running',
          runtime_kind: 'managed',
          created_at: '2026-06-02T00:00:00Z'
        }
      ],
      linked_missions: []
    })

    const streaming = applyDiscussionStreamEvent(view, {
      kind: 'token_delta'
    })
    expect(streaming?.messages.some((message) => message.id.startsWith('streaming-'))).toBe(false)

    const failed = applyDiscussionStreamEvent(streaming, {
      kind: 'error',
      error: 'invalid mission control request: assignee_id must reference an enabled agent instance'
    })

    expect(failed?.runtimeStatus).toBe('failed')
    expect(failed?.runtimeError).toContain('assignee_id must reference')
    expect(failed?.messages.some((message) => message.id.startsWith('streaming-'))).toBe(false)
    expect(failed?.timeline.some((item) => item.id.startsWith('streaming-'))).toBe(false)
  })

  it('keeps streamed mission brief update events after the first run already created a mission', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:02:00Z',
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

    const afterRun = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-2',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-06-02T00:02:00Z'
      }
    })
    const afterDuplicateBriefCall = applyDiscussionStreamEvent(afterRun, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'brief-2',
        name: 'mission_brief_create',
        status: 'running'
      }
    })
    expect(afterDuplicateBriefCall?.runs.at(-1)?.toolCalls.map((tool) => tool.name)).toEqual(['mission_brief_create'])

    const afterBriefUpdate = applyDiscussionStreamEvent(afterDuplicateBriefCall, {
      kind: 'tool_result',
      tool_call: {
        tool_call_id: 'brief-2',
        name: 'mission_brief_create',
        status: 'completed',
        result: { operation: 'updated', mission_id: 'mission-1' }
      }
    })
    const afterRootTask = applyDiscussionStreamEvent(afterBriefUpdate, {
      kind: 'tool_call',
      tool_call: {
        tool_call_id: 'task-2',
        name: 'mission_task_create_root',
        status: 'completed'
      }
    })

    expect(afterRootTask).toBeDefined()
    if (!afterRootTask) throw new Error('expected discussion stream events to update the view')
    expect(afterRootTask.runs.at(-1)?.toolCalls.map((tool) => tool.name)).toEqual(['mission_brief_create', 'mission_task_create_root'])
    expect(afterRootTask.runs.at(-1)?.toolCalls[0].result?.operation).toBe('updated')
  })

  it('makes a mission visible as soon as its runtime tool result arrives', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Create mission',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-06-02T00:00:00Z',
      updated_at: '2026-06-02T00:00:00Z',
      unread_completed: false,
      messages: [],
      runs: [{
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-06-02T00:00:00Z'
      }],
      linked_missions: []
    })

    const next = applyDiscussionStreamEvent(view, {
      kind: 'tool_result',
      tool_call: {
        tool_call_id: 'mission-prepare-1',
        name: 'mission_prepare',
        status: 'completed',
        result: {
          mission_id: 'mission-1',
          linked_missions_json: '[{"mission_id":"mission-1","title":"Snake game","status":"active"}]'
        }
      }
    })

    expect(next?.linkedMissions).toEqual([{
      id: 'mission-1',
      title: 'Snake game',
      status: 'active',
      href: '/missions/mission-1'
    }])
    expect(next?.runs[0].toolCalls).toHaveLength(1)
  })

  it('renders structured plan stream events as live run progress and replaces them on completion', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Plan runtime work',
      status: 'running',
      created_by: 'user-1',
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
        }
      ],
      runs: [],
      linked_missions: []
    })

    const afterRun = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'agent-alpha',
        status: 'running',
        runtime_kind: 'pi',
        created_at: '2026-06-02T00:00:00Z'
      }
    })
    const afterPlan = applyDiscussionStreamEvent(afterRun, {
      kind: 'plan',
      run_id: 'run-1',
      plan: {
        plan_id: 'plan-1',
        status: 'active',
        items: [
          { id: 'step-1', text: 'Inspect runtime bridge', status: 'completed' },
          { id: 'step-2', text: 'Wire plan event', status: 'in_progress' }
        ]
      }
    })

    expect(afterPlan?.runs.at(-1)?.planSteps?.map((step) => step.status)).toEqual(['completed', 'in_progress'])
    expect(afterPlan?.timeline.filter((item) => item.id === 'run-1')).toHaveLength(1)
    expect(afterPlan?.timeline.find((item) => item.id === 'run-1')?.run?.planStatus).toBe('active')

    const afterMessage = applyDiscussionStreamEvent(afterPlan, {
      kind: 'message_completed',
      message: {
        message_id: 'msg-agent',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-alpha',
        author_name: '顾明澈',
        content: 'Completed 2 plan step(s).',
        metadata: {
          display_timeline_json: JSON.stringify([
            {
              kind: 'plan',
              run_id: 'run-1',
              plan: {
                plan_id: 'plan-1',
                status: 'completed',
                items: [
                  { id: 'step-1', text: 'Inspect runtime bridge', status: 'completed' },
                  { id: 'step-2', text: 'Wire plan event', status: 'completed' }
                ]
              }
            }
          ])
        },
        created_at: '2026-06-02T00:00:03Z'
      }
    })

    const planItems = afterMessage?.timeline.filter((item) => item.id === 'run-1') ?? []
    expect(planItems).toHaveLength(1)
    expect(planItems[0].displaySegment).toBeUndefined()
    expect(planItems[0].run?.planStatus).toBe('completed')
    expect(planItems[0].run?.planSteps?.map((step) => step.status)).toEqual(['completed', 'completed'])
  })
})
