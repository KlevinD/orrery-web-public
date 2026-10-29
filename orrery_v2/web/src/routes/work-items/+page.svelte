<script lang="ts">
  import { page } from '$app/stores'
  import { Bell, Inbox, LayoutList, ListTodo, Settings2 } from 'lucide-svelte'
  import WorkActivity from '#lib/components/workhub/WorkActivity.svelte'
  import TaskNotifications from '#lib/components/workhub/TaskNotifications.svelte'
  import { useTaskNotificationSummary } from '#lib/queries/task-notifications'
  import WorkActionWorkspace from '#lib/components/workhub/WorkActionWorkspace.svelte'
  import ApprovalWorkflowWorkspace from '#lib/components/approvals/ApprovalWorkflowWorkspace.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import WorkItemsCenter from '#lib/components/workhub/WorkItemsCenter.svelte'
  import { buildWorkCenterItems } from '#lib/components/workhub/work-center'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useIdeaInfiniteListQuery } from '#lib/queries/ideas'
  import { useMissionInfiniteListQuery } from '#lib/queries/missions'
  import { useTodoInfiniteListQuery } from '#lib/queries/todos'
  import { session } from '#lib/stores/session.svelte'

  const pageSize = 100
  let searchQuery = $state('')
  let committedSearch = $state('')
  const searchPending = $derived(searchQuery.trim() !== committedSearch)
  $effect(() => {
    const query = searchQuery.trim()
    const timer = setTimeout(() => { committedSearch = query }, 300)
    return () => clearTimeout(timer)
  })
  const canReadPending = $derived(['todo.read', 'approvals.read', 'scheduler.read'].some(scope => hasPermissionScope(session.permissionScope, scope)))
  const view = $derived($page.url.searchParams.get('view') ?? (canReadPending ? 'inbox' : 'activity'))
  const section = $derived(['all', 'todo', 'idea', 'mission'].includes(view) ? 'all' : ['activity', 'settings', 'history', 'requested', 'workflows'].includes(view) ? view : 'inbox')
  const inbox = $derived(section !== 'all')
  const canReadApprovals = $derived(hasPermissionScope(session.permissionScope, 'approvals.read'))
  const notifications = useTaskNotificationSummary()
  const L = (zh: string, en: string) => $locale.startsWith('zh') ? zh : en
  const kind = $derived(view === 'todo' || view === 'idea' || view === 'mission' ? view : 'all')
  const t = $derived(createTranslator($locale))
  const canReadTodos = $derived(hasPermissionScope(session.permissionScope, 'todo.read'))
  const canReadIdeas = $derived(hasPermissionScope(session.permissionScope, 'idea.read'))
  const canReadMissions = $derived(hasPermissionScope(session.permissionScope, 'mission.read'))

  const showTodos = $derived(canReadTodos && !inbox && (kind === 'all' || kind === 'todo'))
  const showIdeas = $derived(canReadIdeas && !inbox && (kind === 'all' || kind === 'idea'))
  const showMissions = $derived(canReadMissions && !inbox && (kind === 'all' || kind === 'mission'))
  const todosQuery = useTodoInfiniteListQuery(
    () => ({ includeCompleted: true, q: committedSearch, limit: pageSize }),
    () => showTodos && !searchPending
  )
  const ideasQuery = useIdeaInfiniteListQuery(
    () => ({ q: committedSearch, limit: pageSize }),
    () => showIdeas && !searchPending
  )
  const missionsQuery = useMissionInfiniteListQuery(
    () => ({ search: committedSearch, limit: pageSize }),
    () => showMissions && !searchPending
  )

  const items = $derived(buildWorkCenterItems(
    showTodos ? todosQuery.data?.pages.flatMap(page => page.todos ?? []) ?? [] : [],
    showIdeas ? ideasQuery.data?.pages.flatMap(page => page.ideas ?? []) ?? [] : [],
    showMissions ? missionsQuery.data?.pages.flatMap(page => page.missions) ?? [] : []
  ))
  const loading = $derived(
    searchPending ||
    (showTodos && todosQuery.isPending) ||
    (showIdeas && ideasQuery.isPending) ||
    (showMissions && missionsQuery.isPending)
  )
  const hasMore = $derived(
    (showTodos && todosQuery.hasNextPage) ||
    (showIdeas && ideasQuery.hasNextPage) ||
    (showMissions && missionsQuery.hasNextPage)
  )
  const loadingMore = $derived(
    (showTodos && todosQuery.isFetchingNextPage) ||
    (showIdeas && ideasQuery.isFetchingNextPage) ||
    (showMissions && missionsQuery.isFetchingNextPage)
  )
  const total = $derived(
    (showTodos ? todosQuery.data?.pages[0]?.total ?? 0 : 0) +
    (showIdeas ? ideasQuery.data?.pages[0]?.total ?? 0 : 0) +
    (showMissions ? missionsQuery.data?.pages[0]?.summary.total ?? 0 : 0)
  )
  const errorMessage = $derived.by(() => {
    const errors = [
      canReadTodos && (kind === 'all' || kind === 'todo') && todosQuery.isError ? todosQuery.error : undefined,
      canReadIdeas && (kind === 'all' || kind === 'idea') && ideasQuery.isError ? ideasQuery.error : undefined,
      canReadMissions && (kind === 'all' || kind === 'mission') && missionsQuery.isError ? missionsQuery.error : undefined
    ].filter(Boolean)
    return errors.map(readableError).join(' · ')
  })

  function loadMore() {
    if (loading || loadingMore) return
    if (showTodos && todosQuery.hasNextPage) void todosQuery.fetchNextPage()
    if (showIdeas && ideasQuery.hasNextPage) void ideasQuery.fetchNextPage()
    if (showMissions && missionsQuery.hasNextPage) void missionsQuery.fetchNextPage()
  }

  function readableError(error: unknown) {
    return error instanceof Error ? error.message : String(error || 'Request failed')
  }
