import { render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import MissionRoom from './MissionRoom.svelte'

const cancelMutation = { isPending: false, mutate: vi.fn() }
const retryMutation = { isPending: false, mutate: vi.fn() }

vi.mock('$app/navigation', () => ({
  goto: vi.fn()
}))

vi.mock('#lib/queries/missions', () => ({
  missionQueryKeys: {
    control: (missionId: string) => ['missions', missionId, 'control'],
    discussion: (missionId: string) => ['missions', missionId, 'discussion'],
    ops: (missionId: string) => ['missions', missionId, 'ops']
  },
  subscribeMissionStream: vi.fn(() => () => {}),
  useAgentRunCancelMutation: vi.fn(() => cancelMutation),
  useAgentRunRetryMutation: vi.fn(() => retryMutation),
  useMissionControlQuery: vi.fn(() => ({
    isLoading: false,
    isError: false,
    data: {
      mission: {
        id: 'mission-1',
        title: 'Launch Ops',
        description: 'Ship safely',
        status: 'active',
        statusTone: 'success',
        phase: 'execution',
        riskLevel: 'normal',
        riskTone: 'neutral',
        teamSize: 1,
        activeTaskCount: 1,
        pendingApprovalCount: 0,
        blockerCount: 0,
        deliveryPercent: 0,
        deadline: 'Not provided',
        goals: ['Ship safely'],
        createdBy: 'backend',
        updatedAt: 'Jun 01'
      },
      summary: {
        activeTasks: 1,
        pendingApprovals: 0,
        risks: 0,
        blockers: 0,
        teamSize: 1,
        deliveryPercent: 0,
        version: 1
      },
      team: [],
      tasks: [],
      events: [],
      progressFocus: [],
      evidenceCount: 0,
      qaCount: 0,
      blockers: [],
      discussionGroupId: 'dg-1'
    }
  })),
  useMissionDiscussionQuery: vi.fn(() => ({
    isLoading: false,
    isError: false,
    data: {
      groupId: 'dg-1',
      messages: [],
      pendingMentions: 0,
      decisions: 0,
      blockers: 0
    }
  })),
  useMissionListQuery: vi.fn(() => ({
    isLoading: false,
    isError: false,
    data: {
      summary: { total: 1, active: 1, pendingApprovals: 0, blockers: 0 },
      missions: [
        {
          id: 'mission-1',
          title: 'Launch Ops',
          description: 'Ship safely',
          status: 'active',
          statusTone: 'success',
          phase: 'execution',
          riskLevel: 'normal',
          riskTone: 'neutral',
          teamSize: 1,
          activeTaskCount: 1,
          pendingApprovalCount: 0,
          blockerCount: 0,
          deliveryPercent: 0,
          deadline: 'Not provided',
          goals: ['Ship safely'],
          createdBy: 'backend',
          updatedAt: 'Jun 01'
        }
      ]
    }
  })),
  useMissionOpsQuery: vi.fn(() => ({
    isLoading: false,
    isError: false,
    data: {
      missionId: 'mission-1',
      title: 'Launch Ops',
      status: 'active',
      statusTone: 'success',
      phase: 'execution',
      currentFocus: [
        {
          id: 'focus-1',
          kind: 'approval',
          priority: 'high',
          tone: 'warning',
          title: 'Review evidence',
          summary: 'Human approval needed',
          missionId: 'mission-1',
          taskId: 'task-1',
          agentId: 'profile-builder',
          sourceType: 'qa',
          sourceId: 'qa-1',
          createdAt: 'Jun 01'
        },
        ...[2, 3, 4].map((id) => ({ id: `focus-${id}`, kind: 'approval', priority: 'high', tone: 'warning', title: `Approval ${id}`, summary: 'Review pending', missionId: 'mission-1', taskId: 'task-1', agentId: 'profile-builder', sourceType: 'qa', sourceId: `qa-${id}`, createdAt: 'Jun 01' })),
        { id: 'advice-1', kind: 'recovery_advice', priority: 'normal', tone: 'neutral', title: '核查依赖和下游状态', summary: '确认结果后再决定是否重试', missionId: 'mission-1', taskId: 'task-1', agentId: 'profile-builder', sourceType: 'rsi_observation', sourceId: 'observation-1', createdAt: 'Jun 01' }
      ],
      agentLanes: [
        {
          agentId: 'profile-builder',
          profileId: 'profile-builder',
          instanceId: 'profile-builder:mission-1',
          agentKind: 'ai',
          displayName: 'Ada',
          missionRole: 'Builder',
          status: 'running',
          statusTone: 'info',
          activeTaskIds: ['task-1'],
          waitingTaskIds: [],
          inboxOpenCount: 0,
          runtimeSessions: [],
          resourceBindings: [{ resourceType: 'tool', resourceId: 'tool.deploy', label: 'tool.deploy', source: 'agent_binding' }],
          nextActions: []
        }
      ],
      runtimeSessions: [
        {
          runId: 'run-1',
          agentId: 'profile-builder',
          taskId: 'task-1',
          runtimeKind: 'codex',
          queueId: 'queue-1',
          status: 'running',
          statusTone: 'info',
          lastEvent: 'AgentRunStarted',
          errorSummary: '',
          lastHeartbeatAt: 'Jun 01',
          leaseExpiresAt: 'Jun 01',
          isAlive: true,
          isStale: false,
          laneHealth: 'healthy',
          startedAt: 'Jun 01',
          updatedAt: 'Jun 01',
          canCancel: true,
          canRetry: false
        },
        {
          runId: 'run-2',
          agentId: 'profile-builder',
          taskId: 'task-1',
          runtimeKind: 'codex',
          queueId: '',
          status: 'failed',
          statusTone: 'danger',
          lastEvent: 'AgentRunFailed',
          errorSummary: 'Tool failed',
          lastHeartbeatAt: 'Not provided',
          leaseExpiresAt: 'Not provided',
          isAlive: false,
          isStale: false,
          laneHealth: 'failed',
          startedAt: 'Not provided',
          updatedAt: 'Jun 01',
          canCancel: false,
          canRetry: true
        }
      ],
      actionCards: [],
      evidenceGates: [],
      approvalGates: [],
      autonomyPolicy: {
        mode: 'manual',
        autoStartAllowed: false,
        maxRiskWithoutApproval: 'low',
        requiredApprovals: []
      },
      summary: {
        agentCount: 1,
        runningSessions: 1,
        blockedSessions: 1,
        openActions: 0,
        missingEvidence: 0,
        pendingApprovals: 0
      },
      updatedAt: 'Jun 01',
      version: 1
    }
  }))
}))

describe('MissionRoom', () => {
  beforeEach(() => {
    setLocale('zh-CN')
    cancelMutation.mutate.mockClear()
    retryMutation.mutate.mockClear()
  })

  it('renders the mission ops execution view from backend runtime projection', () => {
    render(MissionRoom, { props: { missionId: 'mission-1' } })

    expect(screen.getByText('执行泳道')).toBeTruthy()
    expect(screen.getByText('运行会话')).toBeTruthy()
    expect(screen.getByText('Ada')).toBeTruthy()
    expect(screen.getAllByText('codex').length).toBeGreaterThan(0)
    expect(screen.getByText('tool.deploy')).toBeTruthy()
    expect(screen.getByText('Approval 4')).toBeTruthy()
    expect(screen.getByRole('region', { name: '失败核查建议' })).toBeTruthy()
    expect(screen.getByText('核查依赖和下游状态')).toBeTruthy()
    expect(screen.getByRole('button', { name: '取消运行 run-1' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '重试运行 run-2' })).toBeTruthy()
  })
})
