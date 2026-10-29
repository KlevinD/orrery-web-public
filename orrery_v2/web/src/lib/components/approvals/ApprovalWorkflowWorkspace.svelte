<script lang="ts">
  import { ArrowLeft, CheckCircle2, Plus } from 'lucide-svelte'
  import WorkflowTemplateEditor from '#lib/components/approvals/WorkflowTemplateEditor.svelte'
  import WorkflowTemplateViewer from '#lib/components/approvals/WorkflowTemplateViewer.svelte'
  import { workflowRevisionLabel } from '#lib/components/approvals/approvalDisplayLabels'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import PermissionDeniedState from '#lib/components/ui/PermissionDeniedState.svelte'
  import { locale } from '#lib/i18n'
  import { hasPermissionScope } from '#lib/permissions/access'
  import {
    useDisableHILWorkflowTemplateMutation,
    useHILWorkflowTemplateListQuery,
    useHILWorkflowTemplateQuery,
    usePublishHILWorkflowTemplateMutation,
    useSaveHILWorkflowTemplateMutation,
    type HILWorkflowTemplate,
    type PublishHILWorkflowTemplateInput,
    type SaveHILWorkflowTemplateInput
  } from '#lib/queries/approvals'
  import { session } from '#lib/stores/session.svelte'
  import type { ApiClientError } from '#lib/api/client'
  let { embedded = false }: { embedded?: boolean } = $props()

  let selectedTemplateId = $state('')
  let creatingTemplate = $state(false)
  let actionMessage = $state('')
  let actionError = $state<ApiClientError | undefined>()
  const canRead = $derived(hasPermissionScope(session.permissionScope, 'approvals.read'))
  const canManage = $derived(hasPermissionScope(session.permissionScope, 'approvals.manage'))
  const templateListQuery = useHILWorkflowTemplateListQuery(() => ({}), () => canRead)
  const templateQuery = useHILWorkflowTemplateQuery(
    () => selectedTemplateId,
    () => canRead && !creatingTemplate && Boolean(selectedTemplateId)
  )
  const saveTemplateMutation = useSaveHILWorkflowTemplateMutation()
  const publishTemplateMutation = usePublishHILWorkflowTemplateMutation()
  const disableTemplateMutation = useDisableHILWorkflowTemplateMutation()
  const templateBusy = $derived(saveTemplateMutation.isPending || publishTemplateMutation.isPending || disableTemplateMutation.isPending)

  $effect(() => {
    if (!templateListQuery.data || creatingTemplate) return
    const next = templateListQuery.data.items.some((item) => item.id === selectedTemplateId)
      ? selectedTemplateId
      : (templateListQuery.data.items[0]?.id ?? '')
    if (next !== selectedTemplateId) selectedTemplateId = next
  })

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function resetFeedback() {
    actionMessage = ''
    actionError = undefined
  }

  function setError(error: unknown) {
    actionError = error as ApiClientError
    actionMessage = ''
  }

  function saveTemplate(body: SaveHILWorkflowTemplateInput) {
    resetFeedback()
    saveTemplateMutation.mutate({ templateId: creatingTemplate ? undefined : selectedTemplateId || undefined, body }, {
      onSuccess: (data) => {
        const saved = data as HILWorkflowTemplate
        creatingTemplate = false
        selectedTemplateId = saved.id
        actionMessage = text('模板草稿已保存', 'Template draft saved')
      },
      onError: setError
    })
  }

  function publishTemplate(body: PublishHILWorkflowTemplateInput) {
    if (!selectedTemplateId) return
    resetFeedback()
    publishTemplateMutation.mutate({ templateId: selectedTemplateId, body }, {
      onSuccess: () => (actionMessage = text('审批流版本已发布', 'Workflow version published')),
      onError: setError
    })
  }

  function disableTemplate(reason: string) {
    if (!selectedTemplateId) return
    resetFeedback()
    disableTemplateMutation.mutate({ templateId: selectedTemplateId, reason }, {
      onSuccess: () => (actionMessage = text('审批流模板已停用', 'Workflow template disabled')),
      onError: setError
    })
  }
</script>

<svelte:head>
  <title>{text('审批流程', 'Approval workflows')} | Orrery</title>
</svelte:head>

