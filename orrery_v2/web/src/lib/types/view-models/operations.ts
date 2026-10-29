import type { BadgeTone } from './common';

export interface ApprovalSummaryViewModel {
  total: number;
  pending: number;
  decided: number;
  highPriority: number;
}

export interface ApprovalActionViewModel {
  id: string;
  label: string;
  style: string;
  decision: string;
  effect: string;
  inputKind: string;
  defaultMessage: string;
  method: string;
  path: string;
  reasonRequired: boolean;
  confirmationRequired: boolean;
  allowsModification: boolean;
  rememberAllowed: boolean;
}

export interface ApprovalPayloadViewModel {
  kind: string;
  language: string;
  title: string;
  content: string;
  fields: Array<{ key: string; value: string }>;
}

export interface BusinessApprovalRiskViewModel {
  description: string;
  mitigation: string;
}

export interface BusinessApprovalMaterialViewModel {
  language: string;
  title: string;
  decisionRequest: string;
  businessContext: string;
  proposedAction: string;
  expectedResult: string;
  impactScope: string;
  risks: BusinessApprovalRiskViewModel[];
}

export interface ApprovalArtifactViewModel {
  label: string;
  url: string;
  kind: string;
}

export interface ApprovalChoiceViewModel {
  id: string;
  label: string;
  description: string;
}

export interface ApprovalItemViewModel {
  id: string;
  type: string;
  displayKind: string;
  displayTone: BadgeTone;
  typeLabel: string;
  status: string;
  statusTone: BadgeTone;
  priority: string;
  priorityTone: BadgeTone;
  missionId: string;
  taskId: string;
  hilDecisionId?: string;
  runtimeRunId?: string;
  runtimeToolCallId?: string;
  runtimeRequestId?: string;
  sourceLabel: string;
  discussionId?: string;
  provider: string;
  toolName: string;
  title: string;
  summary: string;
  details: string;
  riskSummary: string;
  requestedBy: string;
  requestedByName?: string;
  triggeredBy?: string;
  triggeredByName?: string;
  decidedBy?: string;
  decidedByName?: string;
  assignedTo: string;
  assignedToName?: string;
  organizationCode: string;
  organizationName: string;
  requestedByActorLabel: string;
  targetActorLabel: string;
  policyId: string;
  riskLevel: string;
  decisionEffect: string;
  payloadPreview: ApprovalPayloadViewModel;
  choices: ApprovalChoiceViewModel[];
  evidenceRefs: string[];
  createdAt: string;
  createdAtRaw: string;
  expiresAt: string;
  expiresAtRaw: string;
  actions: ApprovalActionViewModel[];
  planDigest?: string;
}

export interface ApprovalDecisionFormViewModel {
  approvalId: string;
  actionPath?: string;
  actionId: string;
  taskId: string;
  message: string;
  modifiedInput?: string;
  choiceId?: string;
  remember?: boolean;
  planDigest?: string;
}

export interface ApprovalCenterViewModel {
  summary: ApprovalSummaryViewModel;
  items: ApprovalItemViewModel[];
}

export interface HILDecisionItemViewModel {
  handled?: boolean;
  id: string;
  councilId: string;
  routeMode: string;
  councilStage: boolean;
  action: string;
  resource: string;
  purpose: string;
  requester: string;
  riskLevel: string;
  riskTone: BadgeTone;
  status: string;
  statusTone: BadgeTone;
  effect: string;
  reasons: string[];
  evidenceRefs: string[];
  contextBundleId: string;
  contextComplete: boolean;
  contextConfidence: number;
  payloadPreview?: Record<string, unknown>;
  createdAt: string;
  createdAtRaw: string;
  expiresAt?: string;
  expiresAtRaw?: string;
}

export interface HILCouncilParticipantViewModel {
  actorKey: string;
  actorId: string;
  actorKind: string;
  displayName: string;
  role: string;
  required: boolean;
  contributed: boolean;
  vote: string;
}

export interface HILCouncilContributionViewModel {
  actorKey: string;
  actorName: string;
  position: string;
  reasoning: string;
  evidenceRefs: string[];
  updatedAt: string;
}

export interface HILCouncilViewModel {
  id: string;
  requestId: string;
  status: string;
  statusTone: BadgeTone;
  riskLevel: string;
  effect: string;
  quorum: number;
  approvalThreshold: number;
  approvals: number;
  rejections: number;
  abstentions: number;
  participants: HILCouncilParticipantViewModel[];
  contributions: HILCouncilContributionViewModel[];
  dissent: string[];
  expiresAt: string;
  resolvedAt: string;
}

