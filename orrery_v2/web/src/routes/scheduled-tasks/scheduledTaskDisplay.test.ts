import { describe, expect, it } from 'vitest'
import { scheduledTaskDisplayTitle, scheduledTaskTargetSummary } from './scheduledTaskDisplay'

describe('scheduled task display', () => {
  it('localizes built-in task records without changing custom titles', () => {
    expect(scheduledTaskDisplayTitle({
      scheduled_task_id: 'todo-agent-automation-periodic-scan',
      title: 'Todo Agent periodic review'
    }, 'zh-CN')).toBe('待办任务自动巡检')
    expect(scheduledTaskDisplayTitle({ scheduled_task_id: 'custom-1', title: 'Customer sync' }, 'zh-CN')).toBe('Customer sync')
  })

  it('derives Chinese target text from structured target modes', () => {
    expect(scheduledTaskTargetSummary({
      target_mode: 'memory_experience_distillation',
      target_summary: 'experience distillation qa to project project-1'
    }, 'zh-CN')).toBe('经验记忆提炼 · qa · 项目 project-1')
    expect(scheduledTaskTargetSummary({
      target_mode: 'tool_call',
      target_summary: 'tool legal_enterprise_search'
    }, 'zh-CN')).toBe('工具 / MCP · legal_enterprise_search')
    expect(scheduledTaskTargetSummary({
      target_mode: 'responsibility_evaluation',
      target_summary: 'responsibility evaluation for agent agent-owner'
    }, 'zh-CN')).toBe('责任评估 · agent-owner')
    expect(scheduledTaskTargetSummary({
      target_mode: 'responsibility_evaluation',
      target_summary: 'responsibility evaluation for People tenant people-tenant-1'
    }, 'zh-CN')).toBe('责任评估 · 全部 People 责任 · people-tenant-1')
  })
})
