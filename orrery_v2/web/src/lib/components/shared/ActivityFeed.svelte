<script lang="ts">
  import { ArrowRight, AtSign, CheckCircle2, MessageSquare, Wrench } from 'lucide-svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'

  export interface ActivityFeedItem {
    id: string
    type?: string
    label: string
    author: string
    authorType?: string
    body: string
    time: string
    metadata?: {
      mentionedUsers?: string[]
      toolName?: string
      oldStatus?: string
      newStatus?: string
    }
  }

  interface Props {
    items: ActivityFeedItem[]
    emptyLabel: string
    class?: string
  }

  let { items, emptyLabel, class: className = '' }: Props = $props()
  let t = $derived(createTranslator($locale))

  function normalizeType(type: string | undefined) {
    return (type || 'message').replaceAll('_', '-')
  }

  function eventIcon(type: string | undefined) {
    const normalized = normalizeType(type)
    if (normalized.includes('mention')) return AtSign
    if (normalized.includes('tool')) return Wrench
    if (normalized.includes('status')) return ArrowRight
    if (normalized.includes('milestone')) return CheckCircle2
    return MessageSquare
  }

  function eventColor(type: string | undefined) {
    const normalized = normalizeType(type)
    if (normalized.includes('mention')) return 'text-chart-1'
    if (normalized.includes('tool')) return 'text-chart-4'
    if (normalized.includes('status')) return 'text-chart-3'
    if (normalized.includes('milestone')) return 'text-status-done'
    return 'text-muted-foreground'
  }

  function eventDotBg(type: string | undefined) {
    const normalized = normalizeType(type)
    if (normalized.includes('mention')) return 'bg-chart-1'
    if (normalized.includes('tool')) return 'bg-chart-4'
    if (normalized.includes('status')) return 'bg-chart-3'
    if (normalized.includes('milestone')) return 'bg-status-done'
    return 'bg-muted-foreground/50'
  }

  function statusLabel(status: string) {
    const key = `status.${status}` as TranslationKey
    const label = t(key)
    return label === key ? status : label
  }
</script>

{#if items.length > 0}
  <div class="relative {className}">
    <!-- Timeline spine -->
    <div class="pointer-events-none absolute bottom-4 left-[18px] top-4 w-px bg-border"></div>

    <div class="space-y-1">
      {#each items as item, i}
        {@const type = normalizeType(item.type)}
        {@const Icon = eventIcon(item.type)}
        {@const dotBg = eventDotBg(item.type)}
        {@const isLast = i === items.length - 1}

        <article class="group relative flex items-start gap-3 rounded-lg px-2 py-2.5 transition-colors hover:bg-muted/40">
          <!-- Timeline dot -->
          <div class="relative z-10 mt-1 flex h-[36px] shrink-0 items-start justify-center">
            <div class="h-2.5 w-2.5 rounded-full {dotBg} ring-2 ring-card"></div>
          </div>

          <!-- Content -->
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium text-foreground">{item.author}</span>
              <Icon class="h-3 w-3 {eventColor(item.type)}" data-event-icon={type} />
              <span class="text-xs font-medium {eventColor(item.type)}">{item.label}</span>
              <span class="ml-auto shrink-0 text-[10px] text-muted-foreground">{item.time}</span>
            </div>
            <p class="mt-0.5 text-sm leading-relaxed text-muted-foreground">{item.body}</p>

            {#if item.metadata?.toolName || (item.metadata?.oldStatus && item.metadata?.newStatus)}
              <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
                {#if item.metadata?.toolName}
                  <span class="inline-flex items-center gap-1 rounded-md bg-chart-4/10 px-2 py-0.5 text-[11px] font-medium text-chart-4">
                    <Wrench size={10} />
                    {item.metadata.toolName}
                  </span>
                {/if}
                {#if item.metadata?.oldStatus && item.metadata?.newStatus}
                  <span class="inline-flex items-center gap-1 text-[11px] text-muted-foreground">
                    <span
                      class="inline-flex items-center rounded-md bg-muted px-1.5 py-0.5"
                    >
                      {statusLabel(item.metadata.oldStatus)}
                    </span>
                    <ArrowRight size={10} class="text-muted-foreground/60" />
                    <span
                      class="inline-flex items-center rounded-md bg-muted px-1.5 py-0.5"
                    >
                      {statusLabel(item.metadata.newStatus)}
                    </span>
                  </span>
                {/if}
              </div>
            {/if}

            {#if item.metadata?.mentionedUsers?.length}
              <div class="mt-1.5 flex flex-wrap gap-1">
                {#each item.metadata.mentionedUsers as user}
                  <span class="inline-flex items-center gap-0.5 rounded-md bg-chart-1/10 px-1.5 py-0.5 text-[11px] font-medium text-chart-1">
                    <AtSign size={9} />
                    {user}
                  </span>
                {/each}
              </div>
            {/if}
          </div>
        </article>
      {/each}
    </div>
  </div>
{:else}
  <div class="rounded-xl border border-border bg-card p-8 text-center text-sm text-muted-foreground">
    {emptyLabel}
  </div>
{/if}
