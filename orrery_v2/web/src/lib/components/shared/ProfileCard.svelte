<script lang="ts">
  import type { Snippet } from 'svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'

  interface Tag {
    label: string
    tone?: 'neutral' | 'success' | 'warning' | 'danger'
  }

  interface Props {
    name: string
    profileId?: string
    subtitle: string
    source: string
    meta?: string
    status?: string
    tags?: Tag[]
    active?: boolean
    children?: Snippet
    class?: string
  }

  let {
    name,
    profileId = '',
    subtitle,
    source,
    meta = '',
    status = '',
    tags = [],
    active,
    children,
    class: className = ''
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  const isAgent = $derived(source === 'agent')
  const sourceLabel = $derived(isAgent ? t('workspace.agent') : t('workspace.human'))

  /** Resolve the active state: explicit prop wins, otherwise infer from status string. */
  const isActive = $derived(
    active ?? status?.toLowerCase() === 'active'
  )

</script>

<article
  class="surface-hover group rounded-xl border border-border bg-card p-4 {className}"
>
  <div class="flex items-start gap-3">
    <!-- Avatar -->
    <div class="relative shrink-0">
      <WorkforceAvatar profileId={profileId} name={name} human={!isAgent} class="h-12 w-12 rounded-lg" textClass="text-sm" />
      {#if isActive !== undefined}
        <span
          class="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-card {isActive
            ? 'bg-status-done'
            : 'bg-muted-foreground/40'}"
        ></span>
      {/if}
    </div>

    <!-- Body -->
    <div class="min-w-0 flex-1">
      <div class="flex items-center justify-between gap-2">
        <div class="flex items-center gap-2 min-w-0">
          <span class="truncate text-sm font-semibold text-foreground">{name}</span>
          <Badge tone={isAgent ? 'source-agent' : 'source-human'} variant="secondary">
            {sourceLabel}
          </Badge>
        </div>
      </div>

      <div class="mt-0.5 truncate text-xs text-muted-foreground">{subtitle}</div>

      {#if meta}
        <div class="mt-1.5 text-xs text-muted-foreground/80">{meta}</div>
      {/if}
    </div>
  </div>

  <!-- Tags row -->
  {#if tags.length > 0 || status}
    <div class="mt-3 flex flex-wrap items-center gap-1.5 border-t border-border/50 pt-3">
      {#if status}
        <span
          class="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground"
        >
          {status}
        </span>
      {/if}
      {#each tags as tag}
        <Badge tone={tag.tone ?? 'neutral'}>{tag.label}</Badge>
      {/each}
    </div>
  {/if}

  {#if children}
    <div class="mt-3">
      {@render children()}
    </div>
  {/if}
</article>
