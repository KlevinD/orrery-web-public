<script lang="ts">
  import { ScanSearch } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import OntologyCursorControls from './OntologyCursorControls.svelte'
  import {
    useOntologyContextEvidenceQuery,
    useOntologyContextFactsQuery,
    useOntologyContextObjectQuery,
    useOntologyContextRelationsQuery
  } from '#lib/queries/ontology-context'
  import { useOntologyLineageAssertionsQuery } from '#lib/queries/ontology-lineage'
  import { objectTypeLabel, ontologyValueLabel, propertyLabel } from '#lib/ontology-workbench/display-labels'

  interface Props { tenantId: string; objectId: string; onexpand?: (id: string) => void }
  let { tenantId, objectId, onexpand }: Props = $props()
  let tab = $state('summary')
  let boundObject = $state('')
  let factCursor = $state('')
  let factHistory = $state<string[]>([])
  let relationCursor = $state('')
  let relationHistory = $state<string[]>([])
  let relationDirection = $state<'in' | 'out' | 'both'>('both')
  let relationLinkTypeDraft = $state('')
  let relationLinkType = $state('')
  let evidenceCursor = $state('')
  let evidenceHistory = $state<string[]>([])
  let selectedFactId = $state('')
  let assertionCursor = $state('')
  let assertionHistory = $state<string[]>([])

  const detail = useOntologyContextObjectQuery(() => tenantId, () => objectId)
  const facts = useOntologyContextFactsQuery(
    () => tenantId,
    () => objectId,
    () => ({ limit: 25, cursor: factCursor || undefined }),
    () => tab === 'facts'
  )
  const relations = useOntologyContextRelationsQuery(
    () => tenantId,
    () => objectId,
    () => ({ direction: relationDirection, linkType: relationLinkType || undefined, limit: 25, cursor: relationCursor || undefined }),
    () => tab === 'relations'
  )
  const evidence = useOntologyContextEvidenceQuery(
    () => tenantId,
    () => objectId,
    () => ({ limit: 25, cursor: evidenceCursor || undefined }),
    () => tab === 'evidence'
  )
  const assertions = useOntologyLineageAssertionsQuery(
    () => tenantId,
    () => selectedFactId,
    () => ({ limit: 10, cursor: assertionCursor || undefined }),
    () => tab === 'facts' && selectedFactId.length > 0
  )

  $effect(() => {
    const objectKey = JSON.stringify([tenantId, objectId])
    if (objectKey === boundObject) return
    boundObject = objectKey
    tab = 'summary'
    factCursor = ''
    factHistory = []
    relationCursor = ''
    relationHistory = []
    relationDirection = 'both'
    relationLinkTypeDraft = ''
    relationLinkType = ''
    evidenceCursor = ''
    evidenceHistory = []
    selectedFactId = ''
    assertionCursor = ''
    assertionHistory = []
  })

  function displayValue(value: unknown) {
    if (typeof value === 'string') return value
    const serialized = JSON.stringify(value)
    return serialized === undefined ? '—' : serialized
  }

  function resetFactSelection() { selectedFactId = ''; assertionCursor = ''; assertionHistory = [] }
  function nextFact(cursor: string) { factHistory = [...factHistory, factCursor]; factCursor = cursor; resetFactSelection() }
  function previousFact() { factCursor = factHistory.at(-1) ?? ''; factHistory = factHistory.slice(0, -1); resetFactSelection() }
  function nextRelation(cursor: string) { relationHistory = [...relationHistory, relationCursor]; relationCursor = cursor }
  function previousRelation() { relationCursor = relationHistory.at(-1) ?? ''; relationHistory = relationHistory.slice(0, -1) }
  function nextEvidence(cursor: string) { evidenceHistory = [...evidenceHistory, evidenceCursor]; evidenceCursor = cursor }
  function previousEvidence() { evidenceCursor = evidenceHistory.at(-1) ?? ''; evidenceHistory = evidenceHistory.slice(0, -1) }
  function nextAssertion(cursor: string) { assertionHistory = [...assertionHistory, assertionCursor]; assertionCursor = cursor }
  function previousAssertion() { assertionCursor = assertionHistory.at(-1) ?? ''; assertionHistory = assertionHistory.slice(0, -1) }
  function selectFact(id: string) { selectedFactId = selectedFactId === id ? '' : id; assertionCursor = ''; assertionHistory = [] }
  function applyRelationFilter() { relationLinkType = relationLinkTypeDraft.trim(); relationCursor = ''; relationHistory = [] }
