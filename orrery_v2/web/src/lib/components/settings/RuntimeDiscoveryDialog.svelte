<script lang="ts">
  import { Save } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { ApiClientError } from '#lib/api/client'
  import type { RuntimeDiscoveryCandidateViewModel } from '#lib/types/view-models'

  interface Props {
    open?: boolean
    candidates?: RuntimeDiscoveryCandidateViewModel[]
    fetching?: boolean
    error?: ApiClientError | null
    busy?: boolean
    selectedKeys: Record<string, boolean>
    discoveryKey: (candidate: RuntimeDiscoveryCandidateViewModel) => string
    selectedCount: () => number
    onclose?: () => void
    ontoggleall?: (checked: boolean) => void
    onselectedchange?: (key: string, checked: boolean) => void
    onapply?: () => void
  }

  let {
    open = false,
    candidates = [],
    fetching = false,
    error = null,
    busy = false,
    selectedKeys = $bindable(),
    discoveryKey,
    selectedCount,
    onclose,
    ontoggleall,
    onselectedchange,
    onapply
  }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<Dialog {open} title={t('settings.runtimeDiscovery')} size="wide" onClose={onclose}>
  <div class="space-y-4">
    {#if fetching}
      <LoadingState title={t('settings.runtimeDiscovery')} description={t('settings.runtimeDiscovery.desc')} />
    {:else if error}
      <ErrorState title={t('settings.runtimeDiscovery')} message={error.message} correlationId={error.correlationId} />
    {:else if candidates.length === 0}
      <div class="rounded-lg border border-dashed border-border bg-muted/20 px-4 py-8 text-center text-sm text-muted-foreground">
        {t('settings.runtimeDiscovery.empty')}
      </div>
    {:else}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/20 px-4 py-3">
        <label class="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <input
            class="h-4 w-4 rounded border-border"
            type="checkbox"
            checked={selectedCount() > 0 && selectedCount() === candidates.length}
            onchange={(event) => ontoggleall?.(event.currentTarget.checked)}
          />
          {t('settings.selectAllDiscovered')}
        </label>
        <Button disabled={busy || selectedCount() === 0} onclick={onapply}>
          <Save class="h-4 w-4" />{t('settings.registerSelected', { count: String(selectedCount()) })}
        </Button>
      </div>
      <div class="grid gap-3 lg:grid-cols-2">
        {#each candidates as candidate (discoveryKey(candidate))}
          {@const candidateKey = discoveryKey(candidate)}
          <label class="block cursor-pointer rounded-xl border border-border bg-card p-4 text-left transition-colors hover:border-brand/50 hover:bg-brand/5">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <input
                    class="h-4 w-4 rounded border-border"
                    type="checkbox"
                    checked={Boolean(selectedKeys[candidateKey])}
                    onchange={(event) => onselectedchange?.(candidateKey, event.currentTarget.checked)}
                  />
                  <div class="text-sm font-semibold text-foreground">{candidate.label}</div>
                </div>
                <div class="mt-1 truncate font-mono text-[11px] text-muted-foreground">{candidate.executable}</div>
              </div>
              <span class="rounded-md bg-muted px-2 py-1 text-[11px] font-medium text-muted-foreground">{candidate.status}</span>
            </div>
            <div class="mt-3 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
              <div>
                <span class="font-medium text-foreground">{t('settings.source')}</span>
                <div class="mt-0.5 font-mono">{candidate.source}</div>
              </div>
              <div>
                <span class="font-medium text-foreground">{t('settings.configRef')}</span>
                <div class="mt-0.5 font-mono">{candidate.configRef}</div>
              </div>
              {#if candidate.version}
                <div class="sm:col-span-2">
                  <span class="font-medium text-foreground">{t('settings.version')}</span>
                  <div class="mt-0.5 font-mono">{candidate.version}</div>
                </div>
              {/if}
            </div>
            {#if candidate.notes.length > 0}
              <div class="mt-3 rounded-md bg-muted/60 px-3 py-2 text-xs text-muted-foreground">
                {candidate.notes.join('; ')}
              </div>
            {/if}
          </label>
        {/each}
      </div>
    {/if}
  </div>
</Dialog>
