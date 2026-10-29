import type { components } from '#lib/types/api.generated'

export type ResponsibilityWorkKind = components['schemas']['ResponsibilityWorkKind']
export type ResponsibilityAssignmentState = components['schemas']['ResponsibilityAssignmentState']
export type ResponsibilityInboxPageDTO = components['schemas']['ResponsibilityInboxPageDTO']
export type ResponsibilityInboxItemDTO = components['schemas']['ResponsibilityInboxItemDTO']
export type ResponsibilityDetailDTO = components['schemas']['ResponsibilityDetailDTO']
export type ResponsibilityPolicyDTO = components['schemas']['ResponsibilityPolicyDTO']
export type ResponsibilityPolicyUpdateRequest = components['schemas']['ResponsibilityPolicyUpdateRequest']
export type ResponsibilityBlockerDTO = components['schemas']['ResponsibilityBlockerDTO']
export type ResponsibilityControlRequest = components['schemas']['ResponsibilityControlRequest']
export type ResponsibilityExternalSignalRequest = components['schemas']['ResponsibilityExternalSignalRequest']
export type ResponsibilityExternalSignalResultDTO = components['schemas']['ResponsibilityExternalSignalResultDTO']
export type ResolveResponsibilityBlockerRequest = components['schemas']['ResolveResponsibilityBlockerRequest']

export type ResponsibilityLane = ResponsibilityInboxItemDTO['lane']
export type ResponsibilityControlAction = 'pause' | 'resume' | 'retry' | 'terminate' | 'reassign'

export interface ResponsibilityFilters {
  agentId?: string
  states?: ResponsibilityAssignmentState[]
  limit?: number
}
