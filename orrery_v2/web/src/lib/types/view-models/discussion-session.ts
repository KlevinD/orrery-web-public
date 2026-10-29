export interface DiscussionSessionListViewModel {
  discussions: DiscussionSessionSummaryViewModel[];
}

export interface DiscussionSessionSummaryViewModel {
  id: string;
  title: string;
  status: string;
  lastMessage: string;
  linkedMissionCount: number;
  attachmentCount?: number;
  updatedAt: string;
  unreadCompleted: boolean;
}

export interface DiscussionSessionViewModel {
  id: string;
  title: string;
  status: string;
  createdBy: string;
  runtimeStatus: string;
  runtimeError?: string;
  updatedAt: string;
  messages: DiscussionSessionMessageViewModel[];
  runs: DiscussionSessionRunViewModel[];
  linkedMissions: DiscussionSessionLinkedMissionViewModel[];
  attachments?: DiscussionSessionAttachmentViewModel[];
}

export interface DiscussionSessionAttachmentViewModel {
  id: string;
  discussionId: string;
  fileName: string;
  contentType: string;
  sizeBytes: number;
  uploadedBy: string;
  uploadedByKind: string;
  uploadedByName: string;
  storagePath: string;
  downloadUrl: string;
  previewUrl?: string;
  createdAt: string;
}

export interface DiscussionSessionMessageViewModel {
  id: string;
  role: string;
  authorId: string;
  authorName: string;
  content: string;
  createdAt: string;
}

export interface DiscussionSessionRunViewModel {
  id: string;
  agentId: string;
  status: string;
  runtimeKind: string;
  toolCalls: DiscussionSessionToolCallViewModel[];
  createdAt: string;
}

export interface DiscussionSessionToolCallViewModel {
  id: string;
  name: string;
  status: string;
}

export interface DiscussionSessionLinkedMissionViewModel {
  id: string;
  title: string;
  status: string;
  href: string;
}

export interface DiscussionSessionMessageFormViewModel {
  content: string;
}
