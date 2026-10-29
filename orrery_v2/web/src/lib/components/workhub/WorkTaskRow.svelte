<script lang="ts">
  import { Check, Clock3, ExternalLink, MessageSquare, UserRound } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import { createTranslator, locale, type Locale, type TranslationKey } from '#lib/i18n'
  import type { TodoItemDTO, TodoPriority, TodoStatus } from '#lib/types/todo'
  import { allowedTodoStatuses, canCompleteTodo } from './model'

  interface Option {
    label: string
    value: string
  }

  interface Props {
    task: TodoItemDTO
    assigneeOptions: Option[]
    busy?: boolean
    editable?: boolean
    onStatusChange?: (task: TodoItemDTO, status: TodoStatus) => void
    onAssigneeChange?: (task: TodoItemDTO, assigneeID: string) => void
    onComplete?: (task: TodoItemDTO) => void
  }

  let {
    task,
    assigneeOptions,
    busy = false,
    editable = true,
    onStatusChange,
    onAssigneeChange,
    onComplete
  }: Props = $props()

  const t = $derived(createTranslator($locale))
  const statusOptions = $derived(allowedTodoStatuses(task.status))
</script>

<article class="grid min-h-[4.5rem] gap-3 border-b border-border/70 px-3 py-3 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_9.5rem_10.5rem_2.25rem] sm:items-center sm:px-4">
  <div class="min-w-0">
    <div class="flex min-w-0 items-center gap-2">
      <span class="h-2 w-2 shrink-0 rounded-full {statusDot(task.status)}"></span>
      <a class="truncate text-sm font-medium text-foreground hover:text-brand" href={`/todos/${encodeURIComponent(task.todo_id)}`}>
        {task.title}
      </a>
      {#if task.automation?.conversation_id}
        <Tooltip text={t('workhub.automation.openDiscussion')}>
          <a
            class="grid h-6 w-6 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
            href={`/discussions/${encodeURIComponent(task.automation.conversation_id)}`}
            aria-label={`${t('workhub.automation.openDiscussion')}: ${task.title}`}
          >
            <MessageSquare class="h-3.5 w-3.5" />
          </a>
        </Tooltip>
      {:else}
        <ExternalLink class="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
      {/if}
    </div>
    <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 pl-4 text-xs text-muted-foreground">
      <span class="font-mono">{task.issue_number ?? task.todo_id}</span>
      <Badge tone={priorityTone(task.priority)}>{t(`workhub.priority.${task.priority}` as TranslationKey)}</Badge>
      {#if task.due_at}
        <span class="inline-flex items-center gap-1 {dueClasses(task.due_at)}">
          <Clock3 class="h-3 w-3" />
          {dueLabel(task.due_at)}
        </span>
      {/if}
      {#if task.source_idea_id}
        <a class="hover:text-foreground" href={`/ideas/${encodeURIComponent(task.source_idea_id)}`}>{t('workhub.detail.sourceIdea')}</a>
      {/if}
    </div>
  </div>

  {#if editable}
    <select
      class="h-8 w-full rounded-md border border-border bg-input-background px-2 text-xs text-foreground outline-none focus:ring-1 focus:ring-ring"
      value={task.status}
      disabled={busy}
      aria-label={`${t('workhub.form.status')}: ${task.title}`}
      onchange={(event) => onStatusChange?.(task, event.currentTarget.value as TodoStatus)}
    >
      {#each statusOptions as status}
        <option value={status}>{t(`workhub.status.${status}` as TranslationKey)}</option>
      {/each}
    </select>

    <label class="relative block">
      <UserRound class="pointer-events-none absolute left-2 top-2 h-4 w-4 text-muted-foreground" />
      <select
        class="h-8 w-full rounded-md border border-border bg-input-background pl-7 pr-2 text-xs text-foreground outline-none focus:ring-1 focus:ring-ring"
        value={task.assignee_id ?? ''}
        disabled={busy}
        aria-label={`${t('workhub.form.assignee')}: ${task.title}`}
        onchange={(event) => onAssigneeChange?.(task, event.currentTarget.value)}
      >
        {#each assigneeOptions as option}
          <option value={option.value}>{option.label}</option>
        {/each}
      </select>
    </label>
  {:else}
    <span class="text-xs text-muted-foreground">{t(`workhub.status.${task.status}` as TranslationKey)}</span>
    <span class="inline-flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
      <UserRound class="h-3.5 w-3.5 shrink-0" />
      <span class="truncate">{task.assignee_name || task.assignee_id || t('workhub.assignee.unassigned')}</span>
    </span>
  {/if}

  {#if editable && canCompleteTodo(task.status)}
    <Tooltip text={t('workhub.action.complete')}>
      <Button
        variant="ghost"
        size="icon"
        class="h-8 w-8 text-muted-foreground hover:text-status-done"
        disabled={busy}
        ariaLabel={`${t('workhub.action.complete')}: ${task.title}`}
        onclick={() => onComplete?.(task)}
      >
        <Check class="h-4 w-4" />
      </Button>
    </Tooltip>
  {:else}
    <span class="hidden h-8 w-8 sm:block"></span>
  {/if}
</article>

<script lang="ts" module>
  function priorityTone(priority: TodoPriority): 'neutral' | 'info' | 'warning' | 'danger' {
    if (priority === 'urgent') return 'danger'
    if (priority === 'high') return 'warning'
    if (priority === 'medium') return 'info'
    return 'neutral'
  }

  function statusDot(status: TodoStatus) {
    switch (status) {
      case 'in_progress':
        return 'bg-blue-500'
      case 'blocked':
        return 'bg-amber-500'
      case 'review':
        return 'bg-violet-500'
      case 'done':
        return 'bg-emerald-500'
      case 'canceled':
        return 'bg-rose-400'
      default:
        return 'bg-slate-400'
    }
  }

  function dueClasses(value: string) {
    const due = new Date(value)
    if (Number.isNaN(due.getTime())) return ''
    const now = new Date()
    if (due.getTime() < now.getTime()) return 'font-medium text-rose-600 dark:text-rose-300'
    return ''
  }

  function dueLabel(value: string) {
    const due = new Date(value)
    if (Number.isNaN(due.getTime())) return '-'
    const now = new Date()
    const start = new Date(now.getFullYear(), now.getMonth(), now.getDate())
    const dueStart = new Date(due.getFullYear(), due.getMonth(), due.getDate())
    const days = Math.round((dueStart.getTime() - start.getTime()) / 86_400_000)
    const dateLocale: Locale = 'zh-CN'
    if (days === 0) return due.toLocaleTimeString(dateLocale, { hour: '2-digit', minute: '2-digit', hour12: false })
    return due.toLocaleDateString(dateLocale, { month: '2-digit', day: '2-digit' })
  }
</script>
