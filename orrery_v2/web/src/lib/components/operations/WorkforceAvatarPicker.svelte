<script lang="ts">
  import { Check, ImagePlus, LoaderCircle, Upload } from 'lucide-svelte'
  import { onDestroy } from 'svelte'
  import { operationsApi } from '#lib/api/operations'
  import type { WorkforceAvatarDefaultDTO } from '#lib/types/api'
  import { createTranslator, locale } from '#lib/i18n'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import WorkforceAvatar from './WorkforceAvatar.svelte'

  interface Props {
    profileId?: string
    name: string
    human?: boolean
    class?: string
    textClass?: string
    disabled?: boolean
    onupload?: (file: File) => void
    ondefaultselect?: (defaultId: string) => void
  }

  let {
    profileId = '',
    name,
    human = false,
    class: className = '',
    textClass = 'text-xs',
    disabled = false,
    onupload,
    ondefaultselect
  }: Props = $props()

  let open = $state(false)
  let loading = $state(false)
  let defaultsLoaded = $state(false)
  let defaults = $state<WorkforceAvatarDefaultDTO[]>([])
  let error = $state('')
  let selecting = $state('')
  let uploadInput = $state<HTMLInputElement | undefined>()
  let alive = true
  let t = $derived(createTranslator($locale))
  let uploadInputId = $derived(
    `workforce-avatar-upload-${profileId.replace(/[^a-zA-Z0-9_-]/g, '-') || 'current'}`
  )
  const canManage = $derived(Boolean(profileId.trim() && (onupload || ondefaultselect)))

  onDestroy(() => {
    alive = false
  })

  async function openPicker() {
    if (!canManage || disabled) return
    open = true
    if (defaultsLoaded || loading) return
    loading = true
    error = ''
    try {
      const catalog = await operationsApi.listWorkforceAvatarDefaults()
      if (!alive) return
      defaults = catalog
      defaultsLoaded = true
    } catch (reason) {
      error = reason instanceof Error ? reason.message : t('people.avatarDefaultsLoadFailed')
    } finally {
      loading = false
    }
  }

  function handleUpload(event: Event) {
    const input = (event.currentTarget ?? event.target) as HTMLInputElement | null
    if (!input) return
    const file = input.files?.[0]
    if (!file) return
    open = false
    onupload?.(file)
    // Clear the control after dispatching so the same file can be selected again.
    input.value = ''
  }

  function selectDefault(defaultId: string) {
    if (selecting || disabled) return
    selecting = defaultId
    ondefaultselect?.(defaultId)
    selecting = ''
    open = false
  }

  function defaultLabel(id: string) {
    return id
      .replace(/[-_]+/g, ' ')
      .replace(/\b\w/g, (letter) => letter.toUpperCase())
  }
</script>

<div class="relative h-full w-full">
  <WorkforceAvatar profileId={profileId} name={name} {human} class={className} {textClass} />
  {#if canManage}
    <button
      type="button"
      class="absolute -bottom-1 -right-1 grid h-7 w-7 place-items-center rounded-full border-2 border-background bg-foreground text-background shadow-sm transition-colors hover:bg-foreground/80 disabled:cursor-wait disabled:opacity-70"
      aria-label={t('people.changeAvatar')}
      title={t('people.changeAvatar')}
      disabled={disabled}
      onclick={() => void openPicker()}
    >
      <ImagePlus class="h-3.5 w-3.5" />
    </button>
  {/if}
</div>

<input
  id={uploadInputId}
  bind:this={uploadInput}
  class="sr-only"
  type="file"
  accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp"
  aria-label={t('people.avatarUploadLocal')}
  disabled={disabled}
  onchange={handleUpload}
/>

<Dialog open={open} title={t('people.avatarPickerTitle')} size="wide" onClose={() => (open = false)}>
  <div class="space-y-5">
    <div>
      <p class="text-sm text-muted-foreground">{t('people.avatarPickerDescription')}</p>
      {#if error}
        <p class="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>
      {/if}
    </div>

    {#if loading}
      <div class="flex min-h-36 items-center justify-center text-sm text-muted-foreground">
        <LoaderCircle class="mr-2 h-4 w-4 animate-spin" />{t('state.loadingWorkforce')}
      </div>
    {:else if defaults.length > 0}
      <div class="grid grid-cols-3 gap-3 sm:grid-cols-4 md:grid-cols-6">
        {#each defaults as avatar (avatar.id)}
          <button
            type="button"
            class="group relative overflow-hidden rounded-lg border border-border bg-muted/20 p-1 text-left transition-colors hover:border-brand hover:bg-brand/5 disabled:cursor-wait disabled:opacity-60"
            aria-label={defaultLabel(avatar.id)}
            title={defaultLabel(avatar.id)}
            disabled={Boolean(selecting) || disabled}
            onclick={() => selectDefault(avatar.id)}
          >
            <img
              class="aspect-square w-full rounded-md object-cover"
              src={avatar.avatar_url}
              alt={defaultLabel(avatar.id)}
              loading="lazy"
            />
            {#if selecting === avatar.id}
              <span class="absolute right-2 top-2 grid h-5 w-5 place-items-center rounded-full bg-brand text-brand-foreground">
                <Check class="h-3 w-3" />
              </span>
            {/if}
          </button>
        {/each}
      </div>
    {:else if !error}
      <div class="flex min-h-36 items-center justify-center text-sm text-muted-foreground">{t('people.avatarDefaultsEmpty')}</div>
    {/if}

    <div class="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
      <p class="text-xs text-muted-foreground">{t('people.avatarPickerUploadHint')}</p>
      <label
        for={uploadInputId}
        class="relative inline-flex h-9 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-md border border-border bg-card px-4 py-2 text-sm font-medium transition-colors hover:border-foreground/15 hover:bg-muted hover:text-foreground focus-within:outline-none focus-within:ring-1 focus-within:ring-ring {disabled ? 'pointer-events-none cursor-not-allowed opacity-50' : ''}"
        aria-label={t('people.avatarUploadLocal')}
        aria-disabled={disabled}
      >
        <Upload class="h-4 w-4" />
        {t('people.avatarUploadLocal')}
      </label>
    </div>
  </div>
</Dialog>
