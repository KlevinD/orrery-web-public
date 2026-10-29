<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    queue: Snippet
    detail: Snippet
    kind?: 'approval' | 'council'
    detailOnly?: boolean
    class?: string
  }

  let { queue, detail, kind = 'approval', detailOnly = false, class: className = '' }: Props = $props()
</script>

<div
  data-approval-workspace
  data-council-workspace={kind === 'council' ? '' : undefined}
  class:detail-only={detailOnly}
  class="grid min-w-0 min-h-[520px] gap-3 {detailOnly ? '' : 'xl:grid-cols-[18rem_minmax(0,1fr)] 2xl:grid-cols-[19rem_minmax(0,1fr)]'} {className}"
>
  {#if !detailOnly}
  <section data-approval-queue class="min-h-0 overflow-hidden rounded-md border border-border bg-card">
    {@render queue()}
  </section>
  {/if}

  <section data-approval-detail class="min-h-0">
    {@render detail()}
  </section>
</div>

<style>
  .detail-only { container: approval-detail / inline-size; }
  @container approval-detail (max-width: 750px) {
    :global([data-approval-review-layout]) { display: block; }
    :global([data-approval-context]) { border-left: 0; border-top-width: 1px; }
  }
</style>
