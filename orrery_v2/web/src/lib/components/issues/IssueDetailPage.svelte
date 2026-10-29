<script lang="ts">
  import { goto } from '$app/navigation'
  import {
    ArrowLeft,
    ArrowRight,
    Activity,
    Bot,
    CalendarClock,
    Check,
    Clipboard,
    ExternalLink,
    Lightbulb,
    Link2,
    MessageSquare,
    Pencil,
    Play,
    Save,
    Send,
    UserRound,
    X
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import MarkdownEditor from '#lib/components/ui/MarkdownEditor.svelte'
  import HumanTodoDetail from './HumanTodoDetail.svelte'
  import { isHumanTodo, workActionHref } from '#lib/components/workhub/work-actions'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import {
    allowedIdeaStatuses,
    allowedTodoStatuses,
    canConvertIdea,
    dateTimeInputISO,
    dateTimeInputValue,
    normalizeTags,
    todoPriorities,
    workforceAssigneeKind,
    workforceMemberName
  } from '#lib/components/workhub/model'
  import { createTranslator, locale, type Locale, type TranslationKey } from '#lib/i18n'
  import { renderSafeMarkdown } from '#lib/markdown'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useIdeaCommandMutations, useIdeaCommentListQuery, useIdeaDetailQuery } from '#lib/queries/ideas'
  import { useDevelopmentProjectListQuery } from '#lib/queries/development-projects'
  import { useTodoCommandMutations, useTodoCommentListQuery, useTodoDetailQuery } from '#lib/queries/todos'
  import { useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { session } from '#lib/stores/session.svelte'
  import type {
    IdeaEffort,
    IdeaImpact,
    IdeaItemDTO,
    IdeaStatus,
    IdeaVisibility
  } from '#lib/types/idea'
  import type {
    AssigneeKind,
    IssueCommentDTO,
    TodoItemDTO,
    TodoAutomationStatus,
    TodoPriority,
    TodoStatus,
    TodoAutomationMode,
    TodoWorkKind
  } from '#lib/types/todo'
  import type { WorkforceMemberViewModel } from '#lib/types/view-models'

  type IssueKind = 'todo' | 'idea'
  type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'teal' | 'violet' | 'rose'

  interface Props {
    kind: IssueKind
    issueId: string
    embedded?: boolean
  }

  interface SelectOption {
    label: string
    value: string
  }

  interface AssigneeSelection {
    id: string
    name: string
    kind: AssigneeKind
  }

  let { kind, issueId, embedded = false }: Props = $props()

  const t = $derived(createTranslator($locale))
  const canReadWorkforce = $derived(hasPermissionScope(session.permissionScope, 'workforce.read'))
  const canReadDevelopmentProjects = $derived(hasPermissionScope(session.permissionScope, 'development.read'))
  const todoQuery = useTodoDetailQuery(() => (kind === 'todo' ? issueId : undefined))
  $effect(() => { if (!embedded && kind === 'todo' && todoQuery.data && isHumanTodo(todoQuery.data)) void goto(workActionHref(`todo:${issueId}`), { replaceState: true }) })
  const ideaQuery = useIdeaDetailQuery(() => (kind === 'idea' ? issueId : undefined))
  const todoCommentsQuery = useTodoCommentListQuery(() => (kind === 'todo' ? issueId : undefined))
  const ideaCommentsQuery = useIdeaCommentListQuery(() => (kind === 'idea' ? issueId : undefined))
  const workforceQuery = useWorkforceMembersQuery(
    () => ({ enabledOnly: true, includeDetails: true, limit: 500, offset: 0 }),
    () => canReadWorkforce
  )
  const todoCommands = useTodoCommandMutations()
  const ideaCommands = useIdeaCommandMutations()

  let editing = $state(false)
  let editTitle = $state('')
  let editBody = $state('')
  let editTags = $state('')
  let editPriority = $state<TodoPriority>('medium')
  let editAssignee = $state('')
  let editReviewer = $state('')
  let editDueAt = $state('')
  let editAcceptance = $state('')
  let editWorkKind = $state<TodoWorkKind>('general')
  let editAutomationMode = $state<TodoAutomationMode>('manual')
  let editDevelopmentProjectID = $state('')
  let editBlockedReason = $state('')
  let humanResult = $state('')
  let editVisibility = $state<IdeaVisibility>('organization')
  let editImpact = $state<IdeaImpact>('medium')
  let editEffort = $state<IdeaEffort>('medium')
  let editOwner = $state('')
  let editDecision = $state('')
  let selectedTodoStatus = $state<TodoStatus>('inbox')
  let selectedIdeaStatus = $state<IdeaStatus>('draft')
  let transitionReason = $state('')
  let replyBody = $state('')
  let actionMessage = $state('')
  let actionError = $state(false)

  const todo = $derived(todoQuery.data as TodoItemDTO | undefined)
  const idea = $derived(ideaQuery.data as IdeaItemDTO | undefined)
  const developmentProjectsQuery = useDevelopmentProjectListQuery(
    () => ({ status: 'active', limit: 100 }),
    () =>
      canReadDevelopmentProjects &&
      kind === 'todo' &&
      ((editing && editWorkKind === 'development') || Boolean(todo?.development_project_id))
  )
  const loading = $derived(kind === 'todo' ? todoQuery.isLoading : ideaQuery.isLoading)
  const failed = $derived(kind === 'todo' ? todoQuery.isError : ideaQuery.isError)
  const comments = $derived<IssueCommentDTO[]>(
    kind === 'todo' ? (todoCommentsQuery.data?.comments ?? []) : (ideaCommentsQuery.data?.comments ?? [])
  )
  const commentsLoading = $derived(kind === 'todo' ? todoCommentsQuery.isLoading : ideaCommentsQuery.isLoading)
  const commentsFailed = $derived(kind === 'todo' ? todoCommentsQuery.isError : ideaCommentsQuery.isError)
  const workforceMembers = $derived(workforceQuery.data?.members ?? [])
  const developmentProjects = $derived(developmentProjectsQuery.data?.items ?? [])
  const assigneeOptions = $derived(buildAssigneeOptions(workforceMembers))
  const title = $derived(kind === 'todo' ? (todo?.title ?? '') : (idea?.title ?? ''))
  const body = $derived(kind === 'todo' ? (todo?.description ?? '') : (idea?.content ?? ''))
  const issueNumber = $derived(
    (kind === 'todo' ? todo?.issue_number : idea?.issue_number) || (kind === 'todo' ? `TASK-${issueId}` : `IDEA-${issueId}`)
  )
  const status = $derived(kind === 'todo' ? todo?.status : idea?.status)
  const statusLabel = $derived(
    kind === 'todo'
      ? t(`workhub.status.${todo?.status ?? 'inbox'}` as TranslationKey)
      : t(`workhub.ideaStatus.${idea?.status ?? 'draft'}` as TranslationKey)
  )
  const statusTone = $derived(kind === 'todo' ? todoStatusTone(todo?.status) : ideaStatusTone(idea?.status))
  const canSave = $derived(editTitle.trim().length > 0)
  const canReply = $derived(replyBody.trim().length > 0)
  const detailPending = $derived(todoCommands.updateTodo.isPending || ideaCommands.updateIdea.isPending)
  const transitionPending = $derived(todoCommands.moveTodo.isPending || ideaCommands.transitionIdea.isPending || ideaCommands.convertIdeaToTodo.isPending)
  const canApplyTransition = $derived(
    kind === 'todo'
      ? Boolean(todo) && selectedTodoStatus !== todo?.status && (selectedTodoStatus !== 'blocked' || editBlockedReason.trim().length > 0) && (!todo?.source || selectedTodoStatus !== 'done' || humanResult.trim().length > 0)
      : Boolean(idea) &&
          selectedIdeaStatus !== idea?.status &&
          (!['accepted', 'rejected'].includes(selectedIdeaStatus) || transitionReason.trim().length > 0)
  )
  const canConvertCurrentIdea = $derived(
    Boolean(idea) && canConvertIdea(idea?.status ?? 'draft') && Boolean(transitionReason.trim() || idea?.decision_reason?.trim())
  )

  $effect(() => {
    if (editing) return
    syncFields()
  })

  function syncFields() {
    if (kind === 'todo' && todo) {
      editTitle = todo.title
      editBody = todo.description ?? ''
      editTags = (todo.tags ?? []).join(', ')
      editPriority = todo.priority
      editAssignee = todo.assignee_id ?? ''
      editReviewer = todo.reviewer_id ?? ''
      editDueAt = dateTimeInputValue(todo.due_at)
      editAcceptance = todo.acceptance_criteria ?? ''
      editWorkKind = todo.work_kind ?? 'general'
      editAutomationMode = todo.automation_mode ?? 'manual'
      editDevelopmentProjectID = todo.development_project_id ?? ''
      editBlockedReason = todo.blocked_reason ?? ''
      selectedTodoStatus = todo.status
      return
    }
    if (kind === 'idea' && idea) {
      editTitle = idea.title
      editBody = idea.content ?? ''
      editTags = (idea.tags ?? []).join(', ')
      editVisibility = idea.visibility
      editImpact = idea.impact
      editEffort = idea.effort
      editOwner = idea.owner_id ?? ''
      editReviewer = idea.reviewer_id ?? ''
      editDecision = idea.decision_reason ?? ''
      selectedIdeaStatus = idea.status
      transitionReason = idea.decision_reason ?? ''
    }
  }

  function startEditing() {
    syncFields()
    editing = true
    clearMessage()
  }

  function cancelEditing() {
    editing = false
    syncFields()
  }

  function saveIssue() {
    if (!canSave) return
    if (kind === 'todo' && todo) {
      const assignee = assigneeSelection(editAssignee)
      const reviewer = assigneeSelection(editReviewer)
      todoCommands.updateTodo.mutate(
        {
          todoID: todo.todo_id,
          body: {
            title: editTitle.trim(),
            description: editBody.trim(),
            priority: editPriority,
            work_kind: editWorkKind,
            automation_mode: editAutomationMode,
            development_project_id: editWorkKind === 'development' ? editDevelopmentProjectID : '',
            assignee_id: assignee?.id ?? '',
            assignee_name: assignee?.name ?? '',
            assignee_kind: assignee?.kind ?? 'human',
            reviewer_id: reviewer?.id ?? '',
            reviewer_name: reviewer?.name ?? '',
            ...(editDueAt
              ? { due_at: dateTimeInputISO(editDueAt) }
              : { clear_due_at: Boolean(todo.due_at) }),
            acceptance_criteria: editAcceptance.trim(),
            tags: normalizeTags(editTags),
            version: todo.version
          }
        },
        {
          onSuccess: () => {
            editing = false
            showMessage(t('workhub.message.taskUpdated'))
          },
          onError: showError
        }
      )
      return
    }
    if (kind === 'idea' && idea) {
      const owner = assigneeSelection(editOwner)
      const reviewer = assigneeSelection(editReviewer)
      ideaCommands.updateIdea.mutate(
        {
          ideaID: idea.idea_id,
          body: {
            title: editTitle.trim(),
            content: editBody.trim(),
            visibility: editVisibility,
            impact: editImpact,
            effort: editEffort,
            owner_id: owner?.id ?? '',
            owner_name: owner?.name ?? '',
            reviewer_id: reviewer?.id ?? '',
            reviewer_name: reviewer?.name ?? '',
            decision_reason: editDecision.trim(),
            tags: normalizeTags(editTags),
            version: idea.version
          }
        },
        {
          onSuccess: () => {
            editing = false
            showMessage(t('workhub.message.ideaUpdated'))
          },
          onError: showError
        }
      )
    }
  }

  function applyTransition() {
    if (!canApplyTransition) return
    if (kind === 'todo' && todo) {
      todoCommands.moveTodo.mutate(
        {
          todoID: todo.todo_id,
          body: {
            status: selectedTodoStatus,
            result: todo.source && selectedTodoStatus === 'done' ? humanResult.trim() : undefined,
            blocked_reason: selectedTodoStatus === 'blocked' ? editBlockedReason.trim() : undefined,
            version: todo.version
          }
        },
        { onSuccess: () => showMessage(t('workhub.message.taskUpdated')), onError: showError }
      )
      return
    }
    if (kind === 'idea' && idea) {
      const reviewer = assigneeSelection(editReviewer || session.actorId)
      ideaCommands.transitionIdea.mutate(
        {
          ideaID: idea.idea_id,
          body: {
            status: selectedIdeaStatus,
            decision_reason: transitionReason.trim() || undefined,
            reviewer_id: reviewer?.id,
            reviewer_name: reviewer?.name,
            version: idea.version
          }
        },
        { onSuccess: () => showMessage(t('workhub.message.ideaTransitioned')), onError: showError }
      )
    }
  }

  function convertCurrentIdea() {
    if (!idea || !canConvertCurrentIdea) return
    ideaCommands.convertIdeaToTodo.mutate(
      {
        ideaID: idea.idea_id,
        body: { decision_reason: transitionReason.trim() || undefined, version: idea.version }
      },
      {
        onSuccess: (result) => {
          showMessage(t('workhub.message.ideaConverted'))
          if (result.todo?.todo_id) void goto(`/todos/${encodeURIComponent(result.todo.todo_id)}`)
        },
        onError: showError
      }
    )
  }

  function submitReply() {
    const body = replyBody.trim()
    if (!body) return
    if (kind === 'todo') {
      todoCommands.createTodoComment.mutate(
        { todoID: issueId, body: { body } },
        {
          onSuccess: () => {
            replyBody = ''
            showMessage(t('home.message.replyCreated'))
          },
          onError: showError
        }
      )
      return
    }
    ideaCommands.createIdeaComment.mutate(
      { ideaID: issueId, body: { body } },
      {
        onSuccess: () => {
          replyBody = ''
          showMessage(t('home.message.replyCreated'))
        },
        onError: showError
      }
    )
  }

  async function copyIssueNumber() {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return
    await navigator.clipboard.writeText(issueNumber)
    showMessage(t('home.message.issueNumberCopied'))
  }

  function buildAssigneeOptions(members: WorkforceMemberViewModel[]): SelectOption[] {
    const options: SelectOption[] = [{ label: t('workhub.assignee.unassigned'), value: '' }]
    const seen = new Set<string>()
    if (session.actorId) {
      options.push({ label: `${t('workhub.assignee.me')} · ${session.displayName || session.actorId}`, value: session.actorId })
      seen.add(session.actorId)
    }
    for (const member of members) {
      if (!member.id || seen.has(member.id)) continue
      seen.add(member.id)
      const role = member.primaryRoleLabel || member.positionDisplayName
      options.push({ label: role ? `${workforceMemberName(member)} · ${role}` : workforceMemberName(member), value: member.id })
    }
    return options
  }

  function optionsWithCurrent(id: string, name: string) {
    if (!id || assigneeOptions.some((option) => option.value === id)) return assigneeOptions
    return [...assigneeOptions, { label: name || id, value: id }]
  }

  function assigneeSelection(id: string): AssigneeSelection | undefined {
    id = id.trim()
    if (!id) return undefined
    if (id === session.actorId) return { id, name: session.displayName || session.username || id, kind: 'human' }
    const member = workforceMembers.find((candidate) => candidate.id === id)
    if (!member) return { id, name: id, kind: 'human' }
    return { id, name: workforceMemberName(member), kind: workforceAssigneeKind(member) }
  }

  function memberForIdentity(id: string) {
    return workforceMembers.find((member) => [member.id, member.profileId, member.agentInstanceId].includes(id))
  }

  function commentProfileId(authorId: string) {
    return memberForIdentity(authorId)?.profileId || authorId
  }

  function commentIsHuman(authorId: string) {
    if (authorId === session.actorId) return true
    const member = memberForIdentity(authorId)
    return member ? workforceAssigneeKind(member) === 'human' : false
  }

  function showMessage(message: string) {
    actionError = false
    actionMessage = message
  }

  function showError(error: unknown) {
    actionError = true
    actionMessage = error instanceof Error ? error.message : t('workhub.message.operationFailed')
  }

  function clearMessage() {
    actionMessage = ''
    actionError = false
  }

  function toolbarLabels() {
    return {
      bold: t('home.markdown.bold'),
      italic: t('home.markdown.italic'),
      code: t('home.markdown.code'),
      heading: t('home.markdown.heading'),
      unorderedList: t('home.markdown.unorderedList'),
      orderedList: t('home.markdown.orderedList'),
      taskList: t('home.markdown.taskList'),
      link: t('home.markdown.link'),
      image: t('home.markdown.image'),
      mention: t('home.markdown.mention'),
      emoji: t('home.markdown.emoji'),
      attach: t('home.markdown.attach')
    }
  }

  function dateTimeLabel(value?: string) {
    if (!value) return '-'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return '-'
    const dateLocale: Locale = $locale === 'en-US' ? 'en-US' : 'zh-CN'
    return date.toLocaleString(dateLocale, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    })
  }

  function todoStatusTone(value?: TodoStatus): BadgeTone {
    if (value === 'done') return 'success'
    if (value === 'canceled') return 'danger'
    if (value === 'blocked') return 'warning'
    if (value === 'review') return 'violet'
    if (value === 'in_progress') return 'info'
    return 'neutral'
  }

  function ideaStatusTone(value?: IdeaStatus): BadgeTone {
    if (value === 'accepted' || value === 'converted') return 'success'
    if (value === 'experiment') return 'teal'
    if (value === 'under_review') return 'violet'
    if (value === 'rejected') return 'rose'
    if (value === 'submitted') return 'warning'
    return 'neutral'
  }

  function priorityTone(value: TodoPriority): BadgeTone {
    if (value === 'urgent') return 'danger'
    if (value === 'high') return 'warning'
    if (value === 'medium') return 'info'
    return 'neutral'
  }

  function startApprovedAutomation() {
    const digest = todo?.automation?.plan_digest?.trim()
    if (!todo || !digest) return
    todoCommands.startAutomation.mutate(
      { todoID: todo.todo_id, planDigest: digest },
      { onSuccess: () => showMessage(t('workhub.automation.started')), onError: showError }
    )
  }

  function percent(value: number | undefined) {
    if (!Number.isFinite(value)) return 0
    const normalized = Number(value) <= 1 ? Number(value) * 100 : Number(value)
    return Math.max(0, Math.min(100, Math.round(normalized)))
  }

  function automationStatusTone(value: TodoAutomationStatus): BadgeTone {
    if (value === 'pending_approval' || value === 'waiting_runtime_approval' || value === 'provisioning_workspace') return 'warning'
    if (value === 'executed' || value === 'approved') return 'success'
    if (value === 'planning_failed' || value === 'execution_failed' || value === 'rejected') return 'danger'
    if (value === 'executing' || value === 'discussing') return 'info'
    return 'neutral'
  }

  function executionStatusTone(value: string | undefined): BadgeTone {
    if (value === 'executed') return 'success'
    if (value === 'execution_failed') return 'danger'
    if (value === 'pause_requested' || value === 'paused' || value === 'waiting_runtime_approval') return 'warning'
    if (value === 'executing' || value === 'dispatching') return 'info'
    return 'neutral'
  }

  function executionStatusLabel(value: string | undefined) {
    return t(`workhub.execution.status.${value || 'creating'}` as TranslationKey)
  }

  function executionReasonLabel(value: string | undefined) {
    if (value === 'Todo changed after the execution plan was prepared') return t('workhub.execution.todoChanged')
    return value ?? ''
  }
