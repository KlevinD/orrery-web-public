<script lang="ts">
  import { Save, Trash2, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SettingsLLMProfileViewModel } from '#lib/types/view-models'
  import type { LLMPanelMode } from './settingsConfig'
  import { modalFocus } from '#lib/actions/modalFocus'

  interface Props {
    open?: boolean
    mode: LLMPanelMode
    profile?: SettingsLLMProfileViewModel
    selectedProfileId: string
    providerOptions: Array<{ label: string; value: string }>
    busy?: boolean
    canSubmit?: boolean
    missingBaseUrl?: boolean
    missingCredential?: boolean
    onclose?: () => void
    onproviderchange?: (provider: string) => void
    onremove?: (profileId: string) => void
    onsubmit?: () => void
  }

  let {
    open = false,
    mode,
    profile = $bindable(),
    selectedProfileId,
    providerOptions,
    busy = false,
    canSubmit = false,
    missingBaseUrl = false,
    missingCredential = false,
    onclose,
    onproviderchange,
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
      aria-label={mode === 'create' ? t('settings.newLlmConnection') : t('settings.llmConnection')}
      onclick={(event) => event.stopPropagation()}
      use:modalFocus={onclose}
    >
      <header class="right-sidebar-header right-sidebar-header-comfortable">
        <div>
          <h2 class="text-sm font-semibold text-foreground">{mode === 'create' ? t('settings.newLlmConnection') : t('settings.llmConnection')}</h2>
          <p class="mt-1 text-xs text-muted-foreground">{t('settings.llmPanel.desc')}</p>
        </div>
        <Button ariaLabel={t('settings.closeLlmPanel')} variant="ghost" size="icon" onclick={onclose}>
          <X class="h-4 w-4" />
        </Button>
      </header>

      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar px-6 py-5">
        <div class="grid gap-4">
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.provider')}</span>
            <Select value={profile.provider} options={providerOptions} onchange={(event) => onproviderchange?.(event.currentTarget.value)} />
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.displayName')}</span>
            <Input bind:value={profile.displayName} />
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.baseUrl')}</span>
            <Input bind:value={profile.baseUrl} />
            {#if missingBaseUrl}
              <span class="text-xs text-destructive">{t('settings.baseUrlRequired')}</span>
            {/if}
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.model')}</span>
            <Input bind:value={profile.model} />
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.apiKeyRef')}</span>
            <Input bind:value={profile.apiKeyRef} />
            {#if missingCredential}
              <span class="text-xs text-destructive">{t('settings.credentialRequired')}</span>
            {/if}
          </label>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="space-y-1.5">
              <span class="text-xs font-medium text-muted-foreground">{t('settings.temperature')}</span>
              <Input bind:value={profile.temperature} />
            </label>
            <label class="space-y-1.5">
              <span class="text-xs font-medium text-muted-foreground">{t('settings.contextLength')}</span>
              <Input bind:value={profile.contextLength} />
            </label>
          </div>
          <label class="flex items-center justify-between gap-4 rounded-lg border border-border px-3 py-2.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.enabled')}</span>
            <div class="relative">
              <input class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand" type="checkbox" bind:checked={profile.enabled} />
              <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
            </div>
          </label>
          <label class="flex items-center justify-between gap-4 rounded-lg border border-border px-3 py-2.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.globalDefaultSwitch')}</span>
            <div class="relative">
              <input class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand" type="checkbox" bind:checked={profile.globalDefault} />
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
