<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { Link2, ShieldCheck } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { AuditEventViewModel } from '#lib/types/view-models'

  interface Props {
    events: AuditEventViewModel[]
  }

  let { events }: Props = $props()
  let t = $derived(createTranslator($locale))
  let selectedEventId = $state('')
  const selectedEvent = $derived(events.find((event) => event.id === selectedEventId) ?? events[0])
</script>

{#if events.length === 0}
  <EmptyState title={t('operations.audit.empty')} description={t('operations.audit.empty.description')} />
{:else}
  <div class="grid min-h-0 gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
    <div class="min-h-0 overflow-hidden rounded-xl border border-border bg-card">
      <div class="hidden grid-cols-[minmax(0,1fr)_110px_150px_70px] gap-3 border-b border-border px-4 py-2 text-[11px] font-medium uppercase text-muted-foreground md:grid">
        <span>{t('filter.action')} / {t('operations.audit.entity')}</span>
        <span>{t('operations.audit.actor')}</span>
        <span>{t('workspace.updatedAt')}</span>
        <span>{t('operations.audit.result')}</span>
      </div>
      <div class="max-h-[30rem] divide-y divide-border/70 overflow-y-auto overscroll-contain">
        {#each events as event (event.id)}
          <button
            type="button"
            class="grid w-full gap-2 px-4 py-3 text-left transition-colors md:grid-cols-[minmax(0,1fr)_110px_150px_70px] md:items-center md:gap-3 {selectedEvent?.id === event.id ? 'bg-muted/60' : 'hover:bg-muted/30'}"
            onclick={() => (selectedEventId = event.id)}
          >
            <span class="flex min-w-0 items-center gap-2.5">
              <span class="grid h-8 w-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground"><ShieldCheck class="h-4 w-4" /></span>
              <span class="min-w-0"><span class="block truncate text-[13px] text-foreground">{event.action}</span><span class="mt-0.5 block truncate text-[11px] text-muted-foreground">{event.entity}</span></span>
            </span>
            <span class="truncate text-[12.5px] text-muted-foreground">{event.actorId}</span>
            <span class="truncate font-mono text-[11px] text-muted-foreground">{event.occurredAt}</span>
            <span><Badge tone={event.severityTone}>{event.result}</Badge></span>
          </button>
        {/each}
      </div>
    </div>

    {#if selectedEvent}
      <aside class="min-h-0 self-start overflow-y-auto rounded-xl border border-border bg-card p-4">
        <h2 class="text-[15px] font-medium text-foreground">{selectedEvent.action}</h2>
        <p class="mt-1 font-mono text-[11px] text-muted-foreground">{selectedEvent.occurredAt}</p>
        <p class="mt-4 border-t border-border pt-4 text-[12.5px] leading-5 text-muted-foreground">{selectedEvent.summary}</p>
        <dl class="mt-4 space-y-3 text-[12.5px]">
          <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">{t('operations.audit.actor')}</dt><dd class="text-right text-foreground">{selectedEvent.actorId}</dd></div>
          <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">{t('operations.audit.result')}</dt><dd class="text-right text-foreground">{selectedEvent.result}</dd></div>
          <div><dt class="text-muted-foreground">{t('operations.audit.entity')}</dt><dd class="mt-1 inline-flex items-center gap-1.5 rounded-md bg-muted px-2.5 py-1.5 text-foreground"><Link2 class="h-3.5 w-3.5 text-muted-foreground" />{selectedEvent.entity}</dd></div>
          <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">{t('operations.audit.mission')}</dt><dd class="max-w-44 break-all text-right font-mono text-[11px] text-foreground">{selectedEvent.missionId}</dd></div>
          <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">{t('operations.audit.correlation')}</dt><dd class="max-w-44 break-all text-right font-mono text-[11px] text-foreground">{selectedEvent.correlationId}</dd></div>
        </dl>
        {#if selectedEvent.metadataPairs.length > 0}
          <div class="mt-4 border-t border-border pt-3">
            {#each selectedEvent.metadataPairs as pair}
              <div class="flex items-start justify-between gap-3 py-1.5 text-[11.5px]"><span class="text-muted-foreground">{pair.key}</span><span class="max-w-44 break-all text-right font-mono text-foreground">{pair.value}</span></div>
            {/each}
          </div>
        {/if}
      </aside>
    {/if}
  </div>
{/if}
