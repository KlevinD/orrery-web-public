<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    children: Snippet
    variant?: 'default' | 'secondary' | 'destructive' | 'outline'
    tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'source-agent' | 'source-human' | 'blue' | 'teal' | 'violet' | 'amber' | 'rose'
    class?: string
  }

  let {
    children,
    variant = undefined,
    tone = undefined,
    class: className = ''
  }: Props = $props()

  const baseClasses =
    'inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium transition-colors'

  const variantClasses: Record<string, string> = {
    default: 'border-transparent bg-brand/10 text-brand dark:bg-brand/15 dark:text-brand',
    secondary: 'border-transparent bg-secondary text-secondary-foreground',
    destructive: 'border-transparent bg-destructive text-destructive-foreground',
    outline: 'border-border text-foreground'
  }

  const toneClasses: Record<string, string> = {
    neutral: 'border-transparent bg-foreground/5 text-muted-foreground dark:bg-foreground/10 dark:text-foreground/70',
    success: 'border-transparent bg-status-done-bg text-status-done',
    warning: 'border-transparent bg-status-blocked-bg text-status-blocked',
    danger: 'border-transparent bg-destructive/10 text-destructive dark:bg-destructive/15',
    info: 'border-transparent bg-status-active-bg text-status-active',
    'source-agent': 'border-transparent bg-source-agent/15 text-source-agent',
    'source-human': 'border-transparent bg-source-human/15 text-source-human',
    blue: 'border-transparent bg-badge-blue-bg text-badge-blue',
    teal: 'border-transparent bg-badge-teal-bg text-badge-teal',
    violet: 'border-transparent bg-badge-violet-bg text-badge-violet',
    amber: 'border-transparent bg-badge-amber-bg text-badge-amber',
    rose: 'border-transparent bg-badge-rose-bg text-badge-rose'
  }

  const classes = $derived(
    [
      baseClasses,
      tone
        ? (toneClasses[tone] ?? toneClasses.neutral)
        : (variantClasses[variant ?? 'default'] ?? variantClasses.default),
      className
    ].join(' ')
  )
</script>

<span class={classes}>
  {@render children()}
</span>
