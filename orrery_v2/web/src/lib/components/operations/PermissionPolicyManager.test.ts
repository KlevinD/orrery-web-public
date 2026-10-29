import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { mapPermissionPolicyCenterView } from '#lib/mappers/operations'
import PermissionPolicyManager from './PermissionPolicyManager.svelte'

describe('PermissionPolicyManager', () => {
  it('opens a create role dialog, generates role ids, and creates roles with grouped permissions', async () => {
    const onaddrole = vi.fn()
    render(PermissionPolicyManager, { props: { view: buildPermissionView(), onaddrole } })

    expect(screen.getByRole('tab', { name: '角色列表 / Roles' })).toBeTruthy()
    expect(screen.getByText('Operations Admin')).toBeTruthy()
    expect(screen.getByText('1 permissions')).toBeTruthy()
    expect(screen.queryByLabelText('搜索角色权限')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '新建角色 / Create role' }))

    const dialog = screen.getByRole('dialog', { name: '新建角色 / Create role' })
    const permissionGroups = within(dialog).getByTestId('new-role-permission-groups')
    expect(within(dialog).queryByLabelText('Role ID')).toBeNull()
    expect(within(dialog).queryByLabelText('Role English name')).toBeNull()
    expect(within(permissionGroups).getByText('权限中心 / Permission center')).toBeTruthy()
    expect(within(permissionGroups).getByText('2 permission packages')).toBeTruthy()

    await fireEvent.change(within(dialog).getByLabelText('权限类型'), { target: { value: 'write' } })
    expect(within(permissionGroups).getByText('权限编辑')).toBeTruthy()
    expect(within(permissionGroups).queryByText('权限查看')).toBeNull()

    await fireEvent.input(within(dialog).getByLabelText('角色名称'), { target: { value: 'Security Reviewer' } })
    await fireEvent.click(within(dialog).getByRole('checkbox', { name: 'Select Permissions Edit for new role' }))
    await fireEvent.click(within(dialog).getByRole('button', { name: '确认新建 / Create' }))

    expect(onaddrole).toHaveBeenCalledWith({
      roleId: 'custom.security-reviewer',
      labelZh: 'Security Reviewer',
      labelEn: 'Security Reviewer',
      permissionScopes: ['permissions.manage']
    })
  })

  it('edits permission packages for one selected role instead of showing every role as matrix columns', async () => {
    const ontogglerolegrant = vi.fn()
    render(PermissionPolicyManager, {
      props: {
        view: buildPermissionView(),
        ontogglerolegrant
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: '编辑权限 / Edit permissions for Operations Admin' }))
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Grant Permissions Read to Operations Admin' }))

    expect(ontogglerolegrant).toHaveBeenCalledWith('ops-admin', ['permissions.read'], ['get.api.permissions.policy.permissions.read'], true)
  })

  it('supports select all, clear all, and invert for every permission group', async () => {
    render(PermissionPolicyManager, { props: { view: buildPermissionView() } })

    await fireEvent.click(screen.getByRole('button', { name: '新建角色 / Create role' }))

    const dialog = screen.getByRole('dialog', { name: '新建角色 / Create role' })
    const read = within(dialog).getByRole('checkbox', { name: 'Select Permissions Read for new role' }) as HTMLInputElement
    const edit = within(dialog).getByRole('checkbox', { name: 'Select Permissions Edit for new role' }) as HTMLInputElement
    const selectAll = within(dialog).getByRole('checkbox', { name: 'Select all permissions in 权限中心 / Permission center for new role' })
    const clearAll = within(dialog).getByRole('checkbox', { name: 'Clear all permissions in 权限中心 / Permission center for new role' })
    const invert = within(dialog).getByRole('checkbox', { name: 'Invert permissions in 权限中心 / Permission center for new role' })

    await fireEvent.click(selectAll)
    expect(read.checked).toBe(true)
    expect(edit.checked).toBe(true)

    await fireEvent.click(clearAll)
    expect(read.checked).toBe(false)
    expect(edit.checked).toBe(false)

    await fireEvent.click(edit)
    await fireEvent.click(invert)
    expect(read.checked).toBe(true)
    expect(edit.checked).toBe(false)
  })

  it('applies group selection actions while editing an existing role', async () => {
    const ontogglerolegrant = vi.fn()
    render(PermissionPolicyManager, { props: { view: buildPermissionView(), ontogglerolegrant } })

    await fireEvent.click(screen.getByRole('button', { name: '编辑权限 / Edit permissions for Operations Admin' }))

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Select all permissions in 权限中心 / Permission center for Operations Admin' }))
    expect(ontogglerolegrant).toHaveBeenLastCalledWith(
      'ops-admin',
      ['permissions.read', 'permissions.manage'],
      ['get.api.permissions.policy.permissions.read', 'put.api.permissions.policy.permissions.manage'],
      true
    )

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Clear all permissions in 权限中心 / Permission center for Operations Admin' }))
    expect(ontogglerolegrant).toHaveBeenLastCalledWith(
      'ops-admin',
      ['permissions.read', 'permissions.manage'],
      ['get.api.permissions.policy.permissions.read', 'put.api.permissions.policy.permissions.manage'],
      false
    )

    ontogglerolegrant.mockClear()
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Invert permissions in 权限中心 / Permission center for Operations Admin' }))
    expect(ontogglerolegrant.mock.calls).toEqual([
      ['ops-admin', ['permissions.read'], ['get.api.permissions.policy.permissions.read'], true],
      ['ops-admin', ['permissions.manage'], ['put.api.permissions.policy.permissions.manage'], false]
    ])
  })

  it('shows built-in role templates but keeps their permissions read-only', async () => {
    const view = buildPermissionView()
    view.roles[0].builtIn = true
    render(PermissionPolicyManager, { props: { view } })

    expect(screen.getByText('系统模板')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '查看权限 / View permissions for Operations Admin' }))

    const checkbox = screen.getByRole('checkbox', { name: 'Grant Permissions Read to Operations Admin' }) as HTMLInputElement
    expect(checkbox.disabled).toBe(true)
  })

  it('filters the permission dictionary by text, function category, and read/write operation type', async () => {
    render(PermissionPolicyManager, { props: { view: buildPermissionView() } })

    await fireEvent.click(screen.getByRole('tab', { name: '权限字典 / Dictionary' }))
    await fireEvent.input(screen.getByLabelText('搜索权限'), { target: { value: 'policy' } })
    await fireEvent.change(screen.getByLabelText('功能分类'), { target: { value: 'Permissions' } })
    await fireEvent.change(screen.getByLabelText('读写类型'), { target: { value: 'write' } })

    const dictionary = screen.getByTestId('permission-dictionary')
    expect(within(dictionary).getByText('Edit permission policy')).toBeTruthy()
    expect(within(dictionary).queryByText('Read permission policy')).toBeNull()
    expect(within(dictionary).queryByText('List tools')).toBeNull()
  })

  it('keeps manual user and agent registration out of the role policy page', async () => {
    const onsave = vi.fn()
    render(PermissionPolicyManager, {
      props: {
        view: buildPermissionView(),
        dirty: true,
        onsave
      }
    })

    expect(screen.queryByRole('button', { name: '用户与角色 / Users' })).toBeNull()
    expect(screen.queryByText('主体')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '保存策略 / Save policy' }))
    expect(onsave).toHaveBeenCalled()
  })

  it('keeps legacy policy controls read-only under the authoritative authorization service', () => {
	const view = buildPermissionView()
	view.enforcementMode = 'authorization'
	view.policyMutable = false
	render(PermissionPolicyManager, { props: { view, dirty: true } })

	expect(screen.getByText('当前由统一授权服务执行权限判定，本页策略只读。角色与关系请通过统一授权数据源管理。')).toBeTruthy()
	expect((screen.getByRole('button', { name: '保存策略 / Save policy' }) as HTMLButtonElement).disabled).toBe(true)
	expect((screen.getByRole('button', { name: '新建角色 / Create role' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('groups approval catalog routes as one approval center instead of one panel per endpoint', async () => {
    render(PermissionPolicyManager, { props: { view: buildApprovalPermissionView() } })

    await fireEvent.click(screen.getByRole('button', { name: '新建角色 / Create role' }))

    const dialog = screen.getByRole('dialog', { name: '新建角色 / Create role' })
    const permissionGroups = within(dialog).getByTestId('new-role-permission-groups')
    const approvalGroup = within(permissionGroups).getByText('审批中心 / Approval center').closest('section')

    expect(approvalGroup).toBeTruthy()
    expect(within(approvalGroup as HTMLElement).getByText('2 permission packages')).toBeTruthy()
    expect(within(approvalGroup as HTMLElement).getByText('审批查看')).toBeTruthy()
    expect(within(approvalGroup as HTMLElement).getByText('审批处理')).toBeTruthy()
    expect(within(approvalGroup as HTMLElement).getByText(/approvals\.decide · 2 operations/)).toBeTruthy()
  })

  it('groups idea routes into read and edit permission packages', async () => {
    render(PermissionPolicyManager, { props: { view: buildIdeaPermissionView() } })

    await fireEvent.click(screen.getByRole('button', { name: '新建角色 / Create role' }))

    const dialog = screen.getByRole('dialog', { name: '新建角色 / Create role' })
    const permissionGroups = within(dialog).getByTestId('new-role-permission-groups')
    const ideaGroup = within(permissionGroups).getByText('想法 / Ideas').closest('section')

    expect(permissionGroups.querySelectorAll('section')).toHaveLength(1)
    expect(ideaGroup).toBeTruthy()
    expect(within(ideaGroup as HTMLElement).getByText('2 permission packages')).toBeTruthy()
    expect(within(ideaGroup as HTMLElement).getByText('想法查看')).toBeTruthy()
    expect(within(ideaGroup as HTMLElement).getByText('想法编辑')).toBeTruthy()
    expect(within(ideaGroup as HTMLElement).getByText(/idea\.read · 3 operations/)).toBeTruthy()
    expect(within(ideaGroup as HTMLElement).getByText(/idea\.write · 7 operations/)).toBeTruthy()
  })

  it('groups workforce and talent market routes without exposing route paths as group names', async () => {
    render(PermissionPolicyManager, { props: { view: buildTalentMarketPermissionView() } })

    await fireEvent.click(screen.getByRole('button', { name: '新建角色 / Create role' }))

    const dialog = screen.getByRole('dialog', { name: '新建角色 / Create role' })
    const permissionGroups = within(dialog).getByTestId('new-role-permission-groups')
    const organizationGroup = within(permissionGroups).getByText('组织中心 / Organization center').closest('section')

    expect(organizationGroup).toBeTruthy()
    expect(permissionGroups.querySelectorAll('section')).toHaveLength(1)
    expect(within(organizationGroup as HTMLElement).getByText('2 permission packages')).toBeTruthy()
    expect(within(organizationGroup as HTMLElement).getByText('人才查看')).toBeTruthy()
    expect(within(organizationGroup as HTMLElement).getByText('人才编辑')).toBeTruthy()
    expect(within(organizationGroup as HTMLElement).getByText(/workforce\.read · 2 operations/)).toBeTruthy()
    expect(within(organizationGroup as HTMLElement).getByText(/workforce\.manage · 2 operations/)).toBeTruthy()
    expect(within(permissionGroups).queryByText('/api/talent-market/members')).toBeNull()
  })
})

function buildPermissionView() {
  return mapPermissionPolicyCenterView({
    capabilities: [
      {
        capability_id: 'get.api.permissions.policy.permissions.read',
        domain: 'permissions',
        action: 'read',
        access_mode: 'read',
        scope: 'permissions.read',
        method: 'GET',
        path: '/api/permissions/policy',
        label_zh: '读取权限策略',
        label_en: 'Read permission policy',
        category_zh: '权限',
        category_en: 'Permissions'
      },
      {
        capability_id: 'put.api.permissions.policy.permissions.manage',
        domain: 'permissions',
        action: 'manage',
        access_mode: 'write',
        scope: 'permissions.manage',
        method: 'PUT',
        path: '/api/permissions/policy',
        label_zh: '更新权限策略',
        label_en: 'Edit permission policy',
        category_zh: '权限',
        category_en: 'Permissions'
      },
      {
        capability_id: 'get.api.resources.tools.resource.read',
        domain: 'resource',
        action: 'read',
        access_mode: 'read',
        scope: 'resource.read',
        method: 'GET',
        path: '/api/resources/tools',
        label_zh: '工具查看',
        label_en: 'List tools',
        category_zh: '资源',
        category_en: 'Resource'
      }
    ],
    policy: {
      roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
      role_assignments: [{ principal_kind: 'user', principal_id: 'operator', role_id: 'ops-admin' }],
      grants: [
        {
          subject_kind: 'role',
          subject_id: 'ops-admin',
          scope: 'permissions.manage'
        }
      ]
    },
    effective: [
      {
        principal_kind: 'user',
        principal_id: 'operator',
        capability_id: 'put.api.permissions.policy.permissions.manage',
        scope: 'permissions.manage',
        source: 'role',
        source_id: 'ops-admin'
      }
    ]
  })
}

function buildApprovalPermissionView() {
  return mapPermissionPolicyCenterView({
    capabilities: [
      {
        capability_id: 'get.api.approvals.approvals.read',
        domain: 'approval',
        action: 'read',
        access_mode: 'read',
        scope: 'approvals.read',
        method: 'GET',
        path: '/api/approvals',
        label_zh: '审批查看',
        label_en: 'List approvals',
        category_zh: '审批',
        category_en: 'Approval'
      },
      {
        capability_id: 'post.api.approvals.approval.id.decision.approvals.decide',
        domain: 'approval',
        action: 'decide',
        access_mode: 'write',
        scope: 'approvals.decide',
        method: 'POST',
        path: '/api/approvals/{approval_id}/decision',
        label_zh: '审批处理',
        label_en: 'Decide approvals',
        category_zh: '审批',
        category_en: 'Approval'
      },
      {
        capability_id: 'post.api.discussions.discussion.id.approvals.approval.id.decision.approvals.decide',
        domain: 'approval',
        action: 'decide',
        access_mode: 'write',
        scope: 'approvals.decide',
        method: 'POST',
        path: '/api/discussions/{discussion_id}/approvals/{approval_id}/decision',
        label_zh: '审批处理',
        label_en: 'Decide discussion runtime approvals',
        category_zh: '审批',
        category_en: 'Approval'
      }
    ],
    policy: {
      roles: [],
      role_assignments: [],
      grants: []
    },
    effective: []
  })
}

function buildIdeaPermissionView() {
  return mapPermissionPolicyCenterView({
    capabilities: [
      {
        capability_id: 'get.api.ideas.idea.read',
        domain: 'idea',
        action: 'read',
        access_mode: 'read',
        scope: 'idea.read',
        method: 'GET',
        path: '/api/ideas',
        label_zh: '想法列表',
        label_en: 'List idea records',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'post.api.ideas.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'POST',
        path: '/api/ideas',
        label_zh: '新建想法',
        label_en: 'Create idea record',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'get.api.ideas.idea.id.idea.read',
        domain: 'idea',
        action: 'read',
        access_mode: 'read',
        scope: 'idea.read',
        method: 'GET',
        path: '/api/ideas/{idea_id}',
        label_zh: '查看想法',
        label_en: 'Read idea record',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'patch.api.ideas.idea.id.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'PATCH',
        path: '/api/ideas/{idea_id}',
        label_zh: '编辑想法',
        label_en: 'Update idea record',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'delete.api.ideas.idea.id.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'DELETE',
        path: '/api/ideas/{idea_id}',
        label_zh: '删除想法',
        label_en: 'Delete idea record',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'get.api.ideas.idea.id.comments.idea.read',
        domain: 'idea',
        action: 'read',
        access_mode: 'read',
        scope: 'idea.read',
        method: 'GET',
        path: '/api/ideas/{idea_id}/comments',
        label_zh: '查看想法评论',
        label_en: 'List idea comments',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'post.api.ideas.idea.id.comments.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'POST',
        path: '/api/ideas/{idea_id}/comments',
        label_zh: '新建想法评论',
        label_en: 'Create idea comment',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'post.api.ideas.idea.id.archive.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'POST',
        path: '/api/ideas/{idea_id}/archive',
        label_zh: '归档想法',
        label_en: 'Archive idea record',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'post.api.ideas.idea.id.restore.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'POST',
        path: '/api/ideas/{idea_id}/restore',
        label_zh: '恢复想法',
        label_en: 'Restore idea record',
        category_zh: '想法',
        category_en: 'Ideas'
      },
      {
        capability_id: 'post.api.ideas.idea.id.convert.to.todo.idea.write',
        domain: 'idea',
        action: 'write',
        access_mode: 'write',
        scope: 'idea.write',
        method: 'POST',
        path: '/api/ideas/{idea_id}/convert-to-todo',
        label_zh: '转为待办',
        label_en: 'Convert idea to todo',
        category_zh: '想法',
        category_en: 'Ideas'
      }
    ],
    policy: {
      roles: [],
      role_assignments: [],
      grants: []
    },
    effective: []
  })
}

function buildTalentMarketPermissionView() {
  return mapPermissionPolicyCenterView({
    capabilities: [
      {
        capability_id: 'get.api.workforce.members.workforce.read',
        domain: 'workforce',
        action: 'read',
        access_mode: 'read',
        scope: 'workforce.read',
        method: 'GET',
        path: '/api/workforce/members',
        label_zh: '人才查看',
        label_en: 'List workforce members',
        category_zh: '人才',
        category_en: 'Workforce'
      },
      {
        capability_id: 'get.api.talent-market.members.workforce.read',
        domain: 'workforce',
        action: 'read',
        access_mode: 'read',
        scope: 'workforce.read',
        method: 'GET',
        path: '/api/talent-market/members',
        label_zh: '人才查看',
        label_en: 'List talent market members',
        category_zh: '人才',
        category_en: 'Workforce'
      },
      {
        capability_id: 'post.api.talent-market.members.delete-batch.workforce.manage',
        domain: 'workforce',
        action: 'manage',
        access_mode: 'write',
        scope: 'workforce.manage',
        method: 'POST',
        path: '/api/talent-market/members/delete-batch',
        label_zh: '人才编辑',
        label_en: 'Batch delete talent market members',
        category_zh: '人才',
        category_en: 'Workforce'
      },
      {
        capability_id: 'post.api.talent-market.members.agent-profile-id.hire.workforce.manage',
        domain: 'workforce',
        action: 'manage',
        access_mode: 'write',
        scope: 'workforce.manage',
        method: 'POST',
        path: '/api/talent-market/members/{agent_profile_id}/hire',
        label_zh: '人才编辑',
        label_en: 'Hire talent market member into workforce',
        category_zh: '人才',
        category_en: 'Workforce'
      }
    ],
    policy: {
      roles: [],
      role_assignments: [],
      grants: []
    },
    effective: []
  })
}