export type HILWorkflowView = 'my_tasks' | 'started_by_me' | 'all' | 'templates';
export type HILWorkflowDecision = 'approve' | 'reject' | 'request_changes';

export interface HILWorkflowStageViewModel {
  id: string;
  key: string;
  name: string;
  order: number;
  type: string;
  status: string;
  statusTone: BadgeTone;
  participantLabels: string[];
  deadlineAt: string;
  deadlineAtRaw: string;
  escalationIndex: number;
}

export interface HILWorkflowTaskViewModel {
  id: string;
  stageId: string;
  stageKey: string;
  stageName: string;
  status: string;
  statusTone: BadgeTone;
  assignee: string;
  assigneeId: string;
  originalAssignee: string;
  decision: string;
  reason: string;
  transferCount: number;
  transferReason: string;
  deadlineAt: string;
  deadlineAtRaw: string;
  actionable: boolean;
}

export interface HILWorkflowNotificationViewModel {
  id: string;
  channel: string;
  recipient: string;
  eventType: string;
  status: string;
  statusTone: BadgeTone;
  attemptCount: number;
  providerReceipt: string;
  lastError: string;
  updatedAt: string;
}

export interface HILWorkflowAuditViewModel {
  id: string;
  eventType: string;
  actor: string;
  reason: string;
  createdAt: string;
  metadata: Record<string, unknown>;
}

export interface HILWorkflowReceiptViewModel {
  id: string;
  effect: string;
  argsHash: string;
  materialHash: string;
  resolverActor: string;
  resolverReason: string;
  singleUse: boolean;
  createdAt: string;
}

export interface HILWorkflowInstanceViewModel {
  id: string;
  caseId: string;
  decisionRequestId: string;
  status: string;
  statusTone: BadgeTone;
  revision: number;
  version: number;
  predecessorInstanceId: string;
  title: string;
  summary: string;
  requester: string;
  requesterId: string;
  toolName: string;
  resource: string;
  riskLevel: string;
  currentStageKey: string;
  definitionHash: string;
  materialHash: string;
  argsHash: string;
  material: ApprovalPayloadViewModel;
  isBusinessApproval: boolean;
  materialComplete: boolean;
  businessMaterial?: BusinessApprovalMaterialViewModel;
  evidenceRefs: string[];
  artifacts: ApprovalArtifactViewModel[];
  stages: HILWorkflowStageViewModel[];
  tasks: HILWorkflowTaskViewModel[];
  activeTask?: HILWorkflowTaskViewModel;
  notifications: HILWorkflowNotificationViewModel[];
  audit: HILWorkflowAuditViewModel[];
  receipts: HILWorkflowReceiptViewModel[];
  createdAt: string;
  createdAtRaw: string;
  updatedAt: string;
  expiresAt: string;
  expiresAtRaw: string;
}

export interface AuditSummaryViewModel {
  total: number;
  warning: number;
  error: number;
  succeeded: number;
}

export interface AuditEventViewModel {
  id: string;
  actorId: string;
  missionId: string;
  entity: string;
  action: string;
  result: string;
  severity: string;
  severityTone: BadgeTone;
  correlationId: string;
  summary: string;
  metadataPairs: Array<{ key: string; value: string }>;
  occurredAt: string;
}

export interface AuditLogViewModel {
  summary: AuditSummaryViewModel;
  events: AuditEventViewModel[];
  page: {
    limit: number;
    offset: number;
    total: number;
    returned: number;
    hasMore: boolean;
  };
}

export interface AuthIPLockViewModel {
  ipAddress: string;
  status: string;
  statusTone: BadgeTone;
  failureCount: number;
  lastUsername: string;
  lastReason: string;
  lockedAt: string;
  updatedAt: string;
}

export interface AuthIPLockCenterViewModel {
  summary: {
    active: number;
    tracking: number;
    totalFailures: number;
  };
  locks: AuthIPLockViewModel[];
}

