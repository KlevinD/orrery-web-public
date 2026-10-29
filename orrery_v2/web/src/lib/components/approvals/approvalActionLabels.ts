import type { TranslationKey } from '#lib/i18n'
import type { ApprovalActionViewModel } from '#lib/types/view-models'

type Translator = (key: TranslationKey, params?: Record<string, string | number>) => string

const labelKeyByEnglish: Record<string, TranslationKey> = {
  'approve': 'approvals.action.approve',
  'approve once': 'approvals.action.approveOnce',
  'approve for session': 'approvals.action.approveForSession',
  'apply once': 'approvals.action.applyOnce',
  'apply and remember': 'approvals.action.applyAndRemember',
  'apply edited': 'approvals.action.applyEdited',
  'suggest change': 'approvals.action.suggestChange',
  'deny': 'approvals.action.deny',
  'reject patch': 'approvals.action.rejectPatch',
  'answer': 'approvals.action.answer',
  'ask for context': 'approvals.action.askForContext',
  'dismiss': 'approvals.action.dismiss',
  'approve plan': 'approvals.action.approvePlan',
  'request changes': 'approvals.action.requestChanges',
  'reject plan': 'approvals.action.rejectPlan',
  'request rework': 'approvals.action.requestRework',
  'need more info': 'approvals.action.needMoreInfo',
  'reject': 'approvals.action.reject',
  'other': 'approvals.other'
}

const fallbackKeyByID: Record<string, TranslationKey> = {
  approve: 'approvals.action.approve',
  approve_once: 'approvals.action.approveOnce',
  approve_session: 'approvals.action.approveForSession',
  approve_edited: 'approvals.action.applyEdited',
  modify: 'approvals.action.suggestChange',
  deny: 'approvals.action.deny',
  reject: 'approvals.action.reject',
  answer: 'approvals.action.answer',
  needs_context: 'approvals.action.needMoreInfo',
  dismiss: 'approvals.action.dismiss',
  other: 'approvals.other'
}

export function approvalActionLabel(action: ApprovalActionViewModel, t: Translator) {
  const labelKey = labelKeyByEnglish[action.label.trim().toLowerCase()] ?? fallbackKeyByID[action.id.trim().toLowerCase()]
  if (labelKey) return t(labelKey)
  if (action.style === 'danger' || ['denied', 'rejected'].includes(action.decision.trim().toLowerCase())) {
    return t('approvals.action.reject')
  }
  if (action.style === 'primary' || action.decision.trim().toLowerCase() === 'approved') {
    return t('approvals.action.approve')
  }
  return t('approvals.other')
}
