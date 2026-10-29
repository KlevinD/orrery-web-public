import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { approvalApi } from '#lib/api/approvals'
import { ApiClientError } from '#lib/api/client'
import { mapApprovalCenterView, mapApprovalDecisionRequest } from '#lib/mappers/approval'
import { collapseRepeatedPendingRuntimeApprovals, mapHILCouncil, mapHILDecisionPage, mapHILDecisionRequest, mapHILDecisionToApprovalItem } from '#lib/mappers/hil'
import { mapHILWorkflowInstance } from '#lib/mappers/workflow'
import { createIdempotencyKey } from '#lib/runtime/ids'
import { operationsQueryKeys } from './operations'
import type {
  ApprovalPageDTO,
  CastHILCouncilVoteRequest,
  DecideHILWorkflowTaskRequest,
  HILCouncilSessionDTO,
  HILDecisionPageDTO,
  HILWorkflowInstanceDetailDTO,
  HILWorkflowInstancePageDTO,
  HILWorkflowInstanceStatusDTO,
  HILWorkflowTemplateDTO,
  HILWorkflowTemplatePageDTO,
  PublishHILWorkflowTemplateRequest,
  ResubmitHILWorkflowRequest,
  SaveHILWorkflowTemplateRequest,
  SubmitHILCouncilContributionRequest
} from '#lib/types/api'
import type { ApprovalCenterViewModel, ApprovalDecisionFormViewModel, HILWorkflowDecision, HILWorkflowView } from '#lib/types/view-models'

export type HILWorkflowTemplate = HILWorkflowTemplateDTO
export type SaveHILWorkflowTemplateInput = SaveHILWorkflowTemplateRequest
export type PublishHILWorkflowTemplateInput = PublishHILWorkflowTemplateRequest

export interface ApprovalFilters {
  missionId?: string
  status?: string
  limit?: number
  offset?: number
}

export interface HILDecisionFilters {
  mode?: 'hil' | 'approval' | 'council'
  status?: 'pending' | 'resolved' | 'denied' | ''
  mine?: boolean
  requestedByMe?: boolean
  limit?: number
  offset?: number
}

export interface HILWorkflowFilters {
  view?: Exclude<HILWorkflowView, 'templates'>
  status?: HILWorkflowInstanceStatusDTO | ''
  limit?: number
  offset?: number
}

export interface HILWorkflowTemplateFilters {
  status?: 'active' | 'disabled' | ''
}

export const approvalRefreshIntervalMs = 15_000

export const approvalQueryKeys = {
  all: ['approvals'] as const,
  list: (filters: ApprovalFilters = {}) => ['approvals', buildApprovalQuery(filters)] as const,
  hilRoot: ['approvals', 'hil'] as const,
  hilList: (filters: HILDecisionFilters = {}) => ['approvals', 'hil', buildHILDecisionQuery(filters)] as const,
  council: (councilId: string) => ['approvals', 'hil', 'council', councilId] as const,
  workflowRoot: ['approvals', 'workflow'] as const,
  workflowList: (filters: HILWorkflowFilters = {}) => ['approvals', 'workflow', 'list', buildHILWorkflowQuery(filters)] as const,
  workflowDetail: (instanceId: string) => ['approvals', 'workflow', 'detail', instanceId] as const,
  workflowTemplateRoot: ['approvals', 'workflow-templates'] as const,
  workflowTemplateList: (filters: HILWorkflowTemplateFilters = {}) => ['approvals', 'workflow-templates', 'list', buildHILWorkflowTemplateQuery(filters)] as const,
  workflowTemplate: (templateId: string) => ['approvals', 'workflow-templates', 'detail', templateId] as const
}

export function useHILWorkflowListQuery(
  filters: () => HILWorkflowFilters,
  viewerActorIds: () => string[] = () => [],
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: approvalQueryKeys.workflowList(current),
      queryFn: async () => {
        const page = await approvalApi.listHILWorkflowInstances(buildHILWorkflowQuery(current)) as HILWorkflowInstancePageDTO
        const actors = viewerActorIds()
        return { items: page.items.map((item) => mapHILWorkflowInstance(item, actors)), total: page.total }
      },
      enabled: enabled(),
      refetchInterval: enabled() ? approvalRefreshIntervalMs : false
    }
  })
}

export function useHILWorkflowDetailQuery(
  instanceId: () => string,
  viewerActorIds: () => string[] = () => [],
  enabled: () => boolean = () => true
) {
  return createQuery(() => ({
    queryKey: approvalQueryKeys.workflowDetail(instanceId()),
    queryFn: async () => mapHILWorkflowInstance(
      await approvalApi.getHILWorkflowInstance(instanceId()) as HILWorkflowInstanceDetailDTO,
      viewerActorIds()
    ),
    enabled: enabled() && instanceId().trim().length > 0,
    refetchInterval: enabled() && instanceId().trim().length > 0 ? approvalRefreshIntervalMs : false
  }))
}