export interface EvaluationItemViewModel {
  id: string;
  entityId: string;
  entityType: string;
  displayName: string;
  missionId: string;
  roleName: string;
  department: string;
  position: string;
  runtimeKind: string;
  assignedTasks: number;
  completedTasks: number;
  reworkCount: number;
  blockerCount: number;
  resourceUsageCount: number;
  discussionReplyCount: number;
  averageDurationSeconds: number;
  tokenConsumed: number;
  metrics: {
    delivery: number;
    quality: number;
    onTime: number;
    collaboration: number;
    efficiency: number;
    stability: number;
  };
  summary: string;
  capabilities: Array<{ name: string; score: number }>;
  investment: {
    tokenConsumed: number;
    estimatedHumanDays: number;
    evaluationTokenConsumed: number;
    returnMultiple: number;
    summary: string;
  };
  latestReview?: {
    reviewId: string;
    entityType: string;
    entityId: string;
    scopeKind: string;
    scopeId: string;
    evaluatorAgentId: string;
    conversationId: string;
    scheduledRunId: string;
    windowDays: number;
    summary: string;
    estimatedHumanDays: number;
    evaluationTokenConsumed: number;
    createdAt: string;
  };
  taskHistory: Array<{
    goal: string;
    responsibility: string;
    type: string;
    result: string;
    quality: number;
    tokenConsumed: number;
    durationSeconds: number;
    risk: string;
    time: string;
  }>;
  trend: Array<{ date: string; tasks: number; score: number; tokenConsumed: number }>;
  representativeCases: Array<{ title: string; result: string; summary: string; date: string }>;
  suggestions: string[];
  advantages: string[];
  risks: string[];
  scenarios: string[];
  lastActivityAt: string;
}

export interface EvaluationCenterViewModel {
  summary: {
    total: number;
    completedTasks: number;
    averageQuality: number;
  };
  items: EvaluationItemViewModel[];
  radarOption: Record<string, any>;
}

export interface PermissionDeclarationViewModel {
  scope: string;
  risk: string;
  riskTone: BadgeTone;
  required: boolean;
  effective: boolean;
  effectiveLabel: string;
  source: string;
}

export interface PermissionResourceViewModel {
  toolId: string;
  toolName: string;
  declarations: PermissionDeclarationViewModel[];
}

export interface PermissionRowViewModel {
  agentId: string;
  resources: PermissionResourceViewModel[];
}

export interface PermissionCenterViewModel {
  summary: {
    agents: number;
    resources: number;
    declarations: number;
    effective: number;
  };
  rows: PermissionRowViewModel[];
  chartOption: Record<string, any>;
}

export interface PermissionPolicyCapabilityViewModel {
  id: string;
  domain: string;
  action: string;
  accessMode: string;
  accessLabelZh: string;
  accessLabelEn: string;
  scope: string;
  method: string;
  path: string;
  labelZh: string;
  labelEn: string;
  displayName: string;
  categoryZh: string;
  categoryEn: string;
  categoryLabel: string;
  description: string;
}

export interface PermissionPolicyRoleViewModel {
  id: string;
  labelZh: string;
  labelEn: string;
  displayName: string;
  builtIn: boolean;
}

export interface PermissionPolicyGrantViewModel {
  subjectKind: string;
  subjectId: string;
  capabilityId: string;
  scope: string;
}

export interface PermissionPolicyRoleCellViewModel {
  roleId: string;
  roleDisplayName: string;
  granted: boolean;
}

export interface PermissionPolicyRoleRowViewModel {
  capability: PermissionPolicyCapabilityViewModel;
  roles: PermissionPolicyRoleCellViewModel[];
}

export interface PermissionPolicyEffectiveViewModel {
  principalKind: string;
  principalId: string;
  principalDisplayName: string;
  capabilityId: string;
  capabilityDisplayName: string;
  scope: string;
  source: string;
  sourceId: string;
  sourceLabel: string;
}

export interface PermissionPolicyCommandViewModel {
  roles: Array<{ role_id: string; label_zh: string; label_en: string; built_in?: boolean }>;
  role_assignments: Array<{
    principal_kind: string;
    principal_id: string;
    role_id: string;
  }>;
  grants: Array<{
    subject_kind: string;
    subject_id: string;
    capability_id?: string;
    scope?: string;
    resource_kind?: string;
    resource_id?: string;
  }>;
}

export interface PermissionPolicyCenterViewModel {
  enforcementMode: string;
  policyMutable: boolean;
  summary: {
    capabilities: number;
    readCapabilities: number;
    writeCapabilities: number;
    roles: number;
    effective: number;
  };
  capabilities: PermissionPolicyCapabilityViewModel[];
  roles: PermissionPolicyRoleViewModel[];
  grants: PermissionPolicyGrantViewModel[];
  rolePermissionRows: PermissionPolicyRoleRowViewModel[];
  effective: PermissionPolicyEffectiveViewModel[];
  command: PermissionPolicyCommandViewModel;
}
