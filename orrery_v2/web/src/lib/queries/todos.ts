import { createInfiniteQuery, createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { todoApi } from '#lib/api/todos'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type {
  ClaimTodoRequest,
  CreateIssueCommentRequest,
  CreateTodoRequest,
  IssueCommentPageDTO,
  MoveTodoRequest,
  SuggestDraftRequest,
  TodoPageDTO,
  TodoPriority,
  TodoStatus,
  UpdateTodoRequest,
  WorkHubSummaryDTO
} from '#lib/types/todo'

export interface TodoFilters {
  status?: TodoStatus | ''
  priority?: TodoPriority | ''
  organizationId?: string
  departmentId?: string
  projectId?: string
  developmentProjectId?: string
  sourceIdeaId?: string
  assigneeId?: string
  createdBy?: string
  q?: string
  dueBefore?: string
  mine?: boolean
  unassigned?: boolean
  includeCompleted?: boolean
  limit?: number
  offset?: number
}

interface QueryClientLike {
  invalidateQueries: (options: { queryKey: readonly unknown[] }) => unknown
}

export const todoQueryKeys = {
  root: ['todos'] as const,
  summaryRoot: ['todos', 'work-hub-summary'] as const,
  summary: (timeZone = 'UTC') => ['todos', 'work-hub-summary', timeZone] as const,
  list: (filters: TodoFilters = {}) => ['todos', buildTodoQuery(filters)] as const,
  detail: (todoID: string) => ['todos', todoID] as const,
  comments: (todoID: string) => ['todos', todoID, 'comments'] as const,
  missions: (todoID: string) => ['todos', todoID, 'missions'] as const,
  progress: (todoID: string) => ['todos', todoID, 'progress'] as const
}

export function useWorkHubSummaryQuery(
  timeZone: () => string = () => 'UTC',
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const currentTimeZone = timeZone()
    return {
      queryKey: todoQueryKeys.summary(currentTimeZone),
      enabled: enabled(),
      queryFn: async ({ signal }) => todoApi.getWorkHubSummary(currentTimeZone, { signal }) as Promise<WorkHubSummaryDTO>
    }
  })
}

export function useTodoListQuery(
  filters: () => TodoFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: todoQueryKeys.list(current),
      enabled: enabled(),
      queryFn: async ({ signal }) => todoApi.listTodos(buildTodoQuery(current), { signal }) as Promise<TodoPageDTO>
    }
  })
}

