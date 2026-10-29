import { expect, test, type Page, type Route } from '@playwright/test'

type RequestTracker = { paths: string[]; summaryInFlight: number; maxSummaryInFlight: number }

const cursorPage = { limit: 25, has_more: false }
const summaryMeta = { as_of: '2026-07-15T01:00:00Z', data_revision: 'revision-1', authorization_revision: 'auth-1' }

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockBackend(page: Page, tracker: RequestTracker) {
  const reliabilityAuthorizations: Array<Record<string, unknown>> = []
  const reliabilityActivationReviews: Array<Record<string, unknown>> = []
  const reliabilityDiagnoses: Array<Record<string, unknown>> = [{
    id: 'diagnosis-1', incident_id: 'incident-1', incident_version: 2, root_cause_category: 'transient',
    redacted_summary: '上游服务在限时内未响应', confidence: 0.93, status: 'confirmed', evidence_ids: ['failure-evidence-1'],
    proposed_by: { kind: 'agent', id: 'diagnoser-1' }, reviewed_by: { kind: 'human', id: 'user-1' }, review_reason: '已核对运行证据',
    version: 2, created_at: summaryMeta.as_of, updated_at: summaryMeta.as_of
  }]
  const reliabilityLearnedRules: Array<Record<string, unknown>> = []
  const reliabilityPreflightRules = new Set<string>()
  let reliabilityExecution = false
  await page.route('**://*/api/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    tracker.paths.push(`${request.method()} ${path}${url.search}`)

    if (path === '/api/auth/session') return json(route, {
      user: { user_id: 'user-1', username: 'steward', display_name: 'Ontology Steward', agent_profile_id: '' },
      permission_scope: ['operations.read', 'operations.manage'], must_change_password: false, rotation_required: false
    })
    if (path === '/api/ontology/scopes') return json(route, { items: [
      { tenant_id: 'tenant-1', kind: 'organization', label: 'Tenant One', capabilities: ['ontology.read', 'ontology.manage'] },
      { tenant_id: 'tenant-2', kind: 'organization', label: 'Tenant Two', capabilities: ['ontology.read', 'ontology.manage'] }
    ], page: cursorPage })
    if (path === '/api/ontology/scopes/resolve') {
      const tenant = url.searchParams.get('tenant_id') ?? 'tenant-1'
      return json(route, { tenant_id: tenant, kind: 'organization', label: tenant === 'tenant-2' ? 'Tenant Two' : 'Tenant One', capabilities: ['ontology.read', 'ontology.manage'], binding_version: 1 })
    }

    if (path.endsWith('/summary') && path.startsWith('/api/ontology/')) {
      tracker.summaryInFlight += 1
      tracker.maxSummaryInFlight = Math.max(tracker.maxSummaryInFlight, tracker.summaryInFlight)
      await new Promise((resolve) => setTimeout(resolve, 50))
      tracker.summaryInFlight -= 1
      if (path.includes('/schema/')) return json(route, { data: { object_types: { status: 'exact', value: 2 }, link_types: { status: 'exact', value: 1 }, property_types: { status: 'exact', value: 1 }, mapping_rules: { status: 'exact', value: 1 }, action_types: { status: 'exact', value: 1 } }, meta: summaryMeta })
      if (path.includes('/context/')) return json(route, { data: { objects: { status: 'exact', value: 1 }, relations: { status: 'exact', value: 0 }, facts: { status: 'exact', value: 1 }, evidence: { status: 'exact', value: 1 } }, meta: summaryMeta })
      if (path.includes('/lineage/')) return json(route, { data: { fact_provenance_coverage: { status: 'exact', numerator: 1, denominator: 1, value: 1 }, fact_evidence_reference_coverage: { status: 'exact', numerator: 1, denominator: 1, value: 1 }, fact_resolvable_evidence_coverage: { status: 'exact', numerator: 1, denominator: 1, value: 1 }, confidence_distribution: { status: 'exact', low: 0, medium: 0, high: 1 }, freshness: { status: 'exact', last_24h: 1, days_1_7: 0, older: 0 } }, meta: summaryMeta })
      if (path.includes('/governance/')) return json(route, { data: { actionable_proposals: { status: 'exact', value: 1 }, latest_receipt: { status: 'unavailable' } }, meta: summaryMeta })
      return json(route, { data: { unresolved_audits: { status: 'exact', value: 0 }, latest_activity: { status: 'exact', observed_at: summaryMeta.as_of } }, meta: summaryMeta })
    }

    if (path === '/api/ontology/schema/graph') return json(route, { view: url.searchParams.get('view') ?? 'semantic', nodes: [
      { id: 'object:mission', kind: 'object', label: 'Mission', status: 'active', origin: 'system' },
      { id: 'object:isolated', kind: 'object', label: 'Isolated', status: 'active', origin: 'tenant' },
      { id: 'link:owns', kind: 'link', label: '负责', status: 'active', origin: 'system' }
    ], edges: [{ id: 'edge-1', source_id: 'object:mission', target_id: 'link:owns', relation: 'allows_link' }], visible_nodes: 3, visible_edges: 1, applied_max_nodes: 180, truncated: false })
    if (path.includes('/api/ontology/schema/definitions/object/isolated/references')) return json(route, { items: [], page: cursorPage })
    if (path === '/api/ontology/schema/definitions/object/isolated') return json(route, { kind: 'object', id: 'isolated', label: 'Isolated', origin: 'tenant', version: '1', status: 'active', attributes: { lifecycle: 'active' } })
    if (path === '/api/ontology/schema/definitions') return json(route, { items: [], page: cursorPage })

    if (path === '/api/ontology/context/objects' && url.searchParams.get('object_type') === 'failure_incident') return json(route, { items: [{ id: 'incident-1', tenant_id: 'tenant-1', object_type: 'failure_incident', label: '任务运行因上游超时失败', status: reliabilityExecution ? 'monitoring' : 'diagnosed', confidence: 0.98, updated_at: summaryMeta.as_of }], page: cursorPage })
    if (path === '/api/ontology/context/objects') return json(route, { items: [{ id: 'mission-1', object_type: 'mission', label: 'Mission A', status: 'active', updated_at: summaryMeta.as_of }], page: cursorPage })
    if (path === '/api/ontology/context/objects/mission-1') return json(route, { id: 'mission-1', object_type: 'mission', label: 'Mission A', status: 'active', canonical_key: 'mission:mission-1', properties: { stage: 'launch' }, updated_at: summaryMeta.as_of })
    if (path === '/api/ontology/context/objects/mission-1/facts') {
      if (url.searchParams.get('cursor') === 'facts-next') return json(route, { items: [{ id: 'fact-2', predicate: 'mission.stage', value: 'launch', status: 'confirmed', updated_at: summaryMeta.as_of }], page: cursorPage })
      return json(route, { items: [{ id: 'fact-1', predicate: 'mission.status', value: 'active', status: 'confirmed', updated_at: summaryMeta.as_of }], page: { limit: 25, has_more: true, next_cursor: 'facts-next' } })
    }
    if (path.includes('/api/ontology/context/objects/mission-1/relations') || path.includes('/api/ontology/context/objects/mission-1/evidence')) return json(route, { items: [], page: cursorPage })
    if (path === '/api/ontology/context/neighborhood') return json(route, { root_id: 'mission-1', nodes: [{ id: 'mission-1', object_type: 'mission', label: 'Mission A', degree: 0 }], edges: [], visible_nodes: 1, visible_edges: 0, applied_depth: 1, applied_max_nodes: 120, applied_max_edges: 240, truncated: false })

    if (path === '/api/ontology/lineage/facts/fact-1/provenance') return json(route, { fact: { id: 'fact-1', subject: { kind: 'mission', id: 'mission-1', label: 'Mission A' }, predicate: 'mission.status' }, provenance_status: 'resolved', event: { id: 'event-1', type: 'mission.updated', occurred_at: summaryMeta.as_of }, mapping: { status: 'exact', candidates: [{ id: 'rule-1', origin: 'tenant' }] } })
    if (path === '/api/ontology/lineage/facts/fact-1/assertions') return json(route, { items: [{ id: 'assertion-1', status: 'asserted', value: 'active', observed_at: summaryMeta.as_of }], page: cursorPage })
    if (path.startsWith('/api/ontology/lineage/facts/fact-1/')) return json(route, { items: [], page: cursorPage })

    if (path === '/api/ontology/governance/proposals') return json(route, { items: [{ id: 'proposal-1', revision: 2, target_kind: 'object_type', target_id: 'mission', metadata_key: 'owner_domain', proposed_value: 'mission_control', source_kind: 'deterministic', status: 'accepted', actionable: true, updated_at: summaryMeta.as_of }], page: cursorPage })
    if (path === '/api/ontology/governance/proposals/proposal-1') return json(route, { id: 'proposal-1', revision: 2, target_kind: 'object_type', target_id: 'mission', metadata_key: 'owner_domain', proposed_value: 'mission_control', source_kind: 'deterministic', status: 'accepted', actionable: true, updated_at: summaryMeta.as_of })
    if (path.endsWith('/analysis')) return json(route, { blocking_count: 0, warning_count: 0, issues: [] })
    if (path.endsWith('/receipts') || path.endsWith('/audit-events')) return json(route, { items: [], page: cursorPage })
    if (path.endsWith('/plans') && request.method() === 'POST') return json(route, { plan_id: 'plan-1', proposal_id: 'proposal-1', proposal_revision: 2, expires_at: '2026-07-15T02:00:00Z', changes: [{ operation: 'update', target_kind: 'object_type', target_id: 'mission', field: 'owner_domain', current_version: 1, expected_version: 1 }] }, 201)

    if (path === '/api/ontology/reliability/incidents/incident-1' && request.method() === 'GET') return json(route, {
      incident: { id: 'incident-1', tenant_id: 'tenant-1', fingerprint: 'sha256:fingerprint', source: 'mission_agent_runtime', component: 'mission-agent', category: 'transient', severity: 'error', status: reliabilityExecution ? 'monitoring' : 'diagnosed', error_code: 'upstream_timeout', redacted_summary: '任务运行因上游超时失败', retryable: true, occurrence_count: 3, version: 2, first_seen_at: summaryMeta.as_of, last_seen_at: summaryMeta.as_of, updated_at: summaryMeta.as_of },
      target: { observation_id: 'observation-3', kind: 'mission_agent_run', authorization_tenant_id: 'tenant-1', mission_id: 'mission-1', run_id: 'run-original', occurred_at: summaryMeta.as_of },
      available_evidence: [{ id: 'failure-evidence-1', label: '任务运行上游超时观察', status: 'observed', confidence: 1, updated_at: summaryMeta.as_of }],
      diagnoses: reliabilityDiagnoses,
      rules: [{ id: 'repair-rule-1', diagnosis_case_id: 'diagnosis-1', strategy: 'retry', action_type_id: 'reliability.repair.execute', risk_level: 'high', status: 'active', confidence: 0.96, support_count: 8, success_count: 7, failure_count: 0, reversible: true, idempotent: true, auto_apply: false, version: 4, args_hash: 'sha256:args', dry_run_ready: true, activation_ready: false, activation_blockers: ['rule_not_proposed'], activation_decision_id: 'activation-existing', activated_by: { kind: 'human', id: 'user-1' }, activated_at: summaryMeta.as_of, apply_ready: reliabilityPreflightRules.has('repair-rule-1') && !reliabilityExecution, apply_blockers: reliabilityPreflightRules.has('repair-rule-1') ? (reliabilityExecution ? ['execution_active'] : []) : ['current_preflight_required'], last_verified_at: summaryMeta.as_of, updated_at: summaryMeta.as_of }, ...reliabilityLearnedRules],
      preflights: Array.from(reliabilityPreflightRules, (ruleId) => ({ evidence_id: `evidence-preflight-${ruleId}`, action_run_id: `action-dry-run-${ruleId}`, incident_id: 'incident-1', incident_version: 2, rule_id: ruleId, rule_version: ruleId === 'repair-rule-1' ? 4 : 1, strategy: 'retry', args_hash: ruleId === 'repair-rule-1' ? 'sha256:args' : 'sha256:learned', target_observation_id: 'observation-3', target_kind: 'mission_agent_run', target_run_id: 'run-original', passed: true, current: true, checked_at: summaryMeta.as_of, expires_at: '2026-07-15T02:00:00Z' })),
      executions: reliabilityExecution ? [{ action_run_id: 'action-apply', incident_id: 'incident-1', rule_id: 'repair-rule-1', preflight_evidence_id: 'evidence-preflight-1', strategy: 'retry', original_run_id: 'run-original', retry_run_id: 'run-retry', status: 'monitoring', version: 2, started_at: summaryMeta.as_of, deadline_at: '2026-07-15T02:00:00Z', updated_at: summaryMeta.as_of }] : [],
      activation_reviews: [...reliabilityActivationReviews].reverse(),
      authorizations: [...reliabilityAuthorizations].reverse()
    })
    if (path === '/api/ontology/reliability/incidents/incident-1/diagnoses' && request.method() === 'POST') {
      const body = request.postDataJSON() as { root_cause_category: string; redacted_summary: string; confidence: number; evidence_ids: string[] }
      const diagnosis = { id: 'diagnosis-2', incident_id: 'incident-1', incident_version: 2, root_cause_category: body.root_cause_category, redacted_summary: body.redacted_summary, confidence: body.confidence, status: 'proposed', evidence_ids: body.evidence_ids, proposed_by: { kind: 'human', id: 'user-1' }, version: 1, created_at: summaryMeta.as_of, updated_at: summaryMeta.as_of }
      reliabilityDiagnoses.push(diagnosis)
      return json(route, diagnosis, 201)
    }
    if (path === '/api/ontology/reliability/incidents/incident-1/diagnoses/diagnosis-2/decision' && request.method() === 'POST') {
      const body = request.postDataJSON() as { decision: 'confirm' | 'reject'; reason: string }
      const diagnosis = reliabilityDiagnoses.find((item) => item.id === 'diagnosis-2')
      if (diagnosis) {
        diagnosis.status = body.decision === 'confirm' ? 'confirmed' : 'rejected'
        diagnosis.reviewed_by = { kind: 'human', id: 'user-1' }
        diagnosis.review_reason = body.reason
        diagnosis.version = 2
      }
      return json(route, diagnosis)
    }
    if (path === '/api/ontology/reliability/incidents/incident-1/diagnoses/diagnosis-2/rules' && request.method() === 'POST') {
      const body = request.postDataJSON() as { strategy: string; reversible: boolean; idempotent: boolean }
      const rule = { id: 'repair-rule-2', diagnosis_case_id: 'diagnosis-2', strategy: body.strategy, action_type_id: 'reliability.repair.execute', risk_level: 'high', status: 'proposed', confidence: 0, support_count: 1, success_count: 0, failure_count: 0, reversible: body.reversible, idempotent: body.idempotent, auto_apply: false, version: 1, args_hash: 'sha256:learned', dry_run_ready: true, activation_ready: false, activation_blockers: ['current_preflight_required'], apply_ready: false, apply_blockers: ['rule_not_active', 'current_preflight_required'], updated_at: summaryMeta.as_of }
      reliabilityLearnedRules.push(rule)
      return json(route, rule, 201)
    }
    if (path.match(/^\/api\/ontology\/reliability\/rules\/[^/]+\/activation-reviews$/) && request.method() === 'POST') {
      const ruleId = decodeURIComponent(path.match(/\/rules\/([^/]+)\/activation-reviews$/)?.[1] ?? '')
      const body = request.postDataJSON() as { purpose?: string }
      const review = { id: `activation-${ruleId}`, rule_id: ruleId, rule_version: 1, incident_id: 'incident-1', diagnosis_case_id: 'diagnosis-2', preflight_evidence_id: `evidence-preflight-${ruleId}`, status: 'pending', route_mode: 'approval', requester: { kind: 'human', id: 'user-1' }, args_hash: 'sha256:activation', purpose: body.purpose, current: true, activated: false, created_at: summaryMeta.as_of, expires_at: '2026-07-15T02:00:00Z' }
      reliabilityActivationReviews.push(review)
      return json(route, review, 201)
    }
    if (path.match(/\/activation-reviews\/[^/]+\/decision$/) && request.method() === 'POST') {
      const body = request.postDataJSON() as { decision: 'authorize' | 'deny' }
      const review = reliabilityActivationReviews.find((item) => path.includes(String(item.id)))
      if (review) review.status = body.decision === 'authorize' ? 'resolved' : 'denied'
      return json(route, { review, effect: body.decision })
    }
    if (path.match(/\/activation-reviews\/[^/]+\/activate$/) && request.method() === 'POST') {
      const review = reliabilityActivationReviews.find((item) => path.includes(String(item.id)))
      const ruleId = String(review?.rule_id ?? '')
      const rule = reliabilityLearnedRules.find((item) => item.id === ruleId)
      if (review) {
        review.current = false
        review.activated = true
      }
      if (rule) {
        rule.status = 'active'
        rule.version = 2
        rule.activation_ready = false
        rule.activation_blockers = ['rule_not_proposed', 'current_preflight_required']
        rule.activation_decision_id = review?.id
        rule.activated_by = { kind: 'human', id: 'user-1' }
        rule.activated_at = summaryMeta.as_of
        rule.apply_blockers = ['current_preflight_required']
      }
      reliabilityPreflightRules.delete(ruleId)
      for (const authorization of reliabilityAuthorizations) {
        if (authorization.rule_id === ruleId) authorization.current = false
      }
      return json(route, rule)
    }
    if (path.endsWith('/authorizations') && path.includes('/api/ontology/reliability/') && request.method() === 'POST') {
      const body = request.postDataJSON() as { execution_mode: 'dry_run' | 'apply'; purpose?: string }
      const ruleId = decodeURIComponent(path.match(/\/rules\/([^/]+)\/authorizations$/)?.[1] ?? 'repair-rule-1')
      const authorization = { id: `authorization-${ruleId}-${body.execution_mode}`, execution_mode: body.execution_mode, rule_id: ruleId, status: 'pending', route_mode: 'approval', requester: { kind: 'human', id: 'user-1' }, args_hash: ruleId === 'repair-rule-1' ? 'sha256:args' : 'sha256:learned', purpose: body.purpose, current: true, created_at: summaryMeta.as_of, expires_at: '2026-07-15T02:00:00Z' }
      reliabilityAuthorizations.push(authorization)
      return json(route, authorization, 201)
    }
    if (path.endsWith('/decision') && path.includes('/api/ontology/reliability/') && request.method() === 'POST') {
      const body = request.postDataJSON() as { decision: 'authorize' | 'deny' }
      const authorization = reliabilityAuthorizations.find((item) => path.includes(String(item.id)))
      if (authorization) authorization.status = body.decision === 'authorize' ? 'resolved' : 'denied'
      return json(route, { authorization, effect: body.decision })
    }
    if (path.endsWith('/invoke') && path.includes('/api/ontology/reliability/') && request.method() === 'POST') {
      const authorization = reliabilityAuthorizations.find((item) => path.includes(String(item.id)))
      const mode = authorization?.execution_mode
      const ruleId = String(authorization?.rule_id ?? 'repair-rule-1')
      if (mode === 'dry_run') {
        reliabilityPreflightRules.add(ruleId)
        const rule = reliabilityLearnedRules.find((item) => item.id === ruleId)
        if (rule) {
          rule.activation_ready = true
          rule.activation_blockers = []
        }
      }
      if (mode === 'apply') reliabilityExecution = true
      return json(route, { id: mode === 'dry_run' ? `action-dry-run-${ruleId}` : 'action-apply', action_type_id: 'reliability.repair.execute', status: mode === 'dry_run' ? 'completed' : 'started', risk_level: 'high', execution_mode: mode, runtime: { kind: 'system', id: 'reliability-control-plane' }, created_at: summaryMeta.as_of }, 202)
    }

    if (path === '/api/ontology/projections/audits') return json(route, { items: [], page: cursorPage })
    return json(route, {})
  })
}

