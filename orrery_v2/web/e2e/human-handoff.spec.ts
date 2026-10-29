import { test, expect } from '@playwright/test'

for (const width of [1440, 390]) {
  test(`human handles a conversation todo at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    let submitted: Record<string, unknown> | undefined
    const todo = {
      todo_id: 'human-request', title: 'Confirm the customer quote', description: 'Agent could not find an approved quote. Confirm the amount before sending.',
      status: 'planned', priority: 'high', work_kind: 'general', automation_mode: 'manual', assignee_id: 'human-a', assignee_kind: 'human',
      created_by: 'agent-a', created_at: new Date().toISOString(), updated_at: new Date().toISOString(), version: 1,
      source: { conversation_id: 'source-chat', delivery_status: 'delivered', can_open: false, result: '' }
    }
    await page.route('**/api/**', async route => {
      const path = new URL(route.request().url()).pathname
      let body: unknown = {}
      if (path === '/api/auth/session') body = { user: { user_id: 'human-a', username: 'ding', display_name: 'Ding' }, permission_scope: ['todo.read', 'todo.write'], must_change_password: false }
      else if (path === '/api/todos') body = { todos: todo.status === 'done' ? [] : [todo], total: todo.status === 'done' ? 0 : 1 }
      else if (path === '/api/todos/human-request') body = todo
      else if (path === '/api/todos/human-request/comments') body = { comments: [], total: 0 }
      else if (path === '/api/todos/human-request/move') {
        submitted = route.request().postDataJSON()
        todo.status = 'done'
        todo.version++
        todo.source.result = String(submitted?.result)
        body = todo
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
    })
    await page.goto('/work-items?view=inbox')
    const main = page.locator('[data-work-inbox="main"]')
    await expect(main.locator('[data-work-item]')).toHaveCount(1)
    await expect(main).toContainText('人工待办')
    await page.getByRole('button', { name: /切换上下文面板|Toggle context panel/ }).click()
    const sidebar = page.locator('[data-work-inbox="sidebar"]')
    await expect(sidebar).toContainText('人工待办')
    await sidebar.getByRole('link', { name: 'Confirm the customer quote' }).click()
    if (width < 1280) await page.keyboard.press('Escape')
    await expect(page.locator('[data-human-handoff]')).toContainText('来源对话未向你开放')
    await expect(page.locator('[data-human-handoff] a')).toHaveCount(0)
    await page.getByRole('combobox', { name: '状态', exact: true }).selectOption('done')
    const complete = page.getByRole('button', { name: '完成并回传结果' })
    await expect(complete).toBeDisabled()
    await page.getByRole('textbox', { name: '处理结果', exact: true }).fill('Customer confirmed: 500. The quote can be sent.')
    await page.screenshot({ path: testInfo.outputPath('human-processing.png'), fullPage: true })
    await complete.click()
    await expect(page.locator('[data-human-handoff]')).toContainText('Customer confirmed: 500.')
    expect(submitted).toMatchObject({ status: 'done', result: 'Customer confirmed: 500. The quote can be sent.', version: 1 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect(errors).toEqual([])
    await page.screenshot({ path: testInfo.outputPath('human-result.png'), fullPage: true })
  })
}
