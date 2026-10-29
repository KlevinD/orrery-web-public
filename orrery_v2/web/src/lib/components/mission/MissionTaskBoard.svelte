<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type { MissionTaskViewModel } from '#lib/types/view-models'

  interface Props {
    tasks: MissionTaskViewModel[]
  }

  let { tasks }: Props = $props()
  let t = $derived(createTranslator($locale))

  const priorityLabelKeys: Record<string, TranslationKey> = {
    critical: 'workspace.priority.critical',
    high: 'workspace.priority.high',
    medium: 'workspace.priority.medium',
    low: 'workspace.priority.low',
    normal: 'workspace.priority.normal'
  }

  function translatedValue(prefix: 'status', value: string) {
    const key = `${prefix}.${value}` as TranslationKey
    const label = t(key)
    return label === key ? value : label
  }

  function priorityLabel(priority: string) {
    const key = priorityLabelKeys[priority]
    return key ? t(key) : priority
  }
</script>

{#if tasks.length === 0}
  <EmptyState title={t('workspace.noTasks')} description={t('workspace.noTasksDescription')} />
{:else}
  <div class="grid gap-3 xl:grid-cols-2">
    {#each tasks as task (task.id)}
      <article class="panel p-4">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold text-foreground">{task.title}</h2>
          <Badge tone={task.statusTone}>{translatedValue('status', task.status)}</Badge>
          <Badge>{translatedValue('status', task.qaStatus)}</Badge>
        </div>
        <p class="mt-2 text-sm text-muted-foreground">{task.goal}</p>
        <div class="mt-4 grid grid-cols-2 gap-3 text-xs text-muted-foreground md:grid-cols-4">
          <div><span class="text-muted-foreground">{t('workspace.assignee')}</span><div class="mt-1 text-foreground">{task.assigneeName}</div></div>
          <div><span class="text-muted-foreground">{t('workspace.priority')}</span><div class="mt-1 text-foreground">{priorityLabel(task.priority)}</div></div>
          <div><span class="text-muted-foreground">{t('workspace.due')}</span><div class="mt-1 text-foreground">{task.dueAt}</div></div>
          <div><span class="text-muted-foreground">{t('workspace.evidence')}</span><div class="mt-1 text-foreground">{task.evidenceRequirements.length}</div></div>
        </div>
      </article>
    {/each}
  </div>
{/if}
