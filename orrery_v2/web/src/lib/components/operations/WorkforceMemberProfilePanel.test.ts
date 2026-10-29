import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import WorkforceMemberProfilePanel from './WorkforceMemberProfilePanel.svelte'
import WorkforceKnowledgeBasePanel from './WorkforceKnowledgeBasePanel.svelte'
import WorkforceMemoryPanel from './WorkforceMemoryPanel.svelte'
import type { WorkforceMemberViewModel } from '#lib/types/view-models'

const knowledgeQueryMocks = vi.hoisted(() => ({
  getKnowledgeBase: vi.fn(),
  listKnowledgeBases: vi.fn(),
  listKnowledgeDocuments: vi.fn(),
  listKnowledgeBindings: vi.fn()
}))

const memoryQueryMocks = vi.hoisted(() => ({
  useMemoryRecordsQuery: vi.fn(() => ({
    data: {
      records: [{
        memory_id: 'memory-record-1',
        profile_id: 'memory-worker',
        candidate_id: 'candidate-1',
        layer: 'experience',
        scope: { kind: 'agent', id: 'profile-worker' },
        topic: '交付偏好',
        summary: '优先提交小范围、可验证的变更，并在交付前运行相关测试。',
        narrative: '过去三次任务中，小步提交显著降低了回归定位时间。',
        facts: ['修改共享模块时需要补充契约测试。'],
        lessons: ['先验证数据契约，再调整界面。'],
        confidence: 0.91,
        visibility: { mode: 'private' },
        sensitivity: 'low',
        de_id_level: 'none',
        lifecycle_status: 'active',
        created_by: 'profile-worker',
        created_at: '2026-07-30T08:00:00Z',
        updated_at: '2026-08-02T09:30:00Z'
      }, {
        memory_id: 'memory-diary-1',
        profile_id: 'memory-worker',
        candidate_id: 'candidate-diary-1',
        layer: 'fact',
        scope: { kind: 'agent', id: 'profile-worker' },
        topic: 'memory/2026-08-02.md',
        summary: '完成审批流联调，并记录了异常分支的处理结论。',
        narrative: '下一次执行应先确认审批人列表，再提交自动化任务。',
        tags: ['agent_diary', 'diary_date:2026-08-02', 'diary_path:memory/2026-08-02.md'],
        confidence: 1,
        visibility: { mode: 'owner' },
        sensitivity: 'normal',
        de_id_level: 'raw',
        lifecycle_status: 'active',
        created_by: 'profile-worker',
        created_at: '2026-08-02T10:00:00Z',
        updated_at: '2026-08-02T10:00:00Z'
      }],
      total: 2,
      limit: 30
    },
    isLoading: false,
    isError: false,
    error: undefined
  })),
  useMemoryRecordsByProfilesQuery: vi.fn((_profileIds: () => string[]) => ({
    data: {
      records: [{
        memory_id: 'memory-record-shared-1',
        profile_id: 'memory-engineering',
        candidate_id: 'candidate-shared-1',
        layer: 'world',
        scope: { kind: 'organization', id: 'org-1' },
        topic: '交付规范',
        summary: '所有生产变更必须关联验收证据。',
        source_evidence_refs: ['evidence:delivery-policy'],
        confidence: 0.96,
        visibility: { mode: 'organization' },
        sensitivity: 'normal',
        de_id_level: 'team_safe',
        lifecycle_status: 'active',
        created_by: 'agent-reviewer',
        created_at: '2026-07-29T08:00:00Z',
        updated_at: '2026-08-01T09:30:00Z'
      }],
      total: 1,
      limit: 30
    },
    isLoading: false,
    isError: false,
    error: undefined
  }))
}))

