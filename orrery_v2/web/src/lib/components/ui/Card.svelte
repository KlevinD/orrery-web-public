<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    children: Snippet
    class?: string
  }

  let { children, class: className = '' }: Props = $props()
</script>

<section class="rounded-xl border border-border bg-card text-card-foreground {className}">
  {@render children()}
</section>
