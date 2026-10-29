import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { queryClient } from '#lib/query-client'
import { setLocale } from '#lib/i18n'
import { applySession } from '#lib/stores/session.svelte'
import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
import { clearDiscussionWorkspaceContext, updateDiscussionWorkspaceContext } from '#lib/discussion-workspace-context.svelte'
import { clearConversationSwitchContext, updateConversationSwitchContext } from '#lib/conversation-switch-context.svelte'
import { discussion, missionContext } from '#lib/components/discussions/DiscussionPanel.fixtures'
import WorkspaceContextPanelHarness from './WorkspaceContextPanelHarness.svelte'

vi.mock('force-graph', () => ({ default: vi.fn(function () {
  const graph = new Proxy({}, { get: () => vi.fn(() => graph) })
  return graph
}) }))

describe('WorkspaceContextPanel', () => {
  beforeEach(() => {
    queryClient.clear()
    setLocale('en-US')
    ontologyScopeContext.mode = 'all'
    ontologyScopeContext.activeScope = undefined
    ontologyScopeContext.draftTenantId = ''
    ontologyScopeContext.resolving = false
    ontologyScopeContext.error = ''
    clearDiscussionWorkspaceContext()
    clearConversationSwitchContext()
    applySession({
      permissionScope: ['todo.read', 'idea.read', 'discussion.read', 'approvals.read', 'mission.read', 'scheduler.read', 'operations.read', 'workforce.read', 'org.read', 'roles.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'operator-1',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: 'operator@example.com',
        agentProfileId: 'human-operator',
        role: 'operator'
      }
    })
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/hil/decisions')) {
        return new Response(JSON.stringify({
          items: [{
            id: 'decision-council-1', tenant_id: 'system', requester: { kind: 'agent', id: 'agent-alpha' }, action: 'Publish launch plan',
            resource: { kind: 'mission', id: 'mission-1' }, args_hash: 'sha256:council', risk_level: 'high',
            route: { mode: 'council', reasons: ['multi-party review required'] },
            context: { bundle_id: 'bundle-council', complete: true }, status: 'pending', council_id: 'council-1',
            purpose: 'Approve the launch plan', created_at: '2026-07-17T00:00:00Z'
          }],
          total: 1
        }))
      }
      if (url.includes('/api/approvals')) {
        return new Response(JSON.stringify({
          items: [{
            approval_id: 'approval-1',
            approval_type: 'task_completion',
            status: 'pending',
            mission_id: 'mission-1',
            source_type: 'task',
            source_id: 'task-1',
            title: 'Review task',
            requested_by: 'agent-alpha',
            assigned_to: 'operator-1',
            available_actions: [],
            created_at: '2026-07-17T00:00:00Z'
          }],
          page: { total: 1, returned: 1 }
        }))
      }
      if (url.includes('/api/discussions')) {
        return new Response(JSON.stringify({
          discussions: [{
            discussion_id: 'discussion-1',
            title: 'Completed work',
            status: 'completed',
            linked_missions: 0,
            updated_at: '2026-07-17T00:00:00Z',
            unread_completed: true
          }]
        }))
      }
      if (url.includes('/api/todos')) {
        return new Response(JSON.stringify({
          todos: [{
            todo_id: 'todo-1',
            work_kind: 'general',
            automation_mode: 'manual',
            title: 'Prepare launch briefing',
            status: 'review',
            priority: 'high',
            created_by: 'operator-1',
            created_at: '2026-07-16T00:00:00Z',
            updated_at: '2026-07-17T00:00:00Z',
            due_at: '2026-07-20T00:00:00Z',
            version: 1
          }],
          total: 1,
          limit: 20,
          offset: 0
        }))
      }
      if (url.includes('/api/missions')) {
        return new Response(JSON.stringify({
          missions: [{
            mission_id: 'mission-1',
            title: 'Global launch',
            final_goal: 'Publish the launch plan',
            status: 'active',
            conversation_id: 'discussion-1',
            created_at: '2026-07-15T00:00:00Z'
          }, {
            mission_id: 'mission-2',
            title: 'Security scan',
            final_goal: 'Review the production security posture',
            status: 'active',
            created_at: '2026-07-14T00:00:00Z'
          }, {
            mission_id: 'mission-3',
            title: 'Completed migration',
            final_goal: 'Move the service safely',
            status: 'completed',
            created_at: '2026-07-13T00:00:00Z'
          }],
          summaries: [{
            mission_id: 'mission-1',
            title: 'Global launch',
            status: 'active',
            team_size: 3,
            active_task_count: 2,
            active_run_count: 0,
            pending_approval_count: 0,
            blocker_count: 1,
            delivery_rate: 0.45,
            updated_at: '2026-07-17T00:00:00Z'
          }, {
            mission_id: 'mission-2',
            title: 'Security scan',
            status: 'active',
            team_size: 2,
            active_task_count: 1,
            active_run_count: 1,
            pending_approval_count: 0,
            blocker_count: 0,
            delivery_rate: 0.4,
            updated_at: '2026-07-16T00:00:00Z'
          }, {
            mission_id: 'mission-3',
            title: 'Completed migration',
            status: 'completed',
            team_size: 2,
            active_task_count: 0,
            active_run_count: 0,
            pending_approval_count: 0,
            blocker_count: 0,
            delivery_rate: 1,
            updated_at: '2026-07-15T00:00:00Z'
          }],
          page: { total: 3, returned: 3 }
        }))
      }
      if (url.includes('/api/scheduled-tasks/schedule-mission-1')) {
        return new Response(JSON.stringify({
          scheduled_task_id: 'schedule-mission-1',
          mission_id: 'mission-1',
          agent_task_id: 'agent-task-1',
          title: 'Scheduled launch plan',
          schedule: { type: 'once', run_at: '2099-08-12T01:30:00Z', timezone: 'Asia/Shanghai' },
          target: { mode: 'agent_task', agent_id: 'lead-agent', task_goal: 'Publish the enterprise launch plan' },
          policy: { misfire: 'run_once', concurrency: 'skip_if_running' },
          status: 'enabled',
          enabled: true,
          next_run_at: '2099-08-12T01:30:00Z',
          created_by: 'operator-1',
          created_at: '2026-08-08T00:00:00Z',
          updated_at: '2026-08-08T00:00:00Z'
        }))
      }
      if (url.includes('/api/scheduled-runs')) {
        const notification = url.includes('limit=5')
        return new Response(JSON.stringify({
          items: [{
            task_title: notification ? 'Daily AI briefing' : 'Daily market digest',
            is_read: false,
            attention_required: !notification,
            run: {
              scheduled_run_id: 'run-failed-1',
              scheduled_task_id: 'schedule-1',
              trigger_type: 'due',
              status: notification ? 'completed' : 'failed',
              target_mode: 'agent_task',
              attempt: 1,
              failure_reason: 'runtime failed',
              created_at: '2026-08-03T09:30:00Z'
            }
          }],
          total: 1,
          unread_total: 1,
          attention_total: 1,
          limit: 3,
          offset: 0
        }))
      }
      if (url.includes('/api/scheduled-tasks')) {
        return new Response(JSON.stringify({
          tasks: [{
            scheduled_task_id: 'schedule-1',
            title: 'Daily market digest',
            status: 'enabled',
            enabled: true,
            schedule_type: 'cron',
            target_mode: 'agent_task',
            target_summary: 'agent-alpha',
            executor_agent_id: 'agent-alpha',
            next_run_at: '2026-08-03T09:30:00Z',
            last_run_status: 'failed',
            failure_reason: 'runtime failed'
          }, {
            scheduled_task_id: 'schedule-2',
            title: 'Weekly planning review',
            status: 'enabled',
            enabled: true,
            schedule_type: 'cron',
            target_mode: 'discussion_workflow',
            target_summary: 'planning-team',
            next_run_at: '2026-08-05T02:00:00Z',
            last_run_status: 'completed'
          }],
          total: 2,
          limit: 30,
          offset: 0
        }))
      }
      if (url.includes('/api/workforce/members')) {
        return new Response(JSON.stringify({
          members: [{
            agent_profile_id: 'agent-alpha',
            real_name: 'Alpha',
            display_name: 'Agent Alpha',
            agent_kind: 'ai',
            position_display_name: 'Operations Agent',
            enabled: true
          }]
        }))
      }
      if (url.includes('/api/ontology/context/company-overview')) {
        return new Response(JSON.stringify({
          data: {
            objects: { status: 'exact', value: 9844 },
            facts: { status: 'exact', value: 13321 },
            relations: { status: 'exact', value: 6380 },
            evidence: { status: 'exact', value: 5102 },
            domain_count: { status: 'exact', value: 4 },
            domains: [
              { id: 'organization', objects: { status: 'exact', value: 540 } },
              { id: 'workforce', objects: { status: 'exact', value: 58 } },
              { id: 'execution', objects: { status: 'exact', value: 1024 } },
              { id: 'automation', objects: { status: 'exact', value: 37 } }
            ],
            recent_objects: [
              { tenant_id: 'org-1', id: 'mission-1', object_type: 'mission', label: 'Global launch mission', status: 'active', updated_at: '2026-08-08T07:30:00Z' },
              { tenant_id: 'system', id: 'agent-task-1', object_type: 'agent_task', label: 'Prepare market digest', status: 'active', updated_at: '2026-08-08T07:20:00Z' }
            ],
            newest_updated_at: '2026-08-08T07:30:00Z'
          },
          meta: { as_of: '2026-08-08T07:31:00Z', data_revision: 'revision-1', authorization_revision: 'auth-1' }
        }))
      }
      if (url.includes('/api/ontology/context/neighborhood')) {
        return new Response(JSON.stringify({
          root_id: 'mission-1',
          nodes: [
            { id: 'mission-1', object_type: 'mission', label: 'Global launch mission', status: 'active', degree: 2 },
            { id: 'agent-task-1', object_type: 'agent_task', label: 'Prepare market digest', status: 'active', degree: 1 },
            { id: 'agent-alpha', object_type: 'agent', label: 'Agent Alpha', status: 'active', degree: 1 }
          ],
          edges: [
            { id: 'mission-task', source_id: 'agent-task-1', target_id: 'mission-1', link_type: 'agent_task_supports_mission', link_type_label: 'Supports mission' },
            { id: 'agent-task', source_id: 'agent-alpha', target_id: 'agent-task-1', link_type: 'agent_assigned_to_task', link_type_label: 'Assigned to task' }
          ],
          visible_nodes: 3,
          visible_edges: 2,
          applied_depth: 2,
          applied_max_nodes: 28,
          applied_max_edges: 48,
          truncated: false
        }))
      }
      return new Response('{}')
    })
  })

  it('adds the conversation switcher without replacing the existing context options', async () => {
    updateConversationSwitchContext({
      conversations: [{
        id: 'discussion-1',
        title: 'Launch planning',
        agentName: 'Planner Agent'
      }],
      selectedConversationId: 'discussion-1'
    })

    const { container } = render(WorkspaceContextPanelHarness)

    for (const label of ['Work inbox', 'Mission', 'Automation', 'People', 'Conversations']) {
      expect(screen.getByRole('button', { name: label })).toBeTruthy()
    }
    await waitFor(() => expect(container.querySelector('[data-conversation-switch-panel]')).toBeTruthy())
    expect(screen.getByText('Launch planning')).toBeTruthy()

    updateDiscussionWorkspaceContext(discussion({ id: 'discussion-2', title: 'Customer research' }), missionContext(), false, '')
    await waitFor(() => expect(container.querySelector('[data-conversation-switch-panel]')).toBeTruthy())

    await fireEvent.click(screen.getByRole('button', { name: 'Mission' }))
    expect(container.querySelector('[data-conversation-switch-panel]')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: 'Conversations' }))
    expect(container.querySelector('[data-conversation-switch-panel]')).toBeTruthy()
  })

  it('shows cross-page context without duplicating the primary navigation', () => {
    render(WorkspaceContextPanelHarness)

    expect(screen.queryByRole('button', { name: 'Workspace' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Work inbox' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Mission' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Automation' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'People' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Knowledge graph' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Resources' })).toBeNull()
    expect(screen.queryByText('Current workspace')).toBeNull()
    expect(screen.queryByText('Related pages')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Run records' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Activity' })).toBeNull()
  })

  it('shares complete counts across the main inbox and sidebar without duplicate requests', async () => {
    const fetcher = globalThis.fetch
    globalThis.fetch = vi.fn(async (input, init) => {
      const url = String(input)
      if (!url.includes('/api/todos')) return fetcher(input, init)
      const offset = Number(new URL(url, 'https://example.test').searchParams.get('offset') ?? 0)
      return new Response(JSON.stringify({
        todos: Array.from({ length: Math.min(100, 125 - offset) }, (_, i) => ({
          todo_id: String(offset + i), title: 'Task ' + (offset + i), status: 'inbox',
          priority: 'medium', created_at: '2026-09-05T00:00:00Z'
        })), total: 125
      }))
    })
    const { container } = render(WorkspaceContextPanelHarness, { withMainInbox: true })
    await waitFor(() => {
      expect(Array.from(container.querySelectorAll('[data-work-action-count]')).map(node => node.textContent)).toEqual(['Total: 128', 'Total: 128'])
    })
    expect(vi.mocked(globalThis.fetch).mock.calls.filter(([input]) => String(input).includes('/api/todos'))).toHaveLength(2)
    expect(container.querySelector('[data-work-inbox="sidebar"]')?.querySelectorAll('[data-work-item]')).toHaveLength(8)
    expect(container.querySelector('[data-work-inbox="main"]')?.querySelectorAll('[data-work-item]')).toHaveLength(25)
  })

  it('does not present partial counts as a complete inbox when a source fails', async () => {
    const fetcher = globalThis.fetch
    globalThis.fetch = vi.fn(async (input, init) => String(input).includes('/api/todos')
      ? new Response('{}', { status: 403 }) : fetcher(input, init))
    const { container } = render(WorkspaceContextPanelHarness)
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(container.querySelector('[data-work-action-count]')?.textContent).toBe('Total: 3+')
    expect(container.querySelectorAll('[data-work-item]')).toHaveLength(3)
  })

  it('shows grouped result notifications inside the unified inbox', async () => {
    const { container } = render(WorkspaceContextPanelHarness)
    await waitFor(() => expect(container.querySelector('[data-work-action-count]')?.textContent).toBe('Total: 4'))
    await fireEvent.click(screen.getByRole('tab', { name: /Results/ }))
    await waitFor(() => expect(container.querySelector('[data-activity-row]')?.getAttribute('href')).toBe('/work-items?view=activity&run=run-failed-1&task=schedule-1'))
    expect(container.querySelector('[data-result-notification-badge]')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Result notifications' })).toBeNull()
  })

  it('loads completed todos and report history only when opened', async () => {
    const { container } = render(WorkspaceContextPanelHarness)
    await waitFor(() => expect(container.querySelector('[data-work-action-count]')?.textContent).toBe('Total: 4'))
    const requested = (query: string) => vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes(query))
    expect(requested('include_completed=true')).toBe(false)
    expect(requested('task_id=schedule-1')).toBe(false)
    await fireEvent.click(screen.getByRole('tab', { name: /Results/ }))
    await waitFor(() => expect(requested('/api/scheduled-runs?limit=100&offset=0')).toBe(true))
    await fireEvent.click(screen.getByRole('tab', { name: 'Processed' }))
    await waitFor(() => expect(requested('include_completed=true')).toBe(true))
  })

  it('shows results even when the todo source fails', async () => {
    const fetcher = globalThis.fetch
    globalThis.fetch = vi.fn(async (input, init) => String(input).includes('/api/todos')
      ? new Response('{}', { status: 403 }) : fetcher(input, init))
    const { container } = render(WorkspaceContextPanelHarness)
    expect(await screen.findByRole('alert')).toBeTruthy()
    await fireEvent.click(screen.getByRole('tab', { name: /Results/ }))
    expect(await screen.findByText('Daily market digest')).toBeTruthy()
    expect(container.querySelector('[data-work-action-count]')).toBeNull()
  })

  it('uses live approval, task, runtime, discussion, and workforce data', async () => {
    const { container } = render(WorkspaceContextPanelHarness)

    await waitFor(() => expect(container.querySelector('[data-work-action-count]')?.textContent).toBe('Total: 4'))
    expect(await screen.findByText('Review task')).toBeTruthy()
    expect(await screen.findAllByText('Approve the launch plan')).toBeTruthy()
    expect(await screen.findByText('Prepare launch briefing')).toBeTruthy()
    expect(await screen.findByText('Daily market digest')).toBeTruthy()
    expect(screen.getByText('Review task').closest('a')?.getAttribute('href')).toBe('/work-items?view=inbox&action=approval%3Aapproval-1')
    expect(screen.getAllByText('Approve the launch plan')[0].closest('a')?.getAttribute('href')).toBe('/work-items?view=inbox&action=council%3Acouncil-1')
    expect(screen.getByText('Prepare launch briefing').closest('a')?.getAttribute('href')).toBe('/work-items?view=inbox&action=todo%3Atodo-1')
    expect(screen.getByText('Daily market digest').closest('a')?.getAttribute('href')).toBe('/work-items?view=inbox&action=result%3Arun-failed-1')
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/scheduled-runs?group_by_task=true&limit=100&offset=0'))).toBe(true)
    expect(container.querySelector('[data-work-inbox="sidebar"]')).toBeTruthy()
    await waitFor(() => expect(container.querySelector('[data-ontology-company-overview]')).toBeTruthy())
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/missions'))).toBe(false)
    await fireEvent.click(screen.getByRole('button', { name: 'Mission' }))
    expect(await screen.findByText('Global launch')).toBeTruthy()
    expect(screen.getByText('Global launch').closest('a')?.getAttribute('href')).toBe('/missions/mission-1')
    expect(screen.getByRole('link', { name: 'Open Mission: Global launch' }).getAttribute('href')).toBe('/missions/mission-1')
    expect(screen.getByText('Needs you 2')).toBeTruthy()
    expect(screen.getByText('Global launch').closest('a')?.getAttribute('title')).toContain('Approve 1 · Council vote 1')
    expect(screen.getByText('Security scan')).toBeTruthy()
    expect(screen.queryByText('Completed migration')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Show older (1)' }))
    expect(screen.getByText('Completed migration')).toBeTruthy()
    expect(container.querySelector('[data-context-mission-history]')).toBeTruthy()
    expect(container.querySelector('[data-mission-history-list]')).toBeTruthy()
    expect(container.querySelector('[data-mission-needs-you="mission-1"]')).toBeTruthy()
    expect(container.querySelector('[data-mission-running="mission-2"]')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Automation' }))
    expect(await screen.findByText('Daily market digest')).toBeTruthy()
    expect(container.querySelector('[data-context-automation]')).toBeTruthy()
    expect(container.querySelector('[data-automation-timeline]')).toBeTruthy()
    expect(container.querySelectorAll('[data-automation-timeline-node]')).toHaveLength(2)
    expect(container.querySelectorAll('[data-automation-timeline-line]')).toHaveLength(1)
    expect(screen.getByText(/Agent task/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'People' }))

    expect(await screen.findByText('Agent Alpha')).toBeTruthy()
    expect(screen.getByText('Operations Agent')).toBeTruthy()
    const personAvatar = container.querySelector('[data-context-people] [data-workforce-profile-id="agent-alpha"]')
    expect(personAvatar?.classList.contains('h-10')).toBe(true)
  })

  it('does not request workforce data without people access', async () => {
    applySession({
      permissionScope: ['todo.read', 'idea.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'reader-1',
        username: 'reader',
        displayName: 'Reader',
        nickname: 'Reader',
        email: '',
        agentProfileId: '',
        role: 'member'
      }
    })
    render(WorkspaceContextPanelHarness)
    await fireEvent.click(screen.getByRole('button', { name: 'People' }))

    expect(screen.getByText('This account cannot access the people directory')).toBeTruthy()
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/workforce/members'))).toBe(false)
  })

  it('shows visible Missions without requesting decision data when approval access is absent', async () => {
    applySession({
      permissionScope: ['mission.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'mission-reader', username: 'mission-reader', displayName: 'Mission Reader', nickname: 'Mission Reader',
        email: '', agentProfileId: '', role: 'member'
      }
    })

    render(WorkspaceContextPanelHarness)
    await fireEvent.click(screen.getByRole('button', { name: 'Mission' }))

    expect(await screen.findByText('Security scan')).toBeTruthy()
    expect(screen.queryByText('Needs you 2')).toBeNull()
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/approvals'))).toBe(false)
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/hil/decisions'))).toBe(false)
  })

  it('keeps the Mission history visible when decision status is temporarily unavailable', async () => {
    const fallbackFetch = globalThis.fetch
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('/api/approvals') || url.includes('/api/hil/decisions')) {
        return new Response(JSON.stringify({ code: 'upstream_unavailable' }), { status: 502 })
      }
      return fallbackFetch(input, init)
    })

    render(WorkspaceContextPanelHarness)
    await fireEvent.click(screen.getByRole('button', { name: 'Mission' }))

    expect(await screen.findByText('Security scan')).toBeTruthy()
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/hil/decisions'))).toBe(true)
    expect(screen.queryByText('Needs you 2')).toBeNull()
  })

  it('loads the company ontology without scheduler access', async () => {
    applySession({
      permissionScope: ['operations.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'ontology-reader', username: 'ontology-reader', displayName: 'Ontology Reader', nickname: 'Ontology Reader',
        email: '', agentProfileId: '', role: 'member'
      }
    })

    render(WorkspaceContextPanelHarness)

    expect(await screen.findByText('9,844 objects')).toBeTruthy()
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/scheduled-tasks'))).toBe(false)
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/ontology/context/company-overview'))).toBe(true)
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/ontology/context/objects'))).toBe(false)
    await waitFor(() => expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/ontology/context/neighborhood'))).toBe(true))
  })

  it('uses the Mission tab as a status-aware history instead of a Mission detail inspector', async () => {
    applySession({
      permissionScope: ['discussion.read', 'operations.read', 'approvals.read', 'mission.read', 'scheduler.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'operator-1', username: 'operator', displayName: 'Operator', nickname: 'Operator',
        email: '', agentProfileId: '', role: 'operator'
      }
    })
    updateDiscussionWorkspaceContext(discussion({
      members: [
        { kind: 'agent', id: 'agent-alpha', name: 'Agent Alpha', role: 'Lead' },
        { kind: 'agent', id: 'agent-beta', name: 'Agent Beta', role: 'Research' },
        { kind: 'user', id: 'operator', name: 'Operator', role: 'Owner' }
      ]
    }), missionContext(), false, '')

    const { container } = render(WorkspaceContextPanelHarness)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Mission' })).toBeTruthy())
    await waitFor(() => expect(container.querySelector('[data-context-mission-history]')).toBeTruthy())
    expect(await screen.findByText('Global launch')).toBeTruthy()
    expect(screen.getByText('My Missions')).toBeTruthy()
    expect(screen.getByText('Needs you 2')).toBeTruthy()
    expect(screen.getByText('Global launch').closest('a')?.getAttribute('title')).toContain('Approve 1 · Council vote 1')
    expect(screen.getByText('Security scan')).toBeTruthy()
    expect(screen.queryByText('Completed migration')).toBeNull()
    await fireEvent.change(screen.getByRole('combobox', { name: 'Processed' }), { target: { value: 'all' } })
    expect(screen.getByText('Completed migration')).toBeTruthy()
    expect(container.querySelector('[data-mission-needs-you="mission-1"]')).toBeTruthy()
    expect(container.querySelector('[data-mission-running="mission-2"]')).toBeTruthy()
    expect(container.querySelector('[data-mission-history-state="idle"]')).toBeTruthy()
    expect(container.querySelector('[data-discussion-mission-thumbnail]')).toBeNull()
    expect(screen.queryByRole('tab', { name: 'Overview' })).toBeNull()
    expect(screen.queryByRole('tab', { name: 'Tasks' })).toBeNull()
    expect(screen.queryByRole('tab', { name: 'Members' })).toBeNull()
    expect(screen.queryByText('Publish the enterprise launch plan')).toBeNull()
    expect(screen.queryByText('25%')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Knowledge graph' })).toBeNull()
    await waitFor(() => expect(container.querySelector('[data-ontology-company-overview]')).toBeTruthy())
  })

  it('does not load scheduled Mission detail in the decision queue', async () => {
    const scheduledMission = missionContext()
    scheduledMission.mission.originKind = 'scheduler'
    scheduledMission.mission.originId = 'schedule-mission-1'
    updateDiscussionWorkspaceContext(discussion(), scheduledMission, false, '')

    const { container } = render(WorkspaceContextPanelHarness)
    await waitFor(() => expect(container.querySelector('[data-context-mission-history]')).toBeTruthy())
    expect(container.querySelector('[data-discussion-mission-schedule]')).toBeNull()
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/scheduled-tasks/schedule-mission-1'))).toBe(false)
  })

  it('does not expose linked Mission selectors or creation controls in the decision queue', async () => {
    applySession({
      permissionScope: ['discussion.read', 'operations.read', 'approvals.read', 'mission.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'operator-1', username: 'operator', displayName: 'Operator', nickname: 'Operator',
        email: '', agentProfileId: '', role: 'operator'
      }
    })
    const onMissionSelect = vi.fn()
    const onMissionCreate = vi.fn()
    updateDiscussionWorkspaceContext(discussion({
      linkedMissions: [
        { id: 'mission-1', title: 'Launch plan', status: 'active', href: '/missions/mission-1' },
        { id: 'mission-2', title: 'Customer validation', status: 'pending', href: '/missions/mission-2' }
      ]
    }), missionContext(), false, '', false, 'mission-1', onMissionSelect, onMissionCreate)

    render(WorkspaceContextPanelHarness)
    expect(await screen.findByText('Global launch')).toBeTruthy()
    expect(screen.queryByLabelText('Select current Mission')).toBeNull()
    expect(screen.queryByRole('button', { name: 'New Mission' })).toBeNull()
    expect(onMissionSelect).not.toHaveBeenCalled()
    expect(onMissionCreate).not.toHaveBeenCalled()
  })

  it('shows a localized unavailable state for a deleted linked Mission', async () => {
    updateDiscussionWorkspaceContext(
      discussion(),
      undefined,
      false,
      'resource not found',
      false,
      'mission-1',
      undefined,
      undefined,
      true
    )

    const { container } = render(WorkspaceContextPanelHarness)
    await waitFor(() => expect(container.querySelector('[data-context-mission-history]')).toBeTruthy())

    expect(screen.queryByText('resource not found')).toBeNull()
    expect(container.querySelector('[data-discussion-mission-thumbnail]')).toBeNull()
  })

  it('uses the actual object tenant for the all-visible footer graph', async () => {
    applySession({
      permissionScope: ['operations.read', 'scheduler.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'operator-1', username: 'operator', displayName: 'Operator', nickname: 'Operator',
        email: '', agentProfileId: '', role: 'operator'
      }
    })

    const { container } = render(WorkspaceContextPanelHarness)

    await waitFor(() => expect(container.querySelector('[data-ontology-company-overview]')).toBeTruthy())
    expect(screen.getByText('Company ontology')).toBeTruthy()
    expect(screen.getByText('9,844 objects')).toBeTruthy()
    expect(screen.getByText('13,321')).toBeTruthy()
    expect(screen.getByText('6,380')).toBeTruthy()
    expect(screen.getByText('5,102')).toBeTruthy()
    expect(screen.getByText('Global launch mission')).toBeTruthy()
    expect(screen.getByText('Global launch mission').closest('a')?.getAttribute('href')).toBe('/resources/ontology/context?tenant_id=org-1&object_id=mission-1&source_tenant_id=org-1')
    const overviewCall = vi.mocked(globalThis.fetch).mock.calls.find(([input]) => String(input).includes('/api/ontology/context/company-overview'))
    expect(String(overviewCall?.[0])).not.toContain('tenant_id=')
    expect(screen.getByRole('button', { name: 'Collapse company ontology' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Collapse company ontology' }))
    expect(container.querySelector('[data-ontology-company-overview]')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Expand company ontology' }))
    await waitFor(() => expect(container.querySelector('[data-ontology-company-overview]')).toBeTruthy())
    await waitFor(() => expect(container.querySelector('[data-ontology-footer-graph] [data-force-graph]')).toBeTruthy())
    expect(screen.getByText('Actual data relationships')).toBeTruthy()
    expect(screen.getByText('3 nodes · 2 relationships')).toBeTruthy()
    expect(container.querySelector('[data-ontology-footer-graph]')?.getAttribute('href')).toBe('/resources/ontology/context?tenant_id=org-1&view=graph&object_id=mission-1&source_tenant_id=org-1&root_id=mission-1&depth=2')
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/ontology/context/objects'))).toBe(false)
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => {
      const url = String(input)
      return url.includes('/api/ontology/context/neighborhood') &&
        url.includes('tenant_id=org-1') &&
        url.includes('root_id=mission-1') &&
        url.includes('depth=2') &&
        url.includes('max_nodes=28') &&
        url.includes('max_edges=48')
    })).toBe(true)
  })

  it('keeps the company overview independent from the active ontology scope', async () => {
    applySession({
      permissionScope: ['operations.read', 'scheduler.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'operator-1', username: 'operator', displayName: 'Operator', nickname: 'Operator',
        email: '', agentProfileId: '', role: 'operator'
      }
    })
    ontologyScopeContext.activeScope = {
      tenant_id: 'project-atlas',
      kind: 'project',
      label: 'Project Atlas',
      capabilities: ['ontology.read'],
      binding_version: 3
    }
    ontologyScopeContext.mode = 'scope'

    const { container } = render(WorkspaceContextPanelHarness)

    expect(await screen.findByText('Company ontology')).toBeTruthy()
    await waitFor(() => expect(container.querySelector('[data-ontology-company-overview]')).toBeTruthy())
    const overviewCall = vi.mocked(globalThis.fetch).mock.calls.find(([input]) => String(input).includes('/api/ontology/context/company-overview'))
    expect(String(overviewCall?.[0])).not.toContain('project-atlas')
    await waitFor(() => expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => {
      const url = String(input)
      return url.includes('/api/ontology/context/neighborhood') && url.includes('tenant_id=org-1') && !url.includes('project-atlas')
    })).toBe(true))
  })

  it('shows an unavailable state without falling back to the schema graph', async () => {
    const fallbackFetch = globalThis.fetch
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/api/ontology/context/company-overview')) {
        return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 })
      }
      return fallbackFetch(input, init)
    })

    const { container } = render(WorkspaceContextPanelHarness)

    expect((await screen.findAllByText('Ontology data is unavailable', {}, { timeout: 5000 })).length).toBeGreaterThan(0)
    expect(container.querySelector('[data-ontology-company-overview]')).toBeNull()
    expect(vi.mocked(globalThis.fetch).mock.calls.some(([input]) => String(input).includes('/api/ontology/schema/graph'))).toBe(false)
  })
})
