<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type { SettingsViewModel } from '#lib/types/view-models'

  interface Props {
    draft: SettingsViewModel
    busy?: boolean
    onsave?: () => void
  }

  let { draft = $bindable(), busy = false, onsave }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<section class="space-y-4 rounded-xl border border-border bg-card p-4 sm:p-6">
  <div>
    <h2 class="text-sm font-semibold text-foreground">{t('settings.rules')}</h2>
    <p class="mt-1 text-xs text-muted-foreground">{t('settings.configureNotifications')}</p>
  </div>
  {#each draft.notificationRules as rule}
    <label class="group flex items-center justify-between gap-4 rounded-lg p-3 -mx-3 hover:bg-muted/30 transition-colors cursor-pointer">
      <div>
        <span class="block text-sm font-medium text-foreground">{t(rule.labelKey as TranslationKey)}</span>
        <span class="text-xs text-muted-foreground">{t(rule.descriptionKey as TranslationKey)}</span>
      </div>
      <div class="relative">
        <input class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand" type="checkbox" bind:checked={rule.enabled} />
        <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
      </div>
    </label>
  {/each}
  <div class="flex justify-end border-t border-border pt-4">
    <Button disabled={busy} onclick={onsave}>{t('settings.save')}</Button>
  </div>
</section>
