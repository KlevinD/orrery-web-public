<script lang="ts">
  import { tick } from 'svelte'
  import { page } from '$app/stores'
  import { Bell, ChevronLeft, ChevronRight, FileText, RefreshCw } from 'lucide-svelte'
  import { locale } from '#lib/i18n'
  import { session } from '#lib/stores/session.svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useWorkActivitySources } from '#lib/queries/work-activity'
  import { useSchedulerCommandMutations } from '#lib/queries/scheduler'
  import { buildWorkActivity, type WorkActivityItem } from './work-activity'
  import { renderSafeMarkdown } from '#lib/markdown'
  import TaskNotifications from './TaskNotifications.svelte'
  import ScheduledRunArtifacts from '../../../routes/scheduled-tasks/ScheduledRunArtifacts.svelte'
  import Button from '#lib/components/ui/Button.svelte'

  let { compact = false }: { compact?: boolean } = $props()
  const L = (zh: string, en: string) => $locale.startsWith('zh') ? zh : en
  const sources = useWorkActivitySources()
  const commands = useSchedulerCommandMutations()
  const canReadScheduler = $derived(hasPermissionScope(session.permissionScope, 'scheduler.read'))
  const canReadNotifications = $derived(!!session.user.agentProfileId)
  const rows = $derived(buildWorkActivity(sources.notifications.data ?? [], sources.results.data ?? []))
  let search = $state('')
  let onlyUnread = $state(false)
  let pageIndex = $state(0)
  let selected = $state<WorkActivityItem | null>(null)
  let actionError = $state('')
  let handledLink = $state('')
  let detailElement = $state<HTMLDivElement | undefined>()
  const directItemID = $derived($page.url.searchParams.get('item') || '')
  const filtered = $derived(rows.filter(row => (!onlyUnread || row.unread) && `${row.title} ${row.summary}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase())))
  const size = $derived(compact ? 8 : 25)
  const pages = $derived(Math.max(1, Math.ceil(filtered.length / size)))
  const visible = $derived(filtered.slice(pageIndex * size, (pageIndex + 1) * size))
  const loading = $derived(canReadNotifications && sources.notifications.isPending || canReadScheduler && sources.results.isPending)
  const failed = $derived(canReadNotifications && sources.notifications.isError || canReadScheduler && sources.results.isError)
  const markdown = $derived(selected?.summary ? renderSafeMarkdown(selected.summary) : '')
  $effect(() => { search; onlyUnread; pageIndex = 0 })
  $effect(() => { if (pageIndex >= pages) pageIndex = pages - 1 })
  $effect(() => {
    if (compact || loading) return
    const itemID = $page.url.searchParams.get('item') || ''
    const runID = $page.url.searchParams.get('run') || ''
    const taskID = $page.url.searchParams.get('task') || ''
    const signature = JSON.stringify([itemID, runID, taskID])
    if ((!itemID && !runID) || signature === handledLink) return
    const row = rows.find(row => itemID ? row.notifications.some(item => item.id === itemID) : row.result?.run.scheduled_run_id === runID && (!taskID || row.result?.run.scheduled_task_id === taskID))
    if (row) { handledLink = signature; void open(row) }
  })
  function href(row: WorkActivityItem) {
    const notification = row.notifications[0]
    return notification ? `/work-items?view=activity&item=${encodeURIComponent(notification.id)}` : `/work-items?view=activity&run=${encodeURIComponent(row.result!.run.scheduled_run_id)}&task=${encodeURIComponent(row.result!.run.scheduled_task_id)}`
  }
  async function open(row: WorkActivityItem) {
    selected = row; actionError = ''
    await tick()
    if (detailElement && (detailElement.parentElement?.clientWidth ?? 0) < 880) detailElement.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    if (row.notifications.length || !row.result) return
    try { await commands.setRunReadState.mutateAsync({ scheduledRunID: row.result.run.scheduled_run_id, read: true }) }
    catch (error) { actionError = error instanceof Error ? error.message : L('已读状态同步失败，请重试。', 'Could not sync read state. Please retry.') }
  }
  function refresh() {
    if (canReadNotifications) void sources.notifications.refetch()
    if (canReadScheduler) void sources.results.refetch()
  }
  function date(value: string) { return new Date(value).toLocaleString($locale, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) }
</script>

<section class="@container min-w-0 space-y-4" aria-label={L('动态与成果', 'Activity and results')} data-work-activity>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <p class="text-xs leading-5 text-muted-foreground">{L('通知与对应的任务成果合并展示，阅读不会改变待办或审批状态。', 'Notifications and matching results appear together. Reading does not resolve tasks or approvals.')}</p>
    <Button variant="ghost" size="sm" onclick={refresh}><RefreshCw class="mr-1 h-3.5 w-3.5" />{L('刷新', 'Refresh')}</Button>
  </div>
  {#if !compact}<div class="flex flex-wrap items-center gap-3"><input class="h-9 min-w-0 flex-1 rounded-md border border-border bg-card px-3 text-sm" aria-label={L('搜索动态与成果', 'Search activity and results')} placeholder={L('搜索标题或内容', 'Search titles or content')} bind:value={search} /><label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={onlyUnread} />{L('只看未读', 'Unread only')}</label></div>{/if}
  {#if failed}<div role="alert" class="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm">{L('部分动态暂时无法加载，当前列表与未读数量可能不完整。', 'Some activity could not be loaded. The list and unread count may be incomplete.')}<button class="ml-2 underline" onclick={refresh}>{L('重试', 'Retry')}</button></div>{/if}
  {#if loading && rows.length > 0}<p role="status" class="text-xs text-muted-foreground">{L('正在汇总其余动态，当前列表尚未完整。', 'Loading remaining activity. The current list is not complete yet.')}</p>{/if}
  {#if loading && rows.length === 0 && !directItemID}<p role="status" class="py-6 text-center text-sm text-muted-foreground">{L('正在汇总动态与成果…', 'Loading activity and results…')}</p>
  {:else}
    <div class={compact ? 'min-w-0' : 'grid min-w-0 items-start gap-4 @[880px]:grid-cols-[minmax(260px,.8fr)_minmax(0,1.2fr)]'}>
      <div class="min-w-0 space-y-2">
        {#each visible as row (row.id)}
          {#snippet content()}
            <span class="flex min-w-0 items-start gap-2">{#if row.notifications.length}<Bell class="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />{:else}<FileText class="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />{/if}<span class="min-w-0 flex-1 break-words text-sm font-medium">{row.title}</span>{#if row.unread}<span class="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-brand" aria-label={L('未读', 'Unread')}></span>{/if}</span>
            <span class="mt-2 line-clamp-2 block break-words text-xs leading-5 text-muted-foreground">{row.summary}</span>
            <span class="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground"><time>{date(row.date)}</time>{#if row.attention}<span class="text-amber-700">{L('需处理', 'Needs action')}</span>{:else if row.notifications.length && row.result}<span>{L('通知 · 任务成果', 'Notification · result')}</span>{/if}</span>
          {/snippet}
          {#if compact}<a href={href(row)} class="block rounded-lg border border-border bg-card p-3 hover:bg-muted/30" data-activity-row={row.id}>{@render content()}</a>
          {:else}<button type="button" class="w-full rounded-lg border p-4 text-left transition-colors {selected?.id === row.id ? 'border-brand bg-brand/5' : 'border-border bg-card hover:bg-muted/30'}" data-activity-row={row.id} aria-pressed={selected?.id === row.id} onclick={() => open(row)}>{@render content()}</button>{/if}
        {:else}<p class="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">{L('暂无匹配的动态与成果', 'No matching activity or results')}</p>{/each}
        <div class="flex flex-wrap items-center justify-between gap-2 pt-2 text-xs text-muted-foreground"><span>{L('共 ', 'Total: ')}{filtered.length}{failed || loading ? '+' : ''}</span><div class="flex items-center gap-2"><Button variant="outline" size="sm" disabled={pageIndex === 0} ariaLabel={L('上一页', 'Previous page')} onclick={() => pageIndex--}><ChevronLeft class="h-3.5 w-3.5" /></Button><span>{pageIndex + 1} / {pages}</span><Button variant="outline" size="sm" disabled={pageIndex + 1 >= pages} ariaLabel={L('下一页', 'Next page')} onclick={() => pageIndex++}><ChevronRight class="h-3.5 w-3.5" /></Button></div></div>
      </div>
      {#if !compact}
        <div class="min-w-0" data-activity-detail bind:this={detailElement}>
          {#if selected?.notifications.length || (!selected && directItemID)}
            {@const notificationID = selected?.notifications[0]?.id || directItemID}
            {#key notificationID}<TaskNotifications embedded detailOnly itemID={notificationID} />{/key}
          {:else if selected?.result}
            {@const run = selected.result.run}
            <article class="space-y-4 rounded-lg border border-border bg-card p-5">
              <h2 class="break-words text-lg font-semibold">{selected.title}</h2><time class="text-xs text-muted-foreground">{date(selected.date)}</time>
              {#if actionError}<div role="alert" class="text-sm text-destructive">{actionError}<button class="ml-2 underline" onclick={() => selected && open(selected)}>{L('重试', 'Retry')}</button></div>{/if}
              {#if selected.attention}<p class="text-sm text-amber-700">{L('这项运行仍需处理，阅读报告不会消除异常。', 'This run still needs action. Reading does not resolve it.')}</p>{/if}
              <div class="prose prose-sm max-w-none break-words dark:prose-invert">{@html markdown}</div>
              <ScheduledRunArtifacts scheduledTaskID={run.scheduled_task_id} scheduledRunID={run.scheduled_run_id} artifacts={run.result?.artifacts ?? []} />
              <a class="inline-block text-sm text-brand underline" href={`/scheduled-tasks?view=results&task=${encodeURIComponent(run.scheduled_task_id)}&run=${encodeURIComponent(run.scheduled_run_id)}`}>{L('查看完整运行与处理入口', 'Open run details and actions')}</a>
            </article>
          {:else}<div class="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">{L('选择一条动态，查看成果和报告。', 'Select an item to read its result and reports.')}</div>{/if}
        </div>
      {/if}
    </div>
  {/if}
</section>
