<script lang="ts">
  import { ChevronLeft, ChevronRight } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  interface Props { hasPrevious?: boolean; hasMore?: boolean; nextCursor?: string; onprevious?: () => void; onnext?: (cursor: string) => void }
  let { hasPrevious = false, hasMore = false, nextCursor = '', onprevious, onnext }: Props = $props()
</script>

{#if hasPrevious || (hasMore && nextCursor)}
  <nav class="mt-3 flex gap-2" aria-label="Pagination">
    {#if hasPrevious}<Button size="sm" variant="outline" onclick={() => onprevious?.()}><ChevronLeft class="size-4" aria-hidden="true" />上一页</Button>{/if}
    {#if hasMore && nextCursor}<Button size="sm" onclick={() => onnext?.(nextCursor)}>下一页<ChevronRight class="size-4" aria-hidden="true" /></Button>{/if}
  </nav>
{/if}
