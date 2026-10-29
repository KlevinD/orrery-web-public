<script lang="ts">
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { EvaluationItemViewModel } from '#lib/types/view-models'

  interface Props {
    items: EvaluationItemViewModel[]
  }

  let { items }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

{#if items.length === 0}
  <EmptyState title={t('operations.evaluation.empty')} description={t('operations.evaluation.empty.description')} />
{:else}
  <div class="grid gap-3 xl:grid-cols-2">
    {#each items as item (item.id)}
      <article class="panel p-4">
        <h2 class="text-base font-semibold text-foreground">{item.displayName}</h2>
        <div class="mt-1 text-xs text-muted-foreground">{item.entityType} · {item.missionId}</div>
        <div class="mt-4 grid grid-cols-2 gap-3 text-xs text-muted-foreground md:grid-cols-4">
          <div><span class="text-muted-foreground">{t('operations.evaluation.delivery')}</span><div class="mt-1 text-foreground">{item.metrics.delivery}%</div></div>
          <div><span class="text-muted-foreground">{t('operations.evaluation.quality')}</span><div class="mt-1 text-foreground">{item.metrics.quality}%</div></div>
          <div><span class="text-muted-foreground">{t('operations.evaluation.onTime')}</span><div class="mt-1 text-foreground">{item.metrics.onTime}%</div></div>
          <div><span class="text-muted-foreground">{t('operations.evaluation.collab')}</span><div class="mt-1 text-foreground">{item.metrics.collaboration}%</div></div>
        </div>
      </article>
    {/each}
  </div>
{/if}
