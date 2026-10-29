import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { goto } from '$app/navigation'
import HomePage from '#lib/test/HomePageHarness.svelte'
import { setLocale } from '#lib/i18n'
import { useIdeaListQuery } from '#lib/queries/ideas'
import { useDevelopmentProjectListQuery } from '#lib/queries/development-projects'
import { useTodoListQuery } from '#lib/queries/todos'
import { applySession, clearSession, session } from '#lib/stores/session.svelte'
import type { IdeaPageDTO } from '#lib/types/idea'
import type { TodoPageDTO, WorkHubSummaryDTO } from '#lib/types/todo'

vi.mock('$app/navigation', () => ({ goto: vi.fn() }))

const todoMutations = {
  createTodo: vi.fn(),
  updateTodo: vi.fn(),
  moveTodo: vi.fn(),
  completeTodo: vi.fn(),
  createTodoComment: vi.fn(),
  claimTodo: vi.fn(),
  cancelTodo: vi.fn(),
  reopenTodo: vi.fn(),
  deleteTodo: vi.fn(),
  suggestDraft: vi.fn()
}

const ideaMutations = {
  createIdea: vi.fn(),
  updateIdea: vi.fn(),
  transitionIdea: vi.fn(),
  voteIdea: vi.fn(),
  convertIdeaToTodo: vi.fn(),
  archiveIdea: vi.fn(),
  restoreIdea: vi.fn(),
  deleteIdea: vi.fn(),
  createIdeaComment: vi.fn()
}

let todoPage: TodoPageDTO
let ideaPage: IdeaPageDTO
let summary: WorkHubSummaryDTO
let todoListQueryInvocation = 0
let focusTodoQueryState = queryState()
let teamTodoQueryState = queryState()
let ideaListQueryState = queryState()
let summaryQueryState = queryState()

vi.mock('#lib/queries/work-inbox', async (importOriginal) => ({
  ...await importOriginal<typeof import('#lib/queries/work-inbox')>(),
  useWorkInboxSources: vi.fn(() => ({
    todos: { ...focusTodoQueryState, data: todoPage.todos.filter((todo) => !['done', 'canceled'].includes(todo.status) && (todo.assignee_id === session.actorId || (!todo.assignee_id && todo.created_by === session.actorId))), refetch: vi.fn() },
    completed: { ...queryState(), data: todoPage.todos.filter((todo) => ['done', 'canceled'].includes(todo.status) && (todo.assignee_id === session.actorId || (!todo.assignee_id && todo.created_by === session.actorId))), refetch: vi.fn() },
    approvals: { ...queryState(), data: undefined, refetch: vi.fn() },
    results: { ...queryState(), data: [], refetch: vi.fn() }
  }))
}))

vi.mock('#lib/queries/work-actions', async (importOriginal) => ({
  ...await importOriginal<typeof import('#lib/queries/work-actions')>(),
  useWorkActionSources: vi.fn(() => ({
    todos: { ...focusTodoQueryState, data: todoPage.todos.filter((todo) => todo.assignee_id === session.actorId || (!todo.assignee_id && todo.created_by === session.actorId)), refetch: vi.fn() },
    business: { ...queryState(), data: [], refetch: vi.fn() },
    manual: { ...queryState(), data: [], refetch: vi.fn() },
    fixed: { ...queryState(), data: [], refetch: vi.fn() },
    councils: { ...queryState(), data: [], refetch: vi.fn() },
    results: { ...queryState(), data: [], refetch: vi.fn() }
  })),
  useWorkActionWorkflows: vi.fn(() => ({ ...queryState(), data: [], refetch: vi.fn() }))
}))

vi.mock('#lib/queries/todos', () => ({
  useTodoListQuery: vi.fn(() => ({
    ...(todoListQueryInvocation++ === 0 ? focusTodoQueryState : teamTodoQueryState),
    data: todoPage
  })),
  useWorkHubSummaryQuery: vi.fn(() => ({ ...summaryQueryState, data: summary })),
  useTodoCommandMutations: vi.fn(() => mutationObjects(todoMutations))
}))

