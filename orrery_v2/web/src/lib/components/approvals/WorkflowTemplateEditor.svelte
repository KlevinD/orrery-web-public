<script lang="ts">
  import { ArrowDown, ArrowUp, Plus, Save, Send, Trash2, UserRoundCheck, UsersRound } from 'lucide-svelte'
  import { approvalRiskDisplayLabel, workflowRevisionLabel } from './approvalDisplayLabels'
  import { workflowStatusLabel } from './workflowLabels'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import { locale } from '#lib/i18n'
  import type {
    HILWorkflowDefinitionDTO,
    HILWorkflowTemplateDTO,
    PublishHILWorkflowTemplateRequest,
    SaveHILWorkflowTemplateRequest
  } from '#lib/types/api'

  type RiskLevel = HILWorkflowDefinitionDTO['risk_levels'][number]
  type StageType = HILWorkflowDefinitionDTO['stages'][number]['type']
  type SelectorKind = HILWorkflowDefinitionDTO['stages'][number]['approval'] extends infer _Policy
    ? 'explicit_human' | 'people_role' | 'organization_position' | 'requester_manager' | 'resource_owner' | 'ontology_recommended'
    : never
  type ApprovalStrategy = 'single' | 'any_one' | 'all' | 'threshold'

  interface EditorSelector {
    kind: SelectorKind
    id: string
  }

  interface EditorStage {
    key: string
    name: string
    type: StageType
    strategy: ApprovalStrategy
    threshold: number
    actors: EditorSelector[]
    deadlineMinutes: number
    escalationEnabled: boolean
    escalationActor: EditorSelector
    escalationDeadlineMinutes: number
    quorum: number
    approvalThreshold: number
  }

  interface Props {
    template?: HILWorkflowTemplateDTO
    busy?: boolean
    onsave?: (body: SaveHILWorkflowTemplateRequest) => void
    onpublish?: (body: PublishHILWorkflowTemplateRequest) => void
    ondisable?: (reason: string) => void
  }

  let { template, busy = false, onsave, onpublish, ondisable }: Props = $props()
  let loadedTemplateKey = $state('')
  let name = $state('')
  let description = $state('')
  let riskLevels = $state<RiskLevel[]>(['high'])
  let stages = $state<EditorStage[]>([newStage('approval', 1)])
  let savedDraftFingerprint = $state('')
  let errors = $state<string[]>([])
  let dialog = $state<'publish' | 'disable' | ''>('')
  let bindingActionType = $state('tool_use')
  let bindingToolName = $state('mcp.marketing.wechat_publish')
  let bindingRisk = $state<RiskLevel>('high')
  let disableReason = $state('')
  const draftDirty = $derived(savedDraftFingerprint !== '' && draftFingerprint() !== savedDraftFingerprint)

  $effect(() => {
    const key = template ? `${template.id}:${template.updated_at}` : 'new'
    if (key === loadedTemplateKey) return
    loadedTemplateKey = key
    loadTemplate(template)
  })

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function stageTypeLabel(value: StageType) {
    return value === 'approval' ? text('审批阶段', 'Approval') : text('和议阶段', 'Council')
  }

  function strategyLabel(value: ApprovalStrategy) {
    const labels: Record<ApprovalStrategy, [string, string]> = {
      single: ['单人审批', 'Single approver'],
      any_one: ['任意一人通过', 'Any one approver'],
      all: ['全部人员通过', 'All approvers'],
      threshold: ['达到指定人数', 'Approval threshold']
    }
    return text(labels[value][0], labels[value][1])
  }

  function newSelector(kind: SelectorKind = 'explicit_human'): EditorSelector {
    return { kind, id: '' }
  }

  function newStage(type: StageType, number: number): EditorStage {
    return {
      key: `${type}_${number}`,
      name: type === 'approval' ? text(`审批阶段 ${number}`, `Approval stage ${number}`) : text(`和议阶段 ${number}`, `Council stage ${number}`),
      type,
      strategy: 'single',
      threshold: 1,
      actors: [newSelector()],
      deadlineMinutes: 60,
      escalationEnabled: false,
      escalationActor: newSelector('requester_manager'),
      escalationDeadlineMinutes: 30,
      quorum: 1,
      approvalThreshold: 1
    }
  }

  function loadTemplate(value?: HILWorkflowTemplateDTO) {
    name = value?.name ?? ''
    description = value?.description ?? ''
    riskLevels = value ? [...value.draft_definition.risk_levels] : ['high']
    stages = value
      ? value.draft_definition.stages.map((stage) => {
          const approval = stage.approval
          const council = stage.council
          const escalation = approval?.escalation?.[0]
          return {
            key: stage.key,
            name: stage.name,
            type: stage.type,
            strategy: approval?.strategy ?? 'single',
            threshold: approval?.threshold ?? 1,
            actors: (approval?.assignees ?? council?.participants ?? []).map((actor) => ({ kind: actor.kind, id: actor.id ?? '' })),
            deadlineMinutes: approval?.deadline_minutes ?? council?.deadline_minutes ?? 60,
            escalationEnabled: Boolean(escalation),
            escalationActor: escalation?.actors[0]
              ? { kind: escalation.actors[0].kind, id: escalation.actors[0].id ?? '' }
              : newSelector('requester_manager'),
            escalationDeadlineMinutes: escalation?.deadline_minutes ?? 30,
            quorum: council?.quorum ?? 1,
            approvalThreshold: council?.approval_threshold ?? 1
          }
        })
      : [newStage('approval', 1)]
    bindingRisk = riskLevels[0] ?? 'high'
    errors = []
    savedDraftFingerprint = draftFingerprint()
  }

  function toggleRisk(risk: RiskLevel, checked: boolean) {
    riskLevels = checked ? [...riskLevels, risk] : riskLevels.filter((value) => value !== risk)
    if (!riskLevels.includes(bindingRisk)) bindingRisk = riskLevels[0] ?? 'high'
  }

  function addStage(type: StageType) {
    const next = newStage(type, stages.length + 1)
    const highRisk = riskLevels.some((risk) => risk === 'high' || risk === 'critical')
    if (type === 'council' && highRisk && stages.at(-1)?.type === 'approval') {
      stages = [...stages.slice(0, -1), next, stages.at(-1)!]
    } else {
      stages = [...stages, next]
    }
  }

  function removeStage(index: number) {
    stages = stages.filter((_, current) => current !== index)
  }

  function moveStage(index: number, offset: -1 | 1) {
    const target = index + offset
    if (target < 0 || target >= stages.length) return
    const next = [...stages]
    ;[next[index], next[target]] = [next[target], next[index]]
    stages = next
  }

  function addActor(stage: EditorStage) {
    stage.actors.push(newSelector())
  }

  function removeActor(stage: EditorStage, index: number) {
    if (stage.actors.length <= 1) return
    stage.actors.splice(index, 1)
  }

  function selectorNeedsId(kind: SelectorKind) {
    return kind === 'explicit_human' || kind === 'people_role' || kind === 'organization_position'
  }

  function validateDraft() {
    const next: string[] = []
    if (!name.trim()) next.push(text('请输入模板名称', 'Enter a template name'))
    if (riskLevels.length === 0) next.push(text('至少选择一个风险级别', 'Select at least one risk level'))
    if (stages.length === 0) next.push(text('至少添加一个阶段', 'Add at least one stage'))
    const keys = new Set<string>()
    stages.forEach((stage, index) => {
      if (!stage.key.trim() || !stage.name.trim()) next.push(text(`阶段 ${index + 1} 需要键和名称`, `Stage ${index + 1} needs a key and name`))
      if (keys.has(stage.key.trim())) next.push(text(`阶段标识 ${stage.key} 重复`, `Duplicate stage key ${stage.key}`))
      keys.add(stage.key.trim())
      if (stage.deadlineMinutes <= 0) next.push(text(`阶段 ${index + 1} 需要有效截止时间`, `Stage ${index + 1} needs a valid deadline`))
      if (stage.actors.length === 0 || stage.actors.some((actor) => selectorNeedsId(actor.kind) && !actor.id.trim())) {
        next.push(text(`阶段 ${index + 1} 需要有效参与人`, `Stage ${index + 1} needs valid actors`))
      }
      if (stage.type === 'approval') {
        if (stage.strategy === 'single' && stage.actors.length !== 1) next.push(text(`阶段 ${index + 1} 的单人审批只能有一位审批人`, `Stage ${index + 1} single approval needs one assignee`))
        if (stage.strategy === 'threshold' && (stage.threshold < 1 || stage.threshold > stage.actors.length)) next.push(text(`阶段 ${index + 1} 的通过人数无效`, `Stage ${index + 1} has an invalid threshold`))
        if (stage.escalationEnabled && (stage.escalationDeadlineMinutes <= 0 || (selectorNeedsId(stage.escalationActor.kind) && !stage.escalationActor.id.trim()))) {
          next.push(text(`阶段 ${index + 1} 的升级设置无效`, `Stage ${index + 1} has an invalid escalation`))
        }
      } else if (stage.quorum < 1 || stage.quorum > stage.actors.length || stage.approvalThreshold < 1 || stage.approvalThreshold > stage.quorum) {
        next.push(text(`阶段 ${index + 1} 的和议阈值无效`, `Stage ${index + 1} has invalid council thresholds`))
      }
    })
    if (riskLevels.some((risk) => risk === 'high' || risk === 'critical') && stages.at(-1)?.type !== 'approval') {
      next.push(text('高风险流程必须以人工审批结束', 'High-risk workflows must end with human approval'))
    }
    errors = next
    return next.length === 0
  }

  function buildRequest(): SaveHILWorkflowTemplateRequest {
    return {
      name: name.trim(),
      description: description.trim() || undefined,
      definition: {
        risk_levels: [...riskLevels],
        stages: stages.map((stage, index) => {
          const actors = stage.actors.map((actor) => ({ kind: actor.kind, id: actor.id.trim() || undefined }))
          if (stage.type === 'council') {
            return {
              key: stage.key.trim(), name: stage.name.trim(), order: index + 1, type: 'council' as const,
              council: { participants: actors, quorum: stage.quorum, approval_threshold: stage.approvalThreshold, deadline_minutes: stage.deadlineMinutes }
            }
          }
          return {
            key: stage.key.trim(), name: stage.name.trim(), order: index + 1, type: 'approval' as const,
            approval: {
              strategy: stage.strategy,
              threshold: stage.strategy === 'threshold' ? stage.threshold : undefined,
              assignees: actors,
              deadline_minutes: stage.deadlineMinutes,
              escalation: stage.escalationEnabled
                ? [{ actors: [{ kind: stage.escalationActor.kind, id: stage.escalationActor.id.trim() || undefined }], deadline_minutes: stage.escalationDeadlineMinutes }]
                : undefined
            }
          }
        })
      }
    }
  }

  function draftFingerprint() {
    return JSON.stringify(buildRequest())
  }

  function saveDraft() {
    if (!validateDraft()) return
    onsave?.(buildRequest())
  }

  function openPublish() {
    if (draftDirty || !validateDraft()) return
    bindingRisk = riskLevels.includes(bindingRisk) ? bindingRisk : (riskLevels[0] ?? 'high')
    dialog = 'publish'
  }

  function confirmPublish() {
    if (!bindingActionType.trim()) return
    onpublish?.({
      bindings: [{
        action_type_id: bindingActionType.trim(),
        tool_name: bindingToolName.trim() || undefined,
        risk_level: bindingRisk,
        active: true
      }]
    })
    dialog = ''
  }

  function confirmDisable() {
    if (!disableReason.trim()) return
    ondisable?.(disableReason.trim())
    disableReason = ''
    dialog = ''
  }

  const selectorKinds = [
    ['explicit_human', text('指定人员', 'Explicit human')],
    ['people_role', text('人员角色', 'People role')],
    ['organization_position', text('组织职位', 'Organization position')],
    ['requester_manager', text('发起人上级', 'Requester manager')],
    ['resource_owner', text('资源负责人', 'Resource owner')]
  ] as const
