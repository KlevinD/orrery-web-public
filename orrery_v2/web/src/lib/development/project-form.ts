import type { UpdateDevelopmentProjectRequest } from '#lib/types/development-project'

export type DevelopmentProjectMemberSubjectKind = 'user' | 'agent'
export type DevelopmentProjectMemberRole = 'owner' | 'maintainer' | 'contributor' | 'viewer' | 'executor' | 'merger'

export interface DevelopmentProjectEditForm {
  name: string
  description: string
  automationMode: 'manual' | 'suggest' | 'managed'
  maxConcurrentRuns: string
  approvalRequired: boolean
  networkMode: 'none' | 'restricted' | 'allowed'
  allowedHosts: string
  allowedCommands: string
  allowedFiles: string
  allowedSecrets: string
  clearSecrets: boolean
}

export interface DevelopmentProjectEditCapabilities {
  canUpdate: boolean
  canConfigure: boolean
}

const userRoles: DevelopmentProjectMemberRole[] = ['owner', 'maintainer', 'contributor', 'viewer', 'merger']
const agentRoles: DevelopmentProjectMemberRole[] = ['contributor', 'viewer', 'executor']

export function developmentProjectMemberRoles(kind: DevelopmentProjectMemberSubjectKind): DevelopmentProjectMemberRole[] {
  return [...(kind === 'agent' ? agentRoles : userRoles)]
}

export function defaultDevelopmentProjectMemberRole(kind: DevelopmentProjectMemberSubjectKind): DevelopmentProjectMemberRole {
  return kind === 'agent' ? 'executor' : 'contributor'
}

export function validDevelopmentProjectMemberRole(kind: DevelopmentProjectMemberSubjectKind, role: string): role is DevelopmentProjectMemberRole {
  return developmentProjectMemberRoles(kind).includes(role as DevelopmentProjectMemberRole)
}

export function buildDevelopmentProjectUpdateRequest(
  form: DevelopmentProjectEditForm,
  capabilities: DevelopmentProjectEditCapabilities
): UpdateDevelopmentProjectRequest {
  const body: UpdateDevelopmentProjectRequest = {}
  if (capabilities.canUpdate) {
    body.name = form.name.trim()
    body.description = form.description.trim()
  }
  if (capabilities.canConfigure) {
    const secretRefs = lines(form.allowedSecrets)
    const executionPolicy: NonNullable<UpdateDevelopmentProjectRequest['execution_policy']> = {
      approval_required: form.approvalRequired,
      network_mode: form.networkMode,
      allowed_hosts: lines(form.allowedHosts),
      allowed_command_prefixes: lines(form.allowedCommands),
      allowed_file_scopes: lines(form.allowedFiles)
    }
    if (secretRefs.length > 0 || form.clearSecrets) executionPolicy.allowed_secret_refs = secretRefs
    body.default_automation_mode = form.automationMode
    body.max_concurrent_runs = Number(form.maxConcurrentRuns)
    body.execution_policy = executionPolicy
  }
  return body
}

function lines(value: string): string[] {
  return value.split(/\r?\n|,/).map((item) => item.trim()).filter(Boolean)
}
