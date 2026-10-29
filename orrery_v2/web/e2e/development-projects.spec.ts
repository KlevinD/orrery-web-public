import { expect, test, type Page, type Route } from '@playwright/test'

const timestamp = '2026-07-25T02:00:00Z'
test.describe.configure({ timeout: 60_000 })

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockDevelopmentBackend(page: Page, readOnly = false) {
  const requests: string[] = []
  await page.route('**://*/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    requests.push(`${request.method()} ${path}`)
    if (path === '/api/auth/session') {
      return json(route, {
        user: { user_id: 'owner-1', username: 'owner-1', display_name: 'Project Owner', agent_profile_id: '' },
        permission_scope: readOnly ? ['development.read'] : ['development.read', 'development.manage', 'connection.read', 'connection.use'],
        must_change_password: false,
        rotation_required: false
      })
    }
    if (path === '/api/development-projects/project-1') {
      return json(route, {
        development_project_id: 'project-1',
        organization_id: 'organization-with-a-deliberately-long-identifier-for-layout-verification',
        name: 'Orrery project with a deliberately long name that must remain readable on narrow screens',
        description: 'A production development project with tenant-safe repository resources.',
        status: 'active', default_automation_mode: 'managed', max_concurrent_runs: 4,
        execution_policy: {
          approval_required: true, network_mode: 'restricted', allowed_hosts: ['api.example.com'],
          allowed_command_prefixes: ['go test'], allowed_file_scopes: ['orrery_v2'], allowed_secret_count: 1
        },
        created_by: 'owner-1', created_at: timestamp, updated_at: timestamp, version: 7,
        member_count: 2, repository_count: 1,
        capabilities: {
          can_view: true, can_update: !readOnly, can_configure: !readOnly, can_manage_members: !readOnly,
          can_bind_repository: !readOnly, can_request_execution: !readOnly, can_submit: !readOnly,
          can_approve: !readOnly, can_merge: !readOnly, can_archive: !readOnly
        }
      })
    }
    if (path === '/api/development-projects/project-1/members') {
      return json(route, { items: [
        { member_id: 'member-1', subject_kind: 'user', subject_id: 'owner-with-a-long-identity', role: 'owner', created_by: 'owner-1', created_at: timestamp, updated_at: timestamp, version: 1 },
        { member_id: 'member-2', subject_kind: 'agent', subject_id: 'implementation-agent', role: 'executor', created_by: 'owner-1', created_at: timestamp, updated_at: timestamp, version: 1 }
      ] })
    }
    if (path === '/api/development-projects/project-1/repositories') {
      return json(route, { items: [{
        binding_id: 'binding-1', connection_id: 'connection-1',
        repository_locator: 'minvia/orrery-with-a-deliberately-long-repository-name-for-responsive-verification',
        display_name: 'orrery-with-a-deliberately-long-repository-name-for-responsive-verification',
        role: 'primary', access_mode: 'read_write', default_branch: 'main', source_subdirectory: 'orrery_v2',
        build_commands: ['go build ./...'], test_commands: ['go test ./...'], branch_prefix: 'orrery/todo/',
        status: 'error', last_fetched_commit: '0123456789012345678901234567890123456789',
        last_verified_at: timestamp, created_at: timestamp, updated_at: timestamp, version: 3
      }] })
    }
    if (path === '/api/scm-connections') return json(route, { items: [], page: { limit: 100, offset: 0, total: 0, returned: 0, has_more: false } })
    return json(route, {})
  })
  return requests
}

test('development project remains bounded across desktop and mobile and exposes repository errors', async ({ page }, testInfo) => {
  await page.addInitScript(() => localStorage.setItem('orrery.locale', 'en-US'))
  await mockDevelopmentBackend(page)
  await page.goto('/development-projects/project-1')
  await expect(page.getByRole('heading', { name: /Orrery project with a deliberately long name/ })).toBeVisible({ timeout: 20_000 })
  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByText('orrery-with-a-deliberately-long-repository-name-for-responsive-verification', { exact: true })).toBeVisible()
  await expect(page.getByText('error', { exact: true })).toBeVisible()

  for (const viewport of [{ width: 1440, height: 900 }, { width: 360, height: 800 }]) {
    await page.setViewportSize(viewport)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`development-project-${viewport.width}x${viewport.height}.png`), fullPage: true })
    const repositoryName = page.getByText('orrery-with-a-deliberately-long-repository-name-for-responsive-verification', { exact: true })
    await repositoryName.scrollIntoViewIfNeeded()
    await expect(repositoryName).toBeVisible()
    const repositoryBox = await repositoryName.boundingBox()
    expect(repositoryBox && repositoryBox.x >= 0 && repositoryBox.x + repositoryBox.width <= viewport.width).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`development-project-repository-${viewport.width}x${viewport.height}.png`) })
  }
})

test('read-only development project never fetches or renders repository mutation controls', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('orrery.locale', 'en-US'))
  const requests = await mockDevelopmentBackend(page, true)
  await page.goto('/development-projects/project-1')
  await expect(page.getByRole('heading', { name: /Orrery project with a deliberately long name/ })).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole('button', { name: 'Edit project' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Archive' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByRole('button', { name: 'Bind repository' })).toHaveCount(0)
  await expect(page.getByTitle('Verify')).toHaveCount(0)
  expect(requests.some((request) => request === 'GET /api/scm-connections')).toBe(false)
})
