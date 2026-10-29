<script lang="ts">
  import { ChevronRight, Cpu, Plus, Search, Trash2 } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SettingsRuntimeProfileViewModel, SettingsViewModel } from '#lib/types/view-models'

  interface Props {
    draft: SettingsViewModel
    busy?: boolean
    discoveryBusy?: boolean
    workspaceRootLabel: string
    runtimeProviderLabel: (kind: string) => string
    runtimeProfileSummary: (profile: SettingsRuntimeProfileViewModel) => string
    ondiscover?: () => void
    oncreate?: () => void
    onedit?: (profile: SettingsRuntimeProfileViewModel) => void
    onremove?: (profileId: string) => void
    onenabledchange?: (profileId: string, enabled: boolean) => void
  }

  let {
    draft,
    busy = false,
    discoveryBusy = false,
    workspaceRootLabel,
    runtimeProviderLabel,
    runtimeProfileSummary,
    ondiscover,
    oncreate,
    onedit,
    onremove,
    onenabledchange
  }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<section class="space-y-6 rounded-xl border border-border bg-card p-4 sm:p-6">
  <div class="flex flex-wrap items-start justify-between gap-3">
    <div>
      <h2 class="text-sm font-semibold text-foreground">{t('settings.runtimeConnections')}</h2>
      <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimeConnections.desc')}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="outline" disabled={discoveryBusy} onclick={ondiscover}>
        <Search class="h-4 w-4" />{t('settings.autoDiscover')}
      </Button>
      <Button disabled={busy} onclick={oncreate}>
        <Plus class="h-4 w-4" />{t('settings.newRuntime')}
      </Button>
    </div>
  </div>

  <div class="rounded-lg border border-border bg-muted/20 px-4 py-3 text-xs text-muted-foreground">
    <span class="font-medium text-foreground">{t('settings.workspaceRootLabel')}</span>
    <p class="mt-1 font-mono break-all">{workspaceRootLabel}</p>
  </div>

  {#if draft.runtimeProfiles.length === 0}
    <div class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-border bg-muted/20 py-10 text-sm text-muted-foreground">
      <Cpu class="h-8 w-8 text-muted-foreground/40" />
      <span>{t('settings.noRuntimeConnections')}</span>
    </div>
  {:else}
    <div class="overflow-hidden rounded-xl border border-border">
      {#each draft.runtimeProfiles as profile (profile.profileId)}
        <div class="grid gap-3 border-b border-border px-4 py-3 last:border-b-0 hover:bg-muted/30 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <button type="button" class="grid min-w-0 gap-1 text-left" onclick={() => onedit?.(profile)}>
            <span class="truncate text-sm font-medium text-foreground">{profile.displayName || runtimeProviderLabel(profile.runtimeKind)}</span>
            <span class="truncate text-xs text-muted-foreground">{runtimeProfileSummary(profile)}</span>
          </button>
          <div class="flex flex-wrap items-center gap-2 sm:justify-end">
            <span class="rounded-md bg-muted px-2 py-1 text-[11px] font-medium text-muted-foreground">{profile.enabled ? t('settings.enabled') : t('settings.disabled')}</span>
            <label class="relative">
              <span class="sr-only">{t('settings.enabled')}</span>
              <input
                class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand"
                type="checkbox"
                checked={profile.enabled}
                disabled={busy}
                onchange={(event) => onenabledchange?.(profile.profileId, event.currentTarget.checked)}
              />
              <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
            </label>
            <Button ariaLabel={t('settings.runtimeConnection')} variant="ghost" size="icon" onclick={() => onedit?.(profile)}>
              <ChevronRight class="h-4 w-4" />
            </Button>
            <Button ariaLabel={t('settings.delete')} variant="ghost" size="icon" disabled={busy} onclick={() => onremove?.(profile.profileId)}>
              <Trash2 class="h-4 w-4" />
            </Button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <div class="grid gap-3 text-xs leading-5 text-muted-foreground md:grid-cols-2">
    <div class="rounded-lg border border-border bg-muted/20 px-4 py-3">
      <span class="font-medium text-foreground">{t('settings.discoveryOrder')}</span>
      <p class="mt-1">{t('settings.discoveryOrder.desc')}</p>
    </div>
    <div class="rounded-lg border border-border bg-muted/20 px-4 py-3">
      <span class="font-medium text-foreground">{t('settings.perEnvSetup')}</span>
      <p class="mt-1">{t('settings.perEnvSetup.desc')}</p>
    </div>
  </div>
</section>
