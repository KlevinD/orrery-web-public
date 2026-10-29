import type { ApprovalItemViewModel } from '#lib/types/view-models'
import type { ApprovalActionDTO } from '#lib/types/api'

export type DiscussionHILMode = 'auto' | 'manual' | 'none'

export interface DiscussionPageDTO {
  discussions: DiscussionSummaryDTO[]
  page?: DiscussionPageMetaDTO
}

export interface DiscussionPageMetaDTO {
  page: number
  page_size: number
  total: number
}

export interface DiscussionSummaryDTO {
  discussion_id: string
  revision?: number
  conversation_kind?: string
  personal_assistant_runtime?: boolean
  direct_agent_id?: string
  direct_target_kind?: string
  direct_target_id?: string
  member_additions_allowed?: boolean
  workspace_root_dir?: string
  hil_mode?: DiscussionHILMode
  title: string
  status: string
  last_message?: string
  linked_missions: number
  attachment_count?: number
  updated_at: string
  viewed_at?: string
  unread_completed: boolean
}

export interface DiscussionDTO {
  discussion_id: string
  revision?: number
  conversation_kind?: string
  personal_assistant_runtime?: boolean
  direct_agent_id?: string
  direct_target_kind?: string
  direct_target_id?: string
  member_additions_allowed?: boolean
  workspace_root_dir?: string
  hil_mode?: DiscussionHILMode
  title: string
  status: string
  created_by: string
  created_by_name?: string
  created_at: string
  updated_at: string
  viewed_at?: string
  unread_completed: boolean
  messages?: DiscussionMessageDTO[]
  message_page?: DiscussionMessagePageMetaDTO
  members?: DiscussionMemberDTO[]
  turns?: DiscussionTurnDTO[]
  runs?: DiscussionRunDTO[]
  events?: DiscussionEventDTO[]
  linked_missions?: DiscussionLinkedMissionDTO[]
  attachments?: DiscussionAttachmentDTO[]
}

export interface DiscussionHILModeUpdateDTO {
  discussion_id: string
  revision: number
  hil_mode: DiscussionHILMode
  updated_at: string
}

export interface DiscussionTurnDTO {
  turn_id: string
  discussion_id: string
  root_message_id: string
  mode: string
  status: string
  error_code?: string
  error_message?: string
  run_ids?: string[]
  started_at?: string
  completed_at?: string
  created_at: string
  updated_at: string
}

export interface DiscussionAttachmentPageDTO {
  attachments: DiscussionAttachmentDTO[]
}

export interface DiscussionAttachmentDraftDTO {
  discussion: DiscussionDTO
  attachment: DiscussionAttachmentDTO
}

export interface DiscussionAttachmentDTO {
  attachment_id: string
  discussion_id: string
  message_id?: string
  file_name: string
  content_type?: string
  size_bytes: number
  uploaded_by: string
  uploaded_by_kind?: string
  uploaded_by_name?: string
  download_url: string
  preview_url?: string
  created_at: string
}

export interface DiscussionMessagePageDTO {
  messages: DiscussionMessageDTO[]
  page: DiscussionMessagePageMetaDTO
}

export interface DiscussionMessagePageMetaDTO {
  limit: number
  offset: number
  total: number
  returned: number
  has_more: boolean
}

export interface DiscussionMemberDTO {
  kind: string
  id: string
  display_name: string
  role?: string
}

export interface DiscussionMessageDTO {
  message_id: string
  discussion_id: string
  turn_id?: string
  run_id?: string
  role: string
  author_id: string
  author_name?: string
  is_current_viewer?: boolean
  content: string
  mentions?: DiscussionMessageMentionDTO[]
  attachments?: DiscussionAttachmentDTO[]
  metadata?: Record<string, string>
  created_at: string
}

export interface DiscussionMessageMentionDTO {
  target_kind: 'agent' | 'user'
  target_id: string
  display_name?: string
}

export interface DiscussionRetryRequestDTO {
  request_id: string
  discussion_id: string
  message_id: string
  status: string
  requested_by: string
  approved_by?: string
  requires_approval: boolean
  created_at: string
  approved_at?: string
}

