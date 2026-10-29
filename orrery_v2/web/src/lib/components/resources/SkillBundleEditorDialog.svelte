<script lang="ts">
  import { FileUp, Plus } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SkillBundleEditorSubmission, SkillBundleViewModel, SkillViewModel } from '#lib/types/view-models'

  interface Props {
    open?: boolean
    mode?: 'create' | 'add'
    bundle?: SkillBundleViewModel
    skills?: SkillViewModel[]
    busy?: boolean
    onsubmit?: (submission: SkillBundleEditorSubmission) => void
    oncancel?: () => void
  }

  let {
    open = false,
    mode = 'create',
    bundle = undefined,
    skills = [],
    busy = false,
    onsubmit,
    oncancel
  }: Props = $props()

  let name = $state('')
  let description = $state('')
  let selectedIds = $state<string[]>([])
  let files = $state<File[]>([])
  let resetKey = $state('')
  let t = $derived(createTranslator($locale))
  const title = $derived(mode === 'create' ? t('resources.skillBundles.createTitle') : t('resources.skillBundles.addTitle'))
  const availableSkills = $derived(mode === 'add' && bundle
    ? skills.filter((skill) => !bundle.skillIds.includes(skill.id))
    : skills)
  const canSubmit = $derived(
    !busy && (mode === 'create'
      ? name.trim().length > 0
      : selectedIds.length > 0 || files.length > 0)
  )

  $effect(() => {
    const nextKey = open ? `${mode}:${bundle?.id ?? ''}` : ''
    if (nextKey === resetKey) return
    resetKey = nextKey
    name = mode === 'add' ? bundle?.name ?? '' : ''
    description = mode === 'add' ? bundle?.description ?? '' : ''
    selectedIds = []
    files = []
  })

  function toggleSkill(skillId: string, checked: boolean) {
    selectedIds = checked
      ? [...selectedIds, skillId]
      : selectedIds.filter((id) => id !== skillId)
  }

  function submit() {
    if (!canSubmit) return
    onsubmit?.({
      name: name.trim(),
      description: description.trim(),
      skillIds: selectedIds,
      files
    })
  }
</script>

<Dialog open={open} {title} size="wide" onClose={oncancel}>
  <div class="space-y-5">
    {#if mode === 'create'}
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="grid gap-1.5 text-xs font-medium text-foreground">
          <span>{t('resources.skillBundles.name')}</span>
          <Input
            value={name}
            ariaLabel={t('resources.skillBundles.name')}
            disabled={busy}
            oninput={(event) => (name = event.currentTarget.value)}
          />
        </label>
        <label class="grid gap-1.5 text-xs font-medium text-foreground">
          <span>{t('resources.skillBundles.description')}</span>
          <Input
            value={description}
            ariaLabel={t('resources.skillBundles.description')}
            disabled={busy}
            oninput={(event) => (description = event.currentTarget.value)}
          />
        </label>
      </div>
    {/if}

    <section>
      <div class="mb-2 text-xs font-semibold text-foreground">{t('resources.skillBundles.members')}</div>
      {#if availableSkills.length === 0}
        <div class="rounded-md border border-border bg-muted/50 px-3 py-3 text-xs text-muted-foreground">
          {t('resources.skillBundles.noAvailableSkills')}
        </div>
      {:else}
        <div class="grid max-h-64 gap-2 overflow-y-auto sm:grid-cols-2">
          {#each availableSkills as skill (skill.id)}
            <label class="flex min-w-0 cursor-pointer items-start gap-2 rounded-md border border-border px-3 py-2 hover:bg-muted/50">
              <input
                type="checkbox"
                class="mt-0.5 h-4 w-4 accent-brand"
                aria-label={t('resources.skillBundles.selectNamed', { name: skill.name })}
                checked={selectedIds.includes(skill.id)}
                disabled={busy}
                onchange={(event) => toggleSkill(skill.id, event.currentTarget.checked)}
              />
              <span class="min-w-0">
                <span class="block truncate text-xs font-medium text-foreground">{skill.name}</span>
                <span class="block truncate text-[11px] text-muted-foreground">{skill.id}</span>
              </span>
            </label>
          {/each}
        </div>
      {/if}
    </section>

    <label class="flex cursor-pointer items-center justify-between gap-3 rounded-md border border-dashed border-border px-3 py-3 hover:bg-muted/40">
      <span class="flex min-w-0 items-center gap-2 text-xs font-medium text-foreground">
        <FileUp class="h-4 w-4 shrink-0 text-brand" />
        <span class="truncate">
          {files.length > 0
            ? t('resources.skillBundles.selectedPackages', { count: files.length })
            : t('resources.skillBundles.importPackages')}
        </span>
      </span>
      <input
        type="file"
        class="sr-only"
        accept=".zip,application/zip"
        multiple
        aria-label={t('resources.skillBundles.importPackages')}
        disabled={busy}
        onchange={(event) => (files = Array.from(event.currentTarget.files ?? []))}
      />
    </label>

    <div class="flex justify-end gap-2">
      <Button variant="outline" disabled={busy} onclick={oncancel}>{t('organization.cancel')}</Button>
      <Button disabled={!canSubmit} onclick={submit}>
        <Plus class="h-4 w-4" />
        {mode === 'create' ? t('resources.skillBundles.submitCreate') : t('resources.skillBundles.submitAdd')}
      </Button>
    </div>
  </div>
</Dialog>
