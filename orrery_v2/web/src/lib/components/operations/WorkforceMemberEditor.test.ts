import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import WorkforceMemberEditor from './WorkforceMemberEditor.svelte'

describe('WorkforceMemberEditor', () => {
  it('submits selected runtime profile refs without inline provider config', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        onsubmit,
        runtimeOptions: [{ label: 'OpenClaw local', value: 'runtime-openclaw-local', runtimeKind: 'openclaw' }],
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'planner',
                memberCount: 0,
                children: [],
                positions: [{ id: 'planner', name: 'Planner', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Planner Agent' } })
    await fireEvent.input(screen.getByLabelText('Agent mailbox'), { target: { value: 'planner@example.test' } })
    await fireEvent.input(screen.getByLabelText('System prompt'), { target: { value: 'Coordinate work through specialist Agents.' } })
    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: '负责规划运行任务并提交验证证据。' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })
    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'planner' } })
    await openTab(/^Capabilities/)
    await fireEvent.change(screen.getByLabelText('Runtime'), { target: { value: 'runtime-openclaw-local' } })
	await openTab(/^Profile/)
	expect((screen.getByLabelText('System prompt') as HTMLTextAreaElement).value).toBe('Coordinate work through specialist Agents.')

    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        runtimeKind: 'openclaw',
        runtimeConfigRef: 'runtime-openclaw-local',
        runtimeConfig: {},
        prompt: 'Coordinate work through specialist Agents.',
        mailboxEmail: 'planner@example.test',
        positionId: 'planner'
      })
    )
  })

  it('requires generated agents to declare a Chinese responsibility boundary before submit', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        onsubmit,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'planner',
                memberCount: 0,
                children: [],
                positions: [{ id: 'planner', name: 'Planner', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Planner Agent' } })
    await fireEvent.input(screen.getByLabelText('System prompt'), { target: { value: 'Plan releases with explicit risks.' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })
    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'planner' } })

    const submitButton = screen.getByRole('button', { name: 'Upsert' }) as HTMLButtonElement
    expect(submitButton.disabled).toBe(false)
    await fireEvent.click(submitButton)
    expect(onsubmit).not.toHaveBeenCalled()
    expect(screen.getByText('Complete the highlighted fields before saving.')).toBeTruthy()
    expect(screen.getByText('Responsibility boundary is required.')).toBeTruthy()

    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: 'Plan release work.' } })

    await fireEvent.click(submitButton)
    expect(onsubmit).not.toHaveBeenCalled()
    expect(screen.getByText('Use Chinese text for generated agents.')).toBeTruthy()

    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: '负责发布计划、风险梳理和交付验证。' } })
    await fireEvent.click(submitButton)

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        responsibilityBoundary: '负责发布计划、风险梳理和交付验证。'
      })
    )
  })

  it('keeps personnel-managed prompt fields visible when a runtime profile is selected', async () => {
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        runtimeOptions: [{ label: 'OpenClaw local', value: 'runtime-openclaw-local', runtimeKind: 'openclaw' }],
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'planner',
                memberCount: 0,
                children: [],
                positions: [{ id: 'planner', name: 'Planner', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    expect(screen.getByLabelText('System prompt')).toBeTruthy()
    expect(screen.getByLabelText('Behavior boundary')).toBeTruthy()

    await openTab(/^Capabilities/)
    await fireEvent.change(screen.getByLabelText('Runtime'), { target: { value: 'runtime-openclaw-local' } })
    await openTab(/^Profile/)

    expect(screen.getByLabelText('System prompt')).toBeTruthy()
    expect(screen.getByLabelText('Behavior boundary')).toBeTruthy()
    expect(screen.getByLabelText('Responsibility boundary')).toBeTruthy()
  })

  it('submits organization tree selections instead of free typed ids', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        onsubmit,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 2,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'pos-operator',
                memberCount: 0,
                children: [
                  {
                    id: 'dept-ai',
                    name: 'AI Lab',
                    organizationId: 'org-1',
                    parentId: 'dept-ops',
                    defaultPositionId: 'pos-researcher',
                    memberCount: 0,
                    children: [],
                    positions: [{ id: 'pos-researcher', name: 'Researcher', departmentId: 'dept-ai', memberCount: 0, members: [] }]
                  }
                ],
                positions: [{ id: 'pos-operator', name: 'Operator', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    expect(screen.queryByLabelText('Organization ID')).toBeNull()
    expect(screen.queryByLabelText('Department ID')).toBeNull()

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Research Agent' } })
    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: '负责研究任务交付。' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ai' } })
    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'pos-researcher' } })

    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        organizationId: 'org-1',
        departmentId: 'dept-ai',
        positionId: 'pos-researcher'
      })
    )
  })

  it('requires an explicit position selection instead of silently using the department default', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        onsubmit,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'pos-default',
                memberCount: 0,
                children: [],
                positions: [
                  { id: 'pos-other', name: 'Other Backend Position', departmentId: 'dept-ops', memberCount: 0, members: [] },
                  { id: 'pos-default', name: 'Default Backend Position', departmentId: 'dept-ops', memberCount: 0, members: [] }
                ]
              }
            ]
          }
        ]
      }
    })

    expect(screen.queryByLabelText('Role ID list')).toBeNull()

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Planner Agent' } })
    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: '负责部门任务规划。' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })
    expect((screen.getByLabelText('Position') as HTMLSelectElement).value).toBe('')
    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))
    expect(onsubmit).not.toHaveBeenCalled()
    expect(screen.getByText('Select a position.')).toBeTruthy()

    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'pos-other' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))
    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        positionId: 'pos-other',
        roles: ''
      })
    )
  })

  it('submits prompt and resources for agents through semantic controls without backend template locks', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'agent',
        onsubmit,
        skillOptions: [{ label: 'Planning (skill.plan)', value: 'skill.plan' }],
        toolOptions: [{ label: 'Git (tool.git)', value: 'tool.git' }],
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'planner',
                memberCount: 0,
                children: [],
                positions: [{ id: 'planner', name: 'Planner', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Planner Agent' } })
    expect(screen.getByLabelText('System prompt').tagName).toBe('TEXTAREA')
    await fireEvent.input(screen.getByLabelText('System prompt'), { target: { value: 'Plan releases with explicit risks.' } })
    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: '负责发布计划工作。' } })
    await fireEvent.input(screen.getByLabelText('Behavior boundary'), { target: { value: 'Ask before risky actions.' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })
    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'planner' } })
    await fireEvent.click(screen.getByRole('switch', { name: 'Toggle member status' }))
    await openTab(/^Capabilities/)
    expect(screen.queryByLabelText('Skills available')).toBeNull()
    expect(screen.queryByLabelText('Tools available')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Configure Skills' }))
    await chooseTransferOption('Skills available', 'skill.plan')
    await fireEvent.click(screen.getByRole('button', { name: 'Add Skills' }))
    expectTransferOption('Skills selected', 'skill.plan')
    await fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Configure Tools' }))
    await chooseTransferOption('Tools available', 'tool.git')
    await fireEvent.click(screen.getByRole('button', { name: 'Add Tools' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.queryByLabelText('Personal memory')).toBeNull()
    expect(screen.queryByLabelText('Collective memory')).toBeNull()
    expect(screen.queryByLabelText('Permission policy')).toBeNull()
    expect(screen.queryByLabelText('Context injection')).toBeNull()
    expect(screen.queryByLabelText('Summary contract')).toBeNull()
    expect(screen.queryByLabelText('Deliverable contract')).toBeNull()
    expect(screen.queryByLabelText('Evidence contract')).toBeNull()
    expect(screen.queryByLabelText('QA contract')).toBeNull()
    await fireEvent.click(screen.getByRole('switch', { name: 'Toggle QA' }))

    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        agentKind: 'ai',
        employmentType: 'ai_agent',
        runtimeKind: '',
        prompt: 'Plan releases with explicit risks.',
        responsibilityBoundary: '负责发布计划工作。',
        behaviorBoundary: 'Ask before risky actions.',
        skillRefs: 'skill.plan',
        toolRefs: 'tool.git',
        qaEnabled: 'true',
        enabled: 'false'
      })
    )
  })

  it('supports fixed Chinese tool assignment with tag and MCP tool shortcuts', async () => {
    const onsubmit = vi.fn()
    const toolOptions = [
      {
        label: '云迹招聘: 搜索简历 (mcp.yunji-zhaopin.yunji.search_resume)',
        value: 'mcp.yunji-zhaopin.yunji.search_resume',
        meta: '云迹招聘 - yunji.search_resume',
        tags: ['招聘', '候选人'],
        mcpServerId: 'mcp.yunji-zhaopin',
        mcpServerName: '云迹招聘'
      },
      {
        label: '云迹招聘: 创建候选人 (mcp.yunji-zhaopin.yunji.create_candidate)',
        value: 'mcp.yunji-zhaopin.yunji.create_candidate',
        meta: '云迹招聘 - yunji.create_candidate',
        tags: ['候选人'],
        mcpServerId: 'mcp.yunji-zhaopin',
        mcpServerName: '云迹招聘'
      },
      {
        label: '发布审批 (tool.approval)',
        value: 'tool.approval',
        meta: '内置工具',
        tags: ['审批']
      }
    ]
    setLocale('zh-CN')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'agent',
        onsubmit,
        toolOptions,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: '运营部',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'planner',
                memberCount: 0,
                children: [],
                positions: [{ id: 'planner', name: '规划师', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('显示名称'), { target: { value: '招聘 Agent' } })
    await fireEvent.input(screen.getByLabelText('职责边界'), { target: { value: '负责招聘工具调用和候选人信息整理。' } })
    await openTab(/^组织与权限/)
    await fireEvent.change(screen.getByLabelText('组织'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('部门'), { target: { value: 'dept-ops' } })
    await fireEvent.change(screen.getByLabelText('岗位'), { target: { value: 'planner' } })
    await openTab(/^能力配置/)
    await fireEvent.click(screen.getByRole('button', { name: '配置工具' }))

    const dialog = screen.getByRole('dialog', { name: '配置工具' })
    expect(dialog.closest('[data-dialog-overlay]')?.className).toContain('fixed')
    expect(screen.getByText('快速分配')).toBeTruthy()
    expect(screen.getByText('按标签')).toBeTruthy()
    expect(screen.getByText('按 MCP 服务')).toBeTruthy()
    expect(screen.queryByLabelText('Tools available')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '添加 标签 招聘 的 1 个工具' }))
    expectTransferOption('已选工具', 'mcp.yunji-zhaopin.yunji.search_resume')

    await fireEvent.click(screen.getByRole('button', { name: '移除 标签 招聘 的 1 个工具' }))
    expectTransferOptionMissing('已选工具', 'mcp.yunji-zhaopin.yunji.search_resume')

    await fireEvent.click(screen.getByRole('button', { name: '添加 MCP 服务 云迹招聘 的 2 个工具' }))
    expectTransferOption('已选工具', 'mcp.yunji-zhaopin.yunji.search_resume')
    expectTransferOption('已选工具', 'mcp.yunji-zhaopin.yunji.create_candidate')
    await fireEvent.click(screen.getByRole('button', { name: '完成' }))

    await fireEvent.click(screen.getByRole('button', { name: '保存成员' }))

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        toolRefs: 'mcp.yunji-zhaopin.yunji.search_resume, mcp.yunji-zhaopin.yunji.create_candidate'
      })
    )
  })

  it('keeps personal assistant skills and tools when its runtime profile changes', async () => {
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        initial: {
          agentProfileId: 'personal-assistant-1',
          personalAssistant: true,
          displayName: 'My Assistant',
          agentKind: 'ai',
          responsibilityBoundary: '仅处理所属用户授权的任务。',
          organizationId: 'org-1',
          departmentId: 'dept-ops',
          positionId: 'assistant',
          runtimeKind: 'pi',
          runtimeConfigRef: 'runtime-pi-old',
          runtimeConfig: {},
          skillRefs: 'skill.personal-planning',
          toolRefs: 'exec_command',
          enabled: 'true'
        },
        runtimeOptions: [
          { label: 'PI old', value: 'runtime-pi-old', runtimeKind: 'pi' },
          { label: 'PI new', value: 'runtime-pi-new', runtimeKind: 'pi' }
        ],
        skillOptions: [{ label: 'Personal planning', value: 'skill.personal-planning' }],
        toolOptions: [{ label: 'Command execution', value: 'exec_command' }]
      }
    })

    await openTab(/^Capabilities/)
    await fireEvent.change(screen.getByLabelText('Runtime'), { target: { value: 'runtime-pi-new' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Configure Skills' }))
    expectTransferOption('Skills selected', 'skill.personal-planning')
    await fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Configure Tools' }))
    expectTransferOption('Tools selected', 'exec_command')
  })

  it('hides backend template lock choices even when initial values exist', async () => {
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        initial: {
          qaEnabled: 'true'
        }
      }
    })

    await openTab(/^Capabilities/)
    expect(screen.queryByLabelText('Permission policy')).toBeNull()
    expect(screen.queryByLabelText('Context injection')).toBeNull()
    expect(screen.queryByText('policy.worker')).toBeNull()
    expect(screen.queryByText('qa.worker')).toBeNull()
    expect(screen.getByRole('switch', { name: 'Toggle QA' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.queryByRole('option', { name: 'policy.freelance-agent.default' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'Mission task scoped' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'Mission scoped' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'summary.freelance-agent.default' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'deliverable.freelance-task.default' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'evidence.freelance-task.required' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'qa.freelance-agent.standard' })).toBeNull()
  })

  it('creates ordinary human members without rendering People password fields', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'human',
        onsubmit,
        runtimeOptions: [{ label: 'Human inbox runtime', value: 'runtime-human-inbox', runtimeKind: 'human' }],
        roleOptions: [{ label: 'Real human contributor (realhumancontributor)', value: 'realhumancontributor' }],
        skillOptions: [{ label: 'Review skill', value: 'skill.review' }],
        toolOptions: [{ label: 'Approval tool', value: 'tool.approval' }],
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'reviewer',
                memberCount: 0,
                children: [],
                positions: [{ id: 'reviewer', name: 'Reviewer', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Operator User' } })
    await fireEvent.input(screen.getByLabelText('Resume'), { target: { value: 'Reviews release plans.' } })
    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: 'Approve risky changes.' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })
    await fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'reviewer' } })
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Real human contributor (realhumancontributor)' }))
    await openTab(/^Account security/)
    await fireEvent.input(screen.getByLabelText('Username'), { target: { value: 'operator' } })
    expect(screen.queryByLabelText('Runtime')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Configure Skills' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Configure Tools' })).toBeNull()
    expect(screen.queryByRole('switch', { name: 'Toggle QA' })).toBeNull()
    expect(screen.getByRole('switch', { name: 'Toggle login account' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.queryByLabelText('Password')).toBeNull()
    expect(screen.queryByLabelText('Confirm password')).toBeNull()
    const loginSwitch = screen.getByRole('switch', { name: 'Toggle login account' })
    const switchKnob = loginSwitch.querySelector('span')
    expect(switchKnob?.className).toContain('left-0.5')

    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        agentKind: 'human',
        employmentType: 'human',
        accountUsername: 'operator',
        roles: 'realhumancontributor',
        runtimeKind: '',
        runtimeConfigRef: '',
        resume: 'Reviews release plans.',
        responsibilityBoundary: 'Approve risky changes.',
        skillRefs: '',
        toolRefs: '',
        qaEnabled: 'false'
      })
    )
    expect(onsubmit.mock.calls[0]?.[0]).not.toHaveProperty('accountPassword')
    expect(onsubmit.mock.calls[0]?.[0]).not.toHaveProperty('accountPasswordConfirm')
  })

  it('shows projection state without password editing for an ordinary human member', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'human',
        onsubmit,
        initial: {
          agentProfileId: 'profile-operator',
          displayName: 'Operator User',
          agentKind: 'human',
          accountUsername: 'operator',
          accountEnabled: 'true',
          accountProjectionStatus: 'applied',
          accountProjectionRevision: 7,
          organizationId: 'org-1',
          departmentId: 'dept-ops',
          positionId: 'reviewer',
          roles: 'realhumancontributor',
          resume: 'Reviews release plans.',
          responsibilityBoundary: 'Approve risky changes.',
          runtimeKind: '',
          runtimeConfigRef: '',
          runtimeConfig: {},
          enabled: 'true'
        },
        roleOptions: [{ label: 'Real human contributor (realhumancontributor)', value: 'realhumancontributor' }],
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'reviewer',
                memberCount: 0,
                children: [],
                positions: [{ id: 'reviewer', name: 'Reviewer', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    expect(screen.queryByLabelText('Password')).toBeNull()
    expect(screen.queryByRole('checkbox', { name: 'Real human contributor (realhumancontributor)' })).toBeNull()
    await openTab(/^Organization & access/)
    expect(screen.getByRole('checkbox', { name: 'Real human contributor (realhumancontributor)' })).toBeTruthy()
    await openTab(/^Account security/)
    expect(screen.queryByRole('button', { name: 'Change password' })).toBeNull()
    expect(screen.queryByLabelText('Password')).toBeNull()
    expect(screen.getByText('Applied')).toBeTruthy()
    expect(screen.getByText('7')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        roles: 'realhumancontributor'
      })
    )
    expect(onsubmit.mock.calls[0]?.[0]).not.toHaveProperty('accountPassword')
    expect(onsubmit.mock.calls[0]?.[0]).not.toHaveProperty('accountPasswordConfirm')
  })

  it('does not expose local password editing for the break-glass principal', async () => {
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'human',
        initial: {
          agentProfileId: 'human-superadmin',
          displayName: 'Superadmin',
          agentKind: 'human',
          accountUsername: 'superadmin',
          accountEnabled: 'true'
        }
      }
    })

    await openTab(/^Account security/)
    expect(screen.queryByRole('button', { name: 'Change password' })).toBeNull()
    expect(screen.queryByLabelText('Password')).toBeNull()
    expect(screen.queryByLabelText('Confirm password')).toBeNull()
  })

  it('shows only roles assignable to the selected member type', async () => {
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'human',
        roleOptions: [
          { label: 'Human administrator', value: 'human.admin', assignableActorTypes: ['human'] },
          { label: 'Agent operator', value: 'agent.operator', assignableActorTypes: ['agent'] },
          { label: 'Shared member', value: 'member', assignableActorTypes: ['human', 'agent'] }
        ]
      }
    })

    await openTab(/^Organization & access/)
    expect(screen.getByRole('checkbox', { name: 'Human administrator' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'Shared member' })).toBeTruthy()
    expect(screen.queryByRole('checkbox', { name: 'Agent operator' })).toBeNull()

    await openTab(/^Profile/)
    await fireEvent.change(screen.getByLabelText('Member type'), { target: { value: 'ai' } })
    await openTab(/^Organization & access/)

    expect(screen.queryByRole('checkbox', { name: 'Human administrator' })).toBeNull()
    expect(screen.getByRole('checkbox', { name: 'Agent operator' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'Shared member' })).toBeTruthy()
  })

  it('allows editing a member when the organization has no position catalog', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'agent',
        onsubmit,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: '',
                memberCount: 0,
                children: [],
                positions: []
              }
            ]
          }
        ]
      }
    })

    await fireEvent.input(screen.getByLabelText('Display name'), { target: { value: 'Operations Agent' } })
    await fireEvent.input(screen.getByLabelText('Responsibility boundary'), { target: { value: '负责运营任务。' } })
    await openTab(/^Organization & access/)
    await fireEvent.change(screen.getByLabelText('Organization'), { target: { value: 'org-1' } })
    await fireEvent.change(screen.getByLabelText('Department'), { target: { value: 'dept-ops' } })

    expect(screen.queryByLabelText('Position')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: ''
    }))
  })

  it('does not block editing when a legacy position is no longer in the catalog', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'agent',
        initial: {
          agentProfileId: 'profile-legacy',
          displayName: 'Legacy Agent',
          agentKind: 'ai',
          organizationId: 'org-1',
          departmentId: 'dept-ops',
          positionId: 'position-legacy',
          responsibilityBoundary: '负责运营任务。',
          enabled: 'true'
        },
        onsubmit,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 1,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'position-current',
                memberCount: 1,
                children: [],
                positions: [{ id: 'position-current', name: 'Operator', departmentId: 'dept-ops', memberCount: 1, members: [] }]
              }
            ]
          }
        ]
      }
    })

    await openTab(/^Organization & access/)
    expect(screen.queryByLabelText('Position')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Upsert' }))

    expect(onsubmit).toHaveBeenCalledWith(expect.objectContaining({
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'position-legacy'
    }))
  })

  it('keeps save clickable and marks missing human account fields after submit', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')
    render(WorkforceMemberEditor, {
      props: {
        defaultKind: 'human',
        onsubmit,
        organizationTree: [
          {
            id: 'org-1',
            name: 'Orrery',
            unitCount: 1,
            memberCount: 0,
            units: [
              {
                id: 'dept-ops',
                name: 'Operations',
                organizationId: 'org-1',
                parentId: '',
                defaultPositionId: 'reviewer',
                memberCount: 0,
                children: [],
                positions: [{ id: 'reviewer', name: 'Reviewer', departmentId: 'dept-ops', memberCount: 0, members: [] }]
              }
            ]
          }
        ]
      }
    })

    const submitButton = screen.getByRole('button', { name: 'Upsert' }) as HTMLButtonElement
    expect(submitButton.disabled).toBe(false)

    await fireEvent.click(submitButton)

    expect(onsubmit).not.toHaveBeenCalled()
    expect(screen.getByText('Complete the highlighted fields before saving.')).toBeTruthy()
    expect(screen.getByText('Display name is required.')).toBeTruthy()
    expect((screen.getByLabelText('Display name') as HTMLInputElement).className).toContain('border-destructive')
    expect(screen.getByRole('tab', { name: /^Organization & access \(2\)$/ })).toBeTruthy()
    expect(screen.getByRole('tab', { name: /^Account security \(1\)$/ })).toBeTruthy()

    await openTab(/^Organization & access/)
    expect(screen.getByText('Select an organization.')).toBeTruthy()

    await openTab(/^Account security/)
    expect(screen.getByText('Username is required.')).toBeTruthy()
    expect((screen.getByLabelText('Username') as HTMLInputElement).className).toContain('border-destructive')
    expect(screen.queryByLabelText('Password')).toBeNull()
  })
})

async function openTab(name: RegExp) {
  await fireEvent.click(screen.getByRole('tab', { name }))
}

async function chooseTransferOption(label: string, value: string) {
  const select = screen.getByLabelText(label) as HTMLSelectElement
  const option = Array.from(select.options).find((item) => item.value === value)
  if (!option) throw new Error(`missing option ${value}`)
  option.selected = true
  await fireEvent.change(select, { target: { value } })
}

function expectTransferOption(label: string, value: string) {
  const select = screen.getByLabelText(label) as HTMLSelectElement
  expect(Array.from(select.options).some((item) => item.value === value)).toBe(true)
}

function expectTransferOptionMissing(label: string, value: string) {
  const select = screen.getByLabelText(label) as HTMLSelectElement
  expect(Array.from(select.options).some((item) => item.value === value)).toBe(false)
}