</script>

<section data-workflow-template-editor class="subtle-scrollbar h-full overflow-y-auto rounded-md border border-border bg-card p-4">
  <header class="flex flex-wrap items-start justify-between gap-3 border-b border-border/70 pb-4">
    <div>
      <div class="flex items-center gap-2">
        <h2 class="text-[15px] font-semibold text-foreground">{template ? template.name : text('新建审批流模板', 'New workflow template')}</h2>
        {#if template}<Badge tone={template.status === 'active' ? 'success' : 'neutral'}>{workflowStatusLabel(template.status, $locale)}</Badge>{/if}
      </div>
      {#if template}<p class="mt-1 text-[10.5px] text-muted-foreground">{text(`已发布${workflowRevisionLabel(template.latest_published_version, $locale)}`, `Published ${workflowRevisionLabel(template.latest_published_version, $locale)}`)}</p>{/if}
    </div>
    <div class="flex flex-wrap gap-2">
      {#if template}
        <Button variant="outline" size="sm" disabled={busy || template.status === 'disabled'} onclick={() => { disableReason = ''; dialog = 'disable' }}><Trash2 class="h-3.5 w-3.5" />{text('停用模板', 'Disable template')}</Button>
        <Button size="sm" disabled={busy || template.status === 'disabled' || draftDirty} title={draftDirty ? text('请先保存草稿', 'Save the draft first') : undefined} onclick={openPublish}><Send class="h-3.5 w-3.5" />{text('发布版本', 'Publish version')}</Button>
      {/if}
      <Button size="sm" disabled={busy || template?.status === 'disabled'} onclick={saveDraft}><Save class="h-3.5 w-3.5" />{text('保存草稿', 'Save draft')}</Button>
    </div>
  </header>

  {#if errors.length > 0}
    <div role="alert" class="mt-4 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-[11.5px] text-destructive">
      {#each errors as error}<p>{error}</p>{/each}
    </div>
  {/if}

  <div class="mt-4 grid gap-3 sm:grid-cols-2">
    <label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('模板名称', 'Template name')}</span><input bind:value={name} aria-label={text('模板名称', 'Template name')} disabled={busy} class="h-9 w-full rounded-md border border-input bg-background px-2.5 text-sm text-foreground" /></label>
    <label class="space-y-1 text-[11px] text-muted-foreground sm:col-span-2"><span>{text('说明', 'Description')}</span><textarea bind:value={description} aria-label={text('说明', 'Description')} disabled={busy} rows="2" class="w-full resize-y rounded-md border border-input bg-background px-2.5 py-2 text-sm text-foreground"></textarea></label>
  </div>

  <fieldset class="mt-4 border-t border-border/70 pt-4">
    <legend class="px-1 text-[11.5px] font-medium text-foreground">{text('适用风险级别', 'Risk levels')}</legend>
    <div class="mt-2 flex flex-wrap gap-4">
      {#each ['low', 'medium', 'high', 'critical'] as risk}
        <label class="flex items-center gap-1.5 text-[11.5px] text-foreground"><input type="checkbox" checked={riskLevels.includes(risk as RiskLevel)} onchange={(event) => toggleRisk(risk as RiskLevel, event.currentTarget.checked)} />{approvalRiskDisplayLabel(risk, $locale)}</label>
      {/each}
    </div>
  </fieldset>

  <div class="mt-5 flex flex-wrap items-center justify-between gap-3 border-t border-border/70 pt-4">
    <h3 class="text-[12.5px] font-medium text-foreground">{text('流程阶段', 'Workflow stages')}</h3>
    <div class="flex gap-2">
      <Button variant="outline" size="sm" disabled={busy} onclick={() => addStage('approval')}><UserRoundCheck class="h-3.5 w-3.5" />{text('添加审批阶段', 'Add approval stage')}</Button>
      <Button variant="outline" size="sm" disabled={busy} onclick={() => addStage('council')}><UsersRound class="h-3.5 w-3.5" />{text('添加和议阶段', 'Add council stage')}</Button>
    </div>
  </div>

  <div class="mt-3 divide-y divide-border/70 border-y border-border/70">
    {#each stages as stage, index (`${stage.type}-${index}`)}
      <section class="py-4">
        <div class="flex items-start justify-between gap-3">
          <div class="flex min-w-0 items-center gap-2"><span class="grid h-6 w-6 shrink-0 place-items-center rounded-full border border-border text-[10px] text-muted-foreground">{index + 1}</span><Badge tone={stage.type === 'approval' ? 'blue' : 'violet'}>{stageTypeLabel(stage.type)}</Badge></div>
          <div class="flex gap-1">
            <Button variant="ghost" size="icon" class="!h-7 !w-7" disabled={busy || index === 0} ariaLabel={text(`上移 ${stage.name}`, `Move ${stage.name} up`)} title={text('上移', 'Move up')} onclick={() => moveStage(index, -1)}><ArrowUp class="h-3.5 w-3.5" /></Button>
            <Button variant="ghost" size="icon" class="!h-7 !w-7" disabled={busy || index === stages.length - 1} ariaLabel={text(`下移 ${stage.name}`, `Move ${stage.name} down`)} title={text('下移', 'Move down')} onclick={() => moveStage(index, 1)}><ArrowDown class="h-3.5 w-3.5" /></Button>
            <Button variant="ghost" size="icon" class="!h-7 !w-7 !text-destructive" disabled={busy || stages.length === 1} ariaLabel={text(`删除 ${stage.name}`, `Delete ${stage.name}`)} title={text('删除', 'Delete')} onclick={() => removeStage(index)}><Trash2 class="h-3.5 w-3.5" /></Button>
          </div>
        </div>

        <div class="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('阶段标识', 'Stage key')}</span><input bind:value={stage.key} aria-label={text(`阶段 ${index + 1} 标识`, `Stage ${index + 1} key`)} class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground" /></label>
          <label class="space-y-1 text-[11px] text-muted-foreground sm:col-span-2"><span>{text('阶段名称', 'Stage name')}</span><input bind:value={stage.name} aria-label={text(`阶段 ${index + 1} 名称`, `Stage ${index + 1} name`)} class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground" /></label>
          <label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('截止时间（分钟）', 'Deadline (minutes)')}</span><input bind:value={stage.deadlineMinutes} aria-label={text(`阶段 ${index + 1} 截止时间`, `Stage ${index + 1} deadline`)} type="number" min="1" class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground" /></label>
        </div>

        {#if stage.type === 'approval'}
          <div class="mt-3 grid gap-3 sm:grid-cols-3">
            <label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('审批策略', 'Approval strategy')}</span><select bind:value={stage.strategy} aria-label={text(`审批阶段 ${index + 1} 策略`, `Approval stage ${index + 1} strategy`)} class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground">{#each ['single', 'any_one', 'all', 'threshold'] as strategy}<option value={strategy}>{strategyLabel(strategy as ApprovalStrategy)}</option>{/each}</select></label>
            {#if stage.strategy === 'threshold'}<label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('通过人数', 'Approval threshold')}</span><input bind:value={stage.threshold} type="number" min="1" max={stage.actors.length} class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground" /></label>{/if}
          </div>
        {:else}
          <div class="mt-3 grid gap-3 sm:grid-cols-3">
            <label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('法定人数', 'Quorum')}</span><input bind:value={stage.quorum} type="number" min="1" max={stage.actors.length} class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground" /></label>
            <label class="space-y-1 text-[11px] text-muted-foreground"><span>{text('通过人数', 'Approval threshold')}</span><input bind:value={stage.approvalThreshold} type="number" min="1" max={stage.quorum} class="h-8 w-full rounded-md border border-input bg-background px-2 text-xs text-foreground" /></label>
          </div>
        {/if}

        <div class="mt-3 space-y-2">
          <div class="flex items-center justify-between"><p class="text-[11px] font-medium text-foreground">{stage.type === 'approval' ? text('审批人', 'Assignees') : text('参与人', 'Participants')}</p><Button variant="ghost" size="sm" disabled={busy || (stage.type === 'approval' && stage.strategy === 'single')} onclick={() => addActor(stage)}><Plus class="h-3.5 w-3.5" />{text('添加人员', 'Add actor')}</Button></div>
          {#each stage.actors as actor, actorIndex}
            <div class="grid gap-2 sm:grid-cols-[minmax(150px,0.7fr)_minmax(0,1fr)_28px]">
              <select bind:value={actor.kind} aria-label={text(`${stage.type === 'council' ? '和议' : '审批'}阶段 ${index + 1} 参与人类型`, `${stage.type} stage ${index + 1} actor type`)} class="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground">
                {#each selectorKinds as option}<option value={option[0]}>{option[1]}</option>{/each}
                {#if stage.type === 'council'}<option value="ontology_recommended">{text('系统推荐', 'System recommended')}</option>{/if}
              </select>
              <input bind:value={actor.id} aria-label={text(`${stage.type === 'council' ? '和议' : '审批'}阶段 ${index + 1} 参与人`, `${stage.type} stage ${index + 1} actor ID`)} disabled={!selectorNeedsId(actor.kind)} placeholder={selectorNeedsId(actor.kind) ? text('请输入人员、角色或职位', 'Human, role, or position ID') : text('自动解析', 'Resolved automatically')} class="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground disabled:opacity-60" />
              <Button variant="ghost" size="icon" class="!h-7 !w-7" disabled={busy || stage.actors.length === 1} ariaLabel={text(`删除参与人 ${actorIndex + 1}`, `Delete actor ${actorIndex + 1}`)} onclick={() => removeActor(stage, actorIndex)}><Trash2 class="h-3.5 w-3.5" /></Button>
            </div>
          {/each}
        </div>

        {#if stage.type === 'approval'}
          <div class="mt-3 border-t border-border/60 pt-3">
            <label class="flex items-center gap-2 text-[11px] text-foreground"><input type="checkbox" bind:checked={stage.escalationEnabled} />{text('启用一级超时升级', 'Enable one escalation rung')}</label>
            {#if stage.escalationEnabled}
              <div class="mt-2 grid gap-2 sm:grid-cols-3">
                <select bind:value={stage.escalationActor.kind} aria-label={text(`审批阶段 ${index + 1} 升级类型`, `Approval stage ${index + 1} escalation type`)} class="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground">{#each selectorKinds as option}<option value={option[0]}>{option[1]}</option>{/each}</select>
                <input bind:value={stage.escalationActor.id} aria-label={text(`审批阶段 ${index + 1} 升级人员`, `Approval stage ${index + 1} escalation actor ID`)} disabled={!selectorNeedsId(stage.escalationActor.kind)} placeholder={text('请输入升级处理人', 'Enter escalation actor ID')} class="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground disabled:opacity-60" />
                <input bind:value={stage.escalationDeadlineMinutes} aria-label={text(`审批阶段 ${index + 1} 升级截止时间`, `Approval stage ${index + 1} escalation deadline`)} type="number" min="1" class="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground" />
              </div>
            {/if}
          </div>
        {/if}
      </section>
    {/each}
  </div>
</section>

<Dialog open={dialog === 'publish'} title={text('发布不可变版本', 'Publish immutable version')} onClose={() => { dialog = '' }}>
  <div class="space-y-3">
    <label class="block space-y-1 text-xs text-muted-foreground"><span>{text('动作类型', 'Action type')}</span><input bind:value={bindingActionType} aria-label={text('动作类型', 'Action type')} class="h-9 w-full rounded-md border border-input bg-background px-2 text-sm text-foreground" /></label>
    <label class="block space-y-1 text-xs text-muted-foreground"><span>{text('工具名称', 'Tool name')}</span><input bind:value={bindingToolName} aria-label={text('工具名称', 'Tool name')} class="h-9 w-full rounded-md border border-input bg-background px-2 text-sm text-foreground" /></label>
    <label class="block space-y-1 text-xs text-muted-foreground"><span>{text('风险级别', 'Risk level')}</span><select bind:value={bindingRisk} aria-label={text('风险级别', 'Risk level')} class="h-9 w-full rounded-md border border-input bg-background px-2 text-sm text-foreground">{#each riskLevels as risk}<option value={risk}>{approvalRiskDisplayLabel(risk, $locale)}</option>{/each}</select></label>
    <div class="flex justify-end gap-2"><Button variant="outline" onclick={() => { dialog = '' }}>{text('取消', 'Cancel')}</Button><Button disabled={busy || !bindingActionType.trim()} onclick={confirmPublish}>{text('确认发布', 'Confirm publish')}</Button></div>
  </div>
</Dialog>

<Dialog open={dialog === 'disable'} title={text('停用审批流模板', 'Disable workflow template')} onClose={() => { dialog = '' }}>
  <div class="space-y-3">
    <label class="block space-y-1 text-xs text-muted-foreground"><span>{text('停用原因', 'Disable reason')}</span><textarea bind:value={disableReason} aria-label={text('停用原因', 'Disable reason')} rows="3" class="w-full resize-y rounded-md border border-input bg-background px-2.5 py-2 text-sm text-foreground"></textarea></label>
    <div class="flex justify-end gap-2"><Button variant="outline" onclick={() => { dialog = '' }}>{text('取消', 'Cancel')}</Button><Button variant="destructive" disabled={busy || !disableReason.trim()} onclick={confirmDisable}>{text('确认停用', 'Confirm disable')}</Button></div>
  </div>
</Dialog>
