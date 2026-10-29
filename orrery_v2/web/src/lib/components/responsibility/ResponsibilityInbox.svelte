<script lang="ts">
  import { AlertTriangle, Bot, ChevronRight, Clock3, ShieldCheck } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { responsibilityHealthTone } from '#lib/responsibility/view-model'
  import type { ResponsibilityInboxItemDTO } from '#lib/types/responsibility'

  interface Props {
    items: ResponsibilityInboxItemDTO[]
    selectedId?: string
    onSelect: (item: ResponsibilityInboxItemDTO) => void
  }

  let { items, selectedId = '', onSelect }: Props = $props()
  let t = $derived(createTranslator($locale))

  function healthLabel(item: ResponsibilityInboxItemDTO) {
    return t(`responsibility.health.${item.health}`)
  }

  function formatDate(value?: string) {
    if (!value) return ''
    const date = new Date(value)
    if (Number.isNaN(date.valueOf())) return value
    return new Intl.DateTimeFormat($locale, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(date)
  }
</script>

<div class="divide-y divide-border" data-responsibility-inbox>
  {#each items as item (item.responsibility_id)}
    <button
      type="button"
      class="grid w-full grid-cols-[minmax(0,1fr)_auto] gap-3 px-4 py-3.5 text-left transition-colors hover:bg-muted/45 {selectedId === item.responsibility_id ? 'bg-brand/5 shadow-[inset_3px_0_0_var(--color-brand)]' : 'bg-card'}"
      aria-pressed={selectedId === item.responsibility_id}
      data-lane={item.lane}
      data-health={item.health}
      onclick={() => onSelect(item)}
    >
      <span class="min-w-0">
        <span class="flex min-w-0 items-center gap-2">
          <span class="grid h-7 w-7 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
            {#if item.health === 'critical'}
              <AlertTriangle class="h-3.5 w-3.5 text-destructive" />
            {:else if item.health === 'at_risk'}
              <Clock3 class="h-3.5 w-3.5 text-status-blocked" />
            {:else}
              <ShieldCheck class="h-3.5 w-3.5 text-status-done" />
            {/if}
          </span>
          <span class="min-w-0 truncate text-sm font-semibold text-foreground">{item.title || item.goal}</span>
        </span>
        <span class="mt-2 block line-clamp-2 text-xs leading-5 text-muted-foreground">{item.goal}</span>
        <span class="mt-2 flex flex-wrap items-center gap-1.5">
          <Badge tone={responsibilityHealthTone(item.health)}>{healthLabel(item)}</Badge>
          <Badge tone="neutral">{item.state}</Badge>
          {#if item.paused}<Badge tone="warning">paused</Badge>{/if}
          {#each item.risk_reasons.slice(0, 2) as reason}
            <Badge tone="danger">{reason}</Badge>
          {/each}
        </span>
        <span class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
          <span class="inline-flex min-w-0 items-center gap-1"><Bot class="h-3 w-3" /><span class="truncate">{item.accountable_agent}</span></span>
          <span>{t('responsibility.priority', { value: item.priority })}</span>
          <span>{t('responsibility.attemptsCount', { count: item.attempt_count })}</span>
          {#if item.due_at}<span>{t('responsibility.due', { time: formatDate(item.due_at) })}</span>{/if}
        </span>
      </span>
      <span class="flex items-center"><ChevronRight class="h-4 w-4 text-muted-foreground" /></span>
    </button>
  {/each}
</div>
