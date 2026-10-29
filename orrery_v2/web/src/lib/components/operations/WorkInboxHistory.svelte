<script lang="ts">
  import { useScheduledRunInboxQuery } from '#lib/queries/scheduler'
  import { createTranslator, locale } from '#lib/i18n'
  let { taskId }: { taskId: string } = $props()
  let offset = $state(0)
  const limit = 20
  const query = useScheduledRunInboxQuery(() => ({ taskId, limit, offset }))
  const t = $derived(createTranslator($locale))
</script>

{#if query.isLoading}
  <p role="status">{t('workInbox.loading')}</p>
{:else if query.isError}
  <p role="alert">{t('workInbox.failed')}</p>
  <button onclick={() => query.refetch()}>{t('workInbox.refresh')}</button>
{:else}
  {#each query.data?.items ?? [] as item (item.run.scheduled_run_id)}
    <a class="block break-words py-1 text-foreground hover:underline" href={`/scheduled-tasks?view=results&task=${encodeURIComponent(taskId)}&run=${encodeURIComponent(item.run.scheduled_run_id)}`}>{new Date(item.run.created_at).toLocaleString($locale)}</a>
  {/each}
  {#if (query.data?.total ?? 0) > limit}
    <div class="flex justify-between gap-2 pt-2">
      <button disabled={offset === 0} class="disabled:opacity-40" onclick={() => offset -= limit}>{t('workInbox.previous')}</button>
      <span>{Math.floor(offset / limit) + 1} / {Math.ceil((query.data?.total ?? 0) / limit)}</span>
      <button disabled={offset + limit >= (query.data?.total ?? 0)} class="disabled:opacity-40" onclick={() => offset += limit}>{t('workInbox.next')}</button>
    </div>
  {/if}
{/if}
