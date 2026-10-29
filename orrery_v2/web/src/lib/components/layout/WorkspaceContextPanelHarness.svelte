<script lang="ts">
  import QueryProvider from '#lib/components/providers/QueryProvider.svelte'
  import WorkspaceContextPanel from './WorkspaceContextPanel.svelte'
  import WorkInbox from '#lib/components/operations/WorkInbox.svelte'

  interface Props {
    onClose?: () => void
    withMainInbox?: boolean
  }

  let { onClose, withMainInbox = false }: Props = $props()
</script>

<QueryProvider>
  {#if withMainInbox}<WorkInbox />{/if}
  <WorkspaceContextPanel {onClose} />
</QueryProvider>
