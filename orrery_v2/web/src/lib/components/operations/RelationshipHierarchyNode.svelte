<script lang="ts">
  import { Building2, ChevronDown, ChevronRight } from 'lucide-svelte'
  import type { RelationshipHierarchyNode as HierarchyNode } from '#lib/types/view-models'
  import WorkforceAvatar from './WorkforceAvatar.svelte'
  import RelationshipHierarchyNode from './RelationshipHierarchyNode.svelte'

  interface Props {
    node: HierarchyNode
    selectedId?: string
    onselect?: (id: string) => void
  }

  let { node, selectedId = '', onselect }: Props = $props()
  const selected = $derived(node.id === selectedId)
  const children = $derived(node.children ?? [])
  let expanded = $state(true)

  function toggleExpansion(event: MouseEvent) {
    event.stopPropagation()
    expanded = !expanded
  }
</script>

<li class="tree-item">
  <div class="node-row">
    <button
      type="button"
      class:selected
      class="hierarchy-node"
      aria-pressed={selected}
      onclick={() => onselect?.(node.id)}
    >
      <span class:person={node.kind === 'person'} class="node-icon">
        {#if node.kind === 'person'}
          <WorkforceAvatar profileId={node.profileId || node.id} name={node.label} human={node.human} class="h-7 w-7 rounded-md" textClass="text-[10px]" />
        {:else}
          <Building2 class="h-3.5 w-3.5" />
        {/if}
      </span>
      <span class="min-w-0 text-left">
        <span class="block break-words text-xs font-semibold leading-4 text-foreground">{node.label}</span>
        <span class="mt-0.5 block break-words text-[11px] leading-4 text-muted-foreground">{node.subtitle}</span>
        {#if node.detail}
          <span class="mt-1 block line-clamp-2 break-words text-[11px] leading-4 text-foreground/70">{node.detail}</span>
        {/if}
      </span>
    </button>
    {#if children.length > 0}
      <button
        type="button"
        class="expand-toggle"
        aria-expanded={expanded}
        aria-label={expanded ? '折叠下属单位' : '展开下属单位'}
        title={expanded ? '折叠下属单位' : '展开下属单位'}
        onclick={toggleExpansion}
      >
        {#if expanded}
          <ChevronDown class="h-3.5 w-3.5" />
        {:else}
          <ChevronRight class="h-3.5 w-3.5" />
        {/if}
      </button>
    {/if}
  </div>

  {#if expanded && children.length > 0}
    <ol class="hierarchy-children">
      {#each children as child (child.id)}
        <RelationshipHierarchyNode node={child} {selectedId} {onselect} />
      {/each}
    </ol>
  {/if}
</li>

<style>
  .tree-item {
    position: relative;
    display: flex;
    min-width: 11.5rem;
    flex-direction: column;
    align-items: center;
    padding: 1.35rem 0.4rem 0;
  }

  .node-row {
    display: flex;
    width: max-content;
    max-width: 100%;
    align-items: center;
    gap: 0.35rem;
  }

  .tree-item::before {
    position: absolute;
    top: 0;
    left: 50%;
    height: 1.35rem;
    border-left: 1px solid var(--color-border);
    content: '';
  }

  .hierarchy-node {
    display: flex;
    width: 14rem;
    min-height: 3.75rem;
    align-items: flex-start;
    gap: 0.55rem;
    border: 1px solid var(--color-border);
    border-radius: 0.375rem;
    background: var(--color-card);
    padding: 0.65rem;
    transition: border-color 150ms ease, box-shadow 150ms ease;
  }

  .expand-toggle {
    display: grid;
    width: 1.75rem;
    height: 1.75rem;
    flex: 0 0 auto;
    place-items: center;
    border: 1px solid var(--color-border);
    border-radius: 0.25rem;
    background: var(--color-card);
    color: var(--color-muted-foreground);
    transition: border-color 150ms ease, color 150ms ease, background-color 150ms ease;
  }

  .expand-toggle:hover {
    border-color: color-mix(in srgb, var(--color-brand) 60%, var(--color-border));
    background: color-mix(in srgb, var(--color-brand) 6%, var(--color-card));
    color: var(--color-brand);
  }

  .hierarchy-node:hover,
  .hierarchy-node.selected {
    border-color: color-mix(in srgb, var(--color-brand) 60%, var(--color-border));
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-brand) 12%, transparent);
  }

  .node-icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 0.25rem;
    background: color-mix(in srgb, var(--color-brand) 12%, transparent);
    color: var(--color-brand);
    padding: 0.35rem;
  }

  .node-icon.person {
    background: transparent;
    padding: 0;
  }

  .hierarchy-children {
    position: relative;
    display: flex;
    justify-content: center;
    margin-top: 1.35rem;
    padding: 0;
  }

  .hierarchy-children::before {
    position: absolute;
    top: 0;
    right: 12%;
    left: 12%;
    border-top: 1px solid var(--color-border);
    content: '';
  }

</style>
