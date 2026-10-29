<script lang="ts">
  import { QueryClient, QueryClientProvider } from '@tanstack/svelte-query'
  import HomePage from '../../routes/+page.svelte'
  let { initialTab = 'focus', dedicated = false }: { initialTab?: 'focus' | 'ideas' | 'team'; dedicated?: boolean } = $props()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
</script>
<QueryClientProvider {client}><HomePage {initialTab} {dedicated} /></QueryClientProvider>
