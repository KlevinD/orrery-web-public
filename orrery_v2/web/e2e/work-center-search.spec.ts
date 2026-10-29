import { test, expect } from '@playwright/test'

for (const width of [1440, 390]) {
  test(`work center searches all records and loads subsequent pages at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    const requests: URL[] = []
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      requests.push(url)
      let body: unknown = {}
      if (url.pathname === '/api/auth/session') body = {
        user: { user_id: 'user-1', username: 'tester', display_name: 'Tester' },
        permission_scope: ['todo.read', 'idea.read', 'mission.read'], must_change_password: false
      }
      else if (['/api/todos', '/api/ideas', '/api/missions'].includes(url.pathname)) {
        const kind = url.pathname.slice(5, -1)
        const query = url.searchParams.get(kind === 'mission' ? 'search' : 'q') || ''
        const offset = Number(url.searchParams.get('offset') || 0)
        const limit = Number(url.searchParams.get('limit') || 100)
        const rows = Array.from({ length: 101 }, (_, index) => ({
          [`${kind}_id`]: `${kind}-${index}`,
          title: index === 100 ? `${kind} needle final record` : `${kind} ordinary ${index}`,
          description: 'Read-only fixture', content: 'Read-only fixture', final_goal: 'Read-only fixture',
          status: kind === 'mission' ? 'active' : kind === 'idea' ? 'submitted' : 'inbox',
          priority: 'medium', phase: 'execution',
          created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:00:00Z'
        })).filter(row => query.split(/\s+/).every(term => row.title.includes(term)))
        const selected = rows.slice(offset, offset + limit)
        body = kind === 'mission'
          ? { missions: selected, summaries: [], page: { total: rows.length, offset, limit } }
          : { [kind === 'todo' ? 'todos' : 'ideas']: selected, total: rows.length, has_more: offset + selected.length < rows.length, limit }
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
    })
    await page.goto('/work-items')
    const center = page.getByTestId('work-items-center')
    const rows = center.locator('[data-testid="work-items-list"] a')
    await expect(center).toHaveAttribute('aria-busy', 'false')
    await expect(rows).toHaveCount(300)
    await center.getByRole('searchbox').fill('needle')
    await expect(rows).toHaveCount(3)
    await center.getByRole('searchbox').fill('')
    await expect(rows).toHaveCount(300)
    await center.getByRole('button', { name: '加载更多' }).click()
    await expect(rows).toHaveCount(303)
    await expect(center.getByRole('button', { name: '加载更多' })).toHaveCount(0)
    for (const path of ['/api/todos', '/api/ideas', '/api/missions']) {
      expect(requests.some(url => url.pathname === path && url.searchParams.get('offset') === '100')).toBe(true)
    }
    await center.getByRole('searchbox').fill('needle')
    await expect(rows).toHaveCount(3)
    for (const path of ['/api/todos', '/api/ideas', '/api/missions']) {
      expect(requests.some(url => url.pathname === path &&
        (url.searchParams.get('q') || url.searchParams.get('search')) === 'needle' &&
        url.searchParams.get('offset') === '0')).toBe(true)
    }
    await page.locator('[data-work-center] nav a[href="/work-items?view=mission"]').click()
    await expect(center.getByRole('searchbox')).toHaveValue('needle')
    await expect(rows).toHaveCount(1)
    await expect(rows).toContainText('mission needle final record')
    await center.getByRole('searchbox').fill('missing-keyword')
    await expect(center.getByText('没有匹配的事项')).toBeVisible()
    await expect(center).toHaveAttribute('aria-busy', 'false')
    await center.getByRole('searchbox').fill('')
    await expect(rows).toHaveCount(101)
    await expect(center.getByRole('button', { name: '加载更多' })).toHaveCount(0)
    await center.getByRole('searchbox').fill('needle')
    await expect(rows).toHaveCount(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(errors).toEqual([])
    await page.screenshot({ path: testInfo.outputPath('work-center-search.png'), fullPage: false })
  })
}

test('pagination failures retain rows, can retry, and do not fetch unauthorized categories', async ({ page }) => {
  const requests: URL[] = []
  let nextPageAttempts = 0
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    requests.push(url)
    let body: unknown = {}
    if (url.pathname === '/api/auth/session') body = {
      user: { user_id: 'user-1', username: 'tester', display_name: 'Tester' },
      permission_scope: ['todo.read'], must_change_password: false
    }
    else if (url.pathname === '/api/todos') {
      const offset = Number(url.searchParams.get('offset') || 0)
      if (offset > 0 && nextPageAttempts++ === 0) {
        await route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ message: 'Page temporarily unavailable' }) })
        return
      }
      body = {
        todos: [{ todo_id: `todo-${offset}`, title: `Visible todo ${offset}`, status: 'inbox', priority: 'medium', created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:00:00Z' }],
        total: 2, has_more: offset === 0, limit: 1
      }
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/work-items')
  const center = page.getByTestId('work-items-center')
  const rows = center.locator('[data-testid="work-items-list"] a')
  await expect(rows).toHaveCount(1)
  await center.getByRole('button', { name: '加载更多' }).click()
  await expect(center.getByText('部分数据暂时无法读取，当前展示其余已加载事项。')).toBeVisible()
  await expect(rows).toHaveCount(1)
  await center.getByRole('button', { name: '加载更多' }).click()
  await expect(rows).toHaveCount(2)
  await expect(center.getByRole('button', { name: '加载更多' })).toHaveCount(0)
  await page.locator('[data-work-center] nav a[href="/work-items?view=mission"]').click()
  await expect(rows).toHaveCount(0)
  await expect(center).toHaveAttribute('aria-busy', 'false')
  expect(requests.filter(url => ['/api/missions', '/api/ideas'].includes(url.pathname))).toHaveLength(0)
})
