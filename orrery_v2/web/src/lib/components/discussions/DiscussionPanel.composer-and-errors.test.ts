import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DiscussionPanel from './DiscussionPanel.svelte'
import { setLocale } from '#lib/i18n'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import { discussion } from './DiscussionPanel.fixtures'

describe('DiscussionPanel messages, composer, and runtime errors', () => {
  beforeEach(() => {
    setLocale('en-US')
    clearSession()
  })

  it('shows an orchestration warning below a preserved Lead answer', () => {
    const warning = '已创建 Mission，但 Lead Agent 没有将任务交给专业执行 Agent。该答复可能并非专业回答。'
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          status: 'completed',
          messages: [{
            id: 'msg-warning',
            role: 'assistant',
            authorId: 'lead-agent',
            authorName: '顾明澈',
            content: '这是 PM 直接生成并保留下来的答案。',
            orchestrationWarning: warning,
            createdAt: 'Jun 02, 09:00'
          }]
        })
      }
    })

    expect(screen.getByText('这是 PM 直接生成并保留下来的答案。')).toBeTruthy()
    const warningElement = screen.getByTestId('discussion-orchestration-warning')
    expect(warningElement.textContent).toContain(warning)
    expect(warningElement.closest('.discussion-message-bubble')).toBeNull()
    expect(screen.queryByText(/discussion orchestration incomplete/)).toBeNull()
  })

  it('only styles real notification mentions, not plain @ references', () => {
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
              content: '普通提一下 @顾明澈，然后通知 [@秦若兰](mention://agent/agent-beta) 处理。',
              createdAt: 'Jun 02, 09:00'
            }
          ]
        })
      }
    })

    expect(screen.getByText(/普通提一下 @顾明澈/)).toBeTruthy()
    expect([...container.querySelectorAll('.discussion-mention')].map((item) => item.textContent)).toEqual(['@秦若兰'])
  })

  it('marks mentions in the current user message bubble for high-contrast styling', () => {
    applySession({
      accessToken: 'token',
      user: {
        userId: 'user-1',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: 'operator@example.com',
        agentProfileId: 'human-operator',
        role: 'admin'
      },
      permissionScope: [],
      expiresAt: ''
    })

    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [
            {
              id: 'msg-user',
              role: 'user',
              authorId: 'operator',
              authorName: 'Operator',
              content: '[@秦若兰](mention://agent/agent-beta) 请确认。',
              createdAt: 'Jun 02, 09:00'
            }
          ]
        })
      }
    })

    const ownBubble = container.querySelector('.discussion-message-bubble[data-message-own="true"]')
    expect(ownBubble?.querySelector('.discussion-mention')?.textContent).toBe('@秦若兰')
  })

  it('uses server ownership first and falls back to browser identity aliases', () => {
    applySession({
      accessToken: 'token',
      user: {
        userId: 'user-ding',
        username: 'ding',
        displayName: 'Ding',
        nickname: 'Ding',
        email: 'ding@example.com',
        agentProfileId: 'human-ding',
        role: 'member'
      },
      permissionScope: [],
      expiresAt: ''
    })

    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        avatarIdentityById: {
          'human-ding': { profileId: 'human-ding', human: true }
        },
        discussion: discussion({
          runtimeStatus: 'running',
          messages: [
            {
              id: 'msg-persisted',
              role: 'user',
              authorId: 'human-ding',
              authorName: 'Ding',
              content: 'Persisted message',
              createdAt: 'Aug 08, 09:47 PM'
            },
            {
              id: 'msg-server-owned',
              role: 'user',
              authorId: 'server-only-id',
              authorName: 'Ding',
              isCurrentViewer: true,
              content: 'Server-owned message',
              createdAt: 'Aug 12, 01:08 AM'
            },
            {
              id: 'msg-pending',
              role: 'user',
              authorId: 'ding',
              authorName: 'Ding',
              content: 'Pending message',
              createdAt: 'Aug 12, 01:09 AM'
            },
            {
              id: 'msg-other',
              role: 'user',
              authorId: 'human-ding',
              authorName: 'Other',
              isCurrentViewer: false,
              content: 'Other message',
              createdAt: 'Aug 12, 01:10 AM'
            }
          ]
        })
      }
    })

    const bubbles = [...container.querySelectorAll<HTMLElement>('.discussion-message-bubble')]
    expect(bubbles.map((bubble) => bubble.dataset.messageOwn)).toEqual(['true', 'true', 'true', 'false'])
    expect(bubbles.slice(0, 3).every((bubble) => bubble.closest('article')?.classList.contains('justify-end'))).toBe(true)
    expect(bubbles[3].closest('article')?.classList.contains('justify-end')).toBe(false)
    expect(container.querySelector('[data-discussion-timeline]')?.classList.contains('space-y-7')).toBe(true)

    const avatars = [...container.querySelectorAll<HTMLElement>('[data-message-avatar]')]
    expect(avatars.map((avatar) => avatar.dataset.messageAvatarSide)).toEqual(['own', 'own', 'own', 'other'])
    expect(avatars.every((avatar) => avatar.querySelector('[data-workforce-avatar]'))).toBe(true)
    expect(avatars.slice(0, 3).map((avatar) => avatar.querySelector('[data-workforce-avatar]')?.getAttribute('data-workforce-profile-id'))).toEqual([
      'human-ding',
      'human-ding',
      'human-ding'
    ])
  })

  it('renders agent acknowledgement chips under the source message', () => {
    const ack = {
      id: 'event-ack',
      agentId: 'agent-beta',
      agentName: '秦若兰',
      emoji: '👍',
      createdAt: 'Jun 02, 09:01'
    }
    const message = {
      id: 'msg-lead',
      role: 'assistant' as const,
      authorId: 'agent-alpha',
      authorName: '顾明澈',
      content: '[@秦若兰](mention://agent/agent-beta) 请确认一下。',
      createdAt: 'Jun 02, 09:00',
      acknowledgements: [ack]
    }
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [message],
          timeline: [
            { id: 'msg-lead', kind: 'message', sortAt: '2026-06-02T09:00:00Z', message }
          ]
        })
      }
    })

    expect(screen.getByText('👍 秦若兰 已收到')).toBeTruthy()
  })

  it('sends a non-empty draft with Enter', async () => {
    const onSend = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: 'Send this message',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onSend
      }
    })

    const composer = screen.getByRole('textbox', { name: 'Chat message' })
    await fireEvent.keyDown(composer, { key: 'Enter' })

    expect(onSend).toHaveBeenCalledTimes(1)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('places the new Agent session action in the composer beside Mission', async () => {
    const onNewSession = vi.fn()
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onNewSession,
        onMissionStart: vi.fn()
      }
    })

    const newSession = screen.getByRole('button', { name: 'Start a new session with this Agent' })
    const mission = screen.getByRole('button', { name: 'Toggle Mission mode' })
    expect(newSession.textContent?.trim()).toBe('New')
    expect(newSession.querySelector('svg')).toBeNull()
    expect(newSession.parentElement).toBe(mission.parentElement)
    expect(newSession.compareDocumentPosition(mission) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    await fireEvent.click(newSession)
    expect(onNewSession).toHaveBeenCalledTimes(1)
    expect(container.querySelector('textarea')).toBeTruthy()
  })

  it('places voice recording beside Send and uploads the captured audio', async () => {
    const stopTrack = vi.fn()
    const getUserMedia = vi.fn().mockResolvedValue({
      getTracks: () => [{ stop: stopTrack }]
    } as unknown as MediaStream)
    const browserNavigator = navigator

    class FakeMediaRecorder {
      static isTypeSupported() {
        return true
      }

      state: RecordingState = 'inactive'
      mimeType: string
      ondataavailable: ((event: BlobEvent) => void) | null = null
      onstop: ((event: Event) => void) | null = null
      onerror: ((event: Event) => void) | null = null

      constructor(_stream: MediaStream, options?: MediaRecorderOptions) {
        this.mimeType = options?.mimeType || 'audio/webm'
      }

      start() {
        this.state = 'recording'
      }

      stop() {
        this.state = 'inactive'
        this.ondataavailable?.({ data: new Blob(['recorded voice'], { type: this.mimeType }) } as BlobEvent)
        this.onstop?.(new Event('stop'))
      }
    }

    vi.stubGlobal('navigator', Object.assign(Object.create(browserNavigator), {
      mediaDevices: { getUserMedia }
    }))
    vi.stubGlobal('MediaRecorder', FakeMediaRecorder)
    const onAttachmentUpload = vi.fn().mockResolvedValue(undefined)

    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' }),
        attachmentEnabled: true,
        onAttachmentUpload
      }
    })

    const voice = await screen.findByRole('button', { name: 'Record voice message' }) as HTMLButtonElement
    const send = screen.getByRole('button', { name: 'Send message' })
    await waitFor(() => expect(voice.disabled).toBe(false))
    expect(voice.parentElement).toBe(send.parentElement)
    expect(voice.compareDocumentPosition(send) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    await fireEvent.click(voice)
    expect(getUserMedia).toHaveBeenCalledWith({ audio: true })
    expect(screen.getByRole('button', { name: 'Stop voice recording' })).toBeTruthy()
    expect(screen.getByText('00:00')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Stop voice recording' }))
    await waitFor(() => expect(onAttachmentUpload).toHaveBeenCalledTimes(1))
    const recordedFile = onAttachmentUpload.mock.calls[0][0] as File
    expect(recordedFile.name).toMatch(/^voice-\d{8}-\d{6}\.webm$/)
    expect(recordedFile.type).toContain('audio/webm')
    expect(recordedFile.size).toBeGreaterThan(0)
    expect(stopTrack).toHaveBeenCalled()
  })

  it('renders audio attachments as playable voice messages', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          messages: [{
            id: 'msg-voice',
            role: 'user',
            authorId: 'user-1',
            authorName: 'Operator',
            content: '',
            createdAt: 'Jun 02, 09:00',
            attachments: [{
              id: 'att-voice',
              discussionId: 'conv-1',
              messageId: 'msg-voice',
              fileName: 'voice.webm',
              contentType: 'audio/webm',
              sizeBytes: 1024,
              uploadedBy: 'user-1',
              uploadedByKind: 'user',
              uploadedByName: 'Operator',
              storagePath: '',
              downloadUrl: '/api/discussions/conv-1/attachments/att-voice/download',
              createdAt: 'Jun 02, 09:00'
            }]
          }]
        })
      }
    })

    const player = screen.getByLabelText('Play voice message voice.webm') as HTMLAudioElement
    expect(player.getAttribute('src')).toBe('/api/discussions/conv-1/attachments/att-voice/download')
  })

  it('keeps modified Enter for multiline text and ignores Enter during IME composition', async () => {
    const onSend = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: 'Keep composing',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onSend
      }
    })

    const composer = screen.getByRole('textbox', { name: 'Chat message' })
    await fireEvent.keyDown(composer, { key: 'Enter', ctrlKey: true })
    await fireEvent.keyDown(composer, { key: 'Enter', shiftKey: true })
    await fireEvent.keyDown(composer, { key: 'Enter', isComposing: true })

    expect(onSend).not.toHaveBeenCalled()
  })

  it('lets the user abort an active discussion run', async () => {
    const onAbort = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'running',
          activeResponders: ['顾明澈']
        }),
        onAbort
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Abort chat' }))

    expect(onAbort).toHaveBeenCalledTimes(1)
  })

  it('shows failed runtime state when the Agent run fails', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: 'discussion orchestration incomplete: agent stopped before task completion'
        })
      }
    })

    expect(screen.getByText('discussion orchestration incomplete: agent stopped before task completion')).toBeTruthy()
  })

  it('does not render an empty work card for a failed run without plan or tool progress', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: 'discussion runtime execution failed',
          runs: [{
            id: 'run-1',
            agentId: 'agent-alpha',
            agentName: '顾明澈',
            status: 'failed',
            runtimeKind: 'pi',
            toolCalls: [],
            createdAt: 'Jun 02, 09:01'
          }],
          timeline: []
        })
      }
    })

    expect(screen.getByText('discussion runtime execution failed')).toBeTruthy()
    expect(screen.queryByLabelText('Agent work card')).toBeNull()
    expect(screen.queryByText('No tool calls yet')).toBeNull()
    expect(screen.queryByText('0/0')).toBeNull()
  })

  it('guides runtime setup errors to settings instead of showing raw executable failures', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: 'discussion runtime unavailable: runtime kind "pi": exec: "pi": executable file not found'
        })
      }
    })

    expect(screen.getByText(/Agent runtime needs setup/)).toBeTruthy()
    const settingsLink = screen.getByRole('link', { name: 'Open Settings' })
    expect(settingsLink.getAttribute('href')).toBe('/settings')
    expect(screen.queryByText(/executable file not found/)).toBeNull()
  })

  it('shows runtime execution diagnostics instead of setup guidance', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: 'discussion runtime unavailable: runtime kind "pi": [NodeRunError] tool run_terminal not found in toolsNode'
        })
      }
    })

    expect(screen.getByText(/run_terminal not found/)).toBeTruthy()
    expect(screen.queryByText(/Agent runtime needs setup/)).toBeNull()
    expect(screen.queryByRole('link', { name: 'Open Settings' })).toBeNull()
  })

  it('shows post-runtime pipeline diagnostics instead of setup guidance', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: 'discussion runtime unavailable: runtime kind "pi": summary cannot be generated without runtime output, deliverables, or evidence'
        })
      }
    })

    expect(screen.getByText(/summary cannot be generated/)).toBeTruthy()
    expect(screen.queryByText(/Agent runtime needs setup/)).toBeNull()
    expect(screen.queryByRole('link', { name: 'Open Settings' })).toBeNull()
  })

  it('shows PI stdout parser diagnostics instead of generic send failure', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: 'discussion runtime execution failed: runtime kind "pi": pi stdout parse failed: invalid runtime event\n[stdout]\nNo API key found for minimax-cn.'
        })
      }
    })

    expect(screen.getByText(/No API key found for minimax-cn/)).toBeTruthy()
    expect(screen.queryByText('Message send failed')).toBeNull()
    expect(screen.queryByText(/Agent runtime needs setup/)).toBeNull()
  })

  it('deduplicates matching stream and runtime errors', () => {
    const message = 'knowledge embedding failed'

    render(DiscussionPanel, {
      props: {
        draft: 'retry',
        errorMessage: message,
        discussion: discussion({
          runtimeStatus: 'failed',
          status: 'failed',
          runtimeError: message
        })
      }
    })

    expect(screen.getAllByText(message)).toHaveLength(1)
  })
})
