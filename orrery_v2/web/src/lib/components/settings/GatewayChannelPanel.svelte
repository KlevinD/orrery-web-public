<script lang="ts">
  import { onDestroy } from 'svelte'
  import { ExternalLink, LoaderCircle, Save, Trash2, X } from 'lucide-svelte'
  import QRCode from 'qrcode'
  import { ApiClientError } from '#lib/api/client'
  import { gatewaySettingsApi } from '#lib/api/gateway-settings'
  import { modalFocus } from '#lib/actions/modalFocus'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import FeishuAccountsEditor from './FeishuAccountsEditor.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { GatewayExtraFieldViewModel, GatewayFeishuAccountViewModel } from '#lib/types/view-models'
  import type { FeishuRegistrationDTO } from '#lib/types/api'
  import type { GatewayFieldGroup } from './settingsConfig'

  interface Props {
    open?: boolean
    mode: 'create' | 'edit'
    selectedChannelKey: string
    editingChannelKey: string
    availableChannels: Array<{ label: string; value: string }>
    group: GatewayFieldGroup
    channelKey: string
    enabled: boolean
    fieldValues: Record<string, string>
    extraFields: GatewayExtraFieldViewModel[]
    feishuAccounts?: Record<string, GatewayFeishuAccountViewModel>
    busy?: boolean
    onclose?: () => void
    onselectedchannelchange?: (channelKey: string) => void
    onenabledchange?: (enabled: boolean) => void
    onfieldchange?: (fieldId: string, value: string) => void
    onextrafieldchange?: (channelKey: string, fieldKey: string, value: string) => void
    onfeishuaccountschange?: (accounts: Record<string, GatewayFeishuAccountViewModel>) => void
    onremove?: (channelKey: string) => void
    onsubmit?: () => void
    onregistrationconnected?: () => void
  }

  let {
    open = false,
    mode,
    selectedChannelKey = $bindable(),
    editingChannelKey,
    availableChannels,
    group,
    channelKey,
    enabled = false,
    fieldValues,
    extraFields,
    feishuAccounts = {},
    busy = false,
    onclose,
    onselectedchannelchange,
    onenabledchange,
    onfieldchange,
    onextrafieldchange,
    onfeishuaccountschange,
    onremove,
    onsubmit,
    onregistrationconnected
  }: Props = $props()

  const registrationPollIntervalMs = 1500
  const maxRegistrationPollIntervalMs = 12000
  const maxRegistrationPollFailures = 4

  let t = $derived(createTranslator($locale))
  let registration = $state<FeishuRegistrationDTO | null>(null)
  let registrationQRCode = $state('')
  let registrationBusy = $state(false)
  let registrationError = $state('')
  let registrationQRCodeError = $state('')
  let panelWasActive = $state(false)
  let registrationEpoch = 0
  let registrationPollFailures = 0
  let pollingTimer: number | undefined

  $effect(() => {
    const url = registration?.verification_url ?? ''
    let cancelled = false
    registrationQRCodeError = ''
    if (!url) {
      registrationQRCode = ''
      return
    }
    QRCode.toDataURL(url, { width: 220, margin: 1, errorCorrectionLevel: 'M' })
      .then((value) => {
        if (!cancelled) registrationQRCode = value
      })
      .catch(() => {
        if (!cancelled) {
          registrationQRCode = ''
          registrationQRCodeError = t('settings.feishuRegistration.qrFailed')
        }
      })
    return () => {
      cancelled = true
    }
  })

  $effect(() => {
    if (open && channelKey === 'feishu') {
      panelWasActive = true
      return
    }
    if (!panelWasActive) return
    endRegistrationSession()
  })

  async function startFeishuRegistration(accountId: string) {
    if (!open || channelKey !== 'feishu' || !accountId.trim()) return
    const epoch = ++registrationEpoch
    clearPollingTimer()
    registrationPollFailures = 0
    registrationBusy = true
    registrationError = ''
    registrationQRCodeError = ''
    registration = null
    registrationQRCode = ''
    try {
      const next = await gatewaySettingsApi.startFeishuRegistration(accountId.trim())
      if (!registrationSessionIsCurrent(epoch)) {
        if (registrationIsPending(next)) void cancelRegistrationById(next.registration_id)
        return
      }
      applyRegistration(next, epoch)
    } catch (error) {
      if (registrationSessionIsCurrent(epoch)) {
        registrationError = error instanceof Error ? error.message : t('settings.feishuRegistration.failed')
      }
    } finally {
      if (registrationSessionIsCurrent(epoch)) registrationBusy = false
    }
  }

  function applyRegistration(next: FeishuRegistrationDTO, epoch: number) {
    if (!registrationSessionIsCurrent(epoch)) return
    if (registrationIsPending(next) && registrationHasExpired(next)) {
      expireRegistration(next)
      return
    }
    registration = next
    registrationPollFailures = 0
    if (next.status === 'connected') {
      registrationError = ''
      onregistrationconnected?.()
    } else if (next.status === 'failed' || next.status === 'expired') {
      registrationError = feishuRegistrationError(next.error_code)
    }
    scheduleRegistrationPoll(next, epoch)
  }

  function scheduleRegistrationPoll(current: FeishuRegistrationDTO, epoch: number) {
    clearPollingTimer()
    if (!registrationIsActive(current) || !registrationSessionIsCurrent(epoch)) return
    const remainingMs = registrationRemainingMs(current)
    if (remainingMs <= 0) {
      expireRegistration(current)
      return
    }
    const retryDelay = Math.min(
      registrationPollIntervalMs * 2 ** registrationPollFailures,
      maxRegistrationPollIntervalMs
    )
    pollingTimer = window.setTimeout(() => {
      pollingTimer = undefined
      void pollRegistration(current.registration_id, epoch)
    }, Math.min(retryDelay, remainingMs))
  }

  async function pollRegistration(registrationId: string, epoch: number) {
    if (!registrationSessionIsCurrent(epoch)) return
    const beforeRequest = registration
    if (beforeRequest?.registration_id === registrationId && registrationHasExpired(beforeRequest)) {
      expireRegistration(beforeRequest)
      return
    }
    try {
      const next = await gatewaySettingsApi.getFeishuRegistration(registrationId)
      if (!registrationSessionIsCurrent(epoch) || next.registration_id !== registrationId) return
      applyRegistration(next, epoch)
    } catch (error) {
      if (!registrationSessionIsCurrent(epoch)) return
      const current = registration
      if (current?.registration_id !== registrationId) return
      if (registrationHasExpired(current)) {
        expireRegistration(current)
        return
      }
      const message = registrationErrorMessage(error)
      if (registrationPollErrorIsTerminal(error)) {
        failRegistration(current, message)
        return
      }
      registrationPollFailures += 1
      registrationError = message
      if (registrationPollFailures >= maxRegistrationPollFailures) {
        void cancelAndFailRegistration(current, message, epoch)
        return
      }
      scheduleRegistrationPoll(current, epoch)
    }
  }

  function registrationSessionIsCurrent(epoch: number) {
    return epoch === registrationEpoch && open && channelKey === 'feishu'
  }

  function registrationIsActive(value: FeishuRegistrationDTO | null) {
    return Boolean(value && registrationIsPending(value) && !registrationHasExpired(value))
  }

  function registrationIsPending(value: FeishuRegistrationDTO) {
    return value.status === 'starting' || value.status === 'pending'
  }

  function registrationRemainingMs(value: FeishuRegistrationDTO) {
    const expiresAt = Date.parse(value.expires_at)
    return Number.isFinite(expiresAt) ? expiresAt - Date.now() : 0
  }

  function registrationHasExpired(value: FeishuRegistrationDTO) {
    return registrationRemainingMs(value) <= 0
  }

  function expireRegistration(current: FeishuRegistrationDTO) {
    clearPollingTimer()
    registration = { ...current, status: 'expired', error_code: current.error_code || 'expired' }
    registrationError = t('settings.feishuRegistration.expired')
  }

  function failRegistration(current: FeishuRegistrationDTO, message: string) {
    clearPollingTimer()
    registration = { ...current, status: 'failed' }
    registrationError = message
  }

  async function cancelAndFailRegistration(current: FeishuRegistrationDTO, message: string, epoch: number) {
    clearPollingTimer()
    registrationBusy = true
    const cancelled = await cancelRegistrationById(current.registration_id)
    if (!registrationSessionIsCurrent(epoch) || registration?.registration_id !== current.registration_id) return
    registrationBusy = false
    if (cancelled) {
      failRegistration(current, message)
      return
    }
    registrationPollFailures = maxRegistrationPollFailures - 1
    registrationError = message
    scheduleRegistrationPoll(current, epoch)
  }

  function registrationPollErrorIsTerminal(error: unknown) {
    return error instanceof ApiClientError && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429
  }

  function registrationErrorMessage(error: unknown) {
    return error instanceof ApiClientError && error.message.trim()
      ? error.message
      : t('settings.feishuRegistration.failed')
  }

  function feishuRegistrationError(code?: string) {
    switch (code) {
      case 'access_denied':
        return t('settings.feishuRegistration.accessDenied')
      case 'expired':
        return t('settings.feishuRegistration.expired')
      case 'gateway_reload_failed':
        return t('settings.feishuRegistration.reloadFailed')
      default:
        return t('settings.feishuRegistration.failed')
    }
  }

  function registrationStatusText() {
    if (registrationBusy && !registration) return t('settings.feishuRegistration.requesting')
    if (registration?.status === 'starting') return t('settings.feishuRegistration.generating')
    if (registration?.status === 'pending') return t('settings.feishuRegistration.pending')
    return ''
  }

  function requiredPrimaryFieldsMissing() {
    if (channelKey === 'feishu') return false
    return group.primaryFields.some((field) => field.required && !(fieldValues[field.id] ?? '').trim())
  }

  function clearPollingTimer() {
    if (pollingTimer === undefined) return
    window.clearTimeout(pollingTimer)
    pollingTimer = undefined
  }

  async function cancelRegistrationById(registrationId: string) {
    try {
      await gatewaySettingsApi.cancelFeishuRegistration(registrationId)
      return true
    } catch {
      // Local teardown must still complete when the server is unreachable.
      return false
    }
  }

  function endRegistrationSession() {
    const current = registration
    registrationEpoch += 1
    panelWasActive = false
    clearPollingTimer()
    registrationPollFailures = 0
    registrationBusy = false
    registration = null
    registrationQRCode = ''
    registrationError = ''
    registrationQRCodeError = ''
    if (current && registrationIsPending(current)) void cancelRegistrationById(current.registration_id)
  }

  function closePanel() {
    endRegistrationSession()
    onclose?.()
  }

  onDestroy(() => {
    endRegistrationSession()
  })
