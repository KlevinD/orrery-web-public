import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('ontology workbench routes', () => {
  it('uses the domain summary queries and shared scope context', () => {
    const source = readFileSync('src/routes/resources/ontology/+page.svelte', 'utf-8')
    expect(source).toContain('useOntologySchemaSummaryQuery')
    expect(source).toContain('useOntologyContextCompanyOverviewQuery')
    expect(source).toContain('useOntologyContextOverviewQuery')
    expect(source).not.toContain('useOntologyContextSummaryQuery')
    expect(source).toContain('useOntologyLineageSummaryQuery')
    expect(source).toContain('useOntologyGovernanceSummaryQuery')
    expect(source).toContain('useOntologyProjectionSummaryQuery')
    expect(source).toContain('ontologyScopeContext.activeScope')
    for (const domain of ['schema', 'context', 'lineage', 'governance', 'projections']) {
      expect(readFileSync(`src/lib/queries/ontology-${domain}.ts`, 'utf-8')).toContain('runOntologySummaryQuery')
    }
    expect(source).not.toContain('!schema.isPending')

    expect(source).toContain("useOntologyContextCompanyOverviewQuery(() => ontologyScopeContext.mode === 'all')")
    expect(source).toContain("useOntologyContextOverviewQuery(tenantId, () => ontologyScopeContext.mode === 'scope')")
  })

  it('provides the new domain routes', () => {
    for (const route of ['schema', 'context', 'problems', 'executions', 'reliability', 'lineage', 'governance', 'operations']) {
      expect(existsSync(`src/routes/resources/ontology/${route}/+page.svelte`)).toBe(true)
    }
  })

  it('uses names that distinguish the model, mapping, and actual data views', () => {
    const schema = readFileSync('src/routes/resources/ontology/schema/+page.svelte', 'utf-8')
    const context = readFileSync('src/routes/resources/ontology/context/+page.svelte', 'utf-8')
    const shell = readFileSync('src/lib/components/ontology/OntologyWorkspaceShell.svelte', 'utf-8')
    expect(schema).toContain("label: '模型关系'")
    expect(schema).toContain("label: '数据映射'")
    expect(context).toContain('实际数据关系图')
    expect(shell).toContain("['/resources/ontology/context', '实际数据']")
    expect(shell).toContain("['/resources/ontology/executions', '智能体执行']")
    expect(shell).toContain("['/resources/ontology/reliability', '可靠性']")
  })

  it('bounds actual-data reads to a selected scope and initializes a graph root from visible objects', () => {
    const context = readFileSync('src/routes/resources/ontology/context/+page.svelte', 'utf-8')
    expect(context).toMatch(/useOntologyContextObjectsQuery\([\s\S]*\(\) => Boolean\(tenantId\(\)\)\s*\)/)
    expect(context).toContain('const firstGraphObject = $derived(')
    expect(context).toContain("const graphRootId = $derived(rootId || selectedId || firstGraphObject?.id || '')")
    expect(context).toContain('请选择授权范围后查看实际数据')
  })

  it('keeps the company range separate from explicit business context', () => {
    const shell = readFileSync('src/lib/components/ontology/OntologyWorkspaceShell.svelte', 'utf-8')
    expect(shell).toContain('scopeOptions')
    expect(shell).toContain('switchScope')
    expect(shell).toContain("{ kind: 'organization', limit: 100 }")
    expect(shell).toContain("label: '公司全景'")
    expect(shell).toContain("activeScope?.kind === 'organization'")
    expect(shell).toContain('activeBusinessContext')
    expect(shell).toContain('业务上下文')
    expect(shell).toContain('返回公司全景')
    expect(shell).not.toContain('授权范围')
    expect(shell).not.toContain("scope.kind === 'system'")
    expect(shell).not.toContain('autoScopeTenant')
    expect(shell).not.toContain('confirmScope')
    expect(shell).not.toContain('范围 ID')
  })

  it('renders aggregate routes without an active-scope gate and binds selected rows to their source tenant', () => {
    for (const route of ['schema', 'context', 'problems', 'executions', 'reliability', 'governance', 'operations/projections']) {
      const source = readFileSync(`src/routes/resources/ontology/${route}/+page.svelte`, 'utf-8')
      expect(source).not.toContain('OntologyNoScopeState')
      expect(source).toContain('source_tenant_id')
      expect(source).toContain('item.tenant_id')
      expect(source).toContain('tenantId()')
    }
    const overview = readFileSync('src/routes/resources/ontology/+page.svelte', 'utf-8')
    const lineage = readFileSync('src/routes/resources/ontology/lineage/+page.svelte', 'utf-8')
    expect(overview).not.toContain('OntologyNoScopeState')
    expect(lineage).not.toContain('OntologyNoScopeState')
    expect(lineage).toContain('选择具体范围后查询事实详情')
    expect(lineage).toContain("status === 'sampled'")
    expect(lineage).toContain('confidence_distribution.unscored')
    expect(readFileSync('src/lib/components/ontology/OntologySchemaGraph.svelte', 'utf-8')).toContain('definition_id')
  })

  it('keeps same-scope browser history and URL-driven state in sync', () => {
    for (const route of ['schema', 'context', 'lineage', 'problems', 'reliability', 'governance', 'operations/projections']) {
      const source = readFileSync(`src/routes/resources/ontology/${route}/+page.svelte`, 'utf-8')
      expect(source).toContain('untrack')
      expect(source).not.toContain('replaceState: true')
    }
  })

  it('exposes the secure governance workflow without accepting client actor or target data', () => {
    const api = readFileSync('src/lib/api/ontology-governance.ts', 'utf-8')
    const workflow = readFileSync('src/lib/components/ontology/OntologyGovernanceWorkflow.svelte', 'utf-8')
    const page = readFileSync('src/routes/resources/ontology/governance/+page.svelte', 'utf-8')
    for (const operation of ['receipts', 'auditEvents', 'decide', 'createPlan', 'applyPlan']) expect(api).toContain(`${operation}:`)
    expect(workflow).toContain('governanceWorkflowKey')
    expect(workflow).toContain('expectedRevision')
    expect(workflow).toContain('idempotencyKey')
    expect(workflow).not.toContain('actor_id')
    expect(page).toContain('useOntologySchemaSummaryQuery')
    expect(page).toContain('治理基线')
    expect(page).toContain('当前无待处理提案')
  })

  it('exposes a reliability control loop without accepting client repair bindings', () => {
    const api = readFileSync('src/lib/api/ontology-reliability.ts', 'utf-8')
    const page = readFileSync('src/routes/resources/ontology/reliability/+page.svelte', 'utf-8')
    const workflow = readFileSync('src/lib/components/ontology/OntologyReliabilityWorkflow.svelte', 'utf-8')
    expect(page).toContain("objectType: 'failure_incident'")
    expect(page).toContain('source_tenant_id')
    expect(page).toContain('useOntologyContextObjectsQuery')
    for (const operation of ['incident', 'requestAuthorization', 'decideAuthorization', 'invokeAuthorization']) expect(api).toContain(`${operation}:`)
    expect(workflow).toContain('useOntologyReliabilityIncidentQuery')
    expect(workflow).toContain('createIdempotencyKey')
    expect(workflow).toContain('current_preflight_required')
    for (const forbidden of ['incident_version:', 'rule_version:', 'args_hash:', 'hil_token_id:', 'receipt_id:']) expect(api).not.toContain(forbidden)
  })

  it('exposes the problem hub as the route into governed organizational memory', () => {
    const api = readFileSync('src/lib/api/ontology-problems.ts', 'utf-8')
    const page = readFileSync('src/routes/resources/ontology/problems/+page.svelte', 'utf-8')
    const inspector = readFileSync('src/lib/components/ontology/OntologyProblemInspector.svelte', 'utf-8')
    expect(page).toContain("objectType: 'problem_case'")
    expect(page).toContain('source_tenant_id')
    expect(page).toContain('useOntologyProblemsQuery')
    for (const operation of ['list', 'problem', 'route']) expect(api).toContain(`${operation}:`)
    expect(inspector).toContain('useOntologyProblemRouteMutation')
    expect(inspector).toContain("'review_outcome'")
    expect(inspector).toContain("'accepted_memory_id'")
    expect(api).toContain("body: JSON.stringify({ owner: { kind: owner.kind.trim(), id: owner.id.trim() } })")
    expect(api).not.toContain('body: JSON.stringify({ actor')
  })

  it('exposes only security-complete advanced operations and preserves active scope', () => {
    const landing = readFileSync('src/routes/resources/ontology/operations/+page.svelte', 'utf-8')
    const projections = readFileSync('src/routes/resources/ontology/operations/projections/+page.svelte', 'utf-8')
    const projectionInspector = readFileSync('src/lib/components/ontology/OntologyProjectionAuditInspector.svelte', 'utf-8')
    const operationsOverview = readFileSync('src/lib/components/ontology/OntologyOperationsOverview.svelte', 'utf-8')

    expect(landing).toContain('/resources/ontology/operations/projections')
    expect(landing).toContain('ontologyScopeContext.activeScope?.tenant_id')
    expect(landing).not.toContain('OntologyNoScopeState')
    expect(landing).toContain('OntologyOperationsOverview')
    expect(operationsOverview).toContain('useOntologyContextObjectsQuery')
    expect(operationsOverview).toContain("view: 'projection'")
    expect(operationsOverview).toContain('最近投影产出')
    expect(projectionInspector).toContain("hasPermissionScope(session.permissionScope, 'operations.manage')")
    expect(projections).not.toContain('{#if !tenantId()}')
    expect(projections).toContain('source_tenant_id')
    expect(projections).toContain('syncRouteState')
    expect(projections).toContain("params.set('reason_code', reasonCode)")
    expect(projections).toContain("params.set('cursor', cursor)")
    expect(projections).toContain('OntologyProjectionAuditInspector')
    for (const deferred of ['reasoning', 'actions', 'models']) {
      expect(existsSync(`src/routes/resources/ontology/operations/${deferred}/+page.svelte`)).toBe(false)
      expect(landing).not.toContain(`/resources/ontology/operations/${deferred}`)
    }
  })

  it('keeps saved graph redirects backed up but disabled', () => {
    const source = readFileSync('src/routes/resources/ontology/graph/+page.ts', 'utf-8')
    expect(source).toContain('requireLegacyRouteAliases()')
    expect(source).toContain("'/resources/ontology/context'")
    expect(source).toContain("'/resources/ontology/schema'")
    expect(source).toContain("searchParams.get('tenant_id')")
  })
})
