import { createInfiniteQuery, createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { ideaApi } from '#lib/api/ideas'
import { createIdempotencyKey } from '#lib/runtime/ids'
import { todoQueryKeys } from './todos'
import type {
  ConvertIdeaToTodoRequest,
  CreateIdeaRequest,
  CreateIssueCommentRequest,
  IdeaPageDTO,
  IdeaStatus,
  IdeaVisibility,
  TransitionIdeaRequest,
  UpdateIdeaRequest
} from '#lib/types/idea'
import type { IssueCommentPageDTO } from '#lib/types/todo'

export interface IdeaFilters {
  status?: IdeaStatus | ''
  visibility?: IdeaVisibility | ''
  organizationId?: string
  departmentId?: string
  ownerId?: string
  reviewerId?: string
  q?: string
  limit?: number
  offset?: number
}

interface QueryClientLike {
  invalidateQueries: (options: { queryKey: readonly unknown[] }) => unknown
}

export const ideaQueryKeys = {
  root: ['ideas'] as const,
  list: (filters: IdeaFilters = {}) => ['ideas', buildIdeaQuery(filters)] as const,
  detail: (ideaID: string) => ['ideas', ideaID] as const,
  comments: (ideaID: string) => ['ideas', ideaID, 'comments'] as const
}

export function useIdeaListQuery(
  filters: () => IdeaFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: ideaQueryKeys.list(current),
      enabled: enabled(),
      queryFn: async ({ signal }) => ideaApi.listIdeas(buildIdeaQuery(current), { signal }) as Promise<IdeaPageDTO>
    }
  })
}

