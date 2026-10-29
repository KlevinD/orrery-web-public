<script lang="ts">
  import { ExternalLink, Link2 } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import { citationEvidenceKindLabel, citationEvidencePreviewHref, formatCitationEvidence, formatCitationEvidenceTitle } from '#lib/memory/citationEvidence'
  import type { CitationEvidenceDTO } from '#lib/types/memory'

  interface Props {
    evidence?: CitationEvidenceDTO[]
    label?: string
  }

  let {
    evidence = [],
    label = 'Citation evidence'
  }: Props = $props()
</script>

{#if evidence.length > 0}
  <div class="rounded-md border border-border bg-muted/30 p-3">
    <div class="flex items-center gap-2 text-xs font-medium text-muted-foreground">
      <Link2 class="h-3.5 w-3.5" />
      <span>{label}</span>
    </div>
    <div class="mt-2 grid gap-2">
      {#each evidence as item, index (`${item.kind}:${item.id}:${index}`)}
        {@const previewHref = citationEvidencePreviewHref(item)}
        <article class="rounded-md border border-border bg-background p-2">
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <Badge tone="info">{citationEvidenceKindLabel(item.kind)}</Badge>
            <span class="min-w-0 break-words text-sm font-medium text-foreground">{formatCitationEvidenceTitle(item)}</span>
            {#if previewHref}
              <a
                class="inline-flex items-center gap-1 break-all text-xs font-medium text-brand hover:underline"
                href={previewHref}
              >
                <ExternalLink class="h-3.5 w-3.5 shrink-0" />
                preview
              </a>
            {/if}
            {#if item.source_uri}
              <a
                class="inline-flex items-center gap-1 break-all text-xs font-medium text-brand hover:underline"
                href={item.source_uri}
                target="_blank"
                rel="noreferrer"
              >
                <ExternalLink class="h-3.5 w-3.5 shrink-0" />
                source
              </a>
            {/if}
          </div>
          <div class="mt-2 flex flex-wrap gap-1.5 text-xs text-muted-foreground">
            {#each formatCitationEvidence(item) as value}
              <span class="max-w-full break-all rounded border border-border bg-muted/40 px-1.5 py-0.5">{value}</span>
            {/each}
          </div>
        </article>
      {/each}
    </div>
  </div>
{/if}
