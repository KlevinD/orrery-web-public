import { describe, expect, it } from 'vitest'
import {
  mapMissionControlView,
  mapMissionControlViewFromSubviews,
  mapMissionCreateRequest,
  mapMissionDiscussionMessageRequest,
  mapMissionDiscussionReplyRequest,
  mapMissionOpsView,
  mapMissionListView,
  mapMissionQaDecisionRequest,
  mapMissionTaskAssignmentRequest,
  mapMissionTaskCreateRequest,
  mapMissionTaskResultRequest
} from './mission'
import type { MissionControlViewDTO, MissionListDTO, MissionOpsViewDTO } from '#lib/types/api'

describe('mission mappers', () => {
  it('maps mission list summaries from backend projections', () => {
    const view = mapMissionListView({
      missions: [{ mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', phase: 'execution', origin_kind: 'todo', origin_id: 'todo-1', origin_todo_id: 'todo-1', execution_attempt: 3, discussion_mode: 'existing_conversation', conversation_id: 'conversation-1', origin_development_project_id: 'project-1', created_at: '2026-06-01T00:00:00Z' }],
      summaries: [{ mission_id: 'm1', title: 'Launch', status: 'active', phase: 'execution', risk_level: 'high', team_size: 3, active_task_count: 2, active_run_count: 1, pending_approval_count: 1, blocker_count: 0, delivery_rate: 0.5, updated_at: '2026-06-01T01:00:00Z' }],
      page: { total: 1, returned: 1 }
    } satisfies MissionListDTO)

    expect(view.summary.active).toBe(1)
    expect(view.missions[0]).toMatchObject({
      id: 'm1', title: 'Launch', phase: 'execute', riskTone: 'danger', activeRunCount: 1, deliveryPercent: 50,
      originKind: 'todo', originId: 'todo-1', originTodoId: 'todo-1', executionAttempt: 3,
      discussionMode: 'existing_conversation', conversationId: 'conversation-1', originDevelopmentProjectId: 'project-1'
    })
  })

  it('uses a translatable sentinel when mission final goal is missing', () => {
    const view = mapMissionListView({
      missions: [{ mission_id: 'm1', title: 'Launch', status: 'active', phase: 'execution', created_at: '2026-06-01T00:00:00Z' }],
      summaries: [],
      page: { total: 1, returned: 1 }
    } satisfies MissionListDTO)

    expect(view.missions[0].description).toBe('Not provided')
    expect(view.missions[0].goals).toEqual([])
  })

  it('maps mission control aggregate into workspace view', () => {
    const dto = {
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', phase: 'build', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 1, pending_approval_count: 1, risk_count: 1, blocker_count: 0, team_size: 2, delivery_rate: 0.25, updated_at: '2026-06-01T01:00:00Z' },
      team: { members: [{ agent_id: 'a1', display_name: 'Ada', real_name: 'Ada', role_name: 'Lead', department: 'Ops', position: 'Lead', mission_role: 'owner', status: 'active', joined_at: '2026-06-01T00:00:00Z' }], assignments: [], staffing_requests: [] },
      task_board: { tasks: [{ task_id: 't1', assignee: 'a1', goal: 'Draft', worker_personal_goal_summary: 'Draft only inside approved scope', status: 'approved', acceptance_criteria: [{ id: 'a1', description: 'Draft is reviewable' }], expected_deliverables: [{ id: 'd1', title: 'Draft memo' }], required_evidence: [{ id: 'e1', description: 'Review note', kind: 'artifact', required: true }], latest_result_ref: 'submission-1', qa_status: 'pending', blocker_refs: ['b1'], blocked_reason: 'Waiting for approval', updated_at: '2026-06-01T01:00:00Z' }], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [{ qa_ref: 'qa-1', task_id: 't1', status: 'pending', message: 'Needs review', created_at: '2026-06-01T01:02:00Z' }], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: 'dg1', conversation_id: 'conv-mission-1', messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: {
        nodes: [
          { id: 'n1', event_id: 'evt-1', event_type: 'phase_started', label: 'Execution started', status: 'active', occurred_at: '2026-06-01T01:03:00Z' },
          {
            id: 'n2',
            event_id: 'evt-2',
            event_type: 'AcceptanceReportCreated',
            label: 'QA report ready',
            status: 'succeeded',
            details: 'QA acceptance report streamed summary',
            occurred_at: '2026-06-01T01:04:00Z'
          }
        ],
        edges: [],
        current_focus: ['n2']
      },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 2
    } satisfies MissionControlViewDTO
    const view = mapMissionControlView(dto)
    const subview = mapMissionControlViewFromSubviews(dto, {
      team: dto.team,
      taskBoard: dto.task_board,
      evidence: dto.evidence,
      qa: dto.qa,
      discussion: dto.discussion,
      progressGraph: dto.progress_graph
    })

    expect(view.mission.id).toBe('m1')
    expect(view.tasks[0]).toMatchObject({
      id: 't1',
      assigneeName: 'Ada',
      description: 'Draft only inside approved scope',
      boundary: 'Draft only inside approved scope',
      blockedReason: 'Waiting for approval',
      statusTone: 'success',
      acceptanceCriteria: ['Draft is reviewable'],
      deliverables: ['Draft memo'],
      evidenceRequirements: ['Review note'],
      latestResultRef: 'submission-1',
      qaRef: 'qa-1',
      qaMessage: 'Needs review'
    })
    expect(subview.tasks[0]).toMatchObject({ id: 't1', qaRef: 'qa-1' })
    expect(subview.progressFocus).toEqual([
      {
        id: 'n2',
        label: 'QA report ready',
        status: 'succeeded',
        details: 'QA acceptance report streamed summary',
        occurredAt: 'Jun 01, 09:04 AM'
      }
    ])
    expect(view.summary.deliveryPercent).toBe(25)
    expect(view.discussionConversationId).toBe('conv-mission-1')
    expect(subview.discussionConversationId).toBe('conv-mission-1')
  })

  it('maps a restricted canonical Todo discussion without restoring its identity or content', () => {
    const dto = {
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', origin_kind: 'todo', origin_todo_id: 'todo-1', execution_attempt: 2, created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 0, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 0, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: { members: [], assignments: [], staffing_requests: [] },
      task_board: { tasks: [], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: '', restricted: true, messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [], blockers: [], updated_at: '2026-06-01T01:00:00Z', version: 1
    } satisfies MissionControlViewDTO

    const view = mapMissionControlView(dto)

    expect(view.discussionRestricted).toBe(true)
    expect(view.discussionConversationId).toBe('')
    expect(view.events).toEqual([])
    expect(view.mission).toMatchObject({ originTodoId: 'todo-1', executionAttempt: 2 })
  })

  it('maps mission ops projection into an execution workspace view', () => {
    const dto = {
      mission_id: 'm1',
      title: 'Launch',
      status: 'active',
      phase: 'execution',
      current_focus: [
        {
          focus_id: 'focus-qa-1',
          kind: 'approval',
          priority: 'high',
          title: 'Review QA',
          summary: 'Evidence needs review',
          mission_id: 'm1',
          task_id: 't1',
          agent_id: 'profile-builder',
          source_type: 'qa',
          source_id: 'qa-1',
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      agent_lanes: [
        {
          agent_id: 'profile-builder',
          agent_profile_id: 'profile-builder',
          agent_instance_id: 'profile-builder:mission-1',
          agent_kind: 'ai',
          display_name: 'Ada',
          mission_role: 'Builder',
          status: 'running',
          active_task_ids: ['t1'],
          waiting_task_ids: ['t2'],
          inbox_open_count: 2,
          runtime_sessions: [
            {
              run_id: 'run-1',
              agent_id: 'profile-builder',
              task_id: 't1',
              runtime_kind: 'codex',
              queue_id: 'queue-1',
              status: 'running',
              last_event: 'AgentRunStarted',
              last_heartbeat_at: '2026-06-01T01:03:00Z',
              lease_expires_at: '2026-06-01T01:08:00Z',
              is_alive: true,
              is_stale: false,
              lane_health: 'healthy',
              started_at: '2026-06-01T01:02:00Z',
              updated_at: '2026-06-01T01:04:00Z'
            }
          ],
          resource_bindings: [
            { resource_type: 'tool', resource_id: 'tool.deploy', alias: 'deploy-cli', source: 'agent_binding' },
            { resource_type: 'skill', resource_id: 'skill.ops-review', source: 'agent_binding' }
          ],
          next_actions: [
            {
              action_id: 'inspect',
              label: 'Inspect run',
              method: 'GET',
              path: '/api/missions/m1/ops',
              required_permission: 'mission.read'
            }
          ]
        }
      ],
      runtime_sessions: [
        {
          run_id: 'run-1',
          agent_id: 'profile-builder',
          task_id: 't1',
          runtime_kind: 'codex',
          queue_id: 'queue-1',
          status: 'running',
          last_event: 'AgentRunStarted',
          last_heartbeat_at: '2026-06-01T01:03:00Z',
          lease_expires_at: '2026-06-01T01:08:00Z',
          is_alive: true,
          is_stale: false,
          lane_health: 'healthy',
          started_at: '2026-06-01T01:02:00Z',
          updated_at: '2026-06-01T01:04:00Z'
        },
        {
          run_id: 'run-2',
          agent_id: 'profile-builder',
          task_id: 't1',
          runtime_kind: 'codex',
          status: 'failed',
          last_event: 'AgentRunFailed',
          error_summary: 'Tool failed',
          is_alive: false,
          is_stale: false,
          lane_health: 'failed',
          updated_at: '2026-06-01T01:05:00Z'
        }
      ],
      action_cards: [
        {
          action_id: 'action-qa-1',
          kind: 'approve_qa',
          priority: 'high',
          title: 'Approve QA',
          summary: 'Review evidence',
          mission_id: 'm1',
          task_id: 't1',
          agent_id: 'profile-builder',
          source_type: 'qa',
          source_id: 'qa-1',
          status: 'open',
          commands: [
            {
              action_id: 'approve',
              label: 'Approve',
              method: 'POST',
              path: '/api/missions/m1/qa/qa-1/decision',
              required_permission: 'mission.write'
            }
          ],
          created_at: '2026-06-01T01:06:00Z'
        }
      ],
      evidence_gates: [
        {
          task_id: 't1',
          status: 'missing',
          required_count: 2,
          submitted_count: 1,
          missing_requirement_ids: ['evidence-required-2'],
          submitted_evidence_ids: ['evidence-1'],
          last_submitted_at: '2026-06-01T01:07:00Z'
        }
      ],
      approval_gates: [
        {
          approval_id: 'qa:m1:qa-1',
          approval_type: 'qa',
          status: 'pending',
          priority: 'high',
          mission_id: 'm1',
          task_id: 't1',
          source_type: 'qa',
          source_id: 'qa-1',
          title: 'Review QA',
          summary: 'Approve or request rework',
          requested_by: 'profile-builder',
          assigned_to: 'human-local-user',
          evidence_refs: ['evidence-1'],
          commands: [],
          created_at: '2026-06-01T01:08:00Z'
        }
      ],
      autonomy_policy: {
        mode: 'manual',
        auto_start_allowed: false,
        max_risk_without_approval: 'low',
        required_approvals: ['tool_access']
      },
      updated_at: '2026-06-01T01:09:00Z',
      version: 7
    } satisfies MissionOpsViewDTO

    const view = mapMissionOpsView(dto)

    expect(view).toMatchObject({
      missionId: 'm1',
      title: 'Launch',
      phase: 'execution',
      summary: {
        agentCount: 1,
        runningSessions: 1,
        blockedSessions: 1,
        openActions: 1,
        missingEvidence: 1,
        pendingApprovals: 1
      }
    })
    expect(view.currentFocus[0]).toMatchObject({
      id: 'focus-qa-1',
      title: 'Review QA',
      tone: 'warning',
      createdAt: 'Jun 01, 09:00 AM'
    })
    expect(view.agentLanes[0]).toMatchObject({
      agentId: 'profile-builder',
      profileId: 'profile-builder',
      instanceId: 'profile-builder:mission-1',
      displayName: 'Ada',
      status: 'running',
      statusTone: 'info',
      activeTaskIds: ['t1'],
      waitingTaskIds: ['t2'],
      inboxOpenCount: 2,
      resourceBindings: [
        { resourceType: 'tool', resourceId: 'tool.deploy', label: 'deploy-cli' },
        { resourceType: 'skill', resourceId: 'skill.ops-review', label: 'skill.ops-review' }
      ],
      nextActions: [{ actionId: 'inspect', label: 'Inspect run' }]
    })
    expect(view.runtimeSessions[0]).toMatchObject({
      runId: 'run-1',
      status: 'running',
      statusTone: 'info',
      laneHealth: 'healthy',
      canCancel: true,
      canRetry: false,
      updatedAt: 'Jun 01, 09:04 AM'
    })
    expect(view.runtimeSessions[1]).toMatchObject({
      runId: 'run-2',
      status: 'failed',
      statusTone: 'danger',
      errorSummary: 'Tool failed',
      canCancel: false,
      canRetry: true
    })
    expect(view.autonomyPolicy).toEqual({
      mode: 'manual',
      autoStartAllowed: false,
      maxRiskWithoutApproval: 'low',
      requiredApprovals: ['tool_access']
    })
  })

  it('uses backend agent kind and stable profile ids for mission team members', () => {
    const view = mapMissionControlView({
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 1, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 1, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: {
        members: [
          {
            agent_id: 'profile-worker',
            agent_profile_id: 'profile-worker',
            agent_instance_id: 'profile-worker:mission-1',
            agent_kind: 'ai',
            display_name: 'Ada',
            real_name: 'Ada',
            role_name: 'Worker',
            mission_role: 'Worker'
          }
        ],
        assignments: [],
        staffing_requests: []
      },
      task_board: { tasks: [{ task_id: 't1', assignee: 'profile-worker', goal: 'Draft', status: 'assigned', assignee_type: 'ai', updated_at: '2026-06-01T01:00:00Z' }], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: 'dg1', messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 1
    } as MissionControlViewDTO)

    expect(view.team[0]).toMatchObject({ id: 'profile-worker', type: 'ai', name: 'Ada' })
    expect(view.tasks[0]).toMatchObject({ assigneeId: 'profile-worker', assigneeType: 'ai' })
  })

  it('does not invent QA refs when the backend has not projected one', () => {
    const view = mapMissionControlView({
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 1, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 0, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: { members: [], assignments: [], staffing_requests: [] },
      task_board: { tasks: [{ task_id: 't1', goal: 'Draft', status: 'started', latest_result_ref: '', updated_at: '2026-06-01T01:00:00Z' }], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: 'dg1', messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 1
    } satisfies MissionControlViewDTO)

    expect(view.tasks[0].qaRef).toBe('')
  })

  it('uses translatable sentinels for missing mission workspace metadata', () => {
    const view = mapMissionControlView({
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 1, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 1, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: { members: [{ agent_id: 'agent-a', display_name: 'Ada', real_name: 'Ada', role_name: 'Lead' }], assignments: [], staffing_requests: [] },
      task_board: { tasks: [{ task_id: 't1', goal: 'Draft', status: 'started', updated_at: '2026-06-01T01:00:00Z' }], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: {
        discussion_group_id: 'dg1',
        messages: [
          {
            message_id: 'msg-1',
            discussion_group_id: 'dg1',
            author_id: 'agent-a',
            body: 'Status update',
            posted_at: '2026-06-01T01:02:00Z'
          }
        ],
        pending_mentions: [],
        decisions: [],
        blockers: []
      },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 1
    } satisfies MissionControlViewDTO)

    expect(view.team[0].status).toBe('Not provided')
    expect(view.events[0].authorType).toBe('Not provided')
    expect(view.tasks[0]).toMatchObject({
      assigneeName: 'Not provided',
      assigneeType: 'Not provided',
      qaStatus: 'Not provided'
    })
  })

  it('does not expose internal agent instance ids as mission team member names', () => {
    const view = mapMissionControlView({
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 0, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 3, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: {
        members: [
          {
            agent_id: 'pm-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'pm-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            real_name: '产品经理',
            role_name: 'PM',
            mission_role: 'PM'
          },
          {
            agent_id: 'human-local-user:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'human-local-user:orrery-527180d9ebfcba8c6c4736171c7303d0',
            real_name: '真人用户',
            role_name: 'RealHumanContributor',
            mission_role: 'RealHumanContributor'
          }
        ],
        assignments: [],
        staffing_requests: []
      },
      task_board: { tasks: [], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: 'dg1', messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 1
    } satisfies MissionControlViewDTO)

    expect(view.team.map((member) => member.name)).toEqual(['产品经理', '真人用户'])
    expect(view.team.map((member) => member.name).join(' ')).not.toContain(':orrery-')
  })

  it('uses configured roles for bootstrapped discussion members when projections only have ids and roles', () => {
    const view = mapMissionControlView({
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 0, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 3, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: {
        members: [
          {
            agent_id: 'lead-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'lead-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            role_name: 'Lead',
            mission_role: 'Lead'
          },
          {
            agent_id: 'staffing-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'staffing-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            role_name: 'Staffing',
            mission_role: 'Staffing'
          },
          {
            agent_id: 'human-local-user:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'human-local-user:orrery-527180d9ebfcba8c6c4736171c7303d0',
            role_name: 'RealHumanContributor',
            mission_role: 'RealHumanContributor'
          }
        ],
        assignments: [],
        staffing_requests: []
      },
      task_board: { tasks: [], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: 'dg1', messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 1
    } satisfies MissionControlViewDTO)

    expect(view.team.map((member) => member.name)).toEqual(['Lead', 'Staffing', '真人用户'])
    expect(view.team.map((member) => member.name).join(' ')).not.toContain(':orrery-')
  })

  it('does not special-case legacy default agent ids over configured roles', () => {
    const view = mapMissionControlView({
      mission: { mission_id: 'm1', title: 'Launch', final_goal: 'Ship', status: 'active', created_at: '2026-06-01T00:00:00Z' },
      summary: { active_task_count: 0, pending_approval_count: 0, risk_count: 0, blocker_count: 0, team_size: 2, delivery_rate: 0, updated_at: '2026-06-01T01:00:00Z' },
      team: {
        members: [
          {
            agent_id: 'pm-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'pm-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            role_name: 'Lead',
            mission_role: 'Lead'
          },
          {
            agent_id: 'review-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            display_name: 'review-agent:orrery-527180d9ebfcba8c6c4736171c7303d0',
            role_name: 'Staffing',
            mission_role: 'Staffing'
          }
        ],
        assignments: [],
        staffing_requests: []
      },
      task_board: { tasks: [], dependencies: [] },
      evidence: { evidence_items: [], missing_requirements: [] },
      qa: { checks: [], approvals: [], rejections: [], rework_requests: [] },
      discussion: { discussion_group_id: 'dg1', messages: [], pending_mentions: [], decisions: [], blockers: [] },
      inbox_summary: { open_count: 0, items: [] },
      progress_graph: { nodes: [], edges: [], current_focus: [] },
      latest_decisions: [],
      blockers: [],
      updated_at: '2026-06-01T01:00:00Z',
      version: 1
    } satisfies MissionControlViewDTO)

    expect(view.team.map((member) => member.name)).toEqual(['Lead', 'Staffing'])
  })

  it('maps mission workspace form values into backend command requests', () => {
    expect(
      mapMissionCreateRequest({
        title: ' Launch ',
        finalGoal: ' Ship safely ',
        businessDirection: ' Growth '
      })
    ).toEqual({
      title: 'Launch',
      final_goal: 'Ship safely',
      business_direction: 'Growth'
    })

    expect(
      mapMissionTaskCreateRequest({
        goal: ' Draft rollout ',
        assigneeId: ' agent-a ',
        scope: '',
        priority: 'high',
        dueAt: '2026-06-03T09:30:00Z',
        expectedDeliverables: 'Launch brief, Rollout checklist'
      })
    ).toEqual({
      goal: 'Draft rollout',
      assignee_id: 'agent-a',
      worker_personal_goal: 'Draft rollout',
      scope: undefined,
      priority: 'high',
      due_at: '2026-06-03T09:30:00Z',
      expected_deliverables: [
        { title: 'Launch brief' },
        { title: 'Rollout checklist' }
      ]
    })

    expect(
      mapMissionTaskAssignmentRequest({
        taskId: 'task-1',
        assigneeId: ' human-1 ',
        reason: ' needs owner '
      })
    ).toEqual({
      taskId: 'task-1',
      body: {
        assignee_id: 'human-1',
        reason: 'needs owner'
      }
    })

    expect(
      mapMissionTaskResultRequest({
        taskId: 'task-1',
        agentId: ' agent-a ',
        runId: '',
        summary: ' Completed '
      })
    ).toMatchObject({
      taskId: 'task-1',
      body: {
        agent_result_id: 'task-1-manual-result',
        agent_id: 'agent-a',
        run_id: 'manual',
        summary: 'Completed',
        created_at: expect.any(String)
      }
    })

    expect(
      mapMissionQaDecisionRequest({
        qaId: 'qa-1',
        taskId: 'task-1',
        decision: 'approved',
        message: ' looks good '
      })
    ).toEqual({
      qaId: 'qa-1',
      body: {
        task_id: 'task-1',
        decision: 'approved',
        message: 'looks good'
      }
    })
  })

  it('maps discussion form values into backend command requests outside the page layer', () => {
    expect(
      mapMissionDiscussionMessageRequest({
        discussionGroupId: 'dg1',
        content: '  Ship it.  '
      })
    ).toEqual({
      discussion_group_id: 'dg1',
      content: 'Ship it.',
      summary: 'Ship it.',
      message_kind: 'request',
      visibility: 'mission'
    })

    expect(
      mapMissionDiscussionMessageRequest({
        discussionGroupId: 'dg1',
        content: '  @Ada please review.  '
      }).message_kind
    ).toBe('request')

    expect(
      mapMissionDiscussionReplyRequest({
        messageId: 'msg-1',
        discussionGroupId: 'dg1',
        content: '  Agree.  ',
        inboxItemId: 'inbox-1'
      })
    ).toEqual({
      messageId: 'msg-1',
      body: {
        discussion_group_id: 'dg1',
        content: 'Agree.',
        summary: 'Agree.',
        inbox_item_id: 'inbox-1'
      }
    })
  })
})
