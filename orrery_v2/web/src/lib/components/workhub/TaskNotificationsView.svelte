<script lang="ts">
 import { page } from '$app/stores'
 import { onDestroy } from 'svelte'
 import { createQuery, useQueryClient } from '@tanstack/svelte-query'
 import { Bell, FileText, RefreshCw, Settings2, Users, ArrowDownToLine } from 'lucide-svelte'
 import { notificationApi, type NotificationItem, type NotificationSource, type TaskSubscription, type NotificationChannel, type NotificationReceipt } from '#lib/api/notifications'
 import { notificationKeys, notificationViewerKey } from '#lib/queries/task-notifications'
 import { session } from '#lib/stores/session.svelte'
 import { hasPermissionScope } from '#lib/permissions/access'
 import { locale } from '#lib/i18n'
 import { readActivityNotification } from '#lib/api/work-activity'
 import Button from '#lib/components/ui/Button.svelte'
 let { initialTab = 'inbox', itemID = '', embedded = false, detailOnly = false, settingsOnly = false }: { initialTab?: string; itemID?: string; embedded?: boolean; detailOnly?: boolean; settingsOnly?: boolean } = $props()
 const abort = new AbortController()
 const options = { signal: abort.signal }
 let active = true
 onDestroy(() => { active = false; abort.abort() })
 let handledItem = $state('')
 const client = useQueryClient()
 const L = (zh: string, en: string) => $locale.startsWith('zh') ? zh : en
 const canManage = $derived(hasPermissionScope(session.permissionScope, 'scheduler.write') || hasPermissionScope(session.permissionScope, 'operations.manage'))
 let tab = $state('inbox')
 let initialized = $state(false)
 let onlyUnread = $state(false)
 let offset = $state(0)
 let selected = $state<NotificationItem | null>(null)
 let busy = $state(false)
 let error = $state('')
 let message = $state('')
 let receipts = $state<NotificationReceipt[] | null>(null)
 let drafts = $state<Record<string, TaskSubscription>>({})
 let preferenceChannels = $state<NotificationChannel[]>([])
 let preferencesLoaded = $state(false)
 let rule = $state<NotificationSource | null>(null)
 let newRule = $state(false)
 let preview = $state('')
 const viewer = $derived(notificationViewerKey())
 const inbox = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'inbox', onlyUnread, offset], queryFn: ({ signal }) => notificationApi.inbox(onlyUnread, offset, { signal }), enabled: tab === 'inbox' && !detailOnly, refetchInterval: 30_000 }))
 const sources = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'sources'], queryFn: ({ signal }) => notificationApi.sources({ signal }), enabled: !detailOnly && (tab === 'subscriptions' || tab === 'manage') }))
 const preferences = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'preferences'], queryFn: ({ signal }) => notificationApi.preferences({ signal }), enabled: tab === 'preferences' }))
 const bindings = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'channels'], queryFn: ({ signal }) => notificationApi.channels({ signal }), enabled: !detailOnly && (tab === 'subscriptions' || tab === 'preferences') }))
 const members = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'members'], queryFn: ({ signal }) => notificationApi.members({ signal }), enabled: canManage && tab === 'manage' }))
 const candidates = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'candidates'], queryFn: ({ signal }) => notificationApi.candidates({ signal }), enabled: canManage && tab === 'manage' }))
 const taskFilter = $derived($page.url.searchParams.get('task') ?? '')
 const events = createQuery(() => ({ queryKey: [...notificationKeys.all, viewer, 'events', rule?.id], queryFn: ({ signal }) => notificationApi.events(rule!.id, { signal }), enabled: canManage && tab === 'manage' && !!rule && !newRule }))
 const shownSources = $derived((sources.data ?? []).filter(s => !taskFilter || s.task_id === taskFilter))
 $effect(() => {
  if (!initialized) {
   const requested = initialTab !== 'inbox' ? initialTab : $page.url.searchParams.get('tab')
   tab = detailOnly ? 'inbox' : requested === 'manage' && canManage ? 'manage' : requested === 'preferences' ? 'preferences' : requested === 'subscriptions' || taskFilter || settingsOnly ? 'subscriptions' : 'inbox'
   initialized = true
  }
  const id = itemID || (!embedded ? $page.url.searchParams.get('item') : '')
  if (id && id !== handledItem) { handledItem = id; void openID(id) }
 })
 $effect(() => {
  if (preferences.data && !preferencesLoaded) { preferenceChannels = [...preferences.data.channels]; preferencesLoaded = true }
 })
 function draft(s: NotificationSource): TaskSubscription {
  return drafts[s.id] ?? { enabled: s.subscription?.enabled ?? false, channels: [...(s.subscription?.channels ?? [])], include_failures: s.subscription?.include_failures ?? false }
 }
 function updateDraft(s: NotificationSource, changes: Partial<TaskSubscription>) { drafts[s.id] = { ...draft(s), ...changes } }
 function toggleChannel(items: NotificationChannel[], channel: NotificationChannel, checked: boolean): NotificationChannel[] { return checked ? [...new Set([...items, channel])] : items.filter(c => c !== channel) }
 function channelName(c: string) { return c === 'weixin' ? L('个人微信', 'WeChat') : L('飞书', 'Feishu') }
 function bindingName(channel: NotificationChannel) {
  if (bindings.isError) return L('绑定状态读取失败', 'Could not load link status')
  if (bindings.isPending) return L('正在读取绑定状态…', 'Loading link status…')
  return bindings.data?.includes(channel) ? L('已绑定', 'Linked') : L('尚未绑定，绑定后才可送达', 'Link this channel before delivery')
 }
 function statusName(status: string) {
  const names: Record<string, [string, string]> = { completed: ['已完成', 'Completed'], partial: ['报告已生成 · 部分步骤未完成', 'Report ready · follow-up needed'], failed: ['未完成', 'Incomplete'], delivered: ['已送达', 'Delivered'], queued: ['待发送', 'Queued'], pending: ['待发送', 'Pending'], retrying: ['等待重试', 'Retrying'], unbound: ['尚未绑定', 'Not linked'], cancelled: ['已停止推送', 'Cancelled'] }
  return names[status] ? L(...names[status]) : status
 }
 async function act(fn: () => Promise<void>, success = '') {
  busy = true; error = ''; message = ''
  try { await fn(); if (active) message = success }
  catch (e) { if (active) error = e instanceof Error ? e.message : L('操作失败，请重试', 'Please try again') }
  finally { if (active) { busy = false; await Promise.all([client.invalidateQueries({ queryKey: [...notificationKeys.all, viewer] }), client.invalidateQueries({ queryKey: ['scheduler'] })]) } }
 }
 async function openID(id: string) {
  selected = null; receipts = null
  await act(async () => {
   const item = await notificationApi.item(id, options)
   if (!active) return
   selected = item
   await readActivityNotification(item, hasPermissionScope(session.permissionScope, 'scheduler.read'))
   if (!active) return
   selected = { ...item, read: true }
  })
 }
 async function open(item: NotificationItem) { await openID(item.id) }
 async function download(id: string, name: string) {
  if (!selected) return
  await act(async () => {
   const blob = await notificationApi.artifact(selected!.id, id, options)
   if (!active) return
   const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 30_000)
  })
 }
 function edit(source: NotificationSource, create = false) { rule = { ...source, people: [...(source.people ?? [])], enabled: create ? true : source.enabled }; newRule = create; preview = ''; receipts = null }
 async function saveRule() {
  if (!rule) return
  await act(async () => { rule = await notificationApi.saveSource(rule!, newRule, options); newRule = false }, L('通知规则已保存；仅影响后续执行。', 'Notification rule saved. Future runs only.'))
 }