</script>

<svelte:head>
  <title>{title || (kind === 'todo' ? t('workhub.detail.task') : t('workhub.detail.idea'))} | Orrery</title>
</svelte:head>

{#if loading}
  <LoadingState title={t('workhub.loading')} description="" />
{:else if failed || !title}
  <ErrorState title={t('workhub.error')} message={t('workhub.message.operationFailed')} />
{:else if kind === 'todo' && todo?.assignee_kind === 'human' && todo.work_kind === 'general' && todo.automation_mode === 'manual' && !editing}
  {#key issueId}<HumanTodoDetail {todo} {comments} {embedded} onmanage={startEditing} onrefresh={() => todoQuery.refetch()} />{/key}
{:else}
  <div class="page-shell min-w-0 max-w-[860px] space-y-4">
    <header class="border-b border-border pb-4">
      <div class="mb-3 flex items-center justify-between gap-3">
        <Button href="/" variant="ghost" size="sm" class="-ml-2 text-muted-foreground">
          <ArrowLeft class="h-4 w-4" />
          {t('workhub.action.back')}
        </Button>
        <div class="flex items-center gap-2">
          {#if editing}
            <Button variant="ghost" size="sm" onclick={cancelEditing}><X class="h-4 w-4" />{t('workhub.action.cancel')}</Button>
            <Button size="sm" disabled={!canSave || detailPending} onclick={saveIssue}><Save class="h-4 w-4" />{t('workhub.action.save')}</Button>
          {:else}
            <Button variant="outline" size="sm" onclick={startEditing}><Pencil class="h-4 w-4" />{t('workhub.action.edit')}</Button>
          {/if}
        </div>
      </div>

      {#if actionMessage}
        <div class="mb-4 flex items-start justify-between gap-3 rounded-md border px-3 py-2 text-sm {actionError ? 'border-rose-200 bg-rose-50 text-rose-800 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-200' : 'border-teal-200 bg-teal-50 text-teal-800 dark:border-teal-900 dark:bg-teal-950/30 dark:text-teal-200'}">
          <span>{actionMessage}</span>
          <button type="button" class="rounded p-0.5 opacity-70 hover:opacity-100" aria-label={t('workhub.action.close')} onclick={clearMessage}><X class="h-4 w-4" /></button>
        </div>
      {/if}

      <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div class="min-w-0 flex-1">
          <div class="mb-2 flex flex-wrap items-center gap-2">
            <Badge tone={statusTone}>{statusLabel}</Badge>
            {#if kind === 'todo' && todo}<Badge tone={priorityTone(todo.priority)}>{t(`workhub.priority.${todo.priority}` as TranslationKey)}</Badge>{/if}
            {#if kind === 'idea' && idea}<Badge tone="neutral">{t(`workhub.visibility.${idea.visibility}` as TranslationKey)}</Badge>{/if}
          </div>
          {#if editing}
            <Input bind:value={editTitle} class="h-10 max-w-4xl text-[22px] font-semibold" ariaLabel={kind === 'todo' ? t('workhub.form.taskTitle') : t('workhub.form.ideaTitle')} />
          {:else}
            <h1 class="break-words text-[22px] font-semibold leading-[33px] text-foreground">{title}</h1>
          {/if}
        </div>
        <button type="button" class="inline-flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 font-mono text-xs text-muted-foreground hover:text-foreground" onclick={copyIssueNumber}>
          <Clipboard class="h-3.5 w-3.5" />
          {issueNumber}
        </button>
      </div>
    </header>

    <div class="grid min-w-0 gap-4 lg:grid-cols-[minmax(0,1fr)_260px]">
      <main class="min-w-0 space-y-4">
        {#if kind === 'todo' && todo?.source}
          <section class="border-y border-border py-3" data-human-handoff>
            <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
              <h2 class="font-semibold">{t('handoff.title')}</h2>
              {#if todo.source.can_open}
                <a class="inline-flex items-center gap-1 hover:underline" href={`/discussions/${encodeURIComponent(todo.source.conversation_id)}`}><Link2 class="h-4 w-4" />{t('handoff.source')}</a>
              {:else}<span class="text-xs text-muted-foreground">{t('handoff.sourceRestricted')}</span>{/if}
            </div>
            <p class="mt-2 text-xs text-muted-foreground" role="status">{t(todo.source.delivery_status === 'delivered' ? 'handoff.delivered' : 'handoff.pending')}</p>
            {#if todo.source.result}
              <h3 class="mt-3 text-sm font-medium">{t('handoff.result')}</h3>
              <p class="mt-1 whitespace-pre-wrap break-words text-sm">{todo.source.result}</p>
            {/if}
          </section>
        {/if}
        {#if kind === 'todo' && todo?.source_idea_id}
          <a class="panel flex items-center justify-between gap-3 px-4 py-3 text-sm hover:border-brand/30" href={`/ideas/${encodeURIComponent(todo.source_idea_id)}`}>
            <span class="inline-flex items-center gap-2"><Link2 class="h-4 w-4 text-amber-600" />{t('workhub.detail.sourceIdea')}</span>
            <span class="inline-flex items-center gap-1 font-mono text-xs text-muted-foreground">{todo.source_idea_id}<ArrowRight class="h-3.5 w-3.5" /></span>
          </a>
        {/if}
        {#if kind === 'idea' && idea?.converted_todo_id}
          <a class="panel flex items-center justify-between gap-3 px-4 py-3 text-sm hover:border-brand/30" href={`/todos/${encodeURIComponent(idea.converted_todo_id)}`}>
            <span class="inline-flex items-center gap-2"><Check class="h-4 w-4 text-emerald-600" />{t('workhub.detail.convertedTask')}</span>
            <span class="inline-flex items-center gap-1 font-mono text-xs text-muted-foreground">{idea.converted_todo_id}<ArrowRight class="h-3.5 w-3.5" /></span>
          </a>
        {/if}

        <section class="panel overflow-hidden" aria-label={t('workhub.detail.description')}>
          <div class="flex items-center justify-between border-b border-border px-4 py-3">
            <h2 class="text-sm font-semibold text-foreground">{t('workhub.detail.description')}</h2>
          </div>
          <div class="p-4 sm:p-5">
            {#if editing}
              <MarkdownEditor
                bind:value={editBody}
                ariaLabel={kind === 'todo' ? t('workhub.form.taskDescription') : t('workhub.form.ideaContent')}
                minHeight="min-h-72"
                writeLabel={t('home.markdown.write')}
                previewLabel={t('home.markdown.preview')}
                toolbarLabels={toolbarLabels()}
              />
              <label class="mt-4 block space-y-1.5">
                <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.tags')}</span>
                <Input bind:value={editTags} ariaLabel={t('workhub.form.tags')} />
              </label>
            {:else if body}
              <div class="prose prose-slate max-w-none text-sm leading-7 dark:prose-invert">{@html renderSafeMarkdown(body)}</div>
            {:else}
              <p class="py-8 text-center text-sm text-muted-foreground">{t('home.detail.noContent')}</p>
            {/if}
          </div>
        </section>

        {#if kind === 'todo' && todo?.automation}
          <section class="panel overflow-hidden" aria-label={t('workhub.automation.title')}>
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
              <h2 class="inline-flex items-center gap-2 text-sm font-semibold text-foreground"><Bot class="h-4 w-4" />{t('workhub.automation.title')}</h2>
              <Badge tone={automationStatusTone(todo.automation.status)}>{t(`workhub.automation.status.${todo.automation.status}` as TranslationKey)}</Badge>
            </div>
            <div class="space-y-4 p-4 sm:p-5">
              {#if todo.automation.plan?.summary}
                <div>
                  <p class="text-sm leading-6 text-foreground">{todo.automation.plan.summary}</p>
                  {#if todo.automation.plan.steps?.length}
                    <ol class="mt-3 divide-y divide-border border-y border-border">
                      {#each todo.automation.plan.steps as step, index (step.step_id)}
                        <li class="grid gap-1 py-3 sm:grid-cols-[2rem_minmax(0,1fr)_auto] sm:items-start">
                          <span class="font-mono text-xs text-muted-foreground">{index + 1}</span>
                          <div class="min-w-0"><p class="break-words text-sm font-medium">{step.title}</p>{#if step.description}<p class="mt-1 break-words text-xs leading-5 text-muted-foreground">{step.description}</p>{/if}</div>
                          {#if step.assignee_agent_id}<Badge tone="source-agent">{step.assignee_agent_id}</Badge>{/if}
                        </li>
                      {/each}
                    </ol>
                  {/if}
                </div>
              {/if}
              {#if todo.automation.proposal}
                <div class="prose prose-slate max-w-none text-sm leading-7 dark:prose-invert">{@html renderSafeMarkdown(todo.automation.proposal)}</div>
              {/if}
              {#if todo.automation.plan_digest}
                <div class="flex min-w-0 items-center justify-between gap-3 text-xs"><span class="text-muted-foreground">{t('workhub.automation.planDigest')}</span><code class="min-w-0 truncate">{todo.automation.plan_digest}</code></div>
              {/if}
              {#if todo.automation.failure_reason}
                <p class="rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-200">{todo.automation.failure_reason}</p>
              {/if}
              <div class="flex flex-wrap gap-2">
                {#if todo.discussion_binding?.conversation_id || todo.automation.conversation_id}
                  <Button href={`/discussions/${encodeURIComponent(todo.discussion_binding?.conversation_id || todo.automation.conversation_id || '')}`} variant="outline" size="sm">
                    <MessageSquare class="h-4 w-4" />
                    {t('workhub.automation.openDiscussion')}
                    <ExternalLink class="h-3.5 w-3.5" />
                  </Button>
                {/if}
                {#if todo.automation.status === 'pending_approval'}
                  <Button href={todo.automation.approval_id ? workActionHref(`approval:${todo.automation.approval_id}`) : '/work-items?view=inbox'} size="sm">
                    <Check class="h-4 w-4" />
                    {t('workhub.automation.review')}
                  </Button>
                {/if}
                {#if todo.automation_mode === 'suggest' && todo.automation.status === 'approved' && todo.automation.plan_digest === todo.automation.approved_plan_digest}
                  <Button size="sm" disabled={todoCommands.startAutomation.isPending} onclick={startApprovedAutomation}>
                    <Play class="h-4 w-4" />{t('workhub.automation.start')}
                  </Button>
                {/if}
              </div>
            </div>
          </section>
        {/if}

        {#if kind === 'todo' && todo?.execution && (todo.execution.current || todo.execution.current_progress || todo.execution.attempts.length)}
          {@const currentExecution = todo.execution.current}
          <section class="panel overflow-hidden" aria-label={t('workhub.execution.title')}>
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
              <h2 class="inline-flex items-center gap-2 text-sm font-semibold"><Activity class="h-4 w-4" />{t('workhub.execution.title')}</h2>
              {#if todo.execution.current_progress}
                <Badge tone={todo.execution.current_progress.blocked_task_count ? 'warning' : 'info'}>{todo.execution.current_progress.current_phase || todo.execution.current_progress.mission_status}</Badge>
              {:else if currentExecution}
                <Badge tone={executionStatusTone(currentExecution.status)}>{executionStatusLabel(currentExecution.status)}</Badge>
              {/if}
            </div>
            <div class="space-y-4 p-4 sm:p-5">
              {#if currentExecution && (currentExecution.status === 'pause_requested' || currentExecution.status === 'paused')}
                <div class="border-l-2 border-amber-500 bg-amber-50 px-3 py-2 text-sm text-amber-950 dark:bg-amber-950/30 dark:text-amber-100">
                  <p class="font-medium">{executionStatusLabel(currentExecution.status)}</p>
                  {#if currentExecution.pause_reason}<p class="mt-1 text-xs leading-5">{executionReasonLabel(currentExecution.pause_reason)}</p>{/if}
                </div>
              {/if}
              {#if todo.execution.current_progress}
                {@const progress = todo.execution.current_progress}
                <div class="space-y-2">
                  <div class="flex items-center justify-between text-xs"><span class="text-muted-foreground">{t('workhub.execution.delivery')}</span><strong>{percent(progress.delivery_rate)}%</strong></div>
                  <div class="h-2 overflow-hidden rounded bg-muted"><div class="h-full bg-emerald-600" style={`width: ${percent(progress.delivery_rate)}%`}></div></div>
                  <dl class="grid grid-cols-2 gap-3 pt-2 text-xs sm:grid-cols-4">
                    <div><dt class="text-muted-foreground">{t('workhub.execution.active')}</dt><dd class="mt-1 text-sm font-semibold">{progress.active_task_count}</dd></div>
                    <div><dt class="text-muted-foreground">{t('workhub.execution.done')}</dt><dd class="mt-1 text-sm font-semibold">{progress.done_task_count}</dd></div>
                    <div><dt class="text-muted-foreground">{t('workhub.execution.blocked')}</dt><dd class="mt-1 text-sm font-semibold">{progress.blocked_task_count}</dd></div>
                    <div><dt class="text-muted-foreground">{t('workhub.execution.approvals')}</dt><dd class="mt-1 text-sm font-semibold">{progress.pending_approval_count}</dd></div>
                  </dl>
                </div>
                <div class="flex flex-wrap gap-2">
                  {#if progress.can_open_control}<Button href={`/missions/${encodeURIComponent(progress.mission_id)}`} size="sm"><ExternalLink class="h-4 w-4" />{t('workhub.execution.openControl')}</Button>{/if}
                  {#if progress.can_open_discussion && progress.conversation_id}<Button href={`/discussions/${encodeURIComponent(progress.conversation_id)}`} variant="outline" size="sm"><MessageSquare class="h-4 w-4" />{t('workhub.automation.openDiscussion')}</Button>{/if}
                </div>
              {/if}
              {#if todo.execution.attempts.length}
                <div class="border-t border-border pt-3">
                  <p class="mb-2 text-xs font-medium text-muted-foreground">{t('workhub.execution.history')}</p>
                  <div class="divide-y divide-border">
                    {#each todo.execution.attempts as attempt (attempt.execution_attempt)}
                      <div class="flex flex-wrap items-start justify-between gap-2 py-2 text-xs">
                        <div>
                          <span>#{attempt.execution_attempt} · {executionStatusLabel(attempt.status)}</span>
                          {#if attempt.failure_reason}<p class="mt-1 max-w-2xl text-muted-foreground">{executionReasonLabel(attempt.failure_reason)}</p>{/if}
                        </div>
                        <span class="text-muted-foreground">{dateTimeLabel(attempt.updated_at)}</span>
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}
            </div>
          </section>
        {/if}

        {#if kind === 'todo' && todo?.acceptance_criteria && !editing}
          <section class="panel overflow-hidden" aria-label={t('workhub.form.acceptance')}>
            <div class="border-b border-border px-4 py-3"><h2 class="text-sm font-semibold">{t('workhub.form.acceptance')}</h2></div>
            <div class="p-4 text-sm leading-6 text-foreground">{todo.acceptance_criteria}</div>
          </section>
        {/if}
        {#if kind === 'idea' && idea?.decision_reason && !editing}
          <section class="panel overflow-hidden" aria-label={t('workhub.detail.decision')}>
            <div class="border-b border-border px-4 py-3"><h2 class="text-sm font-semibold">{t('workhub.detail.decision')}</h2></div>
            <div class="p-4 text-sm leading-6 text-foreground">{idea.decision_reason}</div>
          </section>
        {/if}

        <section class="panel overflow-hidden" aria-label={t('workhub.detail.activity')}>
          <div class="flex items-center justify-between border-b border-border px-4 py-3">
            <h2 class="inline-flex items-center gap-2 text-sm font-semibold text-foreground"><MessageSquare class="h-4 w-4" />{t('workhub.detail.activity')}</h2>
            <span class="text-xs text-muted-foreground">{comments.length}</span>
          </div>

          {#if commentsLoading}
            <div class="p-4 text-sm text-muted-foreground">{t('home.comments.loading')}</div>
          {:else if commentsFailed}
            <div class="p-4 text-sm text-rose-600">{t('home.comments.failed')}</div>
          {:else if comments.length}
            <div class="divide-y divide-border">
              {#each comments as comment (comment.comment_id)}
                <article class="flex gap-3 px-4 py-4">
                  <WorkforceAvatar
                    profileId={commentProfileId(comment.author_id)}
                    name={comment.author_name || comment.author_id}
                    human={commentIsHuman(comment.author_id)}
                    class="h-8 w-8 rounded-md"
                    textClass="text-xs"
                  />
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center justify-between gap-2">
                      <span class="text-sm font-medium text-foreground">{comment.author_name || comment.author_id}</span>
                      <time class="text-xs text-muted-foreground">{dateTimeLabel(comment.created_at)}</time>
                    </div>
                    <div class="mt-2 text-sm leading-6 text-foreground">{@html renderSafeMarkdown(comment.body)}</div>
                  </div>
                </article>
              {/each}
            </div>
          {:else}
            <div class="px-4 py-8 text-center text-sm text-muted-foreground">{t('home.comments.empty')}</div>
          {/if}

          <div class="border-t border-border bg-muted/20 p-4">
            <MarkdownEditor
              bind:value={replyBody}
              ariaLabel={t('home.comments.replyBody')}
              placeholder={t('home.comments.replyPlaceholder')}
              minHeight="min-h-28"
              variant="composer"
              writeLabel={t('home.markdown.write')}
              previewLabel={t('home.markdown.preview')}
              toolbarLabels={toolbarLabels()}
            />
            <div class="mt-3 flex justify-end">
              <Button size="sm" disabled={!canReply || todoCommands.createTodoComment.isPending || ideaCommands.createIdeaComment.isPending} onclick={submitReply}>
                <Send class="h-4 w-4" />
                {t('home.action.sendReply')}
              </Button>
            </div>
          </div>
        </section>
      </main>

      <aside class="space-y-4">
        <section class="panel overflow-hidden" aria-label={t('workhub.detail.metadata')}>
          <div class="border-b border-border px-4 py-3"><h2 class="text-sm font-semibold text-foreground">{t('workhub.detail.metadata')}</h2></div>
          <div class="space-y-4 p-4">
            <div class="space-y-1.5">
              <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.status')}</span>
              {#if kind === 'todo' && todo}
                <select bind:value={selectedTodoStatus} aria-label={t('workhub.form.status')} class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm outline-none focus:ring-1 focus:ring-ring">
                  {#each allowedTodoStatuses(todo.status) as option}
                    <option value={option}>{t(`workhub.status.${option}` as TranslationKey)}</option>
                  {/each}
                </select>
                {#if todo.source && selectedTodoStatus === 'done' && todo.status !== 'done'}
                  <label class="mt-3 block text-sm">{t('handoff.result')}
                    <textarea class="mt-2 min-h-32 w-full rounded-md border border-border bg-input-background p-3" bind:value={humanResult} maxlength={32000} required></textarea>
                  </label>
                {/if}
                {#if selectedTodoStatus === 'blocked'}
                  <textarea bind:value={editBlockedReason} aria-label={t('workhub.form.blockedReason')} placeholder={t('workhub.placeholder.blockedReason')} class="mt-2 min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
                {/if}
              {:else if idea}
                <select bind:value={selectedIdeaStatus} aria-label={t('workhub.form.status')} class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm outline-none focus:ring-1 focus:ring-ring">
                  {#each allowedIdeaStatuses(idea.status) as option}
                    <option value={option}>{t(`workhub.ideaStatus.${option}` as TranslationKey)}</option>
                  {/each}
                </select>
                {#if ['accepted', 'rejected', 'experiment'].includes(selectedIdeaStatus)}
                  <textarea bind:value={transitionReason} aria-label={t('workhub.form.reason')} placeholder={t('workhub.placeholder.reason')} class="mt-2 min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
                {/if}
              {/if}
              <Button class="mt-2 w-full" size="sm" disabled={!canApplyTransition || transitionPending} onclick={applyTransition}>{t(kind === 'todo' && todo?.source && selectedTodoStatus === 'done' ? 'handoff.complete' : 'workhub.action.apply')}</Button>
              {#if kind === 'idea' && idea && canConvertIdea(idea.status)}
                <Button class="mt-2 w-full" variant="outline" size="sm" disabled={!canConvertCurrentIdea || transitionPending} onclick={convertCurrentIdea}>
                  {t('workhub.action.convert')}
                  <ArrowRight class="h-3.5 w-3.5" />
                </Button>
              {/if}
            </div>

            <div class="h-px bg-border"></div>

            {#if kind === 'todo' && todo}
              {#if editing}
                {@render fieldSelect(t('workhub.form.priority'), editPriority, (value) => (editPriority = value as TodoPriority), todoPriorities.map((priority) => ({ label: t(`workhub.priority.${priority}` as TranslationKey), value: priority })))}
                {@render fieldSelect(t('workhub.form.workKind'), editWorkKind, (value) => { editWorkKind = value as TodoWorkKind; if (editWorkKind === 'general') editDevelopmentProjectID = '' }, [{ label: t('workhub.workKind.general'), value: 'general' }, { label: t('workhub.workKind.development'), value: 'development' }])}
                {@render fieldSelect(t('workhub.form.automationMode'), editAutomationMode, (value) => (editAutomationMode = value as TodoAutomationMode), [{ label: t('workhub.automationMode.manual'), value: 'manual' }, { label: t('workhub.automationMode.suggest'), value: 'suggest' }, { label: t('workhub.automationMode.managed'), value: 'managed' }])}
                {#if editWorkKind === 'development'}
                  {@render fieldSelect(t('workhub.form.developmentProject'), editDevelopmentProjectID, (value) => (editDevelopmentProjectID = value), [{ label: '-', value: '' }, ...developmentProjects.map((project) => ({ label: project.name, value: project.development_project_id }))])}
                {/if}
                {@render fieldSelect(t('workhub.form.assignee'), editAssignee, (value) => (editAssignee = value), optionsWithCurrent(todo.assignee_id ?? '', todo.assignee_name ?? ''))}
                {@render fieldSelect(t('workhub.form.reviewer'), editReviewer, (value) => (editReviewer = value), optionsWithCurrent(todo.reviewer_id ?? '', todo.reviewer_name ?? ''))}
                <label class="block space-y-1.5"><span class="text-xs font-medium text-muted-foreground">{t('workhub.form.dueDate')}</span><Input bind:value={editDueAt} type="datetime-local" ariaLabel={t('workhub.form.dueDate')} /></label>
                <label class="block space-y-1.5"><span class="text-xs font-medium text-muted-foreground">{t('workhub.form.acceptance')}</span><textarea bind:value={editAcceptance} aria-label={t('workhub.form.acceptance')} class="min-h-28 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea></label>
              {:else}
                {@render metadataRow(t('workhub.form.assignee'), todo.assignee_name || todo.assignee_id || t('workhub.assignee.unassigned'), UserRound)}
                {@render metadataRow(t('workhub.form.reviewer'), todo.reviewer_name || '-', UserRound)}
                {@render metadataRow(t('workhub.form.dueDate'), dateTimeLabel(todo.due_at), CalendarClock)}
                {@render textMetadata(t('workhub.form.priority'), t(`workhub.priority.${todo.priority}` as TranslationKey))}
                {@render textMetadata(t('workhub.form.workKind'), t(`workhub.workKind.${todo.work_kind ?? 'general'}` as TranslationKey))}
                {@render textMetadata(t('workhub.form.automationMode'), t(`workhub.automationMode.${todo.automation_mode ?? 'manual'}` as TranslationKey))}
                {#if todo.development_project_id}{@render textMetadata(t('workhub.form.developmentProject'), developmentProjects.find((project) => project.development_project_id === todo.development_project_id)?.name || todo.development_project_id)}{/if}
              {/if}
            {:else if idea}
              {#if editing}
                {@render fieldSelect(t('workhub.form.visibility'), editVisibility, (value) => (editVisibility = value as IdeaVisibility), ['organization', 'department', 'private'].map((value) => ({ label: t(`workhub.visibility.${value}` as TranslationKey), value })))}
                {@render fieldSelect(t('workhub.form.impact'), editImpact, (value) => (editImpact = value as IdeaImpact), ['high', 'medium', 'low'].map((value) => ({ label: t(`workhub.impact.${value}` as TranslationKey), value })))}
                {@render fieldSelect(t('workhub.form.effort'), editEffort, (value) => (editEffort = value as IdeaEffort), ['small', 'medium', 'large'].map((value) => ({ label: t(`workhub.effort.${value}` as TranslationKey), value })))}
                {@render fieldSelect(t('workhub.form.owner'), editOwner, (value) => (editOwner = value), optionsWithCurrent(idea.owner_id ?? '', idea.owner_name ?? ''))}
                {@render fieldSelect(t('workhub.form.reviewer'), editReviewer, (value) => (editReviewer = value), optionsWithCurrent(idea.reviewer_id ?? '', idea.reviewer_name ?? ''))}
                <label class="block space-y-1.5"><span class="text-xs font-medium text-muted-foreground">{t('workhub.form.reason')}</span><textarea bind:value={editDecision} aria-label={t('workhub.form.reason')} class="min-h-28 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea></label>
              {:else}
                {@render metadataRow(t('workhub.form.owner'), idea.owner_name || '-', UserRound)}
                {@render metadataRow(t('workhub.form.reviewer'), idea.reviewer_name || '-', UserRound)}
                {@render textMetadata(t('workhub.form.visibility'), t(`workhub.visibility.${idea.visibility}` as TranslationKey))}
                {@render textMetadata(t('workhub.form.impact'), t(`workhub.impact.${idea.impact}` as TranslationKey))}
                {@render textMetadata(t('workhub.form.effort'), t(`workhub.effort.${idea.effort}` as TranslationKey))}
                {@render textMetadata(t('workhub.action.vote'), String(idea.vote_count))}
              {/if}
            {/if}

            <div class="h-px bg-border"></div>
            {@render textMetadata(t('workhub.detail.createdBy'), (kind === 'todo' ? todo?.proposer_name : idea?.proposer_name) || (kind === 'todo' ? todo?.created_by : idea?.created_by) || '-')}
            {@render textMetadata(t('workhub.detail.createdAt'), dateTimeLabel(kind === 'todo' ? todo?.created_at : idea?.created_at))}
            {@render textMetadata(t('workhub.detail.updatedAt'), dateTimeLabel(kind === 'todo' ? todo?.updated_at : idea?.updated_at))}
          </div>
        </section>

        {#if (kind === 'todo' ? todo?.tags?.length : idea?.tags?.length) && !editing}
          <section class="panel p-4">
            <h2 class="text-xs font-medium text-muted-foreground">{t('workhub.form.tags')}</h2>
            <div class="mt-2 flex flex-wrap gap-1.5">
              {#each (kind === 'todo' ? (todo?.tags ?? []) : (idea?.tags ?? [])) as tag}
                <Badge tone="neutral">#{tag}</Badge>
              {/each}
            </div>
          </section>
        {/if}
      </aside>
    </div>
  </div>
{/if}

{#snippet fieldSelect(label: string, value: string, setValue: (value: string) => void, options: Array<{ label: string; value: string }>)}
  <label class="block space-y-1.5">
    <span class="text-xs font-medium text-muted-foreground">{label}</span>
    <select value={value} aria-label={label} class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm outline-none focus:ring-1 focus:ring-ring" onchange={(event) => setValue(event.currentTarget.value)}>
      {#each options as option}<option value={option.value}>{option.label}</option>{/each}
    </select>
  </label>
{/snippet}

{#snippet metadataRow(label: string, value: string, Icon: typeof UserRound)}
  <div class="flex items-start justify-between gap-3 text-sm">
    <span class="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><Icon class="h-3.5 w-3.5" />{label}</span>
    <span class="max-w-44 text-right text-foreground">{value}</span>
  </div>
{/snippet}

{#snippet textMetadata(label: string, value: string)}
  <div class="flex items-start justify-between gap-3 text-sm">
    <span class="text-xs text-muted-foreground">{label}</span>
    <span class="max-w-44 text-right text-foreground">{value}</span>
  </div>
{/snippet}