test('scope activation schedules summaries in two slots and keeps responsive layout bounded', async ({ page }, testInfo) => {
  const tracker: RequestTracker = { paths: [], summaryInFlight: 0, maxSummaryInFlight: 0 }
  await mockBackend(page, tracker)
  await page.goto('/resources/ontology')
  await expect(page.getByRole('heading', { name: '选择知识范围' })).toBeVisible()
  expect(tracker.paths.some((path) => path.includes('/summary'))).toBe(false)

  await page.getByRole('button', { name: /Tenant One/ }).click()
  await page.getByRole('button', { name: '确认' }).click()
  await expect(page.getByRole('link', { name: 'Projection' })).toBeVisible()
  await expect.poll(() => tracker.paths.filter((path) => path.includes('/summary')).length).toBe(5)
  expect(tracker.maxSummaryInFlight).toBeLessThanOrEqual(2)

  for (const viewport of [{ width: 1440, height: 900 }, { width: 768, height: 1024 }, { width: 360, height: 800 }]) {
    await page.setViewportSize(viewport)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    const mainBox = await page.locator('main').boundingBox()
    const filingBox = await page.getByRole('link', { name: '京ICP备2026039091号' }).boundingBox()
    expect(mainBox && filingBox && mainBox.y + mainBox.height <= filingBox.y).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`overview-${viewport.width}x${viewport.height}.png`), fullPage: true })
  }
})

