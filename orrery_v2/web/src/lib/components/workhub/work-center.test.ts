import { describe, expect, it } from 'vitest'
import { buildWorkCenterItems, phaseForIdea, phaseForTodo } from './work-center'
import type { IdeaItemDTO } from '#lib/types/idea'
import type { TodoItemDTO } from '#lib/types/todo'
import type { MissionListItemViewModel } from '#lib/types/view-models/mission'

describe('work center model', () => {
  it('maps persisted entities into the five accepted stages', () => {
    expect(phaseForIdea('under_review')).toBe('explore')
    expect(phaseForIdea('accepted')).toBe('plan')
    expect(phaseForTodo('in_progress')).toBe('mission')
    expect(phaseForTodo('review')).toBe('review')
    expect(phaseForTodo('done')).toBe('done')
  })

  it('combines ideas, todos, and missions with real detail links', () => {
    const items = buildWorkCenterItems([todo()], [idea()], [mission()])

    expect(items.map((item) => item.kind)).toEqual(['mission', 'todo', 'idea'])
    expect(items.map((item) => item.href)).toEqual([
      '/missions/mission-1',
      '/todos/todo-1',
      '/ideas/idea-1'
    ])
    expect(items[1]).toMatchObject({ missionId: 'mission-1', progress: 55, phase: 'mission' })
  })

  it('deduplicates overlapping pages without merging different item types', () => {
    const items = buildWorkCenterItems([todo(), todo()], [idea(), idea()], [mission(), mission()])
    expect(items).toHaveLength(3)
    expect(buildWorkCenterItems([{ ...todo(), todo_id: 'same' }], [{ ...idea(), idea_id: 'same' }], [])).toHaveLength(2)
  })
})

function idea(): IdeaItemDTO {
  return {
    idea_id: 'idea-1', title: 'Explore demand', status: 'submitted', visibility: 'organization',
    impact: 'medium', effort: 'small', vote_count: 0, viewer_has_voted: false, created_by: 'user-1',
    created_at: '2026-08-13T08:00:00Z', updated_at: '2026-08-13T08:00:00Z', version: 1
  }
}

function todo(): TodoItemDTO {
  return {
    todo_id: 'todo-1', title: 'Ship the workflow', status: 'in_progress', priority: 'high',
    work_kind: 'general', automation_mode: 'managed', created_by: 'user-1',
    created_at: '2026-08-14T08:00:00Z', updated_at: '2026-08-14T08:00:00Z', version: 1,
    execution: { attempts: [], current_progress: {
      todo_id: 'todo-1', mission_id: 'mission-1', execution_attempt: 1, mission_status: 'active',
      current_phase: 'execute', delivery_rate: 0.55, total_task_count: 4, active_task_count: 2,
      done_task_count: 2, blocked_task_count: 0, pending_approval_count: 0, current_agent_ids: [],
      last_activity_at: '2026-08-14T08:00:00Z', mission_projection_version: 1,
      can_open_control: true, can_open_discussion: true
    } }
  }
}

function mission(): MissionListItemViewModel {
  return {
    id: 'mission-1', title: 'Deliver workflow', description: 'Complete the accepted plan', status: 'active',
    statusTone: 'success', phase: 'execute', riskLevel: 'medium', riskTone: 'neutral', teamSize: 2,
    activeTaskCount: 2, activeRunCount: 1, pendingApprovalCount: 0, blockerCount: 0, deliveryPercent: 55, deadline: '',
    goals: [], createdBy: 'backend', updatedAt: '2026-08-15T08:00:00Z', originKind: 'todo',
    originId: 'todo-1', originTodoId: 'todo-1', executionAttempt: 1, discussionMode: '',
    conversationId: '', originDevelopmentProjectId: ''
  }
}