</script>

{#if detail.isPending}
  <LoadingState />
{:else if detail.error}
  <ErrorState message={detail.error.message} />
{:else if !detail.data}
  <EmptyState title="对象不可用" />
{:else}
  <div class="min-w-0 space-y-4">
    <header class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-xs text-muted-foreground">{objectTypeLabel(detail.data.object_type)} <span class="font-mono">{detail.data.object_type}</span></p>
        <h3 class="mt-1 break-words text-lg font-semibold">{detail.data.label}</h3>
        <p class="mt-1 break-all font-mono text-xs text-muted-foreground">{detail.data.id}</p>
      </div>
      {#if onexpand}
        <Tooltip text="以此对象为图谱根节点">
          <Button size="icon" variant="outline" ariaLabel="从此对象展开图谱" title="以此对象为图谱根节点" onclick={() => onexpand?.(objectId)}>
            <ScanSearch class="size-4" aria-hidden="true" />
          </Button>
        </Tooltip>
      {/if}
    </header>

    <Tabs bind:value={tab} tabs={[{ label: '摘要', value: 'summary' }, { label: '事实', value: 'facts' }, { label: '关系', value: 'relations' }, { label: '证据', value: 'evidence' }]} class="max-w-full overflow-x-auto" />

    {#if tab === 'summary'}
      <dl class="divide-y divide-border border-y border-border text-sm">
        <div class="grid gap-1 py-2 sm:grid-cols-[110px_minmax(0,1fr)]"><dt class="text-muted-foreground">规范键</dt><dd class="break-all font-mono text-xs">{detail.data.canonical_key ?? '—'}</dd></div>
        <div class="grid gap-1 py-2 sm:grid-cols-[110px_minmax(0,1fr)]"><dt class="text-muted-foreground">状态</dt><dd>{detail.data.status ? ontologyValueLabel(detail.data.status) : '—'}</dd></div>
        <div class="grid gap-1 py-2 sm:grid-cols-[110px_minmax(0,1fr)]"><dt class="text-muted-foreground">置信度</dt><dd>{detail.data.confidence ?? '—'}</dd></div>
        <div class="grid gap-1 py-2 sm:grid-cols-[110px_minmax(0,1fr)]"><dt class="text-muted-foreground">更新时间</dt><dd>{detail.data.updated_at ?? '—'}</dd></div>
      </dl>
      {#if detail.data.properties && Object.keys(detail.data.properties).length}
        <h4 class="font-medium">属性</h4>
        <dl class="divide-y divide-border border-y border-border text-sm">
          {#each Object.entries(detail.data.properties) as property (property[0])}
            <div class="grid gap-1 py-2 sm:grid-cols-[120px_minmax(0,1fr)]"><dt class="text-muted-foreground">{propertyLabel(property[0])}</dt><dd class="break-words">{displayValue(property[1])}</dd></div>
          {/each}
        </dl>
      {/if}
    {:else if tab === 'facts'}
      {#if facts.isPending}
        <LoadingState />
      {:else if facts.error}
        <ErrorState message={facts.error.message} />
      {:else if !facts.data?.items.length}
        <EmptyState title="暂无事实" />
      {:else}
        <ul class="divide-y divide-border border-y border-border text-sm">
          {#each facts.data.items as fact (fact.id)}
            <li class="py-3">
              <div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="font-medium">{propertyLabel(fact.predicate)}</p>{#if propertyLabel(fact.predicate) !== fact.predicate}<p class="font-mono text-[11px] text-muted-foreground">{fact.predicate}</p>{/if}<p class="mt-1 break-words text-muted-foreground">{displayValue(fact.value ?? '—')}</p></div><button class="shrink-0 text-xs text-primary hover:underline" aria-pressed={selectedFactId === fact.id} onclick={() => selectFact(fact.id)}>断言历史</button></div>
              <p class="mt-2 text-xs text-muted-foreground">{fact.status ?? '—'} · {fact.confidence ?? '—'} · {fact.updated_at ?? '—'}</p>
              <div class="mt-1 text-xs"><a class="text-primary hover:underline" href={`/resources/ontology/lineage?tenant_id=${encodeURIComponent(tenantId)}&fact_id=${encodeURIComponent(fact.id)}`}>查看完整来源</a></div>
            </li>
          {/each}
        </ul>
        <OntologyCursorControls hasPrevious={factHistory.length > 0} hasMore={facts.data.page.has_more} nextCursor={facts.data.page.next_cursor} onprevious={previousFact} onnext={nextFact} />
      {/if}
      {#if selectedFactId}
        <section class="border-t border-border pt-3" aria-label="Assertion history">
          <h4 class="font-medium">断言历史</h4>
          {#if assertions.isPending}<LoadingState />{:else if assertions.error}<ErrorState message={assertions.error.message} />{:else if !assertions.data?.items.length}<EmptyState title="暂无断言历史" />{:else}<ol class="mt-2 divide-y divide-border border-y border-border text-sm">{#each assertions.data.items as item (item.id)}<li class="py-2"><div class="flex justify-between gap-3"><span>{item.status ?? '—'}</span><time class="text-xs text-muted-foreground">{item.observed_at ?? '—'}</time></div><p class="mt-1 break-words text-muted-foreground">{displayValue(item.value ?? '—')}</p></li>{/each}</ol><OntologyCursorControls hasPrevious={assertionHistory.length > 0} hasMore={assertions.data.page.has_more} nextCursor={assertions.data.page.next_cursor} onprevious={previousAssertion} onnext={nextAssertion} />{/if}
        </section>
      {/if}
    {:else if tab === 'relations'}
      <div class="grid gap-2 sm:grid-cols-[120px_minmax(0,1fr)_auto] sm:items-end">
        <label class="space-y-1"><span class="text-xs text-muted-foreground">方向</span><Select value={relationDirection} options={[{ label: '全部', value: 'both' }, { label: '流入', value: 'in' }, { label: '流出', value: 'out' }]} onchange={(event) => { relationDirection = event.currentTarget.value as 'in' | 'out' | 'both'; relationCursor = ''; relationHistory = [] }} /></label>
        <label class="space-y-1"><span class="text-xs text-muted-foreground">关系类型</span><Input value={relationLinkTypeDraft} ariaLabel="关系类型" oninput={(event) => (relationLinkTypeDraft = event.currentTarget.value)} /></label>
        <Button variant="outline" onclick={applyRelationFilter}>筛选</Button>
      </div>
      {#if relations.isPending}<LoadingState />{:else if relations.error}<ErrorState message={relations.error.message} />{:else if !relations.data?.items.length}<EmptyState title="暂无关系" />{:else}<ul class="divide-y divide-border border-y border-border text-sm">{#each relations.data.items as relation (relation.id)}<li class="py-3"><p><span class="font-medium">{relation.link_type_label || relation.link_type}</span><span class="ml-2 text-muted-foreground">{relation.direction === 'in' ? '来自' : '指向'} {relation.other_object.label}</span></p>{#if relation.link_type_label && relation.link_type_label !== relation.link_type}<p class="mt-1 font-mono text-[11px] text-muted-foreground">{relation.link_type}</p>{/if}<p class="mt-1 text-xs text-muted-foreground">{relation.status ? ontologyValueLabel(relation.status) : '—'} · {relation.confidence ?? '—'} · {relation.updated_at ?? '—'}</p></li>{/each}</ul><OntologyCursorControls hasPrevious={relationHistory.length > 0} hasMore={relations.data.page.has_more} nextCursor={relations.data.page.next_cursor} onprevious={previousRelation} onnext={nextRelation} />{/if}
    {:else}
      {#if evidence.isPending}<LoadingState />{:else if evidence.error}<ErrorState message={evidence.error.message} />{:else if !evidence.data?.items.length}<EmptyState title="暂无证据" />{:else}<ul class="divide-y divide-border border-y border-border text-sm">{#each evidence.data.items as item (item.id)}<li class="py-3"><p>{item.summary ?? '未提供摘要'}</p><p class="mt-1 break-all font-mono text-xs text-muted-foreground">{item.source_kind ?? '—'}:{item.source_id ?? '—'}</p><p class="mt-1 text-xs text-muted-foreground">{item.confidence ?? '—'} · {item.observed_at ?? '—'}</p></li>{/each}</ul><OntologyCursorControls hasPrevious={evidenceHistory.length > 0} hasMore={evidence.data.page.has_more} nextCursor={evidence.data.page.next_cursor} onprevious={previousEvidence} onnext={nextEvidence} />{/if}
    {/if}
  </div>
{/if}
