<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    children: Snippet
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    href?: string
    variant?: 'default' | 'destructive' | 'outline' | 'secondary' | 'ghost' | 'link'
    size?: 'default' | 'sm' | 'lg' | 'icon'
    onclick?: (e: MouseEvent) => void
    ariaLabel?: string
    title?: string
    class?: string
  }

  let {
    children,
    type = 'button',
    disabled = false,
    href = undefined,
    variant = 'default',
    size = 'default',
    onclick,
    ariaLabel = undefined,
    title = undefined,
    class: className = ''
  }: Props = $props()

  const baseClasses =
    'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50'

  const sizeClasses: Record<string, string> = {
    default: 'h-9 px-4 py-2',
    sm: 'h-8 rounded-md px-3 text-[13px]',
    lg: 'h-10 rounded-md px-8',
    icon: 'h-9 w-9'
  }

  const variantClasses: Record<string, string> = {
    default: 'bg-brand text-brand-foreground hover:bg-brand-hover',
    destructive: 'bg-destructive text-destructive-foreground hover:bg-destructive/90',
    outline: 'border border-border bg-card hover:border-foreground/15 hover:bg-muted hover:text-foreground',
    secondary: 'bg-secondary text-secondary-foreground hover:bg-muted',
    ghost: 'hover:bg-accent hover:text-accent-foreground',
    link: 'text-primary underline-offset-4 hover:underline'
  }

  const classes = $derived(
    [
      baseClasses,
      sizeClasses[size] ?? sizeClasses.default,
      variantClasses[variant] ?? variantClasses.default,
      className
    ].join(' ')
  )
</script>

{#if href && !disabled}
  <a
    {href}
    aria-label={ariaLabel}
    {title}
    class={classes}
  >
    {@render children()}
  </a>
{:else}
  <button
    {type}
    {disabled}
    {onclick}
    aria-label={ariaLabel}
    {title}
    class={classes}
  >
    {@render children()}
  </button>
{/if}
