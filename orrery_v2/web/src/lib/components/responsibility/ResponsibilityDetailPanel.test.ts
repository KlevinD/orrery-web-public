import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import ResponsibilityDetailPanel from './ResponsibilityDetailPanel.svelte'
import type { ResponsibilityDetailDTO } from '#lib/types/responsibility'
import type { ComponentProps } from 'svelte'

describe('ResponsibilityDetailPanel', () => {
  it('renders durable attempt history and source routes', () => {
    renderPanel()

    expect(screen.getByText(/run-1/)).toBeTruthy()
    expect(screen.getByRole('link', { name: /打开 Todo/ }).getAttribute('href')).toBe('/todos/todo%2F1')
    expect(screen.getByRole('link', { name: /打开 Mission/ }).getAttribute('href')).toBe('/missions/mission%2F1')
    expect(screen.getByText('1200 tokens')).toBeTruthy()
  })

  it('submits manager controls and blocker resolution through explicit callbacks', async () => {
    const onControl = vi.fn().mockResolvedValue(undefined)
    const onResolveBlocker = vi.fn().mockResolvedValue(undefined)
    renderPanel({ onControl, onResolveBlocker })

    const reason = screen.getByPlaceholderText('操作原因')
    await fireEvent.input(reason, { target: { value: 'stop unsafe work' } })
    await fireEvent.click(screen.getByRole('button', { name: /终止/ }))
    await waitFor(() => expect(onControl).toHaveBeenCalledWith('terminate', { reason: 'stop unsafe work' }))

    await fireEvent.click(screen.getByRole('tab', { name: /阻塞 1/ }))
    const resolution = screen.getByPlaceholderText('填写解决说明')
    await fireEvent.input(resolution, { target: { value: 'access granted' } })
    await fireEvent.click(screen.getByRole('button', { name: '解决阻塞' }))
    await waitFor(() => expect(onResolveBlocker).toHaveBeenCalledWith('blocker-1', 3, 'access granted'))
  })

  it('edits the effective policy and submits all CAS payload fields', async () => {
    const onSavePolicy = vi.fn().mockResolvedValue(undefined)
    renderPanel({ onSavePolicy })

    await fireEvent.click(screen.getByRole('button', { name: /编辑策略/ }))
    const parallel = screen.getByText('最大并发').querySelector('input')
    expect(parallel).toBeTruthy()
    await fireEvent.input(parallel!, { target: { value: '4' } })
    await fireEvent.click(screen.getByRole('button', { name: /保存策略/ }))

    await waitFor(() => expect(onSavePolicy).toHaveBeenCalledWith(expect.objectContaining({
      autonomy_level: 'execute_scoped',
      max_parallel: 4,
      heartbeat_timeout_seconds: 90,
      retry_backoff_seconds: [30, 120],
      enabled: true
    })))
  })
})

type PanelCallbacks = Pick<ComponentProps<typeof ResponsibilityDetailPanel>, 'onControl' | 'onResolveBlocker' | 'onSavePolicy'>

function renderPanel(overrides: Partial<PanelCallbacks> = {}) {
  return render(ResponsibilityDetailPanel, {
    detail: fixture(),
    canManage: true,
    onControl: overrides.onControl ?? vi.fn().mockResolvedValue(undefined),
    onResolveBlocker: overrides.onResolveBlocker ?? vi.fn().mockResolvedValue(undefined),
    onSavePolicy: overrides.onSavePolicy ?? vi.fn().mockResolvedValue(undefined)
  })
}

function fixture(): ResponsibilityDetailDTO {
  return {
    responsibility_id: 'responsibility-1', tenant_id: 'tenant-1', work_kind: 'mission_task', work_id: 'task-1', mission_id: 'mission/1',
    source_kind: 'todo_plan_step', source_id: 'todo/1', source_step_id: 'step-1',
    title: 'Ship responsibility', goal: 'Implement and verify', accountable_agent: 'agent-1', state: 'active', lane: 'now', health: 'at_risk',
    risk_reasons: ['deadline_approaching'], next_action: 'monitor runtime', next_actor: 'agent-1', priority: 75,
    accept_by: '2026-07-27T10:00:00Z', assigned_at: '2026-07-27T09:00:00Z', due_at: '2026-07-28T09:00:00Z', paused: false,
    attempt_count: 1, blocked_seconds: 120, evidence_completeness: 0.5, runtime_outcome_deliveries: 1, duplicate_runtime_outcomes: 0,
    open_blockers: [], open_alerts: [],
    policy: {
      policy_id: 'policy-1', agent_profile_id: 'agent-1', work_kind: 'mission_task', autonomy_level: 'execute_scoped',
      max_parallel: 2, accept_timeout_seconds: 120, heartbeat_timeout_seconds: 90, max_attempts: 3,
      retry_backoff_seconds: [30, 120], max_cost_per_task: 0, max_token_budget: 10000,
      approval_policy: 'only_high_risk', allowed_risk_levels: ['low'], tool_scopes: ['todo.write'], data_scopes: ['mission'],
      escalation_chain: [], recheck_interval_seconds: 900, deadline_warning_seconds: 1800, enabled: true, version: 2
    },
    version: 4, updated_at: '2026-07-27T09:30:00Z',
    attempts: [{
      attempt_id: 'attempt-1', activation_id: 'activation-1', run_id: 'run-1', runtime_kind: 'codex', attempt: 1,
      usage_tokens: 1200, started_at: '2026-07-27T09:10:00Z', heartbeat_at: '2026-07-27T09:20:00Z'
    }],
    activations: [{ activation_id: 'activation-1', cause: 'assigned', status: 'dispatched', not_before: '2026-07-27T09:05:00Z', assignment_version: 2 }],
    blockers: [{
      blocker_id: 'blocker-1', dedupe_key: 'repository-access', kind: 'permission', needed: 'repository access', requested_from: 'manager-1',
      recheck_at: '2026-07-27T10:00:00Z', escalation_level: 0, status: 'open', version: 3, created_at: '2026-07-27T09:25:00Z'
    }],
    alerts: [{
      alert_id: 'alert-1', kind: 'deadline_warning', severity: 'warning', requested_from: 'manager-1', due_at: '2026-07-28T09:00:00Z',
      escalation_level: 0, status: 'open', version: 1, created_at: '2026-07-27T09:25:00Z'
    }],
    audit: [{ audit_id: 'audit-1', entity_type: 'assignment', entity_id: 'responsibility-1', action: 'responsibility.assigned', occurred_at: '2026-07-27T09:00:00Z' }]
  }
}
