import { describe, expect, it } from 'vitest'
import {
  buildDevelopmentProjectUpdateRequest,
  defaultDevelopmentProjectMemberRole,
  developmentProjectMemberRoles,
  type DevelopmentProjectEditForm
} from './project-form'

const form: DevelopmentProjectEditForm = {
  name: ' Updated project ',
  description: ' Updated description ',
  automationMode: 'managed',
  maxConcurrentRuns: '4',
  approvalRequired: false,
  networkMode: 'restricted',
  allowedHosts: 'github.com',
  allowedCommands: 'go test\nnpm test',
  allowedFiles: 'internal/**',
  allowedSecrets: 'secret://scm/token',
  clearSecrets: false
}

describe('development project edit capabilities', () => {
  it('omits configure-only fields for contributors', () => {
    expect(buildDevelopmentProjectUpdateRequest(form, { canUpdate: true, canConfigure: false })).toEqual({
      name: 'Updated project',
      description: 'Updated description'
    })
  })

  it('omits ordinary fields for configure-only viewers', () => {
    expect(buildDevelopmentProjectUpdateRequest(form, { canUpdate: false, canConfigure: true })).toEqual({
      default_automation_mode: 'managed',
      max_concurrent_runs: 4,
      execution_policy: {
        approval_required: false,
        network_mode: 'restricted',
        allowed_hosts: ['github.com'],
        allowed_command_prefixes: ['go test', 'npm test'],
        allowed_file_scopes: ['internal/**'],
        allowed_secret_refs: ['secret://scm/token']
      }
    })
  })
})

describe('development project member roles', () => {
  it('keeps merge authority human-only and execution authority agent-only', () => {
    expect(developmentProjectMemberRoles('user')).toEqual(['owner', 'maintainer', 'contributor', 'viewer', 'merger'])
    expect(developmentProjectMemberRoles('agent')).toEqual(['contributor', 'viewer', 'executor'])
    expect(defaultDevelopmentProjectMemberRole('user')).toBe('contributor')
    expect(defaultDevelopmentProjectMemberRole('agent')).toBe('executor')
  })
})
