<script lang="ts">
  import type { Snippet } from 'svelte'
  import { TrendingUp, TrendingDown, Minus } from 'lucide-svelte'

  type LucideIcon = typeof import('lucide-svelte').Rocket

  interface Props {
    label: string
    value: string | number
    subtitle?: string
    icon?: LucideIcon
    trend?: 'up' | 'down' | 'flat'
    trendValue?: string
    compact?: boolean
    class?: string
  }

  let {
    label,
    value,
    subtitle = '',
    icon: Icon,
    trend,
    trendValue = '',
    compact = false,
    class: className = ''
  }: Props = $props()

  const trendColor = $derived(
    trend === 'up'
      ? 'text-status-done'
      : trend === 'down'
        ? 'text-status-failed'
        : 'text-muted-foreground'
  )
</script>

<div class="surface-hover rounded-xl {compact ? 'p-3' : 'p-3.5'} border border-border bg-card {className}">
  <div class="flex items-start justify-between">
    <div class="min-w-0 flex-1">
      <div class="text-xs text-muted-foreground">{label}</div>
      <div class="{compact ? 'mt-0.5 text-xl' : 'mt-1 text-[22px]'} font-semibold leading-none tracking-normal text-foreground">{value}</div>
    </div>
    {#if Icon}
      <div class="flex {compact ? 'h-7 w-7 rounded-md' : 'h-8 w-8 rounded-md'} shrink-0 items-center justify-center bg-brand/10 text-brand">
        <Icon size={compact ? 15 : 18} />
      </div>
    {/if}
  </div>

  {#if subtitle || trend}
    <div class="{compact ? 'mt-1' : 'mt-2'} flex items-center gap-1.5 text-xs text-muted-foreground">
      {#if trend === 'up'}
        <TrendingUp size={14} class={trendColor} />
      {:else if trend === 'down'}
        <TrendingDown size={14} class={trendColor} />
      {:else if trend === 'flat'}
        <Minus size={14} class={trendColor} />
      {/if}
      {#if trendValue}
        <span class={trendColor} class:font-medium={trend === 'up' || trend === 'down'}>{trendValue}</span>
      {/if}
      {#if trendValue && subtitle}
        <span class="text-muted-foreground/60">&middot;</span>
      {/if}
      {#if subtitle}
        <span>{subtitle}</span>
      {/if}
    </div>
  {/if}
</div>
