<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { createQuery } from '@tanstack/svelte-query'
  import { ArrowLeft, CheckCircle2, Clock, RefreshCw } from 'lucide-svelte'
  import { session, currentSessionUserIdentityIds } from '#lib/stores/session.svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { locale } from '#lib/i18n'
  import { useWorkActionSources, useWorkActionWorkflows, workActionApprovals } from '#lib/queries/work-actions'
  import { approvalApi } from '#lib/api/approvals'
  import { todoApi } from '#lib/api/todos'
  import { mapApprovalCenterView } from '#lib/mappers/approval'
  import { loadAllPages } from '#lib/queries/work-inbox'
  import { apiFetch, ApiClientError } from '#lib/api/client'
  import { mapHILDecisionPage, mapHILDecisionToApprovalItem } from '#lib/mappers/hil'
  import { approvalStatusDisplayLabel, approvalTitleLabel } from '#lib/components/approvals/approvalDisplayLabels'
  import IssueDetailPage from '#lib/components/issues/IssueDetailPage.svelte'
  import WorkApprovalDetail from './WorkApprovalDetail.svelte'
  import { buildWorkActions, filterWorkActions, mergeWorkActionWorkflows, workActionHref, type WorkActionItem, type WorkActionView, type WorkActionKind } from './work-actions'
  import type { ApprovalPageDTO, HILDecisionPageDTO, HILWorkflowInstanceDetailDTO, HILCouncilSessionDTO } from '#lib/types/api'

  let { view = 'pending', compact = false }: { view?: WorkActionView; compact?: boolean } = $props()
  const L = (zh: string, en: string) => $locale.startsWith('zh') ? zh : en
  const access = $derived({ todos: hasPermissionScope(session.permissionScope, 'todo.read'), approvals: hasPermissionScope(session.permissionScope, 'approvals.read'),
    scheduler: hasPermissionScope(session.permissionScope, 'scheduler.read'), identity: `${session.user.userId}:${session.viewerKind}:${session.securityContextVersion}` })
  const actors = $derived(currentSessionUserIdentityIds())
  const sources = useWorkActionSources(() => access, () => view)
  const workflows = useWorkActionWorkflows(() => access.identity, () => actors, () => access.approvals && view !== 'pending', () => view === 'requested' ? 'started_by_me' : 'all')
  const enabled = $derived([...(access.todos ? [sources.todos] : []), ...(access.approvals ? [sources.business, sources.manual, sources.fixed, sources.councils, ...(view !== 'pending' ? [workflows] : [])] : []), ...(access.scheduler && view === 'pending' ? [sources.results] : [])])
  const loading = $derived(enabled.some(source => source.isPending))
  const failed = $derived(enabled.some(source => source.isError))
  const rows = $derived(mergeWorkActionWorkflows(buildWorkActions(access.todos ? sources.todos.data ?? [] : [], access.approvals ? workActionApprovals(sources) : [], access.approvals ? sources.councils.data ?? [] : [], access.scheduler && view === 'pending' ? sources.results.data ?? [] : []), access.approvals && view !== 'pending' ? workflows.data ?? [] : [], view, actors))
  const scoped = $derived(filterWorkActions(rows, view, actors))
  const kind = $derived(($page.url.searchParams.get('kind') || 'all') as WorkActionKind | 'all')
  let search = $state(''), owner = $state(''), sourceFilter = $state('all'), days = $state('all'), pageIndex = $state(0)
  let selectedID = $state(''), retained = $state<WorkActionItem | undefined>(), identityAtSelection = $state('')
  const routeAction = $derived($page.url.searchParams.get('action') || '')
  const workflowID = $derived($page.url.searchParams.get('workflow_id') || '')
  const missing = $derived(!compact && !loading && (Boolean(routeAction) && !rows.some(item => item.id === routeAction) || Boolean(workflowID) && !routeAction))
  const fallback = createQuery(() => ({
    queryKey: ['approvals', 'work-action-target', access.identity, routeAction, workflowID], enabled: missing,
    queryFn: async ({ signal }): Promise<WorkActionItem | undefined> => {
      if (workflowID && access.approvals && !routeAction) {
        const detail = await approvalApi.getHILWorkflowInstance(workflowID, { signal }) as HILWorkflowInstanceDetailDTO
        const decision = mapHILDecisionPage({ items: [detail.decision], total: 1 })[0]
        return buildWorkActions([], [mapHILDecisionToApprovalItem(decision, decision.routeMode === 'hil' ? 'hil' : 'approval')], [])[0]
      }
      if (routeAction.startsWith('todo:') && access.todos) return buildWorkActions([await todoApi.getTodo(routeAction.slice(5), { signal })], [], [])[0]
      if (routeAction.startsWith('approval:') && access.approvals) {
        try {
          const decision = await apiFetch<HILDecisionPageDTO['items'][number]>(`/api/hil/decisions/${encodeURIComponent(routeAction.slice(9))}`, { signal })
          const mapped = mapHILDecisionPage({ items: [decision], total: 1 })[0]
          return buildWorkActions([], [mapHILDecisionToApprovalItem(mapped, mapped.routeMode === 'hil' ? 'hil' : 'approval')], [])[0]
        } catch (error) {
          if (!(error instanceof ApiClientError) || error.status !== 404) throw error
          const items = await loadAllPages(async offset => {
            const page = await approvalApi.listApprovals(`?limit=100&offset=${offset}`, { signal }) as ApprovalPageDTO
            return { items: page.items ?? [], total: page.page?.total ?? 0 }
          })
          const approvals = mapApprovalCenterView({ items, page: { total: items.length, returned: items.length } }).items
          return buildWorkActions([], approvals, []).find(item => item.id === routeAction)
        }
      }
      if (routeAction.startsWith('council:') && access.approvals) {
        const council = await approvalApi.getHILCouncil(routeAction.slice(8), { signal }) as HILCouncilSessionDTO
        const decision = await apiFetch<HILDecisionPageDTO['items'][number]>(`/api/hil/decisions/${encodeURIComponent(council.request_id)}`, { signal })
        return buildWorkActions([], [], mapHILDecisionPage({ items: [decision], total: 1 }))[0]
      }
      return undefined
    }
  }))
  const filtered = $derived(scoped.filter(item => (kind === 'all' || item.kind === kind)
    && `${item.title} ${item.summary}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase())
    && (!owner || item.assignee.toLocaleLowerCase().includes(owner.trim().toLocaleLowerCase()))
    && (sourceFilter === 'all' || item.source === sourceFilter)
    && (days === 'all' || Date.parse(item.date) >= Date.now() - Number(days) * 86400000)))
  const size = $derived(compact ? 8 : 25)
  const visible = $derived(filtered.slice(pageIndex * size, (pageIndex + 1) * size))
  const selected = $derived(rows.find(item => item.id === selectedID) ?? (fallback.data?.id === selectedID || !selectedID && workflowID ? fallback.data : undefined) ?? (retained?.id === selectedID && identityAtSelection === access.identity ? retained : undefined))
  $effect(() => { view; kind; search; owner; sourceFilter; days; pageIndex = 0 })
  $effect(() => { if (pageIndex > 0 && pageIndex * size >= filtered.length) pageIndex = 0 })
  $effect(() => {
    if (compact) return
    if (identityAtSelection !== access.identity) { retained = undefined; identityAtSelection = access.identity }
    selectedID = routeAction || fallback.data?.id || ''
    const current = rows.find(item => item.id === selectedID) || fallback.data
    if (current && current.id === selectedID) retained = current
  })
  function refresh() { for (const source of enabled) void source.refetch(); if (missing) void fallback.refetch() }
  function label(type: WorkActionKind) {
    return ({ approval: L('审批', 'Approval'), reply: L('答复', 'Reply'), todo: L('办理', 'Task'), council: L('会审', 'Council'), result: L('运行异常', 'Run issue') })[type]
  }
  function statusLabel(item: WorkActionItem) {
    if (item.status === 'handled') return L('我已处理，流程进行中', 'Responded · workflow continuing')
    if (item.status === 'delivery_pending') return L('已办理，反馈待送达', 'Completed · feedback delivery pending')
    if (item.approval) return approvalStatusDisplayLabel(item.status, $locale)
    if (item.pending) return L('待处理', 'Pending')
    return item.status === 'canceled' ? L('已取消', 'Canceled') : L('已处理', 'Processed')
  }
  async function select(item: WorkActionItem) {
    retained = item; identityAtSelection = access.identity
    const params = new URLSearchParams($page.url.search)
    params.set('view', view === 'pending' ? 'inbox' : view); params.set('action', item.id); params.delete('workflow_id')
    await goto(`/work-items?${params}`, { reset: false })
  }
  function close() { const params = new URLSearchParams($page.url.search); params.delete('action'); params.delete('workflow_id'); retained = undefined; void goto(`/work-items?${params}`, { reset: false }) }
  function filterKind(value: string) { const params = new URLSearchParams($page.url.search); if (value === 'all') params.delete('kind'); else params.set('kind', value); void goto(`/work-items?${params}`, { reset: false }) }
  function date(value: string) { const parsed = new Date(value); return Number.isNaN(parsed.valueOf()) ? '' : parsed.toLocaleString($locale, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) }
</script>

<section class="@container min-w-0 space-y-3" data-work-actions={compact ? 'sidebar' : 'main'}>
  <div class="flex flex-wrap items-center justify-between gap-2">
    <span class="text-xs text-muted-foreground" data-work-action-count>{L('共 ', 'Total: ')}{scoped.length}{loading || failed ? '+' : ''}</span>
    <button class="inline-flex items-center gap-1 rounded px-2 py-1 text-xs hover:bg-muted" onclick={refresh}><RefreshCw class="h-3.5 w-3.5" />{L('刷新', 'Refresh')}</button>
  </div>
  {#if !compact}
    <div class="flex flex-wrap gap-2">
      <input class="h-9 min-w-32 flex-1 rounded-md border border-border bg-card px-3 text-sm" aria-label={L('搜索事项', 'Search work')} placeholder={L('搜索事项', 'Search work')} bind:value={search} />
      <select class="h-9 rounded-md border border-border bg-card px-2 text-sm" aria-label={L('事项类型', 'Work type')} value={kind} onchange={event => filterKind(event.currentTarget.value)}>
        <option value="all">{L('全部类型', 'All types')}</option>{#each (['approval', 'reply', 'todo', 'council', 'result'] as const) as value}<option value={value}>{label(value)}</option>{/each}
      </select>
      <details class="rounded-md border border-border bg-card px-3 py-2 text-sm"><summary class="cursor-pointer">{L('更多筛选', 'More filters')}</summary>
        <div class="mt-3 flex flex-wrap gap-2">
          <input class="h-9 min-w-0 rounded border border-border bg-card px-2" aria-label={L('处理人', 'Assignee')} placeholder={L('处理人', 'Assignee')} bind:value={owner} />
          <select class="h-9 rounded border border-border bg-card px-2" aria-label={L('来源', 'Source')} bind:value={sourceFilter}><option value="all">{L('全部来源', 'All sources')}</option><option value="agent">Agent</option><option value="business">{L('业务送审', 'Business')}</option><option value="todo">{L('待办', 'Todo')}</option><option value="scheduler">{L('定时任务', 'Scheduled task')}</option></select>
          <select class="h-9 rounded border border-border bg-card px-2" aria-label={L('时间范围', 'Time range')} bind:value={days}><option value="all">{L('全部时间', 'All time')}</option><option value="7">{L('最近七天', 'Last 7 days')}</option><option value="30">{L('最近三十天', 'Last 30 days')}</option></select>
        </div>
      </details>
    </div>
  {/if}
  {#if failed}<p role="alert" class="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm">{L('部分事项暂时无法加载，已加载的事项仍可处理，数量尚不完整。', 'Some work could not be loaded. Available items can still be processed; the count is incomplete.')}</p>{/if}
  {#if loading}<p role="status" class="text-xs text-muted-foreground">{L('正在汇总事项…', 'Loading work…')}</p>{/if}
  <div class={compact ? 'min-w-0' : 'grid min-w-0 items-start gap-4 @[900px]:grid-cols-[minmax(250px,.75fr)_minmax(0,1.5fr)]'}>
    <div class="min-w-0 space-y-2 {!compact && selected ? 'hidden @[900px]:block' : ''}" data-work-action-list>
      {#each visible as item (item.id)}
        {#snippet content()}
          <span class="flex min-w-0 items-start gap-2">{#if item.pending}<Clock class="mt-0.5 h-4 w-4 shrink-0 text-amber-700" />{:else}<CheckCircle2 class="mt-0.5 h-4 w-4 shrink-0 text-emerald-700" />{/if}<span class="min-w-0 flex-1 break-words text-sm font-medium">{item.approval ? approvalTitleLabel(item.title, $locale) : item.title}</span></span>
          <span class="mt-2 line-clamp-2 block break-words text-xs leading-5 text-muted-foreground">{item.summary}</span>
          <span class="mt-2 flex flex-wrap gap-x-2 gap-y-1 text-[11px] text-muted-foreground"><span>{label(item.kind)}</span><span>{statusLabel(item)}</span><time>{date(item.date)}</time>{#if item.assignee && item.assignee !== 'Not provided'}<span>{item.assignee}</span>{/if}{#if item.deadline}<span>{L('截止', 'Due')} {date(item.deadline)}</span>{/if}</span>
        {/snippet}
        {#if compact}<a href={workActionHref(item.id, view)} class="block rounded-md border border-border p-3 hover:bg-muted/30" data-work-item={item.id}>{@render content()}</a>
        {:else}<button class="w-full rounded-md border p-3 text-left {selected?.id === item.id ? 'border-brand bg-brand/5' : 'border-border bg-card hover:bg-muted/30'}" aria-pressed={selected?.id === item.id} onclick={() => select(item)} data-work-item={item.id}>{@render content()}</button>{/if}
      {:else}{#if !loading}<p class="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">{L('暂无匹配的事项', 'No matching work')}</p>{/if}{/each}
      {#if filtered.length > size}<div class="flex justify-between py-2 text-xs"><button disabled={!pageIndex} onclick={() => pageIndex--}>{L('上一页', 'Previous')}</button><span>{pageIndex + 1} / {Math.ceil(filtered.length / size)}</span><button disabled={(pageIndex + 1) * size >= filtered.length} onclick={() => pageIndex++}>{L('下一页', 'Next')}</button></div>{/if}
    </div>
    {#if !compact}<div class="min-w-0" data-work-action-detail>
      {#if selected}
        <button class="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground" onclick={close}><ArrowLeft class="h-4 w-4" />{L('返回事项列表', 'Back to work list')}</button>
        {#key `${access.identity}:${selected.id}`}
          {#if selected.todo}<IssueDetailPage kind="todo" issueId={selected.todo.todo_id} embedded />
          {:else if selected.approval || selected.council}<WorkApprovalDetail item={selected} workflowId={workflowID || selected.workflowId || ''} onprocessed={refresh} />
          {:else if selected.result}<article class="space-y-4 rounded-md border border-border p-4"><h2 class="break-words text-lg font-semibold">{selected.title}</h2><p class="whitespace-pre-wrap break-words text-sm">{selected.summary}</p><a class="text-sm text-brand underline" href={`/scheduled-tasks?view=results&task=${encodeURIComponent(selected.result.run.scheduled_task_id)}&run=${encodeURIComponent(selected.result.run.scheduled_run_id)}`}>{L('查看运行并处理', 'Open run details')}</a></article>{/if}
        {/key}
      {:else if missing && fallback.isPending}<p role="status" class="p-6 text-sm">{L('正在加载这项事务…', 'Loading this item…')}</p>
      {:else if routeAction || workflowID}<p role="alert" class="rounded-md border border-amber-500/30 p-4 text-sm">{L('这项事务暂时无法加载，或当前账号无权查看。', 'This item could not be loaded, or you do not have access.')} <button class="underline" onclick={refresh}>{L('重试', 'Retry')}</button></p>
      {:else}<p class="hidden rounded-md border border-dashed border-border p-8 text-center text-sm text-muted-foreground @[900px]:block">{L('选择一项事务，在这里查看材料并处理。', 'Select an item to review its material and respond here.')}</p>{/if}
    </div>{/if}
  </div>
</section>
