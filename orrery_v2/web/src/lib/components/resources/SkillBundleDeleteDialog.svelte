<script lang="ts">
  import { AlertTriangle, Trash2 } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SkillBundleViewModel } from '#lib/types/view-models'

  interface Props {
    open?: boolean
    bundle?: SkillBundleViewModel
    busy?: boolean
    onconfirm?: (bundle: SkillBundleViewModel, deleteSkills: boolean) => void
    oncancel?: () => void
  }

  let { open = false, bundle = undefined, busy = false, onconfirm, oncancel }: Props = $props()
  let deleteSkills = $state(false)
  let resetKey = $state('')
  let t = $derived(createTranslator($locale))

  $effect(() => {
    const nextKey = open ? bundle?.id ?? '' : ''
    if (nextKey === resetKey) return
    resetKey = nextKey
    deleteSkills = false
  })
</script>

<Dialog open={open} title={t('resources.skillBundles.deleteTitle')} onClose={oncancel}>
  {#if bundle}
    <div class="space-y-4">
      <div class="flex items-start gap-3 rounded-md border border-destructive/25 bg-destructive/10 px-3 py-2 text-sm">
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <p class="text-foreground">{t('resources.skillBundles.deleteDescription', { name: bundle.name })}</p>
      </div>

      <label class="flex cursor-pointer items-start gap-3 rounded-md border border-border px-3 py-3">
        <input
          type="checkbox"
          class="mt-0.5 h-4 w-4 accent-brand"
          checked={deleteSkills}
          disabled={busy || bundle.memberCount === 0}
          aria-label={t('resources.skillBundles.deleteSkills', { count: bundle.memberCount })}
          onchange={(event) => (deleteSkills = event.currentTarget.checked)}
        />
        <span class="grid gap-1">
          <span class="text-sm font-medium text-foreground">
            {t('resources.skillBundles.deleteSkills', { count: bundle.memberCount })}
          </span>
          <span class="text-xs leading-5 text-muted-foreground">
            {t('resources.skillBundles.deleteSkillsWarning')}
          </span>
        </span>
      </label>

      <div class="flex justify-end gap-2">
        <Button variant="outline" disabled={busy} onclick={oncancel}>{t('organization.cancel')}</Button>
        <Button variant="destructive" disabled={busy} onclick={() => onconfirm?.(bundle, deleteSkills)}>
          <Trash2 class="h-4 w-4" />
          {t('resources.skillBundles.confirmDelete')}
        </Button>
      </div>
    </div>
  {/if}
</Dialog>
