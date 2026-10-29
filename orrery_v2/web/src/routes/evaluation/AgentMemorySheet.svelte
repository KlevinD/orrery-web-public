<script lang="ts">
  import { BookOpen, FileText } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import MemoryCitationEvidenceList from '#lib/components/memory/MemoryCitationEvidenceList.svelte'
  import { groupMemoryDiaryRecords, memoryDiaryDate, memoryDiaryPath } from '#lib/memory/diary'
  import { useMemoryCandidatesQuery, useMemoryRecordQuery, useMemoryRecordsQuery } from '#lib/queries/memory'
  import type { ApiClientError } from '#lib/api/client'
  import type { MemoryRecordDTO } from '#lib/types/memory'
  import { modalFocus } from '#lib/actions/modalFocus'

  interface Props {
    open?: boolean
    agentId: string
    agentName: string
    onclose?: () => void
  }

  let {
    open = false,
    agentId,
    agentName,
    onclose
  }: Props = $props()

  let selectedMemoryId = $state('')

  const recordsQuery = useMemoryRecordsQuery(
    () => ({
      scopeKind: 'agent',
      scopeId: agentId,
      lifecycleStatus: 'active',
      limit: 80
    }),
    () => open && agentId.trim().length > 0
  )
  const candidatesQuery = useMemoryCandidatesQuery(
    () => ({
      scopeKind: 'agent',
      scopeId: agentId,
      limit: 50
    }),
    () => open && agentId.trim().length > 0
  )
  const recordQuery = useMemoryRecordQuery(() => selectedMemoryId)
  const records = $derived(recordsQuery.data?.records ?? [])
  const diaryDays = $derived(groupMemoryDiaryRecords(records))
  const candidates = $derived(candidatesQuery.data?.candidates ?? [])
  const selectedRecord = $derived(recordQuery.data)

  $effect(() => {
    if (!open) selectedMemoryId = ''
  })

  function selectRecord(record: MemoryRecordDTO) {
    selectedMemoryId = record.memory_id
  }

  function field(value: string | number | undefined | null) {
    const text = String(value ?? '').trim()
    return text || '未记录'
  }

  function timeLabel(value: string | undefined) {
    if (!value) return '未记录'
    const date = new Date(value)
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
  }

  function toneForStatus(status: string) {
    switch (status) {
      case 'active':
      case 'accepted':
        return 'success'
      case 'proposed':
      case 'settled':
        return 'info'
      case 'archived':
      case 'rejected':
      case 'deleted':
        return 'neutral'
      default:
        return 'warning'
    }
  }

</script>

