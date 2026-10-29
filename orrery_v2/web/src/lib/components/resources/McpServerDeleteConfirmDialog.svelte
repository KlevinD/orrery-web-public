<script lang="ts">
  import { AlertTriangle, Trash2 } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { McpServerViewModel } from '#lib/types/view-models'

  interface Props {
    open?: boolean
    server?: McpServerViewModel
    busy?: boolean
    onconfirm?: (server: McpServerViewModel) => void
    oncancel?: () => void
  }

  let {
    open = false,
    server = undefined,
    busy = false,
    onconfirm,
    oncancel
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  const bindings = $derived(server?.bindings ?? [])
  const bindingCount = $derived(bindings.length > 0 ? bindings.length : (server?.boundAgentCount ?? 0))
</script>

<Dialog open={open} title={t('resources.mcp.deleteConfirmTitle')} onClose={oncancel}>
  {#if server}
    <div class="space-y-4">
      <div class="flex items-start gap-3 rounded-md border border-destructive/25 bg-destructive/10 px-3 py-2 text-sm">
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <p class="text-foreground">
          {t('resources.mcp.deleteConfirmDescription', { name: server.name, count: bindingCount })}
        </p>
      </div>

      {#if bindings.length > 0}
        <div class="rounded-md border border-border bg-muted/40 px-3 py-2">
          <div class="text-xs font-semibold text-foreground">{t('resources.mcp.deleteImpactedAgents')}</div>
          <div class="mt-2 grid gap-2">
            {#each bindings as binding}
              <div class="flex flex-wrap items-center gap-2 rounded-md bg-background px-2 py-1.5 text-xs">
                <span class="font-medium text-foreground">{binding.label}</span>
                <span class="text-muted-foreground">{binding.scope}</span>
                {#if binding.agentAKConfigured}
                  <span class="font-mono text-foreground">{binding.agentAKMasked}</span>
                {:else}
                  <span class="text-muted-foreground">{t('resources.mcp.deleteNoAgentAK')}</span>
                {/if}
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <div class="flex justify-end gap-2">
        <Button variant="outline" disabled={busy} onclick={oncancel}>
          {t('organization.cancel')}
        </Button>
        <Button variant="destructive" disabled={busy} onclick={() => onconfirm?.(server)}>
          <Trash2 class="h-4 w-4" />
          {t('resources.mcp.confirmDelete')}
        </Button>
      </div>
    </div>
  {/if}
</Dialog>
