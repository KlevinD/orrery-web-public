<script lang="ts">
  import type { Snippet } from 'svelte'
  import { QueryClientProvider } from '@tanstack/svelte-query'
  import { queryClient } from '#lib/query-client'

  interface Props {
    children: Snippet
  }

  let { children }: Props = $props()
</script>

<QueryClientProvider client={queryClient}>
  {@render children()}
</QueryClientProvider>
