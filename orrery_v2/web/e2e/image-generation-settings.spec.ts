import { expect, test, type Page } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  page.on('pageerror', error => console.error(error.message))
})

test.afterEach(async ({ page }, testInfo) => {
  if (testInfo.status !== testInfo.expectedStatus) {
    console.log(await page.locator('body').ariaSnapshot())
    await page.screenshot({ path: testInfo.outputPath('failure.png'), fullPage: true })
  }
})

async function mockSettings(page: Page, readOnly = false) {
  let image = { enabled: false, protocol: 'openai-compatible', base_url: 'https://gateway.example/v1', model: 'image2', api_key_ref: 'secret://image/provider' }
  const requests: string[] = []
  await page.route('**://*/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    requests.push(`${request.method()} ${path}`)
    const respond = (body: unknown) => route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
    if (path === '/api/auth/session') return respond({ user: { user_id: 'image-settings-test', username: 'admin', display_name: 'Settings Admin', agent_profile_id: '' }, permission_scope: readOnly ? ['settings.read'] : ['settings.read', 'settings.manage'], must_change_password: false, rotation_required: false })
    if (path === '/api/settings') return respond({ platform_name: 'Orrery', organization_name: 'Orrery', language: 'zh-CN', auto_create_agent_profile: false, auto_link_evidence: true, hil_default_enabled: true, discussion_agent_pair_loop_limit: 8, notification_rules: [], runtime_profiles: [], llm_profiles: [], prompt_blocks: [], image_generation: image })
    if (path === '/api/settings/image-generation') {
      if (request.method() === 'PUT') image = request.postDataJSON()
      return respond(image)
    }
    if (path === '/api/settings/image-generation/test') return respond({ status: 'ok', reason: 'model_listed', model_available: true, generation_verified: false, latency_ms: 10 })
    if (path === '/api/settings/runtime/discovery') return respond({ candidates: [] })
    return respond({ items: [], total: 0 })
  })
  return requests
}

for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
  test(`native image settings at ${viewport.width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport)
    const requests = await mockSettings(page)
    await page.goto('/settings')
    await page.getByRole('tab', { name: '图片生成', exact: true }).click()
    const section = page.getByRole('region', { name: '图片生成', exact: true })
    await expect(section.getByLabel('网关 API 地址')).toHaveValue('https://gateway.example/v1')
    await section.getByLabel('网关 API 地址').fill('https://updated.example/v1')
    await expect(section.getByRole('button', { name: '检查连接' })).toBeDisabled()
    await section.getByRole('button', { name: '保存', exact: true }).click()
    await expect(section.getByRole('button', { name: '检查连接' })).toBeEnabled()
    await section.getByRole('button', { name: '检查连接' }).click()
    await expect(section.getByText('网关连接正常，模型已列出')).toBeVisible()
    await expect(section.getByText('尚未验证实际生图能力')).toBeVisible()
    expect(requests).toContain('PUT /api/settings/image-generation')
    expect(requests).not.toContain('PUT /api/settings')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`image-settings-${viewport.width}.png`), fullPage: true })
  })
}

test('read-only settings cannot change or test image configuration', async ({ page }) => {
  const requests = await mockSettings(page, true)
  await page.goto('/settings')
  await page.getByRole('tab', { name: '图片生成', exact: true }).click()
  const section = page.getByRole('region', { name: '图片生成', exact: true })
  await expect(section.getByLabel('网关 API 地址')).toBeDisabled()
  await expect(section.getByRole('button', { name: '检查连接' })).toBeDisabled()
  expect(requests.some(value => value.startsWith('PUT '))).toBe(false)
})
