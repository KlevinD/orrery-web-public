export type ApprovalFocus = 'queue' | 'council'
export interface ApprovalRoute {
  focus: ApprovalFocus
  workflowId: string
  councilId: string
}

export function parseApprovalRoute(url: URL): ApprovalRoute {
  const workflowId = url.searchParams.get('workflow_id')?.trim() ?? ''
  const councilId = url.searchParams.get('council')?.trim() ?? ''
  const legacyMode = url.searchParams.get('mode')
  const focus = councilId || legacyMode === 'council' ? 'council' : 'queue'

  return { focus, workflowId, councilId }
}

export function mergeApprovalItems<T extends { id: string }>(...groups: T[][]): T[] {
  const merged = new Map<string, T>()
  for (const group of groups) {
    for (const item of group) {
      const id = item.id.trim()
      if (id && !merged.has(id)) merged.set(id, item)
    }
  }
  return [...merged.values()]
}

export function resolveWorkflowSelection(items: Array<{ id: string }>, selectedId: string, linkedWorkflowId: string) {
  const selected = selectedId.trim()
  const linked = linkedWorkflowId.trim()
  if (linked && selected === linked) return selected
  if (items.some((item) => item.id === selected)) return selected
  return items[0]?.id ?? ''
}
