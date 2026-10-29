import { readFileSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'

// Test the production chunk boundary, not Vite's development module graph.
const manifest = JSON.parse(readFileSync('.svelte-kit/output/client/.vite/manifest.json', 'utf8'))
const panelChunk = `/${manifest['src/lib/components/layout/WorkspaceContextPanel.svelte'].file}`

async function mockBackend(page: Page) {
  const requests: string[] = []
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    requests.push(path)
    const body = path === '/api/scheduled-tasks/summary'
      ? { total: 252, enabled: 251, executors: 2, system: 1, failed: 2 }
      : path === '/api/auth/session'
      ? {
          user: { user_id: 'startup-test', username: 'tester', display_name: 'Startup Test' },
          permission_scope: ['scheduler.read'], must_change_password: false, rotation_required: false
        }
      : path === '/api/scheduled-runs'
        ? { items: [], total: 0, unread_total: 0, attention_total: 0, limit: 30, offset: 0 }
        : { tasks: [], members: [], items: [], total: 0, limit: 30, offset: 0 }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  return requests
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`defers sidebar and task requests in results view at ${viewport.width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport)
    const requests = await mockBackend(page)
    const errors: string[] = []
    page.on('pageerror', (error) => errors.push(error.message))
    const chunks: string[] = []
    page.on('request', (request) => chunks.push(new URL(request.url()).pathname))
    await page.goto('/scheduled-tasks?view=results')
    const refresh = page.getByRole('button', { name: '刷新结果收件箱' })
    await expect(refresh).toBeVisible()
    await expect.poll(() => requests.filter((path) => path === '/api/scheduled-runs').length).toBe(1)
    expect(chunks).not.toContain(panelChunk)
    expect(requests).not.toContain('/api/scheduled-tasks')
    expect(requests).not.toContain('/api/scheduled-tasks/summary')
    expect(requests.some((path) => path.includes('workforce'))).toBe(false)

    await refresh.click()
    await expect.poll(() => requests.filter((path) => path === '/api/scheduled-runs').length).toBe(2)
    expect(requests).not.toContain('/api/scheduled-tasks')
    await page.getByRole('button', { name: '切换上下文面板' }).click()
    const panel = page.locator('[data-workspace-context-panel]')
    await expect(panel).toBeVisible()
    expect(chunks.filter((path) => path === panelChunk)).toHaveLength(1)
    const bounds = await panel.boundingBox()
    expect(bounds).not.toBeNull()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width)
    await page.screenshot({ path: testInfo.outputPath(`sidebar-${viewport.width}.png`) })
    await panel.getByRole('button', { name: '关闭上下文面板' }).click()
    await expect(panel).toHaveCount(0)
    await page.getByRole('button', { name: '切换上下文面板' }).click()
    await expect(panel).toBeVisible()
    expect(chunks.filter((path) => path === panelChunk)).toHaveLength(1)
    await page.keyboard.press('Escape')

    await page.getByRole('tab', { name: '任务计划' }).click()
    await expect.poll(() => requests.filter((path) => path === '/api/scheduled-tasks').length).toBe(2)
    await expect.poll(() => requests.filter((path) => path === '/api/scheduled-tasks/summary').length).toBe(1)
    await expect.poll(() => requests.filter((path) => path === '/api/workforce/members').length).toBe(1)
    const stats = page.getByRole('region', { name: '任务统计' })
    await expect(stats.getByText('252', { exact: true })).toBeVisible()
    await expect(stats.getByText('251', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect.poll(() => requests.filter((path) => path === '/api/scheduled-tasks/summary').length).toBe(2)
    await page.screenshot({ path: testInfo.outputPath(`tasks-${viewport.width}.png`) })
    expect(errors).toEqual([])
  })
}

test('keeps the drawer closable if its chunk fails', async ({ page }) => {
  await mockBackend(page)
  await page.route(`**${panelChunk}`, (route) => route.abort())
  await page.goto('/scheduled-tasks?view=results')
  await page.getByRole('button', { name: '切换上下文面板' }).click()
  await expect(page.getByRole('status')).toContainText('侧栏加载失败')
  await expect(page.getByRole('button', { name: '重试加载侧栏' })).toBeVisible()
  await page.locator('aside.right-sidebar-shell').getByRole('button', { name: '关闭上下文面板' }).click()
  await expect(page.getByRole('status')).toHaveCount(0)
})

test('keeps unavailable task statistics distinct from zero and recovers on refresh', async ({ page }) => {
  await mockBackend(page)
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  let fail = true
  await page.route('**/api/scheduled-tasks/summary', async (route) => {
    await gate
    if (fail) {
      await route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ message: 'Statistics unavailable' }) })
    } else {
      await route.fallback()
    }
  })
  await page.goto('/scheduled-tasks')
  const stats = page.getByRole('region', { name: '任务统计' })
  await expect(stats.getByText('-', { exact: true })).toHaveCount(4)
  release()
  await expect(page.getByText('任务统计加载失败', { exact: true })).toBeVisible()
  await expect(stats.getByText('0', { exact: true })).toHaveCount(0)
  await expect(page.getByText('自动化任务列表', { exact: true })).toBeVisible()
  fail = false
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(stats.getByText('252', { exact: true })).toBeVisible()
  await expect(page.getByText('任务统计加载失败', { exact: true })).toHaveCount(0)
})
