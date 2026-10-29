const activeStatuses = new Set(['active', 'started', 'created', 'assigned', 'todo', 'in_progress'])
const doneStatuses = new Set(['done', 'completed', 'approved'])

export function isActiveTaskStatus(status: string) {
  return activeStatuses.has(status)
}

export function isDoneTaskStatus(status: string) {
  return doneStatuses.has(status)
}