vi.mock('#lib/queries/ideas', () => ({
  useIdeaListQuery: vi.fn(() => ({ ...ideaListQueryState, data: ideaPage })),
  useIdeaCommandMutations: vi.fn(() => mutationObjects(ideaMutations))
}))

vi.mock('#lib/queries/development-projects', () => ({
  useDevelopmentProjectListQuery: vi.fn(() => ({ isLoading: false, isError: false, data: { items: [] } }))
}))

vi.mock('#lib/queries/workforce', () => ({
  useWorkforceMembersQuery: vi.fn(() => ({
    isLoading: false,
    isError: false,
    data: {
      members: [
        {
          id: 'agent-design',
          displayName: '设计 Agent',
          realName: '',
          agentKind: 'ai',
          primaryRoleLabel: '产品设计',
          positionDisplayName: '',
          enabled: true
        }
      ]
    }
  }))
}))

describe('work hub home page', () => {
  beforeEach(() => {
    vi.mocked(goto).mockReset()
    vi.mocked(useTodoListQuery).mockClear()
    vi.mocked(useIdeaListQuery).mockClear()
    vi.mocked(useDevelopmentProjectListQuery).mockClear()
    todoListQueryInvocation = 0
    focusTodoQueryState = queryState()
    teamTodoQueryState = queryState()
    ideaListQueryState = queryState()
    summaryQueryState = queryState()
    setLocale('zh-CN')
    applySession({
      accessToken: 'test-token',
      user: {
        userId: 'user-1',
        username: 'operator',
        displayName: '当前用户',
        nickname: '当前用户',
        email: 'operator@example.com',
        agentProfileId: '',
        role: 'owner'
      },
      permissionScope: [
        'todo.read',
        'todo.write',
        'idea.read',
        'idea.write',
        'discussion.read',
        'discussion.write',
        'workforce.read',
        'operations.read'
      ],
      expiresAt: '2099-01-01T00:00:00Z'
    })
    todoPage = {
      todos: [
        todoFixture({
          todo_id: 'task-mine',
          issue_number: 'TASK-1001',
          title: '整理客户访谈结论',
          status: 'planned',
          assignee_id: 'operator',
          assignee_name: '当前用户',
          priority: 'high'
        }),
        todoFixture({
          todo_id: 'task-blocked',
          issue_number: 'TASK-1002',
          title: '完成定价实验',
          status: 'blocked',
          assignee_id: 'operator',
          assignee_name: '当前用户',
          blocked_reason: '等待财务数据'
        }),
        todoFixture({
          todo_id: 'task-team',
          issue_number: 'TASK-1003',
          title: '更新品牌组件库',
          status: 'in_progress',
          assignee_id: 'agent-design',
          assignee_name: '设计 Agent'
        })
      ],
      total: 3,
      limit: 200,
      offset: 0,
      has_more: false
    }
    ideaPage = {
      ideas: [
        ideaFixture({ idea_id: 'idea-submitted', issue_number: 'IDEA-2001', title: '自动沉淀客户洞察', status: 'submitted' }),
        ideaFixture({ idea_id: 'idea-review', issue_number: 'IDEA-2002', title: '为项目建立风险雷达', status: 'under_review' }),
        ideaFixture({ idea_id: 'idea-accepted', issue_number: 'IDEA-2003', title: '内部专家问答助手', status: 'accepted' })
      ],
      total: 3,
      limit: 200,
      offset: 0,
      has_more: false
    }
    summary = {
      my_open_tasks: 2,
      due_today: 1,
      overdue: 0,
      blocked: 1,
      awaiting_task_review: 0,
      tasks_inbox: 0,
      tasks_planned: 1,
      tasks_in_progress: 1,
      ideas_awaiting_review: 2,
      ideas_submitted: 1,
      ideas_under_review: 1,
      ideas_in_experiment: 0,
      ideas_converted: 4,
      unassigned_team_tasks: 0
    }
    Object.values(todoMutations).forEach(resetMutation)
    Object.values(ideaMutations).forEach(resetMutation)
    ideaMutations.convertIdeaToTodo.mockImplementation((_payload, options) =>
      options?.onSuccess?.({ todo: { todo_id: 'task-converted' } })
    )
  })

  afterEach(() => {
    clearSession()
    setLocale('zh-CN')
    vi.restoreAllMocks()
  })

  it('uses the work hub model and removes the raw tool catalogue', () => {
    const source = readFileSync('src/routes/+page.svelte', 'utf8')
    expect(source).toContain('useWorkHubSummaryQuery')
    expect(source).toContain('useWorkforceMembersQuery')
    expect(source).toContain('<WorkInbox />')
    expect(source).toContain("() => activeTab === 'team'")
    expect(source).toContain("() => activeTab === 'ideas'")
    expect(source).toContain("data-visual-reference={dedicated ? undefined : 'figma-home'}")
    expect(source).toContain('taskFeedFilterOptions')
    expect(source).toContain('homeTaskFeed')
    expect(source).not.toContain('{#if true}')
    expect(source).not.toContain('data-testid="workhub-summary"')
    expect(source).not.toContain('todo_move')
    expect(source).not.toContain('idea_create')
    expect(source).not.toContain('systemTools')
    expect(source).not.toContain('GPT-Skill v2')
    expect(source).not.toContain('<Paperclip')
    expect(source).not.toContain('limit: 200')
    expect(source).toContain("handledCreateRequest = ''")
    const legacyTodos = readFileSync('src/routes/todos/+page.ts', 'utf8')
    const legacyIdeas = readFileSync('src/routes/ideas/+page.ts', 'utf8')
    expect(legacyTodos).toContain('requireLegacyRouteAliases()')
    expect(legacyTodos).toContain("params.set('tab', 'team')")
    expect(legacyIdeas).toContain('requireLegacyRouteAliases()')
    expect(legacyIdeas).toContain("params.set('tab', 'ideas')")
  })

  it('renders the dedicated todo management page without unrelated data requests', () => {
    render(HomePage, { props: { initialTab: 'team', dedicated: true } })

    expect(screen.getByRole('heading', { name: '待办管理' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '新建任务' })).toBeTruthy()
    expect(screen.getByRole('textbox', { name: '搜索任务' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: '我的工作' })).toBeNull()

    const todoCalls = vi.mocked(useTodoListQuery).mock.calls
    expect(todoCalls[0][1]?.()).toBe(false)
    expect(todoCalls[1][1]?.()).toBe(true)
    expect(vi.mocked(useIdeaListQuery).mock.calls[0][1]?.()).toBe(false)
  })

  it('renders the dedicated idea management page without the todo-only summary', () => {
    render(HomePage, { props: { initialTab: 'ideas', dedicated: true } })

    expect(screen.getByRole('heading', { name: '灵感管理' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '记录灵感' })).toBeTruthy()
    expect(screen.getByRole('textbox', { name: '搜索灵感' })).toBeTruthy()
    expect(screen.queryByText('团队进展')).toBeNull()

    expect(vi.mocked(useIdeaListQuery).mock.calls[0][1]?.()).toBe(true)
    const todoCalls = vi.mocked(useTodoListQuery).mock.calls
    expect(todoCalls[0][1]?.()).toBe(false)
    expect(todoCalls[1][1]?.()).toBe(false)
  })

  it('keeps server queries broad while the default feed contains only the current user work', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-05T12:00:00Z'))
    todoPage.todos.push(
      todoFixture({
        todo_id: 'task-review',
        issue_number: 'TASK-1004',
        title: '评审合规风险摘要',
        status: 'review',
        assignee_id: 'operator',
        assignee_name: '当前用户'
      }),
      todoFixture({
        todo_id: 'task-done',
        issue_number: 'TASK-1005',
        title: '归档发布复盘',
        status: 'done',
        assignee_id: 'operator',
        assignee_name: '当前用户'
      })
    )
    render(HomePage)

    const todoCalls = vi.mocked(useTodoListQuery).mock.calls
    expect(todoCalls).toHaveLength(2)
    const focusFilters = todoCalls[0][0]!
    const teamFilters = todoCalls[1][0]!
    expect(focusFilters()).toMatchObject({ mine: true, includeCompleted: true, limit: 50, offset: 0 })
    expect(teamFilters()).toMatchObject({ includeCompleted: true, limit: 50, offset: 0 })

    const ideaFilters = vi.mocked(useIdeaListQuery).mock.calls[0][0]!
    expect(ideaFilters()).toMatchObject({ limit: 50, offset: 0 })

    expect(screen.queryByText('归档发布复盘')).toBeNull()
    expect(screen.queryByText('更新品牌组件库')).toBeNull()
    await fireEvent.click(screen.getByRole('tab', { name: /待处理/ }))
    expect(screen.getByText('评审合规风险摘要')).toBeTruthy()
    expect(screen.queryByText('归档发布复盘')).toBeNull()

    await fireEvent.click(screen.getByRole('tab', { name: '已处理' }))
    expect(screen.getByText('归档发布复盘')).toBeTruthy()
    expect(screen.queryByText('评审合规风险摘要')).toBeNull()
  })

  it('does not replace the active work view with failures from unopened tabs', async () => {
    teamTodoQueryState = queryState({ isError: true })
    ideaListQueryState = queryState({ isError: true })
    summaryQueryState = queryState({ isError: true })

    render(HomePage)

    expect(screen.getByText('整理客户访谈结论')).toBeTruthy()
    expect(screen.queryByText('工作台加载失败')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '灵感池' }))
    expect(screen.getByText('工作台加载失败')).toBeTruthy()
  })

  it('renders delegation and the unified inbox with compact team task rows', async () => {
    render(HomePage)

    expect(screen.getByRole('heading', { name: '交给你的数字员工' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '指派' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '咨询' })).toBeTruthy()
    expect(screen.getByRole('textbox', { name: '描述你想推进的任务...' })).toBeTruthy()
    expect(screen.getByRole('tab', { name: /待处理/ })).toBeTruthy()
    expect(screen.getByRole('tab', { name: '结果' })).toBeTruthy()
    expect(screen.getByRole('tab', { name: '已处理' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '我的工作' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '灵感池' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '团队任务' })).toBeTruthy()
    expect(screen.getByText('整理客户访谈结论')).toBeTruthy()
    expect(screen.getByText('等待财务数据')).toBeTruthy()
    expect(screen.queryByText('更新品牌组件库')).toBeNull()
    expect(screen.queryByTestId('workhub-summary')).toBeNull()
    expect(document.querySelector('[data-work-item="todo:task-mine"]')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: '团队任务' }))
    const issueNumber = screen.getByText('TASK-1001')
    expect(issueNumber).toBeTruthy()
    expect(issueNumber.classList.contains('text-emerald-600')).toBe(false)
  })

  it('keeps Ctrl+Enter and Shift+Enter available for multiline delegation briefs', async () => {
    render(HomePage)

    const composer = screen.getByRole('textbox', { name: '描述你想推进的任务...' }) as HTMLTextAreaElement
    await fireEvent.input(composer, { target: { value: '第一行' } })
    await fireEvent.keyDown(composer, { key: 'Enter', ctrlKey: true })
    await fireEvent.keyDown(composer, { key: 'Enter', shiftKey: true })

    expect(screen.queryByRole('dialog', { name: '新建任务' })).toBeNull()
    expect(goto).not.toHaveBeenCalled()

    await fireEvent.input(composer, { target: { value: '第一行\n第二行' } })
    expect(composer.value).toBe('第一行\n第二行')
  })

  it('opens a consultation with Enter', async () => {
    render(HomePage)

    const composer = screen.getByRole('textbox', { name: '描述你想推进的任务...' })
    await fireEvent.input(composer, { target: { value: '帮我整理今天的计划' } })
    await fireEvent.keyDown(composer, { key: 'Enter' })

    expect(goto).toHaveBeenCalledWith('/discussions', {
      state: {
        discussionComposer: {
          draft: '帮我整理今天的计划',
          sendImmediately: true
        }
      }
    })
  })

  it('opens assignment as a private chat with the selected agent', async () => {
    render(HomePage)

    const selector = screen.getByRole('combobox', { name: '指派对象' }) as HTMLSelectElement
    expect(selector.value).toBe('')
    await fireEvent.change(selector, { target: { value: 'agent-design' } })
    await fireEvent.input(screen.getByRole('textbox', { name: '描述你想推进的任务...' }), {
      target: { value: '审校框架合同' }
    })
    await fireEvent.click(screen.getByRole('button', { name: '打开对话' }))

    expect(goto).toHaveBeenCalledWith('/discussions', {
      state: {
        discussionComposer: {
          draft: '审校框架合同',
          sendImmediately: true,
          directTargetId: 'agent-design',
          directTargetKind: 'agent',
          directTargetName: '设计 Agent'
        }
      }
    })
    expect(todoMutations.createTodo).not.toHaveBeenCalled()
  })

  it('opens a consultation with its draft and selected target intact', async () => {
    render(HomePage)

    await fireEvent.click(screen.getByRole('button', { name: '咨询' }))
    await fireEvent.change(screen.getByRole('combobox', { name: '指派对象' }), { target: { value: 'agent-design' } })
    await fireEvent.input(screen.getByRole('textbox', { name: '描述你想推进的任务...' }), {
      target: { value: '这个合同有哪些付款风险？' }
    })
    await fireEvent.click(screen.getByRole('button', { name: '打开对话' }))

    expect(goto).toHaveBeenCalledWith('/discussions', {
      state: {
        discussionComposer: {
          draft: '这个合同有哪些付款风险？',
          sendImmediately: true,
          directTargetId: 'agent-design',
          directTargetKind: 'agent',
          directTargetName: '设计 Agent'
        }
      }
    })
  })

  it('opens an unassigned composer as the default group discussion', async () => {
    render(HomePage)

    expect((screen.getByRole('combobox', { name: '指派对象' }) as HTMLSelectElement).value).toBe('')
    await fireEvent.input(screen.getByRole('textbox', { name: '描述你想推进的任务...' }), {
      target: { value: '请 PM 协调团队推进发布' }
    })
    await fireEvent.click(screen.getByRole('button', { name: '打开对话' }))

    expect(goto).toHaveBeenCalledWith('/discussions', {
      state: {
        discussionComposer: {
          draft: '请 PM 协调团队推进发布',
          sendImmediately: true
        }
      }
    })
  })

  it('does not expose the conversation composer without discussion write access', () => {
    applySession({
      accessToken: 'task-writer-token',
      user: {
        userId: 'user-1', username: 'operator', displayName: '当前用户', nickname: '当前用户',
        email: 'operator@example.com', agentProfileId: '', role: 'member'
      },
      permissionScope: ['todo.read', 'todo.write', 'idea.read', 'workforce.read'],
      expiresAt: '2099-01-01T00:00:00Z'
    })

    render(HomePage)

    expect(screen.queryByRole('button', { name: '指派' })).toBeNull()
    expect(screen.queryByRole('button', { name: '咨询' })).toBeNull()
    expect(screen.queryByRole('textbox', { name: '描述你想推进的任务...' })).toBeNull()
    expect(screen.queryByRole('button', { name: '新建任务' })).toBeNull()
  })

  it('keeps both conversation modes available without exposing task creation to discussion-only writers', async () => {
    applySession({
      accessToken: 'discussion-writer-token',
      user: {
        userId: 'user-1', username: 'operator', displayName: '当前用户', nickname: '当前用户',
        email: 'operator@example.com', agentProfileId: '', role: 'member'
      },
      permissionScope: ['todo.read', 'idea.read', 'discussion.read', 'discussion.write', 'workforce.read'],
      expiresAt: '2099-01-01T00:00:00Z'
    })

    render(HomePage)

    expect(screen.getByRole('button', { name: '指派' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '咨询' })).toBeTruthy()
    await fireEvent.change(screen.getByRole('combobox', { name: '指派对象' }), { target: { value: 'agent-design' } })
    await fireEvent.input(screen.getByRole('textbox', { name: '描述你想推进的任务...' }), {
      target: { value: '检查合同风险' }
    })
    await fireEvent.click(screen.getByRole('button', { name: '打开对话' }))
    expect(goto).toHaveBeenCalledWith('/discussions', {
      state: {
        discussionComposer: {
          draft: '检查合同风险',
          sendImmediately: true,
          directTargetId: 'agent-design',
          directTargetKind: 'agent',
          directTargetName: '设计 Agent'
        }
      }
    })
    expect(screen.queryByRole('button', { name: '新建任务' })).toBeNull()
  })

  it('keeps status, assignee, and completion actions available in my work', async () => {
    render(HomePage)
    await fireEvent.click(screen.getByRole('button', { name: '团队任务' }))

    const status = screen.getByLabelText('状态: 整理客户访谈结论')
    const assignee = screen.getByLabelText('负责人: 整理客户访谈结论')
    expect(status).toBeTruthy()
    expect(assignee).toBeTruthy()
    await fireEvent.change(status, { target: { value: 'in_progress' } })
    await fireEvent.change(assignee, { target: { value: 'agent-design' } })
    await fireEvent.click(screen.getByRole('button', { name: '完成: 整理客户访谈结论' }))

    expect(todoMutations.moveTodo).toHaveBeenCalled()
    expect(todoMutations.updateTodo).toHaveBeenCalled()
    expect(todoMutations.completeTodo).toHaveBeenCalled()
    expect(document.querySelector('a[href="/todos/task-mine"]')).toBeTruthy()
  })

  it('restores the searchable idea pool and its production actions', async () => {
    render(HomePage)

    await fireEvent.click(screen.getByRole('button', { name: '灵感池' }))

    expect(screen.getByRole('textbox', { name: '搜索灵感' })).toBeTruthy()
    expect(screen.getByText('自动沉淀客户洞察')).toBeTruthy()
    expect(screen.getByRole('button', { name: '支持: 自动沉淀客户洞察' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '开始评审' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '评审' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '转为任务' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '支持: 自动沉淀客户洞察' }))
    expect(ideaMutations.voteIdea).toHaveBeenCalledWith('idea-submitted', expect.any(Object))

    await fireEvent.click(screen.getByRole('button', { name: '开始评审' }))
    expect(ideaMutations.transitionIdea).toHaveBeenCalledWith(
      { ideaID: 'idea-submitted', body: expect.objectContaining({ status: 'under_review', version: 1 }) },
      expect.any(Object)
    )

    await fireEvent.click(screen.getByRole('button', { name: '评审' }))
    const reviewDialog = screen.getByRole('dialog', { name: '评审灵感' })
    await fireEvent.input(within(reviewDialog).getByLabelText('决策依据'), { target: { value: '价值明确，先小范围验证。' } })
    await fireEvent.click(within(reviewDialog).getByRole('button', { name: '应用' }))
    expect(ideaMutations.transitionIdea).toHaveBeenCalledWith(
      {
        ideaID: 'idea-review',
        body: expect.objectContaining({ status: 'accepted', decision_reason: '价值明确，先小范围验证。', version: 1 })
      },
      expect.any(Object)
    )

    await fireEvent.click(screen.getByRole('button', { name: '转为任务' }))
    const convertDialog = screen.getByRole('dialog', { name: '生成执行任务' })
    await fireEvent.input(within(convertDialog).getByLabelText('验收标准'), { target: { value: '完成内部试用' } })
    await fireEvent.click(within(convertDialog).getByRole('button', { name: '转为任务' }))
    expect(ideaMutations.convertIdeaToTodo).toHaveBeenCalledWith(
      {
        ideaID: 'idea-accepted',
        body: expect.objectContaining({ todo_title: '内部专家问答助手', acceptance_criteria: '完成内部试用', version: 1 })
      },
      expect.any(Object)
    )
  })

  it('restores team task filtering and list-board switching', async () => {
    render(HomePage)

    await fireEvent.click(screen.getByRole('button', { name: '团队任务' }))
    expect(screen.getByRole('textbox', { name: '搜索任务' })).toBeTruthy()
    expect(screen.getByText('更新品牌组件库')).toBeTruthy()
    expect(screen.getByRole('button', { name: '列表' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '看板' }))
    expect(screen.getByTestId('team-task-board')).toBeTruthy()
  })

  it('switches the redesigned work hub to English', () => {
    setLocale('en-US')
    render(HomePage)
    expect(screen.getByRole('heading', { name: 'Delegate to your digital team' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Assign' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Ask' })).toBeTruthy()
    expect(screen.getByRole('tab', { name: /Pending/ })).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'Results' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'My work' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Idea pool' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Team tasks' })).toBeTruthy()
  })

  it('keeps read-only users in a non-mutating work hub', async () => {
    applySession({
      accessToken: 'read-only-token',
      user: {
        userId: 'reader-1',
        username: 'reader',
        displayName: '只读用户',
        nickname: '只读用户',
        email: 'reader@example.com',
        agentProfileId: '',
        role: 'member'
      },
      permissionScope: ['todo.read', 'idea.read'],
      expiresAt: '2099-01-01T00:00:00Z'
    })

    render(HomePage)

    expect(screen.queryByRole('textbox', { name: '描述你想推进的任务...' })).toBeNull()
    expect(screen.queryByRole('button', { name: '新建任务' })).toBeNull()
    expect(screen.queryByRole('button', { name: '记录灵感' })).toBeNull()
    expect(screen.queryByText('整理客户访谈结论')).toBeNull()
    expect(screen.queryByLabelText('状态: 整理客户访谈结论')).toBeNull()
    expect(screen.queryByLabelText('负责人: 整理客户访谈结论')).toBeNull()
    expect(screen.queryByRole('button', { name: '完成: 整理客户访谈结论' })).toBeNull()
    expect(screen.getByRole('tab', { name: /待处理/ })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: '团队任务' }))
    expect(screen.getByText('整理客户访谈结论')).toBeTruthy()
    expect(screen.queryByLabelText('状态: 整理客户访谈结论')).toBeNull()
    expect(screen.queryByLabelText('负责人: 整理客户访谈结论')).toBeNull()
    expect(screen.queryByRole('button', { name: '完成: 整理客户访谈结论' })).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '灵感池' }))
    expect(screen.getByText('自动沉淀客户洞察')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '支持: 自动沉淀客户洞察' })).toBeNull()
    expect(screen.queryByRole('button', { name: '开始评审' })).toBeNull()
    expect(screen.queryByRole('button', { name: '评审' })).toBeNull()
    expect(screen.queryByRole('button', { name: '转为任务' })).toBeNull()
    expect(Object.values(todoMutations).every((mutation) => mutation.mock.calls.length === 0)).toBe(true)
    expect(Object.values(ideaMutations).every((mutation) => mutation.mock.calls.length === 0)).toBe(true)
  })
})

