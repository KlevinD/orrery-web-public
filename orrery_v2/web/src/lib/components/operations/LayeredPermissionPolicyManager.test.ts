import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import LayeredPermissionPolicyManager from './LayeredPermissionPolicyManager.svelte'
import type { WorkforceMemberViewModel } from '#lib/types/view-models'

describe('LayeredPermissionPolicyManager', () => {
  it('loads packages only after a role is selected', async () => {
    const onselectrole = vi.fn()
    render(LayeredPermissionPolicyManager, {
      props: {
        roles: [{ role_id: 'custom.security', label_zh: '安全管理员', label_en: 'Security Admin', built_in: false, granted_package_count: 1 }],
        packages: [],
        onselectrole
      }
    })

    expect(screen.queryByText('权限查看')).toBeNull()
    await fireEvent.click(screen.getByRole('tab', { name: '角色列表 / Roles' }))
    await fireEvent.click(screen.getByRole('button', { name: '编辑权限 / Edit permissions for Security Admin' }))
    expect(onselectrole).toHaveBeenCalledWith('custom.security')
  })

  it('shows paginated permission details before confirming a whole-package grant', async () => {
    const onopenpackagedetails = vi.fn()
    const onloadmoredetails = vi.fn()
    const onconfirmpackagechange = vi.fn()
    render(LayeredPermissionPolicyManager, {
      props: {
        roles: [{ role_id: 'custom.security', label_zh: '安全管理员', label_en: 'Security Admin', built_in: false, granted_package_count: 0 }],
        selectedRoleId: 'custom.security',
        packages: [{
          package_id: 'permission.read', page_id: 'permission', page_label_zh: '权限中心', page_label_en: 'Permission center',
          label_zh: '权限查看', label_en: 'Permissions Read', access_mode: 'read', scopes: ['permissions.read'], capability_count: 2, grant_state: 'none'
        }],
        packageDetails: [{
          capability_id: 'get.api.permissions.roles.permissions.read', domain: 'permissions', action: 'read', access_mode: 'read',
          scope: 'permissions.read', method: 'GET', path: '/api/permissions/roles', label_zh: '角色查看', label_en: 'List permission roles',
          category_zh: '权限', category_en: 'Permissions', granted: false
        }],
        detailsNextCursor: 'next-page',
        onopenpackagedetails,
        onloadmoredetails,
        onconfirmpackagechange
      }
    })

    await fireEvent.click(screen.getByRole('tab', { name: '角色列表 / Roles' }))
    await fireEvent.click(screen.getByRole('button', { name: '授予权限查看 / Grant Permissions Read' }))
    expect(onopenpackagedetails).toHaveBeenCalledWith('permission.read', true)

    const dialog = screen.getByRole('dialog', { name: '确认授予权限包' })
    expect(within(dialog).getByText('将授予这些权限')).toBeTruthy()
    expect(within(dialog).getByText('List permission roles')).toBeTruthy()
    await fireEvent.click(within(dialog).getByRole('button', { name: '加载更多权限' }))
    expect(onloadmoredetails).toHaveBeenCalled()
    await fireEvent.click(within(dialog).getByRole('button', { name: '确认授予' }))
    expect(onconfirmpackagechange).toHaveBeenCalledWith({ roleId: 'custom.security', packageId: 'permission.read', effect: 'grant' })
  })

  it('opens effective grants lazily when its tab is selected', async () => {
    const onopeneffective = vi.fn()
    render(LayeredPermissionPolicyManager, { props: { roles: [], packages: [], onopeneffective } })

    await fireEvent.click(screen.getByRole('tab', { name: '有效权限 / Effective' }))
    expect(onopeneffective).toHaveBeenCalledTimes(1)
  })

  it('renders and paginates the independent permission catalog', async () => {
    const onloadmorecatalog = vi.fn()
    const onopenpackagedetails = vi.fn()
    render(LayeredPermissionPolicyManager, {
      props: {
        roles: [],
        packages: [{
          package_id: 'role-only.read', page_id: 'role-only', page_label_zh: '角色权限', page_label_en: 'Role permissions',
          label_zh: '角色专属权限', label_en: 'Role-only permission', access_mode: 'read', scopes: ['role-only.read'], capability_count: 1, grant_state: 'granted'
        }],
        catalogPackages: [{
          package_id: 'catalog.read', page_id: 'catalog', page_label_zh: '权限字典', page_label_en: 'Permission catalog',
          label_zh: '字典权限', label_en: 'Catalog permission', access_mode: 'read', scopes: ['catalog.read'], capability_count: 2, grant_state: 'none'
        }],
        catalogNextCursor: 'next-catalog-page',
        onloadmorecatalog,
        onopenpackagedetails
      }
    })

    await fireEvent.click(screen.getByRole('tab', { name: '权限字典 / Dictionary' }))

    expect(screen.getByText('字典权限 / Catalog permission')).toBeTruthy()
    expect(screen.queryByText('角色专属权限')).toBeNull()
    await fireEvent.click(screen.getByText('字典权限 / Catalog permission'))
    expect(onopenpackagedetails).toHaveBeenCalledWith('catalog.read', false)
    await fireEvent.click(screen.getByRole('button', { name: '加载更多权限包' }))
    expect(onloadmorecatalog).toHaveBeenCalledTimes(1)
  })

  it('selects a user, stages an assignable role, and preserves system-managed roles', async () => {
    const onopenusers = vi.fn()
    const onselectuser = vi.fn()
    const ontoggleuserrole = vi.fn()
    const onsaveuserroles = vi.fn()
    const user = {
      profileId: 'profile-1',
      displayName: 'Ada Admin',
      realName: 'Ada Admin',
      userAccount: { username: 'ada', email: 'ada@example.test', accountEnabled: true }
    } as unknown as WorkforceMemberViewModel

    render(LayeredPermissionPolicyManager, {
      props: {
        users: [user],
        selectedUserId: 'profile-1',
        assignableRoles: [
          {
            role_id: 'platform.admin', name: '平台管理员', description: 'Platform Administrator', category: 'platform',
            status: 'active', assignable_actor_types: ['human'], built_in: true, permission_refs: ['permissions.manage'], approval_capability: true
          },
          {
            role_id: 'custom.viewer', name: '只读用户', description: 'Read only', category: 'custom',
            status: 'active', assignable_actor_types: ['human'], built_in: false, permission_refs: ['org.read'], approval_capability: false
          }
        ],
        userAssignments: [{ assignment_id: 'system-admin', role_id: 'platform.admin', source: 'system' }],
        selectedUserRoleIds: ['platform.admin'],
        userAssignmentDirty: true,
        onopenusers,
        onselectuser,
        ontoggleuserrole,
        onsaveuserroles
      }
    })

    expect(onopenusers).toHaveBeenCalledTimes(1)
    await fireEvent.click(screen.getByRole('button', { name: '选择用户 Ada Admin' }))
    expect(onselectuser).toHaveBeenCalledWith('profile-1')

    const systemRole = screen.getByRole('checkbox', { name: '平台管理员 角色' }) as HTMLInputElement
    expect(systemRole.disabled).toBe(true)
    await fireEvent.click(screen.getByRole('checkbox', { name: '只读用户 角色' }))
    expect(ontoggleuserrole).toHaveBeenCalledWith('custom.viewer', true)

    await fireEvent.click(screen.getByRole('button', { name: '保存 Ada Admin 的用户授权' }))
    expect(onsaveuserroles).toHaveBeenCalledTimes(1)
  })
})
