<script lang="ts">
  interface Props {
    value?: number
    class?: string
  }

  let { value = 0, class: className = '' }: Props = $props()

  const clamped = $derived(Math.max(0, Math.min(100, value)))
</script>

<div class="relative h-2 w-full overflow-hidden rounded-full bg-primary/20 {className}">
  <div
    class="h-full bg-primary transition-all duration-300 ease-in-out"
    style="width: {clamped}%"
    role="progressbar"
    aria-valuenow={clamped}
    aria-valuemin={0}
    aria-valuemax={100}
  ></div>
</div>
