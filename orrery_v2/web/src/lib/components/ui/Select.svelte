<script lang="ts">
  interface Option {
    label: string
    value: string
  }

  interface Props {
    value?: string
    options?: Option[]
    class?: string
    disabled?: boolean
    onchange?: (event: Event & { currentTarget: HTMLSelectElement }) => void
  }

  let {
    value = $bindable(''),
    options = [],
    class: className = '',
    disabled = false,
    onchange
  }: Props = $props()
</script>

<select
  bind:value
  {disabled}
  {onchange}
  class="flex h-9 w-full items-center rounded-md border border-border bg-input-background px-3 py-1 text-sm transition-colors hover:border-foreground/15 hover:bg-card focus-visible:border-[#d8d8d8] focus-visible:outline-none dark:focus-visible:border-[#3a3a3a] disabled:cursor-not-allowed disabled:opacity-50 {className}"
>
  {#each options as option}
    <option value={option.value}>{option.label}</option>
  {/each}
</select>
