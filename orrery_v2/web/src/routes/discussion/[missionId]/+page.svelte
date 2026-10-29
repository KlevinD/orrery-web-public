<script lang="ts">
  import { goto } from '$app/navigation'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { useMissionControlQuery } from '#lib/queries/missions'
  import type { ApiClientError } from '#lib/api/client'

  interface Props {
    data: { missionId: string }
  }

  let { data }: Props = $props()
  const resolvedMissionId = $derived(data.missionId)
  let t = $derived(createTranslator($locale))
  const controlQuery = useMissionControlQuery(() => resolvedMissionId)
  const discussionConversationId = $derived(controlQuery.data?.discussionConversationId ?? '')
  let redirectedConversationId = $state('')

  // Deprecated route alias. Keep until removal is approved; backup: backup/frontend-before-route-consolidation-20260831.
  $effect(() => {
    if (!discussionConversationId || discussionConversationId === redirectedConversationId) return
    redirectedConversationId = discussionConversationId
    void goto(`/discussions/${encodeURIComponent(discussionConversationId)}`, { replaceState: true })
  })
</script>

<svelte:head>
  <title>{t('nav.discussion')} | Orrery</title>
</svelte:head>

{#if controlQuery.isLoading}
  <LoadingState title={t('state.loadingDiscussion')} description="" />
{:else if controlQuery.isError}
  {@const error = controlQuery.error as ApiClientError}
  <ErrorState title={t('state.errorDiscussion')} message={error.message} correlationId={error.correlationId} />
{:else if !discussionConversationId}
  <ErrorState title={t('state.errorDiscussion')} message="Mission discussion is not linked to a group chat yet." />
{:else}
  <LoadingState title={t('state.loadingDiscussion')} description="" />
{/if}