const workforceQueryMocks = vi.hoisted(() => ({
  useAgentResponsibilityProfileQuery: vi.fn(() => ({
    data: {
      scope_type: 'agent',
      scope_id: 'profile-worker',
      source: 'people',
      responsibility_count: 1,
      version_count: 1,
      updated_at: '2026-08-06T04:00:00Z',
      portfolios: [{
        binding_id: 'people-binding-1',
        people_tenant_id: 'tenant-preview',
        subject_id: 'member-worker',
        agent_profile_id: 'profile-worker',
        agent_display_name: 'Worker',
        status: 'active',
        current_version_id: 'version-1',
        current_source_revision: '39:25',
        current_source_digest: 'digest-1',
        pulse_scheduled_task_id: 'responsibility-pulse-1',
        source_as_of: '2026-08-06T03:54:37Z',
        synced_at: '2026-08-06T04:00:00Z',
        gap: undefined,
        audit: [{
          audit_id: 'audit-1',
          entity_type: 'people_responsibility_portfolio_version',
          entity_id: 'version-1',
          action: 'people_responsibility_portfolio_version_recorded',
          to_state: '39:25',
          occurred_at: '2026-08-06T04:00:00Z'
        }],
        responsibilities: [{
          id: 'responsibility-1',
          code: 'RESP-POSITION-WORKER',
          title: 'Deliver verified work',
          description: 'Own delivery and retain verification evidence.',
          valid_from: '2026-06-21T00:00:00Z',
          valid_to: null,
          revision: 1,
          sources: [{
            binding_id: 'source-binding-1',
            responsibility_id: 'responsibility-1',
            target_type: 'position',
            target_id: 'pos-worker',
            valid_from: '2026-06-21T00:00:00Z',
            valid_to: null,
            revision: 1
          }]
        }],
        versions: [{
          version_id: 'version-1',
          source_revision: '39:25',
          source_digest: 'digest-1',
          source_as_of: '2026-08-06T03:54:37Z',
          recorded_at: '2026-08-06T04:00:00Z',
          responsibilities: [{
            id: 'responsibility-1',
            code: 'RESP-POSITION-WORKER',
            title: 'Deliver verified work',
            description: 'Own delivery and retain verification evidence.',
            valid_from: '2026-06-21T00:00:00Z',
            valid_to: null,
            revision: 1,
            sources: [{
              binding_id: 'source-binding-1',
              responsibility_id: 'responsibility-1',
              target_type: 'position',
              target_id: 'pos-worker',
              valid_from: '2026-06-21T00:00:00Z',
              valid_to: null,
              revision: 1
            }]
          }]
        }]
      }]
    },
    isLoading: false,
    isError: false,
    error: undefined
  }))
}))

const settingsQueryMocks = vi.hoisted(() => ({
  useSettingsQuery: vi.fn()
}))

const resourceQueryMocks = vi.hoisted(() => ({
  useMemoryProfileQuery: vi.fn()
}))

vi.mock('#lib/queries/knowledge', () => knowledgeQueryMocks)
vi.mock('#lib/queries/memory', () => memoryQueryMocks)
vi.mock('#lib/queries/workforce', () => workforceQueryMocks)
vi.mock('#lib/queries/settings', () => settingsQueryMocks)
vi.mock('#lib/queries/resources', () => resourceQueryMocks)

