import type { BadgeTone } from './common';

export interface MissionListItemViewModel {
  id: string;
  title: string;
  description: string;
  status: string;
  statusTone: BadgeTone;
  phase: string;
  riskLevel: string;
  riskTone: BadgeTone;
  teamSize: number;
  activeTaskCount: number;
  activeRunCount: number;
  pendingApprovalCount: number;
  blockerCount: number;
  deliveryPercent: number;
  deadline: string;
  goals: string[];
  createdBy: string;
  updatedAt: string;
  updatedAtRaw?: string;
  originKind: string;
  originId: string;
  originTodoId: string;
  executionAttempt: number;
  discussionMode: string;
  conversationId: string;
  originDevelopmentProjectId: string;
}

export interface MissionListViewModel {
  summary: {
    total: number;
    active: number;
    pendingApprovals: number;
    blockers: number;
  };
  missions: MissionListItemViewModel[];
}

export interface MissionCreateFormViewModel {
  title: string;
  finalGoal: string;
  businessDirection: string;
  sourcePersonalAssistantConversationId?: string;
}

export interface MissionTaskViewModel {
  id: string;
  title: string;
  description: string;
  goal: string;
  boundary: string;
  assigneeId: string;
  assigneeName: string;
  assigneeType: string;
  status: string;
  statusTone: BadgeTone;
  priority: string;
  dueAt: string;
  acceptanceCriteria: string[];
  deliverables: string[];
  evidenceRequirements: string[];
  latestResultRef: string;
  qaRef: string;
  qaMessage: string;
  qaStatus: string;
  blockerRefs: string[];
  blockedReason: string;
  updatedAt: string;
}

export interface MissionTaskFormViewModel {
  goal: string;
  assigneeId: string;
  scope: string;
  priority: string;
  dueAt: string;
  expectedDeliverables: string;
}

export interface MissionTaskAssignmentFormViewModel {
  taskId: string;
  assigneeId: string;
  reason: string;
}

export interface MissionTaskResultFormViewModel {
  taskId: string;
  agentId: string;
  runId: string;
  summary: string;
}

export interface MissionQaDecisionFormViewModel {
  qaId: string;
  taskId: string;
  decision: "approved" | "rejected";
  message: string;
}

export interface MissionTeamMemberViewModel {
  id: string;
  profileId?: string;
  name: string;
  role: string;
  type: string;
  department: string;
  position: string;
  status: string;
}

export interface MissionProgressFocusViewModel {
  id: string;
  label: string;
  status: string;
  details: string;
  occurredAt: string;
}

export interface MissionControlViewModel {
  mission: MissionListItemViewModel;
  summary: {
    activeTasks: number;
    pendingApprovals: number;
    risks: number;
    blockers: number;
    teamSize: number;
    deliveryPercent: number;
    version: number;
  };
  team: MissionTeamMemberViewModel[];
  tasks: MissionTaskViewModel[];
  events: DiscussionMessageViewModel[];
  progressFocus: MissionProgressFocusViewModel[];
  evidenceCount: number;
  qaCount: number;
  blockers: Array<{ id: string; taskId: string; summary: string }>;
  discussionGroupId: string;
  discussionConversationId: string;
  discussionRestricted: boolean;
}

export interface DiscussionMessageViewModel {
  id: string;
  groupId: string;
  kind: string;
  authorId: string;
  authorName: string;
  authorType: string;
  body: string;
  mentions: string[];
  replyToMessageId: string;
  replyToAuthor: string;
  replyPreview: string;
  toolName: string;
  oldStatus: string;
  newStatus: string;
  postedAt: string;
}

export interface DiscussionViewModel {
  groupId: string;
  messages: DiscussionMessageViewModel[];
  pendingMentions: number;
  decisions: number;
  blockers: number;
}

export interface DiscussionMessageFormViewModel {
  discussionGroupId: string;
  content: string;
}

export interface DiscussionReplyFormViewModel extends DiscussionMessageFormViewModel {
  messageId: string;
  inboxItemId?: string;
}

export interface MissionOpsViewModel {
  missionId: string;
  title: string;
  status: string;
  statusTone: BadgeTone;
  phase: string;
  currentFocus: OpsFocusViewModel[];
  agentLanes: AgentLaneViewModel[];
  runtimeSessions: RuntimeSessionViewModel[];
  actionCards: ActionCardViewModel[];
  evidenceGates: EvidenceGateViewModel[];
  approvalGates: ApprovalGateViewModel[];
  autonomyPolicy: AutonomyPolicyViewModel;
  summary: MissionOpsSummaryViewModel;
  updatedAt: string;
  version: number;
}

export interface MissionOpsSummaryViewModel {
  agentCount: number;
  runningSessions: number;
  blockedSessions: number;
  openActions: number;
  missingEvidence: number;
  pendingApprovals: number;
}

export interface OpsFocusViewModel {
  id: string;
  kind: string;
  priority: string;
  tone: BadgeTone;
  title: string;
  summary: string;
  missionId: string;
  taskId: string;
  agentId: string;
  sourceType: string;
  sourceId: string;
  createdAt: string;
}

export interface AgentLaneViewModel {
  agentId: string;
  profileId: string;
  instanceId: string;
  agentKind: string;
  displayName: string;
  missionRole: string;
  status: string;
  statusTone: BadgeTone;
  activeTaskIds: string[];
  waitingTaskIds: string[];
  inboxOpenCount: number;
  runtimeSessions: RuntimeSessionViewModel[];
  resourceBindings: ResourceRefViewModel[];
  nextActions: CommandLinkViewModel[];
}

export interface RuntimeSessionViewModel {
  runId: string;
  agentId: string;
  taskId: string;
  runtimeKind: string;
  queueId: string;
  status: string;
  statusTone: BadgeTone;
  lastEvent: string;
  errorSummary: string;
  lastHeartbeatAt: string;
  leaseExpiresAt: string;
  isAlive: boolean;
  isStale: boolean;
  laneHealth: string;
  startedAt: string;
  updatedAt: string;
  canCancel: boolean;
  canRetry: boolean;
}

export interface ResourceRefViewModel {
  resourceType: string;
  resourceId: string;
  label: string;
  source: string;
}

export interface ActionCardViewModel {
  actionId: string;
  kind: string;
  priority: string;
  tone: BadgeTone;
  title: string;
  summary: string;
  missionId: string;
  taskId: string;
  agentId: string;
  sourceType: string;
  sourceId: string;
  status: string;
  commands: CommandLinkViewModel[];
  createdAt: string;
}

export interface CommandLinkViewModel {
  actionId: string;
  label: string;
  method: string;
  path: string;
  requiredPermission: string;
}

export interface EvidenceGateViewModel {
  taskId: string;
  status: string;
  tone: BadgeTone;
  requiredCount: number;
  submittedCount: number;
  missingRequirementIds: string[];
  submittedEvidenceIds: string[];
  lastSubmittedAt: string;
}

export interface ApprovalGateViewModel {
  approvalId: string;
  approvalType: string;
  status: string;
  tone: BadgeTone;
  priority: string;
  missionId: string;
  taskId: string;
  sourceType: string;
  sourceId: string;
  title: string;
  summary: string;
  requestedBy: string;
  assignedTo: string;
  evidenceRefs: string[];
  commands: CommandLinkViewModel[];
  createdAt: string;
  decidedAt: string;
}

export interface AutonomyPolicyViewModel {
  mode: string;
  autoStartAllowed: boolean;
  maxRiskWithoutApproval: string;
  requiredApprovals: string[];
}