export function useTodoInfiniteListQuery(filters: () => TodoFilters, enabled: () => boolean) {
  return createInfiniteQuery(() => {
    const current = { ...filters(), offset: 0 }
    return {
      queryKey: [...todoQueryKeys.list(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        todoApi.listTodos(buildTodoQuery({ ...current, offset: pageParam }), { signal }) as Promise<TodoPageDTO>,
      getNextPageParam: (lastPage, _pages, lastOffset) => {
        const count = lastPage.todos?.length ?? 0
        const next = lastOffset + count
        return count > 0 && (lastPage.has_more ?? next < lastPage.total) ? next : undefined
      },
      enabled: enabled()
    }
  })
}

export function useTodoDetailQuery(todoID: () => string | undefined) {
  return createQuery(() => {
    const id = todoID()
    return {
      queryKey: todoQueryKeys.detail(id ?? ''),
      enabled: Boolean(id),
      queryFn: async ({ signal }) => todoApi.getTodo(id ?? '', { signal })
    }
  })
}

export function useTodoCommentListQuery(todoID: () => string | undefined) {
  return createQuery(() => {
    const id = todoID()
    return {
      queryKey: todoQueryKeys.comments(id ?? ''),
      enabled: Boolean(id),
      queryFn: async ({ signal }) => todoApi.listTodoComments(id ?? '', '?limit=100', { signal }) as Promise<IssueCommentPageDTO>
    }
  })
}

export function useTodoExecutionQuery(todoID: () => string | undefined) {
  return createQuery(() => {
    const id = todoID()
    return {
      queryKey: todoQueryKeys.missions(id ?? ''),
      enabled: Boolean(id),
      queryFn: ({ signal }) => todoApi.getTodoMissions(id ?? '', { signal })
    }
  })
}

export function createTodoMutationRegistry(client: QueryClientLike) {
  const invalidateTodos = () => client.invalidateQueries({ queryKey: todoQueryKeys.root })
  const invalidateTodoDetail = (todoID: string) => client.invalidateQueries({ queryKey: todoQueryKeys.detail(todoID) })
  const invalidateTodoComments = (todoID: string) => client.invalidateQueries({ queryKey: todoQueryKeys.comments(todoID) })
  const invalidateTodoExecution = (todoID: string) => client.invalidateQueries({ queryKey: todoQueryKeys.missions(todoID) })
  const idempotency = createIdempotencyKey

  return {
    createTodo: async (body: CreateTodoRequest) => {
      const result = await todoApi.createTodo(body, { idempotencyKey: idempotency() })
      invalidateTodos()
      return result
    },
    updateTodo: async (todoID: string, body: UpdateTodoRequest) => {
      const result = await todoApi.updateTodo(todoID, body, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    claimTodo: async (todoID: string, body: ClaimTodoRequest = {}) => {
      const result = await todoApi.claimTodo(todoID, body, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    moveTodo: async (todoID: string, body: MoveTodoRequest) => {
      const result = await todoApi.moveTodo(todoID, body, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    completeTodo: async (todoID: string, version?: number) => {
      const result = await todoApi.completeTodo(todoID, version, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    cancelTodo: async (todoID: string, version?: number) => {
      const result = await todoApi.cancelTodo(todoID, version, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    reopenTodo: async (todoID: string, version?: number) => {
      const result = await todoApi.reopenTodo(todoID, version, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    startAutomation: async (todoID: string, planDigest: string) => {
      const result = await todoApi.startAutomation(todoID, planDigest, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      invalidateTodoExecution(todoID)
      return result
    },
    deleteTodo: async (todoID: string, version?: number) => {
      const result = await todoApi.deleteTodo(todoID, version, { idempotencyKey: idempotency() })
      invalidateTodos()
      invalidateTodoDetail(todoID)
      return result
    },
    createTodoComment: async (todoID: string, body: CreateIssueCommentRequest) => {
      const result = await todoApi.createTodoComment(todoID, body, { idempotencyKey: idempotency() })
      invalidateTodoDetail(todoID)
      invalidateTodoComments(todoID)
      return result
    },
    suggestDraft: async (body: SuggestDraftRequest) => {
      return todoApi.suggestDraft(body, { idempotencyKey: idempotency() })
    }
  }
}

export function useTodoCommandMutations() {
  const client = useQueryClient()
  const registry = createTodoMutationRegistry(client)

  return {
    createTodo: createMutation(() => ({ mutationFn: registry.createTodo })),
    updateTodo: createMutation(() => ({
      mutationFn: ({ todoID, body }: { todoID: string; body: UpdateTodoRequest }) =>
        registry.updateTodo(todoID, body)
    })),
    claimTodo: createMutation(() => ({
      mutationFn: ({ todoID, body }: { todoID: string; body?: ClaimTodoRequest }) =>
        registry.claimTodo(todoID, body ?? {})
    })),
    moveTodo: createMutation(() => ({
      mutationFn: ({ todoID, body }: { todoID: string; body: MoveTodoRequest }) =>
        registry.moveTodo(todoID, body)
    })),
    completeTodo: createMutation(() => ({
      mutationFn: (input: string | { todoID: string; version?: number }) =>
        typeof input === 'string' ? registry.completeTodo(input) : registry.completeTodo(input.todoID, input.version)
    })),
    cancelTodo: createMutation(() => ({
      mutationFn: (input: string | { todoID: string; version?: number }) =>
        typeof input === 'string' ? registry.cancelTodo(input) : registry.cancelTodo(input.todoID, input.version)
    })),
    reopenTodo: createMutation(() => ({
      mutationFn: (input: string | { todoID: string; version?: number }) =>
        typeof input === 'string' ? registry.reopenTodo(input) : registry.reopenTodo(input.todoID, input.version)
    })),
    startAutomation: createMutation(() => ({
      mutationFn: ({ todoID, planDigest }: { todoID: string; planDigest: string }) =>
        registry.startAutomation(todoID, planDigest)
    })),
    deleteTodo: createMutation(() => ({
      mutationFn: (input: string | { todoID: string; version?: number }) =>
        typeof input === 'string' ? registry.deleteTodo(input) : registry.deleteTodo(input.todoID, input.version)
    })),
    createTodoComment: createMutation(() => ({
      mutationFn: ({ todoID, body }: { todoID: string; body: CreateIssueCommentRequest }) =>
        registry.createTodoComment(todoID, body)
    })),
    suggestDraft: createMutation(() => ({ mutationFn: registry.suggestDraft }))
  }
}

export function buildTodoQuery(filters: TodoFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'status', filters.status)
  appendParam(params, 'priority', filters.priority)
  appendParam(params, 'organization_id', filters.organizationId)
  appendParam(params, 'department_id', filters.departmentId)
  appendParam(params, 'project_id', filters.projectId)
  appendParam(params, 'development_project_id', filters.developmentProjectId)
  appendParam(params, 'source_idea_id', filters.sourceIdeaId)
  appendParam(params, 'assignee_id', filters.assigneeId)
  appendParam(params, 'created_by', filters.createdBy)
  appendParam(params, 'q', filters.q)
  appendParam(params, 'due_before', filters.dueBefore)
  appendParam(params, 'mine', filters.mine)
  appendParam(params, 'unassigned', filters.unassigned)
  appendParam(params, 'include_completed', filters.includeCompleted)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | number | boolean | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}
