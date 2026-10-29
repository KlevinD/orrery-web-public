<script lang="ts">
  import { onMount } from 'svelte'
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  const target = $derived.by(() => { const params = new URLSearchParams($page.url.search); params.set('view', 'workflows'); return `/work-items?${params}` })
  onMount(() => { void goto(target, { replaceState: true }) })
</script>

<a class="m-6 inline-block text-brand underline" href={target}>事务中心 / Work center</a>