</script>

<svelte:head><title>{t('workcenter.title')} · Orrery</title></svelte:head>

<div class="mx-auto w-full min-w-0 max-w-[1440px] space-y-4 px-4 py-5 sm:px-6 lg:px-8" data-work-center>
  <header class="flex flex-wrap items-center justify-between gap-3"><h1 class="text-xl font-semibold text-foreground">{t('workcenter.title')}</h1><div class="flex flex-wrap gap-2">{#if canReadApprovals}<a class="rounded-md border border-border px-3 py-2 text-sm hover:bg-muted" href="/work-items?view=workflows">{L('流程设置', 'Workflow settings')}</a>{/if}<a class="inline-flex items-center gap-2 rounded-md border border-border px-3 py-2 text-sm hover:bg-muted" href="/work-items?view=settings&tab=subscriptions"><Settings2 class="h-4 w-4" />{L('订阅与通知设置', 'Subscriptions and settings')}</a></div></header>
  <nav class="flex gap-4 overflow-x-auto border-b border-border" aria-label={t('workcenter.title')}>
    {#each [{ value: 'inbox', label: L('需我处理', 'Needs my action'), icon: Inbox }, { value: 'requested', label: L('我发起的', 'Started by me'), icon: ListTodo }, { value: 'history', label: L('已处理', 'Processed'), icon: LayoutList }, { value: 'activity', label: L('动态与成果', 'Activity and results'), icon: Bell }, { value: 'all', label: L('全部事务', 'All work'), icon: LayoutList }] as tab}
      {@const Icon = tab.icon}
      {@const active = tab.value === section}
      <a
        href={`/work-items?view=${tab.value}`}
        aria-current={active ? 'page' : undefined}
        class="inline-flex min-h-10 items-center gap-1 border-b-2 px-2 py-2 text-sm transition-colors sm:gap-2 sm:px-3 {active ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
      ><Icon class="h-4 w-4 shrink-0" />{tab.label}</a>
    {/each}
  </nav>
  {#if !inbox}
    {#key kind}
      <WorkItemsCenter
        {items} {loading} {errorMessage} {hasMore} {loadingMore} {total}
        bind:searchQuery serverSearch onLoadMore={loadMore} kindFilter={kind} embedded
      />
    {/key}
  {:else if section === 'activity'}
    <WorkActivity />
  {:else if section === 'settings'}
    <TaskNotifications embedded settingsOnly initialTab={$page.url.searchParams.get('tab') || 'subscriptions'} />
  {:else if section === 'workflows'}
    {#if canReadApprovals}<ApprovalWorkflowWorkspace embedded />{:else}<p role="alert">{L('当前账号无权查看审批流程。', 'You do not have access to approval workflows.')}</p>{/if}
  {:else}
    <WorkActionWorkspace view={section === 'history' || section === 'requested' ? section : 'pending'} />
  {/if}
</div>
