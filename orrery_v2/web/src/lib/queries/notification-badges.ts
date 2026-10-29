import { createQuery } from '@tanstack/svelte-query'
import { approvalApi } from '#lib/api/approvals'
import { discussionApi } from '#lib/api/discussions'
import { mapApprovalCenterView } from '#lib/mappers/approval'
import { mapDiscussionListView } from '#lib/mappers/discussions'
import { buildApprovalQuery, buildHILDecisionQuery } from './approvals'
import { collapseRepeatedPendingRuntimeApprovals, mapHILDecisionPage, mapHILDecisionToApprovalItem } from '#lib/mappers/hil'
import type { ApprovalPageDTO, HILDecisionPageDTO } from '#lib/types/api'
import type { ApprovalItemViewModel, HILDecisionItemViewModel } from '#lib/types/view-models'
import type { DiscussionPageDTO, DiscussionSummaryViewModel } from '#lib/types/discussions'

export interface NotificationDiscussionItem extends DiscussionSummaryViewModel {
  updatedAtRaw: string
}

export interface NotificationBadgeCounts {
  discussions: number
  approvals: number
  hilApprovals: number
  councils: number
  discussionItems: NotificationDiscussionItem[]
  approvalItems: ApprovalItemViewModel[]
  councilItems: HILDecisionItemViewModel[]
  allApprovalItems: ApprovalItemViewModel[]
  allCouncilItems: HILDecisionItemViewModel[]
}

export interface NotificationBadgeAccess {
  discussions: boolean
  approvals: boolean
}

export type NotificationApprovalBadgeCounts = Pick<
  NotificationBadgeCounts,
  'approvals' | 'hilApprovals' | 'councils' | 'approvalItems' | 'councilItems' | 'allApprovalItems' | 'allCouncilItems'
>

export type NotificationDiscussionBadgeCounts = Pick<
  NotificationBadgeCounts,
  'discussions' | 'discussionItems'
>

export const notificationBadgeQueryKeys = {
  all: ['notification-badges'] as const,
  approvals: ['notification-badges', 'approvals'] as const,
  discussions: ['notification-badges', 'discussions'] as const
}

export function useNotificationApprovalBadgeCountsQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: notificationBadgeQueryKeys.approvals,
    enabled: enabled(),
    queryFn: ({ signal }) => loadNotificationApprovalBadgeCounts(signal),
    staleTime: 30_000,
    refetchInterval: 60_000
  }))
}

export function useNotificationDiscussionBadgeCountsQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: notificationBadgeQueryKeys.discussions,
    enabled: enabled(),
    queryFn: ({ signal }) => loadNotificationDiscussionBadgeCounts(signal),
    staleTime: 30_000,
    refetchInterval: 60_000
  }))
}

export async function loadNotificationBadgeCounts(
  current: NotificationBadgeAccess,
  signal?: AbortSignal
): Promise<NotificationBadgeCounts> {
  const [discussionBadges, approvalBadges] = await Promise.all([
    current.discussions
      ? loadNotificationDiscussionBadgeCounts(signal)
      : Promise.resolve<NotificationDiscussionBadgeCounts>({ discussions: 0, discussionItems: [] }),
    current.approvals
        ? loadNotificationApprovalBadgeCounts(signal)
        : Promise.resolve<NotificationApprovalBadgeCounts>({
          approvals: 0,
          hilApprovals: 0,
          councils: 0,
          approvalItems: [],
          councilItems: [],
          allApprovalItems: [],
          allCouncilItems: []
        })
  ])
  return { ...discussionBadges, ...approvalBadges }
}

export async function loadNotificationDiscussionBadgeCounts(
  signal?: AbortSignal
): Promise<NotificationDiscussionBadgeCounts> {
  const discussionPage = (await discussionApi.listDiscussions({}, { signal })) as DiscussionPageDTO
  const discussionView = discussionPage ? mapDiscussionListView(discussionPage) : undefined
  const discussionUpdatedAt = new Map(
    (discussionPage?.discussions ?? []).map((discussion) => [discussion.discussion_id, discussion.updated_at ?? ''])
  )
  const discussionItems = (discussionView?.discussions ?? [])
    .filter((discussion) => discussion.unreadCompleted)
    .map((discussion) => ({
      ...discussion,
      updatedAtRaw: discussionUpdatedAt.get(discussion.id) ?? ''
    }))

  return {
    discussions: discussionItems.length,
    discussionItems: discussionItems.slice(0, 3)
  }
}

