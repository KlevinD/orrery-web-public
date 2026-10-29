import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PermissionsPage from './+page.svelte'

const mocks = vi.hoisted(() => ({
  packageQueries: [] as Array<{ filters: () => Record<string, unknown>; enabled: () => boolean }>,
  mutate: vi.fn(),
  putAssignments: vi.fn(),
  deleteAssignment: vi.fn(),
  fetchNextUsers: vi.fn(),
  refetchAssignments: vi.fn(),
  refetchOrgRoles: vi.fn(),
  refetchRoles: vi.fn(),
  refetchRolePackages: vi.fn(),
  refetchCatalog: vi.fn()
}))

vi.mock('#lib/queries/workforce', () => ({
  combineWorkforcePages: (pages: Array<{ members?: unknown[]; page?: unknown }>) => ({
    members: pages.flatMap((page) => page.members ?? []),
    page: pages.at(-1)?.page
  }),
  useWorkforceMembersInfiniteQuery: () => ({
    data: {
      pages: [{
        members: [{
          agent_id: 'user-1', agent_profile_id: 'profile-1', real_name: 'Ada Admin', display_name: 'Ada Admin',
          agent_kind: 'human', user_account: { username: 'ada', email: 'ada@example.test', account_enabled: true },
          employment_type: 'employee', organization_id: 'org-1', department_id: 'dept-1', role_ids: [], enabled: true
        }],
        page: { total: 1, returned: 1, has_more: false }
      }]
    },
    isFetching: false,
    hasNextPage: false,
    error: null,
    fetchNextPage: mocks.fetchNextUsers
  }),
  useOrgRolesQuery: () => ({
    data: {
      items: [{
        role_id: 'platform.admin', name: '平台管理员', description: 'Platform Administrator', category: 'platform', status: 'active',
        assignable_actor_types: ['human'], built_in: true, permission_refs: ['permissions.manage'], approval_capability: true
      }]
    },
    isFetching: false,
    error: null,
    refetch: mocks.refetchOrgRoles
  }),
  useOrgGraphQuery: () => ({ data: { organizations: [] }, isFetching: false, error: null }),
  useOrgRoleAssignmentsQuery: () => ({
    data: { items: [], page: { total: 0, returned: 0 } },
    isFetching: false,
    error: null,
    refetch: mocks.refetchAssignments
  }),
  useOrgRoleAssignmentsMutation: () => ({ mutateAsync: mocks.putAssignments, isPending: false }),
  useOrgRoleAssignmentDeleteMutation: () => ({ mutateAsync: mocks.deleteAssignment, isPending: false })
}))

vi.mock('#lib/queries/operations', () => ({
  usePermissionRolesQuery: () => ({
    data: {
      revision: 7,
      items: [{ role_id: 'custom.security', label_zh: '安全管理员', label_en: 'Security Admin', built_in: false, granted_package_count: 1 }],
      next_cursor: ''
    },
    isLoading: false,
    isError: false,
    isFetching: false,
    refetch: mocks.refetchRoles
  }),
  usePermissionPackagesQuery: (filters: () => Record<string, unknown>, enabled: () => boolean) => {
    const index = mocks.packageQueries.length
    mocks.packageQueries.push({ filters, enabled })
    return {
      data: index === 0
        ? {
            items: [{
              package_id: 'role.read', page_id: 'role', page_label_zh: '角色权限', page_label_en: 'Role permissions',
              label_zh: '角色权限包', label_en: 'Role package', access_mode: 'read', scopes: ['role.read'], capability_count: 1, grant_state: 'granted'
            }],
            next_cursor: ''
          }
        : {
            items: [{
              package_id: 'catalog.read', page_id: 'catalog', page_label_zh: '权限字典', page_label_en: 'Permission catalog',
              label_zh: '字典权限包', label_en: 'Catalog package', access_mode: 'read', scopes: ['catalog.read'], capability_count: 2, grant_state: 'none'
            }],
            next_cursor: 'catalog-next'
          },
      isFetching: false,
      refetch: index === 0 ? mocks.refetchRolePackages : mocks.refetchCatalog
    }
  },
  usePermissionPackageCapabilitiesQuery: () => ({ data: undefined, isFetching: false }),
  usePermissionEffectiveGrantsQuery: () => ({ data: undefined, isFetching: false }),
  usePermissionPolicyBatchMutation: () => ({ mutate: mocks.mutate, isPending: false })
}))

describe('permissions page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.putAssignments.mockResolvedValue({ items: [] })
    mocks.refetchAssignments.mockResolvedValue({ data: { items: [] } })
    mocks.packageQueries.length = 0
  })

  afterEach(cleanup)

  it('selects the first role and loads the permission catalog independently', async () => {
    render(PermissionsPage)

    await fireEvent.click(screen.getByRole('tab', { name: '角色列表 / Roles' }))
    await waitFor(() => expect(screen.getByText('角色权限包')).toBeTruthy())
    expect(mocks.packageQueries).toHaveLength(2)
    expect(mocks.packageQueries[0].enabled()).toBe(true)
    expect(mocks.packageQueries[0].filters()).toEqual({ roleId: 'custom.security', cursor: '', limit: 20 })
    expect(mocks.packageQueries[1].enabled()).toBe(true)
    expect(mocks.packageQueries[1].filters()).toEqual({ cursor: '', limit: 20 })

    await fireEvent.click(screen.getByRole('tab', { name: '权限字典 / Dictionary' }))
    expect(screen.getByText('字典权限包 / Catalog package')).toBeTruthy()
    expect(screen.queryByText('角色权限包')).toBeNull()
    expect(screen.getByRole('button', { name: '加载更多权限包' })).toBeTruthy()
  })

  it('selects a workforce user and saves a platform role grant', async () => {
    render(PermissionsPage)

    const role = await screen.findByRole('checkbox', { name: '平台管理员 角色' })
    await fireEvent.click(role)
    await fireEvent.click(screen.getByRole('button', { name: '保存 Ada Admin 的用户授权' }))

    await waitFor(() => expect(mocks.putAssignments).toHaveBeenCalledWith({
      assignments: [{
        actor: { actor_type: 'human', actor_id: 'profile-1' },
        role_id: 'platform.admin',
        source: 'direct',
        scope_mode: 'platform'
      }]
    }))
    expect(mocks.refetchAssignments).toHaveBeenCalledTimes(1)
  })
})
