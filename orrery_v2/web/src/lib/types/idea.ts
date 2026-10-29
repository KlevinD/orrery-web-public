import type {
  AssigneeKind,
  CreateIssueCommentRequest,
  TodoItemDTO,
  TodoPriority
} from './todo'

export type IdeaStatus =
  | 'draft'
  | 'submitted'
  | 'under_review'
  | 'accepted'
  | 'experiment'
  | 'converted'
  | 'rejected'
  | 'archived'

export type IdeaVisibility = 'organization' | 'department' | 'private'
export type IdeaImpact = 'low' | 'medium' | 'high'
export type IdeaEffort = 'small' | 'medium' | 'large'

export interface IdeaItemDTO {
  tenant_id?: string
  organization_id?: string
  department_id?: string
  idea_id: string
  issue_number?: string
  title: string
  content?: string
  status: IdeaStatus
  visibility: IdeaVisibility
  impact: IdeaImpact
  effort: IdeaEffort
  proposer_name?: string
  tags?: string[]
  owner_id?: string
  owner_name?: string
  reviewer_id?: string
  reviewer_name?: string
  decision_reason?: string
  vote_count: number
  viewer_has_voted: boolean
  created_by: string
  created_at: string
  updated_at: string
  submitted_at?: string
  reviewed_at?: string
  archived_at?: string
  converted_at?: string
  converted_todo_id?: string
  version: number
}

export interface IdeaPageDTO {
  ideas: IdeaItemDTO[]
  total: number
  limit?: number
  offset?: number
  has_more?: boolean
}

export interface CreateIdeaRequest {
  title: string
  content?: string
  status?: IdeaStatus
  visibility?: IdeaVisibility
  organization_id?: string
  department_id?: string
  owner_id?: string
  owner_name?: string
  impact?: IdeaImpact
  effort?: IdeaEffort
  tags?: string[]
}

export interface UpdateIdeaRequest {
  title?: string
  content?: string
  visibility?: IdeaVisibility
  organization_id?: string
  department_id?: string
  owner_id?: string
  owner_name?: string
  reviewer_id?: string
  reviewer_name?: string
  impact?: IdeaImpact
  effort?: IdeaEffort
  decision_reason?: string
  tags?: string[]
  version?: number
}

export interface TransitionIdeaRequest {
  status: IdeaStatus
  decision_reason?: string
  reviewer_id?: string
  reviewer_name?: string
  version?: number
}

export interface ConvertIdeaToTodoRequest {
  todo_title?: string
  todo_description?: string
  priority?: TodoPriority
  organization_id?: string
  department_id?: string
  project_id?: string
  assignee_id?: string
  assignee_name?: string
  assignee_kind?: AssigneeKind
  reviewer_id?: string
  reviewer_name?: string
  due_at?: string
  acceptance_criteria?: string
  decision_reason?: string
  version?: number
}

export interface IdeaConversionDTO {
  idea: IdeaItemDTO
  todo: TodoItemDTO
}

export type { CreateIssueCommentRequest }
