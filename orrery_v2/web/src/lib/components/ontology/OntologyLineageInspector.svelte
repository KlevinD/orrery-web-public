<script lang="ts">
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import OntologyCursorControls from './OntologyCursorControls.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import { useOntologyLineageAssertionsQuery, useOntologyLineageEvidenceQuery, useOntologyLineageProjectionTraceQuery, useOntologyLineageProvenanceQuery } from '#lib/queries/ontology-lineage'

  interface Props { tenantId: string; factId: string }
  let { tenantId, factId }: Props = $props()
  let tab = $state('assertions')
  let boundFact = $state('')
  let assertionCursor = $state('')
  let assertionHistory = $state<string[]>([])
  let evidenceCursor = $state('')
  let evidenceHistory = $state<string[]>([])
  let traceCursor = $state('')
  let traceHistory = $state<string[]>([])
  const provenance = useOntologyLineageProvenanceQuery(() => tenantId, () => factId)
  const assertions = useOntologyLineageAssertionsQuery(() => tenantId, () => factId, () => ({ limit: 25, cursor: assertionCursor || undefined }), () => tab === 'assertions')
  const evidence = useOntologyLineageEvidenceQuery(() => tenantId, () => factId, () => ({ limit: 25, cursor: evidenceCursor || undefined }), () => tab === 'evidence')
  const trace = useOntologyLineageProjectionTraceQuery(() => tenantId, () => factId, () => ({ limit: 25, cursor: traceCursor || undefined }), () => tab === 'trace')
  $effect(() => { if (factId !== boundFact) { boundFact = factId; tab = 'assertions'; assertionCursor = ''; assertionHistory = []; evidenceCursor = ''; evidenceHistory = []; traceCursor = ''; traceHistory = [] } })
  function displayValue(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value) }
  function nextAssertion(cursor: string) { assertionHistory = [...assertionHistory, assertionCursor]; assertionCursor = cursor }
  function previousAssertion() { assertionCursor = assertionHistory.at(-1) ?? ''; assertionHistory = assertionHistory.slice(0, -1) }
  function nextEvidence(cursor: string) { evidenceHistory = [...evidenceHistory, evidenceCursor]; evidenceCursor = cursor }
  function previousEvidence() { evidenceCursor = evidenceHistory.at(-1) ?? ''; evidenceHistory = evidenceHistory.slice(0, -1) }
  function nextTrace(cursor: string) { traceHistory = [...traceHistory, traceCursor]; traceCursor = cursor }
  function previousTrace() { traceCursor = traceHistory.at(-1) ?? ''; traceHistory = traceHistory.slice(0, -1) }
</script>

{#if provenance.isPending}<LoadingState />{:else if provenance.error}<ErrorState message={provenance.error.message} />{:else if !provenance.data}<EmptyState title="Fact 不可用" />{:else}
  <div class="space-y-5">
    <section class="border-y border-border py-4" aria-label="Provenance anchor">
      <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto_minmax(0,1fr)] lg:items-center">
        <div><p class="text-xs text-muted-foreground">当前事实</p><h3 class="mt-1 font-medium">{provenance.data.fact.predicate}</h3><p class="mt-1 text-xs text-muted-foreground">{provenance.data.fact.subject.label ?? provenance.data.fact.subject.id}</p></div>
        <span class="hidden text-muted-foreground lg:block">→</span>
        <div><p class="text-xs text-muted-foreground">来源事件</p>{#if provenance.data.event}<p class="mt-1 font-medium">{provenance.data.event.type}</p><p class="mt-1 font-mono text-xs text-muted-foreground">{provenance.data.event.id}</p>{:else}<p class="mt-1 text-sm">{provenance.data.provenance_status}</p>{/if}</div>
        <span class="hidden text-muted-foreground lg:block">→</span>
        <div><p class="text-xs text-muted-foreground">映射结果</p><p class="mt-1 font-medium">{provenance.data.mapping.status}</p>{#if provenance.data.mapping.candidates?.length}<ul class="mt-1 text-xs text-muted-foreground">{#each provenance.data.mapping.candidates as candidate (candidate.id)}<li>{candidate.id} · {candidate.origin}</li>{/each}</ul>{/if}</div>
      </div>
    </section>
    <Tabs bind:value={tab} tabs={[{ label: '断言历史', value: 'assertions' }, { label: '证据', value: 'evidence' }, { label: '投影轨迹', value: 'trace' }]} class="max-w-full overflow-x-auto" />
    {#if tab === 'assertions'}
      {#if assertions.isPending}<LoadingState />{:else if assertions.error}<ErrorState message={assertions.error.message} />{:else if !assertions.data?.items.length}<EmptyState title="暂无断言历史" />{:else}<ol class="divide-y divide-border border-y border-border text-sm">{#each assertions.data.items as item (item.id)}<li class="grid gap-2 py-3 sm:grid-cols-[150px_minmax(0,1fr)_auto]"><time class="text-xs text-muted-foreground">{item.observed_at ?? '—'}</time><span class="break-words">{displayValue(item.value ?? '—')}</span><span>{item.status ?? '—'}</span></li>{/each}</ol><OntologyCursorControls hasPrevious={assertionHistory.length > 0} hasMore={assertions.data.page.has_more} nextCursor={assertions.data.page.next_cursor} onprevious={previousAssertion} onnext={nextAssertion} />{/if}
    {:else if tab === 'evidence'}
      {#if evidence.isPending}<LoadingState />{:else if evidence.error}<ErrorState message={evidence.error.message} />{:else if !evidence.data?.items.length}<EmptyState title="暂无可见证据" />{:else}<ul class="divide-y divide-border border-y border-border text-sm">{#each evidence.data.items as item, index (`${item.evidence_id ?? item.ref_id ?? item.status}:${index}`)}<li class="py-3"><div class="flex items-center justify-between gap-3"><span class="font-medium">{item.status}</span><span class="text-xs text-muted-foreground">{item.observed_at ?? '—'}</span></div><p class="mt-1">{item.summary ?? (item.status === 'unavailable' ? '引用不可用' : '未提供摘要')}</p>{#if item.ref_kind && item.ref_id}<p class="mt-1 font-mono text-xs text-muted-foreground">{item.ref_kind}:{item.ref_id}</p>{/if}</li>{/each}</ul><OntologyCursorControls hasPrevious={evidenceHistory.length > 0} hasMore={evidence.data.page.has_more} nextCursor={evidence.data.page.next_cursor} onprevious={previousEvidence} onnext={nextEvidence} />{/if}
    {:else}
      {#if trace.isPending}<LoadingState />{:else if trace.error}<ErrorState message={trace.error.message} />{:else if !trace.data?.items.length}<EmptyState title="暂无关联投影记录" />{:else}<ul class="divide-y divide-border border-y border-border text-sm">{#each trace.data.items as item (item.id)}<li class="py-3"><div class="flex items-center justify-between gap-3"><span class="font-medium">{item.status} · {item.reason_code ?? '—'}</span><span class="text-xs text-muted-foreground">{item.observed_at ?? '—'}</span></div><p class="mt-1 text-muted-foreground">{item.reason ?? item.id}</p></li>{/each}</ul><OntologyCursorControls hasPrevious={traceHistory.length > 0} hasMore={trace.data.page.has_more} nextCursor={trace.data.page.next_cursor} onprevious={previousTrace} onnext={nextTrace} />{/if}
    {/if}
  </div>
{/if}