export interface DiscussionRunDTO {
  run_id: string
  discussion_id: string
  turn_id?: string
  mode?: string
  has_plan?: boolean
  plan?: DiscussionPlanDTO
  agent_id: string
  logical_agent_id?: string
  agent_name?: string
  status: string
  runtime_kind: string
  session_id?: string
  thinking_summary?: string
  tool_calls?: DiscussionToolCallDTO[]
  usage?: Record<string, DiscussionModelUsageDTO>
  duration_ms?: number
  started_at?: string
  completed_at?: string
  created_at: string
}

export interface DiscussionModelUsageDTO {
  cached_input_tokens?: number
  reasoning_tokens?: number
  cached_input_tokens_known?: boolean
  reasoning_tokens_known?: boolean
  input_tokens?: number
  output_tokens?: number
  cache_read_tokens?: number
  cache_write_tokens?: number
  total_tokens?: number
}

export interface DiscussionToolCallDTO {
  tool_call_id: string
  name: string
  status: string
  arguments?: Record<string, string>
  result?: Record<string, string>
  created_at?: string
}

export interface DiscussionEventDTO {
  event_id: string
  discussion_id: string
  kind: string
  status?: string
  summary?: string
  metadata?: Record<string, string>
  available_actions?: ApprovalActionDTO[]
  created_at: string
}

export interface DiscussionLinkedMissionDTO {
  mission_id: string
  title: string
  status: string
  created_at: string
}

export interface DiscussionMessageRequest {
  content: string
  conversation_kind?: string
  force_new_conversation?: boolean
  direct_agent_id?: string
  direct_target_kind?: string
  direct_target_id?: string
  expected_turn_id?: string
  expected_run_id?: string
  attachment_ids?: string[]
  mentions?: DiscussionMessageMentionDTO[]
}

export interface DiscussionStreamEventDTO {
  kind: string
  event_id?: string
  turn_id?: string
  discussion_id?: string
  revision?: number
  discussion?: DiscussionDTO
  run?: DiscussionRunDTO
  message?: DiscussionMessageDTO
  tool_call?: DiscussionToolCallDTO
  approval?: DiscussionEventDTO
  plan?: DiscussionPlanDTO
  delta?: string
  run_id?: string
  sequence?: number
  error?: string
  hil_mode?: DiscussionHILMode
  updated_at?: string
}

export interface DiscussionPlanDTO {
  plan_id?: string
  planId?: string
  status?: string
  items?: DiscussionPlanItemDTO[]
}

export interface DiscussionPlanItemDTO {
  id?: string
  text?: string
  status?: string
}

export interface DiscussionRunDebugSelectedDTO {
  agent_id: string
  runtime_kind: string
  model?: string
  config_ref?: string
}

export interface DiscussionRunDebugDTO {
  discussion_id: string
  run_id: string
  outbound_prompt: string
  runtime_response: string
  provider_metadata?: Record<string, string>
  selected: DiscussionRunDebugSelectedDTO
  created_at: string
  redacted: boolean
}

export interface DiscussionListViewModel {
  discussions: DiscussionSummaryViewModel[]
  page: DiscussionPageViewModel
}

export interface DiscussionPageViewModel {
  page: number
  pageSize: number
  total: number
  hasPrevious: boolean
  hasNext: boolean
}

export interface DiscussionSummaryViewModel {
  id: string
  kind: string
  directAgentId: string
  directTargetKind?: string
  directTargetId?: string
  title: string
  status: string
  lastMessage: string
  linkedMissionCount: number
  attachmentCount?: number
  updatedAt: string
  unreadCompleted: boolean
  workspaceRootDir?: string
  hilMode?: DiscussionHILMode
  personalAssistantRuntime?: boolean
}

export interface DiscussionViewModel {
  id: string
  revision?: number
  kind: string
  directAgentId: string
  directAgentName: string
  directTargetKind?: string
  directTargetId?: string
  directTargetName?: string
  title: string
  status: string
  createdBy: string
  createdById: string
  runtimeStatus: string
  runtimeError?: string
  activeResponders: string[]
  updatedAt: string
  workspaceRootDir?: string
  hilMode?: DiscussionHILMode
  personalAssistantRuntime?: boolean
  messages: DiscussionMessageViewModel[]
  messagePage?: DiscussionMessagePageViewModel
  members: DiscussionMemberViewModel[]
  runs: DiscussionRunViewModel[]
  timeline: DiscussionTimelineItemViewModel[]
  linkedMissions: DiscussionLinkedMissionViewModel[]
  attachments?: DiscussionAttachmentViewModel[]
  streamState?: DiscussionStreamStateViewModel
}

