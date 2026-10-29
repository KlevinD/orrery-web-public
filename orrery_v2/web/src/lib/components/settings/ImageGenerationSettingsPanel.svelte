<script lang="ts">
  import { LoaderCircle, PlugZap, RotateCcw, Save } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type { ImageConnectionTestDTO, ImageGenerationSettingsDTO } from '#lib/types/api/image-generation'

  interface Props {
    settings?: ImageGenerationSettingsDTO
    loading?: boolean
    loadFailed?: boolean
    readOnly?: boolean
    busy?: boolean
    onsave: (settings: ImageGenerationSettingsDTO) => Promise<ImageGenerationSettingsDTO>
    ontest: () => Promise<ImageConnectionTestDTO>
    onretry?: () => void
  }
  let { settings, loading = false, loadFailed = false, readOnly = false, busy = false, onsave, ontest, onretry }: Props = $props()
  let base = $state<ImageGenerationSettingsDTO>()
  let lastIncoming = $state('')
  let draft = $state<ImageGenerationSettingsDTO>({ enabled: false, protocol: 'openai-compatible', base_url: '', model: '', api_key_ref: '' })
  let result = $state<ImageConnectionTestDTO>()
  let resultFor = $state('')
  let feedback = $state('')
  let failed = $state(false)
  let pending = $state(false)
  const t = $derived(createTranslator($locale))
  const dirty = $derived(Boolean(base && JSON.stringify(draft) !== JSON.stringify(base)))
  const complete = $derived(Boolean(draft.base_url.trim() && draft.model.trim() && draft.api_key_ref.trim()))
  const disabled = $derived(readOnly || busy || pending)
  const reasons: Record<ImageConnectionTestDTO['reason'], TranslationKey> = {
    model_listed: 'settings.image.result.modelListed',
    model_not_listed: 'settings.image.result.modelMissing',
    credential_unavailable: 'settings.image.result.credential',
    connection_failed: 'settings.image.result.connection',
    authentication_failed: 'settings.image.result.authentication',
    upstream_unavailable: 'settings.image.result.upstream',
    invalid_response: 'settings.image.result.invalid',
  }

  $effect(() => {
    if (settings) {
      const incoming = JSON.stringify(settings)
      if (incoming !== lastIncoming) {
        lastIncoming = incoming
        if (!base || !dirty) {
          base = { ...settings }
          draft = { ...settings }
          result = undefined
        }
      }
    }
  })

  async function save() {
    if (disabled || !dirty || (draft.enabled && !complete)) return
    pending = true
    feedback = ''
    result = undefined
    try {
      const saved = await onsave({ ...draft, base_url: draft.base_url.trim().replace(/\/+$/, ''), model: draft.model.trim(), api_key_ref: draft.api_key_ref.trim() })
      base = saved
      draft = { ...saved }
      failed = false
      feedback = t('settings.saved')
    } catch {
      failed = true
      feedback = t('settings.image.saveFailed')
    } finally { pending = false }
  }

  async function test() {
    if (disabled || dirty || !complete) return
    pending = true
    feedback = ''
    result = undefined
    const fingerprint = JSON.stringify(draft)
    try {
      result = await ontest()
      resultFor = fingerprint
    } catch {
      failed = true
      feedback = t('settings.image.checkFailed')
    } finally { pending = false }
  }

  function reset() {
    if (!settings || disabled) return
    base = settings
    draft = { ...settings }
    result = undefined
    feedback = ''
  }
</script>

<section class="space-y-5 py-2" aria-label={t('settings.image.title')}>
  <h2 class="text-sm font-semibold text-foreground">{t('settings.image.title')}</h2>
  {#if loading}
    <div class="flex min-h-24 items-center gap-2 text-sm text-muted-foreground" role="status"><LoaderCircle class="h-4 w-4 animate-spin" />{t('settings.image.loading')}</div>
  {:else if loadFailed}
    <p class="text-sm text-destructive" role="alert">{t('settings.image.loadFailed')}</p>
    <Button variant="outline" onclick={onretry}><RotateCcw class="h-4 w-4" />{t('settings.image.retry')}</Button>
  {:else if base}
    <fieldset disabled={disabled} class="grid min-w-0 gap-4 sm:grid-cols-2">
      <label class="flex items-center justify-between gap-4 border-b border-border pb-4 sm:col-span-2">
        <span class="text-sm font-medium">{t('settings.image.enabled')}</span>
        <input type="checkbox" class="h-4 w-4 accent-brand" bind:checked={draft.enabled} />
      </label>
      <label class="min-w-0 space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.image.protocol')}</span>
        <Select value={draft.protocol} options={[{ value: 'openai-compatible', label: t('settings.image.compatible') }]} />
      </label>
      <label class="min-w-0 space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.model')}</span>
        <Input bind:value={draft.model} />
      </label>
      <div class="min-w-0 space-y-1.5 sm:col-span-2">
        <label class="block space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.image.gateway')}</span>
          <Input type="url" bind:value={draft.base_url} />
        </label>
        <p class="text-xs text-muted-foreground">https://gateway.example/v1</p>
      </div>
      <div class="min-w-0 space-y-1.5 sm:col-span-2">
        <label class="block space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.apiKeyRef')}</span>
          <Input bind:value={draft.api_key_ref} />
        </label>
        <p class="text-xs text-muted-foreground">secret://image-generation/provider</p>
      </div>
    </fieldset>
    <div class="flex flex-wrap items-center gap-2">
      <Button disabled={disabled || !dirty || (draft.enabled && !complete)} onclick={save}><Save class="h-4 w-4" />{t('settings.saveShort')}</Button>
      <Button variant="outline" disabled={disabled || dirty || !complete} onclick={test}><PlugZap class="h-4 w-4" />{t('settings.image.check')}</Button>
      <Button variant="ghost" size="icon" ariaLabel={t('settings.image.reset')} title={t('settings.image.reset')} disabled={disabled || !dirty} onclick={reset}><RotateCcw class="h-4 w-4" /></Button>
      {#if pending}<LoaderCircle class="h-4 w-4 animate-spin" aria-label={t('settings.image.checking')} />{/if}
      {#if dirty}<span class="text-xs text-muted-foreground">{t('settings.image.unsaved')}</span>{/if}
    </div>
    {#if feedback}<p class={failed ? 'text-sm text-destructive' : 'text-sm text-muted-foreground'} role={failed ? 'alert' : 'status'}>{feedback}</p>{/if}
    {#if result && resultFor === JSON.stringify(draft) && !dirty}
      <div class="space-y-1 border-t border-border pt-4 text-sm" role="status">
        <p class={result.status === 'failed' ? 'text-destructive' : 'text-foreground'}>{t(reasons[result.reason] ?? 'settings.image.checkFailed')}</p>
        <p class="text-xs text-muted-foreground">{t('settings.image.generationUnverified')}</p>
      </div>
    {/if}
  {/if}
</section>
