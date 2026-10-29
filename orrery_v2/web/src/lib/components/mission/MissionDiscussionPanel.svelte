<script lang="ts">
  import { AtSign, CheckCircle2, Hash, ListChecks, MessageSquare, Send, Target, Users, Wrench, AlertCircle, ArrowRight } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { isActiveTaskStatus } from '#lib/mission/taskStatus'
  import { isCurrentSessionUserIdentity } from '#lib/stores/session.svelte'
  import type { InboxItemDTO } from '#lib/types/api'
  import type { DiscussionMessageViewModel, DiscussionViewModel, MissionControlViewModel, MissionTeamMemberViewModel } from '#lib/types/view-models'

  interface Props {
    discussion?: DiscussionViewModel
    context?: MissionControlViewModel
    inboxItems?: InboxItemDTO[]
    busy?: boolean
    replyBusy?: boolean
    onpost?: (content: string) => void
    onreply?: (messageId: string, content: string, inboxItemId?: string) => void
  }

  type DiscussionMode = 'all' | 'chat' | 'events'

  const chatTypes = ['message', 'reply', 'mention']
  const eventTypes = ['tool-call', 'tool_call', 'status-change', 'status_change', 'milestone', 'assignment']
  const modeOptions: Array<{ id: DiscussionMode; label: TranslationKey }> = [
    { id: 'all', label: 'discussion.all' },
    { id: 'chat', label: 'discussion.chat' },
    { id: 'events', label: 'discussion.events' }
  ]

  let { discussion, context, inboxItems = [], busy = false, replyBusy = false, onpost, onreply }: Props = $props()
  let content = $state('')
  let mode = $state<DiscussionMode>('all')
  let replyTo = $state<DiscussionMessageViewModel | undefined>()
  let mentionQuery = $state('')
  let messageScroller = $state<HTMLDivElement | undefined>()
  let composerInput = $state<HTMLInputElement | undefined>()
  let t = $derived(createTranslator($locale))
  let messages = $derived(discussion?.messages ?? [])
  let messageCount = $derived(messages.length)
  let filteredMessages = $derived(
    messages.filter((message) => {
      if (mode === 'chat') return chatTypes.includes(message.kind)
      if (mode === 'events') return eventTypes.includes(message.kind)
      return true
    })
  )
  let teamMembers = $derived(context?.team ?? [])
  let activeTasks = $derived((context?.tasks ?? []).filter((task) => isActiveTaskStatus(task.status) || task.status === 'blocked'))
  let blockedTasks = $derived((context?.tasks ?? []).filter((task) => task.status === 'blocked'))

  $effect(() => {
    updateMentionQuery(content)
  })

  $effect(() => {
    messageCount
    if (messageScroller) {
      messageScroller.scrollTop = messageScroller.scrollHeight
    }
  })

  function tx(key: string) {
    return t(key as TranslationKey)
  }

  function updateMentionQuery(value: string) {
    const lastAt = value.lastIndexOf('@')
    if (lastAt < 0) {
      mentionQuery = ''
      return
    }
    const afterAt = value.slice(lastAt + 1)
    mentionQuery = afterAt.length > 0 && !afterAt.includes(' ') ? afterAt : ''
  }

  function insertMention(member: MissionTeamMemberViewModel) {
    const lastAt = content.lastIndexOf('@')
    const targetKind = member.type === 'human' ? 'user' : 'agent'
    content = `${content.slice(0, lastAt)}[@${member.name}](mention://${targetKind}/${member.id}) `
    mentionQuery = ''
    composerInput?.focus()
  }

  function send() {
    const text = content.trim()
    if (!text) return
    if (replyTo) {
      onreply?.(replyTo.id, text, inboxItemIdForMessage(replyTo.id))
    } else {
      onpost?.(text)
    }
    content = ''
    replyTo = undefined
    mentionQuery = ''
  }

  function eventIcon(kind: string) {
    if (kind.includes('tool')) return Wrench
    if (kind.includes('status')) return ArrowRight
    if (kind.includes('milestone')) return CheckCircle2
    if (kind.includes('assignment')) return ListChecks
    if (kind.includes('mention')) return AtSign
    return MessageSquare
  }

  function eventLabel(kind: string) {
    const normalized = kind.replaceAll('_', '-')
    if (normalized.includes('tool')) return t('discussion.toolCall')
    if (normalized.includes('status')) return t('discussion.statusChange')
    if (normalized.includes('milestone')) return t('discussion.milestone')
    if (normalized.includes('assignment')) return t('discussion.assignment')
    if (normalized.includes('mention')) return t('discussion.mention')
    if (normalized.includes('reply')) return t('discussion.replyType')
    return t('discussion.message')
  }

  function eventClass(kind: string) {
    const normalized = kind.replaceAll('_', '-')
    if (normalized.includes('tool')) return 'text-chart-4'
    if (normalized.includes('status')) return 'text-chart-3'
    if (normalized.includes('milestone')) return 'text-status-done'
    if (normalized.includes('assignment')) return 'text-brand'
    if (normalized.includes('mention')) return 'text-chart-1'
    return 'text-muted-foreground'
  }

  function bodyParts(body: string) {
    const parts: string[] = []
    const mentionPattern = /\[@?(.+?)\]\(mention:\/\/[A-Za-z_]+\/[^)]+\)/g
    let cursor = 0
    const appendPlainText = (text: string) => {
      parts.push(...text.split(/(@\S+)/g).filter(Boolean))
    }
    for (const match of body.matchAll(mentionPattern)) {
      const index = match.index ?? 0
      appendPlainText(body.slice(cursor, index))
      parts.push(`@${match[1]}`)
      cursor = index + match[0].length
    }
    appendPlainText(body.slice(cursor))
    return parts
  }

  function isOwnMessage(message: DiscussionMessageViewModel) {
    return isCurrentSessionUserIdentity(message.authorId)
  }

  function teamMemberForIdentity(id: string) {
    return teamMembers.find((member) => [member.id, member.profileId].includes(id))
  }

  function messageProfileId(message: DiscussionMessageViewModel) {
    return teamMemberForIdentity(message.authorId)?.profileId || message.authorId
  }

  function messageIsHuman(message: DiscussionMessageViewModel) {
    const member = teamMemberForIdentity(message.authorId)
    return member ? (member.type || '').toLowerCase() === 'human' : (message.authorType || '').toLowerCase() === 'human'
  }

  function replyReferenceText(message: DiscussionMessageViewModel) {
    const author = message.replyToAuthor ? `${message.replyToAuthor}: ` : ''
    const preview = message.replyPreview ? `${message.replyPreview}...` : ''
    return `${author}${preview}`
  }

  function inboxItemIdForMessage(messageId: string) {
    return inboxItems.find((item) => item.message_id === messageId && ['open', 'pending'].includes(item.status))?.inbox_item_id
  }
