<script lang="ts">
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import OntologyCursorControls from './OntologyCursorControls.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import type { SchemaDefinitionKind } from '#lib/api/ontology-schema'
  import { useOntologySchemaDefinitionQuery, useOntologySchemaReferencesQuery } from '#lib/queries/ontology-schema'
  import { attributeLabel, attributeValueLabel, definitionKindLabel, ontologyValueLabel, schemaRelationLabel } from '#lib/ontology-workbench/display-labels'

  interface Props { tenantId: string; kind: SchemaDefinitionKind; definitionId: string; onselect?: (kind: SchemaDefinitionKind, id: string) => void }
  let { tenantId, kind, definitionId, onselect }: Props = $props()
  let cursor = $state('')
  let history = $state<string[]>([])
  let boundDefinition = $state('')
  const detail = useOntologySchemaDefinitionQuery(() => tenantId, () => kind, () => definitionId)
  const references = useOntologySchemaReferencesQuery(() => tenantId, () => kind, () => definitionId, () => ({ direction: 'both', limit: 25, cursor: cursor || undefined }))
  $effect(() => { const key = `${tenantId}\u0000${kind}\u0000${definitionId}`; if (key !== boundDefinition) { boundDefinition = key; cursor = ''; history = [] } })
  function displayAttribute(name: string, value: unknown) { return attributeValueLabel(name, value) }
  function nextPage(nextCursor: string) { history = [...history, cursor]; cursor = nextCursor }
  function previousPage() { cursor = history.at(-1) ?? ''; history = history.slice(0, -1) }
</script>

{#if !definitionId}<EmptyState title="选择定义查看详情" />{:else if detail.isPending}<LoadingState />{:else if detail.error}<ErrorState message={detail.error.message} />{:else if detail.data}
  <div><p class="text-xs text-muted-foreground">{definitionKindLabel(detail.data.kind)} · {ontologyValueLabel(detail.data.origin)} · {ontologyValueLabel(detail.data.status)}</p><h3 class="mt-1 text-lg font-semibold">{detail.data.label}</h3><p class="mt-1 break-all font-mono text-xs text-muted-foreground">{detail.data.id}</p><dl class="mt-4 divide-y divide-border border-y border-border text-sm">{#each Object.entries(detail.data.attributes) as attribute (attribute[0])}{#if attribute[1] !== undefined}<div class="grid gap-1 py-2 sm:grid-cols-[130px_minmax(0,1fr)]"><dt class="text-muted-foreground">{attributeLabel(attribute[0])}</dt><dd class="break-words">{displayAttribute(attribute[0], attribute[1])}</dd></div>{/if}{/each}</dl><h4 class="mt-5 font-medium">关联定义</h4>{#if references.isPending}<LoadingState />{:else if references.error}<ErrorState message={references.error.message} />{:else if !references.data?.items.length}<p class="mt-2 text-sm text-muted-foreground">暂无关联</p>{:else}<ul class="mt-2 divide-y divide-border border-y border-border text-sm">{#each references.data.items as ref (ref.kind + ref.id)}<li class="py-2"><button class="text-left font-medium hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onclick={() => onselect?.(ref.kind, ref.id)}>{ref.label}</button><span class="ml-2 text-muted-foreground">{schemaRelationLabel(ref.relation)} · {ref.direction === 'in' ? '流入' : '流出'}</span></li>{/each}</ul><OntologyCursorControls hasPrevious={history.length > 0} hasMore={references.data.page.has_more} nextCursor={references.data.page.next_cursor} onprevious={previousPage} onnext={nextPage} />{/if}</div>
{/if}