</script>

{#if open}
  <div class="fixed inset-0 z-50 bg-black/40 backdrop-blur-sm" role="presentation" onclick={closePanel}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="right-sidebar-shell right-sidebar-drawer ml-auto max-w-2xl"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      aria-label={mode === 'create' ? t('settings.addGatewayChannel') : t('settings.gatewayChannel')}
      onclick={(event) => event.stopPropagation()}
      use:modalFocus={closePanel}
    >
      <header class="right-sidebar-header right-sidebar-header-comfortable">
        <div>
          <h2 class="text-sm font-semibold text-foreground">{mode === 'create' ? t('settings.addGatewayChannel') : t('settings.gatewayChannel')}</h2>
          <p class="mt-1 text-xs text-muted-foreground">{t('settings.gatewayPanel.desc')}</p>
        </div>
        <Button ariaLabel={t('settings.closeGatewayPanel')} variant="ghost" size="icon" onclick={closePanel}>
          <X class="h-4 w-4" />
        </Button>
      </header>

      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar px-6 py-5">
        {#if mode === 'create'}
          <label class="mb-4 block space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.channel')}</span>
            <Select bind:value={selectedChannelKey} options={[{ label: t('settings.selectChannel'), value: '' }, ...availableChannels]} onchange={(event) => onselectedchannelchange?.(event.currentTarget.value)} />
          </label>
        {/if}

        {#if mode === 'edit' || (mode === 'create' && selectedChannelKey)}
          <div class="grid gap-4">
            {#if group.descriptionKey || group.description || group.setupStepKeys?.length || group.setupSteps?.length}
              <div class="rounded-md border border-border bg-muted/20 px-4 py-3">
                {#if group.descriptionKey}
                  <p class="text-xs leading-5 text-foreground">{t(group.descriptionKey)}</p>
                {:else if group.description}
                  <p class="text-xs leading-5 text-foreground">{group.description}</p>
                {/if}
                {#if group.setupStepKeys?.length}
                  <ol class="mt-2 list-decimal space-y-1 pl-4 text-xs leading-5 text-muted-foreground">
                    {#each group.setupStepKeys as stepKey}
                      <li>{t(stepKey)}</li>
                    {/each}
                  </ol>
                {:else if group.setupSteps?.length}
                  <ol class="mt-2 list-decimal space-y-1 pl-4 text-xs leading-5 text-muted-foreground">
                    {#each group.setupSteps as step}
                      <li>{step}</li>
                    {/each}
                  </ol>
                {/if}
                {#if group.setupLink}
                  <a class="mt-3 inline-flex items-center gap-1.5 text-xs font-medium text-brand hover:underline" href={group.setupLink.url} target="_blank" rel="noreferrer">
                    {t(group.setupLink.labelKey)}<ExternalLink class="h-3.5 w-3.5" />
                  </a>
                {/if}
              </div>
            {/if}

            {#if channelKey === 'feishu'}
              <FeishuAccountsEditor
                accounts={feishuAccounts}
                busy={busy || registrationBusy || registration?.status === 'starting' || registration?.status === 'pending'}
                onaccountschange={onfeishuaccountschange}
                onregister={startFeishuRegistration}
              />
              <div class="grid gap-3 border-b border-border pb-4">
                {#if registrationStatusText()}
                  <p class="flex items-center gap-2 rounded-md border border-brand/20 bg-brand/5 px-3 py-2 text-xs text-foreground" role="status" aria-live="polite">
                    {#if registrationBusy || registration?.status === 'starting'}
                      <LoaderCircle class="h-4 w-4 shrink-0 animate-spin text-brand" />
                    {/if}
                    {registrationStatusText()}
                  </p>
                {/if}
                {#if registration?.verification_url && registration?.status === 'pending'}
                  <div class="flex flex-col items-center gap-2 rounded-md border border-border bg-white p-3">
                    {#if registrationQRCode}
                      <img class="h-52 w-52" src={registrationQRCode} alt={t('settings.feishuRegistration.qrAlt')} />
                    {:else if !registrationQRCodeError}
                      <div class="flex h-52 w-52 items-center justify-center text-muted-foreground">
                        <LoaderCircle class="h-5 w-5 animate-spin" />
                      </div>
                    {/if}
                    <a class="max-w-full break-all text-xs text-brand hover:underline" href={registration.verification_url} target="_blank" rel="noreferrer">
                      {t('settings.feishuRegistration.openLink')}
                    </a>
                  </div>
                {:else if registration?.status === 'connected'}
                  <p class="rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-xs text-emerald-700">
                    {registration.account_id}: {t('settings.feishuRegistration.connected')}
                  </p>
                {/if}
                {#if registrationError || registrationQRCodeError}
                  <p class="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">{registrationError || registrationQRCodeError}</p>
                {/if}
              </div>
            {/if}

            <label class="flex items-center justify-between gap-4 rounded-md border border-border px-3 py-2.5">
              <span class="text-xs font-medium text-muted-foreground">{t('settings.enabled')}</span>
              <div class="relative">
                <input
                  class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand"
                  type="checkbox"
                  checked={enabled}
                  onchange={(event) => onenabledchange?.(event.currentTarget.checked)}
                />
                <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
              </div>
            </label>

            {#if channelKey !== 'feishu'}
              {#each group.primaryFields as field}
                <label class="space-y-1.5">
                  <span class="text-xs font-medium text-muted-foreground">{field.label}{field.required ? ' *' : ''}</span>
                  <Input
                    type={field.secret ? 'password' : 'text'}
                    autocomplete={field.secret ? 'new-password' : undefined}
                    value={fieldValues[field.id] ?? ''}
                    placeholder={field.placeholder ?? ''}
                    oninput={(event) => onfieldchange?.(field.id, event.currentTarget.value)}
                  />
                  {#if field.helpKey}
                    <span class="block text-[11px] leading-4 text-muted-foreground">{t(field.helpKey)}</span>
                  {:else if field.help}
                    <span class="block text-[11px] leading-4 text-muted-foreground">{field.help}</span>
                  {/if}
                </label>
              {/each}
            {/if}

            {#if channelKey !== 'feishu' && group.extraFields.some((field) => !field.hidden)}
              <div class="grid gap-3 border-t border-border pt-4">
                <span class="text-xs font-medium text-muted-foreground">{t('settings.advanced')}</span>
                {#each group.extraFields as opt}
                  {#if !opt.hidden}
                    {@const existing = extraFields.find((field) => field.key === opt.key)}
                    <label class="space-y-1.5">
                      <span class="text-xs font-medium text-muted-foreground">{opt.label}</span>
                      {#if opt.options?.length}
                        <Select
                          value={existing?.value ?? opt.defaultValue ?? ''}
                          options={opt.options}
                          onchange={(event) => onextrafieldchange?.(channelKey, opt.key, event.currentTarget.value)}
                        />
                      {:else if opt.multiline}
                        <textarea
                          class="min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 font-mono text-xs text-foreground shadow-sm outline-none focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring"
                          value={existing?.value ?? opt.defaultValue ?? ''}
                          placeholder={opt.placeholder ?? ''}
                          oninput={(event) => onextrafieldchange?.(channelKey, opt.key, event.currentTarget.value)}
                        ></textarea>
                      {:else}
                        <Input
                          value={existing?.value ?? opt.defaultValue ?? ''}
                          placeholder={opt.placeholder ?? ''}
                          oninput={(event) => onextrafieldchange?.(channelKey, opt.key, event.currentTarget.value)}
                        />
                      {/if}
                      {#if opt.helpKey}
                        <span class="block text-[11px] leading-4 text-muted-foreground">{t(opt.helpKey)}</span>
                      {:else if opt.help}
                        <span class="block text-[11px] leading-4 text-muted-foreground">{opt.help}</span>
                      {/if}
                    </label>
                  {/if}
                {/each}
              </div>
            {/if}
          </div>
        {:else if mode === 'create' && !selectedChannelKey}
          <div class="rounded-md border border-dashed border-border bg-muted/20 px-4 py-8 text-center text-sm text-muted-foreground">
            {t('settings.selectChannelType')}
          </div>
        {/if}
      </div>

      <footer class="right-sidebar-footer flex-wrap justify-between gap-3 px-4 sm:px-6">
        {#if mode === 'edit'}
          <Button variant="ghost" disabled={busy} onclick={() => onremove?.(editingChannelKey)}>
            <Trash2 class="h-4 w-4" />{t('settings.remove')}
          </Button>
        {:else}
          <span></span>
        {/if}
        <div class="flex flex-wrap items-center justify-end gap-2">
          <Button variant="outline" onclick={closePanel}>{t('settings.cancel')}</Button>
          <Button disabled={busy || (mode === 'create' && !selectedChannelKey) || requiredPrimaryFieldsMissing()} onclick={onsubmit}>
            <Save class="h-4 w-4" />{mode === 'create' ? t('settings.add') : t('settings.done')}
          </Button>
        </div>
      </footer>
    </div>
  </div>
{/if}
