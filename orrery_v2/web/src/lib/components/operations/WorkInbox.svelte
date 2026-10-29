<script lang="ts">
  import { createTranslator, locale } from '#lib/i18n'
  import WorkActionWorkspace from '#lib/components/workhub/WorkActionWorkspace.svelte'
  import WorkActivity from '#lib/components/workhub/WorkActivity.svelte'
  let { compact = false, view }: { compact?: boolean; view?: 'pending' | 'results' | 'done' } = $props()
  let tab = $state<'pending' | 'results' | 'done'>('pending')
  const t = $derived(createTranslator($locale))
  $effect(() => { if (view) tab = view })
</script>

<section data-work-inbox={compact ? 'sidebar' : 'main'} class="min-w-0">
  {#if !view}
    <h2 class="mb-3 text-sm font-semibold">{t('workInbox.title')}</h2>
    <div role="tablist" aria-label={t('workInbox.title')} class="mb-3 flex border-b border-border">
      {#each (['pending', 'results', 'done'] as const) as value}
        <button role="tab" aria-selected={tab === value} class="min-w-0 flex-1 border-b-2 px-1 py-2 text-xs {tab === value ? 'border-foreground font-semibold' : 'border-transparent text-muted-foreground'}" onclick={() => tab = value}>{value === 'done' ? ($locale.startsWith('zh') ? '已处理' : 'Processed') : t(`workInbox.${value}`)}</button>
      {/each}
    </div>
  {/if}
  {#if tab === 'results'}<WorkActivity {compact} />{:else}<WorkActionWorkspace {compact} view={tab === 'done' ? 'history' : 'pending'} />{/if}
  {#if compact}<a class="mt-4 block text-right text-xs text-muted-foreground hover:text-foreground" href="/work-items?view=inbox">{t('workInbox.open')}</a>{/if}
</section>