export async function loadNotificationApprovalBadgeCounts(
  signal?: AbortSignal,
  complete = false
): Promise<NotificationApprovalBadgeCounts> {
  const requestOptions = { signal }
  let [approvalPage, hilPage, hilApprovalPage, councilPage] = await Promise.all([
    approvalApi.listApprovals(buildApprovalQuery({ status: 'pending' }), requestOptions) as Promise<ApprovalPageDTO>,
    approvalApi.listHILDecisions(buildHILDecisionQuery({ mode: 'hil', status: 'pending', mine: true, limit: 100 }), requestOptions) as Promise<HILDecisionPageDTO>,
    approvalApi.listHILDecisions(buildHILDecisionQuery({ mode: 'approval', status: 'pending', mine: true, limit: 100 }), requestOptions) as Promise<HILDecisionPageDTO>,
    approvalApi.listHILDecisions(buildHILDecisionQuery({ mode: 'council', status: 'pending', mine: true, limit: 100 }), requestOptions) as Promise<HILDecisionPageDTO>
  ])
  if (complete) {
    async function remainingHIL(page: HILDecisionPageDTO, mode: 'hil' | 'approval' | 'council') {
      const items = [...(page?.items ?? [])]
      while (items.length < (page?.total ?? 0)) {
        const next = await approvalApi.listHILDecisions(buildHILDecisionQuery({ mode, status: 'pending', mine: true, limit: 100, offset: items.length }), requestOptions) as HILDecisionPageDTO
        if (!next.items?.length || items.length > 10_000) throw new Error('Incomplete approval inbox response')
        items.push(...next.items)
      }
      return { ...page, items }
    }
    const businessItems = [...(approvalPage?.items ?? [])]
    while (businessItems.length < (approvalPage?.page?.total ?? 0)) {
      const next = await approvalApi.listApprovals(buildApprovalQuery({ status: 'pending', limit: 100, offset: businessItems.length }), requestOptions) as ApprovalPageDTO
      if (!next.items?.length || businessItems.length > 10_000) throw new Error('Incomplete approval inbox response')
      businessItems.push(...next.items)
    }
    approvalPage = { ...approvalPage, items: businessItems }
    ;[hilPage, hilApprovalPage, councilPage] = await Promise.all([
      remainingHIL(hilPage, 'hil'), remainingHIL(hilApprovalPage, 'approval'), remainingHIL(councilPage, 'council')
    ])
  }
  const approvalView = mapApprovalCenterView(approvalPage)
  const councilItems = mapHILDecisionPage(councilPage)
    .filter((decision) => decision.status === 'pending' && decision.routeMode === 'council')
  // The full inbox must keep every independent decision, even when materials match.
  const approvalItems = complete
    ? [
        ...[hilPage, hilApprovalPage].flatMap(page => mapHILDecisionPage(page))
          .filter(item => item.status === 'pending' && (item.routeMode === 'hil' || item.routeMode === 'approval'))
          .map(item => mapHILDecisionToApprovalItem(item, item.routeMode === 'hil' ? 'hil' : 'approval')),
        ...mergeNotificationApprovalItems(approvalView?.items ?? [])
      ]
    : mergeNotificationApprovalItems(approvalView?.items ?? [], hilPage, hilApprovalPage)
  const hilApprovals = approvalItems.filter((item) => item.sourceLabel === 'hil:hil').length

  return {
    approvals: approvalItems.length,
    hilApprovals,
    councils: councilItems.length,
    approvalItems: approvalItems.slice(0, 3),
    councilItems: councilItems.slice(0, 3),
    allApprovalItems: approvalItems,
    allCouncilItems: councilItems
  }
}

export function mergeNotificationApprovalItems(
  businessApprovals: ApprovalItemViewModel[],
  ...hilApprovalPages: Array<HILDecisionPageDTO | undefined>
) {
  const fixedHILApprovals = collapseRepeatedPendingRuntimeApprovals(
    hilApprovalPages.flatMap((page) => page ? mapHILDecisionPage(page) : [])
  )
    .filter((decision) => decision.status === 'pending' && (decision.routeMode === 'hil' || decision.routeMode === 'approval'))
    .map((decision) => mapHILDecisionToApprovalItem(decision, decision.routeMode === 'hil' ? 'hil' : 'approval'))
  const legacyBusinessApprovals = businessApprovals.filter((approval) =>
    isPendingApproval(approval.status) && !isHILBackedLegacyApproval(approval)
  )
  return [...fixedHILApprovals, ...legacyBusinessApprovals]
}

function isPendingApproval(status: string) {
  const normalized = status.trim().toLowerCase()
  return normalized === '' || normalized === 'pending' || normalized === 'open'
}

function isHILBackedLegacyApproval(item: ApprovalItemViewModel) {
  return item.payloadPreview.fields.some((field) => field.key === 'hil_decision_id' && field.value.trim().length > 0)
}
