<script lang="ts">
  import { page } from '$app/stores'
  import DiscussionWorkspace from '#lib/components/discussions/DiscussionWorkspace.svelte'
  import { createTranslator, locale } from '#lib/i18n'

  let t = $derived(createTranslator($locale))
</script>

<svelte:head>
  <title>{t('nav.discussions')} | Orrery</title>
</svelte:head>

<DiscussionWorkspace
  initialDiscussionId={$page.url.searchParams.get('discussion') ?? ''}
  initialAssistant={$page.url.searchParams.get('assistant') === '1'}
  initialDraft={$page.state.discussionComposer?.draft ?? ''}
  initialSendImmediately={$page.state.discussionComposer?.sendImmediately ?? false}
  initialDirectTargetId={$page.state.discussionComposer?.directTargetId ?? ''}
  initialDirectTargetKind={$page.state.discussionComposer?.directTargetKind}
  initialDirectTargetName={$page.state.discussionComposer?.directTargetName ?? ''}
/>
