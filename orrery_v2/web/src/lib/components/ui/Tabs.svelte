<script lang="ts">
  interface Tab {
    label: string
    mobileLabel?: string
    value: string
  }

  interface Props {
    value?: string
    tabs?: Tab[]
    variant?: 'segmented' | 'underline'
    selectionMaterial?: boolean
    ariaLabel?: string
    class?: string
  }

  let {
    value = $bindable(''),
    tabs = [],
    variant = 'segmented',
    selectionMaterial = false,
    ariaLabel = undefined,
    class: className = ''
  }: Props = $props()
</script>

<div
  role="tablist"
  aria-label={ariaLabel}
  class={variant === 'underline'
    ? `flex w-full items-center gap-5 border-b border-border text-muted-foreground ${className}`
    : `inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground ${className}`}
>
  {#each tabs as tab}
    <button
      type="button"
      role="tab"
      aria-selected={value === tab.value}
      class={variant === 'underline'
        ? '-mb-px inline-flex h-9 items-center justify-center whitespace-nowrap border-b-2 border-transparent px-0 text-[13.5px] font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring data-[active=true]:border-brand data-[active=true]:text-foreground'
        : selectionMaterial
          ? `inline-flex items-center justify-center whitespace-nowrap rounded-md border border-transparent px-3 py-1 text-[13px] font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring data-[active=true]:text-foreground ${value === tab.value ? 'selection-wash' : ''}`
          : 'inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-[13px] font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring data-[active=true]:bg-background data-[active=true]:text-foreground data-[active=true]:shadow-sm'}
      data-active={value === tab.value}
      aria-label={tab.label}
      onclick={() => (value = tab.value)}
    >
      {#if tab.mobileLabel}
        <span class="sm:hidden" aria-hidden="true">{tab.mobileLabel}</span>
        <span class="hidden sm:inline" aria-hidden="true">{tab.label}</span>
      {:else}
        {tab.label}
      {/if}
    </button>
  {/each}
</div>
