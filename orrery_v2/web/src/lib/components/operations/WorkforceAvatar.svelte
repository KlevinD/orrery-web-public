<script lang="ts">
  import { Bot, User } from 'lucide-svelte'
  import { loadWorkforceAvatar, workforceAvatarRevisions } from '#lib/avatar/workforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'

  interface Props {
    profileId?: string
    name: string
    human?: boolean
    showIdentity?: boolean
    class?: string
    textClass?: string
  }

  let {
    profileId = '',
    name,
    human = false,
    showIdentity = true,
    class: className = '',
    textClass = 'text-xs'
  }: Props = $props()
  let source = $state('')
  let avatarElement = $state<HTMLElement>()
  let shouldLoad = $state(false)
  let revision = $derived(workforceAvatarRevisions[profileId] ?? 0)
  let fallback = $derived(name.trim().slice(0, 2) || '?')
  let t = $derived(createTranslator($locale))
  let identityLabel = $derived(human ? t('people.identityHuman') : t('people.identityAgent'))

  $effect(() => {
    const element = avatarElement
    if (!element) return
    if (typeof IntersectionObserver === 'undefined') {
      shouldLoad = true
      return
    }
    shouldLoad = false
    const observer = new IntersectionObserver((entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return
      shouldLoad = true
      observer.disconnect()
    }, { rootMargin: '200px' })
    observer.observe(element)
    return () => observer.disconnect()
  })

  $effect(() => {
    const currentProfileId = profileId.trim()
    const currentRevision = revision
    let active = true
    source = ''
    if (currentProfileId && shouldLoad) {
      void loadWorkforceAvatar(currentProfileId, currentRevision).then((nextSource) => {
        if (active) source = nextSource ?? ''
      })
    }
    return () => {
      active = false
    }
  })
</script>

<span
  bind:this={avatarElement}
  data-workforce-avatar
  data-workforce-profile-id={profileId}
  data-workforce-kind={human ? 'human' : 'agent'}
  class="workforce-avatar relative grid shrink-0 place-items-center font-medium text-white {human ? 'bg-source-human' : 'bg-brand'} {textClass} {className}"
  aria-label={`${name} · ${identityLabel}`}
  title={`${name} · ${identityLabel}`}
  role="img"
>
  <span class="avatar-surface absolute inset-0 grid place-items-center overflow-hidden" aria-hidden="true">
    <span>{fallback}</span>
    {#if source}
      <img class="absolute inset-0 h-full w-full object-cover" src={source} alt="" />
    {/if}
  </span>
  {#if showIdentity}
    <span
      data-workforce-identity={human ? 'human' : 'agent'}
      class="identity-marker {human ? 'bg-source-human' : 'bg-source-agent'}"
      aria-hidden="true"
    >
      {#if human}<User />{:else}<Bot />{/if}
    </span>
  {/if}
</span>
