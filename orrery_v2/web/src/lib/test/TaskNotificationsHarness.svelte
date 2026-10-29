<script lang="ts">
  import { QueryClient, QueryClientProvider } from '@tanstack/svelte-query'
  import TaskNotifications from '#lib/components/workhub/TaskNotifications.svelte'
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
</script>

<QueryClientProvider {client}><TaskNotifications /></QueryClientProvider>
