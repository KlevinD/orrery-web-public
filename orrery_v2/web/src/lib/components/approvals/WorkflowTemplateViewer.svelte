<script lang="ts">
  import { Clock3, ShieldCheck, UserRoundCheck, UsersRound } from 'lucide-svelte'
  import { approvalActorDisplayName, approvalRiskDisplayLabel, workflowRevisionLabel } from './approvalDisplayLabels'
  import Badge from '#lib/components/ui/Badge.svelte'
  import { locale } from '#lib/i18n'
  import type { HILWorkflowTemplateDTO } from '#lib/types/api'

  interface Props {
    template: HILWorkflowTemplateDTO
  }

  type Stage = HILWorkflowTemplateDTO['draft_definition']['stages'][number]
  type ApprovalPolicy = NonNullable<Stage['approval']>
  type CouncilPolicy = NonNullable<Stage['council']>
  type ActorSelector = ApprovalPolicy['assignees'][number]

  let { template }: Props = $props()

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function riskLabel(risk: string) {
    return approvalRiskDisplayLabel(risk, $locale)
  }

  function actorLabel(actor: ActorSelector) {
    const labels: Record<ActorSelector['kind'], [string, string]> = {
      explicit_human: ['指定审批人', 'Named approver'],
      people_role: ['指定角色', 'People role'],
      organization_position: ['指定组织职位', 'Organization position'],
      requester_manager: ['发起人的直属上级', "Requester's manager"],
      resource_owner: ['相关资源负责人', 'Resource owner'],
      ontology_recommended: ['系统推荐审批人', 'System-recommended approver']
    }
    const label = labels[actor.kind]
    return actor.id ? `${text(label[0], label[1])} · ${approvalActorDisplayName(actor.id, $locale)}` : text(label[0], label[1])
  }

  function actorList(actors: ActorSelector[]) {
    return actors.map(actorLabel).join(text('、', ', '))
  }

  function deadlineLabel(minutes: number) {
    if (minutes < 60) return text(`${minutes} 分钟内`, `within ${minutes} minutes`)
    if (minutes % 60 === 0) return text(`${minutes / 60} 小时内`, `within ${minutes / 60} hours`)
    return text(`${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分钟内`, `within ${Math.floor(minutes / 60)}h ${minutes % 60}m`)
  }

  function approvalRule(policy: ApprovalPolicy) {
    if (policy.strategy === 'all') return text('全部审批人同意后通过', 'All approvers must agree')
    if (policy.strategy === 'threshold') return text(`至少 ${policy.threshold ?? 1} 人同意后通过`, `At least ${policy.threshold ?? 1} approvals required`)
    if (policy.strategy === 'any_one') return text('任意一人同意后通过', 'Any one approver may approve')
    return text('由一名审批人确认', 'One approver must confirm')
  }

  function stagePolicy(stage: Stage) {
    if (stage.type === 'council' && stage.council) {
      return text(
        `至少 ${stage.council.quorum} 人参与，其中 ${stage.council.approval_threshold} 人同意后通过`,
        `${stage.council.quorum} participants required, with ${stage.council.approval_threshold} approvals`
      )
    }
    return stage.approval ? approvalRule(stage.approval) : ''
  }

  function stageActors(stage: Stage) {
    if (stage.type === 'council' && stage.council) return actorList(stage.council.participants)
    return stage.approval ? actorList(stage.approval.assignees) : ''
  }

  function stageDeadline(stage: Stage) {
    const minutes = stage.type === 'council' ? stage.council?.deadline_minutes : stage.approval?.deadline_minutes
    return deadlineLabel(minutes ?? 0)
  }

  function council(stage: Stage): CouncilPolicy | undefined {
    return stage.council
  }
</script>

<section data-workflow-template-viewer class="subtle-scrollbar h-full overflow-y-auto rounded-md border border-border bg-card p-5">
  <header class="border-b border-border/70 pb-4">
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="text-base font-semibold text-foreground">{template.name}</h2>
      <Badge tone={template.status === 'active' ? 'success' : 'neutral'}>
        {template.status === 'active' ? text('使用中', 'Active') : text('已停用', 'Disabled')}
      </Badge>
    </div>
    <p class="mt-1.5 max-w-3xl text-[13px] leading-6 text-muted-foreground">
      {template.description || text('此流程没有补充说明。', 'No additional description is available.')}
    </p>
    <div class="mt-3 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
      <span>{text(`已发布${workflowRevisionLabel(template.latest_published_version, $locale)}`, `Published ${workflowRevisionLabel(template.latest_published_version, $locale)}`)}</span>
      <span aria-hidden="true">·</span>
      <span>{text(`${template.draft_definition.stages.length} 个审批阶段`, `${template.draft_definition.stages.length} workflow stages`)}</span>
    </div>
  </header>

  <div class="border-b border-border/70 py-4">
    <div class="flex items-center gap-2 text-[12px] font-medium text-foreground">
      <ShieldCheck class="h-4 w-4 text-brand" />{text('适用风险', 'Applicable risk')}
    </div>
    <div class="mt-2 flex flex-wrap gap-2">
      {#each template.draft_definition.risk_levels as risk}
        <Badge tone={risk === 'critical' ? 'danger' : risk === 'high' ? 'warning' : 'neutral'}>{riskLabel(risk)}</Badge>
      {/each}
    </div>
  </div>

  <div class="py-4">
    <h3 class="text-[12px] font-medium text-foreground">{text('审批顺序', 'Approval sequence')}</h3>
    <ol class="mt-4">
      {#each template.draft_definition.stages as stage, index (stage.key)}
        <li class="relative grid grid-cols-[36px_minmax(0,1fr)] gap-3 pb-6 last:pb-0">
          {#if index < template.draft_definition.stages.length - 1}
            <span data-workflow-template-connector class="absolute bottom-0 left-[17px] top-9 w-px bg-border" aria-hidden="true"></span>
          {/if}
          <span class="relative z-10 grid h-9 w-9 place-items-center rounded-full border border-border bg-background text-brand">
            {#if stage.type === 'council'}<UsersRound class="h-4 w-4" />{:else}<UserRoundCheck class="h-4 w-4" />{/if}
          </span>
          <div class="min-w-0 pt-0.5">
            <div class="flex flex-wrap items-center gap-2">
              <h4 class="text-[13.5px] font-medium text-foreground">{stage.name}</h4>
              <Badge tone={stage.type === 'council' ? 'violet' : 'blue'}>
                {stage.type === 'council' ? text('会签', 'Council') : text('审批', 'Approval')}
              </Badge>
            </div>
            <p class="mt-1.5 text-[12.5px] leading-5 text-foreground/85">{stageActors(stage)}</p>
            <p class="mt-1 text-[11.5px] leading-5 text-muted-foreground">{stagePolicy(stage)}</p>
            <p class="mt-1 flex items-center gap-1.5 text-[11px] text-muted-foreground">
              <Clock3 class="h-3.5 w-3.5" />{stageDeadline(stage)}{text('完成', ' to complete')}
            </p>
            {#if stage.approval?.escalation?.length}
              <p class="mt-1 text-[11px] text-muted-foreground">
                {text('逾期后转交：', 'Escalates to: ')}{actorList(stage.approval.escalation[0].actors)}
              </p>
            {:else if council(stage)?.participants.length === 0}
              <p class="mt-1 text-[11px] text-muted-foreground">{text('参与人将在流程启动时解析。', 'Participants are resolved when the workflow starts.')}</p>
            {/if}
          </div>
        </li>
      {/each}
    </ol>
  </div>
</section>
