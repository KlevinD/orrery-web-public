<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    text: string
    children: Snippet
    class?: string
  }

  let { text, children, class: className = '' }: Props = $props()
</script>

<div class="relative group inline-flex {className}">
  {@render children()}
  <div
    class="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 hidden px-3 py-1.5 text-xs rounded-md bg-popover text-popover-foreground border shadow-md group-hover:block whitespace-nowrap z-50 pointer-events-none"
    role="tooltip"
  >
    {text}
  </div>
</div>
