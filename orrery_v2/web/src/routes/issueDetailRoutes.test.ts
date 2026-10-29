import { readFileSync } from 'node:fs'
import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TodoDetailPage from './todos/[todoId]/+page.svelte'
import IdeaDetailPage from './ideas/[ideaId]/+page.svelte'
import { setLocale } from '#lib/i18n'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import type { IdeaItemDTO } from '#lib/types/idea'
import type { IssueCommentPageDTO, TodoItemDTO } from '#lib/types/todo'

const todoMutations = {
  updateTodo: vi.fn(),
  moveTodo: vi.fn(),
  createTodoComment: vi.fn(),
  claimTodo: vi.fn(),
  completeTodo: vi.fn(),
  cancelTodo: vi.fn(),
  reopenTodo: vi.fn(),
  deleteTodo: vi.fn(),
  suggestDraft: vi.fn(),
  startAutomation: vi.fn()
}

const ideaMutations = {
  updateIdea: vi.fn(),
  transitionIdea: vi.fn(),
  convertIdeaToTodo: vi.fn(),
  createIdeaComment: vi.fn(),
  voteIdea: vi.fn(),
  archiveIdea: vi.fn(),
  restoreIdea: vi.fn(),
  deleteIdea: vi.fn()
}

let todoItem: TodoItemDTO
let ideaItem: IdeaItemDTO
let todoComments: IssueCommentPageDTO
let ideaComments: IssueCommentPageDTO

vi.mock('$app/navigation', () => ({ goto: vi.fn() }))

vi.mock('#lib/queries/todos', () => ({
  useTodoDetailQuery: vi.fn(() => ({ isLoading: false, isError: false, data: todoItem })),
  useTodoCommentListQuery: vi.fn(() => ({ isLoading: false, isError: false, data: todoComments })),
  useTodoCommandMutations: vi.fn(() => mutationObjects(todoMutations))
}))

vi.mock('#lib/queries/ideas', () => ({
  useIdeaDetailQuery: vi.fn(() => ({ isLoading: false, isError: false, data: ideaItem })),
  useIdeaCommentListQuery: vi.fn(() => ({ isLoading: false, isError: false, data: ideaComments })),
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
          id: 'agent-research',
          displayName: '研究 Agent',
          realName: '',
          agentKind: 'ai',
          primaryRoleLabel: '研究',
          positionDisplayName: '',
          enabled: true
        }
      ]
    }
  }))
}))