function todoFixture(overrides: Partial<TodoPageDTO['todos'][number]> = {}): TodoPageDTO['todos'][number] {
  return {
    todo_id: 'task-1',
    title: 'Task',
    description: 'Task details',
    status: 'inbox',
    priority: 'medium',
    work_kind: 'general',
    automation_mode: 'manual',
    created_by: 'operator',
    created_at: '2026-07-11T08:00:00Z',
    updated_at: '2026-07-11T08:00:00Z',
    version: 1,
    ...overrides
  }
}

function ideaFixture(overrides: Partial<IdeaPageDTO['ideas'][number]> = {}): IdeaPageDTO['ideas'][number] {
  return {
    idea_id: 'idea-1',
    title: 'Idea',
    content: 'Idea details',
    status: 'submitted',
    visibility: 'organization',
    impact: 'high',
    effort: 'small',
    owner_id: 'operator',
    owner_name: '当前用户',
    vote_count: 3,
    viewer_has_voted: false,
    created_by: 'operator',
    created_at: '2026-07-11T08:00:00Z',
    updated_at: '2026-07-11T08:00:00Z',
    version: 1,
    ...overrides
  }
}

function resetMutation(fn: ReturnType<typeof vi.fn>) {
  fn.mockReset()
  fn.mockImplementation((_payload, options) => options?.onSuccess?.())
}

function queryState(overrides: Partial<{ isLoading: boolean; isError: boolean }> = {}) {
  return { isLoading: false, isError: false, ...overrides }
}

function mutationObjects(source: Record<string, ReturnType<typeof vi.fn>>) {
  return Object.fromEntries(
    Object.entries(source).map(([key, mutate]) => [key, { mutate, isPending: false }])
  )
}
