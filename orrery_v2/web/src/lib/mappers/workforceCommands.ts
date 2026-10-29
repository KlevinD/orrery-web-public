import type {
  CreateOrganizationCommand,
  CreateOrganizationPositionCommand,
  CreateOrganizationUnitCommand,
  HireTalentFromMarketCommand,
  UpsertWorkforceMemberCommand,
  WorkforceSkillRefDTO,
  WorkforceToolRefDTO
} from '#lib/types/api'
import type {
  OrganizationCreateFormViewModel,
  OrganizationPositionCreateFormViewModel,
  OrganizationUnitCreateFormViewModel,
  TalentMarketHireFormViewModel,
  WorkforceMemberFormViewModel
} from '#lib/types/view-models'

export function mapWorkforceMemberCommand(form: WorkforceMemberFormViewModel): UpsertWorkforceMemberCommand {
  const isHuman = form.agentKind === 'human'
  const displayName = optionalText(form.displayName) || optionalText(form.realName)
  const personaText = optionalText(form.resume)
  const runtimeKind = optionalText(form.runtimeKind)
  const runtimeConfigRef = optionalText(form.runtimeConfigRef)
  const runtimeConfig = optionalRecord(form.runtimeConfig)
  const delegatesCapabilitiesToRuntime = !isHuman && form.personalAssistant !== true && (runtimeKind.length > 0 || runtimeConfigRef.length > 0)
  const command: UpsertWorkforceMemberCommand = {
    real_name: optionalText(form.realName) || displayName || undefined,
    display_name: displayName || undefined,
    email: optionalText(form.mailboxEmail) || undefined,
    agent_kind: form.agentKind,
    employment_type: optionalText(form.employmentType) || (isHuman ? 'human' : 'ai_agent'),
    organization_id: form.organizationId.trim(),
    department_id: form.departmentId.trim(),
    position_id: form.positionId.trim(),
    role_ids: Array.from(new Set([
      ...splitList(form.roles),
      ...splitList(form.functionalRoles ?? '')
    ])),
    workspace_root: !isHuman ? optionalText(form.workspaceRoot) || undefined : undefined,
    runtime_kind: !isHuman ? runtimeKind || undefined : undefined,
    runtime_config_ref: !isHuman ? runtimeConfigRef || undefined : undefined,
    runtime_config: !isHuman && !runtimeConfigRef ? runtimeConfig : undefined,
    system_prompt: !isHuman ? optionalText(form.prompt) || undefined : undefined,
    qa_enabled: !isHuman && form.qaEnabled === 'true',
    enabled: form.enabled === 'true'
  }
  const agentProfileId = optionalText(form.agentProfileId)
  if (agentProfileId) command.agent_profile_id = agentProfileId
  if (isHuman) {
    const username = optionalText(form.accountUsername)
    const email = optionalText(form.accountEmail)
    if (username || email || form.accountEnabled === 'true') {
      command.user_account = {
        username: username || undefined,
        email: email || undefined,
        account_enabled: form.accountEnabled === 'true'
      }
    }
  }
  const responsibilityBoundary = optionalText(form.responsibilityBoundary)
  const behaviorBoundary = optionalText(form.behaviorBoundary)
  if (personaText || responsibilityBoundary || behaviorBoundary) {
    command.persona = {
      backstory: personaText,
      responsibility_boundary: responsibilityBoundary,
      behavior_boundary: behaviorBoundary
    }
  }
  if (!isHuman && !delegatesCapabilitiesToRuntime) {
    const skillRefs = parseSkillRefs(optionalText(form.skillRefs))
    if (skillRefs) command.skill_refs = skillRefs
    const toolRefs = parseToolRefs(optionalText(form.toolRefs))
    if (toolRefs) command.tool_refs = toolRefs
  }
  return command
}