test('outcome routes deep-link, lazy-load and discard a secured plan only after scope confirmation', async ({ page }, testInfo) => {
  const tracker: RequestTracker = { paths: [], summaryInFlight: 0, maxSummaryInFlight: 0 }
  await mockBackend(page, tracker)

  await page.goto('/resources/ontology/schema?tenant_id=tenant-1&view=semantic-map')
  await expect(page.getByRole('img', { name: '本体模型关系图' })).toBeVisible()
  expect(tracker.paths.some((path) => path === 'GET /api/ontology/scopes' || path.startsWith('GET /api/ontology/scopes?'))).toBe(false)
  await expect(page.getByRole('button', { name: '切换范围' })).toBeVisible()
  await expect(page.locator('line[marker-end="url(#ontology-schema-arrow)"]')).toHaveCount(1)
  await page.screenshot({ path: testInfo.outputPath('schema-graph.png'), fullPage: true })
  await page.getByText('键盘可访问节点与关系列表').click()
  await page.getByRole('button', { name: /Isolated/ }).click()
  await expect(page.getByRole('heading', { name: 'Isolated' })).toBeVisible()

  await page.goto('/resources/ontology/context?tenant_id=tenant-1&view=graph&root_id=mission-1&object_id=mission-1')
  await expect(page.getByRole('img', { name: '实际数据关系图' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('context-neighborhood.png'), fullPage: true })

  await page.goto('/resources/ontology/context?tenant_id=tenant-1')
  await page.getByRole('button', { name: 'Mission A' }).click()
  await page.getByRole('button', { name: '事实' }).click()
  await page.getByRole('button', { name: '断言历史' }).click()
  await expect(page.getByText('asserted')).toBeVisible()
  await page.getByRole('button', { name: '下一页' }).click()
  await expect(page.getByText('mission.stage')).toBeVisible()
  await expect(page.getByText('asserted')).toHaveCount(0)
  await page.getByRole('button', { name: '上一页' }).click()
  await page.getByRole('link', { name: '查看完整来源' }).click()
  await expect(page).toHaveURL(/fact_id=fact-1/)
  await expect(page.getByRole('heading', { name: 'mission.status' })).toBeVisible()

  await page.goto('/resources/ontology/governance?tenant_id=tenant-1')
  await page.getByRole('button', { name: 'object_type:mission' }).click()
  await page.getByRole('button', { name: '生成安全应用计划' }).click()
  await expect(page.getByRole('heading', { name: '待应用变更' })).toBeVisible()
  await page.getByRole('button', { name: '切换范围' }).click()
  await page.getByRole('button', { name: /Tenant Two/ }).click()
  page.once('dialog', (dialog) => dialog.dismiss())
  await page.getByRole('button', { name: '确认' }).click()
  await expect(page).toHaveURL(/tenant_id=tenant-1/)
  await expect(page.getByRole('heading', { name: '待应用变更' })).toBeVisible()
  page.once('dialog', (dialog) => dialog.accept())
  await page.getByRole('button', { name: '确认' }).click()
  await expect(page).toHaveURL(/tenant_id=tenant-2/)
  await expect(page.getByRole('heading', { name: '待应用变更' })).toHaveCount(0)

  tracker.paths.length = 0
  await page.goto('/resources/ontology/operations/projections')
  await expect(page.getByRole('heading', { name: '选择知识范围' })).toBeVisible()
  expect(tracker.paths.some((path) => path.includes('/replay-history'))).toBe(false)
})

test('reliability workbench carries an incident through approved preflight and exact repair', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  const tracker: RequestTracker = { paths: [], summaryInFlight: 0, maxSummaryInFlight: 0 }
  await mockBackend(page, tracker)
  await page.goto('/resources/ontology/reliability?tenant_id=tenant-1')

  await page.getByRole('button', { name: /任务运行因上游超时失败/ }).click()
  await expect(page.getByText('run-original')).toBeVisible()
  await expect(page.getByRole('button', { name: /重试/ })).toBeVisible()
  await page.getByRole('button', { name: '申请预检审批' }).click()
  await page.getByLabel('可靠性审批意见').fill('目标和版本已核对')
  await page.getByRole('button', { name: '批准预检' }).click()
  await page.getByRole('button', { name: '执行预检' }).click()
  await expect(page.getByText(/通过 ·/)).toBeVisible()

  await page.getByRole('tab', { name: '修复' }).click()
  await page.getByRole('button', { name: '申请修复审批' }).click()
  await page.getByLabel('可靠性审批意见').fill('批准精确重试')
  await page.getByRole('button', { name: '批准修复' }).click()
  await page.getByRole('button', { name: '执行修复' }).click()
  await expect(page.getByText(/观察中 ·/)).toBeVisible()
  expect(tracker.paths.some((path) => path.includes('source_tenant_id'))).toBe(false)
  expect(tracker.paths.filter((path) => path.includes('/api/ontology/reliability/')).length).toBeGreaterThanOrEqual(8)

  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    await page.locator('main').evaluate((element) => { element.scrollTop = 0 })
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    await expect.poll(() => page.locator('main').evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`reliability-${viewport.width}x${viewport.height}.png`), fullPage: true })
  }
})

