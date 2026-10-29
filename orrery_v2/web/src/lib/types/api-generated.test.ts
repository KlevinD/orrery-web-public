import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join, relative } from 'node:path'
import process from 'node:process'
import { describe, expect, it } from 'vitest'
import type { components, paths } from './api.generated'

describe('generated OpenAPI types', () => {
  it('exposes mission-control paths to frontend modules', () => {
    type HasMissionList = '/api/missions' extends keyof paths ? true : false
    type HasOAuth2ConsentPending = '/api/oauth2/consent/pending' extends keyof paths ? true : false
    type HasOntologyGovernanceProposals = '/api/ontology/governance/proposals' extends keyof paths
      ? true
      : false
    type HasOntologyProjectionAudits = '/api/ontology/projections/audits' extends keyof paths
      ? true
      : false
    type HasOntologyAgentRuns = '/api/ontology/executions/runs/{run_id}' extends keyof paths ? true : false
    type HasOntologyActionInvoke = '/api/ontology/actions/{action_type_id}/invoke' extends keyof paths
      ? true
      : false
    type HasOntologyReliabilityIncident = '/api/ontology/reliability/incidents/{incident_id}' extends keyof paths
      ? true
      : false
    type HasOntologyProblemList = '/api/ontology/problems' extends keyof paths ? true : false
    type HasKratosLoginFlow = '/api/auth/identity/{provider}/login-flow' extends keyof paths ? true : false
    type HasDevelopmentProjects = '/api/development-projects' extends keyof paths ? true : false
    type HasDevelopmentRepositoryVerification = '/api/development-projects/{development_project_id}/repositories/{binding_id}/verify' extends keyof paths ? true : false
    type HasSCMConnections = '/api/scm-connections' extends keyof paths ? true : false
    type HasResponsibilityEvaluationItems = '/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/responsibility-evaluations' extends keyof paths ? true : false
    type HasScheduledRunInbox = '/api/scheduled-runs' extends keyof paths ? true : false
    type HasScheduledRunReadState = '/api/scheduled-runs/{scheduled_run_id}/read' extends keyof paths ? true : false
    const hasMissionList: HasMissionList = true
    const hasOAuth2ConsentPending: HasOAuth2ConsentPending = true
    const hasOntologyGovernanceProposals: HasOntologyGovernanceProposals = true
    const hasOntologyProjectionAudits: HasOntologyProjectionAudits = true
    const hasOntologyAgentRuns: HasOntologyAgentRuns = true
    const hasOntologyActionInvoke: HasOntologyActionInvoke = true
    const hasOntologyReliabilityIncident: HasOntologyReliabilityIncident = true
    const hasOntologyProblemList: HasOntologyProblemList = true
    const hasKratosLoginFlow: HasKratosLoginFlow = true
    const hasDevelopmentProjects: HasDevelopmentProjects = true
    const hasDevelopmentRepositoryVerification: HasDevelopmentRepositoryVerification = true
    const hasSCMConnections: HasSCMConnections = true
    const hasResponsibilityEvaluationItems: HasResponsibilityEvaluationItems = true
    const hasScheduledRunInbox: HasScheduledRunInbox = true
    const hasScheduledRunReadState: HasScheduledRunReadState = true
    const flowErrorCodes: components['schemas']['ErrorResponse']['code'][] = [
      'flow_cookie_invalid',
      'flow_not_found',
      'flow_expired'
    ]
    const loginFlow: components['schemas']['BrowserLoginFlow'] = {
      id: 'flow-1',
      action: 'https://identity.example/self-service/login?flow=flow-1',
      method: 'POST',
      nodes: [],
      messages: []
    }
    const responsibilityEvaluationBatch: components['schemas']['ResponsibilityEvaluationBatchResultDTO'] = {
      scope_id: 'people-tenant-1',
      discovered_count: 2,
      evaluated_count: 2,
      no_action_count: 1,
      linked_existing_mission_count: 1,
      materialized_count: 0,
      skipped_count: 0,
      failed_count: 0,
      candidate_count: 3,
      actionable_count: 1,
      waiting_count: 1,
      review_count: 1,
      escalated_count: 0,
      paused_count: 0,
      invalid_count: 0
    }

    expect(hasMissionList).toBe(true)
    expect(hasOAuth2ConsentPending).toBe(true)
    expect(hasOntologyGovernanceProposals).toBe(true)
    expect(hasOntologyProjectionAudits).toBe(true)
    expect(hasOntologyAgentRuns).toBe(true)
    expect(hasOntologyActionInvoke).toBe(true)
    expect(hasOntologyReliabilityIncident).toBe(true)
    expect(hasOntologyProblemList).toBe(true)
    expect(hasKratosLoginFlow).toBe(true)
    expect(hasDevelopmentProjects).toBe(true)
    expect(hasDevelopmentRepositoryVerification).toBe(true)
    expect(hasSCMConnections).toBe(true)
    expect(hasResponsibilityEvaluationItems).toBe(true)
    expect(hasScheduledRunInbox).toBe(true)
    expect(hasScheduledRunReadState).toBe(true)
    expect(flowErrorCodes).toHaveLength(3)
    expect(loginFlow.method).toBe('POST')
    expect(responsibilityEvaluationBatch.evaluated_count).toBe(2)
  })

  it('keeps handwritten API DTOs split behind a small barrel', () => {
    const apiEntrypoint = webProjectPath('src/lib/types/api.ts')
    const apiSource = readFileSync(apiEntrypoint, 'utf8')
    const apiTypesDir = webProjectPath('src/lib/types/api')
    const oversized = collectTypeScriptFiles(apiTypesDir)
      .map((file) => ({
        name: relative(apiTypesDir, file).replaceAll('\\', '/'),
        lineCount: readFileSync(file, 'utf8').split(/\r?\n/).length
      }))
      .filter((item) => item.lineCount > 800)

    expect(apiSource.split(/\r?\n/).length).toBeLessThanOrEqual(220)
    expect(apiSource).not.toContain('export interface SettingsDTO')
    expect(apiSource).not.toContain('export interface MissionDTO')
    expect(oversized).toEqual([])
  })

  it('keeps frontend view models split behind a small barrel', () => {
    const entrypoint = webProjectPath('src/lib/types/view-models.ts')
    const source = readFileSync(entrypoint, 'utf8')
    const viewModelsDir = webProjectPath('src/lib/types/view-models')
    const oversized = collectTypeScriptFiles(viewModelsDir)
      .map((file) => ({
        name: relative(viewModelsDir, file).replaceAll('\\', '/'),
        lineCount: readFileSync(file, 'utf8').split(/\r?\n/).length
      }))
      .filter((item) => item.lineCount > 800)

    expect(source.split(/\r?\n/).length).toBeLessThanOrEqual(220)
    expect(source).not.toContain('export interface SettingsViewModel')
    expect(source).not.toContain('export interface WorkforceMemberViewModel')
    expect(oversized).toEqual([])
  })

  it('keeps the settings route as a small shell with focused settings components', () => {
    const route = webProjectPath('src/routes/settings/+page.svelte')
    const source = readFileSync(route, 'utf8')
    const settingsComponentsDir = webProjectPath('src/lib/components/settings')
    const oversized = collectSvelteAndTypeScriptFiles(settingsComponentsDir)
      .map((file) => ({
        name: relative(settingsComponentsDir, file).replaceAll('\\', '/'),
        lineCount: readFileSync(file, 'utf8').split(/\r?\n/).length
      }))
      .filter((item) => item.lineCount > 800)

    expect(source.split(/\r?\n/).length).toBeLessThanOrEqual(220)
    expect(source).toContain("import SettingsWorkspace from '#lib/components/settings/SettingsWorkspace.svelte'")
    expect(source).not.toContain("activeTab === 'runtime'")
    expect(source).not.toContain('<RuntimeProviderConfigForm')
    expect(oversized).toEqual([])
  })
})

function collectTypeScriptFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return collectTypeScriptFiles(path)
    return entry.name.endsWith('.ts') ? [path] : []
  })
}

function collectSvelteAndTypeScriptFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return collectSvelteAndTypeScriptFiles(path)
    return entry.name.endsWith('.svelte') || entry.name.endsWith('.ts') ? [path] : []
  })
}

function webProjectPath(path: string): string {
  const candidates = [join(process.cwd(), path), join(process.cwd(), 'web', path)]
  const found = candidates.find((candidate) => existsSync(candidate))
  if (!found) throw new Error(`Unable to locate web project path: ${path}`)
  return found
}
