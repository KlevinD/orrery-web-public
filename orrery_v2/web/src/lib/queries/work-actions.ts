import { createQuery } from '@tanstack/svelte-query'
import { approvalApi } from '#lib/api/approvals'
import { todoApi } from '#lib/api/todos'
import { schedulerApi } from '#lib/api/scheduler'
import { mapApprovalCenterView } from '#lib/mappers/approval'
import { mapHILDecisionPage, mapHILDecisionToApprovalItem } from '#lib/mappers/hil'
import { mapHILWorkflowInstance } from '#lib/mappers/workflow'
import { buildApprovalQuery, buildHILDecisionQuery, buildHILWorkflowQuery } from './approvals'
import { loadAllPages, type WorkInboxAccess } from './work-inbox'
import { currentSessionUserIdentityIds } from '#lib/stores/session.svelte'
import { actorMatches } from '#lib/components/workhub/work-actions'
import type { ApprovalPageDTO, HILDecisionPageDTO, HILWorkflowInstancePageDTO } from '#lib/types/api'
import type { WorkActionView } from '#lib/components/workhub/work-actions'

export function useWorkActionSources(access: () => WorkInboxAccess, view: () => WorkActionView = () => 'pending') {
  const status = () => view() === 'pending' ? 'pending' as const : '' as const
  const options = { staleTime: 15_000, refetchInterval: 30_000, refetchIntervalInBackground: false }
  const todos = createQuery(() => { const queryView = view(); return ({
    ...options, queryKey: ['todos', 'work-actions', access().identity, queryView], enabled: access().todos,
    queryFn: ({ signal }) => loadAllPages(async offset => {
      const params = new URLSearchParams({ mine: String(queryView !== 'requested'), include_completed: String(queryView !== 'pending'), include_pending_delivery: 'true', limit: '100', offset: String(offset) })
      const page = await todoApi.listTodos(`?${params}`, { signal })
      return { items: page.todos ?? [], total: page.total }
    })
  }) })
  const business = createQuery(() => { const queryStatus = status(), queryView = view(), actors = currentSessionUserIdentityIds(); return ({
    ...options, queryKey: ['approvals', 'work-actions', 'business', access().identity, queryView], enabled: access().approvals,
    queryFn: async ({ signal }) => {
      const items = await loadAllPages(async offset => {
        const page = await approvalApi.listApprovals(buildApprovalQuery({ status: queryStatus, limit: 100, offset }), { signal }) as ApprovalPageDTO
        return { items: page.items ?? [], total: page.page?.total ?? 0 }
      })
      return mapApprovalCenterView({ items, page: { total: items.length, returned: items.length } }).items
        .filter(item => queryView === 'requested' || !item.assignedTo || item.assignedTo === 'Not provided' || actorMatches(item.assignedTo, actors))
    }
  }) })
  function hil(mode: 'hil' | 'approval' | 'council') {
    return createQuery(() => { const queryView = view(), queryStatus = status(); return ({
      ...options, queryKey: ['approvals', 'work-actions', mode, access().identity, queryView], enabled: access().approvals,
      queryFn: async ({ signal }) => {
        const items = await loadAllPages(async offset => {
          const page = await approvalApi.listHILDecisions(buildHILDecisionQuery({ mode, status: queryStatus, mine: queryView !== 'requested', requestedByMe: queryView === 'requested' ? true : undefined, limit: 100, offset }), { signal }) as HILDecisionPageDTO
          return { items: page.items ?? [], total: page.total ?? 0 }
        })
        const mapped = mapHILDecisionPage({ items, total: items.length })
        if (mode === 'council' && queryView === 'history') {
          const pending = await loadAllPages(async offset => {
            const page = await approvalApi.listHILDecisions(buildHILDecisionQuery({ mode, status: 'pending', mine: true, limit: 100, offset }), { signal }) as HILDecisionPageDTO
            return { items: page.items ?? [], total: page.total ?? 0 }
          })
          const pendingIDs = new Set(pending.map(item => item.id))
          return mapped.map(item => ({ ...item, handled: !pendingIDs.has(item.id) }))
        }
        return mapped
      }
    }) })
  }
  const manual = hil('hil'), fixed = hil('approval'), councils = hil('council')
  const results = createQuery(() => ({
    ...options, queryKey: ['scheduler', 'work-actions', access().identity], enabled: access().scheduler && view() === 'pending',
    queryFn: ({ signal }) => loadAllPages(async offset => {
      const page = await schedulerApi.listRunInbox(`?group_by_task=true&limit=100&offset=${offset}`, { signal })
      return { items: page.items ?? [], total: page.total }
    })
  }))
  return { todos, business, manual, fixed, councils, results }
}

export function workActionApprovals(sources: ReturnType<typeof useWorkActionSources>) {
  return [
    ...(sources.manual.data ?? []).filter(item => item.routeMode === 'hil').map(item => mapHILDecisionToApprovalItem(item, 'hil')),
    ...(sources.fixed.data ?? []).filter(item => item.routeMode === 'approval').map(item => mapHILDecisionToApprovalItem(item, 'approval')),
    ...(sources.business.data ?? [])
  ]
}

export function useWorkActionWorkflows(identity: () => string, actors: () => string[], enabled: () => boolean, view: () => 'all' | 'started_by_me' = () => 'all', decisionId: () => string = () => '') {
  return createQuery(() => { const queryView = view(), queryDecision = decisionId(), queryActors = [...actors()]; return ({
    queryKey: ['approvals', 'workflow', 'work-actions', identity(), queryView, queryDecision], enabled: enabled(), staleTime: 15_000,
    refetchInterval: enabled() ? 30_000 : false,
    queryFn: async ({ signal }) => {
      const items = await loadAllPages(async offset => {
        const query = buildHILWorkflowQuery({ view: queryView, limit: 100, offset }) + (queryDecision ? `&decision_id=${encodeURIComponent(queryDecision)}` : '')
        const page = await approvalApi.listHILWorkflowInstances(query, { signal }) as HILWorkflowInstancePageDTO
        return { items: page.items ?? [], total: page.total }
      })
      return items.map(item => {
        const workflow = mapHILWorkflowInstance(item, queryActors)
        const decision = mapHILDecisionPage({ items: [item.decision], total: 1 })[0]
        return { ...workflow, workApproval: { ...mapHILDecisionToApprovalItem(decision, decision.routeMode === 'hil' ? 'hil' : 'approval'), title: workflow.title, summary: workflow.summary, requestedBy: workflow.requester } }
      })
    }
  }) })
}
