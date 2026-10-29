import { expect, test } from '@playwright/test'

for (const width of [1440, 390]) {
  test(`AgentCore operation review stays paused at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(90_000)
    await page.setViewportSize({ width, height: 1080 })
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    const now = '2026-10-16T10:00:00Z'
    const mission = { mission_id: 'mission-core', title: 'AgentCore 长任务核对', final_goal: '核对公开资料检索任务的外部操作回执', status: 'active', phase: 'execute', risk_level: 'normal', created_at: now }
    const task = { task_id: 'task-1', title: '检索公开产品资料', goal: '汇总公开资料', status: 'blocked', assignee: 'agent-1', blocked_reason: '工具操作结果待核对', updated_at: now }
    const control = {
      mission, summary: { team_size: 1, active_task_count: 1, pending_approval_count: 0, risk_count: 0, blocker_count: 1, delivery_rate: 0, updated_at: now },
      team: { members: [{ agent_id: 'agent-1', display_name: '资料研究员', mission_role: 'researcher', status: 'active' }] },
      task_board: { tasks: [task] }, evidence: { evidence_items: [] }, qa: { checks: [] },
      discussion: { discussion_group_id: 'discussion-1', messages: [] }, progress_graph: { nodes: [], edges: [], current_focus: [] }, blockers: [], version: 1
    }
    const recovery = {
      available: true, revision: '9007199254740993', phase: 'tools_pending', model_calls: 12, tool_calls: 5, compactions: 2,
      summarizing: false, summary_processed_bytes: 0, summary_total_bytes: 0, deadline: '2026-10-16T10:15:00Z', updated_at: now,
      restart_blocked: true, can_review: true, can_restart: false,
      pending_tools: [{ call_id: 'call-public-42', tool_id: 'knowledge.search', state: 'unknown' }], latest_review: undefined as unknown
    }
    let reads = 0
    const writes: Array<{ path: string; body: Record<string, unknown>; key?: string }> = []
    await page.route('**://*/api/**', async route => {
      const request = route.request()
      const path = new URL(request.url()).pathname
      let body: unknown = {}
      if (path === '/api/auth/session') body = { user: { user_id: 'operator-1', username: 'operator-1', display_name: '运行管理员' }, permission_scope: ['mission.read', 'mission.write', 'operations.read', 'operations.manage'], must_change_password: false }
      else if (path === '/api/missions/mission-core/control') body = control
      else if (path === '/api/missions/mission-core/ops') body = {
        mission_id: mission.mission_id, title: mission.title, status: 'active', phase: 'execute', current_focus: [], agent_lanes: [],
        runtime_sessions: [{ run_id: 'run-core-1', agent_id: 'agent-1', task_id: 'task-1', runtime_kind: 'managed', status: 'failed', error_summary: '工具结果未确认，执行已暂停', is_alive: false, updated_at: now }],
        action_cards: [], evidence_gates: [], approval_gates: [], autonomy_policy: { mode: 'manual' }, version: 1, updated_at: now
      }
      else if (path === '/api/missions/mission-core/discussion') body = control.discussion
      else if (path.endsWith('/recovery/reviews')) {
        const input = request.postDataJSON()
        writes.push({ path, body: input, key: request.headers()['idempotency-key'] })
        recovery.latest_review = { ...input, review_id: 'review-1', actor_id: 'operator-1', recorded_at: now }
        body = recovery.latest_review
      } else if (path.endsWith('/recovery')) { reads++; body = recovery }
      else if (request.method() === 'POST') writes.push({ path, body: request.postDataJSON() })
      await route.fulfill({ status: path.endsWith('/reviews') ? 201 : 200, contentType: 'application/json', body: JSON.stringify(body) })
    })
    await page.goto('/missions/mission-core?tab=operations')
    await expect(page.getByRole('button', { name: '长任务状态' })).toBeVisible({ timeout: 60_000 })
    expect(reads).toBe(0)
    await expect(page.getByRole('button', { name: '重试运行 run-core-1' })).toHaveCount(0)
    await page.getByRole('button', { name: '长任务状态' }).click()
    await expect(page.getByText('工具操作待核对', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: '重新执行', exact: true })).toHaveCount(0)
    await page.getByLabel('核对说明').fill('已检查外部系统回执，执行结果仍需核实。')
    await page.getByLabel('证据或回执编号').fill('external-audit:public-retrieval-42')
    await page.getByRole('button', { name: '保存核对记录' }).click()
    await expect(page.getByText('核对已登记，任务仍暂停，需要先处置外部操作。')).toBeVisible()
    await expect(page.getByLabel('最近核对记录')).toContainText('operator-1')
    expect(writes).toHaveLength(1)
    expect(writes[0].body.revision).toBe('9007199254740993')
    expect(writes[0].body).not.toHaveProperty('actor_id')
    expect(writes[0].key).toBeTruthy()
    await expect(page.getByRole('button', { name: '重新执行', exact: true })).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(errors).toEqual([])
    await page.setViewportSize({ width, height: width < 500 ? 2200 : 1600 })
    await page.screenshot({ path: testInfo.outputPath(`agentcore-review-${width}.png`), fullPage: true })
  })
}