describe('work hub issue detail routes', () => {
  beforeEach(() => {
    setLocale('zh-CN')
    applySession({
      accessToken: 'test-token',
      user: {
        userId: 'user-current',
        username: 'operator',
        displayName: '当前用户',
        nickname: '当前用户',
        email: 'operator@example.com',
        agentProfileId: '',
        role: 'owner'
      },
      permissionScope: ['todo.read', 'todo.write', 'idea.read', 'idea.write', 'operations.read'],
      expiresAt: '2099-01-01T00:00:00Z'
    })
    todoItem = {
      todo_id: 'task-1',
      source_idea_id: 'idea-source',
      issue_number: 'TASK-1001',
      title: '汇总客户访谈反馈',
      description: '# 背景\n\n需要形成可执行结论。',
      status: 'planned',
      priority: 'high',
      work_kind: 'general',
      automation_mode: 'manual',
      proposer_name: '当前用户',
      tags: ['feedback'],
      assignee_id: 'operator',
      assignee_name: '当前用户',
      reviewer_id: 'agent-research',
      reviewer_name: '研究 Agent',
      acceptance_criteria: '输出访谈洞察与行动项',
      created_by: 'operator',
      created_at: '2026-07-10T10:00:00Z',
      updated_at: '2026-07-11T11:00:00Z',
      due_at: '2026-07-15T10:00:00Z',
      version: 3
    }
    ideaItem = {
      idea_id: 'idea-1',
      issue_number: 'IDEA-2001',
      title: '建立项目风险雷达',
      content: '## 价值\n\n提前暴露交付风险。',
      status: 'under_review',
      visibility: 'department',
      impact: 'high',
      effort: 'medium',
      proposer_name: '当前用户',
      tags: ['risk'],
      owner_id: 'operator',
      owner_name: '当前用户',
      reviewer_id: 'agent-research',
      reviewer_name: '研究 Agent',
      vote_count: 5,
      viewer_has_voted: true,
      created_by: 'operator',
      created_at: '2026-07-10T10:00:00Z',
      updated_at: '2026-07-11T11:00:00Z',
      version: 2
    }
    todoComments = commentPage('todo', 'task-1')
    ideaComments = commentPage('idea', 'idea-1')
    Object.values(todoMutations).forEach(resetMutation)
    Object.values(ideaMutations).forEach(resetMutation)
  })

  afterEach(() => {
    clearSession()
    vi.restoreAllMocks()
  })

  it('keeps todo and idea routes on the shared detail experience', () => {
    expect(readFileSync('src/routes/todos/[todoId]/+page.svelte', 'utf8')).toContain('IssueDetailPage')
    expect(readFileSync('src/routes/ideas/[ideaId]/+page.svelte', 'utf8')).toContain('IssueDetailPage')
    const detail = readFileSync('src/lib/components/issues/IssueDetailPage.svelte', 'utf8')
    expect(detail).toContain('useWorkforceMembersQuery')
    expect(detail).toContain('allowedTodoStatuses')
    expect(detail).toContain('allowedIdeaStatuses')
  })

  it('edits task execution metadata, changes lifecycle, and posts comments', async () => {
    render(TodoDetailPage, { props: { data: { todoId: 'task-1' } } })

    expect(screen.getByRole('heading', { name: '汇总客户访谈反馈' })).toBeTruthy()
    expect(screen.getByText('TASK-1001')).toBeTruthy()
    expect(screen.getByText('输出访谈洞察与行动项')).toBeTruthy()
    expect(document.querySelector('a[href="/ideas/idea-source"]')).toBeTruthy()
    expect(screen.getByText('产品经理')).toBeTruthy()
    expect(screen.getByText('工程师')).toBeTruthy()

    await fireEvent.click(screen.getAllByRole('button', { name: '编辑' })[0])
    await fireEvent.input(screen.getByLabelText('任务标题'), { target: { value: '汇总并评审客户访谈反馈' } })
    await fireEvent.change(screen.getByLabelText('优先级'), { target: { value: 'urgent' } })
    await fireEvent.change(screen.getByLabelText('负责人'), { target: { value: 'agent-research' } })
    await fireEvent.input(screen.getByLabelText('验收标准'), { target: { value: '评审通过并形成行动项' } })
    await fireEvent.click(screen.getByRole('button', { name: '保存' }))

    expect(todoMutations.updateTodo).toHaveBeenCalledWith(
      {
        todoID: 'task-1',
        body: expect.objectContaining({
          title: '汇总并评审客户访谈反馈',
          priority: 'urgent',
          assignee_id: 'agent-research',
          assignee_name: '研究 Agent',
          assignee_kind: 'agent',
          acceptance_criteria: '评审通过并形成行动项',
          version: 3
        })
      },
      expect.any(Object)
    )
    expect(todoMutations.updateTodo.mock.calls[0]![0].body).not.toHaveProperty('blocked_reason')

    await fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'blocked' } })
    await fireEvent.input(screen.getByLabelText('阻塞原因'), { target: { value: '等待数据授权' } })
    await fireEvent.click(screen.getByRole('button', { name: '应用' }))
    expect(todoMutations.moveTodo).toHaveBeenCalledWith(
      { todoID: 'task-1', body: { status: 'blocked', blocked_reason: '等待数据授权', version: 3 } },
      expect.any(Object)
    )

    await fireEvent.input(screen.getByLabelText('回复内容'), { target: { value: '已申请数据权限' } })
    await fireEvent.click(screen.getByRole('button', { name: '发送回复' }))
    expect(todoMutations.createTodoComment).toHaveBeenCalledWith(
      { todoID: 'task-1', body: { body: '已申请数据权限' } },
      expect.any(Object)
    )
  })

  it('shows the Agent proposal and links the approval and group discussion', () => {
    todoItem.automation = {
      proposal_id: 'proposal-1',
      approval_id: 'todo-execution-proposal-1',
      todo_id: 'task-1',
      todo_version: 3,
      status: 'pending_approval',
      conversation_id: 'discussion-1',
      proposal: '## 执行步骤\n\n1. 汇总证据\n2. 交叉评审',
      plan: { version: 1, summary: '汇总证据并交叉评审', steps: [{ step_id: 'step-1', title: '汇总证据' }] },
      requested_by: 'todo-automation-agent',
      approver_id: 'operator',
      created_at: '2026-07-11T11:30:00Z',
      updated_at: '2026-07-11T11:30:00Z'
    }

    render(TodoDetailPage, { props: { data: { todoId: 'task-1' } } })

    expect(screen.getByRole('heading', { name: 'Agent 执行方案' })).toBeTruthy()
    expect(screen.getByText('待审批')).toBeTruthy()
    expect(screen.getAllByText('汇总证据').length).toBeGreaterThanOrEqual(1)
    expect(document.querySelector('a[href="/discussions/discussion-1"]')).toBeTruthy()
    expect(Array.from(document.querySelectorAll('a')).map(link => link.getAttribute('href'))).toContain('/work-items?view=inbox&action=approval%3Atodo-execution-proposal-1')
  })

  it('shows a durable Mission pause after the Todo invalidates an approved plan', () => {
    todoItem.execution = {
      current: {
        binding_id: 'binding-1',
        todo_id: 'task-1',
        mission_id: 'mission-1',
        conversation_id: 'discussion-1',
        proposal_id: 'proposal-1',
        todo_version: 3,
        execution_attempt: 1,
        plan_digest: 'a'.repeat(64),
        mission_task_ids: ['task-mission-1'],
        status: 'paused',
        pause_reason: 'Todo changed after the execution plan was prepared',
        created_by: 'operator',
        created_at: '2026-07-11T11:30:00Z',
        updated_at: '2026-07-11T12:00:00Z',
        version: 2
      },
      attempts: [{
        todo_id: 'task-1',
        execution_attempt: 1,
        mission_id: 'mission-1',
        mission_task_ids: ['task-mission-1'],
        proposal_id: 'proposal-1',
        todo_version: 3,
        plan_digest: 'a'.repeat(64),
        status: 'paused',
        failure_reason: 'Todo changed after the execution plan was prepared',
        started_at: '2026-07-11T11:30:00Z',
        completed_at: '2026-07-11T12:00:00Z',
        updated_at: '2026-07-11T12:00:00Z'
      }]
    }

    render(TodoDetailPage, { props: { data: { todoId: 'task-1' } } })

    expect(screen.getAllByText('关联任务已暂停').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Todo 内容已变更，原执行方案失效；关联任务已暂停，等待新方案审批。').length).toBeGreaterThanOrEqual(1)
  })

  it('edits idea governance fields and records a review decision', async () => {
    render(IdeaDetailPage, { props: { data: { ideaId: 'idea-1' } } })

    expect(screen.getByRole('heading', { name: '建立项目风险雷达' })).toBeTruthy()
    expect(screen.getByText('IDEA-2001')).toBeTruthy()
    expect(screen.getAllByText('本部门').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('5')).toBeTruthy()

    await fireEvent.click(screen.getAllByRole('button', { name: '编辑' })[0])
    await fireEvent.change(screen.getByLabelText('可见范围'), { target: { value: 'organization' } })
    await fireEvent.change(screen.getByLabelText('预计投入'), { target: { value: 'large' } })
    await fireEvent.change(screen.getByLabelText('负责人'), { target: { value: 'agent-research' } })
    await fireEvent.input(screen.getByLabelText('决策依据'), { target: { value: '先以研发团队为试点。' } })
    await fireEvent.click(screen.getByRole('button', { name: '保存' }))

    expect(ideaMutations.updateIdea).toHaveBeenCalledWith(
      {
        ideaID: 'idea-1',
        body: expect.objectContaining({
          visibility: 'organization',
          effort: 'large',
          owner_id: 'agent-research',
          decision_reason: '先以研发团队为试点。',
          version: 2
        })
      },
      expect.any(Object)
    )

    await fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'accepted' } })
    await fireEvent.input(screen.getByLabelText('决策依据'), { target: { value: '价值和边界均已明确。' } })
    await fireEvent.click(screen.getByRole('button', { name: '应用' }))
    expect(ideaMutations.transitionIdea).toHaveBeenCalledWith(
      {
        ideaID: 'idea-1',
        body: expect.objectContaining({ status: 'accepted', decision_reason: '价值和边界均已明确。', version: 2 })
      },
      expect.any(Object)
    )

    await fireEvent.input(screen.getByLabelText('回复内容'), { target: { value: '安排试点评审' } })
    await fireEvent.click(screen.getByRole('button', { name: '发送回复' }))
    expect(ideaMutations.createIdeaComment).toHaveBeenCalledWith(
      { ideaID: 'idea-1', body: { body: '安排试点评审' } },
      expect.any(Object)
    )
  })
})