test('reliability workbench turns visible evidence into a human-confirmed candidate rule', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  const tracker: RequestTracker = { paths: [], summaryInFlight: 0, maxSummaryInFlight: 0 }
  await mockBackend(page, tracker)
  await page.goto('/resources/ontology/reliability?tenant_id=tenant-1')

  await page.getByRole('button', { name: /任务运行因上游超时失败/ }).click()
  await page.getByRole('checkbox', { name: /任务运行上游超时观察/ }).check()
  await page.getByLabel('诊断根因类别').selectOption('configuration')
  await page.getByLabel('诊断置信度').fill('0.8')
  await page.getByLabel('诊断摘要').fill('连接池超时配置低于上游响应窗口')
  await page.getByRole('button', { name: '提交诊断提议' }).click()
  await expect(page.getByText('连接池超时配置低于上游响应窗口')).toBeVisible()

  await page.getByLabel('诊断审核意见').fill('已核对配置快照与故障观察')
  await page.getByRole('button', { name: '确认诊断' }).click()
  await page.getByRole('button', { name: '生成候选规则' }).click()
  await expect(page.getByText('已形成候选规则').last()).toBeVisible()
  await expect(page.getByText('诊断支撑').last()).toBeVisible()
  expect(tracker.paths.some((path) => path.includes('/diagnoses/diagnosis-2/rules'))).toBe(true)

  await page.getByRole('button', { name: '申请预检审批' }).click()
  await page.getByLabel('可靠性审批意见').fill('先验证候选规则的精确目标')
  await page.getByRole('button', { name: '批准预检' }).click()
  await page.getByRole('button', { name: '执行预检' }).click()
  await expect(page.getByRole('button', { name: '申请激活评审' })).toBeVisible()
  await page.getByRole('button', { name: '申请激活评审' }).click()
  await page.getByLabel('规则激活评审意见').fill('诊断证据和候选规则预检均已核对')
  await page.getByRole('button', { name: '批准激活' }).click()
  await page.getByRole('button', { name: '激活规则' }).click()
  await expect(page.getByText('activation-repair-rule-2')).toBeVisible()
  expect(tracker.paths.some((path) => path.includes('/rules/repair-rule-2/activation-reviews'))).toBe(true)

  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    await expect.poll(() => page.locator('main').evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`reliability-learning-${viewport.width}x${viewport.height}.png`), fullPage: true })
  }
})
