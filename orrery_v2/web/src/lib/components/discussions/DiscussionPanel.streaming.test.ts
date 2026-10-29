import { render } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it } from 'vitest'
import DiscussionPanel from './DiscussionPanel.svelte'
import { discussion } from './DiscussionPanel.fixtures'
import { setLocale } from '#lib/i18n'
import { mapDiscussionView } from '#lib/mappers/discussions'
import { clearSession } from '#lib/stores/session.svelte'

describe('DiscussionPanel streaming output', () => {
  beforeEach(() => {
    setLocale('en-US')
    clearSession()
  })

  it('renders streaming output as escaped plain text until completion', () => {
    const streaming = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          messages: [{
            id: 'streaming-run-1',
            role: 'assistant',
            authorId: 'agent-alpha',
            authorName: 'Lead Agent',
            content: '**draft**\n<img src=x onerror=alert(1)>',
            createdAt: 'Jun 02, 09:00'
          }]
        })
      }
    })
    const streamingContent = streaming.container.querySelector('.discussion-message-content')
    expect(streamingContent?.textContent).toContain('**draft**')
    expect(streamingContent?.querySelector('strong')).toBeNull()
    expect(streamingContent?.querySelector('img')).toBeNull()
    expect(streamingContent?.getAttribute('aria-busy')).toBe('true')
    expect(streamingContent?.querySelector('[data-streaming-cursor]')).not.toBeNull()
    streaming.unmount()

    const completed = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          messages: [{
            id: 'msg-final',
            role: 'assistant',
            authorId: 'agent-alpha',
            authorName: 'Lead Agent',
            content: '**final**',
            createdAt: 'Jun 02, 09:00'
          }]
        })
      }
    })
    expect(completed.container.querySelector('.discussion-message-content strong')?.textContent).toBe('final')
    expect(completed.container.querySelector('[data-streaming-cursor]')).toBeNull()
  })

  it('keeps the timeline at the bottom when the latest message updates', async () => {
    const message = {
      id: 'streaming-run-1',
      role: 'assistant' as const,
      authorId: 'agent-alpha',
      authorName: 'Lead Agent',
      content: 'Draft response',
      createdAt: 'Jun 02, 09:00'
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ messages: [message] })
      }
    })
    const timelineScroller = view.container.querySelector('[data-discussion-timeline-scroller]') as HTMLDivElement

    Object.defineProperty(timelineScroller, 'scrollHeight', { configurable: true, value: 1200 })
    timelineScroller.scrollTop = 0

    await view.rerender({
      draft: '',
      discussion: discussion({
        messages: [{ ...message, content: 'Draft response with the latest streamed content' }]
      })
    })
    await tick()

    expect(timelineScroller.scrollTop).toBe(1200)
  })

  it('renders Claude task progress as one persistent work card', async () => {
    const run = {
      id: 'run-claude',
      hasPlan: true,
      agentId: 'agent-alpha',
      agentName: 'Claude Agent',
      status: 'running',
      runtimeKind: 'claude',
      toolCalls: [{
        id: 'tool-write-1',
        name: 'Write',
        status: 'running',
        arguments: { file_path: 'game.html', content: '<html>game</html>' },
        createdAt: 'Jun 02, 09:01'
      }],
      planId: 'claude-tasks',
      planStatus: 'active',
      planSteps: [
        { id: '1', text: 'Inspect parser', status: 'completed' },
        { id: '2', text: 'Fix rendering', status: 'in_progress' }
      ],
      preview: 'Applying the rendering fix.',
      createdAt: 'Jun 02, 09:00',
      sortAt: '2026-06-02T01:00:00Z'
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          messages: [],
          runs: [run],
          timeline: [{ id: 'run-claude:plan:claude-tasks', kind: 'run', sortAt: run.sortAt, run }]
        })
      }
    })

    const card = view.container.querySelector('[data-testid="discussion-agent-work-card-run-claude"]')
    expect(card).not.toBeNull()
    expect(card?.textContent).toContain('Work in progress')
    expect(view.container.querySelector('.discussion-message-content')).toBeNull()

    const expand = card?.querySelector('button') as HTMLButtonElement
    expand.click()
    await tick()
    expect(card?.textContent).toContain('1/2')
    expect(card?.textContent).toContain('50%')
    expect(view.container.querySelector('[data-testid="discussion-plan-step-1"]')?.textContent).toContain('Completed')
    expect(view.container.querySelector('[data-testid="discussion-plan-step-2"]')?.textContent).toContain('In progress')
    const tools = view.container.querySelector('[data-testid="discussion-work-tools-run-claude"]')
    expect(tools?.textContent).toContain('Write')
    ;(tools?.querySelector('button[aria-label="Write running"]') as HTMLButtonElement).click()
    await tick()
    expect(tools?.textContent).toContain('game.html')

  })

  it('shows progress while Claude is still generating a large tool input', async () => {
    const run = {
      id: 'run-claude-progress',
      hasPlan: true,
      agentId: 'agent-alpha',
      agentName: 'Claude Agent',
      status: 'running',
      runtimeKind: 'claude',
      toolCalls: [{
        id: 'tool-write-progress',
        name: 'Write',
        status: 'running',
        arguments: {
          __orrery_streaming_input: 'true',
          __orrery_input_characters: '8192',
          __orrery_input_preview: '<canvas id="game">latest generated chunk'
        },
        createdAt: 'Jun 02, 09:01'
      }],
      planId: 'claude-tasks',
      planStatus: 'active',
      planSteps: [{ id: '1', text: 'Build the game', status: 'in_progress' }],
      createdAt: 'Jun 02, 09:00',
      sortAt: '2026-06-02T01:00:00Z'
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          messages: [],
          runs: [run],
          timeline: [{ id: 'run-claude-progress:plan:claude-tasks', kind: 'run', sortAt: run.sortAt, run }]
        })
      }
    })

    const card = view.container.querySelector('[data-testid="discussion-agent-work-card-run-claude-progress"]') as HTMLElement
    ;(card.querySelector('button') as HTMLButtonElement).click()
    await tick()
    ;(card.querySelector('button[aria-label="Write running"]') as HTMLButtonElement).click()
    await tick()

    const progress = view.container.querySelector('[data-testid="discussion-tool-input-progress-tool-write-progress"]')
    expect(progress?.textContent).toContain('Generating tool input')
    expect(progress?.textContent).toContain('8192 characters received')
    expect(progress?.textContent).toContain('latest generated chunk')
  })

  it('keeps the same direct-run work card when thinking turns into tool execution', async () => {
    const thinkingRun = {
      id: 'run-transition',
      mode: 'direct',
      agentId: 'agent-alpha',
      agentName: 'Claude Agent',
      status: 'running',
      runtimeKind: 'claude',
      toolCalls: [],
      thinking: 'Inspecting the workspace.',
      createdAt: 'Jun 02, 09:00',
      sortAt: '2026-06-02T01:00:00Z'
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          kind: 'direct',
          messages: [],
          runs: [thinkingRun],
          timeline: [{ id: thinkingRun.id, kind: 'run', sortAt: thinkingRun.sortAt, run: thinkingRun }]
        })
      }
    })

    expect(view.container.querySelectorAll('[data-testid="discussion-agent-work-card-run-transition"]')).toHaveLength(1)

    const toolRun = {
      ...thinkingRun,
      thinking: undefined,
      toolCalls: [{
        id: 'read-1',
        name: 'Read',
        status: 'running',
        arguments: { file_path: 'index.html' },
        createdAt: 'Jun 02, 09:01',
        sortAt: '2026-06-02T01:00:00.001Z'
      }]
    }
    await view.rerender({
      draft: '',
      discussion: discussion({
        kind: 'direct',
        messages: [],
        runs: [toolRun],
        timeline: [{ id: toolRun.id, kind: 'run', sortAt: toolRun.sortAt, run: toolRun }]
      })
    })
    await tick()

    const card = view.container.querySelector('[data-testid="discussion-agent-work-card-run-transition"]') as HTMLElement
    expect(view.container.querySelectorAll('[data-testid="discussion-agent-work-card-run-transition"]')).toHaveLength(1)
    expect(view.container.querySelector('[data-tool-agent-name]')).toBeNull()
    expect(card.querySelector('button')?.getAttribute('aria-expanded')).toBe('true')
    expect(card.querySelector('[data-testid="discussion-work-tools-run-transition"]')?.textContent).toContain('Read')

    const cancelledRun = {
      ...toolRun,
      status: 'cancelled',
      toolCalls: toolRun.toolCalls.map((tool) => ({ ...tool, status: 'cancelled' }))
    }
    await view.rerender({
      draft: '',
      discussion: discussion({
        kind: 'direct',
        status: 'cancelled',
        runtimeStatus: 'cancelled',
        messages: [],
        runs: [cancelledRun],
        timeline: [{ id: cancelledRun.id, kind: 'run', sortAt: cancelledRun.sortAt, run: cancelledRun }]
      })
    })
    await tick()

    expect(view.container.querySelectorAll('[data-testid="discussion-agent-work-card-run-transition"]')).toHaveLength(1)
    expect(view.container.querySelector('[data-testid="discussion-agent-work-card-run-transition"]')?.textContent).toContain('cancelled')
    expect(view.container.querySelector('[data-testid="discussion-process-details-run-transition"]')).toBeNull()
  })

  it('renders personal-assistant work as a compact expandable process', async () => {
    const run = {
      id: 'run-assistant-process',
      mode: 'group',
      agentId: 'assistant-ding',
      agentName: 'Fafa',
      status: 'completed',
      runtimeKind: 'codex',
      toolCalls: [{
        id: 'read-profile',
        name: 'Read profile',
        status: 'completed',
        createdAt: 'Aug 12, 11:35 AM',
        sortAt: '2026-08-12T03:35:01Z'
      }],
      thinking: 'Checked the request and relevant profile.',
      durationMs: 1250,
      createdAt: 'Aug 12, 11:35 AM',
      sortAt: '2026-08-12T03:35:00Z'
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          kind: 'personal_assistant',
          messages: [],
          runs: [run],
          timeline: [{ id: run.id, kind: 'run', sortAt: run.sortAt, run }]
        })
      }
    })

    const process = view.container.querySelector('[data-testid="discussion-agent-work-card-run-assistant-process"]') as HTMLElement
    expect(process.hasAttribute('data-discussion-process')).toBe(true)
    expect(process.querySelector('[data-discussion-process-summary]')?.textContent).toContain('Checked the request')
    expect(process.textContent).toContain('1.3s')
    expect(view.container.querySelector('[data-testid="discussion-process-details-run-assistant-process"]')).toBeNull()

    ;(process.querySelector('button') as HTMLButtonElement).click()
    await tick()

    expect(view.container.querySelector('[data-testid="discussion-thinking-run-assistant-process"]')?.textContent).toContain('relevant profile')
    expect(view.container.querySelector('[data-testid="discussion-work-tools-run-assistant-process"]')?.textContent).toContain('Read profile')
  })

  it('shows persisted token usage in the compact run process', () => {
    const run = {
      id: 'run-usage',
      mode: 'direct',
      agentId: 'agent-alpha',
      agentName: 'Codex Agent',
      status: 'completed',
      runtimeKind: 'codex',
      toolCalls: [],
      thinking: 'Checked the request.',
      usage: {
        'gpt-5-codex': { input_tokens: 1200, output_tokens: 345, total_tokens: 1545 }
      },
      createdAt: 'Aug 12, 11:35 AM',
      sortAt: '2026-08-12T03:35:00Z'
    }

    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          kind: 'direct',
          messages: [],
          runs: [run],
          timeline: [{ id: run.id, kind: 'run', sortAt: run.sortAt, run }]
        })
      }
    })

    expect(view.container.querySelector('[data-testid="discussion-agent-work-card-run-usage"]')?.textContent).toContain('1,545 tokens')
  })

  it.each([
    { details: { cached_input_tokens: 800, reasoning_tokens: 40, cached_input_tokens_known: true, reasoning_tokens_known: true }, cached: 'Cached input: 800', reasoning: 'Reasoning in output: 40' },
    { details: { cached_input_tokens_known: true, reasoning_tokens_known: true }, cached: 'Cached input: 0', reasoning: 'Reasoning in output: 0' },
    { details: {}, cached: 'Cached input: Not reported', reasoning: 'Reasoning in output: Not reported' },
    { details: { cached_input_tokens: 800, reasoning_tokens: 40 }, cached: 'Cached input: 800 (partially reported)', reasoning: 'Reasoning in output: 40 (partially reported)' }
  ])('shows usage coverage without changing totals: $cached', async ({ details, cached, reasoning }) => {
    const run = {
      id: 'run-cache-usage', mode: 'group', agentId: 'agent-alpha', agentName: 'Agent',
      status: 'completed', runtimeKind: 'managed', toolCalls: [], thinking: 'Checked the request.',
      usage: { model: { input_tokens: 1200, output_tokens: 345, total_tokens: 1545, ...details } },
      createdAt: 'Aug 12, 11:35 AM', sortAt: '2026-08-12T03:35:00Z'
    }
    const view = render(DiscussionPanel, { props: { draft: '', discussion: discussion({
      kind: 'group', messages: [], runs: [run],
      timeline: [{ id: run.id, kind: 'run', sortAt: run.sortAt, run }]
    }) } })
    const process = view.container.querySelector('[data-testid="discussion-agent-work-card-run-cache-usage"]') as HTMLElement
    expect(process.textContent).toContain('1,545 tokens')
    ;(process.querySelector('button') as HTMLButtonElement).click()
    await tick()
    expect(process.querySelector('[data-run-cached-input]')?.textContent).toBe(cached)
    expect(process.querySelector('[data-run-reasoning-tokens]')?.textContent).toBe(reasoning)
  })

  it('renders a group Codex run as a live reasoning summary while approval is pending', () => {
    const run = {
      id: 'run-codex-approval',
      mode: 'group',
      agentId: 'agent-alpha',
      agentName: 'Codex Agent',
      status: 'awaiting_approval',
      runtimeKind: 'codex',
      toolCalls: [],
      thinking: 'Checked the goal and found one protected Mission operation.',
      planSteps: [
        { id: 'step-check', text: 'Check Mission constraints', status: 'completed' },
        { id: 'step-plan', text: 'Create parallel tasks', status: 'pending' }
      ],
      createdAt: 'Aug 12, 11:35 AM',
      sortAt: '2026-08-12T03:35:00Z'
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          kind: 'group',
          messages: [],
          runs: [run],
          timeline: [{ id: run.id, kind: 'run', sortAt: run.sortAt, run }]
        })
      }
    })

    const process = view.container.querySelector('[data-testid="discussion-agent-work-card-run-codex-approval"]') as HTMLElement
    const reasoning = process.querySelector('[data-reasoning-summary]')
    expect(process.hasAttribute('data-discussion-process')).toBe(true)
    expect(process.querySelector('button')?.getAttribute('aria-expanded')).toBe('true')
    expect(reasoning?.getAttribute('aria-live')).toBe('polite')
    expect(reasoning?.getAttribute('aria-busy')).toBe('true')
    expect(reasoning?.textContent).toContain('Reasoning summary')
    expect(reasoning?.textContent).toContain('Awaiting approval')
    expect(reasoning?.textContent).toContain('protected Mission operation')
    expect(reasoning?.querySelector('[data-reasoning-cursor]')).toBeNull()
  })

  it('coalesces mixed runtime tools in execution order above the final answer', async () => {
    const mapped = mapDiscussionView({
      discussion_id: 'conv-mixed-tools',
      conversation_kind: 'direct',
      title: 'Claude Agent',
      status: 'completed',
      created_by: 'user-1',
      created_at: '2026-06-02T01:00:00Z',
      updated_at: '2026-06-02T01:00:10Z',
      unread_completed: false,
      messages: [
        {
          message_id: 'msg-user',
          discussion_id: 'conv-mixed-tools',
          role: 'user',
          author_id: 'user-1',
          content: 'Build the game',
          created_at: '2026-06-02T01:00:00Z'
        },
        {
          message_id: 'msg-agent',
          discussion_id: 'conv-mixed-tools',
          run_id: 'run-mixed',
          role: 'assistant',
          author_id: 'agent-alpha',
          author_name: 'Claude Agent',
          content: 'The game is ready.',
          metadata: {
            display_timeline_json: JSON.stringify([
              { kind: 'tool', run_id: 'run-mixed', tool_call_id: 'native-read' },
              { kind: 'tool', run_id: 'run-mixed', tool_call_id: 'system-update' },
              { kind: 'tool', run_id: 'run-mixed', tool_call_id: 'native-write' },
              { kind: 'text', text: 'The game is ready.' }
            ])
          },
          created_at: '2026-06-02T01:00:10Z'
        }
      ],
      runs: [{
        run_id: 'run-mixed',
        discussion_id: 'conv-mixed-tools',
        mode: 'direct',
        agent_id: 'agent-alpha',
        agent_name: 'Claude Agent',
        status: 'completed',
        runtime_kind: 'claude',
        thinking_summary: 'Implementing the game.',
        created_at: '2026-06-02T01:00:01Z',
        completed_at: '2026-06-02T01:00:10Z',
        tool_calls: [
          { tool_call_id: 'system-update', name: 'discussion_status_update', status: 'completed', created_at: '2026-06-02T01:00:07Z' },
          { tool_call_id: 'native-write', name: 'Write', status: 'completed', created_at: '2026-06-02T01:00:03Z' },
          { tool_call_id: 'native-read', name: 'Read', status: 'completed', created_at: '2026-06-02T01:00:09Z' }
        ]
      }],
      events: [{
        event_id: 'approval-native-read',
        discussion_id: 'conv-mixed-tools',
        kind: 'discussion_runtime_approval_requested',
        status: 'approved',
        summary: 'Runtime approval required for Read',
        metadata: {
          approval_id: 'approval-native-read',
          approval_request_id: 'request-native-read',
          approval_run_id: 'run-mixed',
          approval_tool_use_id: 'native-read',
          approval_type: 'file_change',
          approval_tool_name: 'Read'
        },
        created_at: '2026-06-02T01:00:02Z'
      }],
      linked_missions: []
    })
    const view = render(DiscussionPanel, { props: { draft: '', discussion: mapped } })

    expect([...view.container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual([
      'msg-user',
      'run-mixed',
      'msg-agent'
    ])
    expect(view.container.querySelectorAll('[data-testid="discussion-agent-work-card-run-mixed"]')).toHaveLength(1)
    expect(view.container.querySelector('[data-testid="discussion-approval-card-approval-native-read"]')).toBeNull()

    const card = view.container.querySelector('[data-testid="discussion-agent-work-card-run-mixed"]') as HTMLElement
    ;(card.querySelector('button') as HTMLButtonElement).click()
    await tick()
    const toolButtons = [...card.querySelectorAll('[data-tool-call-drawer-content] button[aria-label]')]
    expect(toolButtons.map((button) => button.getAttribute('aria-label'))).toEqual([
      'Read completed',
      'discussion_status_update completed',
      'Write completed'
    ])
  })
})
