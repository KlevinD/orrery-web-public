<script lang="ts">
  import { ExternalLink, FileText, ShieldAlert } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { renderSafeMarkdown } from '#lib/markdown'
  import type { ApprovalArtifactViewModel, ApprovalPayloadViewModel, BusinessApprovalMaterialViewModel } from '#lib/types/view-models'

  interface Props {
    material: ApprovalPayloadViewModel
    summary?: string
    businessMaterial?: BusinessApprovalMaterialViewModel
    evidenceRefs?: string[]
    artifacts?: ApprovalArtifactViewModel[]
    readableOnly?: boolean
    class?: string
  }

  let {
    material,
    summary = '',
    businessMaterial,
    evidenceRefs = [],
    artifacts = [],
    readableOnly = false,
    class: className = ''
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  const renderedContent = $derived(readableOnly && material.content ? renderSafeMarkdown(material.content) : '')
  const displaySummary = $derived(genericRuntimeSummary(summary) ? '' : summary.trim())
  const readableFields = $derived(readableMaterialFields(material.fields, material.content, displaySummary))
  const showSummary = $derived(Boolean(displaySummary && displaySummary !== material.content.trim()))
  const hasReadableMaterial = $derived(Boolean(
    businessMaterial || displaySummary || material.content.trim() || readableFields.length || artifacts.length
  ))
  const visible = $derived(Boolean(
    readableOnly
      || businessMaterial
      || displaySummary
      || material.content
      || artifacts.length
      || material.fields.length
      || evidenceRefs.length
  ))

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function genericRuntimeSummary(value: string) {
    const normalized = value.trim()
    return normalized === ''
      || normalized === 'Not provided'
      || /^runtime approval required for\b/i.test(normalized)
      || /^(?:runtime\.approval|hil decision|approval decision|human review required)$/i.test(normalized)
  }

  function readableMaterialFields(fields: ApprovalPayloadViewModel['fields'], content: string, currentSummary: string) {
    const labels: Record<string, [string, string]> = {
      request_context: ['原始请求', 'Original request'],
      approval_summary: ['审批摘要', 'Approval summary'],
      approval_description: ['审批说明', 'Approval details'],
      business_direction: ['业务目标', 'Business goal'],
      final_goal: ['最终目标', 'Final goal'],
      root_goal: ['任务目标', 'Task goal'],
      goal: ['目标', 'Goal'],
      task_goal: ['任务目标', 'Task goal'],
      plan_summary: ['执行计划', 'Plan summary'],
      query: ['查询内容', 'Query'],
      prompt: ['请求内容', 'Request'],
      question: ['需要确认', 'Question'],
      description: ['说明', 'Description'],
      message: ['补充信息', 'Message'],
      reason: ['原因', 'Reason'],
      command: ['待执行命令', 'Command'],
      path: ['目标路径', 'Target path'],
      url: ['目标地址', 'Target URL']
    }
    const seen = new Set([content.trim(), currentSummary.trim()].filter(Boolean))
    return fields.flatMap((field) => {
      const label = labels[field.key]
      const value = field.value.trim()
      if (!label || !value || seen.has(value) || value === 'Not provided') return []
      seen.add(value)
      return [{ key: field.key, label: text(label[0], label[1]), value }]
    })
  }
</script>

{#if visible}
  <section data-approval-material class="space-y-4 {className}">
    {#if businessMaterial}
      <div>
        <div class="flex items-center gap-2 text-[12.5px] font-medium text-foreground">
          <FileText class="h-4 w-4 text-muted-foreground" />
          <h3>{text('审批内容', 'Approval material')}</h3>
        </div>
        <dl class="mt-3 divide-y divide-border/70 border-y border-border/70">
          {#each [
            [text('需要您决定', 'Decision requested'), businessMaterial.decisionRequest],
            [text('业务背景', 'Business context'), businessMaterial.businessContext],
            [text('拟执行动作', 'Proposed action'), businessMaterial.proposedAction],
            [text('预期结果', 'Expected result'), businessMaterial.expectedResult],
            [text('影响范围', 'Impact scope'), businessMaterial.impactScope]
          ] as field}
            <div class="grid gap-1.5 py-3 sm:grid-cols-[112px_minmax(0,1fr)] sm:gap-4">
              <dt class="text-[11px] font-medium text-muted-foreground">{field[0]}</dt>
              <dd class="break-words text-[12.5px] leading-5 text-foreground">{field[1]}</dd>
            </div>
          {/each}
        </dl>
      </div>

      <div>
        <div class="flex items-center gap-2 text-[12px] font-medium text-foreground">
          <ShieldAlert class="h-4 w-4 text-amber-600" />
          <h3>{text('风险与应对', 'Risks and mitigations')}</h3>
        </div>
        <ol class="mt-2 divide-y divide-border/70 border-y border-border/70">
          {#each businessMaterial.risks as risk, index}
            <li class="grid gap-2 py-3 text-[12px] sm:grid-cols-[112px_minmax(0,1fr)] sm:gap-4">
              <span class="text-muted-foreground">{text(`风险 ${index + 1}`, `Risk ${index + 1}`)}</span>
              <div class="space-y-1.5">
                <p class="break-words text-foreground">{risk.description}</p>
                <p class="break-words text-muted-foreground"><span class="font-medium text-foreground">{text('应对措施：', 'Mitigation: ')}</span>{risk.mitigation}</p>
              </div>
            </li>
          {/each}
        </ol>
      </div>
    {:else}
      <div class="flex items-center justify-between gap-2 text-[12.5px] font-medium text-foreground">
        <span class="flex items-center gap-2"><FileText class="h-4 w-4 text-muted-foreground" />{readableOnly ? text('审批内容', 'Approval material') : (material.title || t('approvals.payload'))}</span>
        {#if !readableOnly && material.language}
          <span class="rounded border border-border px-2 py-0.5 font-mono text-[10px] text-muted-foreground">{material.language}</span>
        {/if}
      </div>
      {#if showSummary}
        <p data-approval-summary class="border-l-2 border-brand/50 pl-4 text-[13.5px] leading-6 text-foreground/85">{displaySummary}</p>
      {/if}
      {#if material.content}
        {#if readableOnly}
          <div data-approval-readable-content class="approval-readable-content discussion-message-content break-words text-[14px] leading-7 text-foreground [&_img]:h-auto [&_img]:max-w-full">{@html renderedContent}</div>
        {:else}
          <pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-background p-3 font-mono text-xs leading-5 text-foreground">{material.content}</pre>
        {/if}
      {/if}
      {#if readableOnly && readableFields.length}
        <dl data-approval-readable-fields class="divide-y divide-border/70 border-y border-border/70">
          {#each readableFields as field (field.key)}
            <div class="grid gap-1.5 py-3 sm:grid-cols-[112px_minmax(0,1fr)] sm:gap-4">
              <dt class="text-[11px] font-medium text-muted-foreground">{field.label}</dt>
              <dd class="break-words text-[13px] leading-6 text-foreground/85">{field.value}</dd>
            </div>
          {/each}
        </dl>
      {/if}
      {#if !readableOnly && material.fields.length}
        <dl class="divide-y divide-border/70 border-y border-border/70 text-xs">
          {#each material.fields as field}
            <div class="grid gap-1 py-2 sm:grid-cols-[minmax(110px,0.35fr)_minmax(0,1fr)] sm:gap-3">
              <dt class="break-words font-mono text-[10px] text-muted-foreground">{field.key}</dt>
              <dd class="break-words text-foreground">{field.value}</dd>
            </div>
          {/each}
        </dl>
      {/if}
      {#if readableOnly && !hasReadableMaterial}
        <div data-approval-content-unavailable role="alert" class="flex items-start gap-2.5 border-y border-amber-200 bg-amber-50/70 px-3 py-3 text-amber-950">
          <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
          <div>
            <p class="text-[12.5px] font-medium">{text('未提供可读的审批内容', 'Readable approval content is unavailable')}</p>
            <p class="mt-1 text-[11.5px] leading-5 text-amber-900/75">{text('为避免盲目审批，该请求暂时不能处理。', 'This request cannot be reviewed safely until its content is available.')}</p>
          </div>
        </div>
      {/if}
    {/if}

    {#if artifacts.length || (!readableOnly && evidenceRefs.length)}
      <div class="border-t border-border/70 pt-4">
        <h3 class="text-[12px] font-medium text-foreground">{text('附件', 'Attachments')}</h3>
        <div class="mt-2 space-y-1.5 text-[11.5px]">
          {#each artifacts as artifact}
            <a class="flex min-w-0 items-center gap-2 text-primary hover:underline" href={artifact.url} target="_blank" rel="noreferrer">
              <FileText class="h-3.5 w-3.5 shrink-0" /><span class="truncate">{artifact.label}</span><ExternalLink class="h-3 w-3 shrink-0" />
            </a>
          {/each}
          {#if !readableOnly}
            {#each evidenceRefs as reference}
              <p class="break-all font-mono text-[10.5px] text-muted-foreground">{reference}</p>
            {/each}
          {/if}
        </div>
      </div>
    {/if}
  </section>
{/if}
