<script lang="ts">
  import { session } from '#lib/stores/session.svelte'
  import { notificationViewerKey } from '#lib/queries/task-notifications'
  import TaskNotificationsView from './TaskNotificationsView.svelte'
  let { initialTab = 'inbox', itemID = '', embedded = false, detailOnly = false, settingsOnly = false }: { initialTab?: string; itemID?: string; embedded?: boolean; detailOnly?: boolean; settingsOnly?: boolean } = $props()
</script>

{#if session.authenticated && session.viewerKind === 'user' && session.user.agentProfileId}
  {#key notificationViewerKey()}
    <TaskNotificationsView {initialTab} {itemID} {embedded} {detailOnly} {settingsOnly} />
  {/key}
{/if}