export function mapTalentMarketHireCommand(form: TalentMarketHireFormViewModel): HireTalentFromMarketCommand {
  const command: HireTalentFromMarketCommand = {
    agent_profile_id: form.agentProfileId.trim(),
    organization_id: form.organizationId.trim(),
    department_id: form.departmentId.trim(),
    position_id: form.positionId.trim()
  }
  const roleIds = splitList(form.roles || '')
  if (roleIds.length > 0) command.role_ids = roleIds
  const managerAgentId = optionalText(form.managerId)
  const reportingLineId = optionalText(form.reportingLineId)
  if (managerAgentId) command.manager_agent_id = managerAgentId
  if (reportingLineId) command.reporting_line_id = reportingLineId
  return command
}

function optionalText(value: string | undefined) {
  return (value ?? '').trim()
}

function optionalRecord(value: Record<string, unknown> | undefined) {
  const entries = Object.entries(value ?? {}).filter(([, entry]) => {
    if (typeof entry === 'string') return entry.trim().length > 0
    if (Array.isArray(entry)) return entry.length > 0
    return entry !== undefined && entry !== null
  })
  return entries.length > 0 ? Object.fromEntries(entries) : undefined
}

function parseSkillRefs(value: string): WorkforceSkillRefDTO[] | undefined {
  const refs = splitList(value).map((raw) => {
    const level = bracketValue(raw)
    const withoutLevel = raw.replace(/\[[^\]]+\]/g, '').trim()
    const [idAndVersion, aliasPart = ''] = withoutLevel.split(/\s+as\s+/i)
    const [skillId, versionConstraint = ''] = idAndVersion.split('@')
    return {
      skill_id: skillId.trim(),
      version_constraint: versionConstraint.trim() || undefined,
      alias: aliasPart.trim() || undefined,
      level: level || undefined
    }
  }).filter((ref) => ref.skill_id)
  return refs.length > 0 ? refs : undefined
}

function parseToolRefs(value: string): WorkforceToolRefDTO[] | undefined {
  const refs = splitList(value).map((raw) => {
    const tokens = raw.split(/\s+/).filter(Boolean)
    const [idAndVersion = '', ...rest] = tokens
    const [toolId, versionConstraint = ''] = idAndVersion.split('@')
    const asIndex = rest.findIndex((token) => token.toLowerCase() === 'as')
    const alias = asIndex >= 0 ? rest[asIndex + 1] : ''
    const scope = prefixedToken(rest, 'scope=')
    const policy = prefixedToken(rest, 'policy=')
    return {
      tool_id: toolId.trim(),
      version_constraint: versionConstraint.trim() || undefined,
      alias: alias || undefined,
      permission_scope: scope || undefined,
      binding_policy: policy || undefined
    }
  }).filter((ref) => ref.tool_id)
  return refs.length > 0 ? refs : undefined
}

function splitList(value: string) {
  return value
    .split(/[,;\n]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function bracketValue(value: string) {
  return value.match(/\[([^\]]+)\]/)?.[1]?.trim() || ''
}

function prefixedToken(tokens: string[], prefix: string) {
  return tokens.find((token) => token.startsWith(prefix))?.slice(prefix.length).trim() || ''
}

export function mapOrganizationCreateCommand(form: OrganizationCreateFormViewModel): CreateOrganizationCommand {
  return {
    name: form.name.trim()
  }
}

export function mapOrganizationUnitCreateCommand(form: OrganizationUnitCreateFormViewModel): CreateOrganizationUnitCommand {
  const command: CreateOrganizationUnitCommand = {
    organization_id: form.organizationId.trim(),
    name: form.name.trim(),
    parent_department_id: form.parentDepartmentId.trim() || undefined
  }
  const defaultPositionName = optionalText(form.defaultPositionName)
  if (defaultPositionName) command.default_position_name = defaultPositionName
  return command
}

export function mapOrganizationPositionCreateCommand(form: OrganizationPositionCreateFormViewModel): CreateOrganizationPositionCommand {
  return {
    organization_id: form.organizationId.trim(),
    department_id: optionalText(form.departmentId) || undefined,
    name: form.name.trim()
  }
}
