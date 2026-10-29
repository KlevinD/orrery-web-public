<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    header: Snippet
    content: Snippet
    context: Snippet
    actions?: Snippet
    actionsVisible?: boolean
    kind?: 'approval' | 'workflow' | 'council'
    class?: string
  }

  let {
    header,
    content,
    context,
    actions,
    actionsVisible = true,
    kind = 'approval',
    class: className = ''
  }: Props = $props()
</script>

<article
  data-approval-review-shell
  class="rounded-md border border-border bg-card xl:grid xl:h-full xl:min-h-[34rem] xl:grid-rows-[auto_minmax(0,1fr)] xl:overflow-hidden {className}"
>
  <header data-approval-review-header class="border-b border-border/70 px-5 py-4 xl:px-6">
    {@render header()}
  </header>

  <div
    data-approval-review-layout
    data-workflow-review-layout={kind === 'workflow' ? '' : undefined}
    data-council-review-layout={kind === 'council' ? '' : undefined}
    class="min-h-0 xl:grid xl:h-full xl:grid-cols-[minmax(0,1fr)_18rem]"
  >
    <section data-approval-content class="subtle-scrollbar min-w-0 px-5 py-6 xl:overflow-y-auto xl:px-7 xl:py-7">
      <div data-approval-reading-column class="mx-auto w-full max-w-[62rem]">
        {@render content()}
      </div>
    </section>

    <aside data-approval-context class="flex min-h-0 flex-col border-t border-border/70 xl:border-l xl:border-t-0">
      <div data-approval-context-scroll class="subtle-scrollbar min-h-0 p-5 xl:flex-1 xl:overflow-y-auto">
        {@render context()}
      </div>

      {#if actions && actionsVisible}
        <footer data-approval-action-bar class="shrink-0 border-t border-border/70 bg-card p-4">
          {@render actions()}
        </footer>
      {/if}
    </aside>
  </div>
</article>
