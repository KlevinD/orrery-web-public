import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import MissionTaskBoard from './MissionTaskBoard.svelte'
import type { MissionTaskViewModel } from '#lib/types/view-models'

const task: MissionTaskViewModel = {
  id: 'task-1',
  title: 'Review evidence',
  description: 'Review the evidence pack',
  goal: 'Review the evidence pack',
  boundary: 'IAM only',
  assigneeId: 'agent-1',
  assigneeName: 'Planner',
  assigneeType: 'agent',
  status: 'active',
  statusTone: 'success',
  priority: 'high',
  dueAt: 'Jun 03',
  acceptanceCriteria: ['Report is reviewable'],
  deliverables: ['Report'],
  evidenceRequirements: ['Audit log'],
  latestResultRef: '',
  qaRef: '',
  qaMessage: '',
  qaStatus: 'pending',
  blockerRefs: [],
  blockedReason: '',
  updatedAt: 'Jun 02'
}

describe('MissionTaskBoard', () => {
  it('localizes task status and priority chrome', () => {
    setLocale('zh-CN')

    render(MissionTaskBoard, { props: { tasks: [task] } })

    expect(screen.getByText('进行中')).toBeTruthy()
    expect(screen.getByText('高优')).toBeTruthy()
  })
})
