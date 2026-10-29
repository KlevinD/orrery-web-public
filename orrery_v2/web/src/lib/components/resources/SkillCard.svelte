<script lang="ts">
  import {
    Ban,
    Download,
    Link2,
    MoreHorizontal,
    Pencil,
    FileCode2,
    Trash2
  } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SkillViewModel } from '#lib/types/view-models'

  interface Props {
    skill: SkillViewModel
    busy?: boolean
    selected?: boolean
    onopen?: (skillId: string) => void
    onedit?: (skillId: string) => void
    onexport?: (skill: SkillViewModel) => void
    onbind?: (skill: SkillViewModel) => void
    ondisable?: (skill: SkillViewModel) => void
    ondelete?: (skill: SkillViewModel) => void
  }

  let {
    skill,
    busy = false,
    selected = false,
    onopen,
    onedit,
    onexport,
    onbind,
    ondisable,
    ondelete
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  let mobileActionsOpen = $state(false)

  const visibleTags = $derived(skill.taskTypes.slice(0, 3))
  const hiddenTagCount = $derived(Math.max(skill.taskTypes.length - visibleTags.length, 0))
  const visibleTools = $derived(skill.requiredTools.slice(0, 3))
  const hiddenToolCount = $derived(Math.max(skill.requiredTools.length - visibleTools.length, 0))
  const resourceCounters = $derived([
    { label: t('resources.skills.skillMd'), count: skill.skillMdPresent ? 1 : 0 },
    { label: t('resources.skills.assets'), count: skill.resourceCounts.assets },
    { label: t('resources.skills.resources'), count: skill.resourceCounts.resources },
    { label: t('resources.skills.references'), count: skill.resourceCounts.references },
    { label: t('resources.skills.scripts'), count: skill.resourceCounts.scripts }
  ].filter((counter) => counter.count > 0))

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function isEnglishOnly(value: string) {
    return /[A-Za-z]/.test(value) && !/[\u3400-\u9fff]/.test(value)
  }

  function descriptionLabel() {
    const description = displayField(skill.description)
    return $locale === 'zh-CN' && isEnglishOnly(description)
      ? t('resources.skills.descriptionFallback')
      : description
  }

  function ownerLabel() {
    return skill.boundAgents[0] || t('resources.skills.systemOwner')
  }
</script>

<article class="group relative overflow-hidden bg-card transition-colors hover:bg-muted/30 {selected ? 'selection-wash' : ''}">
  <button
    type="button"
    class="grid min-w-0 w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3 text-left lg:grid-cols-[minmax(0,1fr)_90px_120px_110px_80px]"
    aria-label={t('resources.skills.openDetailsFor', { name: skill.name })}
    onclick={() => onopen?.(skill.id)}
  >
    <span class="col-span-2 flex min-w-0 items-center gap-2.5 pr-8 lg:col-span-1 lg:pr-0">
      <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
        <FileCode2 class="h-4 w-4" />
      </span>
      <span class="min-w-0">
        <span class="block truncate text-[13px] text-foreground">{skill.name}</span>
        <span class="mt-0.5 block truncate text-[11.5px] text-muted-foreground">{descriptionLabel()}</span>
      </span>
    </span>

    <span class="font-mono text-xs text-muted-foreground">
      <span class="mr-1 lg:hidden">{t('resources.skills.version')}</span>v{displayField(skill.version)}
    </span>

    <span class="flex items-center justify-end gap-1.5 text-[12.5px] lg:justify-start {skill.enabled ? 'text-emerald-600' : 'text-muted-foreground'}">
      <span class="h-2 w-2 rounded-full {skill.enabled ? 'bg-emerald-500' : 'bg-muted-foreground/40'}"></span>
      {t(skill.enabled ? 'kpi.enabled' : 'kpi.disabled')}
    </span>

    <span class="hidden truncate text-[12.5px] text-foreground lg:block" title={ownerLabel()}>
      {ownerLabel()}
    </span>

    <span class="hidden text-right font-mono text-xs text-muted-foreground lg:block">
      {skill.usageCount}
    </span>

    <span class="sr-only">
      {#each resourceCounters as counter}
        <span aria-label={`${counter.label}: ${counter.count}`}>
          {counter.label}: {counter.count}
        </span>
      {/each}
      {#each visibleTags as tag}<span>{tag}</span>{/each}
      {#if hiddenTagCount > 0}<span>+{hiddenTagCount}</span>{/if}
      {#each visibleTools as toolId}<span>{toolId}</span>{/each}
      {#if hiddenToolCount > 0}<span>+{hiddenToolCount}</span>{/if}
    </span>
  </button>

  <Button
    variant="ghost"
    size="icon"
    class="!absolute !right-2 !top-2 !h-7 !w-7 lg:!hidden"
    ariaLabel={t('discussionSession.moreActions')}
    onclick={() => (mobileActionsOpen = !mobileActionsOpen)}
  >
    <MoreHorizontal class="h-4 w-4" />
  </Button>

  <div class="{mobileActionsOpen ? 'flex' : 'hidden'} absolute right-2 top-10 z-10 items-center justify-end gap-0.5 rounded-md border border-border bg-card/95 p-0.5 shadow-sm lg:flex lg:right-2 lg:top-1/2 lg:-translate-y-1/2 lg:opacity-0 lg:transition-opacity lg:group-focus-within:opacity-100 lg:group-hover:opacity-100">
    <Button variant="ghost" size="icon" class="!h-7 !w-7" ariaLabel={t('resources.skills.bindNamed', { name: skill.name })} onclick={() => onbind?.(skill)}>
      <Link2 class="h-3.5 w-3.5" />
    </Button>
    <Button variant="ghost" size="icon" class="!h-7 !w-7" ariaLabel={t('resources.skills.editNamed', { name: skill.name })} onclick={() => onedit?.(skill.id)}>
      <Pencil class="h-3.5 w-3.5" />
    </Button>
    <Button variant="ghost" size="icon" class="!h-7 !w-7" disabled={busy} ariaLabel={t('resources.skills.exportNamed', { name: skill.name })} onclick={() => onexport?.(skill)}>
      <Download class="h-3.5 w-3.5" />
    </Button>
    <Button variant="ghost" size="icon" class="!h-7 !w-7" disabled={!skill.enabled || busy} ariaLabel={t('resources.skills.disableNamed', { name: skill.name })} onclick={() => ondisable?.(skill)}>
      <Ban class="h-3.5 w-3.5" />
    </Button>
    <Button variant="ghost" size="icon" class="!h-7 !w-7 text-muted-foreground hover:bg-destructive/10 hover:text-destructive" disabled={busy} ariaLabel={t('resources.skills.deleteNamed', { name: skill.name })} onclick={() => ondelete?.(skill)}>
      <Trash2 class="h-3.5 w-3.5" />
    </Button>
  </div>
</article>
