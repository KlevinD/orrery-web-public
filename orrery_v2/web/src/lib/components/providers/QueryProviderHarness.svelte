<script lang="ts">
  import { useQueryClient } from '@tanstack/svelte-query'
  import QueryProvider from './QueryProvider.svelte'

  function clientStatus(): string {
    const client = useQueryClient()
    return client ? 'query-client-ready' : 'query-client-missing'
  }
</script>

<QueryProvider>
  <div>{clientStatus()}</div>
</QueryProvider>
