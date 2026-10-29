<script lang="ts">
  import type { RelationshipHierarchyNode } from '#lib/types/view-models'
  import RelationshipHierarchyNodeComponent from './RelationshipHierarchyNode.svelte'

  interface Props {
    roots: RelationshipHierarchyNode[]
    selectedId?: string
    emptyMessage: string
    onselect?: (id: string) => void
  }

  let { roots, selectedId = '', emptyMessage, onselect }: Props = $props()
</script>

{#if roots.length > 0}
  <div class="hierarchy-viewport" data-relationship-hierarchy-graph>
    <ol class="hierarchy-root">
      {#each roots as root (root.id)}
        <RelationshipHierarchyNodeComponent node={root} {selectedId} {onselect} />
      {/each}
    </ol>
  </div>
{:else}
  <div class="grid min-h-[420px] place-items-center p-6 text-sm text-muted-foreground">{emptyMessage}</div>
{/if}

<style>
  .hierarchy-viewport {
    min-height: 420px;
    overflow: auto;
    padding: 1rem 1.5rem 2rem;
  }

  .hierarchy-root {
    display: flex;
    min-width: max-content;
    justify-content: center;
    padding: 0;
  }

</style>
