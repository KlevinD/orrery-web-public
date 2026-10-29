import { apiFetch, type ApiFetchOptions } from './client'
import type {
  ClaimTodoRequest,
  CreateIssueCommentRequest,
  CreateTodoRequest,
  DraftSuggestionDTO,
  IssueCommentDTO,
  IssueCommentPageDTO,
  MoveTodoRequest,
  SuggestDraftRequest,
  TodoItemDTO,
  TodoAttachmentDTO,
  TodoAutomationStateDTO,
  TodoExecutionProgressDTO,
  TodoExecutionSummaryDTO,
  TodoPageDTO,
  WorkHubSummaryDTO,
  UpdateTodoRequest
} from '#lib/types/todo'

export const todoApi = {
  deleteAttachment: (todoID: string, attachmentID: string) =>
    apiFetch<void>(`/api/todos/${encodeURIComponent(todoID)}/attachments/${encodeURIComponent(attachmentID)}`, { method: 'DELETE' }),
  uploadAttachment: (todoID: string, file: File) => {
    const body = new FormData()
    body.set('file', file)
    return apiFetch<TodoAttachmentDTO>(`/api/todos/${encodeURIComponent(todoID)}/attachments`, { method: 'POST', body })
  },
  downloadAttachment: (todoID: string, attachmentID: string) =>
    apiFetch<Blob>(`/api/todos/${encodeURIComponent(todoID)}/attachments/${encodeURIComponent(attachmentID)}`, { responseType: 'blob' }),
	getWorkHubSummary: (timeZone = 'UTC', options?: ApiFetchOptions) =>
		apiFetch<WorkHubSummaryDTO>(`/api/work-hub/summary?timezone=${encodeURIComponent(timeZone)}`, options),
  listTodos: (query = '', options?: ApiFetchOptions) => apiFetch<TodoPageDTO>(`/api/todos${query}`, options),
  getTodo: (todoID: string, options?: ApiFetchOptions) =>
    apiFetch<TodoItemDTO>(`/api/todos/${todoID}`, options),
  getTodoMissions: (todoID: string, options?: ApiFetchOptions) =>
    apiFetch<TodoExecutionSummaryDTO>(`/api/todos/${todoID}/missions`, options),
  getTodoProgress: (todoID: string, options?: ApiFetchOptions) =>
    apiFetch<TodoExecutionProgressDTO>(`/api/todos/${todoID}/progress`, options),
  createTodo: (body: CreateTodoRequest, options?: ApiFetchOptions) =>
    apiFetch<TodoItemDTO>('/api/todos', jsonMutation('POST', body, options)),
  updateTodo: (todoID: string, body: UpdateTodoRequest, options?: ApiFetchOptions) =>
    apiFetch<TodoItemDTO>(`/api/todos/${todoID}`, jsonMutation('PATCH', body, options)),
  deleteTodo: (todoID: string, version?: number, options?: ApiFetchOptions) =>
    apiFetch<void>(`/api/todos/${todoID}`, jsonMutation('DELETE', version ? { version } : {}, options)),
  listTodoComments: (todoID: string, query = '', options?: ApiFetchOptions) =>
    apiFetch<IssueCommentPageDTO>(`/api/todos/${todoID}/comments${query}`, options),
  createTodoComment: (todoID: string, body: CreateIssueCommentRequest, options?: ApiFetchOptions) =>
    apiFetch<IssueCommentDTO>(`/api/todos/${todoID}/comments`, jsonMutation('POST', body, options)),
  claimTodo: (todoID: string, body: ClaimTodoRequest = {}, options?: ApiFetchOptions) =>
    apiFetch<TodoItemDTO>(`/api/todos/${todoID}/claim`, jsonMutation('POST', body, options)),
  moveTodo: (todoID: string, body: MoveTodoRequest, options?: ApiFetchOptions) =>
    apiFetch<TodoItemDTO>(`/api/todos/${todoID}/move`, jsonMutation('POST', body, options)),
  completeTodo: (todoID: string, version?: number, options?: ApiFetchOptions) =>
		apiFetch<TodoItemDTO>(`/api/todos/${todoID}/complete`, jsonMutation('POST', version ? { version } : {}, options)),
  cancelTodo: (todoID: string, version?: number, options?: ApiFetchOptions) =>
		apiFetch<TodoItemDTO>(`/api/todos/${todoID}/cancel`, jsonMutation('POST', version ? { version } : {}, options)),
  reopenTodo: (todoID: string, version?: number, options?: ApiFetchOptions) =>
		apiFetch<TodoItemDTO>(`/api/todos/${todoID}/reopen`, jsonMutation('POST', version ? { version } : {}, options)),
  startAutomation: (todoID: string, planDigest: string, options?: ApiFetchOptions) =>
    apiFetch<TodoAutomationStateDTO>(`/api/todos/${todoID}/automation/start`, jsonMutation('POST', { plan_digest: planDigest }, options)),
  suggestDraft: (body: SuggestDraftRequest, options?: ApiFetchOptions) =>
    apiFetch<DraftSuggestionDTO>('/api/todo-drafts/suggest', jsonMutation('POST', body, options))
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}
