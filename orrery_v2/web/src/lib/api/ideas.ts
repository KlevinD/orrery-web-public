import { apiFetch, type ApiFetchOptions } from './client'
import type {
  ConvertIdeaToTodoRequest,
  CreateIdeaRequest,
  CreateIssueCommentRequest,
  IdeaConversionDTO,
  IdeaItemDTO,
  IdeaPageDTO,
	TransitionIdeaRequest,
  UpdateIdeaRequest
} from '#lib/types/idea'
import type { IssueCommentDTO, IssueCommentPageDTO } from '#lib/types/todo'

export const ideaApi = {
  listIdeas: (query = '', options?: ApiFetchOptions) => apiFetch<IdeaPageDTO>(`/api/ideas${query}`, options),
  getIdea: (ideaID: string, options?: ApiFetchOptions) =>
    apiFetch<IdeaItemDTO>(`/api/ideas/${ideaID}`, options),
  createIdea: (body: CreateIdeaRequest, options?: ApiFetchOptions) =>
    apiFetch<IdeaItemDTO>('/api/ideas', jsonMutation('POST', body, options)),
  updateIdea: (ideaID: string, body: UpdateIdeaRequest, options?: ApiFetchOptions) =>
    apiFetch<IdeaItemDTO>(`/api/ideas/${ideaID}`, jsonMutation('PATCH', body, options)),
  deleteIdea: (ideaID: string, version?: number, options?: ApiFetchOptions) =>
    apiFetch<void>(`/api/ideas/${ideaID}`, jsonMutation('DELETE', version ? { version } : {}, options)),
  listIdeaComments: (ideaID: string, query = '', options?: ApiFetchOptions) =>
    apiFetch<IssueCommentPageDTO>(`/api/ideas/${ideaID}/comments${query}`, options),
  createIdeaComment: (ideaID: string, body: CreateIssueCommentRequest, options?: ApiFetchOptions) =>
    apiFetch<IssueCommentDTO>(`/api/ideas/${ideaID}/comments`, jsonMutation('POST', body, options)),
  archiveIdea: (ideaID: string, options?: ApiFetchOptions) =>
    apiFetch<IdeaItemDTO>(`/api/ideas/${ideaID}/archive`, { ...options, method: 'POST' }),
  restoreIdea: (ideaID: string, options?: ApiFetchOptions) =>
    apiFetch<IdeaItemDTO>(`/api/ideas/${ideaID}/restore`, { ...options, method: 'POST' }),
	transitionIdea: (ideaID: string, body: TransitionIdeaRequest, options?: ApiFetchOptions) =>
		apiFetch<IdeaItemDTO>(`/api/ideas/${ideaID}/transition`, jsonMutation('POST', body, options)),
	voteIdea: (ideaID: string, options?: ApiFetchOptions) =>
		apiFetch<IdeaItemDTO>(`/api/ideas/${ideaID}/vote`, { ...options, method: 'POST' }),
  convertIdeaToTodo: (ideaID: string, body: ConvertIdeaToTodoRequest = {}, options?: ApiFetchOptions) =>
    apiFetch<IdeaConversionDTO>(`/api/ideas/${ideaID}/convert-to-todo`, jsonMutation('POST', body, options))
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}
