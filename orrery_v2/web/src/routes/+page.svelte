<script lang="ts">
  import WorkInbox from '#lib/components/operations/WorkInbox.svelte'
  import {
    ArrowUp,
    CheckCircle2,
    ChevronDown,
    ChevronLeft,
    ChevronRight,
    Columns3,
    FlaskConical,
    LayoutList,
    Lightbulb,
    Plus,
    Search,
    Sparkles,
    Users,
    X
  } from 'lucide-svelte'
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import MarkdownEditor from '#lib/components/ui/MarkdownEditor.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import IdeaReviewRow from '#lib/components/workhub/IdeaReviewRow.svelte'
  import WorkTaskRow from '#lib/components/workhub/WorkTaskRow.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import {
    allowedIdeaStatuses,
    allowedTodoStatuses,
    canCompleteTodo,
    dateTimeInputISO,
    normalizeTags,
    openTodoStatuses,
    todoPriorities,
    todoStatuses,
    workforceAssigneeKind,
    workforceMemberName
  } from '#lib/components/workhub/model'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useIdeaCommandMutations, useIdeaListQuery } from '#lib/queries/ideas'
  import { useDevelopmentProjectListQuery } from '#lib/queries/development-projects'
  import { useTodoCommandMutations, useTodoListQuery, useWorkHubSummaryQuery } from '#lib/queries/todos'
  import { useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { session } from '#lib/stores/session.svelte'
  import type {
    IdeaEffort,
    IdeaImpact,
    IdeaItemDTO,
    IdeaStatus,
    IdeaVisibility
  } from '#lib/types/idea'
  import type { AssigneeKind, TodoAutomationMode, TodoItemDTO, TodoPriority, TodoStatus, TodoWorkKind } from '#lib/types/todo'
  import type { WorkforceMemberViewModel } from '#lib/types/view-models'

  type HubTab = 'focus' | 'ideas' | 'team'
  type TeamMode = 'list' | 'board'
  type CreateKind = 'task' | 'idea'
  type ComposerMode = 'assign' | 'consult'
  type TaskFeedFilter = 'all' | 'active' | 'review' | 'done'

  interface Props {
    initialTab?: HubTab
    dedicated?: boolean
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

  function hubTabFromURL(url: URL): HubTab {
    const tab = url.searchParams.get('tab')
    return tab === 'ideas' || tab === 'team' ? tab : 'focus'
  }

  let { initialTab = 'focus', dedicated = false }: Props = $props()

  const t = $derived(createTranslator($locale))
  const canReadWorkforce = $derived(hasPermissionScope(session.permissionScope, 'workforce.read'))
  const canReadDevelopmentProjects = $derived(hasPermissionScope(session.permissionScope, 'development.read'))
  const canWriteTodos = $derived(hasPermissionScope(session.permissionScope, 'todo.write'))
  const canWriteIdeas = $derived(hasPermissionScope(session.permissionScope, 'idea.write'))
  const canConsult = $derived(
    hasPermissionScope(session.permissionScope, 'discussion.read') &&
      hasPermissionScope(session.permissionScope, 'discussion.write')
  )

  const pageSize = 50
  const timeZone = resolvedTimeZone()

  let selectedTab = $state<HubTab>(hubTabFromURL($page.url))
  const activeTab = $derived(dedicated ? initialTab : selectedTab)
  let teamMode = $state<TeamMode>('list')
  let composerMode = $state<ComposerMode>('assign')
  let composerAssignee = $state('')
  let taskFeedFilter = $state<TaskFeedFilter>('all')
  let taskSearch = $state('')
  let taskStatusFilter = $state<TodoStatus | ''>('')
  let taskPriorityFilter = $state<TodoPriority | ''>('')
  let taskAssigneeFilter = $state('')
  let ideaSearch = $state('')
  let ideaStatusFilter = $state<IdeaStatus | ''>('')
  let ideaVisibilityFilter = $state<IdeaVisibility | ''>('')
  let focusOffset = $state(0)
  let teamOffset = $state(0)
  let ideaOffset = $state(0)
  let actionMessage = $state('')
  let actionError = $state(false)
  let quickTitle = $state('')
  let handledCreateRequest = $state('')

  let createOpen = $state(false)
  let createKind = $state<CreateKind>('task')
  let createTitle = $state('')
  let createBody = $state('')
  let createTags = $state('')
  let createTaskStatus = $state<TodoStatus>('planned')
  let createPriority = $state<TodoPriority>('medium')
  let createAssignee = $state('')
  let createDueAt = $state('')
  let createAcceptance = $state('')
  let createWorkKind = $state<TodoWorkKind>('general')
  let createAutomationMode = $state<TodoAutomationMode>('manual')
  let createDevelopmentProjectID = $state('')
  let createIdeaStatus = $state<IdeaStatus>('submitted')
  let createVisibility = $state<IdeaVisibility>('organization')
  let createImpact = $state<IdeaImpact>('medium')
  let createEffort = $state<IdeaEffort>('medium')

  let blockedTask = $state<TodoItemDTO | undefined>()
  let blockedReason = $state('')

  let reviewIdea = $state<IdeaItemDTO | undefined>()
  let reviewStatus = $state<IdeaStatus>('accepted')
  let reviewReason = $state('')
  let reviewReviewer = $state('')

  let convertIdea = $state<IdeaItemDTO | undefined>()
  let convertTitle = $state('')
  let convertBody = $state('')
  let convertPriority = $state<TodoPriority>('medium')
  let convertAssignee = $state('')
  let convertDueAt = $state('')
  let convertAcceptance = $state('')
  let convertReason = $state('')

  const focusTodosQuery = useTodoListQuery(
    () => ({ mine: true, includeCompleted: true, limit: pageSize, offset: focusOffset }),
    () => false
  )
  const teamTodosQuery = useTodoListQuery(() => ({
    status: taskStatusFilter,
    priority: taskPriorityFilter,
    assigneeId: taskAssigneeFilter === '__unassigned' ? undefined : taskAssigneeFilter,
    unassigned: taskAssigneeFilter === '__unassigned',
    q: taskSearch.trim() || undefined,
    includeCompleted: teamMode === 'list',
    limit: pageSize,
    offset: teamOffset
  }), () => activeTab === 'team')
  const ideasQuery = useIdeaListQuery(
    () => ({
      status: ideaStatusFilter,
      visibility: ideaVisibilityFilter,
      q: ideaSearch.trim() || undefined,
      limit: pageSize,
      offset: ideaOffset
    }),
    () => activeTab === 'ideas'
  )
  const summaryQuery = useWorkHubSummaryQuery(() => timeZone, () => !dedicated && activeTab === 'ideas')
  const workforceQuery = useWorkforceMembersQuery(
    () => ({ enabledOnly: true, includeDetails: true, limit: 500, offset: 0 }),
    () => canReadWorkforce
  )
  const developmentProjectsQuery = useDevelopmentProjectListQuery(
    () => ({ status: 'active', limit: 100 }),
    () => canReadDevelopmentProjects && createOpen && createKind === 'task' && createWorkKind === 'development'
  )
  const todoCommands = useTodoCommandMutations()
  const ideaCommands = useIdeaCommandMutations()

  const tasks = $derived(teamTodosQuery.data?.todos ?? [])
  const focusTasks = $derived(focusTodosQuery.data?.todos ?? [])
  const ideas = $derived(ideasQuery.data?.ideas ?? [])
  const workforceMembers = $derived(workforceQuery.data?.members ?? [])
  const developmentProjects = $derived(developmentProjectsQuery.data?.items ?? [])
  const summary = $derived(summaryQuery.data)
  const assigneeOptions = $derived(buildAssigneeOptions(workforceMembers))
  const composerAssigneeOptions = $derived(buildComposerAssigneeOptions(workforceMembers))
  const myTaskFeed = $derived(
    focusTasks.filter(
      (task) => task.assignee_id === session.actorId || (!task.assignee_id && task.created_by === session.actorId)
    )
  )
  const filteredTeamTasks = $derived(tasks)
  const filteredIdeas = $derived(ideas)
  const homeTaskFeed = $derived(filteredHomeTaskFeed())
  const activeViewLoading = $derived(
    activeTab === 'focus'
      ? focusTodosQuery.isLoading
      : activeTab === 'ideas'
        ? ideasQuery.isLoading || (!dedicated && summaryQuery.isLoading)
        : teamTodosQuery.isLoading
  )
  const activeViewError = $derived(
    activeTab === 'focus'
      ? focusTodosQuery.isError
      : activeTab === 'ideas'
        ? ideasQuery.isError || (!dedicated && summaryQuery.isError)
        : teamTodosQuery.isError
  )
  const createPending = $derived(todoCommands.createTodo.isPending || ideaCommands.createIdea.isPending)
  const todoMutationPending = $derived(
    todoCommands.updateTodo.isPending || todoCommands.moveTodo.isPending || todoCommands.completeTodo.isPending
  )
  const ideaMutationPending = $derived(
    ideaCommands.transitionIdea.isPending || ideaCommands.voteIdea.isPending || ideaCommands.convertIdeaToTodo.isPending
  )
  const canCreate = $derived(createTitle.trim().length > 0)
  const reviewStatusOptions = $derived(
    reviewIdea
      ? allowedIdeaStatuses(reviewIdea.status).filter((status) => ['accepted', 'experiment', 'rejected', 'submitted'].includes(status))
      : []
  )
  const canApplyReview = $derived(
    Boolean(reviewIdea) &&
      reviewStatus !== reviewIdea?.status &&
      (!['accepted', 'rejected'].includes(reviewStatus) || reviewReason.trim().length > 0)
  )

  $effect(() => {
    if (dedicated) return
    selectedTab = hubTabFromURL($page.url)
  })

  $effect(() => {
    const request = $page.url.searchParams.get('create')?.trim().toLowerCase() ?? ''
    if (!request) {
      handledCreateRequest = ''
      return
    }
    if (handledCreateRequest === request) return
    handledCreateRequest = request
    const kind: CreateKind = request === 'idea' ? 'idea' : 'task'
    if ((kind === 'task' && canWriteTodos) || (kind === 'idea' && canWriteIdeas)) openCreate(kind)
    const nextURL = new URL($page.url)
    nextURL.searchParams.delete('create')
    void goto(`${nextURL.pathname}${nextURL.search}${nextURL.hash}`, { replaceState: true, reset: false })
  })

  function selectTab(tab: HubTab) {
    selectedTab = tab
    const nextURL = new URL($page.url)
    if (tab === 'focus') nextURL.searchParams.delete('tab')
    else nextURL.searchParams.set('tab', tab)
    nextURL.searchParams.delete('create')
    void goto(`${nextURL.pathname}${nextURL.search}${nextURL.hash}`, { replaceState: false, reset: false })
  }

  function openCreate(kind: CreateKind) {
    if ((kind === 'task' && !canWriteTodos) || (kind === 'idea' && !canWriteIdeas)) return
    createKind = kind
    createTitle = ''
    createBody = ''
    createTags = ''
    createTaskStatus = 'planned'
    createPriority = 'medium'
    createAssignee = ''
    createDueAt = ''
    createAcceptance = ''
    createWorkKind = 'general'
    createAutomationMode = 'manual'
    createDevelopmentProjectID = ''
    createIdeaStatus = 'submitted'
    createVisibility = 'organization'
    createImpact = 'medium'
    createEffort = 'medium'
    createOpen = true
    clearMessage()
  }

  function submitComposer() {
    if (!canConsult) return
    const draft = quickTitle.trim()
    if (!draft) return
    const directTarget = consultationTarget(composerAssignee)
    void goto('/discussions', {
      state: {
        discussionComposer: {
          draft,
          sendImmediately: true,
          ...(directTarget ?? {})
        }
      }
    })
  }

  function handleQuickCreateKeydown(event: KeyboardEvent) {
    if (!canConsult || event.key !== 'Enter' || event.isComposing) return
    if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    submitComposer()
  }

  function submitCreate() {
    if (!canCreate || (createKind === 'task' ? !canWriteTodos : !canWriteIdeas)) return
    const title = createTitle.trim()
    const tags = normalizeTags(createTags)
    if (createKind === 'task') {
      const assignee = assigneeSelection(createAssignee)
      todoCommands.createTodo.mutate(
        {
          title,
          description: optionalText(createBody),
          status: createTaskStatus,
          priority: createPriority,
          work_kind: createWorkKind,
          automation_mode: createAutomationMode,
          ...(createWorkKind === 'development' && createDevelopmentProjectID
            ? { development_project_id: createDevelopmentProjectID }
            : {}),
          ...(assignee
            ? { assignee_id: assignee.id, assignee_name: assignee.name, assignee_kind: assignee.kind }
            : {}),
          due_at: dateTimeInputISO(createDueAt),
          acceptance_criteria: optionalText(createAcceptance),
          tags
        },
        {
          onSuccess: () => {
            createOpen = false
            showMessage(t('workhub.message.taskCreated'))
          },
          onError: showError
        }
      )
      return
    }
    ideaCommands.createIdea.mutate(
      {
        title,
        content: optionalText(createBody),
        status: createIdeaStatus,
        visibility: createVisibility,
        impact: createImpact,
        effort: createEffort,
        tags
      },
      {
        onSuccess: () => {
          createOpen = false
          showMessage(t('workhub.message.ideaCreated'))
        },
        onError: showError
      }
    )
  }

  function changeTaskStatus(task: TodoItemDTO, status: TodoStatus) {
    if (!canWriteTodos || status === task.status) return
    if (status === 'blocked') {
      blockedTask = task
      blockedReason = task.blocked_reason ?? ''
      return
    }
    todoCommands.moveTodo.mutate(
      { todoID: task.todo_id, body: { status, version: task.version } },
      { onSuccess: () => showMessage(t('workhub.message.taskUpdated')), onError: showError }
    )
  }

  function applyBlockedStatus() {
    if (!canWriteTodos || !blockedTask || !blockedReason.trim()) return
    todoCommands.moveTodo.mutate(
      {
        todoID: blockedTask.todo_id,
        body: { status: 'blocked', blocked_reason: blockedReason.trim(), version: blockedTask.version }
      },
      {
        onSuccess: () => {
          blockedTask = undefined
          showMessage(t('workhub.message.taskUpdated'))
        },
        onError: showError
      }
    )
  }

  function completeTask(task: TodoItemDTO) {
    if (!canWriteTodos) return
    todoCommands.completeTodo.mutate(
      { todoID: task.todo_id, version: task.version },
      { onSuccess: () => showMessage(t('workhub.message.taskUpdated')), onError: showError }
    )
  }

  function changeTaskAssignee(task: TodoItemDTO, assigneeID: string) {
    if (!canWriteTodos) return
    const selection = assigneeSelection(assigneeID)
    todoCommands.updateTodo.mutate(
      {
        todoID: task.todo_id,
        body: selection
          ? {
              assignee_id: selection.id,
              assignee_name: selection.name,
              assignee_kind: selection.kind,
              version: task.version
            }
          : { assignee_id: '', assignee_name: '', assignee_kind: 'human', version: task.version }
      },
      { onSuccess: () => showMessage(t('workhub.message.taskUpdated')), onError: showError }
    )
  }

  function changeIdeaStatus(idea: IdeaItemDTO, status: IdeaStatus) {
    if (!canWriteIdeas || status === idea.status) return
    const reviewer = status === 'under_review' ? assigneeSelection(session.actorId) : undefined
    ideaCommands.transitionIdea.mutate(
      {
        ideaID: idea.idea_id,
        body: {
          status,
          version: idea.version,
          reviewer_id: reviewer?.id,
          reviewer_name: reviewer?.name
        }
      },
      { onSuccess: () => showMessage(t('workhub.message.ideaTransitioned')), onError: showError }
    )
  }

  function voteIdea(idea: IdeaItemDTO) {
    if (!canWriteIdeas) return
    ideaCommands.voteIdea.mutate(idea.idea_id, {
      onSuccess: () => showMessage(t('workhub.message.ideaUpdated')),
      onError: showError
    })
  }

  function openReviewIdea(idea: IdeaItemDTO) {
    if (!canWriteIdeas) return
    reviewIdea = idea
    reviewStatus = 'accepted'
    reviewReason = idea.decision_reason ?? ''
    reviewReviewer = idea.reviewer_id || session.actorId
    clearMessage()
  }

  function applyIdeaReview() {
    if (!canWriteIdeas || !reviewIdea || !canApplyReview) return
    const reviewer = assigneeSelection(reviewReviewer)
    ideaCommands.transitionIdea.mutate(
      {
        ideaID: reviewIdea.idea_id,
        body: {
          status: reviewStatus,
          decision_reason: optionalText(reviewReason),
          reviewer_id: reviewer?.id,
          reviewer_name: reviewer?.name,
          version: reviewIdea.version
        }
      },
      {
        onSuccess: () => {
          reviewIdea = undefined
          showMessage(t('workhub.message.ideaTransitioned'))
        },
        onError: showError
      }
    )
  }

  function openConvertIdea(idea: IdeaItemDTO) {
    if (!canWriteIdeas) return
    convertIdea = idea
    convertTitle = idea.title
    convertBody = idea.content ?? ''
    convertPriority = priorityFromIdea(idea)
    convertAssignee = idea.owner_id ?? ''
    convertDueAt = ''
    convertAcceptance = ''
    convertReason = idea.decision_reason ?? ''
    clearMessage()
  }

  function applyIdeaConversion() {
    if (!canWriteIdeas || !convertIdea || !convertTitle.trim()) return
    const assignee = assigneeSelection(convertAssignee)
    ideaCommands.convertIdeaToTodo.mutate(
      {
        ideaID: convertIdea.idea_id,
        body: {
          todo_title: convertTitle.trim(),
          todo_description: optionalText(convertBody),
          priority: convertPriority,
          ...(assignee
            ? { assignee_id: assignee.id, assignee_name: assignee.name, assignee_kind: assignee.kind }
            : {}),
          due_at: dateTimeInputISO(convertDueAt),
          acceptance_criteria: optionalText(convertAcceptance),
          decision_reason: optionalText(convertReason),
          version: convertIdea.version
        }
      },
      {
        onSuccess: () => {
          convertIdea = undefined
          showMessage(t('workhub.message.ideaConverted'))
        },
        onError: showError
      }
    )
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

  function assigneeOptionsFor(task: TodoItemDTO) {
    if (!task.assignee_id || assigneeOptions.some((option) => option.value === task.assignee_id)) return assigneeOptions
    return [...assigneeOptions, { label: task.assignee_name || task.assignee_id, value: task.assignee_id }]
  }

  function assigneeSelection(id: string): AssigneeSelection | undefined {
    id = id.trim()
    if (!id) return undefined
    if (id === session.actorId) {
      return { id, name: session.displayName || session.username || id, kind: 'human' }
    }
    const member = workforceMembers.find((candidate) => candidate.id === id)
    if (!member) return { id, name: id, kind: 'human' }
    return { id, name: workforceMemberName(member), kind: workforceAssigneeKind(member) }
  }

  function composerAssigneeMember() {
    const id = composerAssignee.trim()
    if (!id) return undefined
    return workforceMembers.find((member) => [member.id, member.profileId, member.agentInstanceId].includes(id))
  }

  function composerAssigneeName() {
    const member = composerAssigneeMember()
    return member
      ? workforceMemberName(member)
      : composerAssigneeOptions.find((option) => option.value === composerAssignee)?.label || t('workhub.assignee.unassigned')
  }

  function tasksForBoard(status: TodoStatus) {
    return filteredTeamTasks.filter((task) => task.status === status)
  }

  function setTeamMode(mode: TeamMode) {
    teamMode = mode
    teamOffset = 0
    if (mode === 'board' && (taskStatusFilter === 'done' || taskStatusFilter === 'canceled')) {
      taskStatusFilter = ''
    }
  }

  function updateTaskSearch(event: Event & { currentTarget: HTMLInputElement }) {
    taskSearch = event.currentTarget.value
    teamOffset = 0
  }

  function updateIdeaSearch(event: Event & { currentTarget: HTMLInputElement }) {
    ideaSearch = event.currentTarget.value
    ideaOffset = 0
  }

  function updateTaskStatus(event: Event & { currentTarget: HTMLSelectElement }) {
    taskStatusFilter = event.currentTarget.value as TodoStatus | ''
    teamOffset = 0
  }

  function updateTaskPriority(event: Event & { currentTarget: HTMLSelectElement }) {
    taskPriorityFilter = event.currentTarget.value as TodoPriority | ''
    teamOffset = 0
  }

  function updateTaskAssignee(event: Event & { currentTarget: HTMLSelectElement }) {
    taskAssigneeFilter = event.currentTarget.value
    teamOffset = 0
  }

  function updateIdeaStatus(event: Event & { currentTarget: HTMLSelectElement }) {
    ideaStatusFilter = event.currentTarget.value as IdeaStatus | ''
    ideaOffset = 0
  }

  function updateIdeaVisibility(event: Event & { currentTarget: HTMLSelectElement }) {
    ideaVisibilityFilter = event.currentTarget.value as IdeaVisibility | ''
    ideaOffset = 0
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

  function buildComposerAssigneeOptions(members: WorkforceMemberViewModel[]): SelectOption[] {
    const options: SelectOption[] = [{ label: t('workhub.assignee.unassigned'), value: '' }]
    for (const member of members) {
      if (!member.id || workforceAssigneeKind(member) !== 'agent') continue
      const role = member.primaryRoleLabel || member.positionDisplayName
      options.push({
        label: role ? `${workforceMemberName(member)} · ${role}` : workforceMemberName(member),
        value: member.id
      })
    }
    return options
  }

  function consultationTarget(assigneeID: string) {
    const member = workforceMembers.find((candidate) => candidate.id === assigneeID)
    const id = (member?.profileId || member?.id || '').trim()
    if (!member || !id || workforceAssigneeKind(member) !== 'agent') return undefined
    return {
      directTargetId: id,
      directTargetKind: 'agent' as const,
      directTargetName: workforceMemberName(member)
    }
  }

  function composerPlaceholder() {
    return composerMode === 'assign'
      ? t('workhub.composer.assignPlaceholder')
      : t('workhub.composer.consultPlaceholder')
  }

  function taskFeedFilterOptions(): Array<{ value: TaskFeedFilter; label: string }> {
    return [
      { value: 'all', label: t('workhub.feed.all') },
      { value: 'active', label: t('workhub.feed.active') },
      { value: 'review', label: t('workhub.feed.review') },
      { value: 'done', label: t('workhub.feed.done') }
    ]
  }

  function filteredHomeTaskFeed() {
    return myTaskFeed.filter((task) => taskMatchesTaskFeedFilter(task, taskFeedFilter))
  }

  function taskMatchesTaskFeedFilter(task: TodoItemDTO, filter: TaskFeedFilter) {
    if (filter === 'all') return true
    if (filter === 'active') return ['inbox', 'planned', 'in_progress', 'blocked'].includes(task.status)
    if (filter === 'review') return task.status === 'review'
    return task.status === 'done'
  }

  function taskAssigneeMember(task: TodoItemDTO) {
    const id = task.assignee_id?.trim()
    if (!id) return undefined
    return workforceMembers.find((member) => [member.id, member.profileId, member.agentInstanceId].includes(id))
  }

  function taskAssigneeProfileId(task: TodoItemDTO) {
    return taskAssigneeMember(task)?.profileId || task.assignee_id || ''
  }

  function taskAssigneeIsHuman(task: TodoItemDTO) {
    const member = taskAssigneeMember(task)
    if (member) return workforceAssigneeKind(member) === 'human'
    return task.assignee_kind === 'human' || task.assignee_id === session.actorId
  }

  function taskSecondaryLine(task: TodoItemDTO) {
    const owner = task.assignee_name || t('workhub.assignee.unassigned')
    const priority = t(`workhub.priority.${task.priority}` as TranslationKey)
    const time = shortDate(task.updated_at || task.created_at)
    return [owner, task.issue_number, priority, time].filter(Boolean).join(' · ')
  }

  function taskStatusPillClass(status: TodoStatus) {
    if (status === 'done') return 'bg-emerald-50 text-emerald-700'
    if (status === 'review') return 'bg-amber-50 text-amber-700'
    if (status === 'blocked') return 'bg-rose-50 text-rose-700'
    return 'bg-[#f0f0f0] text-[#3a3a3a]'
  }

  function taskStatusDotClass(status: TodoStatus) {
    if (status === 'done') return 'bg-emerald-500'
    if (status === 'review') return 'bg-amber-500'
    if (status === 'blocked') return 'bg-rose-500'
    return 'bg-[#8a8a8a]'
  }

  function taskStatusOptions() {
    return [
      { label: t('workhub.filter.allStatus'), value: '' },
      ...todoStatuses.map((status) => ({ label: t(`workhub.status.${status}` as TranslationKey), value: status }))
    ]
  }

  function taskPriorityOptions() {
    return [
      { label: t('workhub.filter.allPriority'), value: '' },
      ...todoPriorities.map((priority) => ({ label: t(`workhub.priority.${priority}` as TranslationKey), value: priority }))
    ]
  }

  function ideaStatusOptions() {
    const statuses: IdeaStatus[] = ['draft', 'submitted', 'under_review', 'accepted', 'experiment', 'converted', 'rejected', 'archived']
    return [
      { label: t('workhub.filter.allStatus'), value: '' },
      ...statuses.map((status) => ({ label: t(`workhub.ideaStatus.${status}` as TranslationKey), value: status }))
    ]
  }

  function ideaVisibilityOptions() {
    const values: IdeaVisibility[] = ['organization', 'department', 'private']
    return [
      { label: t('workhub.filter.allVisibility'), value: '' },
      ...values.map((value) => ({ label: t(`workhub.visibility.${value}` as TranslationKey), value }))
    ]
  }
</script>

<svelte:head>
  <title>{dedicated ? t(initialTab === 'ideas' ? 'nav.ideas' : 'nav.todos') : t('workhub.title')} | Orrery</title>
</svelte:head>

<div
  class="mx-auto pb-24 {dedicated ? 'max-w-[880px] pt-2' : 'max-w-[720px] px-6 pt-10 sm:pt-16'}"
  data-testid="workhub-page"
  data-visual-reference={dedicated ? undefined : 'figma-home'}
  data-management-page={dedicated ? initialTab : undefined}
>
  {#if dedicated}
    <header class="mb-6 flex flex-col gap-4 border-b border-[#ececec] pb-5 sm:flex-row sm:items-end sm:justify-between">
      <div class="min-w-0">
        <div class="flex items-center gap-2.5">
          <span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg {initialTab === 'ideas' ? 'bg-amber-50 text-amber-700' : 'bg-emerald-50 text-emerald-700'}">
            {#if initialTab === 'ideas'}
              <Lightbulb class="h-4.5 w-4.5" />
            {:else}
              <CheckCircle2 class="h-4.5 w-4.5" />
            {/if}
          </span>
          <div class="min-w-0">
            <h1 class="page-title">{t(initialTab === 'ideas' ? 'nav.ideas' : 'nav.todos')}</h1>
            <p class="page-description">{t(initialTab === 'ideas' ? 'nav.ideas.description' : 'nav.todos.description')}</p>
          </div>
        </div>
      </div>
      {#if (initialTab === 'ideas' && canWriteIdeas) || (initialTab === 'team' && canWriteTodos)}
        <Button onclick={() => openCreate(initialTab === 'ideas' ? 'idea' : 'task')}>
          <Plus class="h-4 w-4" />
          {t(initialTab === 'ideas' ? 'workhub.action.captureIdea' : 'workhub.action.createTask')}
        </Button>
      {/if}
    </header>
  {:else}
    <header class="mb-8 text-center">
      <h1 class="text-[26px] font-semibold leading-tight text-black">{t('workhub.heroTitle')}</h1>
      <p class="mt-1.5 text-[14px] leading-5 text-[#8a8a8a]">{t('workhub.heroSubtitle')}</p>
    </header>
  {/if}

  {#if canConsult && !dedicated}
    <section class="w-full" aria-label={t('workhub.quickPlaceholder')}>
      <div class="mb-4 flex justify-center">
        <div class="inline-flex rounded-full border border-[#ececec] bg-white p-0.5">
          <button
            type="button"
            class="rounded-full px-4 py-1 text-[13px] transition-colors {composerMode === 'assign' ? 'bg-brand text-white' : 'text-[#6a6a6a] hover:text-black'}"
            onclick={() => (composerMode = 'assign')}
          >
            {t('workhub.composer.assign')}
          </button>
          <button
            type="button"
            class="rounded-full px-4 py-1 text-[13px] transition-colors {composerMode === 'consult' ? 'bg-brand text-white' : 'text-[#6a6a6a] hover:text-black'}"
            onclick={() => (composerMode = 'consult')}
          >
            {t('workhub.composer.consult')}
          </button>
        </div>
      </div>

      <div class="rounded-2xl border border-[#e4e4e4] bg-white shadow-[0_1px_2px_rgba(0,0,0,0.04)] transition-shadow focus-within:shadow-[0_2px_10px_rgba(0,0,0,0.06)]">
        <textarea
          bind:value={quickTitle}
          rows="3"
          placeholder={composerPlaceholder()}
          aria-label={t('workhub.quickPlaceholder')}
          class="w-full resize-none bg-transparent px-4 pt-4 text-[14px] leading-relaxed text-black outline-none placeholder:text-[#a8a8a8]"
          onkeydown={handleQuickCreateKeydown}
        ></textarea>
        <div class="flex items-center justify-between gap-3 px-3 pb-3">
          <div class="flex min-w-0 items-center gap-1.5">
            <label class="relative flex min-w-0 items-center rounded-lg border border-[#ececec] bg-white hover:bg-[#f6f6f6]">
              {#if composerAssignee}
                <WorkforceAvatar
                  profileId={composerAssigneeMember()?.profileId || composerAssignee}
                  name={composerAssigneeName()}
                  human={composerAssigneeMember() ? workforceAssigneeKind(composerAssigneeMember()!) === 'human' : composerAssignee === session.actorId}
                  class="pointer-events-none absolute left-2 h-4 w-4 rounded-full"
                  textClass="text-[8px]"
                />
              {:else}
                <span class="pointer-events-none absolute left-2 flex h-4 w-4 items-center justify-center rounded-full bg-[#e8e8e8] text-[9px] font-medium text-[#3a3a3a]">--</span>
              {/if}
              <select
                bind:value={composerAssignee}
                aria-label={t('workhub.composer.assignee')}
                class="h-7 max-w-48 appearance-none truncate bg-transparent py-0 pl-8 pr-7 text-[12.5px] text-[#3a3a3a] outline-none"
              >
                {#each composerAssigneeOptions as option}
                  <option value={option.value}>{option.label}</option>
                {/each}
              </select>
              <ChevronDown class="pointer-events-none absolute right-2 h-3.5 w-3.5 text-[#a0a0a0]" />
            </label>
          </div>
          <button
            type="button"
            disabled={!quickTitle.trim() || !canConsult}
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-brand text-white transition-opacity disabled:opacity-25"
            aria-label={t('workhub.composer.openConsultation')}
            onclick={submitComposer}
          >
            <ArrowUp class="h-4 w-4" />
          </button>
        </div>
      </div>

      <div class="mt-3 flex flex-wrap justify-center gap-2">
        {#each ['workhub.suggestion.brief', 'workhub.suggestion.contracts', 'workhub.suggestion.blockers', 'workhub.suggestion.ideas'] as key}
          <button
            type="button"
            class="flex items-center gap-1.5 rounded-full border border-[#ececec] bg-white px-3 py-1.5 text-[12.5px] text-[#5a5a5a] hover:border-[#d5d5d5] hover:text-black"
            onclick={() => (quickTitle = t(key as TranslationKey))}
          >
            <Lightbulb class="h-3.5 w-3.5 text-[#b0b0b0]" />
            {t(key as TranslationKey)}
          </button>
        {/each}
      </div>
    </section>
  {/if}

  {#if actionMessage}
    <div class="mt-6 flex items-start justify-between gap-3 rounded-lg border px-3 py-2.5 text-sm {actionError ? 'border-rose-200 bg-rose-50 text-rose-800 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-200' : 'border-teal-200 bg-teal-50 text-teal-800 dark:border-teal-900 dark:bg-teal-950/30 dark:text-teal-200'}">
      <span>{actionMessage}</span>
      <button type="button" class="rounded p-0.5 opacity-70 hover:opacity-100" aria-label={t('workhub.action.close')} onclick={clearMessage}>
        <X class="h-4 w-4" />
      </button>
    </div>
  {/if}

  <section class="w-full {dedicated ? 'mt-0' : 'mt-12'}" aria-label={dedicated ? t(initialTab === 'ideas' ? 'nav.ideas' : 'nav.todos') : t('workhub.title')}>
    {#if !dedicated}
      <div class="mb-5 flex items-center gap-5 border-b border-[#ececec]">
        {#each [
          { value: 'focus' as HubTab, label: t('workhub.tab.focus') },
          { value: 'ideas' as HubTab, label: t('workhub.tab.ideas') },
          { value: 'team' as HubTab, label: t('workhub.tab.team') }
        ] as tab}
          <button
            type="button"
            class="-mb-px border-b-2 pb-2.5 text-[13px] transition-colors {activeTab === tab.value ? 'border-black font-medium text-black' : 'border-transparent text-[#8a8a8a] hover:text-black'}"
            aria-pressed={activeTab === tab.value}
            onclick={() => selectTab(tab.value)}
          >
            {tab.label}
          </button>
        {/each}
      </div>
    {/if}

    {#if activeTab === 'focus'}
      <WorkInbox />
    {:else if activeViewLoading}
      <LoadingState title={t('workhub.loading')} description="" />
    {:else if activeViewError}
      <ErrorState title={t('workhub.error')} message={t('workhub.message.operationFailed')} />
      <!-- Legacy markup preserved in web/legacy-backup/home-focus.svelte.txt. -->
    {:else if activeTab === 'ideas'}
      <div class="overflow-hidden rounded-xl border border-[#ececec] bg-white">
        <div class="flex flex-col gap-2 border-b border-[#ececec] p-3 sm:flex-row sm:items-center">
          <div class="relative min-w-0 flex-1">
            <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-[#a0a0a0]" />
            <Input value={ideaSearch} oninput={updateIdeaSearch} class="pl-9" placeholder={t('workhub.filter.searchIdeas')} ariaLabel={t('workhub.filter.searchIdeas')} />
          </div>
          <Select value={ideaStatusFilter} onchange={updateIdeaStatus} class="sm:w-40 sm:flex-none" options={ideaStatusOptions()} />
          <Select value={ideaVisibilityFilter} onchange={updateIdeaVisibility} class="sm:w-40 sm:flex-none" options={ideaVisibilityOptions()} />
          {#if canWriteIdeas && !dedicated}
            <Button variant="outline" size="sm" onclick={() => openCreate('idea')}>
              <Lightbulb class="h-3.5 w-3.5" />
              {t('workhub.action.captureIdea')}
            </Button>
          {/if}
        </div>
        {#if !dedicated}
          <div class="grid grid-cols-2 border-b border-[#ececec] bg-[#fafafa] sm:grid-cols-4">
            {@render pipelineStat('submitted', summary?.ideas_submitted ?? 0, Lightbulb)}
            {@render pipelineStat('under_review', summary?.ideas_under_review ?? 0, Users)}
            {@render pipelineStat('experiment', summary?.ideas_in_experiment ?? 0, FlaskConical)}
            {@render pipelineStat('converted', summary?.ideas_converted ?? 0, CheckCircle2)}
          </div>
        {/if}
        {#if filteredIdeas.length}
          {#each filteredIdeas as idea (idea.idea_id)}
            <IdeaReviewRow
              {idea}
              editable={canWriteIdeas}
              busy={ideaMutationPending}
              onVote={voteIdea}
              onTransition={changeIdeaStatus}
              onReview={openReviewIdea}
              onConvert={openConvertIdea}
            />
          {/each}
        {:else}
          <EmptyState title={t('workhub.empty.ideas')} description={t('workhub.empty.ideaDescription')} />
        {/if}
        {@render pagination(ideaOffset, ideasQuery.data?.total ?? 0, Boolean(ideasQuery.data?.has_more), (offset) => (ideaOffset = offset))}
      </div>
    {:else}
      <div class="space-y-3">
        <div class="flex flex-col gap-2 rounded-xl border border-[#ececec] bg-white p-3">
          <div class="relative min-w-0 flex-1">
            <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-[#a0a0a0]" />
            <Input value={taskSearch} oninput={updateTaskSearch} class="pl-9" placeholder={t('workhub.filter.searchTasks')} ariaLabel={t('workhub.filter.searchTasks')} />
          </div>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-[repeat(3,minmax(0,1fr))_auto]">
            <Select value={taskStatusFilter} onchange={updateTaskStatus} options={taskStatusOptions()} />
            <Select value={taskPriorityFilter} onchange={updateTaskPriority} options={taskPriorityOptions()} />
            <Select
              value={taskAssigneeFilter}
              onchange={updateTaskAssignee}
              options={[
                { label: t('workhub.filter.allOwners'), value: '' },
                { label: t('workhub.filter.unassigned'), value: '__unassigned' },
                ...assigneeOptions.filter((option) => option.value)
              ]}
            />
            <div class="inline-flex h-9 items-center justify-self-start rounded-lg border border-[#ececec] bg-white p-0.5 sm:justify-self-end">
              <button type="button" class="grid h-8 w-8 place-items-center rounded-md {teamMode === 'list' ? 'bg-[#f0f0f0] text-black' : 'text-[#8a8a8a]'}" aria-label={t('workhub.mode.list')} onclick={() => setTeamMode('list')}><LayoutList class="h-4 w-4" /></button>
              <button type="button" class="grid h-8 w-8 place-items-center rounded-md {teamMode === 'board' ? 'bg-[#f0f0f0] text-black' : 'text-[#8a8a8a]'}" aria-label={t('workhub.mode.board')} onclick={() => setTeamMode('board')}><Columns3 class="h-4 w-4" /></button>
            </div>
          </div>
        </div>

        {#if teamMode === 'list'}
          <div class="overflow-hidden rounded-xl border border-[#ececec] bg-white" data-testid="team-task-list">
            {#if filteredTeamTasks.length}
              {#each filteredTeamTasks as task (task.todo_id)}
                <WorkTaskRow
                  {task}
                  editable={canWriteTodos}
                  assigneeOptions={assigneeOptionsFor(task)}
                  busy={todoMutationPending}
                  onStatusChange={changeTaskStatus}
                  onAssigneeChange={changeTaskAssignee}
                  onComplete={completeTask}
                />
              {/each}
            {:else}
              <EmptyState title={t('workhub.empty.tasks')} description={t('workhub.empty.filterDescription')} />
            {/if}
          </div>
        {:else}
          <div class="grid min-w-0 gap-3 sm:grid-cols-2" data-testid="team-task-board">
            {#each openTodoStatuses as status}
              <section class="min-w-0 rounded-xl border border-[#ececec] bg-[#fafafa] p-2" aria-label={t(`workhub.status.${status}` as TranslationKey)}>
                <div class="flex h-8 items-center justify-between px-1 text-xs font-semibold text-black">
                  <span>{t(`workhub.status.${status}` as TranslationKey)}</span>
                  <span class="rounded bg-white px-1.5 py-0.5 text-[#8a8a8a]">{tasksForBoard(status).length}</span>
                </div>
                <div class="mt-1 space-y-2">
                  {#each tasksForBoard(status) as task (task.todo_id)}
                    <article class="min-h-28 rounded-xl border border-[#ececec] bg-white p-3">
                      <a class="line-clamp-2 text-sm font-medium leading-5 hover:underline" href={`/todos/${encodeURIComponent(task.todo_id)}`}>{task.title}</a>
                      <div class="mt-2 flex items-center justify-between text-xs text-[#8a8a8a]">
                        <span class="font-mono">{task.issue_number ?? task.todo_id}</span>
                        <span class={priorityTextClass(task.priority)}>{t(`workhub.priority.${task.priority}` as TranslationKey)}</span>
                      </div>
                      <div class="mt-3 flex items-center justify-between gap-2 border-t border-[#ececec] pt-2 text-xs">
                        <span class="truncate text-[#8a8a8a]">{task.assignee_name || t('workhub.assignee.unassigned')}</span>
                        {#if task.due_at}<span class={isOverdue(task.due_at) ? 'text-rose-600' : 'text-[#8a8a8a]'}>{shortDate(task.due_at)}</span>{/if}
                      </div>
                    </article>
                  {/each}
                </div>
              </section>
            {/each}
          </div>
        {/if}
        {@render pagination(teamOffset, teamTodosQuery.data?.total ?? 0, Boolean(teamTodosQuery.data?.has_more), (offset) => (teamOffset = offset))}
      </div>
    {/if}
  </section>
</div>

<Dialog open={createOpen} title={createKind === 'task' ? t('workhub.dialog.createTask') : t('workhub.dialog.captureIdea')} size="wide" onClose={() => (createOpen = false)}>
  <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_18rem]">
    <div class="min-w-0 space-y-4">
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{createKind === 'task' ? t('workhub.form.taskTitle') : t('workhub.form.ideaTitle')}</span>
        <Input bind:value={createTitle} ariaLabel={createKind === 'task' ? t('workhub.form.taskTitle') : t('workhub.form.ideaTitle')} />
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{createKind === 'task' ? t('workhub.form.taskDescription') : t('workhub.form.ideaContent')}</span>
        <MarkdownEditor
          bind:value={createBody}
          ariaLabel={createKind === 'task' ? t('workhub.form.taskDescription') : t('workhub.form.ideaContent')}
          placeholder={createKind === 'task' ? t('workhub.placeholder.taskDescription') : t('workhub.placeholder.ideaContent')}
          minHeight="min-h-60"
          writeLabel={t('home.markdown.write')}
          previewLabel={t('home.markdown.preview')}
          toolbarLabels={toolbarLabels()}
        />
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.tags')}</span>
        <Input bind:value={createTags} ariaLabel={t('workhub.form.tags')} placeholder={t('workhub.placeholder.tags')} />
      </label>
    </div>
    <div class="space-y-4 border-t border-border pt-4 lg:border-l lg:border-t-0 lg:pl-5 lg:pt-0">
      {#if createKind === 'task'}
        {@render fieldSelect(t('workhub.form.status'), createTaskStatus, (value) => (createTaskStatus = value as TodoStatus), [
          { label: t('workhub.status.inbox'), value: 'inbox' },
          { label: t('workhub.status.planned'), value: 'planned' },
          { label: t('workhub.status.in_progress'), value: 'in_progress' }
        ])}
        {@render fieldSelect(t('workhub.form.priority'), createPriority, (value) => (createPriority = value as TodoPriority), todoPriorities.map((priority) => ({ label: t(`workhub.priority.${priority}` as TranslationKey), value: priority })))}
        {@render fieldSelect(t('workhub.form.workKind'), createWorkKind, (value) => { createWorkKind = value as TodoWorkKind; if (createWorkKind === 'general') createDevelopmentProjectID = '' }, [{ label: t('workhub.workKind.general'), value: 'general' }, { label: t('workhub.workKind.development'), value: 'development' }])}
        {@render fieldSelect(t('workhub.form.automationMode'), createAutomationMode, (value) => (createAutomationMode = value as TodoAutomationMode), [{ label: t('workhub.automationMode.manual'), value: 'manual' }, { label: t('workhub.automationMode.suggest'), value: 'suggest' }, { label: t('workhub.automationMode.managed'), value: 'managed' }])}
        {#if createWorkKind === 'development'}
          {@render fieldSelect(t('workhub.form.developmentProject'), createDevelopmentProjectID, (value) => (createDevelopmentProjectID = value), [{ label: '-', value: '' }, ...developmentProjects.map((project) => ({ label: project.name, value: project.development_project_id }))])}
        {/if}
        {@render fieldSelect(t('workhub.form.assignee'), createAssignee, (value) => (createAssignee = value), assigneeOptions)}
        <label class="block space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.dueDate')}</span>
          <Input bind:value={createDueAt} type="datetime-local" ariaLabel={t('workhub.form.dueDate')} />
        </label>
        <label class="block space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.acceptance')}</span>
          <textarea bind:value={createAcceptance} aria-label={t('workhub.form.acceptance')} placeholder={t('workhub.placeholder.acceptance')} class="min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
        </label>
      {:else}
        {@render fieldSelect(t('workhub.form.status'), createIdeaStatus, (value) => (createIdeaStatus = value as IdeaStatus), [
          { label: t('workhub.ideaStatus.submitted'), value: 'submitted' },
          { label: t('workhub.ideaStatus.draft'), value: 'draft' }
        ])}
        {@render fieldSelect(t('workhub.form.visibility'), createVisibility, (value) => (createVisibility = value as IdeaVisibility), ['organization', 'department', 'private'].map((value) => ({ label: t(`workhub.visibility.${value}` as TranslationKey), value })))}
        {@render fieldSelect(t('workhub.form.impact'), createImpact, (value) => (createImpact = value as IdeaImpact), ['high', 'medium', 'low'].map((value) => ({ label: t(`workhub.impact.${value}` as TranslationKey), value })))}
        {@render fieldSelect(t('workhub.form.effort'), createEffort, (value) => (createEffort = value as IdeaEffort), ['small', 'medium', 'large'].map((value) => ({ label: t(`workhub.effort.${value}` as TranslationKey), value })))}
      {/if}
    </div>
  </div>
  <div class="mt-5 flex justify-end gap-2 border-t border-border pt-4">
    <Button variant="ghost" onclick={() => (createOpen = false)}>{t('workhub.action.cancel')}</Button>
    <Button disabled={!canCreate || createPending || (createKind === 'task' ? !canWriteTodos : !canWriteIdeas)} onclick={submitCreate}>
      {#if createKind === 'idea'}<Sparkles class="h-4 w-4" />{:else}<Plus class="h-4 w-4" />{/if}
      {createKind === 'task' ? t('workhub.action.createTask') : t('workhub.action.captureIdea')}
    </Button>
  </div>
</Dialog>

<Dialog open={Boolean(blockedTask)} title={t('workhub.dialog.moveBlocked')} onClose={() => (blockedTask = undefined)}>
  <div class="space-y-4">
    <p class="text-sm font-medium text-foreground">{blockedTask?.title}</p>
    <label class="block space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.blockedReason')}</span>
      <textarea bind:value={blockedReason} aria-label={t('workhub.form.blockedReason')} placeholder={t('workhub.placeholder.blockedReason')} class="min-h-28 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
    </label>
    <div class="flex justify-end gap-2">
      <Button variant="ghost" onclick={() => (blockedTask = undefined)}>{t('workhub.action.cancel')}</Button>
      <Button disabled={!canWriteTodos || !blockedReason.trim() || todoMutationPending} onclick={applyBlockedStatus}>{t('workhub.action.apply')}</Button>
    </div>
  </div>
</Dialog>

<Dialog open={Boolean(reviewIdea)} title={t('workhub.dialog.reviewIdea')} onClose={() => (reviewIdea = undefined)}>
  <div class="space-y-4">
    <div>
      <p class="font-medium text-foreground">{reviewIdea?.title}</p>
      {#if reviewIdea?.content}<p class="mt-1 line-clamp-3 text-sm text-muted-foreground">{reviewIdea.content}</p>{/if}
    </div>
    {@render fieldSelect(t('workhub.form.decision'), reviewStatus, (value) => (reviewStatus = value as IdeaStatus), reviewStatusOptions.map((status) => ({ label: t(`workhub.ideaStatus.${status}` as TranslationKey), value: status })))}
    {@render fieldSelect(t('workhub.form.reviewer'), reviewReviewer, (value) => (reviewReviewer = value), assigneeOptions.filter((option) => option.value))}
    <label class="block space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.reason')}</span>
      <textarea bind:value={reviewReason} aria-label={t('workhub.form.reason')} placeholder={t('workhub.placeholder.reason')} class="min-h-28 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
    </label>
    <div class="flex justify-end gap-2">
      <Button variant="ghost" onclick={() => (reviewIdea = undefined)}>{t('workhub.action.cancel')}</Button>
      <Button disabled={!canWriteIdeas || !canApplyReview || ideaMutationPending} onclick={applyIdeaReview}>{t('workhub.action.apply')}</Button>
    </div>
  </div>
</Dialog>

<Dialog open={Boolean(convertIdea)} title={t('workhub.dialog.convertIdea')} size="wide" onClose={() => (convertIdea = undefined)}>
  <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_18rem]">
    <div class="space-y-4">
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.taskTitle')}</span>
        <Input bind:value={convertTitle} ariaLabel={t('workhub.form.taskTitle')} />
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.taskDescription')}</span>
        <MarkdownEditor bind:value={convertBody} ariaLabel={t('workhub.form.taskDescription')} minHeight="min-h-52" writeLabel={t('home.markdown.write')} previewLabel={t('home.markdown.preview')} toolbarLabels={toolbarLabels()} />
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.reason')}</span>
        <textarea bind:value={convertReason} aria-label={t('workhub.form.reason')} placeholder={t('workhub.placeholder.reason')} class="min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
      </label>
    </div>
    <div class="space-y-4 border-t border-border pt-4 lg:border-l lg:border-t-0 lg:pl-5 lg:pt-0">
      {@render fieldSelect(t('workhub.form.priority'), convertPriority, (value) => (convertPriority = value as TodoPriority), todoPriorities.map((priority) => ({ label: t(`workhub.priority.${priority}` as TranslationKey), value: priority })))}
      {@render fieldSelect(t('workhub.form.assignee'), convertAssignee, (value) => (convertAssignee = value), assigneeOptions)}
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.dueDate')}</span>
        <Input bind:value={convertDueAt} type="datetime-local" ariaLabel={t('workhub.form.dueDate')} />
      </label>
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('workhub.form.acceptance')}</span>
        <textarea bind:value={convertAcceptance} aria-label={t('workhub.form.acceptance')} placeholder={t('workhub.placeholder.acceptance')} class="min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"></textarea>
      </label>
    </div>
  </div>
  <div class="mt-5 flex justify-end gap-2 border-t border-border pt-4">
    <Button variant="ghost" onclick={() => (convertIdea = undefined)}>{t('workhub.action.cancel')}</Button>
    <Button disabled={!canWriteIdeas || !convertTitle.trim() || ideaMutationPending} onclick={applyIdeaConversion}>{t('workhub.action.convert')}</Button>
  </div>
</Dialog>

{#snippet pipelineStat(status: IdeaStatus, value: number, Icon: typeof Lightbulb)}
  <div class="flex min-h-16 items-center gap-2 border-r border-border px-4 py-3 last:border-r-0">
    <Icon class="h-4 w-4 text-muted-foreground" />
    <div>
      <div class="text-sm font-semibold text-foreground">{value}</div>
      <div class="text-xs text-muted-foreground">{t(`workhub.ideaStatus.${status}` as TranslationKey)}</div>
    </div>
  </div>
{/snippet}

{#snippet pagination(offset: number, total: number, hasMore: boolean, setOffset: (offset: number) => void)}
  {#if total > pageSize || offset > 0}
    <div class="flex items-center justify-between gap-3 border-t border-border px-3 py-2.5">
      <span class="text-xs text-muted-foreground">{t('workhub.pagination.info', { page: Math.floor(offset / pageSize) + 1, total })}</span>
      <div class="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon"
          disabled={offset <= 0}
          ariaLabel={t('workhub.pagination.previous')}
          title={t('workhub.pagination.previous')}
          onclick={() => setOffset(Math.max(0, offset - pageSize))}
        >
          <ChevronLeft class="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          disabled={!hasMore}
          ariaLabel={t('workhub.pagination.next')}
          title={t('workhub.pagination.next')}
          onclick={() => setOffset(offset + pageSize)}
        >
          <ChevronRight class="h-4 w-4" />
        </Button>
      </div>
    </div>
  {/if}
{/snippet}

{#snippet fieldSelect(label: string, value: string, setValue: (value: string) => void, options: Array<{ label: string; value: string }>)}
  <label class="block space-y-1.5">
    <span class="text-xs font-medium text-muted-foreground">{label}</span>
    <select value={value} aria-label={label} class="h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm outline-none focus:ring-1 focus:ring-ring" onchange={(event) => setValue(event.currentTarget.value)}>
      {#each options as option}
        <option value={option.value}>{option.label}</option>
      {/each}
    </select>
  </label>
{/snippet}

<script lang="ts" module>
  function resolvedTimeZone() {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  }

  function optionalText(value: string) {
    return value.trim() || undefined
  }

  function isOverdue(value?: string) {
    if (!value) return false
    const time = Date.parse(value)
    return Number.isFinite(time) && time < Date.now()
  }

  function shortDate(value?: string) {
    if (!value) return ''
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return ''
    return date.toLocaleDateString(undefined, { month: '2-digit', day: '2-digit' })
  }

  function priorityTextClass(priority: TodoPriority) {
    if (priority === 'urgent') return 'font-medium text-rose-600'
    if (priority === 'high') return 'font-medium text-amber-600'
    return 'text-muted-foreground'
  }

  function priorityFromIdea(idea: IdeaItemDTO): TodoPriority {
    if (idea.impact === 'high' && idea.effort === 'small') return 'urgent'
    if (idea.impact === 'high') return 'high'
    if (idea.impact === 'low') return 'low'
    return 'medium'
  }
</script>
