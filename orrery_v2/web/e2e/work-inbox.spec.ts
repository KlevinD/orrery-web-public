import { test, expect } from '@playwright/test'

for (const width of [1440, 390]) {
  test(`unified inbox at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(90_000)
    page.on('pageerror', error => console.error(error.message))
    await page.setViewportSize({ width, height: 900 })
    const requests: string[] = []
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      requests.push(url.pathname + url.search)
      let body: unknown = {}
      if (url.pathname === '/api/auth/session') body = {
        user: { user_id: 'user-1', username: 'tester', display_name: 'Tester' },
        permission_scope: ['todo.read', 'scheduler.read'], must_change_password: false
      }
      else if (url.pathname === '/api/todos') body = {
        todos: [...Array.from({ length: 4 }, (_, i) => ({
          todo_id: 'todo-' + i, title: ['Review customer proposal', 'Verify knowledge indexing', 'Prepare AI research briefing', 'Publish weekly report'][i],
          description: 'Review the latest evidence and confirm the next action.',
          status: 'inbox', priority: 'medium', created_at: '2026-09-05T01:00:00Z'
        })), { todo_id: 'old-done', title: 'Previously completed', status: 'done', created_at: '2020-01-01T00:00:00Z' },
        { todo_id: 'today-done', title: 'Completed today', status: 'done', created_at: new Date().toISOString() }].filter(todo => url.searchParams.get('include_completed') === 'true' || todo.status === 'inbox'), total: url.searchParams.get('include_completed') === 'true' ? 6 : 4
      }
      else if (url.pathname === '/api/scheduled-runs') body = {
        items: (url.searchParams.has('group_by_task') ? [2] : [1, 2]).map(day => ({
          run: { scheduled_run_id: 'run-' + day, scheduled_task_id: 'daily',
            created_at: `2026-09-0${day}T01:00:00Z`, status: 'completed', result: { summary: 'AI research briefing is ready.' } },
          task_title: 'Daily AI research', is_read: false, attention_required: false
        })), total: url.searchParams.has('group_by_task') ? 1 : 2
      }
      else if (url.pathname === '/api/workforce/members') body = { members: [] }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
    })
    await page.goto('/work-items?view=inbox')
    const main = page.locator('[data-work-inbox="main"]')
    await expect(main.locator('[data-work-inbox-pending]')).toHaveText('4', { timeout: 60_000 })
    await expect(main.locator('[data-work-item]')).toHaveCount(4)
    await page.getByRole('button', { name: '切换上下文面板' }).click()
    const sidebar = page.locator('[data-work-inbox="sidebar"]')
    await expect(sidebar.locator('[data-work-inbox-pending]')).toHaveText('4')
    expect(requests.filter(url => url.startsWith('/api/scheduled-runs'))).toHaveLength(1)
    expect(requests.some(url => url.includes('include_completed=true'))).toBe(false)
    await sidebar.getByRole('tab').nth(2).click()
    await expect(sidebar.locator('[data-work-item]')).toHaveCount(1)
    await sidebar.locator('select').selectOption('all')
    await expect(sidebar.locator('[data-work-item]')).toHaveCount(2)
    await sidebar.locator('select').selectOption('today')
    await page.screenshot({ path: testInfo.outputPath('sidebar.png'), fullPage: true })
    if (width < 1280) await page.keyboard.press('Escape')
    await main.getByRole('tab').nth(2).click()
    await expect(main.locator('[data-work-item]')).toHaveCount(1)
    await main.locator('[data-history-window] button').click()
    await expect(main.locator('[data-work-item]')).toHaveCount(2)
    await main.locator('select').selectOption('7d')
    await expect(main.locator('[data-work-item]')).toHaveCount(1)
    await main.locator('select').selectOption('30d')
    await expect(main.locator('[data-work-item]')).toHaveCount(1)
    await page.screenshot({ path: testInfo.outputPath('processed-history.png'), fullPage: true })
    await main.getByRole('tab').filter({ hasText: /结果|Results/ }).click()
    await expect(main.locator('[data-work-item]')).toHaveCount(1)
    await main.locator('summary').click()
    await expect(main.locator('details a')).toHaveCount(2)
    expect(requests.some(url => url.includes('task_id=daily') && url.includes('limit=20'))).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('inbox.png'), fullPage: true })
  })
}