export function useHILWorkflowViewedMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ instanceId }: { instanceId: string; revision: number }) =>
      approvalApi.recordHILWorkflowViewed(instanceId, { idempotencyKey: `workflow-viewed:${instanceId}` }),
    onSuccess: (_data, variables) => {
      client.invalidateQueries({ queryKey: approvalQueryKeys.workflowDetail(variables.instanceId) })
    }
  }))
}

export function useHILWorkflowTaskDecisionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ taskId, decision, reason, expectedVersion }: {
      instanceId: string
      taskId: string
      decision: HILWorkflowDecision
      reason: string
      expectedVersion: number
    }) => {
      const body: DecideHILWorkflowTaskRequest = {
        decision,
        reason: reason || undefined,
        expected_instance_version: expectedVersion
      }
      return approvalApi.decideHILWorkflowTask(taskId, body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: (_data, variables) => invalidateHILWorkflowQueries(client, variables.instanceId),
    onError: (error, variables) => {
      if (shouldRefreshHILWorkflowAfterError(error)) invalidateHILWorkflowQueries(client, variables.instanceId)
    }
  }))
}

export function useHILWorkflowTaskTransferMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ instanceId: _instanceId, taskId, newAssigneeId, reason, expectedVersion, administrator = false }: {
      instanceId: string
      taskId: string
      newAssigneeId: string
      reason: string
      expectedVersion: number
      administrator?: boolean
    }) => {
      const body = { new_assignee: { kind: 'human' as const, id: newAssigneeId }, reason, expected_instance_version: expectedVersion }
      return administrator
        ? approvalApi.reassignHILWorkflowTask(taskId, body, { idempotencyKey: createIdempotencyKey() })
        : approvalApi.transferHILWorkflowTask(taskId, body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: (_data, variables) => invalidateHILWorkflowQueries(client, variables.instanceId),
    onError: (error, variables) => {
      if (shouldRefreshHILWorkflowAfterError(error)) invalidateHILWorkflowQueries(client, variables.instanceId)
    }
  }))
}

export function useHILWorkflowResubmitMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ instanceId, body }: { instanceId: string; body: ResubmitHILWorkflowRequest }) =>
      approvalApi.resubmitHILWorkflow(instanceId, body, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: (_data, variables) => invalidateHILWorkflowQueries(client, variables.instanceId),
    onError: (error, variables) => {
      if (shouldRefreshHILWorkflowAfterError(error)) invalidateHILWorkflowQueries(client, variables.instanceId)
    }
  }))
}

export function useHILWorkflowTemplateListQuery(
  filters: () => HILWorkflowTemplateFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: approvalQueryKeys.workflowTemplateList(current),
      queryFn: () => approvalApi.listHILWorkflowTemplates(buildHILWorkflowTemplateQuery(current)) as Promise<HILWorkflowTemplatePageDTO>,
      enabled: enabled()
    }
  })
}

export function useHILWorkflowTemplateQuery(
  templateId: () => string,
  enabled: () => boolean = () => true
) {
  return createQuery(() => ({
    queryKey: approvalQueryKeys.workflowTemplate(templateId()),
    queryFn: () => approvalApi.getHILWorkflowTemplate(templateId()) as Promise<HILWorkflowTemplateDTO>,
    enabled: enabled() && templateId().trim().length > 0
  }))
}

export function useSaveHILWorkflowTemplateMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ templateId, body }: { templateId?: string; body: SaveHILWorkflowTemplateRequest }) =>
      templateId
        ? approvalApi.updateHILWorkflowTemplateDraft(templateId, body, { idempotencyKey: createIdempotencyKey() })
        : approvalApi.createHILWorkflowTemplate(body, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => client.invalidateQueries({ queryKey: approvalQueryKeys.workflowTemplateRoot })
  }))
}

export function usePublishHILWorkflowTemplateMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ templateId, body }: { templateId: string; body: PublishHILWorkflowTemplateRequest }) =>
      approvalApi.publishHILWorkflowTemplate(templateId, body, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => client.invalidateQueries({ queryKey: approvalQueryKeys.workflowTemplateRoot })
  }))
}

export function useDisableHILWorkflowTemplateMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ templateId, reason }: { templateId: string; reason: string }) =>
      approvalApi.disableHILWorkflowTemplate(templateId, { reason }, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => client.invalidateQueries({ queryKey: approvalQueryKeys.workflowTemplateRoot })
  }))
}

export function useApprovalListQuery(filters: () => ApprovalFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: approvalQueryKeys.list(current),
      queryFn: async () => mapApprovalCenterView((await approvalApi.listApprovals(buildApprovalQuery(current))) as ApprovalPageDTO),
      enabled: enabled()
    }
  })
}

export function useHILDecisionListQuery(filters: () => HILDecisionFilters, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: approvalQueryKeys.hilList(current),
      queryFn: async () => mapHILDecisionPage((await approvalApi.listHILDecisions(buildHILDecisionQuery(current))) as HILDecisionPageDTO),
      enabled: enabled(),
      refetchInterval: enabled() ? approvalRefreshIntervalMs : false
    }
  })
}

