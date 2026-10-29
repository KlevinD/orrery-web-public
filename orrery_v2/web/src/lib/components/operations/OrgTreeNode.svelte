<script lang="ts">
  import { ChevronDown, ChevronRight, Lock } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import type { OrganizationTreeViewModel } from '#lib/types/view-models'
  import OrgTreeNode from './OrgTreeNode.svelte'

  interface Props {
    node: OrganizationTreeViewModel
    selectedId?: string
    depth?: number
    onselect?: (id: string) => void
  }

  let { node, selectedId = '', depth = 0, onselect }: Props = $props()
  let expanded = $state(true)

  const children = $derived(node.children ?? [])
  const isSelected = $derived(node.id === selectedId)
  const rowClasses = $derived(
    [
      'flex min-h-8 w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-[13px] transition-colors',
      isSelected
        ? 'selection-wash font-medium text-foreground'
        : 'text-foreground/75 hover:bg-muted/60 hover:text-foreground'
    ].join(' ')
  )

  function selectNode() {
    onselect?.(node.id)
  }

  function toggleExpanded(event: MouseEvent) {
    event.stopPropagation()
    expanded = !expanded
  }
</script>

<div class="min-w-0" style={`padding-left: ${Math.min(depth, 6) * 0.5}rem`}>
  <div class={rowClasses} role="button" tabindex="0" title={node.displayCode || node.code || '--'} onclick={selectNode} onkeydown={(event) => event.key === 'Enter' && selectNode()}>
    {#if children.length > 0}
      <Tooltip text={expanded ? '收起' : '展开'}>
        <Button size="icon" variant="ghost" class="h-5 w-5 shrink-0" ariaLabel={expanded ? '收起组织' : '展开组织'} onclick={toggleExpanded}>
          {#if expanded}
            <ChevronDown class="h-3.5 w-3.5" />
          {:else}
            <ChevronRight class="h-3.5 w-3.5" />
          {/if}
        </Button>
      </Tooltip>
    {:else}
      <span class="h-5 w-5 shrink-0"></span>
    {/if}

    <Lock class="h-3 w-3 shrink-0 text-muted-foreground/55" />
    <span class="min-w-0 flex-1 truncate">{node.name}</span>
    <span class="ml-auto shrink-0 rounded bg-status-done-bg px-1 py-0.5 text-[9.5px] leading-none text-status-done">{node.status || 'active'}</span>
  </div>

  {#if expanded && children.length > 0}
    <div class="mt-0.5 space-y-0.5">
      {#each children as child (child.id)}
        <OrgTreeNode node={child} {selectedId} depth={depth + 1} {onselect} />
      {/each}
    </div>
  {/if}
</div>
