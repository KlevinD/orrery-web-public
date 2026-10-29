<script lang="ts">
  import DiscussionWorkspace from '#lib/components/discussions/DiscussionWorkspace.svelte'
  import { createTranslator, locale } from '#lib/i18n'

  interface Props {
    data: { discussionId: string }
  }

  let { data }: Props = $props()
  let t = $derived(createTranslator($locale))
  const discussionId = $derived(data.discussionId)
</script>

<svelte:head>
  <title>{t('nav.discussion')} | Orrery</title>
</svelte:head>

<DiscussionWorkspace initialDiscussionId={discussionId} />
