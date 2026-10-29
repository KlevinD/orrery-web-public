<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements'

  interface Props {
    id?: string
    value?: string
    placeholder?: string
    ariaLabel?: string
    name?: string
    autocomplete?: HTMLInputAttributes['autocomplete']
    inputmode?: HTMLInputAttributes['inputmode']
    type?: string
    class?: string
    oninput?: (event: Event & { currentTarget: HTMLInputElement }) => void
    ref?: HTMLInputElement
    disabled?: boolean
  }

  let {
    id = undefined,
    value = $bindable(''),
    placeholder = '',
    ariaLabel = undefined,
    name = undefined,
    autocomplete = undefined,
    inputmode = undefined,
    type = 'text',
    class: className = '',
    oninput,
    ref = $bindable(),
    disabled = false
  }: Props = $props()
</script>

<input
  bind:this={ref}
  {id}
  bind:value
  {placeholder}
  aria-label={ariaLabel}
  {name}
  {autocomplete}
  {inputmode}
  {type}
  {oninput}
  {disabled}
  class="flex h-9 w-full rounded-md border border-border bg-input-background px-3 py-1 text-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground hover:border-foreground/15 hover:bg-card focus-visible:border-[#d8d8d8] focus-visible:bg-card focus-visible:outline-none dark:focus-visible:border-[#3a3a3a] disabled:cursor-not-allowed disabled:opacity-50 {className}"
/>