</script>

<div class={embedded ? "min-w-0 space-y-4" : "mx-auto w-full max-w-7xl space-y-5 p-4 md:p-6"}>
 {#if !embedded}
 <header class="flex flex-wrap items-start justify-between gap-4">
  <div><div class="flex items-center gap-2"><Bell class="h-5 w-5 text-brand" /><h1 class="text-xl font-semibold">{L('通知中心', 'Notifications')}</h1></div><p class="mt-2 text-sm text-muted-foreground">{L('查看 Agent 的成果，订阅你关心的任务。', 'Read agent results and subscribe to the work you follow.')}</p></div>
  <div class="flex gap-2"><a class="rounded-lg border px-3 py-2 text-sm hover:bg-muted" href="/approvals">{L('审批与待办', 'Approvals')}</a><Button variant="outline" disabled={busy} onclick={() => client.invalidateQueries({ queryKey: notificationKeys.all })}><RefreshCw class="mr-1 h-4 w-4" />{L('刷新', 'Refresh')}</Button></div>
 </header>
 {/if}
 {#if !detailOnly}
 <nav aria-label={L('通知视图', 'Notification views')} class="flex flex-wrap gap-1 border-b pb-2">
  {#each [['inbox', L('任务成果', 'Results')], ['subscriptions', L('我的订阅', 'Subscriptions')], ['preferences', L('接收偏好', 'Preferences')], ...(canManage ? [['manage', L('任务通知设置', 'Task notification settings')]] : [])].filter(([key]) => !settingsOnly || key !== 'inbox') as [key, label]}
   <button type="button" class="rounded-lg px-4 py-2 text-sm transition-colors {tab === key ? 'bg-brand text-white' : 'text-muted-foreground hover:bg-muted'}" aria-current={tab === key ? 'page' : undefined} onclick={() => { tab = key; error = ''; message = '' }}>{label}{#if key === 'inbox' && inbox.data?.unread}<span class="ml-2 rounded bg-black/10 px-1.5">{inbox.data.unread}</span>{/if}</button>
  {/each}
 </nav>
 {/if}
 {#if error}<div role="alert" class="rounded-lg border border-red-300 bg-red-50 p-3 text-sm text-red-800">{error}{#if detailOnly && itemID}<button class="ml-2 underline" onclick={() => openID(itemID)}>{L('重试', 'Retry')}</button>{/if}</div>{/if}
 {#if message}<div role="status" class="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800">{message}</div>{/if}

 {#if tab === 'inbox'}
  {#if !detailOnly}<div class="flex items-center justify-between text-sm"><label class="flex items-center gap-2"><input type="checkbox" bind:checked={onlyUnread} onchange={() => offset = 0} />{L('只看未读', 'Unread only')}</label><span class="text-muted-foreground">{L('共 ', 'Total: ')}{inbox.data?.total ?? 0}</span></div>{/if}
  {#if !detailOnly && inbox.isError}<div role="alert" class="rounded-xl border p-5">{L('通知加载失败，请点击刷新重试。', 'Could not load notifications. Please refresh.')}</div>
  {:else if !detailOnly && inbox.isPending}<p class="p-8 text-center text-muted-foreground">{L('正在读取通知…', 'Loading notifications…')}</p>
  {:else}
   <div class={detailOnly ? "min-w-0" : "grid gap-5 lg:grid-cols-[minmax(260px,0.85fr)_minmax(0,1.4fr)]"}>
    {#if !detailOnly}
    <section aria-label={L('任务成果列表', 'Task results')} class="min-w-0 space-y-2">
     {#each inbox.data?.items ?? [] as item (item.id)}
      <button type="button" class="w-full rounded-xl border p-4 text-left transition-colors {selected?.id === item.id ? 'border-brand bg-brand/5' : 'bg-card hover:bg-muted/30'}" onclick={() => open(item)}>
       <div class="flex gap-2"><span class="mt-1.5 h-2 w-2 shrink-0 rounded-full {item.read ? 'bg-muted' : 'bg-brand'}"></span><span class="font-medium">{item.event.title}</span></div>
       <p class="mt-2 line-clamp-2 text-sm text-muted-foreground">{item.event.summary}</p>
       <div class="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground"><span>{statusName(item.event.status)}</span><time>{new Date(item.event.created_at).toLocaleString($locale)}</time></div>
      </button>
     {:else}<div class="rounded-xl border border-dashed p-8 text-center"><Bell class="mx-auto mb-3 h-7 w-7 text-muted-foreground" /><p>{L('暂无任务通知', 'No task notifications yet')}</p><p class="mt-2 text-sm text-muted-foreground">{L('订阅任务后，后续成果会出现在这里。', 'Subscribe to a task to receive its future results.')}</p><button type="button" class="mt-4 text-sm text-brand underline" onclick={() => tab = 'subscriptions'}>{L('查看可订阅任务', 'Browse subscriptions')}</button></div>{/each}
     <div class="flex justify-between gap-3 pt-2"><Button variant="outline" disabled={offset === 0} onclick={() => offset = Math.max(0, offset - 25)}>{L('上一页', 'Previous')}</Button><Button variant="outline" disabled={offset + 25 >= (inbox.data?.total ?? 0)} onclick={() => offset += 25}>{L('下一页', 'Next')}</Button></div>
    </section>
    {/if}
    <article class="min-w-0 rounded-xl border bg-card p-5 md:p-6">
     {#if selected}
      <div class="flex items-start justify-between gap-3"><div><p class="text-xs text-muted-foreground">{selected.reason === 'subscription' ? L('来自你的订阅', 'From your subscription') : L('来自组织任务通知', 'From your organization')}</p><h2 class="mt-2 text-lg font-semibold">{selected.event.title}</h2></div><span class="rounded-full bg-muted px-2 py-1 text-xs">{statusName(selected.event.status)}</span></div>
      <p class="mt-2 text-xs text-muted-foreground">{new Date(selected.event.created_at).toLocaleString($locale)}</p>
      <p class="mt-2 text-xs text-muted-foreground">{L('报告作者：', 'Report author: ')}{selected.event.agent_id}</p>
      {#if !selected.read}<button type="button" disabled={busy} class="mt-3 text-sm text-brand underline" onclick={() => openID(selected!.id)}>{L('重试标为已读', 'Retry marking as read')}</button>{/if}
      <div class="mt-5 whitespace-pre-wrap break-words text-sm leading-7">{selected.event.summary}</div>
      {#if selected.event.artifacts.length}<div class="mt-6 space-y-2 border-t pt-4"><h3 class="text-sm font-medium">{L('完整报告', 'Reports')}</h3>{#each selected.event.artifacts as a}<button type="button" class="flex w-full items-center gap-3 rounded-lg border p-3 text-left text-sm hover:bg-muted" disabled={busy} onclick={() => download(a.id, a.name)}><FileText class="h-4 w-4 shrink-0 text-brand" /><span class="min-w-0 flex-1 break-all">{a.name}</span><ArrowDownToLine class="h-4 w-4 shrink-0" /></button>{/each}</div>{/if}
      <div class="mt-5 border-t pt-4 text-xs text-muted-foreground"><p>{selected.read ? L('站内已读', 'Read in app') : L('站内未读', 'Unread in app')}</p>{#each selected.pushes as p}<p class="mt-2">{channelName(p.channel)} · {statusName(p.status)} {p.error ?? ''}</p>{:else}<p class="mt-2">{L('本条仅站内接收。可在订阅或接收偏好中开启外部推送。', 'In-app only. Enable external channels in your subscription or preferences.')}</p>{/each}</div>
      {#if canManage}<button type="button" class="mt-5 text-sm text-brand underline" onclick={() => act(async () => { receipts = await notificationApi.receipts(selected!.event.id, options) })}>{L('查看逐人投递状态', 'View delivery receipts')}</button>{/if}
      {#if receipts}<div class="mt-4 space-y-3 border-t pt-3">{#each receipts as receipt}<div class="text-sm"><span class="font-medium">{receipt.name || receipt.human_id}</span><span class="ml-2 text-muted-foreground">{receipt.read ? L('站内已读', 'Read in app') : L('站内未读', 'Unread')}</span>{#each receipt.pushes as p}<p class="mt-1 text-xs text-muted-foreground">{channelName(p.channel)} · {statusName(p.status)}</p>{/each}</div>{/each}</div>{/if}
     {:else}<div class="py-16 text-center text-sm text-muted-foreground">{L('选择一条通知，阅读成果和报告。', 'Select a notification to read its results.')}</div>{/if}
    </article>
   </div>
  {/if}
 {:else if tab === 'subscriptions'}
  <div class="flex flex-wrap items-center justify-between gap-3"><p class="text-sm text-muted-foreground">{L('订阅只影响未来的任务成果，不会重新运行任务。', 'Subscriptions apply to future results and never rerun a task.')}</p>{#if taskFilter}<a class="text-sm text-brand underline" href="/work-items?view=settings&tab=subscriptions">{L('查看全部', 'Show all')}</a>{/if}</div>
  {#if sources.isError}<p role="alert">{L('订阅加载失败，请刷新。', 'Could not load subscriptions.')}</p>{/if}
  <div class="grid gap-4 md:grid-cols-2">
   {#each shownSources as source (source.id)}
    {@const sub = draft(source)}
    <section class="rounded-xl border bg-card p-5">
     <div class="flex items-start justify-between gap-3"><h2 class="font-semibold">{source.title}</h2><span class="shrink-0 rounded-full bg-muted px-2 py-1 text-xs">{source.subscription?.enabled ? L('已订阅', 'Subscribed') : source.audience === 'all' ? L('组织全员通知', 'Organization notice') : L('可订阅', 'Available')}</span></div>
     <label class="mt-5 flex items-center gap-2 text-sm"><input type="checkbox" checked={sub.enabled} onchange={e => updateDraft(source, { enabled: e.currentTarget.checked })} />{L('订阅这个任务', 'Subscribe to this task')}</label>
     <fieldset class="mt-4 space-y-3" disabled={!sub.enabled}><legend class="mb-2 text-sm text-muted-foreground">{L('站内接收，并额外推送到', 'Receive in app, and optionally via')}</legend>
      {#each ['weixin', 'feishu'] as c}<label class="flex items-center gap-2 text-sm"><input type="checkbox" checked={sub.channels.includes(c as NotificationChannel)} onchange={e => updateDraft(source, { channels: toggleChannel(sub.channels, c as NotificationChannel, e.currentTarget.checked) })} />{channelName(c)}<span class="text-xs text-muted-foreground">{bindingName(c as NotificationChannel)}</span></label>{/each}
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" checked={sub.include_failures} onchange={e => updateDraft(source, { include_failures: e.currentTarget.checked })} />{L('接收含 Agent 公开报告的失败结果', 'Include failed results with a public Agent report')}</label>
     </fieldset>
     <Button class="mt-5" disabled={busy} onclick={() => act(async () => { await notificationApi.subscribe(source.id, draft(source), options); delete drafts[source.id] }, L('订阅设置已保存', 'Subscription saved'))}>{L('保存订阅', 'Save subscription')}</Button>
    </section>
   {:else}<div class="rounded-xl border border-dashed p-8 text-sm text-muted-foreground">{sources.isPending ? L('正在读取…', 'Loading…') : L('暂无开放订阅的任务。负责人可在任务通知设置中开启。', 'No tasks are open for subscription. A task owner can enable one in task notification settings.')}{#if canManage}<button type="button" class="mt-3 block text-brand underline" onclick={() => tab = 'manage'}>{L('设置任务通知', 'Configure task notifications')}</button>{/if}</div>{/each}
  </div>
 {:else if tab === 'preferences'}
  <section class="max-w-2xl rounded-xl border bg-card p-6"><h2 class="font-semibold">{L('组织任务通知的接收方式', 'How to receive organization task results')}</h2><p class="mt-2 text-sm text-muted-foreground">{L('站内通知始终保留。每个个人订阅可以单独选择渠道。', 'Your in-app inbox is always available. Individual subscriptions may choose their own channels.')}</p>
   {#if preferences.isError}<p role="alert" class="mt-4 text-red-600">{L('偏好加载失败，请刷新后再保存。', 'Could not load preferences. Refresh before saving.')}</p>{/if}
   <div class="my-6 space-y-4">{#each ['weixin', 'feishu'] as c}<label class="flex items-center gap-3"><input type="checkbox" disabled={!preferencesLoaded || preferences.isError || busy} checked={preferenceChannels.includes(c as NotificationChannel)} onchange={e => preferenceChannels = toggleChannel(preferenceChannels, c as NotificationChannel, e.currentTarget.checked)} />{channelName(c)}<span class="text-xs text-muted-foreground">{bindingName(c as NotificationChannel)}</span></label>{/each}</div>
   <div class="flex items-center gap-4"><Button disabled={busy || !preferencesLoaded || preferences.isError} onclick={() => act(async () => { await notificationApi.savePreferences(preferenceChannels, options) }, L('接收偏好已保存', 'Preferences saved'))}>{L('保存偏好', 'Save preferences')}</Button><a class="text-sm text-brand underline" href="/profile">{L('管理微信 / 飞书绑定', 'Manage channel bindings')}</a></div>
  </section>
 {:else if tab === 'manage' && canManage}
  {#if members.isPending || candidates.isPending || sources.isPending}<p role="status" class="text-sm text-muted-foreground">{L('正在读取任务和成员…', 'Loading tasks and members…')}</p>{/if}
  {#if sources.isError || candidates.isError || members.isError}<p role="alert" class="rounded-lg border border-destructive p-3 text-sm">{L('任务或成员加载失败，请刷新后再保存规则。', 'Could not load tasks or members. Refresh before saving a rule.')}</p>{/if}
  <div class="grid gap-5 lg:grid-cols-[minmax(250px,0.8fr)_minmax(0,1.5fr)]">
   <section class="space-y-3"><h2 class="flex items-center gap-2 font-medium"><Settings2 class="h-4 w-4" />{L('已配置任务', 'Configured tasks')}</h2>
    {#each sources.data ?? [] as source}<button type="button" class="w-full rounded-xl border bg-card p-4 text-left hover:bg-muted" onclick={() => edit(source)}><span class="block font-medium">{source.title}</span><span class="mt-2 block text-xs text-muted-foreground">{source.enabled ? L('已启用', 'Enabled') : L('已停用', 'Disabled')} · {source.audience === 'all' ? L('组织全员', 'Organization') : source.audience === 'people' ? L('指定人员', 'Selected people') : L('仅订阅者', 'Subscribers')}</span></button>{/each}
    <label class="block pt-3 text-sm">{L('为其他任务开启通知', 'Enable another task')}<select class="mt-2 w-full rounded-lg border bg-background p-2" value="" onchange={e => { const task = candidates.data?.find(c => c.id === e.currentTarget.value); if (task) edit(task, true) }}><option value="">{L('选择任务…', 'Choose a task…')}</option>{#each (candidates.data ?? []).filter(c => !(sources.data ?? []).some(s => s.id === c.id) && (!taskFilter || c.task_id === taskFilter)) as c}<option value={c.id}>{c.title}</option>{/each}</select></label>
   </section>
   <section class="min-w-0 rounded-xl border bg-card p-6">
    {#if rule}
     <h2 class="text-lg font-semibold">{rule.title}</h2><p class="mt-2 text-sm text-muted-foreground">{L('配置成果的站内接收范围，外部推送遵循每个人的选择。', 'Choose the in-app audience. External delivery follows each person’s preferences.')}</p>
     <label class="mt-5 flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={rule.enabled} />{L('启用后续任务通知', 'Enable notifications for future runs')}</label>
     <fieldset class="mt-5 space-y-3"><legend class="mb-3 text-sm font-medium">{L('谁会收到成果', 'Who receives results')}</legend>{#each [['subscribers', L('仅订阅者', 'Subscribers only')], ['people', L('指定人员', 'Selected people')], ['all', L('组织全员', 'Everyone in the organization')]] as [value, label]}<label class="flex items-center gap-2 text-sm"><input type="radio" name="audience" value={value} bind:group={rule.audience} />{label}</label>{/each}</fieldset>
     {#if rule.audience === 'people'}<div class="mt-4 max-h-64 space-y-2 overflow-auto rounded-lg border p-3">{#each members.data ?? [] as m}<label class="flex items-center gap-2 text-sm"><input type="checkbox" checked={rule.people.includes(m.id)} onchange={e => { if (rule) rule.people = e.currentTarget.checked ? [...rule.people, m.id] : rule.people.filter(id => id !== m.id) }} />{m.name || m.id}</label>{/each}</div>{/if}
     {#if rule.audience !== 'subscribers'}<p class="mt-4 flex items-center gap-2 text-sm text-muted-foreground"><Users class="h-4 w-4" />{L('预计站内接收：', 'Expected recipients: ')}{rule.audience === 'all' ? members.data?.length ?? '…' : rule.people.length}{L(' 人', '')}</p>{/if}
     <label class="mt-5 flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={rule.include_failures} />{L('向此范围发送含 Agent 公开报告的失败结果', 'Include failed results with a public Agent report for this audience')}</label>
     <p class="mt-4 text-xs leading-6 text-muted-foreground">{L('启用后向以上成员分享该任务的 Agent 公开报告。没有公开报告的运行不会发出通知。保存不会补发历史结果。', 'Enabling shares public Agent reports with these members. Runs without a public report do not produce notifications. Saving does not resend historical results.')}</p>
     <div class="mt-5 flex flex-wrap items-center gap-3"><Button disabled={busy || members.isError || members.isPending || sources.isError || candidates.isError} onclick={saveRule}>{L('保存通知规则', 'Save notification rule')}</Button>{#if !newRule}<Button variant="outline" disabled={busy} onclick={() => act(async () => { const p = await notificationApi.preview(rule!.id, options); preview = L('已保存规则的接收人数：', 'Saved rule recipients: ') + p.total })}>{L('核对已保存接收范围', 'Check saved audience')}</Button><Button variant="outline" disabled={busy} onclick={() => act(async () => { const saved = await notificationApi.source(rule!.id, options); edit(saved) })}>{L('丢弃修改并重载规则', 'Discard changes and reload rule')}</Button>{/if}</div>
     {#if preview}<p class="mt-3 text-sm text-muted-foreground">{preview}</p>{/if}
     {#if !newRule}<section class="mt-6 border-t pt-5"><h3 class="text-sm font-semibold">{L('最近的通知与投递记录', 'Recent notifications and delivery receipts')}</h3>
      {#if events.isError}<p role="alert" class="mt-3 text-sm text-destructive">{L('投递记录加载失败，请刷新重试。', 'Delivery history could not be loaded. Please refresh.')}</p>
      {:else}{#each events.data ?? [] as event}<button type="button" class="mt-3 block w-full rounded-lg border p-3 text-left text-sm hover:bg-muted" onclick={() => act(async () => { receipts = await notificationApi.receipts(event.id, options) })}><span class="block">{new Date(event.created_at).toLocaleString($locale)} · {statusName(event.status)}</span><span class="mt-1 block text-xs text-muted-foreground">{L('查看接收人员与投递状态', 'View recipients and delivery status')}</span></button>{:else}<p class="mt-3 text-sm text-muted-foreground">{events.isPending ? L('正在读取…', 'Loading…') : L('尚未产生新通知；规则只影响后续执行。', 'No notifications yet. Rules apply to future runs.')}</p>{/each}{/if}
      {#if receipts}<div class="mt-4 space-y-3 rounded-lg bg-muted p-3">{#each receipts as receipt}<div class="text-sm"><span class="font-medium">{receipt.name || receipt.human_id}</span><span class="ml-2 text-muted-foreground">{receipt.read ? L('站内已读', 'Read in app') : L('站内未读', 'Unread')}</span>{#each receipt.pushes as p}<p class="mt-1 text-xs text-muted-foreground">{channelName(p.channel)} · {statusName(p.status)} {p.error ?? ''}</p>{/each}</div>{:else}<p class="text-sm text-muted-foreground">{L('本次执行没有符合接收条件的人员。', 'No eligible recipients for this run.')}</p>{/each}</div>{/if}
     </section>{/if}
    {:else}<p class="py-12 text-center text-sm text-muted-foreground">{L('选择一个任务，配置通知范围。', 'Choose a task to configure its notifications.')}</p>{/if}
   </section>
  </div>
 {/if}
</div>