export function useHILCouncilQuery(councilId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: approvalQueryKeys.council(councilId()),
    queryFn: async () => mapHILCouncil((await approvalApi.getHILCouncil(councilId())) as HILCouncilSessionDTO),
    enabled: enabled() && councilId().length > 0
  }))
}

export function useHILContributionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ councilId, body }: { councilId: string; body: SubmitHILCouncilContributionRequest }) =>
      approvalApi.submitHILContribution(councilId, body, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: (_data, variables) => {
      client.invalidateQueries({ queryKey: approvalQueryKeys.council(variables.councilId) })
      client.invalidateQueries({ queryKey: approvalQueryKeys.hilRoot })
      client.invalidateQueries({ queryKey: ['notification-badges'] })
    }
  }))
}

export function useHILVoteMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ councilId, body }: { councilId: string; body: CastHILCouncilVoteRequest }) =>
      approvalApi.castHILVote(councilId, body, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: (_data, variables) => {
      client.invalidateQueries({ queryKey: approvalQueryKeys.council(variables.councilId) })
      client.invalidateQueries({ queryKey: approvalQueryKeys.hilRoot })
      client.invalidateQueries({ queryKey: ['notification-badges'] })
    }
  }))
}

/** Query the new HIL service and expose its decisions to the shared approval queue. */
export function useHILApprovalListQuery(
  mode: () => 'hil' | 'approval',
  tab: () => 'pending' | 'history',
  enabled: () => boolean = () => true,
  mine: () => boolean = () => true
) {
  return createQuery(() => {
    const currentMode = mode()
    const status = tab() === 'pending' ? 'pending' : ''
    const filters = {
      mode: currentMode,
      status,
      mine: mine(),
      limit: 100
    } satisfies HILDecisionFilters
    return {
      queryKey: approvalQueryKeys.hilList(filters),
      queryFn: async (): Promise<ApprovalCenterViewModel> => {
        const page = collapseRepeatedPendingRuntimeApprovals(
          mapHILDecisionPage((await approvalApi.listHILDecisions(buildHILDecisionQuery(filters))) as HILDecisionPageDTO)
        )
        const items = page.map((item) => mapHILDecisionToApprovalItem(item, currentMode))
        return {
          summary: {
            total: items.length,
            pending: items.filter((item) => item.status === 'pending').length,
            decided: items.filter((item) => item.status !== 'pending').length,
            highPriority: items.filter((item) => ['high', 'critical'].includes(item.priority)).length
          },
          items
        }
      },
      enabled: enabled(),
      refetchInterval: enabled() ? approvalRefreshIntervalMs : false
    }
  })
}

export function useHILDecisionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (form: ApprovalDecisionFormViewModel) => {
      const command = mapHILDecisionRequest(form)
      return approvalApi.decideHILDecision(command.decisionId, command.body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: approvalQueryKeys.hilRoot })
      client.invalidateQueries({ queryKey: approvalQueryKeys.all })
      client.invalidateQueries({ queryKey: ['notification-badges'] })
      client.invalidateQueries({ queryKey: operationsQueryKeys.auditRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.permissionRoot })
    }
  }))
}

export function useApprovalDecisionMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: ApprovalDecisionFormViewModel) => {
      const command = mapApprovalDecisionRequest(form)
      const options = { idempotencyKey: createIdempotencyKey() }
      return command.actionPath
        ? approvalApi.decideApproval(command.approvalId, command.body, options, command.actionPath)
        : approvalApi.decideApproval(command.approvalId, command.body, options)
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: approvalQueryKeys.all })
      client.invalidateQueries({ queryKey: ['notification-badges'] })
      client.invalidateQueries({ queryKey: operationsQueryKeys.auditRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.permissionRoot })
    }
  }))
}

export function buildApprovalQuery(filters: ApprovalFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'mission_id', filters.missionId)
  appendParam(params, 'status', filters.status)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildHILDecisionQuery(filters: HILDecisionFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'mode', filters.mode)
  appendParam(params, 'status', filters.status)
  appendParam(params, 'mine', filters.mine)
  appendParam(params, 'requested_by_me', filters.requestedByMe)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildHILWorkflowQuery(filters: HILWorkflowFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'view', filters.view)
  appendParam(params, 'status', filters.status)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildHILWorkflowTemplateQuery(filters: HILWorkflowTemplateFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'status', filters.status)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function shouldRefreshHILWorkflowAfterError(error: unknown) {
  return error instanceof ApiClientError && error.status === 409
}

async function invalidateHILWorkflowQueries(client: ReturnType<typeof useQueryClient>, instanceId: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: approvalQueryKeys.workflowRoot }),
    client.invalidateQueries({ queryKey: approvalQueryKeys.workflowDetail(instanceId) }),
    client.invalidateQueries({ queryKey: approvalQueryKeys.hilRoot }),
    client.invalidateQueries({ queryKey: approvalQueryKeys.all }),
    client.invalidateQueries({ queryKey: ['notification-badges'] }),
    client.invalidateQueries({ queryKey: operationsQueryKeys.auditRoot })
  ])
}

function appendParam(params: URLSearchParams, key: string, value: string | number | boolean | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}