export function useIdeaInfiniteListQuery(filters: () => IdeaFilters, enabled: () => boolean) {
  return createInfiniteQuery(() => {
    const current = { ...filters(), offset: 0 }
    return {
      queryKey: [...ideaQueryKeys.list(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        ideaApi.listIdeas(buildIdeaQuery({ ...current, offset: pageParam }), { signal }) as Promise<IdeaPageDTO>,
      getNextPageParam: (lastPage, _pages, lastOffset) => {
        const count = lastPage.ideas?.length ?? 0
        const next = lastOffset + count
        return count > 0 && (lastPage.has_more ?? next < lastPage.total) ? next : undefined
      },
      enabled: enabled()
    }
  })
}

export function useIdeaDetailQuery(ideaID: () => string | undefined) {
  return createQuery(() => {
    const id = ideaID()
    return {
      queryKey: ideaQueryKeys.detail(id ?? ''),
      enabled: Boolean(id),
      queryFn: async ({ signal }) => ideaApi.getIdea(id ?? '', { signal })
    }
  })
}

export function useIdeaCommentListQuery(ideaID: () => string | undefined) {
  return createQuery(() => {
    const id = ideaID()
    return {
      queryKey: ideaQueryKeys.comments(id ?? ''),
      enabled: Boolean(id),
      queryFn: async ({ signal }) => ideaApi.listIdeaComments(id ?? '', '?limit=100', { signal }) as Promise<IssueCommentPageDTO>
    }
  })
}

export function createIdeaMutationRegistry(client: QueryClientLike) {
  const invalidateIdeas = () => {
    client.invalidateQueries({ queryKey: ideaQueryKeys.root })
    client.invalidateQueries({ queryKey: todoQueryKeys.summaryRoot })
  }
  const invalidateIdeaDetail = (ideaID: string) => client.invalidateQueries({ queryKey: ideaQueryKeys.detail(ideaID) })
  const invalidateIdeaComments = (ideaID: string) => client.invalidateQueries({ queryKey: ideaQueryKeys.comments(ideaID) })
  const invalidateTodos = () => client.invalidateQueries({ queryKey: todoQueryKeys.root })
  const invalidateTodoDetail = (todoID: string) => client.invalidateQueries({ queryKey: todoQueryKeys.detail(todoID) })
  const idempotency = createIdempotencyKey

  return {
    createIdea: async (body: CreateIdeaRequest) => {
      const result = await ideaApi.createIdea(body, { idempotencyKey: idempotency() })
      invalidateIdeas()
      return result
    },
    updateIdea: async (ideaID: string, body: UpdateIdeaRequest) => {
      const result = await ideaApi.updateIdea(ideaID, body, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      return result
    },
    transitionIdea: async (ideaID: string, body: TransitionIdeaRequest) => {
      const result = await ideaApi.transitionIdea(ideaID, body, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      return result
    },
    voteIdea: async (ideaID: string) => {
      const result = await ideaApi.voteIdea(ideaID, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      return result
    },
    archiveIdea: async (ideaID: string) => {
      const result = await ideaApi.archiveIdea(ideaID, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      return result
    },
    restoreIdea: async (ideaID: string) => {
      const result = await ideaApi.restoreIdea(ideaID, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      return result
    },
    convertIdeaToTodo: async (ideaID: string, body: ConvertIdeaToTodoRequest = {}) => {
      const result = await ideaApi.convertIdeaToTodo(ideaID, body, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      invalidateTodos()
      if (result.todo?.todo_id) invalidateTodoDetail(result.todo.todo_id)
      return result
    },
    deleteIdea: async (ideaID: string, version?: number) => {
      const result = await ideaApi.deleteIdea(ideaID, version, { idempotencyKey: idempotency() })
      invalidateIdeas()
      invalidateIdeaDetail(ideaID)
      return result
    },
    createIdeaComment: async (ideaID: string, body: CreateIssueCommentRequest) => {
      const result = await ideaApi.createIdeaComment(ideaID, body, { idempotencyKey: idempotency() })
      invalidateIdeaDetail(ideaID)
      invalidateIdeaComments(ideaID)
      return result
    }
  }
}

export function useIdeaCommandMutations() {
  const client = useQueryClient()
  const registry = createIdeaMutationRegistry(client)

  return {
    createIdea: createMutation(() => ({ mutationFn: registry.createIdea })),
    updateIdea: createMutation(() => ({
      mutationFn: ({ ideaID, body }: { ideaID: string; body: UpdateIdeaRequest }) =>
        registry.updateIdea(ideaID, body)
    })),
    transitionIdea: createMutation(() => ({
      mutationFn: ({ ideaID, body }: { ideaID: string; body: TransitionIdeaRequest }) =>
        registry.transitionIdea(ideaID, body)
    })),
    voteIdea: createMutation(() => ({ mutationFn: registry.voteIdea })),
    archiveIdea: createMutation(() => ({ mutationFn: registry.archiveIdea })),
    restoreIdea: createMutation(() => ({ mutationFn: registry.restoreIdea })),
    convertIdeaToTodo: createMutation(() => ({
      mutationFn: ({ ideaID, body }: { ideaID: string; body?: ConvertIdeaToTodoRequest }) =>
        registry.convertIdeaToTodo(ideaID, body ?? {})
    })),
    deleteIdea: createMutation(() => ({
      mutationFn: (input: string | { ideaID: string; version?: number }) =>
        typeof input === 'string' ? registry.deleteIdea(input) : registry.deleteIdea(input.ideaID, input.version)
    })),
    createIdeaComment: createMutation(() => ({
      mutationFn: ({ ideaID, body }: { ideaID: string; body: CreateIssueCommentRequest }) =>
        registry.createIdeaComment(ideaID, body)
    }))
  }
}

export function buildIdeaQuery(filters: IdeaFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'status', filters.status)
  appendParam(params, 'visibility', filters.visibility)
  appendParam(params, 'organization_id', filters.organizationId)
  appendParam(params, 'department_id', filters.departmentId)
  appendParam(params, 'owner_id', filters.ownerId)
  appendParam(params, 'reviewer_id', filters.reviewerId)
  appendParam(params, 'q', filters.q)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | number | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}
