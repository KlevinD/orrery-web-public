<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SettingsViewModel } from '#lib/types/view-models'
  import { generalToggles } from './settingsConfig'

  interface Props {
    draft: SettingsViewModel
    busy?: boolean
    onsave?: () => void
  }

  let { draft = $bindable(), busy = false, onsave }: Props = $props()
  let t = $derived(createTranslator($locale))

  function toggleChecked(key: string) {
    if (key === 'memory.enabled') return draft.memory.enabled
    return Boolean(draft[key as 'autoCreateAgentProfile' | 'autoLinkEvidence' | 'hilDefaultEnabled'])
  }

  function setToggleChecked(key: string, checked: boolean) {
    if (key === 'memory.enabled') {
      draft.memory.enabled = checked
      return
    }
    draft[key as 'autoCreateAgentProfile' | 'autoLinkEvidence' | 'hilDefaultEnabled'] = checked
  }
</script>

<section class="space-y-6 rounded-xl border border-border bg-card p-4 sm:p-6">
  <div>
    <h2 class="text-sm font-semibold text-foreground">{t('settings.platformInfo')}</h2>
    <p class="mt-1 text-xs text-muted-foreground">{t('settings.general.desc')}</p>
  </div>
  <div class="grid gap-4 md:grid-cols-2">
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.platformName')}</span>
      <Input bind:value={draft.platformName} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.organizationName')}</span>
      <Input bind:value={draft.organizationName} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('topbar.language')}</span>
      <Select bind:value={draft.language} options={[{ label: t('topbar.language.zh'), value: 'zh-CN' }, { label: t('topbar.language.en'), value: 'en-US' }]} />
    </label>
    <label class="space-y-1.5 md:col-span-2">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.workspaceRoot')}</span>
      <Input bind:value={draft.workspaceRoot} placeholder={t('settings.workspaceRoot.placeholder')} />
      <span class="block text-xs text-muted-foreground">{t('settings.workspaceRoot.desc')}</span>
    </label>
    <label class="space-y-1.5 md:col-span-2">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.workspaceAllowlist')}</span>
      <textarea
        bind:value={draft.workspaceAllowlist}
        placeholder={t('settings.workspaceAllowlist.placeholder')}
        rows="4"
        class="min-h-24 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
      ></textarea>
      <span class="block text-xs text-muted-foreground">{t('settings.workspaceAllowlist.desc')}</span>
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.discussionAgentPairLoopLimit')}</span>
      <Input type="number" bind:value={draft.discussionAgentPairLoopLimit} />
      <span class="block text-xs text-muted-foreground">{t('settings.discussionAgentPairLoopLimit.desc')}</span>
    </label>
  </div>

  <div class="space-y-3 border-t border-border pt-4">
    {#each generalToggles as row}
      <label class="group flex items-center justify-between gap-4 rounded-lg p-3 -mx-3 hover:bg-muted/30 transition-colors cursor-pointer">
        <div>
          <span class="block text-sm font-medium text-foreground">{t(row.title)}</span>
          <span class="text-xs text-muted-foreground">{t(row.desc)}</span>
        </div>
        <div class="relative">
          <input
            class="peer h-5 w-9 cursor-pointer appearance-none rounded-full bg-muted transition-colors checked:bg-brand"
            type="checkbox"
            checked={toggleChecked(row.key)}
            onchange={(event) => setToggleChecked(row.key, event.currentTarget.checked)}
          />
          <div class="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></div>
        </div>
      </label>
    {/each}
  </div>

  <div class="flex justify-end border-t border-border pt-4">
    <Button disabled={busy} onclick={onsave}>{t('settings.save')}</Button>
  </div>
</section>
