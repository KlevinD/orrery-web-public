<script lang="ts">
  import { Bot, MessageSquare } from 'lucide-svelte'
  import type { ConversationSwitchItem } from '#lib/conversation-switch-context.svelte'
  import { createTranslator, locale } from '#lib/i18n'

  interface Props {
    conversations: ConversationSwitchItem[]
    selectedConversationId: string
    embedded?: boolean
    onSelect?: (conversation: ConversationSwitchItem) => void
  }

  let { conversations, selectedConversationId, embedded = false, onSelect }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<section
  class={embedded
    ? 'flex h-full min-h-0 w-full flex-col bg-sidebar text-sidebar-foreground'
    : 'right-sidebar-shell right-sidebar-rail hidden xl:flex'}
  aria-label={t('context.conversations')}
  data-conversation-switch-panel
>
  {#if embedded}
    <div class="flex h-9 shrink-0 items-center justify-between px-4">
      <span class="text-[11px] font-medium text-muted-foreground">{t('discussionSession.recentConversations')}</span>
      <span class="text-[10px] tabular-nums text-muted-foreground">{conversations.length}</span>
    </div>
  {:else}
    <header class="right-sidebar-header">
      <span class="text-[13px] font-medium">{t('discussionSession.recentConversations')}</span>
      <span class="text-[10px] tabular-nums text-muted-foreground">{conversations.length}</span>
    </header>
  {/if}

  <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar p-2" data-conversation-switch-list>
    {#each conversations as conversation (conversation.id)}
      {@const selected = selectedConversationId === conversation.id}
      <button
        type="button"
        class="relative flex min-h-[54px] w-full min-w-0 items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors {selected ? 'selection-wash text-sidebar-accent-foreground' : 'hover:bg-sidebar-accent'}"
        aria-label={`${t('context.conversations')}: ${conversation.title}`}
        aria-pressed={selected}
        data-conversation-switch={conversation.id}
        onclick={() => onSelect?.(conversation)}
      >
        <span class="grid h-8 w-8 shrink-0 place-items-center rounded-full border border-sidebar-border bg-background text-muted-foreground">
          <Bot class="h-3.5 w-3.5" />
        </span>

        <span class="min-w-0 flex-1">
          <span class="block truncate text-[12px] font-medium">{conversation.title}</span>
          <span class="mt-1 block truncate text-[10.5px] text-muted-foreground">{conversation.agentName}</span>
        </span>
      </button>
    {:else}
      <div class="grid min-h-40 place-items-center px-6 text-center">
        <div>
          <MessageSquare class="mx-auto h-5 w-5 text-muted-foreground" />
          <p class="mt-2 text-[11px] text-muted-foreground">{t('discussionSession.empty')}</p>
        </div>
      </div>
    {/each}
  </div>
</section>
