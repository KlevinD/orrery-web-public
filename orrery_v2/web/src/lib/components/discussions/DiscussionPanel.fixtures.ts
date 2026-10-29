import type { DiscussionRunDebugViewModel, DiscussionViewModel } from '#lib/types/discussions'
import type { MissionControlViewModel } from '#lib/types/view-models'

export function discussion(overrides: Partial<DiscussionViewModel> = {}): DiscussionViewModel {
  const base: DiscussionViewModel = {
    id: 'conv-1',
    kind: 'group',
    directAgentId: '',
    directAgentName: '',
    title: 'Launch plan',
    status: 'running',
    createdBy: 'user-1',
    createdById: 'user-1',
    runtimeStatus: 'running',
    activeResponders: [],
    updatedAt: 'Jun 02, 09:00',
    hilMode: 'manual',
    messages: [
      {
        id: 'msg-1',
        role: 'assistant',
        authorId: 'agent-alpha',
        authorName: 'Lead Agent',
        content: 'I created the Mission.',
        createdAt: 'Jun 02, 09:00'
      }
    ],
    members: [],
    runs: [],
    timeline: [],
    linkedMissions: [
      {
        id: 'mission-1',
        title: 'Launch plan',
        status: 'active',
        href: '/missions/mission-1'
      }
    ],
  }
  return { ...base, ...overrides, timeline: overrides.timeline ?? base.timeline }
}

export function missionContext(): MissionControlViewModel {
  return {
    mission: {
      id: 'mission-1',
      title: 'Launch plan',
      description: 'Launch planning mission',
      status: 'active',
      statusTone: 'success',
      phase: 'planning',
      riskLevel: 'medium',
      riskTone: 'warning',
      teamSize: 2,
      activeTaskCount: 2,
      activeRunCount: 1,
      pendingApprovalCount: 0,
      blockerCount: 1,
      deliveryPercent: 25,
      deadline: 'Jun 10',
      goals: ['Publish the enterprise launch plan'],
      createdBy: 'operator',
      updatedAt: 'Jun 02',
      originKind: '',
      originId: '',
      originTodoId: '',
      executionAttempt: 0,
      discussionMode: '',
      conversationId: 'discussion-1',
      originDevelopmentProjectId: ''
    },
    summary: {
      activeTasks: 2,
      pendingApprovals: 0,
      risks: 1,
      blockers: 1,
      teamSize: 2,
      deliveryPercent: 25,
      version: 1
    },
    team: [
      { id: 'agent-1', name: 'Planner', role: 'Launch Lead', type: 'agent', department: 'Ops', position: 'Lead', status: 'active' },
      { id: 'user-1', name: 'Operator', role: 'Owner', type: 'human', department: 'Ops', position: 'Director', status: 'active' }
    ],
    tasks: [
      {
        id: 'task-done',
        title: 'Research launch requirements',
        description: 'Research complete',
        goal: 'Research',
        boundary: 'Launch only',
        assigneeId: 'agent-1',
        assigneeName: 'Planner',
        assigneeType: 'agent',
        status: 'approved',
        statusTone: 'success',
        priority: 'high',
        dueAt: 'Jun 03',
        acceptanceCriteria: [],
        deliverables: [],
        evidenceRequirements: [],
        latestResultRef: '',
        qaRef: '',
        qaMessage: '',
        qaStatus: '',
        blockerRefs: [],
        blockedReason: '',
        updatedAt: 'Jun 01'
      },
      {
        id: 'task-1',
        title: 'Draft launch checklist',
        description: 'Draft checklist',
        goal: 'Checklist',
        boundary: 'Launch only',
        assigneeId: 'agent-1',
        assigneeName: 'Planner',
        assigneeType: 'agent',
        status: 'active',
        statusTone: 'success',
        priority: 'high',
        dueAt: 'Jun 04',
        acceptanceCriteria: [],
        deliverables: [],
        evidenceRequirements: [],
        latestResultRef: '',
        qaRef: '',
        qaMessage: '',
        qaStatus: '',
        blockerRefs: [],
        blockedReason: '',
        updatedAt: 'Jun 02'
      },
      {
        id: 'task-2',
        title: 'Security approval blocked',
        description: 'Approval pending',
        goal: 'Approval',
        boundary: 'Security',
        assigneeId: 'user-1',
        assigneeName: 'Operator',
        assigneeType: 'human',
        status: 'blocked',
        statusTone: 'danger',
        priority: 'high',
        dueAt: 'Jun 04',
        acceptanceCriteria: [],
        deliverables: [],
        evidenceRequirements: [],
        latestResultRef: '',
        qaRef: '',
        qaMessage: '',
        qaStatus: '',
        blockerRefs: ['blocker-1'],
        blockedReason: 'Waiting for security',
        updatedAt: 'Jun 02'
      }
    ],
    events: [],
    progressFocus: [],
    evidenceCount: 0,
    qaCount: 0,
    blockers: [{ id: 'blocker-1', taskId: 'task-2', summary: 'Waiting for security' }],
    discussionGroupId: 'dg-1',
    discussionConversationId: 'conv-1',
    discussionRestricted: false
  }
}

export function debugPrompt(): DiscussionRunDebugViewModel {
  return {
    discussionId: 'conv-1',
    runId: 'run-1',
    outboundPrompt: [
      'system token=secret-system',
      '',
      'Discussion ID: conv-1',
      'Current user request:',
      'Plan launch',
      'available_tools: mission_brief_create'
    ].join('\n'),
    runtimeResponse: [
      'I created the Mission and then stopped.',
      '',
      'tool_use mission_brief_create',
      'call_id: tool-1',
      '{',
      '  "title": "Launch"',
      '}'
    ].join('\n'),
    providerMetadata: {
      pi_debug_runtime_input_path: 'D:\\Orrery\\pi-debug\\run-1\\runtime-input.json',
      pi_debug_provider_requests_path: 'D:\\Orrery\\pi-debug\\run-1\\provider-requests.jsonl'
    },
    selected: {
      agentId: 'agent-alpha',
      runtimeKind: 'codex',
      model: 'gpt-5-codex',
      configRef: 'runtime-ref'
    },
    createdAt: 'Jun 02, 08:01',
    redacted: false
  }
}
