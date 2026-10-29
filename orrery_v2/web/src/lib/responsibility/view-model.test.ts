import { describe, expect, it } from 'vitest'
import {
  durationLabel,
  filterResponsibilities,
  percentage,
  responsibilitySourceLinks
} from './view-model'
import type { ResponsibilityInboxItemDTO } from '#lib/types/responsibility'

describe('responsibility view model', () => {
  it('filters lane and risk text without changing server ordering', () => {
    const items = [
      fixture({ responsibility_id: 'risk-1', lane: 'risk', title: 'Deploy API', risk_reasons: ['overdue'] }),
      fixture({ responsibility_id: 'now-1', lane: 'now', title: 'Review feedback', risk_reasons: [] })
    ]
    expect(filterResponsibilities(items, 'risk', '')).toEqual([items[0]])
    expect(filterResponsibilities(items, 'all', 'OVERDUE')).toEqual([items[0]])
    expect(filterResponsibilities(items, 'all', 'feedback')).toEqual([items[1]])
  })

  it('maps Todo, Scheduler, and Mission origins to inspectable product routes', () => {
    expect(responsibilitySourceLinks(fixture({
      source_kind: 'todo_plan_step', source_id: 'todo/1', mission_id: 'mission/1'
    }))).toEqual([
      { kind: 'todo', href: '/todos/todo%2F1' },
      { kind: 'mission', href: '/missions/mission%2F1' }
    ])
    expect(responsibilitySourceLinks(fixture({
      source_kind: 'scheduler_agent_task', source_id: 'scheduled/1', source_step_id: 'run/1'
    }))).toEqual([
      { kind: 'scheduler', href: '/scheduled-tasks?task=scheduled%2F1&run=run%2F1' }
    ])
  })

  it('formats operational rates and durations with bounded output', () => {
    expect(percentage(0.826)).toBe('83%')
    expect(percentage(2)).toBe('100%')
    expect(durationLabel(95)).toBe('2m')
    expect(durationLabel(7200)).toBe('2.0h')
  })
})

function fixture(overrides: Partial<ResponsibilityInboxItemDTO> = {}): ResponsibilityInboxItemDTO {
  return {
    responsibility_id: 'responsibility-1', tenant_id: 'tenant-1', work_kind: 'mission_task', work_id: 'task-1',
    title: 'Responsibility', goal: 'Complete work', accountable_agent: 'agent-1', state: 'active', lane: 'now', health: 'healthy',
    risk_reasons: [], next_action: 'monitor', next_actor: 'agent-1', priority: 50,
    accept_by: '2026-07-27T10:00:00Z', assigned_at: '2026-07-27T09:00:00Z', paused: false,
    attempt_count: 1, blocked_seconds: 0, evidence_completeness: 0.5, runtime_outcome_deliveries: 0,
    duplicate_runtime_outcomes: 0, open_blockers: [], open_alerts: [],
    policy: {
      policy_id: 'policy-1', agent_profile_id: 'agent-1', work_kind: 'mission_task', autonomy_level: 'execute_scoped',
      max_parallel: 2, accept_timeout_seconds: 120, heartbeat_timeout_seconds: 90, max_attempts: 3,
      retry_backoff_seconds: [30], max_cost_per_task: 0, max_token_budget: 10000,
      approval_policy: 'only_high_risk', allowed_risk_levels: [], tool_scopes: [], data_scopes: [], escalation_chain: [],
      recheck_interval_seconds: 900, deadline_warning_seconds: 1800, enabled: true, version: 1
    },
    version: 1, updated_at: '2026-07-27T09:30:00Z',
    ...overrides
  }
}
