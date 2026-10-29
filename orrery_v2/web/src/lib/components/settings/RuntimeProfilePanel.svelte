<script lang="ts">
  import { Save, Trash2, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import RuntimeProviderConfigForm from '#lib/components/runtime/RuntimeProviderConfigForm.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { RuntimeConfigValue, RuntimeProviderKind } from '#lib/runtime/providerConfig'
  import type { SettingsRuntimeProfileViewModel } from '#lib/types/view-models'
  import type { RuntimePanelMode } from './settingsConfig'
  import { modalFocus } from '#lib/actions/modalFocus'

  interface Props {
    open?: boolean
    mode: RuntimePanelMode
    profile?: SettingsRuntimeProfileViewModel
    selectedProfileId: string
    provider?: { kind: RuntimeProviderKind } | undefined
    providerOptions: Array<{ label: string; value: string }>
    formValues: Record<string, string>
    busy?: boolean
    canSubmit?: boolean
    onclose?: () => void
    onproviderchange?: (kind: string) => void
    onconfigchange?: (config: {
      runtimeKind: RuntimeProviderKind
      runtimeConfigRef: string
      runtimeConfig: Record<string, RuntimeConfigValue>
    }) => void
    onremove?: (profileId: string) => void
    onsubmit?: () => void
  }

  let {
    open = false,
    mode,
    profile = $bindable(),
    selectedProfileId,
    provider,
    providerOptions,
    formValues,
    busy = false,
    canSubmit = false,
    onclose,
    onproviderchange,
    onconfigchange,
    onremove,
    onsubmit
  }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

{#if open && profile}
  <div class="fixed inset-0 z-50 bg-black/40 backdrop-blur-sm" role="presentation" onclick={onclose}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="right-sidebar-shell right-sidebar-drawer ml-auto max-w-xl"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      aria-label={mode === 'create' ? t('settings.newRuntimeConnection') : t('settings.runtimeConnection')}
      onclick={(event) => event.stopPropagation()}
      use:modalFocus={onclose}
    >
      <header class="right-sidebar-header right-sidebar-header-comfortable">
        <div>
          <h2 class="text-sm font-semibold text-foreground">{mode === 'create' ? t('settings.newRuntimeConnection') : t('settings.runtimeConnection')}</h2>
          <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimePanel.desc')}</p>
        </div>
        <Button ariaLabel={t('settings.closeRuntimePanel')} variant="ghost" size="icon" onclick={onclose}>
          <X class="h-4 w-4" />
        </Button>
      </header>

      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar px-6 py-5">
        <div class="grid gap-4">
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.type')}</span>
            <Select value={profile.runtimeKind} options={providerOptions} onchange={(event) => onproviderchange?.(event.currentTarget.value)} />
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.displayName')}</span>
            <Input bind:value={profile.displayName} />
          </label>
          {#if provider}
            <div class="space-y-1.5">
              <span class="text-xs font-medium text-muted-foreground">{t('settings.config')}</span>
              <RuntimeProviderConfigForm kind={provider.kind} values={formValues} onchange={onconfigchange} />
            </div>
          {:else}
            <div class="flex min-h-16 items-center rounded-lg border border-dashed border-border bg-muted/20 px-4 text-sm text-muted-foreground">
              {t('settings.selectRuntimeType')}
            </div>
          {/if}
          <label class="flex items-center justify-between gap-4 rounded-lg border border-border px-3 py-2.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.enabled')}</span>
            <div class="relative">
              <input class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand" type="checkbox" bind:checked={profile.enabled} />
              <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
            </div>
          </label>
          {#if mode === 'edit'}
            <div class="rounded-lg bg-muted/40 px-3 py-2 font-mono text-[11px] text-muted-foreground">
              {profile.profileId}
            </div>
          {/if}
        </div>
      </div>

      <footer class="right-sidebar-footer flex-wrap justify-between gap-3 px-4 sm:px-6">
        {#if mode === 'edit'}
          <Button variant="ghost" disabled={busy} onclick={() => onremove?.(selectedProfileId)}>
            <Trash2 class="h-4 w-4" />Delete
          </Button>
        {:else}
          <span></span>
        {/if}
        <div class="flex flex-wrap items-center justify-end gap-2">
          <Button variant="outline" disabled={busy} onclick={onclose}>{t('settings.cancel')}</Button>
          <Button disabled={busy || !canSubmit} onclick={onsubmit}>
            <Save class="h-4 w-4" />{mode === 'create' ? t('settings.create') : t('settings.saveShort')}
          </Button>
        </div>
      </footer>
    </div>
  </div>
{/if}
