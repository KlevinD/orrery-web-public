import type { ApprovalItemViewModel, HILDecisionItemViewModel, HILWorkflowInstanceViewModel } from '#lib/types/view-models'
import type { TodoItemDTO } from '#lib/types/todo'
import type { ScheduledRunInboxItemDTO } from '#lib/types/scheduler'

export type WorkActionView = 'pending' | 'history' | 'requested'
export type WorkActionKind = 'approval' | 'reply' | 'todo' | 'council' | 'result'
export interface WorkActionItem {
  id: string
  kind: WorkActionKind
  title: string
  summary: string
  status: string
  date: string
  requester: string
  assignee: string
  source: string
  deadline?: string
  pending: boolean
  priority: number
  todo?: TodoItemDTO
  approval?: ApprovalItemViewModel
  council?: HILDecisionItemViewModel
  result?: ScheduledRunInboxItemDTO
  workflowId?: string
}

export function workActionHref(id: string, view: WorkActionView = 'pending') {
  return `/work-items?view=${view === 'pending' ? 'inbox' : view}&action=${encodeURIComponent(id)}`
}

export function isHumanTodo(todo: TodoItemDTO) {
  return todo.assignee_kind === 'human' && todo.work_kind === 'general' && todo.automation_mode === 'manual'
}

export function actorMatches(value: string, actors: readonly string[]) {
  const normalized = value.replace(/^(human|user|agent):/, '')
  return Boolean(normalized) && actors.some(actor => actor.replace(/^(human|user|agent):/, '') === normalized)
}

export function isPendingApproval(status: string) {
  return ['', 'pending', 'open'].includes(status.trim().toLowerCase())
}

export function buildWorkActions(todos: TodoItemDTO[], approvals: ApprovalItemViewModel[], councils: HILDecisionItemViewModel[], results: ScheduledRunInboxItemDTO[] = []): WorkActionItem[] {
  const items = new Map<string, WorkActionItem>()
  for (const todo of todos) {
    const terminal = ['done', 'canceled'].includes(todo.status)
    const deliveryPending = todo.source?.delivery_status === 'pending'
    const row: WorkActionItem = {
      id: `todo:${todo.todo_id}`, kind: 'todo', title: todo.title,
      summary: todo.blocked_reason || todo.description || '', status: terminal && deliveryPending ? 'delivery_pending' : todo.status,
      date: todo.completed_at || todo.updated_at || todo.created_at, requester: todo.created_by || '',
      assignee: todo.assignee_name || todo.assignee_id || '', source: todo.source ? 'agent' : 'todo', deadline: todo.due_at,
      pending: !terminal || deliveryPending, priority: ({ urgent: 0, high: 1, medium: 2, low: 3 })[todo.priority] ?? 2, todo
    }
    items.set(row.id, row)
  }
  for (const approval of approvals) {
    // HIL-backed legacy records have their own canonical entry. Never deduplicate by title.
    if (!approval.sourceLabel?.startsWith('hil:') && approval.payloadPreview.fields.some(field => field.key === 'hil_decision_id' && field.value)) continue
    const id = `approval:${approval.hilDecisionId || approval.id}`
    if (items.has(id)) continue
    items.set(id, {
      id, kind: approval.displayKind === 'user_input' ? 'reply' : 'approval', title: approval.title, summary: approval.summary,
      status: approval.status, date: approval.createdAtRaw, requester: approval.requestedBy, assignee: approval.assignedToName || approval.assignedTo,
      source: approval.payloadPreview.kind === 'business_approval' ? 'business' : 'agent', deadline: approval.expiresAtRaw,
      pending: isPendingApproval(approval.status), priority: 0, approval
    })
  }
  for (const council of councils) {
    // A council that has escalated to human approval is represented by its current approval entry.
    if (council.status === 'pending' && council.routeMode !== 'council' && !council.handled) continue
    const id = `council:${council.councilId || council.id}`
    items.set(id, { id, kind: 'council', title: council.purpose || council.action, summary: council.purpose, status: council.handled && council.status === 'pending' ? 'handled' : council.status,
      date: council.createdAtRaw, requester: council.requester, assignee: '', source: 'agent', deadline: council.expiresAtRaw,
      pending: council.status === 'pending' && !council.handled, priority: 0, council })
  }
  for (const result of results) {
    if (!result.attention_required) continue
    const id = `result:${result.run.scheduled_run_id}`
    items.set(id, { id, kind: 'result', title: result.task_title, summary: result.run.failure_reason || result.run.result?.summary || '',
      status: result.run.status, date: result.run.completed_at || result.run.created_at, requester: '', assignee: '', source: 'scheduler',
      pending: true, priority: 1, result })
  }
  return [...items.values()].sort((a, b) => a.priority - b.priority || Date.parse(b.date) - Date.parse(a.date) || a.id.localeCompare(b.id))
}

export function filterWorkActions(items: WorkActionItem[], view: WorkActionView, actors: readonly string[]) {
  return items.filter(item => view === 'pending' ? item.pending : view === 'history' ? !item.pending : actorMatches(item.requester, actors))
}

export function mergeWorkActionWorkflows(items: WorkActionItem[], workflows: Array<HILWorkflowInstanceViewModel & { workApproval: ApprovalItemViewModel }>, view: WorkActionView, actors: readonly string[]) {
  const rows = new Map(items.map(item => [item.id, item]))
  for (const workflow of workflows) {
    const mine = workflow.tasks.filter(task => actorMatches(task.assignee, actors) || actorMatches(task.originalAssignee, actors))
    const handled = mine.some(task => !['scheduled', 'pending'].includes(task.status)) && !mine.some(task => task.actionable)
    if (view === 'requested' && !actorMatches(workflow.requester, actors)) continue
    if (view === 'history' && !handled) continue
    const row = buildWorkActions([], [workflow.workApproval], [])[0]
    if (!row) continue
    row.workflowId = workflow.id
    if (view === 'history' && handled) {
      row.pending = false
      if (workflow.status === 'running') row.status = 'handled'
    }
    rows.set(row.id, row)
  }
  return [...rows.values()]
}

export function legacyApprovalHref(url: URL) {
  const params = new URLSearchParams(url.search)
  params.set('view', params.get('tab') === 'history' || params.get('view') === 'history' ? 'history' : 'inbox')
  const decision = params.get('decision_id') || params.get('approval_id') || params.get('decision')
  const council = params.get('council')
  if (decision) params.set('action', `approval:${decision}`)
  else if (council) params.set('action', `council:${council}`)
  else if (params.get('mode') === 'council') params.set('kind', 'council')
  return `/work-items?${params}${url.hash}`
}
