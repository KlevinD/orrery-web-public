<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { LoaderCircle, Wifi } from 'lucide-svelte'
  import type { SettingsPageIndexConnectionTestViewModel, SettingsViewModel } from '#lib/types/view-models'

  interface Props {
    draft: SettingsViewModel
    busy?: boolean
    onsave?: () => void
    testing?: boolean
    testResult?: SettingsPageIndexConnectionTestViewModel | null
    testError?: string
    ontestpageindex?: () => void
  }

  let {
    draft = $bindable(),
    busy = false,
    onsave,
    testing = false,
    testResult = null,
    testError = '',
    ontestpageindex
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  const providerOptions = [
    { label: 'PageIndex', value: 'pageindex' },
    { label: 'Local', value: 'local' }
  ]
  let resultTone = $derived(
    testError || testResult?.status === 'failed'
      ? 'border-destructive/35 bg-destructive/5 text-destructive'
      : testResult?.status === 'warning'
        ? 'border-amber-500/35 bg-amber-500/5 text-amber-700'
        : 'border-brand/30 bg-brand/5 text-brand'
  )
  let resultTitle = $derived(
    testError
      ? t('settings.pageindexTestFailed')
      : testResult?.status === 'warning'
        ? t('settings.pageindexTestWarning')
        : t('settings.pageindexTestOk')
  )
  let resultDetails = $derived(
    testError
      ? testError
      : testResult
        ? t('settings.pageindexTestDetails', {
            status: testResult.httpStatus ?? '-',
            latency: testResult.latencyMs,
            endpoint: testResult.endpoint
          })
        : ''
  )
</script>

<section class="space-y-6 rounded-xl border border-border bg-card p-4 sm:p-6">
  <div class="flex flex-wrap items-start justify-between gap-3">
    <div>
      <h2 class="text-sm font-semibold text-foreground">{t('settings.knowledge')}</h2>
      <p class="mt-1 text-xs text-muted-foreground">{t('settings.knowledge.desc')}</p>
    </div>
    <label class="relative">
      <span class="sr-only">{t('settings.enabled')}</span>
      <input
        class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand"
        type="checkbox"
        bind:checked={draft.knowledge.enabled}
      />
      <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
    </label>
  </div>

  <div class="grid gap-4 md:grid-cols-2">
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeProvider')}</span>
      <Select bind:value={draft.knowledge.defaultProvider} options={providerOptions} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeTimeout')}</span>
      <Input type="number" bind:value={draft.knowledge.pageindex.timeoutSeconds} />
    </label>
    <div class="space-y-1.5 md:col-span-2">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.pageindexApiUrl')}</span>
      <div class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
        <Input bind:value={draft.knowledge.pageindex.apiUrl} placeholder={t('settings.pageindexApiUrl.placeholder')} />
        <Button
          variant="outline"
          disabled={testing || busy}
          onclick={ontestpageindex}
          ariaLabel={t('settings.pageindexTest')}
          class="w-full sm:w-auto"
        >
          {#if testing}
            <LoaderCircle class="h-4 w-4 animate-spin" />
          {:else}
            <Wifi class="h-4 w-4" />
          {/if}
          {t('settings.pageindexTest')}
        </Button>
      </div>
    </div>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.apiKeyRef')}</span>
      <Input bind:value={draft.knowledge.pageindex.apiKeyRef} placeholder={t('settings.apiKeyRef.placeholder')} />
    </label>
    <label class="space-y-1.5 md:col-span-2">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.pageindexDefaultFolder')}</span>
      <Input bind:value={draft.knowledge.pageindex.defaultFolderId} />
    </label>
  </div>

  {#if testResult || testError}
    <div class="rounded-lg border px-4 py-3 text-sm {resultTone}">
      <div class="flex items-center gap-2 font-medium">
        <Wifi class="h-4 w-4" />
        {resultTitle}
      </div>
      <p class="mt-1 break-words text-xs opacity-90">{resultDetails}</p>
      {#if testResult?.message}
        <p class="mt-1 break-words text-xs opacity-80">{testResult.message}</p>
      {/if}
    </div>
  {/if}

  <div class="space-y-3 rounded-lg border border-border bg-muted/20 px-4 py-3">
    <label class="flex items-center justify-between gap-4">
      <div>
        <span class="block text-sm font-medium text-foreground">{t('settings.localKnowledgeArchive')}</span>
        <span class="text-xs text-muted-foreground">{t('settings.localKnowledgeArchive.desc')}</span>
      </div>
      <div class="relative">
        <input
          class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand"
          type="checkbox"
          bind:checked={draft.knowledge.local.enabled}
        />
        <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
      </div>
    </label>
  </div>

  <div class="flex justify-end border-t border-border pt-4">
    <Button disabled={busy} onclick={onsave}>{t('settings.save')}</Button>
  </div>
</section>
