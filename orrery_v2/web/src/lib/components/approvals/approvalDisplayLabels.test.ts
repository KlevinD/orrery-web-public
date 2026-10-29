import { describe, expect, it } from 'vitest'
import { createTranslator } from '#lib/i18n'
import { approvalActionLabel } from './approvalActionLabels'
import {
  approvalActorDisplayName,
  approvalOperationLabel,
  approvalReferenceLabel,
  approvalRiskDisplayLabel,
  approvalStatusDisplayLabel,
  approvalTitleLabel,
  approvalTypeDisplayLabel,
  councilPositionDisplayLabel,
  councilVoteDisplayLabel,
  participantRoleDisplayLabel,
  workflowRevisionLabel
} from './approvalDisplayLabels'

describe('approval display labels', () => {
  it('uses natural Chinese labels for canonical approval fields', () => {
    expect(approvalOperationLabel('communication.wechat_draft.create', 'zh-CN')).toBe('创建微信公众号草稿')
    expect(approvalTitleLabel('runtime.approval', 'zh-CN')).toBe('执行操作确认')
    expect(approvalTitleLabel('Runtime approval required for memory_diary_append', 'zh-CN')).toBe('操作确认：写入工作记忆')
    expect(approvalTitleLabel('Runtime approval required for load_skill', 'zh-CN')).toBe('操作确认：加载任务能力')
    expect(approvalTypeDisplayLabel('HIL', 'zh-CN')).toBe('人工确认')
    expect(approvalStatusDisplayLabel('changes_requested', 'zh-CN')).toBe('待修改')
    expect(approvalRiskDisplayLabel('critical', 'zh-CN')).toBe('严重风险')
    expect(councilPositionDisplayLabel('approve', 'zh-CN')).toBe('建议通过')
    expect(councilVoteDisplayLabel('approve', 'zh-CN')).toBe('已同意')
    expect(participantRoleDisplayLabel('owner', 'zh-CN')).toBe('负责人')
    expect(approvalActorDisplayName('human:ding', 'zh-CN')).toBe('ding')
    expect(approvalReferenceLabel('approval-1', 'zh-CN')).toBe('审批编号：approval-1')
    expect(workflowRevisionLabel(2, 'zh-CN')).toBe('第 2 版')
  })

  it('does not expose unknown machine values as user-facing labels', () => {
    expect(approvalOperationLabel('vendor.internal_action.execute', 'zh-CN')).toBe('需要审批的操作')
    expect(approvalOperationLabel('internalaction', 'zh-CN')).toBe('需要审批的操作')
    expect(approvalTypeDisplayLabel('vendor_approval', 'zh-CN')).toBe('其他审批')
    expect(approvalStatusDisplayLabel('queued_for_review', 'zh-CN')).toBe('状态待确认')
    expect(approvalRiskDisplayLabel('extreme', 'zh-CN')).toBe('风险未标注')
    expect(councilPositionDisplayLabel('conditional', 'zh-CN')).toBe('已提交意见')
    expect(councilVoteDisplayLabel('conditional', 'zh-CN')).toBe('已投票')
    expect(participantRoleDisplayLabel('internal_owner_role', 'zh-CN')).toBe('参与人')
    expect(approvalActionLabel({
      id: 'vendor_execute', label: 'vendor_execute', style: 'primary', decision: 'approved', effect: 'once',
      inputKind: 'message', defaultMessage: '', method: 'POST', path: '/decision', reasonRequired: false,
      confirmationRequired: true, allowsModification: false, rememberAllowed: false
    }, createTranslator('zh-CN'))).toBe('批准')
  })
})
