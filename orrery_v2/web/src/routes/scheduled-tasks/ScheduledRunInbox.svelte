<script lang="ts">
  import {
    AlertTriangle,
    ArrowLeft,
    CheckCircle2,
    ChevronLeft,
    ChevronRight,
    FileText,
    Inbox,
    Mail,
    MailOpen,
    Network,
    PackageOpen,
    RefreshCw
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import type { ApiClientError } from '#lib/api/client'
  import { locale } from '#lib/i18n'
  import { renderSafeMarkdown } from '#lib/markdown'
  import {
    useScheduledRunInboxQuery,
    useScheduledTaskQuery,
    useSchedulerCommandMutations
  } from '#lib/queries/scheduler'
  import type {
    RunStatus,
    ScheduledRunDeliveryStatus,
    ScheduledRunInboxItemDTO
  } from '#lib/types/scheduler'
  import ResponsibilityEvaluationBatch from './ResponsibilityEvaluationBatch.svelte'
  import ScheduledRunArtifacts from './ScheduledRunArtifacts.svelte'
  import { scheduledTaskTargetLabel } from './scheduledTaskDisplay'

  type InboxFilter = 'all' | 'unread' | 'attention'
  type ResultStatusFilter = '' | 'completed' | 'failed' | 'skipped'

  interface Props {
    initialTaskID?: string
    initialRunID?: string
    initialFilter?: InboxFilter
  }

  let {
    initialTaskID = '',
    initialRunID = '',
    initialFilter = 'all'
  }: Props = $props()

  const pageSize = 30
  const automaticReadRequests = new Set<string>()
  let filter = $state<InboxFilter>('all')
  let statusFilter = $state<ResultStatusFilter>('')
  let taskIDFilter = $state('')
  let exactRunID = $state('')
  let selectedRunID = $state('')
  let pageIndex = $state(0)
  let showMobileDetail = $state(false)
  let manuallyUnreadRunID = $state('')
  let requestedReadRunID = $state('')
  let actionError = $state('')
  let handledInitialState = $state('')

  const inboxQuery = useScheduledRunInboxQuery(() => ({
    taskId: taskIDFilter,
    runId: exactRunID,
    status: statusFilter,
    readState: exactRunID || filter !== 'unread' ? '' : 'unread',
    attention: !exactRunID && filter === 'attention',
    limit: pageSize,
    offset: pageIndex * pageSize
  }))
  const commands = useSchedulerCommandMutations()
  const items = $derived(inboxQuery.data?.items ?? [])
  const total = $derived(inboxQuery.data?.total ?? 0)
  const totalPages = $derived(Math.max(1, Math.ceil(total / pageSize)))
  const selectedItem = $derived(
    items.find((item) => item.run.scheduled_run_id === selectedRunID) ?? items[0]
  )
  const selectedTaskQuery = useScheduledTaskQuery(() => selectedItem?.run.scheduled_task_id ?? '')
  const renderedSummary = $derived(
    selectedItem?.run.result?.summary ? renderSafeMarkdown(selectedItem.run.result.summary) : ''
  )

  $effect(() => {
    const signature = `${initialTaskID}\u0000${initialRunID}\u0000${initialFilter}`
    if (signature === handledInitialState) return
    handledInitialState = signature
    taskIDFilter = initialTaskID.trim()
    exactRunID = initialRunID.trim()
    filter = ['all', 'unread', 'attention'].includes(initialFilter) ? initialFilter : 'all'
    statusFilter = ''
    selectedRunID = exactRunID
    requestedReadRunID = exactRunID
    pageIndex = 0
    showMobileDetail = Boolean(exactRunID)
    manuallyUnreadRunID = ''
  })

  $effect(() => {
    if (pageIndex >= totalPages) pageIndex = totalPages - 1
    if (pageIndex < 0) pageIndex = 0
  })

  $effect(() => {
    if (items.length === 0) {
      selectedRunID = ''
      return
    }
    if (!items.some((item) => item.run.scheduled_run_id === selectedRunID)) {
      selectedRunID = items[0].run.scheduled_run_id
      manuallyUnreadRunID = ''
    }
  })

  $effect(() => {
    const item = selectedItem
    if (!item || item.is_read || item.run.scheduled_run_id !== requestedReadRunID || item.run.scheduled_run_id === manuallyUnreadRunID) return
    const runID = item.run.scheduled_run_id
    requestedReadRunID = ''
    if (automaticReadRequests.has(runID)) return
    automaticReadRequests.add(runID)
    commands.setRunReadState.mutate(
      { scheduledRunID: runID, read: true },
      {
        onError: (error) => {
          automaticReadRequests.delete(runID)
          actionError = error instanceof Error ? error.message : '已读状态保存失败。'
        }
      }
    )
  })

  function selectItem(item: ScheduledRunInboxItemDTO) {
    selectedRunID = item.run.scheduled_run_id
    requestedReadRunID = item.is_read ? '' : item.run.scheduled_run_id
    manuallyUnreadRunID = ''
    actionError = ''
    showMobileDetail = true
  }

  function setFilter(next: InboxFilter) {
    filter = next
    exactRunID = ''
    selectedRunID = ''
    requestedReadRunID = ''
    pageIndex = 0
    showMobileDetail = false
    manuallyUnreadRunID = ''
  }

  function clearContextFilter() {
    taskIDFilter = ''
    exactRunID = ''
    selectedRunID = ''
    requestedReadRunID = ''
    pageIndex = 0
    showMobileDetail = false
    manuallyUnreadRunID = ''
  }

  function toggleReadState() {
    if (!selectedItem || commands.setRunReadState.isPending) return
    const runID = selectedItem.run.scheduled_run_id
    const read = !selectedItem.is_read
    if (!read) {
      manuallyUnreadRunID = runID
      automaticReadRequests.delete(runID)
    } else {
      manuallyUnreadRunID = ''
      automaticReadRequests.add(runID)
    }
    actionError = ''
    commands.setRunReadState.mutate(
      { scheduledRunID: runID, read },
      {
        onError: (error) => {
          if (!read) manuallyUnreadRunID = ''
          automaticReadRequests.delete(runID)
          actionError = error instanceof Error ? error.message : '已读状态保存失败。'
        }
      }
    )
  }

  function goToPage(nextPage: number) {
    pageIndex = Math.min(totalPages - 1, Math.max(0, nextPage))
    selectedRunID = ''
    requestedReadRunID = ''
    showMobileDetail = false
    manuallyUnreadRunID = ''
  }

  function statusLabel(status: RunStatus) {
    switch (status) {
      case 'completed': return '完成'
      case 'failed': return '失败'
      case 'skipped': return '跳过'
      case 'queued': return '排队中'
      case 'running': return '运行中'
    }
  }

  function statusTone(status: RunStatus): 'success' | 'danger' | 'warning' | 'info' {
    switch (status) {
      case 'completed': return 'success'
      case 'failed': return 'danger'
      case 'skipped': return 'warning'
      default: return 'info'
    }
  }

  function deliveryStatusLabel(status: ScheduledRunDeliveryStatus) {
    switch (status) {
      case 'delivered': return '通知已送达'
      case 'pending':
      case 'sending':
      case 'retrying': return '通知发送中'
      case 'failed':
      case 'dead_letter':
      case 'not_recovered': return '通知失败'
      case 'not_requested': return '未配置通知'
    }
  }

  function deliveryStatusTone(status: ScheduledRunDeliveryStatus): 'neutral' | 'success' | 'danger' | 'info' {
    switch (status) {
      case 'delivered': return 'success'
      case 'pending':
      case 'sending':
      case 'retrying': return 'info'
      case 'failed':
      case 'dead_letter':
      case 'not_recovered': return 'danger'
      case 'not_requested': return 'neutral'
    }
  }

  function resultPreview(item: ScheduledRunInboxItemDTO) {
    const value = item.run.failure_reason || item.run.result?.summary || (
      item.run.status === 'skipped' ? '本次运行已跳过。' : '本次运行未生成可展示的摘要。'
    )
    return value.replace(/\s+/g, ' ').trim()
  }

  function formatDateTime(value: string | undefined) {
    if (!value) return '-'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat($locale === 'en-US' ? 'en-US' : 'zh-CN', {
      month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'
    }).format(date)
  }

  function ontologyExecutionHref(item: ScheduledRunInboxItemDTO) {
    const params = new URLSearchParams()
    const tenantID = selectedTaskQuery.data?.tenant_id?.trim()
    if (tenantID) params.set('tenant_id', tenantID)
    params.set('run_id', item.run.command_id?.trim() || item.run.scheduled_run_id)
    return `/resources/ontology/executions?${params.toString()}`
  }
</script>

<section class="overflow-hidden rounded-lg border border-border bg-card" data-scheduled-run-inbox>
  <div class="grid min-h-[620px] lg:h-[min(72vh,780px)] lg:min-h-[580px] lg:grid-cols-[340px_minmax(0,1fr)] lg:grid-rows-[minmax(0,1fr)]">
    <aside
      class="min-h-0 min-w-0 border-border lg:flex lg:flex-col lg:border-r {showMobileDetail ? 'hidden' : 'flex flex-col'}"
      data-scheduled-run-list
    >
      <div class="border-b border-border px-4 py-3">
        <div class="flex items-center justify-between gap-3">
          <div class="flex min-w-0 items-center gap-2">
            <Inbox class="h-4 w-4 shrink-0 text-brand" />
            <h2 class="truncate text-sm font-semibold text-foreground">结果收件箱</h2>
          </div>
          <Button
            variant="ghost"
            size="icon"
            class="!h-8 !w-8"
            ariaLabel="刷新结果收件箱"
            title="刷新"
            disabled={inboxQuery.isFetching}
            onclick={() => inboxQuery.refetch()}
          ><RefreshCw class={`h-4 w-4 ${inboxQuery.isFetching ? 'animate-spin' : ''}`} /></Button>
        </div>
        <div class="mt-3 grid grid-cols-3 rounded-md bg-muted p-0.5" role="tablist" aria-label="结果筛选">
          <button type="button" role="tab" aria-selected={filter === 'all'} class="h-8 rounded text-xs font-medium {filter === 'all' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}" onclick={() => setFilter('all')}>全部</button>
          <button type="button" role="tab" aria-selected={filter === 'unread'} class="h-8 rounded text-xs font-medium {filter === 'unread' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}" onclick={() => setFilter('unread')}>未读 {inboxQuery.data?.unread_total ?? 0}</button>
          <button type="button" role="tab" aria-selected={filter === 'attention'} class="h-8 rounded text-xs font-medium {filter === 'attention' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}" onclick={() => setFilter('attention')}>待处理 {inboxQuery.data?.attention_total ?? 0}</button>
        </div>
        <div class="mt-3">
          <Select
            value={statusFilter}
            options={[
              { label: '状态：全部', value: '' },
              { label: '完成', value: 'completed' },
              { label: '失败', value: 'failed' },
              { label: '跳过', value: 'skipped' }
            ]}
            onchange={(event) => { statusFilter = event.currentTarget.value as ResultStatusFilter; exactRunID = ''; selectedRunID = ''; pageIndex = 0 }}
          />
        </div>
        {#if taskIDFilter || exactRunID}
          <div class="mt-3 flex items-center justify-between gap-2 border-l-2 border-brand bg-brand/5 px-2.5 py-2 text-xs">
            <span class="min-w-0 truncate text-foreground">{exactRunID ? '已定位指定报告' : `任务：${selectedItem?.task_title ?? taskIDFilter}`}</span>
            <button type="button" class="shrink-0 font-medium text-brand hover:underline" onclick={clearContextFilter}>查看全部</button>
          </div>
        {/if}
      </div>

      <div class="subtle-scrollbar min-h-0 flex-1 overflow-y-auto" aria-live="polite">
        {#if inboxQuery.isLoading}
          <div class="py-16"><LoadingState title="正在加载结果" description=" " /></div>
        {:else if inboxQuery.isError}
          {@const error = inboxQuery.error as ApiClientError}
          <div class="p-4"><ErrorState title="结果收件箱加载失败" message={error.message} correlationId={error.correlationId} /></div>
        {:else if items.length === 0}
          <div class="flex min-h-72 flex-col items-center justify-center px-6 text-center">
            <PackageOpen class="h-10 w-10 text-muted-foreground/35" />
            <div class="mt-3 text-sm font-medium text-foreground">暂无匹配结果</div>
            <div class="mt-1 text-xs leading-5 text-muted-foreground">任务完成后，报告会出现在这里。</div>
          </div>
        {:else}
          <div class="divide-y divide-border/70">
            {#each items as item (item.run.scheduled_run_id)}
              <button
                type="button"
                class="relative block w-full px-4 py-3 text-left transition-colors hover:bg-muted/45 {selectedItem?.run.scheduled_run_id === item.run.scheduled_run_id ? 'bg-brand/5' : 'bg-card'}"
                class:font-semibold={!item.is_read}
                aria-current={selectedItem?.run.scheduled_run_id === item.run.scheduled_run_id ? 'true' : undefined}
                onclick={() => selectItem(item)}
                data-scheduled-run-inbox-item={item.run.scheduled_run_id}
                data-read={item.is_read}
              >
                {#if !item.is_read}<span class="absolute left-1.5 top-5 h-1.5 w-1.5 rounded-full bg-brand" aria-label="未读"></span>{/if}
                <div class="flex min-w-0 items-start justify-between gap-2">
                  <span class="min-w-0 truncate text-[13px] text-foreground">{item.task_title}</span>
                  <span class="shrink-0 text-[10px] font-normal tabular-nums text-muted-foreground">{formatDateTime(item.run.completed_at ?? item.run.created_at)}</span>
                </div>
                <div class="mt-1.5 flex items-center gap-1.5">
                  <Badge tone={statusTone(item.run.status)}>{statusLabel(item.run.status)}</Badge>
                  {#if item.attention_required}<span class="inline-flex items-center gap-1 text-[10px] font-medium text-destructive"><AlertTriangle class="h-3 w-3" />待处理</span>{/if}
                </div>
                <p class="mt-1.5 line-clamp-2 text-xs font-normal leading-5 text-muted-foreground">{resultPreview(item)}</p>
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <div class="flex h-12 shrink-0 items-center justify-between border-t border-border px-3 text-xs text-muted-foreground">
        <span>共 {total} 封</span>
        <div class="flex items-center gap-1.5">
          <Button variant="ghost" size="icon" class="!h-8 !w-8" ariaLabel="上一页" disabled={pageIndex <= 0} onclick={() => goToPage(pageIndex - 1)}><ChevronLeft class="h-4 w-4" /></Button>
          <span class="min-w-12 text-center tabular-nums">{pageIndex + 1} / {totalPages}</span>
          <Button variant="ghost" size="icon" class="!h-8 !w-8" ariaLabel="下一页" disabled={pageIndex >= totalPages - 1} onclick={() => goToPage(pageIndex + 1)}><ChevronRight class="h-4 w-4" /></Button>
        </div>
      </div>
    </aside>

    <main
      class="min-h-0 min-w-0 bg-background {showMobileDetail ? 'flex flex-col' : 'hidden'} lg:flex lg:flex-col"
      data-scheduled-run-reading-pane
    >
      {#if selectedItem}
        <div class="flex min-h-14 shrink-0 items-center justify-between gap-3 border-b border-border px-4 lg:px-6">
          <div class="flex min-w-0 items-center gap-2">
            <Button variant="ghost" size="icon" class="!h-8 !w-8 lg:hidden" ariaLabel="返回结果列表" title="返回" onclick={() => (showMobileDetail = false)}><ArrowLeft class="h-4 w-4" /></Button>
            <span class="truncate text-xs text-muted-foreground">{selectedItem.run.scheduled_run_id}</span>
          </div>
          <Button
            variant="outline"
            size="sm"
            disabled={commands.setRunReadState.isPending}
            ariaLabel={selectedItem.is_read ? '标记为未读' : '标记为已读'}
            title={selectedItem.is_read ? '标记为未读' : '标记为已读'}
            onclick={toggleReadState}
          >
            {#if selectedItem.is_read}<Mail class="h-4 w-4" />标记未读{:else}<MailOpen class="h-4 w-4" />标记已读{/if}
          </Button>
        </div>

        <div class="subtle-scrollbar min-h-0 flex-1 overflow-y-auto">
          <article class="mx-auto w-full max-w-[960px] px-5 py-6 sm:px-8 sm:py-8 lg:px-10" data-scheduled-run-report>
            <header>
              <div class="flex flex-wrap items-center gap-2">
                <Badge tone={statusTone(selectedItem.run.status)}>{statusLabel(selectedItem.run.status)}</Badge>
                {#if selectedItem.run.delivery_status}
                  <Badge tone={deliveryStatusTone(selectedItem.run.delivery_status)}>{deliveryStatusLabel(selectedItem.run.delivery_status)}</Badge>
                {/if}
                {#if selectedItem.attention_required}<Badge tone="danger">需要关注</Badge>{/if}
              </div>
              <h2 class="mt-4 break-words text-xl font-semibold leading-8 text-foreground">{selectedItem.task_title}</h2>
              <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span>{formatDateTime(selectedItem.run.completed_at ?? selectedItem.run.created_at)}</span>
                <span>{scheduledTaskTargetLabel(selectedItem.run.target_mode)}</span>
                <span>第 {selectedItem.run.attempt} 次尝试</span>
                <span>{selectedItem.run.trigger_type === 'manual' ? '手动触发' : selectedItem.run.trigger_type === 'retry' ? '重试' : '计划触发'}</span>
              </div>
            </header>

            {#if actionError}
              <div class="mt-5 border-l-2 border-destructive bg-destructive/5 px-4 py-3 text-sm text-destructive" role="alert">{actionError}</div>
            {/if}

            {#if selectedItem.run.failure_reason}
              <section class="mt-6 border-l-2 border-destructive bg-destructive/5 px-4 py-3">
                <div class="flex items-center gap-2 text-sm font-semibold text-destructive"><AlertTriangle class="h-4 w-4" />运行失败</div>
                <p class="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-foreground">{selectedItem.run.failure_reason}</p>
              </section>
            {/if}

            <section class="mt-7 border-t border-border pt-6">
              <div class="flex items-center gap-2 text-sm font-semibold text-foreground"><FileText class="h-4 w-4 text-brand" />最终结果报告</div>
              {#if renderedSummary}
                <div class="report-body mt-4 max-w-[82ch] text-[15px] leading-7" data-scheduled-run-summary>{@html renderedSummary}</div>
              {:else}
                <p class="mt-4 text-sm leading-6 text-muted-foreground">本次运行未生成可展示的摘要。</p>
              {/if}
              <ScheduledRunArtifacts
                scheduledTaskID={selectedItem.run.scheduled_task_id}
                scheduledRunID={selectedItem.run.scheduled_run_id}
                artifacts={selectedItem.run.result?.artifacts ?? []}
              />
            </section>

            {#if selectedItem.run.responsibility_reconcile}
              <section class="mt-7 border-t border-border pt-6" data-responsibility-reconcile-stats>
                <h3 class="text-sm font-semibold text-foreground">责任协调结果</h3>
                <div class="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
                  <span>候选 {selectedItem.run.responsibility_reconcile.candidates}</span>
                  <span>已认领 {selectedItem.run.responsibility_reconcile.claimed}</span>
                  <span>已派发 {selectedItem.run.responsibility_reconcile.dispatched}</span>
                  <span>已恢复 {selectedItem.run.responsibility_reconcile.recovered_outcomes}</span>
                  <span>阻塞复核 {selectedItem.run.responsibility_reconcile.blockers_rechecked}</span>
                  <span>阻塞解除 {selectedItem.run.responsibility_reconcile.blockers_resolved}</span>
                  <span>超时 {selectedItem.run.responsibility_reconcile.timed_out}</span>
                  <span class:text-destructive={selectedItem.run.responsibility_reconcile.failed > 0}>失败 {selectedItem.run.responsibility_reconcile.failed}</span>
                </div>
              </section>
            {/if}

            {#if selectedItem.run.responsibility_evaluation_batch}
              <div class="mt-7 border-t border-border pt-6">
                <ResponsibilityEvaluationBatch
                  scheduledTaskID={selectedItem.run.scheduled_task_id}
                  scheduledRunID={selectedItem.run.scheduled_run_id}
                  batch={selectedItem.run.responsibility_evaluation_batch}
                />
              </div>
            {/if}

            {#if selectedItem.run.responsibility_evaluation}
              {@const evaluation = selectedItem.run.responsibility_evaluation}
              <section class="mt-7 border-t border-border pt-6" data-responsibility-evaluation>
                <h3 class="text-sm font-semibold text-foreground">责任评估结果</h3>
                <div class="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
                  <span>{evaluation.actionable ? '需要行动' : '无需行动'}</span>
                  <span>{evaluation.decision_kind}</span>
                  <span>候选 {evaluation.candidate_count}</span>
                  <span>可执行 {evaluation.actionable_count}</span>
                  <span>等待 {evaluation.waiting_count}</span>
                  <span>复核 {evaluation.review_count}</span>
                  <span>升级 {evaluation.escalated_count}</span>
                  <span>无效 {evaluation.invalid_count}</span>
                </div>
                {#if evaluation.reason}<p class="mt-3 text-sm leading-6 text-muted-foreground">{evaluation.reason}</p>{/if}
              </section>
            {/if}

            <footer class="mt-7 flex flex-wrap items-center gap-2 border-t border-border pt-5">
              {#if selectedItem.run.mission_id}<Button href={`/missions/${encodeURIComponent(selectedItem.run.mission_id)}`} variant="outline" size="sm">Mission</Button>{/if}
              {#if selectedItem.run.responsibility_id}<Button href={`/responsibilities?responsibility=${encodeURIComponent(selectedItem.run.responsibility_id)}`} variant="outline" size="sm">Responsibility</Button>{/if}
              {#if selectedItem.run.target_mode === 'agent_task'}<Button href={ontologyExecutionHref(selectedItem)} variant="outline" size="sm"><Network class="h-3.5 w-3.5" />在本体中查看</Button>{/if}
              {#if selectedItem.run.task_id}<span class="font-mono text-xs text-muted-foreground">Task {selectedItem.run.task_id}</span>{/if}
            </footer>
          </article>
        </div>
      {:else}
        <div class="flex min-h-[420px] flex-1 flex-col items-center justify-center px-6 text-center">
          {#if inboxQuery.isLoading}
            <LoadingState title="正在打开结果" description=" " />
          {:else}
            <CheckCircle2 class="h-11 w-11 text-muted-foreground/30" />
            <div class="mt-3 text-sm font-medium text-foreground">选择一封结果报告</div>
            <p class="mt-1 text-xs text-muted-foreground">报告正文、产出物和关联事务会显示在这里。</p>
          {/if}
        </div>
      {/if}
    </main>
  </div>
</section>
