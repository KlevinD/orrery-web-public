<script lang="ts">
  import { QueryClient, QueryClientProvider } from '@tanstack/svelte-query'
  import WorkCenter from '../../routes/work-items/+page.svelte'
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
</script>
<QueryClientProvider {client}><WorkCenter /></QueryClientProvider>
