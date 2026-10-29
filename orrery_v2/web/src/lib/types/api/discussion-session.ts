export interface DiscussionSessionPageDTO {
  discussions: DiscussionSessionSummaryDTO[];
}

export interface DiscussionSessionSummaryDTO {
  discussion_id: string;
  title: string;
  status: string;
  last_message?: string;
  linked_missions: number;
  attachment_count?: number;
  updated_at: string;
  viewed_at?: string;
  unread_completed: boolean;
}

export interface DiscussionSessionDTO {
  discussion_id: string;
  title: string;
  status: string;
  created_by: string;
  created_by_name?: string;
  created_at: string;
  updated_at: string;
  viewed_at?: string;
  unread_completed: boolean;
  messages?: DiscussionSessionMessageDTO[];
  runs?: DiscussionSessionRunDTO[];
  events?: DiscussionSessionEventDTO[];
  linked_missions?: DiscussionSessionLinkedMissionDTO[];
  attachments?: DiscussionSessionAttachmentDTO[];
}

export interface DiscussionSessionAttachmentPageDTO {
  attachments: DiscussionSessionAttachmentDTO[];
}

export interface DiscussionSessionAttachmentDTO {
  attachment_id: string;
  discussion_id: string;
  file_name: string;
  content_type?: string;
  size_bytes: number;
  uploaded_by: string;
  uploaded_by_kind?: string;
  uploaded_by_name?: string;
  download_url: string;
  preview_url?: string;
  created_at: string;
}

export interface DiscussionSessionMessageDTO {
  message_id: string;
  discussion_id: string;
  role: string;
  author_id: string;
  author_name?: string;
  content: string;
  metadata?: Record<string, string>;
  created_at: string;
}

export interface DiscussionSessionRunDTO {
  run_id: string;
  discussion_id: string;
  agent_id: string;
  status: string;
  runtime_kind: string;
  session_id?: string;
  tool_calls?: DiscussionSessionToolCallDTO[];
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface DiscussionSessionToolCallDTO {
  tool_call_id: string;
  name: string;
  status: string;
  arguments?: Record<string, string>;
  result?: Record<string, string>;
  created_at?: string;
}

export interface DiscussionSessionPlanDTO {
  plan_id: string;
  status: string;
  items: DiscussionSessionPlanItemDTO[];
}

export interface DiscussionSessionPlanItemDTO {
  id: string;
  text: string;
  status: string;
}

export interface DiscussionSessionEventDTO {
  event_id: string;
  discussion_id: string;
  kind: string;
  status?: string;
  summary?: string;
  metadata?: Record<string, string>;
  created_at: string;
}

export interface DiscussionSessionLinkedMissionDTO {
  mission_id: string;
  title: string;
  status: string;
  created_at: string;
}

export interface DiscussionSessionMessageRequest {
  content: string;
}

export interface DiscussionSessionStreamEventDTO {
  kind: string;
  discussion?: DiscussionSessionDTO;
  run?: DiscussionSessionRunDTO;
  message?: DiscussionSessionMessageDTO;
  tool_call?: DiscussionSessionToolCallDTO;
  plan?: DiscussionSessionPlanDTO;
  delta?: string;
  run_id?: string;
  sequence?: number;
  error?: string;
}
