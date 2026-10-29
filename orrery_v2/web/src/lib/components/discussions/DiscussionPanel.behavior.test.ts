import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DiscussionPanel from './DiscussionPanel.svelte'
import { setLocale } from '#lib/i18n'
import { clearSession } from '#lib/stores/session.svelte'
import { discussion } from './DiscussionPanel.fixtures'

describe('DiscussionPanel', () => {
  beforeEach(() => {
    setLocale('en-US')
    clearSession()
  })

  it('copies a completed message from its inline action', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          messages: [{
            id: 'msg-copy',
            role: 'assistant',
            authorId: 'agent-alpha',
            authorName: 'Lead Agent',
            content: '**Done.**',
            createdAt: 'Jun 02, 09:00'
          }]
        })
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Copy message' }))
    expect(writeText).toHaveBeenCalledWith('**Done.**')
    expect(screen.getByRole('button', { name: 'Message copied' })).toBeTruthy()
  })

  it('interleaves messages and agent run status in timeline order', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [
            {
              id: 'msg-1',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: 'Plan launch',
              createdAt: 'Jun 02, 09:00'
            },
            {
              id: 'msg-2',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: 'I created the Mission.',
              createdAt: 'Jun 02, 09:02'
            },
            {
              id: 'msg-3',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: 'Add the QA checklist',
              createdAt: 'Jun 02, 09:03'
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
              toolCalls: [
                { id: 'tool-1', name: 'mission_brief_create', status: 'completed', createdAt: 'Jun 02, 09:01' }
              ]
            },
            {
              id: 'run-2',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:04',
              toolCalls: [
                { id: 'tool-2', name: 'mission_task_create_root', status: 'completed', createdAt: 'Jun 02, 09:04' }
              ]
            }
          ],
          timeline: [
            { id: 'msg-1', kind: 'message', sortAt: '2026-06-02T09:00:00Z', message: {
              id: 'msg-1',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: 'Plan launch',
              createdAt: 'Jun 02, 09:00'
            } },
            { id: 'run-1', kind: 'run', sortAt: '2026-06-02T09:01:00Z', run: {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:01',
              toolCalls: [
                { id: 'tool-1', name: 'mission_brief_create', status: 'completed', createdAt: 'Jun 02, 09:01' }
              ]
            } },
            { id: 'msg-2', kind: 'message', sortAt: '2026-06-02T09:02:00Z', message: {
              id: 'msg-2',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: 'I created the Mission.',
              createdAt: 'Jun 02, 09:02'
            } },
            { id: 'msg-3', kind: 'message', sortAt: '2026-06-02T09:03:00Z', message: {
              id: 'msg-3',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: 'Add the QA checklist',
              createdAt: 'Jun 02, 09:03'
            } },
            { id: 'run-2', kind: 'run', sortAt: '2026-06-02T09:04:00Z', run: {
              id: 'run-2',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:04',
              toolCalls: [
                { id: 'tool-2', name: 'mission_task_create_root', status: 'completed', createdAt: 'Jun 02, 09:04' }
              ]
            } }
          ]
        })
      }
    })

    expect([...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual([
      'msg-1',
      'msg-2',
      'msg-3',
      'run-2'
    ])
    expect(screen.queryByLabelText('Agent work card')).toBeNull()
    expect(screen.getByText('I created the Mission.')).toBeTruthy()
    expect(screen.getByText('mission_brief_create')).toBeTruthy()
    expect(screen.getByText('mission_task_create_root')).toBeTruthy()
  })

  it('renders supplied timeline items in occurrence order without timestamp resorting', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          timeline: [
            { id: 'msg-later', kind: 'message', sortAt: '2026-06-02T09:05:00Z', message: {
              id: 'msg-later',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: 'This happened first in the event stream.',
              createdAt: 'Jun 02, 09:05'
            } },
            { id: 'msg-earlier', kind: 'message', sortAt: '2026-06-02T09:01:00Z', message: {
              id: 'msg-earlier',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: 'This arrived second.',
              createdAt: 'Jun 02, 09:01'
            } }
          ]
        })
      }
    })

    expect([...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual([
      'msg-later',
      'msg-earlier'
    ])
  })

  it('renders two consecutive tool calls as dense inline rows', () => {
    const { container } = render(DiscussionPanel, {
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
        })
      }
    })

    expect(container.querySelector('[data-testid="discussion-tool-call-drawer"]')).toBeNull()
    expect(container.querySelectorAll('[data-testid="discussion-tool-call-list"]')).toHaveLength(1)
    expect(screen.queryByLabelText('Agent work card')).toBeNull()
    expect(screen.getByText('mission_prepare')).toBeTruthy()
    expect(screen.getByText('mission_worker_request_approve')).toBeTruthy()
    expect([...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual(['tool-a:tool-group'])
  })

  it('keeps consecutive tool calls available when the discussion is failed', async () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          timeline: [
            { id: 'tool-a', kind: 'run', sortAt: '2026-06-02T09:01:00Z', run: {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'failed',
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
              status: 'failed',
              runtimeKind: 'codex',
              createdAt: 'Jun 02, 09:02',
              toolCalls: [
                { id: 'tool-b', name: 'mission_task_create_root', status: 'failed', createdAt: 'Jun 02, 09:02' }
              ]
            } }
          ]
        })
      }
    })

    expect(container.querySelector('[data-testid="discussion-tool-call-drawer"]')).toBeNull()
    expect(container.querySelectorAll('[data-testid="discussion-tool-call-list"]')).toHaveLength(1)
    expect(screen.queryByLabelText('Agent work card')).toBeNull()
    expect(screen.getByText('mission_prepare')).toBeTruthy()
    expect(screen.getByText('mission_task_create_root')).toBeTruthy()
  })

  it('renders multiple tool calls inside one run without absorbing following messages', async () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          timeline: [
            { id: 'run-1', kind: 'run', sortAt: '2026-06-02T09:01:00Z', run: {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'managed',
              createdAt: 'Jun 02, 09:01',
              toolCalls: [
                { id: 'tool-a', name: 'list_skills', status: 'completed', createdAt: 'Jun 02, 09:01' },
                { id: 'tool-b', name: 'mission_prepare', status: 'completed', createdAt: 'Jun 02, 09:02' }
              ]
            } },
            { id: 'msg-after', kind: 'message', sortAt: '2026-06-02T09:03:00Z', message: {
              id: 'msg-after',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: '顾明澈',
              content: '好的，我先看看技能情况，然后创建 Mission Brief。',
              createdAt: 'Jun 02, 09:03'
            } }
          ]
        })
      }
    })

    expect(container.querySelector('[data-testid="discussion-tool-call-drawer"]')).toBeNull()
    expect(container.querySelectorAll('[data-testid="discussion-tool-call-list"]')).toHaveLength(1)
    expect(screen.queryByLabelText('Agent work card')).toBeNull()
    expect(screen.getByText('好的，我先看看技能情况，然后创建 Mission Brief。')).toBeTruthy()
    expect(screen.getByText('list_skills')).toBeTruthy()
    expect(screen.getByText('mission_prepare')).toBeTruthy()
    expect([...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual([
      'msg-after'
    ])
    const messageItem = container.querySelector('[data-discussion-timeline-id="msg-after"]') as HTMLElement
    const author = messageItem.querySelector('[data-message-author]') as HTMLElement
    const toolList = messageItem.querySelector('[data-message-tool-calls]') as HTMLElement
    const bubble = messageItem.querySelector('.discussion-message-bubble') as HTMLElement
    expect(author.textContent).toBe('顾明澈')
    expect(author.compareDocumentPosition(toolList) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(toolList.compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('auto-collapses historical tool drawers after a response and leaves a trailing tool drawer expanded', async () => {
    const toolRun = (id: string, name: string, minute: string) => ({
      id,
      kind: 'run' as const,
      sortAt: `2026-06-02T09:${minute}:00Z`,
      run: {
        id,
        agentId: 'agent-alpha',
        agentName: '顾明澈',
        status: 'completed',
        runtimeKind: 'codex',
        createdAt: `Jun 02, 09:${minute}`,
        toolCalls: [{ id: `call-${id}`, name, status: 'completed', createdAt: `Jun 02, 09:${minute}` }]
      }
    })
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          timeline: [
            toolRun('run-a', 'tool_a', '01'),
            toolRun('run-b', 'tool_b', '02'),
            toolRun('run-c', 'tool_c', '03'),
            { id: 'msg-break', kind: 'message', sortAt: '2026-06-02T09:04:00Z', message: {
              id: 'msg-break',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: '顾明澈',
              content: '工具调用告一段落。',
              createdAt: 'Jun 02, 09:04'
            } },
            toolRun('run-d', 'tool_d', '05'),
            toolRun('run-e', 'tool_e', '06'),
            toolRun('run-f', 'tool_f', '07')
          ]
        })
      }
    })

    const drawers = container.querySelectorAll('[data-testid="discussion-tool-call-drawer"]')
    expect(drawers).toHaveLength(2)
    expect(drawers[0].querySelector('button')?.getAttribute('aria-expanded')).toBe('false')
    expect(drawers[1].querySelector('button')?.getAttribute('aria-expanded')).toBe('true')
    expect(screen.queryByText('tool_a')).toBeNull()
    expect(screen.getByText('tool_d')).toBeTruthy()
    expect(screen.getByText('tool_f')).toBeTruthy()
    await fireEvent.click(within(drawers[0] as HTMLElement).getByRole('button', { name: 'Tool calls' }))
    expect(drawers[0].querySelector('[data-tool-call-drawer-content]')).toBeTruthy()
    expect(screen.getByText('tool_a')).toBeTruthy()
    expect(screen.getByText('工具调用告一段落。')).toBeTruthy()
    expect(container.querySelectorAll('[data-testid="discussion-tool-call-list"]')).toHaveLength(0)
    expect([...container.querySelectorAll('[data-discussion-timeline-item]')].map((item) => item.getAttribute('data-discussion-timeline-id'))).toEqual([
      'msg-break',
      'run-d:tool-group'
    ])
  })

  it('automatically closes the active tool drawer when the next non-tool response arrives', async () => {
    const toolItem = {
      id: 'run-tail',
      kind: 'run' as const,
      sortAt: '2026-06-02T09:01:00Z',
      run: {
        id: 'run-tail',
        agentId: 'agent-alpha',
        agentName: '顾明澈',
        status: 'running',
        runtimeKind: 'codex',
        createdAt: 'Jun 02, 09:01',
        toolCalls: [
          { id: 'call-a', name: 'tool_a', status: 'completed', createdAt: 'Jun 02, 09:01' },
          { id: 'call-b', name: 'tool_b', status: 'completed', createdAt: 'Jun 02, 09:02' },
          { id: 'call-c', name: 'tool_c', status: 'running', createdAt: 'Jun 02, 09:03' }
        ]
      }
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'running', timeline: [toolItem] })
      }
    })

    expect(screen.getByRole('button', { name: 'Tool calls' }).getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByText('tool_a')).toBeTruthy()

    await view.rerender({
      draft: '',
      discussion: discussion({
        runtimeStatus: 'completed',
        timeline: [
          { ...toolItem, run: { ...toolItem.run, status: 'completed', toolCalls: toolItem.run.toolCalls.map((tool) => ({ ...tool, status: 'completed' })) } },
          { id: 'msg-after-tools', kind: 'message', sortAt: '2026-06-02T09:04:00Z', message: {
            id: 'msg-after-tools',
            role: 'assistant',
            authorId: 'agent-alpha',
            authorName: '顾明澈',
            content: '工具执行完成。',
            createdAt: 'Jun 02, 09:04'
          } }
        ]
      })
    })

    expect(screen.getByRole('button', { name: 'Tool calls' }).getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByText('tool_a')).toBeNull()
    expect(screen.getByText('工具执行完成。')).toBeTruthy()
  })

  it('does not repaint an unchanged tool drawer while response text is streaming', async () => {
    const toolItem = {
      id: 'run-stable-tools',
      kind: 'run' as const,
      sortAt: '2026-06-02T09:01:00Z',
      run: {
        id: 'run-stable-tools',
        agentId: 'agent-alpha',
        agentName: '顾明澈',
        status: 'completed',
        runtimeKind: 'codex',
        createdAt: 'Jun 02, 09:01',
        toolCalls: [
          { id: 'stable-a', name: 'tool_a', status: 'completed', createdAt: 'Jun 02, 09:01' },
          { id: 'stable-b', name: 'tool_b', status: 'completed', createdAt: 'Jun 02, 09:02' },
          { id: 'stable-c', name: 'tool_c', status: 'completed', createdAt: 'Jun 02, 09:03' }
        ]
      }
    }
    const responseItem = {
      id: 'streaming-run-stable-tools',
      kind: 'message' as const,
      sortAt: '2026-06-02T09:04:00Z',
      message: {
        id: 'streaming-run-stable-tools',
        role: 'assistant',
        authorId: 'agent-alpha',
        authorName: '顾明澈',
        content: '正在整理',
        createdAt: 'Jun 02, 09:04'
      }
    }
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'running', timeline: [toolItem, responseItem] })
      }
    })
    const drawer = screen.getByTestId('discussion-tool-call-drawer')
    await fireEvent.click(within(drawer).getByRole('button', { name: 'Tool calls' }))
    const drawerContent = drawer.querySelector('[data-tool-call-drawer-content]')
    const mutations: MutationRecord[] = []
    const observer = new MutationObserver((records) => mutations.push(...records))
    observer.observe(drawer, { attributes: true, childList: true, characterData: true, subtree: true })

    await view.rerender({
      draft: '',
      discussion: discussion({
        runtimeStatus: 'running',
        timeline: [
          { ...toolItem, run: { ...toolItem.run, toolCalls: toolItem.run.toolCalls.map((tool) => ({ ...tool })) } },
          { ...responseItem, message: { ...responseItem.message, content: '正在整理最终结果' } }
        ]
      })
    })
    await Promise.resolve()
    observer.disconnect()

    expect(screen.getByTestId('discussion-tool-call-drawer')).toBe(drawer)
    expect(drawer.querySelector('[data-tool-call-drawer-content]')).toBe(drawerContent)
    expect(mutations).toHaveLength(0)
    expect(screen.getByText('正在整理最终结果')).toBeTruthy()
  })

  it('attaches an agent result only to the nearest preceding work card', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [{
            id: 'msg-final',
            role: 'assistant',
            authorId: 'agent-alpha',
            authorName: '顾明澈',
            content: 'Second run result is ready.',
            createdAt: 'Jun 02, 09:04',
            sortAt: '2026-06-02T09:04:00Z'
          }],
          runs: [
            {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'pi',
              createdAt: 'Jun 02, 09:01',
              sortAt: '2026-06-02T09:01:00Z',
              toolCalls: [],
              planStatus: 'completed',
              planSteps: [
                { id: 'run-1-step-1', text: 'Prepare launch context', status: 'completed' }
              ]
            },
            {
              id: 'run-2',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'pi',
              createdAt: 'Jun 02, 09:03',
              sortAt: '2026-06-02T09:03:00Z',
              toolCalls: [],
              planStatus: 'completed',
              planSteps: [
                { id: 'run-2-step-1', text: 'Create follow-up task', status: 'completed' }
              ]
            }
          ],
          timeline: [
            { id: 'run-1', kind: 'run', sortAt: '2026-06-02T09:01:00Z', run: {
              id: 'run-1',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'pi',
              createdAt: 'Jun 02, 09:01',
              sortAt: '2026-06-02T09:01:00Z',
              toolCalls: [],
              planStatus: 'completed',
              planSteps: [
                { id: 'run-1-step-1', text: 'Prepare launch context', status: 'completed' }
              ]
            } },
            { id: 'run-2', kind: 'run', sortAt: '2026-06-02T09:03:00Z', run: {
              id: 'run-2',
              agentId: 'agent-alpha',
              agentName: '顾明澈',
              status: 'completed',
              runtimeKind: 'pi',
              createdAt: 'Jun 02, 09:03',
              sortAt: '2026-06-02T09:03:00Z',
              toolCalls: [],
              planStatus: 'completed',
              planSteps: [
                { id: 'run-2-step-1', text: 'Create follow-up task', status: 'completed' }
              ]
            } },
            { id: 'msg-final', kind: 'message', sortAt: '2026-06-02T09:04:00Z', message: {
              id: 'msg-final',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: '顾明澈',
              content: 'Second run result is ready.',
              createdAt: 'Jun 02, 09:04',
              sortAt: '2026-06-02T09:04:00Z'
            } }
          ]
        })
      }
    })

    expect(screen.getByTestId('discussion-agent-work-card-run-1').textContent).toContain('Work complete')
    expect(screen.getByTestId('discussion-agent-work-card-run-1').textContent).not.toContain('Second run result is ready.')
    expect(screen.getAllByText('Second run result is ready.')).toHaveLength(1)
  })

  it('renders multiple mention links as readable @names in Discussion messages', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [
            {
              id: 'msg-1',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: '[@本地用户](mention://user/local-user) [@协作员](mention://agent/agent-beta) 好的，我来创建 Mission。',
              createdAt: 'Jun 02, 09:00'
            }
          ]
        })
      }
    })

    expect(screen.getByText('@本地用户')).toBeTruthy()
    expect(screen.getByText('@协作员')).toBeTruthy()
    expect(screen.queryByText(/mention:\/\/user\/local-user/)).toBeNull()
    expect(screen.queryByText(/mention:\/\/agent\/agent-beta/)).toBeNull()
    expect([...container.querySelectorAll('.discussion-mention')].map((item) => item.textContent)).toEqual(['@本地用户', '@协作员'])
  })

  it('renders a bare provider mention URI with the persisted member name', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          members: [
            { kind: 'agent', id: 'profile-f54c4401bfb058eadd0e48a5a878b4e4', name: '前端开发 Agent', role: 'Frontend' }
          ],
          messages: [
            {
              id: 'msg-bare-mention',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: '已安排执行。\n\nmention://agent/profile-f54c4401bfb058eadd0e48a5a878b4e4 请负责交付。',
              createdAt: 'Jun 02, 09:00'
            }
          ]
        })
      }
    })

    expect(screen.getByText('@前端开发 Agent')).toBeTruthy()
    expect(container.textContent).not.toContain('mention://agent/')
  })

  it('resolves a bare provider mention from the run name before members refresh', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'running',
          members: [],
          activeResponders: ['Codex'],
          runs: [
            {
              id: 'run-codex',
              agentId: 'profile-f54c4401bfb058eadd0e48a5a878b4e4',
              agentName: 'Codex',
              status: 'running',
              runtimeKind: 'codex',
              toolCalls: [],
              createdAt: 'Jul 22, 12:53'
            }
          ],
          messages: [
            {
              id: 'msg-bare-mention-before-member-snapshot',
              role: 'assistant',
              authorId: 'lead-agent',
              authorName: 'Lead Agent',
              content: '已把 Codex 拉进来了。\n\nmention://agent/profile-f54c4401bfb058eadd0e48a5a878b4e4 Codex，请响应。',
              createdAt: 'Jul 22, 12:53'
            }
          ]
        })
      }
    })

    expect(screen.getByText('@Codex')).toBeTruthy()
    expect(container.textContent).not.toContain('profile-f54c4401bfb058eadd0e48a5a878b4e4')
  })

  it('sanitizes discussion message markdown before injecting HTML', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [
            {
              id: 'msg-xss',
              role: 'assistant',
              authorId: 'agent-alpha',
              authorName: 'Lead Agent',
              content: '<img src=x onerror=alert(1)> [bad](javascript:alert(1)) [ok](https://example.com)',
              createdAt: 'Jun 02, 09:00'
            }
          ]
        })
      }
    })

    const content = container.querySelector('.discussion-message-content')
    expect(content?.textContent).toContain('<img src=x onerror=alert(1)>')
    expect(content?.querySelector('img')).toBeNull()
    expect(content?.querySelector('[onerror]')).toBeNull()
    expect(content?.innerHTML).not.toContain('javascript:alert')
    expect(content?.querySelector('a[href="https://example.com"]')?.textContent).toBe('ok')
  })

})