export interface DiscussionStreamStateViewModel {
  seenEventIds: string[]
  tokenSequenceByRun: Record<string, number>
}

export interface DiscussionAttachmentViewModel {
  id: string
  discussionId: string
  messageId: string
  fileName: string
  contentType: string
  sizeBytes: number
  uploadedBy: string
  uploadedByKind: string
  uploadedByName: string
  storagePath: string
  downloadUrl: string
  previewUrl?: string
  createdAt: string
}

export interface DiscussionMessagePageViewModel {
  limit: number
  offset: number
  total: number
  returned: number
  hasMore: boolean
}

export interface DiscussionMemberViewModel {
  kind: string
  id: string
  name: string
  role: string
}

export interface DiscussionMessageViewModel {
  id: string
  turnId?: string
  runId?: string
  role: string
  authorId: string
  authorName: string
  isCurrentViewer?: boolean
  content: string
  orchestrationWarning?: string
  delivery?: string
  humanTodoId?: string
  createdAt: string
  sortAt?: string
  displayTimeline?: DiscussionDisplayTimelineSegmentViewModel[]
  attachments?: DiscussionAttachmentViewModel[]
  acknowledgements?: DiscussionAcknowledgementViewModel[]
  canRequestRetry?: boolean
  canApproveRetry?: boolean
  retryPending?: boolean
}

export interface DiscussionDisplayTimelineSegmentViewModel {
  kind: 'text' | 'tool' | 'plan'
  text?: string
  runId?: string
  toolCallId?: string
  plan?: DiscussionPlanViewModel
}

export interface DiscussionPlanViewModel {
  id: string
  status: string
  items: DiscussionPlanItemViewModel[]
}

export interface DiscussionPlanItemViewModel {
  id: string
  text: string
  status: string
}

export interface DiscussionAcknowledgementViewModel {
  id: string
  agentId: string
  agentName: string
  emoji: string
  createdAt: string
}

export interface DiscussionRunViewModel {
  id: string
  turnId?: string
  mode?: string
  hasPlan?: boolean
  agentId: string
  logicalAgentId?: string
  agentName: string
  status: string
  runtimeKind: string
  error?: string
  toolCalls: DiscussionToolCallViewModel[]
  planId?: string
  planStatus?: string
  planSteps?: DiscussionPlanItemViewModel[]
  thinking?: string
  preview?: string
  usage?: Record<string, DiscussionModelUsageDTO>
  durationMs?: number
  createdAt: string
  sortAt?: string
}

export interface DiscussionToolCallViewModel {
  id: string
  name: string
  status: string
  arguments?: Record<string, string>
  result?: Record<string, string>
  createdAt: string
  sortAt?: string
}

export interface DiscussionTimelineItemViewModel {
  id: string
  kind: 'message' | 'run' | 'approval'
  sortAt: string
  displaySegment?: boolean
  message?: DiscussionMessageViewModel
  run?: DiscussionRunViewModel
  approval?: ApprovalItemViewModel
}

export interface DiscussionRunDebugSelectedViewModel {
  agentId: string
  runtimeKind: string
  model: string
  configRef: string
}

export interface DiscussionRunDebugViewModel {
  discussionId: string
  runId: string
  outboundPrompt: string
  runtimeResponse: string
  providerMetadata: Record<string, string>
  selected: DiscussionRunDebugSelectedViewModel
  createdAt: string
  redacted: boolean
}

export interface DiscussionLinkedMissionViewModel {
  id: string
  title: string
  status: string
  href: string
}

export interface DiscussionMessageFormViewModel {
  content: string
  conversationKind?: string
  forceNewConversation?: boolean
  directAgentId?: string
  directTargetKind?: string
  directTargetId?: string
  expectedTurnId?: string
  expectedRunId?: string
  attachmentIds?: string[]
  attachments?: DiscussionAttachmentViewModel[]
  mentions?: DiscussionMessageMentionDTO[]
}