describe('WorkforceMemberProfilePanel', () => {
  const internalPlacement = {
    placementStatus: 'internal',
    releasedFromOrganizationId: '',
    releasedFromDepartmentId: '',
    releasedFromPositionId: '',
    releasedAt: '',
    releasedReason: ''
  }
  const emptyUserAccount = {
    username: '',
    nickname: '',
    email: '',
    role: '',
    accountEnabled: false,
    permissionScope: []
  }

  beforeEach(() => {
    vi.clearAllMocks()
    settingsQueryMocks.useSettingsQuery.mockReturnValue({
      data: { memory: { enabled: true } },
      isLoading: false,
      isError: false,
      error: undefined
    })
    resourceQueryMocks.useMemoryProfileQuery.mockReturnValue({
      data: { id: 'memory-worker', enabled: true },
      isLoading: false,
      isError: false,
      error: undefined
    })
    knowledgeQueryMocks.getKnowledgeBase.mockResolvedValue({
      id: 'kb-worker',
      name: 'Engineering Knowledge',
      description: 'Architecture and delivery references.',
      owner_type: 'agent',
      owner_id: 'profile-worker',
      is_default: true,
      visibility: 'private',
      provider: 'pageindex',
      status: 'active'
    })
    knowledgeQueryMocks.listKnowledgeBases.mockResolvedValue({
      items: [{
        id: 'kb-worker',
        name: 'Engineering Knowledge',
        description: 'Architecture and delivery references.',
        owner_type: 'agent',
        owner_id: 'profile-worker',
        is_default: true,
        visibility: 'private',
        provider: 'pageindex',
        status: 'active'
      }],
      total: 1
    })
    knowledgeQueryMocks.listKnowledgeDocuments.mockResolvedValue({
      items: [{
        id: 'doc-architecture',
        knowledge_base_id: 'kb-worker',
        filename: 'architecture.pdf',
        size_bytes: 4096,
        status: 'indexed'
      }],
      total: 1
    })
    knowledgeQueryMocks.listKnowledgeBindings.mockResolvedValue({
      items: [{
        id: 'binding-worker',
        knowledge_base_id: 'kb-worker',
        bound_to_type: 'agent',
        bound_to_id: 'profile-worker',
        access: 'manage'
      }],
      total: 1
    })
  })

  it('shows agent prompt, resources, memory, and the current employment information', async () => {
    setLocale('en-US')
    const onedit = vi.fn()
    render(WorkforceMemberProfilePanel, {
      props: {
        member: {
          id: 'agent-worker',
          profileId: 'profile-worker',
          agentInstanceId: 'profile-worker:default',
          realName: 'Worker Agent',
          displayName: 'Worker',
          agentKind: 'ai',
          userAccount: emptyUserAccount,
          employmentType: 'ai_agent',
          organizationId: 'org-1',
          departmentId: 'dept-ops',
          positionId: 'pos-worker',
          departmentDisplayName: 'Operations',
          positionDisplayName: 'Worker',
          roleName: 'Worker',
          roleLabels: ['Worker'],
          primaryRoleLabel: 'Worker',
          capabilityTags: [],
          roles: ['worker'],
          managerId: 'Not assigned',
          authorityScope: 'repo:write',
          supervisionPolicy: 'supervised',
          workspaceRoot: '/srv/orrery/workspaces/agent-worker',
          runtimeKind: 'codex',
          runtimeConfigRef: 'runtime-codex',
          runtimeConfig: { model: 'codex-fixture' },
          baseAgent: {
            inheritsBaseAgent: true,
            auditRequired: true,
            hasSystemPrompt: true,
            hasLlmApiKeyRef: true,
            llmApiKeyRef: 'secret://system/llm-api-key',
            skillScope: 'profile_bound',
            toolScope: 'profile_bound',
            mcpScope: 'profile_bound'
          },
          resume: '',
          prompt: 'You are the personnel-managed backend execution Agent.',
          responsibilityBoundary: 'Own backend task delivery.',
          behaviorBoundary: 'Do not modify unrelated files.',
          knowledgeBaseId: 'kb-worker',
          skillRefs: [{ skillId: 'skill.tdd', versionConstraint: '^1', alias: 'TDD', level: 'senior', evidenceRefs: ['evidence.tests'] }],
          toolRefs: [{ toolId: 'tool.git', versionConstraint: '>=2', alias: 'Git', permissionScope: 'repo:write', bindingPolicy: 'approval_required' }],
          personalMemoryRef: {
            memoryProfileId: 'memory-worker',
            ownerAgentId: 'agent-worker',
            memoryKind: 'personal',
            accessScope: 'agent',
            retrievalPolicy: '',
            compressionPolicy: ''
          },
          collectiveMemoryRefs: [
            {
              memoryProfileId: 'memory-engineering',
              ownerAgentId: '',
              memoryKind: 'organization',
              accessScope: 'org',
              retrievalPolicy: '',
              compressionPolicy: ''
            }
          ],
          qaEnabled: true,
          enabled: true,
          statusLabel: 'Enabled',
          statusTone: 'success',
          email: 'Not provided',
          availabilityStatus: 'available',
          ...internalPlacement
        } satisfies WorkforceMemberViewModel,
        organizationName: 'Acme',
        managerName: 'Team Lead',
        onedit
      }
    })

    expect(workforceQueryMocks.useAgentResponsibilityProfileQuery).toHaveBeenCalledTimes(2)
    const responsibilityQueryCalls = workforceQueryMocks.useAgentResponsibilityProfileQuery.mock.calls as unknown as Array<[
      () => string,
      () => boolean,
      () => number
    ]>
    const currentOnlyLimit = responsibilityQueryCalls[0]?.[2]
    const historyEnabled = responsibilityQueryCalls[1]?.[1]
    const historyLimit = responsibilityQueryCalls[1]?.[2]
    expect(currentOnlyLimit?.()).toBe(0)
    expect(historyLimit?.()).toBe(20)
    expect(historyEnabled?.()).toBe(false)

    expect(screen.getByText('System prompt')).toBeTruthy()
    expect(screen.queryByText('Position responsibility relation')).toBeNull()
    expect(screen.getByText('You are the personnel-managed backend execution Agent.')).toBeTruthy()
    expect(screen.getByText('skill.tdd')).toBeTruthy()
    expect(screen.getByText('tool.git')).toBeTruthy()
    expect(screen.getByText('Personal memory connected')).toBeTruthy()
    expect(screen.getByText('Access to 1 collective memory spaces')).toBeTruthy()
    expect(screen.queryByText('Template locks')).toBeNull()
    expect(screen.queryByText('Permission policy')).toBeNull()
    expect(screen.queryByText('QA enabled')).toBeNull()
    expect(screen.queryByText('policy.worker')).toBeNull()
    expect(screen.queryByText('deliverable.patch')).toBeNull()
		expect(screen.queryByText('Employment timeline')).toBeNull()
		expect(screen.getByRole('tab', { name: 'Feishu accounts' })).toBeTruthy()

    const skillsTab = screen.getByRole('tab', { name: 'Skills' })
    await fireEvent.click(skillsTab)
    expect(skillsTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('skill.tdd')).toBeTruthy()

    const toolsTab = screen.getByRole('tab', { name: 'Tools' })
    await fireEvent.click(toolsTab)
    expect(toolsTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('tool.git')).toBeTruthy()

    const knowledgeTab = screen.getByRole('tab', { name: 'Knowledge base' })
    await fireEvent.click(knowledgeTab)
    expect(knowledgeTab.getAttribute('aria-selected')).toBe('true')
    await waitFor(() => expect(screen.getAllByText('Engineering Knowledge')).toHaveLength(2))
    expect(screen.getByText('architecture.pdf')).toBeTruthy()
    expect(screen.getByRole('link', { name: /Manage knowledge base/ }).getAttribute('href')).toBe('/resources/knowledge/kb-worker')
    expect(knowledgeQueryMocks.listKnowledgeBases).toHaveBeenCalledWith(
      { ownerType: 'agent', ownerId: 'profile-worker' },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    expect(knowledgeQueryMocks.getKnowledgeBase).toHaveBeenCalledWith('kb-worker', expect.objectContaining({ signal: expect.any(AbortSignal) }))

    const memoryTab = screen.getByRole('tab', { name: 'Memory' })
    await fireEvent.click(memoryTab)
    expect(memoryTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('Memory content')).toBeTruthy()
    expect(screen.getByText('优先提交小范围、可验证的变更，并在交付前运行相关测试。')).toBeTruthy()
    expect(screen.getByText('过去三次任务中，小步提交显著降低了回归定位时间。')).toBeTruthy()
    expect(screen.getByText('修改共享模块时需要补充契约测试。')).toBeTruthy()
    expect(screen.queryByText('memory-worker')).toBeNull()
    expect(screen.getByRole('tab', { name: /Personal memory/ }).classList.contains('selection-wash')).toBe(true)

    const diaryTab = screen.getByRole('tab', { name: /Diary 1/ })
    await fireEvent.click(diaryTab)
    expect(diaryTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText(/Diary for Aug 2, 2026/)).toBeTruthy()
    expect(screen.getByText('完成审批流联调，并记录了异常分支的处理结论。')).toBeTruthy()
    expect(screen.queryByText('memory/2026-08-02.md')).toBeNull()

    const collectiveMemoryTab = screen.getByRole('tab', { name: /Collective memory/ })
    await fireEvent.click(collectiveMemoryTab)
    expect(collectiveMemoryTab.getAttribute('aria-selected')).toBe('true')
    expect(collectiveMemoryTab.classList.contains('selection-wash')).toBe(true)
    expect(screen.getByText('所有生产变更必须关联验收证据。')).toBeTruthy()
    expect(screen.getByText('evidence:delivery-policy')).toBeTruthy()
    expect(screen.getByText('Confidence 96%')).toBeTruthy()
    const collectiveProfileIds = memoryQueryMocks.useMemoryRecordsByProfilesQuery.mock.calls.at(-1)?.[0]
    expect(collectiveProfileIds).toBeTypeOf('function')
    expect(collectiveProfileIds?.()).toEqual(['memory-engineering'])

    const worldLayerTab = screen.getByRole('tab', { name: /World model 1/ })
    await fireEvent.click(worldLayerTab)
    expect(worldLayerTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('所有生产变更必须关联验收证据。')).toBeTruthy()

    await fireEvent.click(screen.getByRole('tab', { name: /Fact 0/ }))
    expect(screen.getByText('There are no memories in this layer.')).toBeTruthy()

		const rolesTab = screen.getByRole('tab', { name: 'Roles' })
    await fireEvent.click(rolesTab)
    expect(rolesTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText(/runtime-codex/)).toBeTruthy()

    const responsibilitiesTab = screen.getByRole('tab', { name: 'Responsibilities' })
    await fireEvent.click(responsibilitiesTab)
    expect(responsibilitiesTab.getAttribute('aria-selected')).toBe('true')
    expect(historyEnabled?.()).toBe(true)
    expect(screen.getByText('Deliver verified work')).toBeTruthy()
    expect(screen.getAllByText('39:25').length).toBeGreaterThan(0)
    expect(screen.getByText('pos-worker')).toBeTruthy()
    expect(screen.getByText('Current version')).toBeTruthy()
    expect(screen.getByText('Agent execution responsibility boundary')).toBeTruthy()
    expect(screen.getByText('Formal responsibilities')).toBeTruthy()
    expect(screen.getAllByText('Own backend task delivery.').length).toBeGreaterThan(0)
    await fireEvent.click(screen.getByRole('button', { name: 'Edit boundary' }))
    expect(onedit).toHaveBeenCalledTimes(1)

    await fireEvent.click(screen.getByRole('tab', { name: 'Employment information' }))

    expect(screen.getByText('Current assignment')).toBeTruthy()
    expect(screen.getByText('Internal member')).toBeTruthy()
    expect(screen.getByText('AI agent')).toBeTruthy()
    expect(screen.getByText('Acme')).toBeTruthy()
    expect(screen.getByText('Team Lead')).toBeTruthy()
    expect(screen.getByText('Supervised execution')).toBeTruthy()
    expect(screen.getByText('Permissions and responsibilities')).toBeTruthy()
    expect(screen.queryByText('Employment timeline')).toBeNull()
    expect(screen.queryByText('Joined')).toBeNull()
    expect(screen.getAllByText('Own backend task delivery.').length).toBeGreaterThan(0)
    expect(screen.getByText('Do not modify unrelated files.')).toBeTruthy()
  })

  it('explains why scheduled runs cannot form memory when capture is disabled', () => {
    setLocale('en-US')
    settingsQueryMocks.useSettingsQuery.mockReturnValue({
      data: { memory: { enabled: false } },
      isLoading: false,
      isError: false,
      error: undefined
    })

    render(WorkforceMemoryPanel, {
      props: {
        memberName: 'Worker',
        ownerId: 'profile-worker',
        personalProfileId: 'memory-worker',
        collectiveProfileIds: []
      }
    })

    expect(screen.getByText('Memory capture is disabled')).toBeTruthy()
    expect(screen.getByText(/scheduled Agent runs cannot form new memories/)).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Open memory settings' }).getAttribute('href')).toBe('/settings')
  })

  it('shows a separate warning for a disabled personal memory profile', () => {
    setLocale('en-US')
    resourceQueryMocks.useMemoryProfileQuery.mockReturnValue({
      data: { id: 'memory-worker', enabled: false },
      isLoading: false,
      isError: false,
      error: undefined
    })

    render(WorkforceMemoryPanel, {
      props: {
        memberName: 'Worker',
        ownerId: 'profile-worker',
        personalProfileId: 'memory-worker',
        collectiveProfileIds: []
      }
    })

    expect(screen.getByText('Personal memory is disabled for this member')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Manage memory' }).getAttribute('href')).toBe('/resources/memory')
    expect(screen.getByRole('link', { name: 'Open memory settings' }).getAttribute('href')).toBe('/resources/memory')
  })

  it('uses localized empty copy when profile-bound resources are absent', async () => {
    setLocale('en-US')
    render(WorkforceMemberProfilePanel, {
      props: {
        member: {
          id: 'agent-unbound',
          profileId: 'agent-unbound',
          agentInstanceId: 'agent-unbound:default',
          realName: 'Unbound Agent',
          displayName: 'Unbound Agent',
          agentKind: 'ai',
          userAccount: emptyUserAccount,
          employmentType: 'ai_agent',
          organizationId: 'org-1',
          departmentId: 'dept-ops',
          positionId: 'pos-worker',
          departmentDisplayName: 'Operations',
          positionDisplayName: 'Worker',
          roleName: 'Worker',
          roleLabels: ['Worker'],
          primaryRoleLabel: 'Worker',
          capabilityTags: ['old-capability-tag'],
          roles: ['worker'],
          managerId: '',
          authorityScope: 'market_agent',
          supervisionPolicy: 'supervised',
          workspaceRoot: '',
          runtimeKind: '',
          runtimeConfigRef: '',
          runtimeConfig: {},
          baseAgent: {
            inheritsBaseAgent: false,
            auditRequired: false,
            hasSystemPrompt: false,
            hasLlmApiKeyRef: false,
            llmApiKeyRef: '',
            skillScope: '',
            toolScope: '',
            mcpScope: ''
          },
          resume: '',
          prompt: '',
          responsibilityBoundary: '',
          behaviorBoundary: '',
          skillRefs: [],
          toolRefs: [],
          personalMemoryRef: {
            memoryProfileId: '',
            ownerAgentId: '',
            memoryKind: '',
            accessScope: '',
            retrievalPolicy: '',
            compressionPolicy: ''
          },
          collectiveMemoryRefs: [],
          qaEnabled: false,
          enabled: true,
          statusLabel: 'Enabled',
          statusTone: 'success',
          email: '',
          availabilityStatus: '',
          placementStatus: 'market',
          releasedFromOrganizationId: 'org-old',
          releasedFromDepartmentId: 'dept-old',
          releasedFromPositionId: 'pos-old',
          releasedAt: '2026-07-20T09:00:00Z',
          releasedReason: 'Team adjustment'
        } satisfies WorkforceMemberViewModel,
        organizationName: 'Previous Acme',
        departmentName: 'Previous Operations',
        positionName: 'Previous Operator'
      }
    })

    expect(screen.queryByText('old-capability-tag')).toBeNull()
    await fireEvent.click(screen.getByRole('tab', { name: 'Employment information' }))
    expect(screen.getByText('Talent market')).toBeTruthy()
    expect(screen.getByText('Recent departure information')).toBeTruthy()
    expect(screen.getByText('Previous Acme')).toBeTruthy()
    expect(screen.getByText('Previous Operations')).toBeTruthy()
    expect(screen.getByText('Previous Operator')).toBeTruthy()
    expect(screen.getByText('2026-07-20T09:00:00Z')).toBeTruthy()
    expect(screen.getByText('Team adjustment')).toBeTruthy()
    expect(screen.getByText('market_agent')).toBeTruthy()
    expect(screen.queryByText('Not provided')).toBeNull()
    expect(screen.getAllByText('Not configured').length).toBeGreaterThan(0)

    knowledgeQueryMocks.listKnowledgeBases.mockResolvedValueOnce({ items: [], total: 0 })
    await fireEvent.click(screen.getByRole('tab', { name: 'Knowledge base' }))
    expect(screen.getByText('No knowledge base bound')).toBeTruthy()
    expect(knowledgeQueryMocks.listKnowledgeBases).toHaveBeenCalledWith(
      { ownerType: 'agent', ownerId: 'agent-unbound' },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    expect(knowledgeQueryMocks.getKnowledgeBase).not.toHaveBeenCalled()
  })

  it('shows the empty state instead of demo knowledge when the API catalog is empty', async () => {
    setLocale('en-US')
    knowledgeQueryMocks.listKnowledgeBases.mockResolvedValueOnce({ items: [], total: 0 })

    render(WorkforceKnowledgeBasePanel, {
      props: {
        memberName: 'Xiaodoubao',
        ownerId: 'xiaodoubao',
        ownerType: 'agent'
      }
    })

    await waitFor(() => expect(screen.getByText('No knowledge base bound')).toBeTruthy())
    expect(screen.queryByText('Demo data')).toBeNull()
    expect(knowledgeQueryMocks.listKnowledgeDocuments).not.toHaveBeenCalled()
    expect(knowledgeQueryMocks.listKnowledgeBindings).not.toHaveBeenCalled()
  })
})
