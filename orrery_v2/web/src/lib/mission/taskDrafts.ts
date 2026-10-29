export interface TaskCommandDraft {
  assigneeId: string
  resultSummary: string
  qaMessage: string
}

export type TaskDraftMap = Record<string, TaskCommandDraft>

export function getTaskDraft(drafts: TaskDraftMap, taskId: string): TaskCommandDraft {
  return drafts[taskId] ?? { assigneeId: '', resultSummary: '', qaMessage: '' }
}

export function updateTaskDraft(drafts: TaskDraftMap, taskId: string, patch: Partial<TaskCommandDraft>): TaskDraftMap {
  return { ...drafts, [taskId]: { ...getTaskDraft(drafts, taskId), ...patch } }
}
