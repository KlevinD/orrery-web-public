<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { PermissionRowViewModel } from '#lib/types/view-models'

  interface Props {
    rows: PermissionRowViewModel[]
  }

  let { rows }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

{#if rows.length === 0}
  <EmptyState title={t('operations.permissions.empty')} description={t('operations.permissions.empty.description')} />
{:else}
  <div class="grid gap-3">
    {#each rows as row (row.agentId)}
      <article class="panel p-4">
        <h2 class="text-sm font-semibold text-foreground">{row.agentId}</h2>
        <div class="mt-3 grid gap-3">
          {#each row.resources as resource (resource.toolId)}
            <div class="rounded-md border border-border p-3">
              <div class="text-sm text-foreground">{resource.toolName}</div>
              <div class="mt-2 flex flex-wrap gap-2">
                {#each resource.declarations as declaration}
                  <Badge tone={declaration.effective ? 'success' : 'danger'}>
                    {declaration.scope} · {declaration.effectiveLabel} · {declaration.source}
                  </Badge>
                  <Badge tone={declaration.riskTone}>{declaration.risk}</Badge>
                {/each}
              </div>
            </div>
          {/each}
        </div>
      </article>
    {/each}
  </div>
{/if}
