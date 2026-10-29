<script lang="ts">
  import { onMount } from 'svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { HistoryWindow } from '#lib/queries/history-window'

  let { range = $bindable<HistoryWindow>('today'), now = $bindable(Date.now()), hiddenCount = 0 }: {
    range?: HistoryWindow; now?: number; hiddenCount?: number
  } = $props()
  let t = $derived(createTranslator($locale))
  onMount(() => {
    now = Date.now()
    const timer = setInterval(() => { now = Date.now() }, 60_000)
    return () => clearInterval(timer)
  })
</script>

<div class="my-2 flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground" data-history-window>
  <label class="flex min-w-0 items-center gap-2">
    <span>{t('historyWindow.label')}</span>
    <select class="h-8 min-w-0 rounded border border-border bg-background px-2 text-foreground" bind:value={range}>
      <option value="today">{t('historyWindow.today')}</option>
      <option value="7d">{t('historyWindow.week')}</option>
      <option value="30d">{t('historyWindow.month')}</option>
      <option value="all">{t('historyWindow.all')}</option>
    </select>
  </label>
  {#if hiddenCount > 0}
    <button type="button" class="break-words hover:text-foreground hover:underline" onclick={() => { range = 'all' }}>{t('historyWindow.hidden', { count: hiddenCount })}</button>
  {/if}
</div>
