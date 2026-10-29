<script lang="ts">
  import { Package, Plus, Trash2, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SkillBundleViewModel, SkillViewModel } from '#lib/types/view-models'

  interface Props {
    bundle: SkillBundleViewModel
    busy?: boolean
    onadd?: (bundle: SkillBundleViewModel) => void
    onremove?: (bundle: SkillBundleViewModel, skill: SkillViewModel) => void
    ondelete?: (bundle: SkillBundleViewModel) => void
  }

  let { bundle, busy = false, onadd, onremove, ondelete }: Props = $props()
  let t = $derived(createTranslator($locale))
  const memberLabel = $derived(bundle.memberCount === 1
    ? t('resources.skillBundles.memberSingular')
    : t('resources.skillBundles.memberPlural'))

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function descriptionLabel() {
    const description = displayField(bundle.description)
    const isEnglishOnly = /[A-Za-z]/.test(description) && !/[\u3400-\u9fff]/.test(description)
    return $locale === 'zh-CN' && isEnglishOnly
      ? t('resources.skills.bundleDescriptionFallback')
      : description
  }
</script>

<article class="overflow-hidden rounded-xl border border-border bg-card">
  <div class="p-4">
    <div class="flex min-w-0 items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="flex min-w-0 items-center gap-2">
          <Package class="h-4 w-4 shrink-0 text-brand" />
          <h3 class="truncate text-sm font-semibold text-foreground">{bundle.name}</h3>
        </div>
        <p class="mt-2 line-clamp-2 min-h-8 text-xs leading-4 text-muted-foreground">
          {descriptionLabel()}
        </p>
      </div>
      <span class="shrink-0 rounded-md bg-muted px-2 py-1 text-xs font-medium text-muted-foreground">
        {bundle.memberCount} {memberLabel}
      </span>
    </div>

    <div class="mt-4 border-t border-border pt-3">
      <div class="mb-2 text-[11px] font-semibold uppercase text-muted-foreground">
        {t('resources.skillBundles.members')}
      </div>
      {#if bundle.skills.length === 0}
        <div class="py-2 text-xs text-muted-foreground">{t('resources.skillBundles.noAvailableSkills')}</div>
      {:else}
        <div class="grid gap-1.5">
          {#each bundle.skills as skill (skill.id)}
            <div class="flex min-w-0 items-center justify-between gap-2 rounded-md bg-muted/60 px-2.5 py-1.5">
              <div class="min-w-0">
                <div class="truncate text-xs font-medium text-foreground">{skill.name}</div>
                <div class="truncate text-[11px] text-muted-foreground">{skill.id}</div>
              </div>
              <Button
                variant="ghost"
                size="icon"
                class="h-7 w-7 shrink-0"
                disabled={busy}
                ariaLabel={t('resources.skillBundles.removeNamed', { bundle: bundle.name, skill: skill.name })}
                title={t('resources.skillBundles.removeNamed', { bundle: bundle.name, skill: skill.name })}
                onclick={() => onremove?.(bundle, skill)}
              >
                <X class="h-3.5 w-3.5" />
              </Button>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>

  <div class="flex items-center justify-between border-t border-border px-2.5 py-1.5">
    <Button
      variant="ghost"
      size="sm"
      disabled={busy}
      ariaLabel={t('resources.skillBundles.addNamed', { name: bundle.name })}
      onclick={() => onadd?.(bundle)}
    >
      <Plus class="h-3.5 w-3.5" />
      {t('resources.skillBundles.submitAdd')}
    </Button>
    <Button
      variant="ghost"
      size="icon"
      disabled={busy}
      ariaLabel={t('resources.skillBundles.deleteNamed', { name: bundle.name })}
      title={t('resources.skillBundles.deleteNamed', { name: bundle.name })}
      onclick={() => ondelete?.(bundle)}
    >
      <Trash2 class="h-4 w-4" />
    </Button>
  </div>
</article>
