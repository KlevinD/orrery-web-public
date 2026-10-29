import { expect, test } from '@playwright/test'

test('Agent Session dropdown switches history and New creates a fresh Session', async ({ page }, testInfo) => {
  await page.addInitScript(() => localStorage.setItem('orrery.locale', 'en-US'))
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/discussions/design-demo')

  const sessionSelect = page.getByRole('combobox', { name: 'Session' })
  await expect(sessionSelect).toBeVisible({ timeout: 20_000 })
  await expect(sessionSelect.locator('option')).toHaveCount(6, { timeout: 20_000 })

  await sessionSelect.selectOption('discussion-assistant-research')
  await expect(sessionSelect).toHaveValue('discussion-assistant-research')

  await page.getByRole('button', { name: 'Start a new session with this Agent' }).click()

  await expect(sessionSelect.locator('option')).toHaveCount(7)
  await expect(sessionSelect).not.toHaveValue('discussion-assistant-research')
  await expect(sessionSelect.locator('option:checked')).toContainText('Session 7')
  await page.screenshot({ path: testInfo.outputPath('discussion-session-dropdown.png'), fullPage: true })

  const dataAgent = page.getByRole('button').filter({ hasText: '数据分析 Agent' })
  await expect(dataAgent).toHaveCount(1)
  await dataAgent.click()
  await expect(sessionSelect.locator('option')).toHaveCount(1)

  await page.getByRole('button', { name: 'Start a new session with this Agent' }).click()
  await expect(sessionSelect.locator('option')).toHaveCount(2)
  await expect(sessionSelect.locator('option:checked')).toContainText('Session 2')

  const human = page.getByRole('button').filter({ hasText: '产品负责人' })
  await expect(human).toHaveCount(1)
  await human.click()
  await expect(page.getByRole('combobox', { name: 'Session' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Start a new session with this Agent' })).toHaveCount(0)
})