</script>

<div class="-m-4 flex h-[calc(100vh-3.5rem)] overflow-hidden bg-background">
  <div class="flex min-w-0 flex-1 flex-col">
    <header class="flex items-center gap-4 border-b border-border px-6 py-3">
      <Hash class="h-4 w-4 text-muted-foreground" />
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <span class="truncate text-sm font-medium text-foreground">{context?.mission.title ?? t('nav.discussion')}</span>
          {#if context}
            <Badge>{context.mission.id}</Badge>
          {/if}
        </div>
        <p class="text-xs text-muted-foreground">{t('discussion.memberCount', { count: teamMembers.length })}</p>
      </div>
      <div class="flex rounded-lg bg-muted/80 p-0.5">
        {#each modeOptions as item}
          <button
            class="h-7 rounded-md px-3 text-xs text-muted-foreground data-[active=true]:bg-accent data-[active=true]:text-foreground"
            data-active={mode === item.id}
            type="button"
            onclick={() => (mode = item.id)}
          >
            {t(item.label)}
          </button>
        {/each}
      </div>
    </header>

    <div class="subtle-scrollbar flex-1 overflow-auto p-6" bind:this={messageScroller}>
      <div class="mx-auto max-w-3xl space-y-1">
        {#each filteredMessages as message (message.id)}
          {@const isEvent = eventTypes.includes(message.kind)}
          {@const Icon = eventIcon(message.kind)}
          {#if isEvent}
            <article class="flex items-start gap-3 py-2">
              <div class="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-muted">
                <Icon class="h-3.5 w-3.5 {eventClass(message.kind)}" />
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <span class="text-xs font-medium {eventClass(message.kind)}">{eventLabel(message.kind)}</span>
                  <span class="text-xs text-muted-foreground">{message.authorName}</span>
                  <span class="ml-auto text-[10px] text-muted-foreground">{message.postedAt}</span>
                </div>
                <p class="mt-0.5 text-sm text-muted-foreground">{message.body}</p>
              </div>
            </article>
          {:else}
            {@const isOwn = isOwnMessage(message)}
            <article
              class="group -mx-2 flex items-start gap-3 rounded-lg px-2 py-2.5 transition-colors hover:bg-muted/35"
              class:flex-row-reverse={isOwn}
              data-message-row
            >
              <WorkforceAvatar
                profileId={messageProfileId(message)}
                name={message.authorName}
                human={messageIsHuman(message)}
                class="h-8 w-8 rounded-full"
                textClass="text-[10px]"
              />
              <div class="min-w-0 flex-1" class:flex={isOwn} class:flex-col={isOwn} class:items-end={isOwn}>
                <div class="flex items-center gap-2" class:flex-row-reverse={isOwn}>
                  <span class="text-xs font-medium text-foreground">{message.authorName}</span>
                  {#if message.authorType === 'agent'}
                    <Badge>{t('workspace.agent')}</Badge>
                  {/if}
                  <span class="text-[10px] text-muted-foreground">{message.postedAt}</span>
                </div>
                {#if message.replyToMessageId || message.replyPreview}
                  <div
                    class="mt-1 max-w-[85%] rounded-r border-l-2 border-border bg-muted/50 px-2 py-1 text-xs text-muted-foreground"
                    class:ml-auto={isOwn}
                  >
                    {replyReferenceText(message)}
                  </div>
                {/if}
                <div
                  class="mt-1 max-w-[85%] rounded-xl px-3 py-2 text-sm"
                  class:bg-brand={isOwn}
                  class:text-brand-foreground={isOwn}
                  class:bg-muted={!isOwn}
                  class:text-foreground={!isOwn}
                  data-message-bubble
                >
                  {#each bodyParts(message.body) as part}
                    {#if part.startsWith('@')}
                      <span class={`font-medium ${isOwn ? 'text-brand-foreground/80' : 'text-source-agent'}`}>{part}</span>
                    {:else}
                      {part}
                    {/if}
                  {/each}
                </div>
                <div class="mt-1 flex items-center gap-2 opacity-0 transition-opacity group-hover:opacity-100">
                  <button class="text-[10px] text-muted-foreground hover:text-brand" type="button" onclick={() => (replyTo = message)}>{t('discussion.reply')}</button>
                </div>
              </div>
            </article>
          {/if}
        {/each}
      </div>
    </div>

    {#if replyTo}
      <div class="flex items-center gap-2 border-t border-border bg-muted/40 px-6 py-2 text-xs text-muted-foreground">
        <span>{t('discussion.replyingTo')} <span class="font-medium text-foreground">{replyTo.authorName}</span>: {replyTo.body.slice(0, 50)}...</span>
        <button class="ml-auto text-muted-foreground hover:text-foreground" type="button" onclick={() => (replyTo = undefined)}>{t('discussion.cancel')}</button>
      </div>
    {/if}

    {#if mentionQuery}
      <div class="px-6 pb-1">
        <div class="max-w-xs rounded-lg border border-border bg-card p-1 shadow-md">
          {#each teamMembers.filter((member) => member.name.includes(mentionQuery) || member.role.includes(mentionQuery)).slice(0, 5) as member}
            <button class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-sm text-foreground hover:bg-muted" type="button" onclick={() => insertMention(member)}>
              <WorkforceAvatar profileId={member.profileId || member.id} name={member.name} human={(member.type || '').toLowerCase() === 'human'} class="h-5 w-5 rounded-full" textClass="text-[8px]" />
              <span class="font-medium">{member.name}</span>
              <Badge>{member.type === 'agent' ? t('workspace.agent') : t('workspace.human')}</Badge>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <footer class="border-t border-border px-6 py-4">
      <form class="flex gap-2" onsubmit={(event) => { event.preventDefault(); send() }}>
        <label class="flex min-w-0 flex-1 overflow-hidden rounded-md border border-input bg-input-background">
          <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
            {t('discussion.message')}
          </span>
          <Input bind:value={content} bind:ref={composerInput} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
        </label>
        <Button disabled={busy || replyBusy || content.trim().length === 0} onclick={send}>
          <Send class="h-4 w-4" />
        </Button>
      </form>
    </footer>
  </div>

  <aside class="right-sidebar-shell right-sidebar-rail hidden md:flex">
    <header class="right-sidebar-header">
      <span class="truncate text-[13px] font-medium">{t('context.missionOverview')}</span>
    </header>
    <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar space-y-5 p-4">
      <section>
        <h2 class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Target class="h-3 w-3" />{t('workspace.goals')}</h2>
        <ul class="space-y-1.5">
          {#each context?.mission.goals.length ? context.mission.goals : [context?.mission.description ?? ''] as goal}
            <li class="flex items-start gap-2 text-xs text-foreground"><span class="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-brand"></span>{goal}</li>
          {/each}
        </ul>
      </section>

      <section class="border-t border-border pt-4">
        <h2 class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Users class="h-3 w-3" />{t('discussion.members')} ({teamMembers.length})</h2>
        <div class="space-y-1.5">
          {#each teamMembers as member}
            <div class="flex items-center gap-2">
              <WorkforceAvatar profileId={member.profileId || member.id} name={member.name} human={(member.type || '').toLowerCase() === 'human'} class="h-6 w-6 rounded-full" textClass="text-[8px]" />
              <span class="truncate text-xs font-medium text-foreground">{member.name}</span>
              <span class="ml-auto truncate text-[10px] text-muted-foreground">{member.role}</span>
            </div>
          {/each}
        </div>
      </section>

      <section class="border-t border-border pt-4">
        <h2 class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><ListChecks class="h-3 w-3" />{t('discussion.activeTasks')}</h2>
        <div class="space-y-1.5">
          {#each activeTasks as task}
            <div class="text-xs">
              <div class="flex items-center gap-1.5">
                <span class="h-1.5 w-1.5 shrink-0 rounded-full" class:bg-brand={isActiveTaskStatus(task.status)} class:bg-status-failed={task.status === 'blocked'}></span>
                <span class="truncate font-medium text-foreground">{task.title}</span>
              </div>
              <div class="ml-3 text-muted-foreground">{task.assigneeName}</div>
            </div>
          {:else}
            <p class="text-xs text-muted-foreground">{t('discussion.noActiveTasks')}</p>
          {/each}
        </div>
      </section>

      {#if blockedTasks.length > 0}
        <section class="border-t border-border pt-4">
          <h2 class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><AlertCircle class="h-3 w-3" />{t('discussion.blockers')}</h2>
          <div class="space-y-2">
            {#each blockedTasks as task}
              <div class="rounded-md bg-destructive/10 p-2 text-xs text-destructive-foreground">
                <div class="font-medium">{task.title}</div>
                <p class="mt-0.5 opacity-80">{t('workspace.assignee')}: {task.assigneeName}</p>
              </div>
            {/each}
          </div>
        </section>
      {/if}
    </div>
  </aside>
</div>