function commentPage(kind: 'todo' | 'idea', issueId: string): IssueCommentPageDTO {
  return {
    comments: [
      commentFixture({ comment_id: 'comment-1', issue_kind: kind, issue_id: issueId, author_name: '产品经理', body: '产品侧补充' }),
      commentFixture({ comment_id: 'comment-2', issue_kind: kind, issue_id: issueId, author_name: '工程师', body: '工程侧 **已确认**' })
    ],
    total: 2,
    limit: 100,
    offset: 0,
    has_more: false
  }
}

function commentFixture(overrides: Partial<IssueCommentPageDTO['comments'][number]> = {}): IssueCommentPageDTO['comments'][number] {
  return {
    comment_id: 'comment-1',
    issue_kind: 'todo',
    issue_id: 'task-1',
    body: '回复',
    author_id: 'user-1',
    author_name: '用户',
    created_at: '2026-07-11T12:00:00Z',
    updated_at: '2026-07-11T12:00:00Z',
    ...overrides
  }
}

function resetMutation(fn: ReturnType<typeof vi.fn>) {
  fn.mockReset()
  fn.mockImplementation((_payload, options) => options?.onSuccess?.())
}

function mutationObjects(source: Record<string, ReturnType<typeof vi.fn>>) {
  return Object.fromEntries(Object.entries(source).map(([key, mutate]) => [key, { mutate, isPending: false }]))
}