<section class="flex min-w-0 flex-col gap-4 {embedded ? '' : 'page-shell min-h-[calc(100vh-5rem)] max-w-[1180px]'}">
  <a href="/work-items" class="inline-flex w-fit items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
    <ArrowLeft class="h-4 w-4" />{text('返回事务中心', 'Back to work center')}
  </a>
  <header class="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-4">
    <div>
      <h1 class="page-title">{text('审批流程', 'Approval workflows')}</h1>
      <p class="mt-1 text-[13px] text-muted-foreground">
        {canManage
          ? text('管理普通审批与合议的系统路由模板', 'Manage system routing templates for approvals and councils')
          : text('查看不同业务操作会经过哪些审批步骤', 'See the approval steps used for different business actions')}
      </p>
    </div>
    {#if canManage}
      <Button onclick={() => { creatingTemplate = true; selectedTemplateId = ''; resetFeedback() }}>
        <Plus class="h-4 w-4" />{text('新建模板', 'New template')}
      </Button>
    {/if}
  </header>

  {#if actionMessage}
    <div class="flex items-center gap-2 border-y border-status-done/30 bg-status-done-bg px-3 py-2 text-[13px] text-status-done">
      <CheckCircle2 class="h-4 w-4" />{actionMessage}
    </div>
  {:else if actionError}
    <ErrorState message={actionError.message} correlationId={actionError.correlationId} />
  {/if}

  {#if !canRead}
    <PermissionDeniedState />
  {:else if templateListQuery.isLoading}
    <LoadingState title={text('加载审批流模板', 'Loading workflow templates')} />
  {:else if templateListQuery.isError}
    {@const error = templateListQuery.error as ApiClientError}
    <ErrorState message={error.message} correlationId={error.correlationId} />
  {:else}
    <div class="grid min-h-0 flex-1 gap-3 lg:grid-cols-[280px_minmax(0,1fr)]">
      <aside class="min-h-0 overflow-hidden rounded-md border border-border bg-card">
        <div class="border-b border-border px-3 py-2.5">
          <h2 class="text-[12.5px] font-medium text-foreground">{text('模板', 'Templates')}</h2>
        </div>
        <div class="subtle-scrollbar max-h-[calc(100vh-15rem)] divide-y divide-border/70 overflow-y-auto">
          {#if (templateListQuery.data?.items.length ?? 0) === 0}
            <p class="px-4 py-8 text-center text-[11.5px] text-muted-foreground">{text('暂无模板', 'No templates')}</p>
          {:else}
            {#each templateListQuery.data?.items ?? [] as item (item.id)}
              <button
                type="button"
                aria-pressed={!creatingTemplate && item.id === selectedTemplateId}
                class="w-full px-3 py-3 text-left transition-colors {(!creatingTemplate && item.id === selectedTemplateId) ? 'selection-wash' : 'hover:bg-muted/35'}"
                onclick={() => { creatingTemplate = false; selectedTemplateId = item.id; resetFeedback() }}
              >
                <span class="block truncate text-[12.5px] font-medium text-foreground">{item.name}</span>
                <span class="mt-1 flex justify-between text-[10px] text-muted-foreground">
                  <span>{item.status === 'active' ? text('使用中', 'Active') : text('已停用', 'Disabled')}</span>
                  <span>{workflowRevisionLabel(item.latest_published_version, $locale)}</span>
                </span>
              </button>
            {/each}
          {/if}
        </div>
      </aside>

      {#if !creatingTemplate && selectedTemplateId && templateQuery.isLoading}
        <LoadingState title={text('加载模板草稿', 'Loading template draft')} />
      {:else if !creatingTemplate && selectedTemplateId && templateQuery.isError}
        {@const error = templateQuery.error as ApiClientError}
        <ErrorState message={error.message} correlationId={error.correlationId} />
      {:else}
        {#key creatingTemplate ? 'new-template' : selectedTemplateId}
          {#if canManage}
            <WorkflowTemplateEditor
              template={creatingTemplate ? undefined : templateQuery.data}
              busy={templateBusy}
              onsave={saveTemplate}
              onpublish={publishTemplate}
              ondisable={disableTemplate}
            />
          {:else if templateQuery.data}
            <WorkflowTemplateViewer template={templateQuery.data} />
          {/if}
        {/key}
      {/if}
    </div>
  {/if}
</section>
