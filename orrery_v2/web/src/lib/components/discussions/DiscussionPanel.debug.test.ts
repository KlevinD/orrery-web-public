import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DiscussionPanel from './DiscussionPanel.svelte'
import { setLocale } from '#lib/i18n'
import { debugPrompt, discussion } from './DiscussionPanel.fixtures'

describe('DiscussionPanel debug drawer', () => {
  beforeEach(() => {
    setLocale('en-US')
  })

  it('opens and renders the agent work process drawer with run debug details', async () => {
    const onDebugOpen = vi.fn()
    const onDebugClose = vi.fn()
    const onDebugRunSelect = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'failed',
          runtimeError: 'discussion orchestration incomplete: agent stopped before task completion',
          messages: [
            {
              id: 'msg-user',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: 'Plan launch',
              createdAt: 'Jun 02, 08:59'
            },
            {
              id: 'msg-assistant',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: 'I created the Mission and then stopped.',
              createdAt: 'Jun 02, 09:02'
            }
          ],
          runs: [
            {
              id: 'run-0',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'managed',
              createdAt: 'Jun 02, 08:58',
              toolCalls: []
            },
            {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'failed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:01',
              error: 'discussion orchestration incomplete: agent stopped before task completion',
              toolCalls: [
                { id: 'tool-1', name: 'mission_brief_create', status: 'completed', createdAt: 'Jun 02, 09:01' }
              ]
            }
          ],
          timeline: [
            {
              id: 'msg-user',
              kind: 'message',
              sortAt: '2026-06-02T08:59:00Z',
              message: {
                id: 'msg-user',
                role: 'user',
                authorId: 'operator',
                authorName: 'Operator',
                content: 'Plan launch',
                createdAt: 'Jun 02, 08:59'
              }
            },
            {
              id: 'run-1',
              kind: 'run',
              sortAt: '2026-06-02T09:01:00Z',
              run: {
                id: 'run-1',
                agentId: 'agent-alpha',
                agentName: '顾明澈',
                status: 'failed',
                runtimeKind: 'codex',
                createdAt: 'Jun 02, 09:01',
                error: 'discussion orchestration incomplete: agent stopped before task completion',
                toolCalls: [
                  { id: 'tool-1', name: 'mission_brief_create', status: 'completed', createdAt: 'Jun 02, 09:01' }
                ]
              }
            },
            {
              id: 'msg-assistant',
              kind: 'message',
              sortAt: '2026-06-02T09:02:00Z',
              message: {
                id: 'msg-assistant',
                role: 'assistant',
                authorId: 'agent-alpha',
                authorName: 'Lead Agent',
                content: 'I created the Mission and then stopped.',
                createdAt: 'Jun 02, 09:02'
              }
            }
          ]
        }),
        debugOpen: true,
        debug: debugPrompt(),
        debugRuns: [
          {
            id: 'run-0',
            agentId: 'agent-alpha',
            agentName: '顾明澈',
            status: 'completed',
            runtimeKind: 'managed',
            createdAt: 'Jun 02, 08:58',
            toolCalls: []
          },
          {
            id: 'run-1',
            agentId: 'agent-alpha',
            agentName: '顾明澈',
            status: 'failed',
            runtimeKind: 'codex',
            createdAt: 'Jun 02, 09:01',
            error: 'discussion orchestration incomplete: agent stopped before task completion',
            toolCalls: []
          }
        ],
        selectedDebugRunId: 'run-1',
        onDebugOpen,
        onDebugRunSelect,
        onDebugClose
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Debug prompt' }))

    expect(onDebugOpen).toHaveBeenCalledTimes(1)
    expect(onDebugOpen).toHaveBeenCalledWith('')
    expect(screen.getByRole('complementary', { name: 'Agent work process' })).toBeTruthy()
    expect(screen.getByText('Agent work process')).toBeTruthy()
    expect(screen.getByText('Run timeline')).toBeTruthy()
    expect(screen.getByTestId('discussion-debug-run-run-1').getAttribute('data-run-state')).toBe('failed')
    expect(screen.getAllByText('mission_brief_create').length).toBeGreaterThan(0)
    expect(screen.getByText('Tool calls')).toBeTruthy()
    expect(screen.getByText('Sent prompt')).toBeTruthy()
    expect(screen.getByText('Response')).toBeTruthy()
    expect(screen.getByTestId('discussion-debug-outbound-prompt').textContent).toContain('system token=secret-system')
    expect(screen.getByTestId('discussion-debug-outbound-prompt').textContent).toContain('Plan launch')
    expect(screen.getByTestId('discussion-debug-runtime-response').textContent).toContain('I created the Mission and then stopped.')
    expect(screen.getByTestId('discussion-debug-runtime-response').textContent).toContain('tool_use mission_brief_create')
    expect(screen.getByText('Provider metadata')).toBeTruthy()
    expect(screen.getByTestId('discussion-debug-provider-metadata').textContent).toContain('pi_debug_runtime_input_path')
    expect(screen.getByTestId('discussion-debug-provider-metadata').textContent).toContain('runtime-input.json')
    expect(screen.getByText('Possible error')).toBeTruthy()
    expect(screen.getAllByText('discussion orchestration incomplete: agent stopped before task completion').length).toBeGreaterThan(0)
    expect(screen.getByText('agent-alpha / codex / gpt-5-codex')).toBeTruthy()
    expect(screen.queryByText('Redacted')).toBeNull()
    expect(screen.queryByText('System prompt')).toBeNull()
    expect(screen.queryByText('Runtime prompt')).toBeNull()
    expect(screen.queryByText('Tool contract')).toBeNull()
    expect(screen.queryByText('History')).toBeNull()

    await fireEvent.click(screen.getByTestId('discussion-debug-run-run-0'))

    expect(onDebugRunSelect).toHaveBeenCalledWith('run-0')
  })

  it('opens the debug prompt for the run associated with messages and agent work cards', async () => {
    const onDebugOpen = vi.fn()
    const runs = [
      {
        id: 'run-1',
        agentId: 'agent-alpha',
        agentName: 'Lead Agent',
        status: 'completed',
        runtimeKind: 'pi',
        createdAt: '2026-06-02T09:01:00Z',
        sortAt: '2026-06-02T09:01:00Z',
        toolCalls: []
      },
      {
        id: 'run-2',
        agentId: 'agent-alpha',
        agentName: 'Lead Agent',
        status: 'completed',
        runtimeKind: 'pi',
        createdAt: '2026-06-02T09:03:00Z',
        sortAt: '2026-06-02T09:03:00Z',
        toolCalls: [],
        planStatus: 'completed',
        planSteps: [
          { id: 'run-2-step-1', text: 'Create follow-up task', status: 'completed' }
        ]
      }
    ]
    const userMessage = {
      id: 'msg-user',
      role: 'user',
      authorId: 'operator',
      authorName: 'Operator',
      content: 'Plan launch',
      createdAt: 'Jun 02, 09:00',
      sortAt: '2026-06-02T09:00:00Z'
    }
    const assistantMessage = {
      id: 'msg-assistant',
      role: 'assistant',
      authorId: 'agent-alpha',
      authorName: 'Lead Agent',
      content: 'Done.',
      createdAt: 'Jun 02, 09:04',
      sortAt: '2026-06-02T09:04:00Z'
    }
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [userMessage, assistantMessage],
          runs,
          timeline: [
            { id: 'msg-user', kind: 'message', sortAt: userMessage.sortAt, message: userMessage },
            { id: 'run-2', kind: 'run', sortAt: '2026-06-02T09:03:00Z', run: runs[1] },
            { id: 'msg-assistant', kind: 'message', sortAt: assistantMessage.sortAt, message: assistantMessage }
          ]
        }),
        debugRuns: runs,
        onDebugOpen
      }
    })

    await fireEvent.click(screen.getByTestId('discussion-message-debug-msg-user'))
    await fireEvent.click(screen.getByRole('button', { name: 'View work process' }))

    expect(onDebugOpen).toHaveBeenNthCalledWith(1, 'run-1')
    expect(onDebugOpen).toHaveBeenNthCalledWith(2, 'run-2')
  })

  it('disables debug prompt before the discussion has a run', async () => {
    const onDebugOpen = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runs: [],
          runtimeStatus: 'completed'
        }),
        onDebugOpen
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))
    const button = screen.getByRole('button', { name: 'Debug prompt' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)

    await fireEvent.click(button)

    expect(onDebugOpen).not.toHaveBeenCalled()
  })

  it('keeps long debug prompts and responses in independent scroll regions', () => {
    const longLine = 'long-token-without-natural-breaks-'.repeat(80)
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [
            {
              id: 'msg-assistant',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: `${longLine}\n${longLine}`,
              createdAt: 'Jun 02, 09:02'
            }
          ],
          runs: [
            {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:01',
              toolCalls: []
            }
          ],
          timeline: [
            {
              id: 'run-1',
              kind: 'run',
              sortAt: '2026-06-02T09:01:00Z',
              run: {
                id: 'run-1',
                agentId: 'agent-alpha',
                agentName: '顾明澈',
                status: 'completed',
                runtimeKind: 'codex',
                createdAt: 'Jun 02, 09:01',
                toolCalls: []
              }
            },
            {
              id: 'msg-assistant',
              kind: 'message',
              sortAt: '2026-06-02T09:02:00Z',
              message: {
                id: 'msg-assistant',
                role: 'assistant',
                authorId: 'agent-alpha',
                authorName: 'Lead Agent',
                content: `${longLine}\n${longLine}`,
                createdAt: 'Jun 02, 09:02'
              }
            }
          ]
        }),
        debugOpen: true,
        debug: {
          ...debugPrompt(),
          outboundPrompt: `${longLine}\n${longLine}`,
          runtimeResponse: `${longLine}\n${longLine}`
        },
        debugRuns: [
          {
            id: 'run-1',
            agentId: 'agent-alpha',
            agentName: '顾明澈',
            status: 'completed',
            runtimeKind: 'codex',
            createdAt: 'Jun 02, 09:01',
            toolCalls: []
          }
        ],
        selectedDebugRunId: 'run-1'
      }
    })

    for (const testId of [
      'discussion-debug-outbound-prompt',
      'discussion-debug-runtime-response'
    ]) {
      const region = screen.getByTestId(testId)
      expect(region.className).toContain('overflow-auto')
      expect(region.className).toContain('break-words')
    }
  })

  it('shows merged work-card tool calls in the process drawer', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          timeline: [
            { id: 'tool-a', kind: 'run', sortAt: '2026-06-02T09:01:00Z', run: {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:01',
              toolCalls: [
                { id: 'tool-a', name: 'mission_prepare', status: 'completed', createdAt: 'Jun 02, 09:01' }
              ]
            } },
            { id: 'tool-b', kind: 'run', sortAt: '2026-06-02T09:02:00Z', run: {
              id: 'run-2',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:02',
              toolCalls: [
                { id: 'tool-b', name: 'mission_worker_request_approve', status: 'completed', createdAt: 'Jun 02, 09:02' }
              ]
            } }
          ]
        }),
        debugOpen: true,
        debug: { ...debugPrompt(), runId: 'tool-a:tool-group:tool-b' },
        selectedDebugRunId: 'tool-a:tool-group:tool-b'
      }
    })

    expect(screen.getByText('mission_prepare')).toBeTruthy()
    expect(screen.getByText('mission_worker_request_approve')).toBeTruthy()
  })
})
