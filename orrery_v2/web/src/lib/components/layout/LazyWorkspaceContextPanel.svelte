<script lang="ts">
  import { LoaderCircle, RefreshCw, X } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'

  interface Props {
    showClose?: boolean
    onClose?: () => void
  }

  let { showClose = false, onClose }: Props = $props()
  let t = $derived(createTranslator($locale))
  let panel = $state(import('./WorkspaceContextPanel.svelte'))
</script>

{#snippet placeholder(failed: boolean)}
  <aside class="right-sidebar-shell right-sidebar-rail" aria-label={t('context.toggle')}>
    <header class="right-sidebar-header">
      <span class="min-w-0 flex-1 truncate text-[13px] font-medium">{t('context.toggle')}</span>
      {#if showClose}
        <button type="button" class="grid h-7 w-7 place-items-center rounded-md" aria-label={t('context.close')} title={t('context.close')} onclick={onClose}>
          <X class="h-4 w-4" />
        </button>
      {/if}
    </header>
    <div class="flex items-center gap-2 p-4 text-sm text-muted-foreground" role="status" aria-live="polite">
      {#if failed}
        <span class="min-w-0 flex-1">{t('context.panelLoadFailed')}</span>
        <button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded-md" aria-label={t('context.panelRetry')} title={t('context.panelRetry')} onclick={() => (panel = import('./WorkspaceContextPanel.svelte'))}>
          <RefreshCw class="h-4 w-4" />
        </button>
      {:else}
        <LoaderCircle class="h-4 w-4 shrink-0 animate-spin" />
        <span>{t('context.panelLoading')}</span>
      {/if}
    </div>
  </aside>
{/snippet}

{#await panel}
  {@render placeholder(false)}
{:then module}
  <module.default {showClose} {onClose} />
{:catch}
  {@render placeholder(true)}
{/await}
