import { expect, test, type Page, type Route } from '@playwright/test'

const observedAt = '2026-09-03T08:00:00Z'

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockProblemHub(page: Page) {
  let owner = { kind: 'team', id: 'platform' }
  let routeBody: Record<string, unknown> | undefined
  const problem = () => ({
    id: 'problem-1', tenant_id: 'tenant-1', fingerprint: 'reliability:provider-timeout', source_domain: 'reliability',
    category: 'transient', severity: 'error', status: 'resolved', title: '上游服务响应超时',
    redacted_summary: '支付同步任务连续三次超过响应时限。', owner,
    primary_source: { kind: 'failure_incident', id: 'incident-1' },
    source_refs: [{ kind: 'failure_incident', id: 'incident-1' }], evidence_ids: ['evidence-observed-1', 'evidence-verified-1'],
    occurrence_count: 3, latest_outcome_id: 'outcome-1', latest_outcome_success: true,
    latest_outcome_summary: '退避重试后连续 24 小时验证成功。', latest_resolution: { kind: 'repair_rule', id: 'repair-rule-1' },
    lesson_candidate_id: 'lesson-1', version: 5, first_seen_at: observedAt, last_seen_at: observedAt,
    resolved_at: observedAt, created_at: observedAt, updated_at: observedAt
  })

  await page.route('**://*/api/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.pathname === '/api/auth/session') return json(route, {
      user: { user_id: 'user-1', username: 'steward', display_name: 'Ontology Steward', agent_profile_id: '' },
      permission_scope: ['operations.read', 'operations.manage'], must_change_password: false, rotation_required: false
    })
    if (url.pathname === '/api/ontology/scopes') return json(route, {
      items: [{ tenant_id: 'tenant-1', kind: 'organization', label: 'Orrery', capabilities: ['ontology.read', 'ontology.manage'] }],
      page: { limit: 100, has_more: false }
    })
    if (url.pathname === '/api/ontology/scopes/resolve') return json(route, {
      tenant_id: 'tenant-1', kind: 'organization', label: 'Orrery', capabilities: ['ontology.read', 'ontology.manage'], binding_version: 1
    })
    if (url.pathname === '/api/ontology/problems' && request.method() === 'GET') return json(route, {
      items: [problem()], limit: 100, returned: 1, has_more: false
    })
    if (url.pathname === '/api/ontology/problems/problem-1' && request.method() === 'GET') return json(route, problem())
    if (url.pathname === '/api/ontology/problems/problem-1/route' && request.method() === 'POST') {
      routeBody = request.postDataJSON() as Record<string, unknown>
      owner = routeBody.owner as { kind: string; id: string }
      return json(route, problem())
    }
    if (url.pathname === '/api/ontology/context/objects/lesson-1') return json(route, {
      id: 'lesson-1', tenant_id: 'tenant-1', object_type: 'lesson_candidate', label: '已验证的超时恢复经验',
      status: 'proposed', properties: { review_outcome: 'accepted', accepted_memory_id: 'memory-1' }, updated_at: observedAt
    })
    return json(route, {})
  })

  return { routeBody: () => routeBody }
}

test('problem hub keeps the learning chain usable on desktop and mobile', async ({ page }, testInfo) => {
  const state = await mockProblemHub(page)
  await page.goto('/resources/ontology/problems?tenant_id=tenant-1&problem_id=problem-1&source_tenant_id=tenant-1')

  await expect(page.getByRole('heading', { name: '问题枢纽' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '上游服务响应超时' })).toBeVisible()
  await expect(page.getByText('已接纳 · memory-1')).toBeVisible()
  await expect(page.getByRole('list', { name: '问题知识沉淀进度' }).getByText('记忆')).toBeVisible()

  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`problem-hub-${viewport.width}x${viewport.height}.png`), fullPage: true })
  }

  await page.getByRole('heading', { name: '结果与组织记忆' }).scrollIntoViewIfNeeded()
  await expect(page.getByRole('heading', { name: '结果与组织记忆' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('problem-hub-mobile-detail.png') })

  await page.getByLabel('负责人 ID').fill('incident-response')
  await page.getByRole('button', { name: '转派' }).click()
  await expect(page.getByText('负责人已更新')).toBeVisible()
  expect(state.routeBody()).toEqual({ owner: { kind: 'team', id: 'incident-response' } })
})
