<script lang="ts">
  import QueryProvider from '#lib/components/providers/QueryProvider.svelte'
  import Sidebar from './Sidebar.svelte'

  interface Props {
    currentPath?: string
  }

  let { currentPath = '/' }: Props = $props()
</script>

<QueryProvider>
  <Sidebar {currentPath} />
</QueryProvider>
