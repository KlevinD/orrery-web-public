import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
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

  it('renders linked missions as workspace entry links', async () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'completed' })
      }
    })

    expect(container.querySelector('[data-discussion-mission-strip]')).toBeTruthy()
    expect(screen.getByRole('tab', { name: /Launch plan/ }).getAttribute('aria-selected')).toBe('true')

    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))

    const link = screen.getByRole('link', { name: 'Open Mission workspace: Launch plan' })
    expect(link.getAttribute('href')).toBe('/missions/mission-1')
  })

  it('renders a single workspace entry for the active linked mission', async () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          linkedMissions: [
            { id: 'mission-1', title: 'Launch plan', status: 'active', href: '/missions/mission-1' },
            { id: 'mission-2', title: 'Launch plan follow-up', status: 'active', href: '/missions/mission-2' }
          ]
        }),
        activeMissionId: 'mission-2'
      }
    })

    const firstMission = screen.getByRole('tab', { name: /^Launch plan ·/ })
    const secondMission = screen.getByRole('tab', { name: /Launch plan follow-up/ })
    expect(firstMission.getAttribute('aria-selected')).toBe('false')
    expect(secondMission.getAttribute('aria-selected')).toBe('true')

    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))
    let links = screen.getAllByRole('link', { name: /Open Mission workspace/ })
    expect(links).toHaveLength(1)
    await waitFor(() => expect(links[0].getAttribute('href')).toBe('/missions/mission-2'))
    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))

    await fireEvent.click(firstMission)
    expect(firstMission.getAttribute('aria-selected')).toBe('true')

    await fireEvent.click(screen.getByRole('button', { name: 'More actions' }))

    links = screen.getAllByRole('link', { name: /Open Mission workspace/ })
    expect(links).toHaveLength(1)
    await waitFor(() => expect(links[0].getAttribute('href')).toBe('/missions/mission-1'))
  })

  it('uses unified workforce avatars as a group stack in the top-left header', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          members: [
            { kind: 'user', id: 'owner', name: 'Owner', role: 'Owner' },
            { kind: 'agent', id: 'planner', name: 'Planner', role: 'Planning' },
            { kind: 'agent', id: 'researcher', name: 'Researcher', role: 'Research' },
            { kind: 'agent', id: 'operator', name: 'Operator', role: 'Operations' },
            { kind: 'user', id: 'reviewer', name: 'Reviewer', role: 'Review' }
          ]
        })
      }
    })

    const stack = container.querySelector('[data-group-avatar-stack]')
    expect(stack).toBeTruthy()
    expect(stack?.querySelectorAll('[data-workforce-avatar]')).toHaveLength(4)
    expect(stack?.textContent).toContain('+1')
  })

  it('starts a new Mission from the composer instead of the Mission tabs', async () => {
    const onMissionStart = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: 'Complete customer validation',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onMissionStart
      }
    })

    expect(screen.queryByRole('button', { name: 'New Mission' })).toBeNull()
    const missionMode = screen.getByRole('button', { name: 'Toggle Mission mode' })
    await fireEvent.click(missionMode)

    expect(missionMode.getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByRole('textbox', { name: 'Mission goal' })).toBeTruthy()
    expect(screen.getByRole('combobox', { name: 'Mission launch mode' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Start Mission' }))
    expect(onMissionStart).toHaveBeenCalledWith({
      prompt: 'Complete customer validation',
      launchMode: 'immediate',
      scheduledAt: ''
    })
  })

  it('keeps Mission mode selected when launch fails', async () => {
    const onMissionStart = vi.fn().mockRejectedValue(new Error('Mission unavailable'))
    render(DiscussionPanel, {
      props: {
        draft: 'Complete customer validation',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onMissionStart
      }
    })

    const missionMode = screen.getByRole('button', { name: 'Toggle Mission mode' })
    await fireEvent.click(missionMode)
    await fireEvent.click(screen.getByRole('button', { name: 'Start Mission' }))

    await waitFor(() => expect(onMissionStart).toHaveBeenCalled())
    expect(missionMode.getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByRole('textbox', { name: 'Mission goal' })).toBeTruthy()
  })

  it('keeps current chat attachments out of a new Mission', async () => {
    const onMissionStart = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: 'Review the attached brief',
        discussion: discussion({ runtimeStatus: 'completed' }),
        pendingAttachments: [{
          id: 'attachment-1',
          discussionId: 'conv-1',
          messageId: '',
          fileName: 'brief.pdf',
          contentType: 'application/pdf',
          sizeBytes: 1024,
          uploadedBy: 'user-1',
          uploadedByKind: 'user',
          uploadedByName: 'Owner',
          storagePath: '',
          downloadUrl: '/api/discussions/conv-1/attachments/attachment-1/download',
          createdAt: 'Jun 02, 09:00'
        }],
        onMissionStart
      }
    })

    const missionMode = screen.getByRole('button', { name: 'Toggle Mission mode' })
    expect(missionMode.hasAttribute('disabled')).toBe(true)
    expect(missionMode.getAttribute('title')).toBe('Send the current attachments before starting a Mission')
    await fireEvent.click(missionMode)

    expect(missionMode.getAttribute('aria-pressed')).toBe('false')
    expect(onMissionStart).not.toHaveBeenCalled()
  })

  it('disables attachment upload while composing a Mission', async () => {
    render(DiscussionPanel, {
      props: {
        draft: 'Complete customer validation',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onMissionStart: vi.fn()
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Toggle Mission mode' }))

    expect(screen.getByRole('button', { name: 'Upload attachment' }).hasAttribute('disabled')).toBe(true)
  })

  it('requires a start time for a scheduled Mission', async () => {
    const onMissionStart = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: 'Prepare the weekly operating review',
        discussion: discussion({ runtimeStatus: 'completed' }),
        onMissionStart
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Toggle Mission mode' }))
    await fireEvent.change(screen.getByRole('combobox', { name: 'Mission launch mode' }), { target: { value: 'scheduled' } })
    const startButton = screen.getByRole('button', { name: 'Start Mission' })
    expect(startButton.hasAttribute('disabled')).toBe(true)

    await fireEvent.input(screen.getByLabelText('Mission start time'), { target: { value: '2026-08-12T09:30' } })
    expect(startButton.hasAttribute('disabled')).toBe(false)
    await fireEvent.click(startButton)

    expect(onMissionStart).toHaveBeenCalledWith({
      prompt: 'Prepare the weekly operating review',
      launchMode: 'scheduled',
      scheduledAt: '2026-08-12T09:30'
    })
  })

  it('uses unified workforce avatars as a group stack in the top-left header', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'completed',
          members: [
            { kind: 'user', id: 'owner', name: 'Owner', role: 'Owner' },
            { kind: 'agent', id: 'planner', name: 'Planner', role: 'Planning' },
            { kind: 'agent', id: 'researcher', name: 'Researcher', role: 'Research' },
            { kind: 'agent', id: 'operator', name: 'Operator', role: 'Operations' },
            { kind: 'user', id: 'reviewer', name: 'Reviewer', role: 'Review' }
          ]
        })
      }
    })

    const stack = container.querySelector('[data-group-avatar-stack]')
    expect(stack).toBeTruthy()
    expect(stack?.querySelectorAll('[data-workforce-avatar]')).toHaveLength(4)
    expect(stack?.textContent).toContain('+1')
  })

  it('labels direct conversations as private chats', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          kind: 'direct',
          directAgentId: 'staffing-agent',
          directAgentName: '秦若兰',
          title: '秦若兰',
          runtimeStatus: 'completed',
          linkedMissions: [],
          members: [
            { kind: 'agent', id: 'staffing-agent', name: '秦若兰', role: 'Private chat target' },
            { kind: 'user', id: 'operator', name: 'Operator', role: 'Starter' }
          ]
        })
      }
    })

    expect(screen.getAllByText('Private chat').length).toBeGreaterThan(0)
    expect(screen.getAllByText('秦若兰').length).toBeGreaterThan(0)
  })

  it('renders the streamed Codex reasoning summary in the work card', () => {
    const run = {
      id: 'run-thinking',
      agentId: 'agent-alpha',
      agentName: 'Codex',
      status: 'running',
      runtimeKind: 'codex',
      toolCalls: [],
      thinking: 'Checking the runtime bridge.',
      createdAt: 'Jun 02, 09:01',
      sortAt: '2026-06-02T09:01:00Z'
    }
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          runtimeStatus: 'running',
          runs: [run],
          timeline: [{ id: run.id, kind: 'run', sortAt: run.sortAt, run }]
        })
      }
    })

    expect(screen.getByText('Reasoning summary')).toBeTruthy()
    expect(screen.getByText('Checking the runtime bridge.')).toBeTruthy()
  })

  it('keeps a person chat avatar consistent with the conversation list', () => {
    const { container } = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({
          kind: 'direct',
          directAgentId: '',
          directAgentName: '',
          directTargetKind: 'user',
          directTargetId: 'human-reviewer',
          directTargetName: 'Reviewer',
          title: 'Reviewer',
          runtimeStatus: 'completed',
          linkedMissions: [],
          members: [
            { kind: 'user', id: 'human-reviewer', name: 'Reviewer', role: 'Private chat target' },
            { kind: 'user', id: 'operator', name: 'Operator', role: 'Starter' }
          ]
        })
      }
    })

    const avatar = container.querySelector('header [data-workforce-avatar]')
    expect(avatar).toBeTruthy()
    expect(avatar?.getAttribute('data-workforce-profile-id')).toBe('human-reviewer')
    expect(avatar?.classList.contains('bg-source-human')).toBe(true)
    expect(avatar?.classList.contains('bg-amber-500')).toBe(false)
  })
})
