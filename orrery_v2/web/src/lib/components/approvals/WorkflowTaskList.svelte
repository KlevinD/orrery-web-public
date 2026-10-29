<script lang="ts">
  import { Clock, FileText, UserRound } from 'lucide-svelte'
  import { approvalActorDisplayName, approvalTitleLabel, workflowRevisionLabel } from './approvalDisplayLabels'
  import Badge from '#lib/components/ui/Badge.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { HILWorkflowInstanceViewModel } from '#lib/types/view-models'
  import { workflowStageLabel, workflowStatusLabel } from './workflowLabels'

  interface Props {
    items: HILWorkflowInstanceViewModel[]
    selectedId?: string
    onselect?: (instanceId: string) => void
    class?: string
  }

  let { items, selectedId = '', onselect, class: className = '' }: Props = $props()
  let t = $derived(createTranslator($locale))
</script>

<section data-workflow-task-list class="min-h-0 overflow-hidden rounded-md border border-border bg-card {className}">
  {#if items.length === 0}
    <div class="flex min-h-64 flex-col items-center justify-center gap-2 px-5 text-center text-muted-foreground">
      <FileText class="h-6 w-6" />
      <p class="text-[13px] font-medium text-foreground">{t('approvals.empty')}</p>
    </div>
  {:else}
    <div class="subtle-scrollbar h-full divide-y divide-border/70 overflow-y-auto">
      {#each items as item (item.id)}
        <button
          type="button"
          aria-label={`${approvalTitleLabel(item.title, $locale)} ${workflowStageLabel(item.activeTask?.stageName ?? item.currentStageKey, $locale)}`}
          aria-pressed={item.id === selectedId}
          class="grid w-full grid-cols-[minmax(0,1fr)_auto] gap-3 px-4 py-3 text-left transition-colors {item.id === selectedId ? 'selection-wash' : 'hover:bg-muted/35'}"
          onclick={() => onselect?.(item.id)}
        >
          <span class="min-w-0">
            <span class="block truncate text-[13.5px] font-medium text-foreground">{approvalTitleLabel(item.title, $locale)}</span>
            <span class="mt-1 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
              <span class="truncate">{workflowStageLabel(item.activeTask?.stageName || item.currentStageKey, $locale)}</span>
              <span aria-hidden="true">&middot;</span>
              <span class="flex min-w-0 items-center gap-1"><UserRound class="h-3 w-3 shrink-0" /><span class="truncate">{approvalActorDisplayName(item.activeTask?.assignee || item.requester, $locale)}</span></span>
            </span>
            <span class="mt-1 flex items-center gap-1 text-[10.5px] text-muted-foreground/80">
              <Clock class="h-3 w-3" />{item.activeTask?.deadlineAt || item.updatedAt}
            </span>
          </span>
          <span class="flex flex-col items-end gap-1.5">
            <Badge tone={item.statusTone} class="text-[10px]">{workflowStatusLabel(item.status, $locale)}</Badge>
            <span class="text-[10px] text-muted-foreground">{workflowRevisionLabel(item.revision, $locale)}</span>
          </span>
        </button>
      {/each}
    </div>
  {/if}
</section>
