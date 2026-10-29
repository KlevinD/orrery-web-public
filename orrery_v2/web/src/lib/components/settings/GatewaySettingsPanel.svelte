<script lang="ts">
  import { ChevronRight, Plus } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { ApiClientError } from '#lib/api/client'
  import type { GatewayAccountStatusDTO } from '#lib/types/api'
  import type { GatewayChannelSettingsViewModel, GatewayExtraFieldViewModel, GatewaySettingsViewModel } from '#lib/types/view-models'
  import type { GatewayChannelKey, GatewayFieldGroup, GatewayPrimaryFieldId } from './settingsConfig'

  interface Props {
    draft?: GatewaySettingsViewModel
    loading?: boolean
    error?: ApiClientError | null
    visibleChannels: Array<{ key: GatewayChannelKey; label: string; channel: GatewayChannelSettingsViewModel }>
    accountStatuses?: GatewayAccountStatusDTO[]
    fieldGroup: (channelKey: GatewayChannelKey) => GatewayFieldGroup
    primaryFieldValue: (channel: GatewayChannelSettingsViewModel, fieldId: GatewayPrimaryFieldId) => string
    extraFieldLabel: (channelKey: GatewayChannelKey, field: GatewayExtraFieldViewModel) => string
    oncreate?: () => void
    onedit?: (channelKey: GatewayChannelKey) => void
  }

  let {
    draft = undefined,
    loading = false,
    error = null,
    visibleChannels,
    accountStatuses = [],
    fieldGroup,
    primaryFieldValue,
    extraFieldLabel,
    oncreate,
    onedit
  }: Props = $props()
  let t = $derived(createTranslator($locale))

  function channelStatus(channelKey: string) {
    return accountStatuses.find((account) => account.channel_key === channelKey)
  }
</script>

{#if loading}
  <LoadingState title={t('settings.gateway')} description="" />
{:else if error}
  <ErrorState title={t('settings.gateway')} message={error.message} correlationId={error.correlationId} />
{:else if draft}
  <section class="mt-4 space-y-4 rounded-xl border border-border bg-card p-4 sm:p-6">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-sm font-semibold text-foreground">{t('settings.gatewayChannels')}</h2>
        <p class="mt-1 text-xs text-muted-foreground">{t('settings.gatewayChannels.desc')}</p>
      </div>
      <Button onclick={oncreate}>
        <Plus class="h-4 w-4" />{t('settings.addChannel')}
      </Button>
    </div>
  </section>

  <div class="mt-4 grid gap-4 lg:grid-cols-2">
    {#each visibleChannels as item (item.key)}
      {@const channel = item.channel}
      {@const group = fieldGroup(item.key)}
      {@const runtimeStatus = channelStatus(item.key)}
      <button
        type="button"
        class="surface-hover group rounded-xl border border-border bg-card p-5 text-left"
        onclick={() => onedit?.(item.key)}
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="text-sm font-semibold text-foreground">{item.label}</h3>
            <p class="mt-0.5 text-xs text-muted-foreground">{group.title} {t('settings.source')}</p>
          </div>
          <div class="flex items-center gap-2">
            <span class={`rounded-full px-2 py-0.5 text-[10px] font-medium ${runtimeStatus?.status === 'connected' ? 'bg-status-done/15 text-status-done' : runtimeStatus?.status === 'degraded' ? 'bg-destructive/10 text-destructive' : 'bg-muted text-muted-foreground'}`}>
              {runtimeStatus?.status === 'connected'
                ? t('settings.gatewayStatus.connected')
                : runtimeStatus?.status === 'degraded'
                  ? t('settings.gatewayStatus.degraded')
                  : runtimeStatus?.status === 'awaiting_binding'
                    ? t('settings.gatewayStatus.awaitingBinding')
                  : channel.enabled
                    ? t('settings.gatewayStatus.connecting')
                    : t('settings.disabled')}
            </span>
            <ChevronRight class="h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
          </div>
        </div>
        <div class="mt-3 flex flex-wrap gap-1.5">
          {#each group.primaryFields as field}
            {@const val = primaryFieldValue(channel, field.id)}
            {#if val}
              <span class="rounded-md bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{field.label}: {val.length > 20 ? val.slice(0, 20) + '...' : val}</span>
            {/if}
          {/each}
          {#each channel.extraFields as field}
            {@const fieldOption = group.extraFields.find((option) => option.key === field.key)}
            {#if field.value && !fieldOption?.hidden}
              <span class="rounded-md bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{extraFieldLabel(item.key, field)}</span>
            {/if}
          {/each}
        </div>
        {#if runtimeStatus?.last_error}
          <p class="mt-3 line-clamp-2 text-[11px] text-destructive">{runtimeStatus.last_error}</p>
        {/if}
      </button>
    {/each}
  </div>
{/if}
