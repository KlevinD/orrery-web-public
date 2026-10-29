import type { WorkforceMemberFormViewModel } from '#lib/types/view-models'

export interface WorkforceMemberEditorOption {
  label: string
  value: string
  meta?: string
  category?: string
  assignableActorTypes?: string[]
  permissionRefs?: string[]
  runtimeKind?: string
  tags?: string[]
  mcpServerId?: string
  mcpServerName?: string
  quickAssignable?: boolean
}

export interface EditorFormState extends Omit<WorkforceMemberFormViewModel, 'runtimeConfig'> {
  resume: string
  prompt: string
  responsibilityBoundary: string
  behaviorBoundary: string
  skillRefs: string
  toolRefs: string
  personalMemoryId: string
  collectiveMemoryRefs: string
  qaEnabled: string
  runtimeConfig: Record<string, string | string[]>
}

export function buildInitialForm(form: Partial<WorkforceMemberFormViewModel>, kind: 'human' | 'agent'): EditorFormState {
  const agentKind = form.agentKind ?? (kind === 'human' ? 'human' : 'ai')
  return {
    agentProfileId: form.agentProfileId,
    personalAssistant: form.personalAssistant,
    agentInstanceId: form.agentInstanceId,
    realName: form.realName ?? '',
    displayName: form.displayName ?? '',
    agentKind,
    accountUsername: form.accountUsername ?? '',
    accountNickname: form.accountNickname ?? '',
    accountEmail: form.accountEmail ?? '',
    accountRole: form.accountRole ?? '',
    accountEnabled: form.accountEnabled ?? (agentKind === 'human' ? 'true' : 'false'),
    accountProjectionStatus: form.accountProjectionStatus ?? '',
    accountProjectionRevision: form.accountProjectionRevision ?? 0,
    accountPermissionScope: form.accountPermissionScope ?? '',
    accountPassword: '',
    accountPasswordConfirm: '',
    employmentType: form.employmentType ?? (kind === 'human' ? 'human' : 'ai_agent'),
    organizationId: form.organizationId ?? '',
    departmentId: form.departmentId ?? '',
    positionId: form.positionId ?? '',
    roles: form.roles ?? '',
    functionalRoles: form.functionalRoles ?? '',
    resume: form.resume ?? '',
    prompt: form.prompt ?? '',
    responsibilityBoundary: form.responsibilityBoundary ?? '',
    behaviorBoundary: form.behaviorBoundary ?? '',
    skillRefs: form.skillRefs ?? '',
    toolRefs: form.toolRefs ?? '',
    personalMemoryId: form.personalMemoryId ?? '',
    collectiveMemoryRefs: form.collectiveMemoryRefs ?? '',
    qaEnabled: form.qaEnabled ?? 'false',
    workspaceRoot: form.workspaceRoot ?? '',
    runtimeKind: form.runtimeKind ?? '',
    runtimeConfigRef: form.runtimeConfigRef ?? '',
    runtimeConfig: form.runtimeConfig ?? {},
    enabled: form.enabled ?? 'true'
  }
}

export function selectedValues(value: string) {
  return value
    .split(/[,;\n]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

export function transferIn(value: string, additions: string[]) {
  const next = selectedValues(value)
  for (const addition of additions) {
    if (!next.includes(addition)) next.push(addition)
  }
  return next.join(', ')
}

export function transferOut(value: string, removals: string[]) {
  const removing = new Set(removals)
  return selectedValues(value).filter((item) => !removing.has(item)).join(', ')
}

export function availableOptions(options: WorkforceMemberEditorOption[], value: string): WorkforceMemberEditorOption[] {
  const selected = new Set(selectedValues(value))
  return options.filter((option) => !selected.has(option.value))
}

export function selectedOptions(options: WorkforceMemberEditorOption[], value: string): WorkforceMemberEditorOption[] {
  const byValue = new Map(options.map((option) => [option.value, option]))
  return selectedValues(value).map((item) => byValue.get(item) ?? { label: item, value: item })
}

export function selectedSummary(options: WorkforceMemberEditorOption[], value: string): WorkforceMemberEditorOption[] {
  return selectedOptions(options, value).slice(0, 6)
}

export function optionsWithSelected(options: WorkforceMemberEditorOption[], value: string): WorkforceMemberEditorOption[] {
  const seen = new Set(options.map((option) => option.value))
  const fallbacks = selectedValues(value)
    .filter((item) => !seen.has(item))
    .map((item) => ({ label: item, value: item }))
  return [...options, ...fallbacks]
}

export function optionsWithCurrent(options: WorkforceMemberEditorOption[], value: string): WorkforceMemberEditorOption[] {
  if (!value || options.some((option) => option.value === value)) return options
  return [{ label: value, value }, ...options]
}
