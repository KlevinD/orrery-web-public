<script lang="ts">
  import QueryProvider from '#lib/components/providers/QueryProvider.svelte'
  import TopBar from './TopBar.svelte'
  let contextOpen = $state(true)
</script>

<QueryProvider>
  <TopBar {contextOpen} onContextToggle={() => (contextOpen = !contextOpen)} />
  <output data-testid="context-state">{contextOpen ? 'open' : 'closed'}</output>
</QueryProvider>