{#if open}
  <button
    type="button"
    class="fixed inset-0 z-50 bg-black/40"
    aria-label="关闭 Agent 记忆"
    onclick={() => onclose?.()}
  ></button>
  <div
    class="right-sidebar-shell right-sidebar-drawer fixed inset-y-0 right-0 z-50 max-w-5xl"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label={`${agentName} 记忆`}
    use:modalFocus={onclose}
  >
    <header class="right-sidebar-header right-sidebar-header-comfortable items-start">
      <div class="min-w-0">
        <div class="flex items-center gap-2 text-sm font-semibold text-brand">
          <BookOpen class="h-4 w-4" />
          Agent 记忆
        </div>
        <h2 class="mt-2 truncate text-xl font-semibold text-foreground">{agentName}</h2>
        <p class="mt-1 break-all text-xs text-muted-foreground">{agentId}</p>
      </div>
      <Button variant="outline" size="sm" onclick={() => onclose?.()}>关闭</Button>
    </header>

    <div class="right-sidebar-body grid gap-4 overflow-hidden p-4 lg:grid-cols-[minmax(0,1fr)_360px]">
      <div class="min-h-0 space-y-4 overflow-y-auto pr-1">
        <section class="rounded-xl border border-border bg-card p-4">
          <div class="mb-3 flex items-center justify-between gap-3">
            <h3 class="flex items-center gap-2 text-sm font-semibold text-foreground">
              <BookOpen class="h-4 w-4 text-brand" />
              每日记忆日记
            </h3>
            <span class="text-xs text-muted-foreground">{diaryDays.length} 天 / {recordsQuery.data?.total ?? records.length} 条</span>
          </div>
          {#if recordsQuery.isLoading}
            <LoadingState title="正在加载记忆记录" description="按当前 Agent 作用域读取个人记忆。" />
          {:else if recordsQuery.isError}
            {@const error = recordsQuery.error as ApiClientError}
            <ErrorState title="记忆记录加载失败" message={error.message} correlationId={error.correlationId} />
          {:else if records.length === 0}
            <EmptyState title="暂无 Agent 日记" description="该 Agent 还没有按天沉淀的记忆日记，或记忆开关尚未启用。" />
          {:else}
            <div class="grid gap-3">
              {#each diaryDays as day (day.date)}
                <article class="rounded-md border border-border bg-background p-3">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div class="min-w-0">
                      <div class="flex flex-wrap items-center gap-2">
                        <h4 class="font-mono text-sm font-semibold text-foreground">{day.path}</h4>
                        <Badge tone="info">{day.records.length} 条运行记录</Badge>
                      </div>
                      <div class="mt-1 text-xs text-muted-foreground">{day.date}</div>
                    </div>
                  </div>
                  <div class="mt-3 grid gap-2">
                    {#each day.records as record (record.memory_id)}
                      <div class="rounded-md border p-3 transition-colors {selectedMemoryId === record.memory_id ? 'border-brand bg-brand/5' : 'border-border bg-card hover:border-brand/50'}">
                        <div class="flex items-start justify-between gap-3">
                          <div class="min-w-0">
                            <div class="flex flex-wrap items-center gap-2">
                              <span class="min-w-0 truncate text-sm font-semibold text-foreground">{record.summary}</span>
                              <Badge tone={toneForStatus(record.lifecycle_status)}>{record.lifecycle_status}</Badge>
                              <Badge tone="info">{record.layer}</Badge>
                            </div>
                            <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                              <span class="break-all">{record.memory_id}</span>
                              <span>{timeLabel(record.created_at)}</span>
                            </div>
                          </div>
                          <Button variant="outline" size="sm" onclick={() => selectRecord(record)}>查看</Button>
                        </div>
                      </div>
                    {/each}
                  </div>
                </article>
              {/each}
            </div>
          {/if}
        </section>

        <section class="rounded-xl border border-border bg-card p-4">
          <div class="mb-3 flex items-center justify-between gap-3">
            <h3 class="flex items-center gap-2 text-sm font-semibold text-foreground">
              <FileText class="h-4 w-4 text-brand" />
              候选记忆
            </h3>
            <span class="text-xs text-muted-foreground">{candidatesQuery.data?.total ?? candidates.length}</span>
          </div>
          {#if candidatesQuery.isLoading}
            <LoadingState title="正在加载候选记忆" description="读取该 Agent 的待处理记忆候选。" />
          {:else if candidatesQuery.isError}
            {@const error = candidatesQuery.error as ApiClientError}
            <ErrorState title="候选记忆加载失败" message={error.message} correlationId={error.correlationId} />
          {:else if candidates.length === 0}
            <EmptyState title="暂无候选记忆" description="当前 Agent 没有待查看的候选记忆。" />
          {:else}
            <div class="grid gap-3">
              {#each candidates as candidate (candidate.candidate_id)}
                <article class="rounded-md border border-border bg-background p-3">
                  <div class="flex flex-wrap items-center gap-2">
                    <h4 class="min-w-0 truncate text-sm font-semibold text-foreground">{candidate.summary}</h4>
                    <Badge tone={toneForStatus(candidate.status)}>{candidate.status}</Badge>
                    <Badge>{candidate.source_type}</Badge>
                  </div>
                  <div class="mt-2 grid gap-1 text-xs text-muted-foreground md:grid-cols-3">
                    <span class="break-all">{candidate.candidate_id}</span>
                    <span>{field(candidate.topic)}</span>
                    <span>{timeLabel(candidate.created_at)}</span>
                  </div>
                </article>
              {/each}
            </div>
          {/if}
        </section>
      </div>

      <section class="min-h-0 overflow-y-auto rounded-xl border border-border bg-card p-4">
        <h3 class="text-sm font-semibold text-foreground">记忆详情</h3>
        {#if selectedMemoryId && recordQuery.isLoading}
          <LoadingState title="正在加载记忆详情" description={selectedMemoryId} />
        {:else if recordQuery.isError}
          {@const error = recordQuery.error as ApiClientError}
          <ErrorState title="记忆详情加载失败" message={error.message} correlationId={error.correlationId} />
        {:else if selectedRecord}
          <div class="mt-4 space-y-4">
            <div>
              <div class="flex flex-wrap gap-2">
                <Badge tone={toneForStatus(selectedRecord.lifecycle_status)}>{selectedRecord.lifecycle_status}</Badge>
                <Badge tone="info">{selectedRecord.layer}</Badge>
                <Badge>{selectedRecord.sensitivity}</Badge>
              </div>
              <p class="mt-3 text-sm leading-6 text-foreground">{selectedRecord.summary}</p>
            </div>
            <dl class="grid gap-2 text-sm">
              {@render detailRow('日记文件', memoryDiaryPath(selectedRecord, memoryDiaryDate(selectedRecord)))}
              {@render detailRow('Memory ID', selectedRecord.memory_id)}
              {@render detailRow('Profile ID', selectedRecord.profile_id)}
              {@render detailRow('Candidate ID', selectedRecord.candidate_id)}
              {@render detailRow('创建者', selectedRecord.created_by)}
              {@render detailRow('创建时间', timeLabel(selectedRecord.created_at))}
            </dl>
            {@render textBlock('内容', selectedRecord.content || selectedRecord.narrative)}
            {@render listBlock('事实', selectedRecord.facts ?? [])}
            {@render listBlock('经验', selectedRecord.lessons ?? [])}
            {@render listBlock('警告', selectedRecord.warnings ?? [])}
            <MemoryCitationEvidenceList label="引用证据" evidence={selectedRecord.citation_evidence ?? []} />
            {@render listBlock('来源', [...(selectedRecord.source_memory_refs ?? []), ...(selectedRecord.source_evidence_refs ?? [])])}
          </div>
        {:else}
          <div class="mt-4 rounded-md border border-border bg-muted/30 p-4 text-sm leading-6 text-muted-foreground">
            从左侧选择一条记忆记录查看详情。
          </div>
        {/if}
      </section>
    </div>
  </div>
{/if}

{#snippet detailRow(label: string, value: string)}
  <div class="flex items-start justify-between gap-3 border-b border-border pb-2 last:border-b-0">
    <dt class="text-muted-foreground">{label}</dt>
    <dd class="max-w-52 break-words text-right font-medium text-foreground">{field(value)}</dd>
  </div>
{/snippet}

{#snippet textBlock(label: string, value: string | undefined)}
  {#if value}
    <div class="rounded-md border border-border bg-muted/30 p-3">
      <div class="text-xs font-medium text-muted-foreground">{label}</div>
      <p class="mt-2 whitespace-pre-wrap text-sm leading-6 text-foreground">{value}</p>
    </div>
  {/if}
{/snippet}

{#snippet listBlock(label: string, values: string[])}
  {#if values.length > 0}
    <div class="rounded-md border border-border bg-muted/30 p-3">
      <div class="text-xs font-medium text-muted-foreground">{label}</div>
      <ul class="mt-2 space-y-1 text-sm leading-6 text-foreground">
        {#each values as value}
          <li>{value}</li>
        {/each}
      </ul>
    </div>
  {/if}
{/snippet}
