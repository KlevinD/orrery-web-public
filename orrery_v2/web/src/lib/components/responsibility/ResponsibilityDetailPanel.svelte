<script lang="ts">
  import {
    Ban,
    BellRing,
    ExternalLink,
    FileClock,
    Pause,
    Play,
    RotateCcw,
    Save,
    ShieldAlert,
    UserRoundCog,
    X
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { responsibilityHealthTone, responsibilitySourceLinks } from '#lib/responsibility/view-model'
  import type {
    ResponsibilityControlAction,
    ResponsibilityControlRequest,
    ResponsibilityDetailDTO,
    ResponsibilityPolicyDTO,
    ResponsibilityPolicyUpdateRequest
  } from '#lib/types/responsibility'

  interface Props {
    detail: ResponsibilityDetailDTO
    policy?: ResponsibilityPolicyDTO
    canManage?: boolean
    pending?: boolean
    onControl: (action: ResponsibilityControlAction, body: ResponsibilityControlRequest) => Promise<void>
    onResolveBlocker: (blockerId: string, version: number, resolution: string) => Promise<void>
    onSavePolicy: (body: ResponsibilityPolicyUpdateRequest) => Promise<void>
  }

  let {
    detail,
    policy = undefined,
    canManage = false,
    pending = false,
    onControl,
    onResolveBlocker,
    onSavePolicy
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  let detailTab = $state<'attempts' | 'activations' | 'blockers' | 'alerts' | 'audit'>('attempts')
  let actionReason = $state('')
  let reassignAgent = $state('')
  let acceptBy = $state('')
  let actionError = $state('')
  let actionMessage = $state('')
  let blockerResolutions = $state<Record<string, string>>({})
  let policyEditing = $state(false)
  let policyError = $state('')
  let policyMessage = $state('')
  let initializedPolicyKey = $state('')
  let policyForm = $state({
    autonomy: 'execute_scoped', approval: 'only_high_risk', maxParallel: '2', acceptTimeout: '120',
    heartbeatTimeout: '90', maxAttempts: '3', retryBackoff: '30,120,600', maxCost: '0', tokenBudget: '0',
    recheckInterval: '900', deadlineWarning: '1800', risks: '', tools: '', data: '', escalation: '', enabled: true
  })

  const sourceLinks = $derived(responsibilitySourceLinks(detail))
  const tabs = $derived([
    { value: 'attempts', label: `${t('responsibility.detail.attempts')} ${detail.attempts.length}` },
    { value: 'activations', label: `${t('responsibility.detail.activations')} ${detail.activations.length}` },
    { value: 'blockers', label: `${t('responsibility.detail.blockers')} ${detail.blockers.length}` },
    { value: 'alerts', label: `${t('responsibility.detail.alerts')} ${detail.alerts.length}` },
    { value: 'audit', label: `${t('responsibility.detail.audit')} ${detail.audit.length}` }
  ])

  $effect(() => {
    const current = policy ?? detail.policy
    const key = `${detail.responsibility_id}:${current.version}`
    if (key === initializedPolicyKey) return
    initializedPolicyKey = key
    policyForm = {
      autonomy: current.autonomy_level,
      approval: current.approval_policy,
      maxParallel: String(current.max_parallel),
      acceptTimeout: String(current.accept_timeout_seconds),
      heartbeatTimeout: String(current.heartbeat_timeout_seconds),
      maxAttempts: String(current.max_attempts),
      retryBackoff: current.retry_backoff_seconds.join(', '),
      maxCost: String(current.max_cost_per_task),
      tokenBudget: String(current.max_token_budget),
      recheckInterval: String(current.recheck_interval_seconds),
      deadlineWarning: String(current.deadline_warning_seconds),
      risks: current.allowed_risk_levels.join(', '),
      tools: current.tool_scopes.join(', '),
      data: current.data_scopes.join(', '),
      escalation: current.escalation_chain.map((step) => `${step.subject_kind}:${step.subject_id}:${step.after_seconds}`).join('\n'),
      enabled: current.enabled
    }
  })

  async function runControl(action: ResponsibilityControlAction) {
    actionError = ''
    actionMessage = ''
    const reason = actionReason.trim()
    if ((action === 'terminate' || action === 'reassign') && !reason) {
      actionError = t('responsibility.control.reasonRequired')
      return
    }
    if (action === 'reassign' && !reassignAgent.trim()) {
      actionError = t('responsibility.control.agentRequired')
      return
    }
    const body: ResponsibilityControlRequest = { reason }
    if (action === 'reassign') {
      body.agent_profile_id = reassignAgent.trim()
      if (acceptBy) body.accept_by = new Date(acceptBy).toISOString()
    }
    try {
      await onControl(action, body)
      actionMessage = t('responsibility.control.success')
      actionReason = ''
      if (action === 'reassign') {
        reassignAgent = ''
        acceptBy = ''
      }
    } catch (error) {
      actionError = error instanceof Error ? error.message : t('responsibility.control.failed')
    }
  }

  async function resolveBlocker(blockerId: string, version: number) {
    const resolution = (blockerResolutions[blockerId] ?? '').trim()
    if (!resolution) return
    actionError = ''
    try {
      await onResolveBlocker(blockerId, version, resolution)
      blockerResolutions = { ...blockerResolutions, [blockerId]: '' }
      actionMessage = t('responsibility.control.success')
    } catch (error) {
      actionError = error instanceof Error ? error.message : t('responsibility.control.failed')
    }
  }

  async function savePolicy() {
    policyError = ''
    policyMessage = ''
    const body = policyPayload()
    if (!body) {
      policyError = t('responsibility.policy.invalid')
      return
    }
    try {
      await onSavePolicy(body)
      policyEditing = false
      policyMessage = t('responsibility.policy.saved')
    } catch (error) {
      policyError = error instanceof Error ? error.message : t('responsibility.policy.failed')
    }
  }

  function policyPayload(): ResponsibilityPolicyUpdateRequest | undefined {
    const maxParallel = positiveInteger(policyForm.maxParallel)
    const acceptTimeout = positiveInteger(policyForm.acceptTimeout)
    const heartbeatTimeout = positiveInteger(policyForm.heartbeatTimeout)
    const maxAttempts = positiveInteger(policyForm.maxAttempts)
    const recheckInterval = positiveInteger(policyForm.recheckInterval)
    const deadlineWarning = nonnegativeInteger(policyForm.deadlineWarning)
    const tokenBudget = nonnegativeInteger(policyForm.tokenBudget)
    const maxCost = Number(policyForm.maxCost)
    const retryBackoff = parseNumbers(policyForm.retryBackoff)
    const escalation = parseEscalation(policyForm.escalation)
    if ([maxParallel, acceptTimeout, heartbeatTimeout, maxAttempts, recheckInterval, deadlineWarning, tokenBudget].some((value) => value === undefined) ||
      !Number.isFinite(maxCost) || maxCost < 0 || retryBackoff.length === 0 || !escalation) return undefined
    return {
      autonomy_level: policyForm.autonomy as ResponsibilityPolicyUpdateRequest['autonomy_level'],
      approval_policy: policyForm.approval as ResponsibilityPolicyUpdateRequest['approval_policy'],
      max_parallel: maxParallel!, accept_timeout_seconds: acceptTimeout!, heartbeat_timeout_seconds: heartbeatTimeout!,
      max_attempts: maxAttempts!, retry_backoff_seconds: retryBackoff, max_cost_per_task: maxCost,
      max_token_budget: tokenBudget!, recheck_interval_seconds: recheckInterval!, deadline_warning_seconds: deadlineWarning!,
      allowed_risk_levels: parseList(policyForm.risks), tool_scopes: parseList(policyForm.tools), data_scopes: parseList(policyForm.data),
      escalation_chain: escalation, enabled: policyForm.enabled
    }
  }

  function sourceLabel(kind: 'todo' | 'scheduler' | 'mission') {
    return t(`responsibility.source.${kind}` as TranslationKey)
  }

  function formatDate(value?: string) {
    if (!value) return '—'
    const date = new Date(value)
    if (Number.isNaN(date.valueOf())) return value
    return new Intl.DateTimeFormat($locale, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
  }

  function toneForStatus(status?: string) {
    if (!status) return 'info' as const
    if (['completed', 'fulfilled', 'resolved'].includes(status)) return 'success' as const
    if (['failed', 'timed_out', 'cancelled', 'overdue'].includes(status)) return 'danger' as const
    if (['blocked', 'waiting', 'claimed'].includes(status)) return 'warning' as const
    return 'info' as const
  }

  function parseList(value: string) {
    return Array.from(new Set(value.split(',').map((item) => item.trim()).filter(Boolean)))
  }

  function parseNumbers(value: string) {
    const values = parseList(value).map(Number)
    return values.length > 0 && values.every((number) => Number.isInteger(number) && number >= 0) ? values : []
  }

  function parseEscalation(value: string): ResponsibilityPolicyUpdateRequest['escalation_chain'] | undefined {
    if (!value.trim()) return []
    const out: ResponsibilityPolicyUpdateRequest['escalation_chain'] = []
    for (const line of value.split('\n').map((item) => item.trim()).filter(Boolean)) {
      const [subjectKind, subjectId, secondsRaw, ...rest] = line.split(':').map((item) => item.trim())
      const seconds = Number(secondsRaw)
      if (!subjectKind || !subjectId || rest.length > 0 || !Number.isInteger(seconds) || seconds <= 0) return undefined
      out.push({ subject_kind: subjectKind, subject_id: subjectId, after_seconds: seconds })
    }
    return out
  }

  function positiveInteger(value: string) {
    const number = Number(value)
    return Number.isInteger(number) && number > 0 ? number : undefined
  }

  function nonnegativeInteger(value: string) {
    const number = Number(value)
    return Number.isInteger(number) && number >= 0 ? number : undefined
  }
</script>

<section class="min-w-0 bg-card" data-responsibility-detail>
  <header class="border-b border-border px-5 py-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold text-foreground">{detail.title || detail.goal}</h2>
          <Badge tone={responsibilityHealthTone(detail.health)}>{t(`responsibility.health.${detail.health}` as TranslationKey)}</Badge>
          <Badge tone="neutral">{detail.state}</Badge>
        </div>
        <p class="mt-2 text-sm leading-6 text-muted-foreground">{detail.goal}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#each sourceLinks as link}
          <Button href={link.href} variant="outline" size="sm">
            <ExternalLink class="h-3.5 w-3.5" />{sourceLabel(link.kind)}
          </Button>
        {/each}
      </div>
    </div>
    <div class="mt-4 grid gap-3 text-xs sm:grid-cols-2 xl:grid-cols-4">
      <div><span class="text-muted-foreground">Agent</span><div class="mt-1 truncate font-medium text-foreground">{detail.accountable_agent}</div></div>
      <div><span class="text-muted-foreground">{t('responsibility.nextAction')}</span><div class="mt-1 text-foreground">{detail.next_action}</div></div>
      <div><span class="text-muted-foreground">{t('responsibility.nextActor')}</span><div class="mt-1 truncate text-foreground">{detail.next_actor}</div></div>
      <div><span class="text-muted-foreground">ID</span><div class="mt-1 truncate font-mono text-foreground">{detail.responsibility_id}</div></div>
    </div>
  </header>

  {#if canManage}
    <section class="border-b border-border bg-muted/15 px-5 py-4" aria-label={t('responsibility.controls')}>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h3 class="text-sm font-semibold text-foreground">{t('responsibility.controls')}</h3>
        <div class="flex flex-wrap items-center gap-2">
          {#if detail.paused}
            <Button variant="outline" size="sm" disabled={pending} onclick={() => runControl('resume')}><Play class="h-3.5 w-3.5" />{t('responsibility.control.resume')}</Button>
          {:else}
            <Button variant="outline" size="sm" disabled={pending} onclick={() => runControl('pause')}><Pause class="h-3.5 w-3.5" />{t('responsibility.control.pause')}</Button>
          {/if}
          <Button variant="outline" size="sm" disabled={pending || detail.paused} onclick={() => runControl('retry')}><RotateCcw class="h-3.5 w-3.5" />{t('responsibility.control.retry')}</Button>
          <Button variant="destructive" size="sm" disabled={pending || detail.state === 'terminated'} onclick={() => runControl('terminate')}><Ban class="h-3.5 w-3.5" />{t('responsibility.control.terminate')}</Button>
        </div>
      </div>
      <div class="mt-3 grid gap-2 lg:grid-cols-[minmax(0,1fr)_minmax(180px,.6fr)_190px_auto]">
        <Input bind:value={actionReason} placeholder={t('responsibility.control.reason')} ariaLabel={t('responsibility.control.reason')} />
        <Input bind:value={reassignAgent} placeholder={t('responsibility.control.agent')} ariaLabel={t('responsibility.control.agent')} />
        <Input bind:value={acceptBy} type="datetime-local" ariaLabel={t('responsibility.control.acceptBy')} />
        <Button variant="secondary" disabled={pending} onclick={() => runControl('reassign')}><UserRoundCog class="h-4 w-4" />{t('responsibility.control.reassign')}</Button>
      </div>
      {#if actionMessage}<p class="mt-2 text-xs text-status-done">{actionMessage}</p>{/if}
      {#if actionError}<p class="mt-2 text-xs text-destructive">{actionError}</p>{/if}
    </section>
  {/if}

  <section class="border-b border-border px-5 py-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h3 class="text-sm font-semibold text-foreground">{t('responsibility.policy')}</h3>
        <p class="mt-1 text-xs text-muted-foreground">{(policy ?? detail.policy).autonomy_level} · {(policy ?? detail.policy).approval_policy} · v{(policy ?? detail.policy).version}</p>
      </div>
      {#if canManage}
        <Button variant="outline" size="sm" onclick={() => { policyEditing = !policyEditing; policyError = ''; policyMessage = '' }}>
          {#if policyEditing}<X class="h-3.5 w-3.5" />{t('responsibility.policy.cancel')}{:else}<ShieldAlert class="h-3.5 w-3.5" />{t('responsibility.policy.edit')}{/if}
        </Button>
      {/if}
    </div>
    {#if policyMessage}<p class="mt-2 text-xs text-status-done">{policyMessage}</p>{/if}
    {#if policyEditing}
      <div class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4" data-policy-editor>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.autonomy')}<Select bind:value={policyForm.autonomy} class="mt-1" options={[
          { value: 'advise', label: 'advise' }, { value: 'execute_reversible', label: 'execute_reversible' },
          { value: 'execute_scoped', label: 'execute_scoped' }, { value: 'continuous', label: 'continuous' }
        ]} /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.approval')}<Select bind:value={policyForm.approval} class="mt-1" options={[
          { value: 'before_side_effect', label: 'before_side_effect' }, { value: 'only_high_risk', label: 'only_high_risk' }, { value: 'none', label: 'none' }
        ]} /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.parallel')}<Input bind:value={policyForm.maxParallel} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.attempts')}<Input bind:value={policyForm.maxAttempts} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.acceptTimeout')}<Input bind:value={policyForm.acceptTimeout} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.heartbeatTimeout')}<Input bind:value={policyForm.heartbeatTimeout} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.recheck')}<Input bind:value={policyForm.recheckInterval} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.deadline')}<Input bind:value={policyForm.deadlineWarning} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground sm:col-span-2">{t('responsibility.policy.retry')}<Input bind:value={policyForm.retryBackoff} class="mt-1" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.cost')}<Input bind:value={policyForm.maxCost} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground">{t('responsibility.policy.tokens')}<Input bind:value={policyForm.tokenBudget} class="mt-1" type="number" /></label>
        <label class="text-xs text-muted-foreground sm:col-span-2">{t('responsibility.policy.risks')}<Input bind:value={policyForm.risks} class="mt-1" /></label>
        <label class="text-xs text-muted-foreground sm:col-span-2">{t('responsibility.policy.tools')}<Input bind:value={policyForm.tools} class="mt-1" /></label>
        <label class="text-xs text-muted-foreground sm:col-span-2">{t('responsibility.policy.data')}<Input bind:value={policyForm.data} class="mt-1" /></label>
        <label class="text-xs text-muted-foreground sm:col-span-2 xl:col-span-4">{t('responsibility.policy.escalation')}<textarea bind:value={policyForm.escalation} rows="3" class="mt-1 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 font-mono text-xs text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"></textarea></label>
        <label class="inline-flex items-center gap-2 text-xs text-muted-foreground"><input type="checkbox" bind:checked={policyForm.enabled} class="h-4 w-4 accent-brand" />{t('responsibility.policy.enabled')}</label>
        <div class="flex justify-end sm:col-span-2 xl:col-span-3"><Button disabled={pending} onclick={savePolicy}><Save class="h-4 w-4" />{t('responsibility.policy.save')}</Button></div>
      </div>
      {#if policyError}<p class="mt-2 text-xs text-destructive">{policyError}</p>{/if}
    {/if}
  </section>

  <section class="px-5 py-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-foreground">{t('responsibility.detail.execution')}</h3>
      <div class="max-w-full overflow-x-auto"><Tabs bind:value={detailTab} {tabs} /></div>
    </div>
    <div class="mt-4 min-h-52" data-detail-tab={detailTab}>
      {#if detailTab === 'attempts'}
        {#if detail.attempts.length === 0}{@render EmptyLine(FileClock, t('responsibility.detail.noAttempts'))}{:else}
          <div class="divide-y divide-border rounded-md border border-border">
            {#each detail.attempts as attempt}
              <article class="px-3 py-3 text-xs">
                <div class="flex flex-wrap items-center justify-between gap-2"><div class="font-medium text-foreground">#{attempt.attempt} · {attempt.runtime_kind}</div><Badge tone={toneForStatus(attempt.outcome)}>{attempt.outcome || 'running'}</Badge></div>
                <div class="mt-2 grid gap-1 text-muted-foreground sm:grid-cols-2"><span>{t('responsibility.detail.run', { id: attempt.run_id })}</span><span>{t('responsibility.detail.tokens', { count: attempt.usage_tokens })}</span><span>{formatDate(attempt.started_at)}</span>{#if attempt.heartbeat_at}<span>{t('responsibility.detail.heartbeat', { time: formatDate(attempt.heartbeat_at) })}</span>{/if}</div>
                {#if attempt.error}<p class="mt-2 text-destructive">{attempt.error}</p>{/if}
              </article>
            {/each}
          </div>
        {/if}
      {:else if detailTab === 'activations'}
        {#if detail.activations.length === 0}{@render EmptyLine(FileClock, t('responsibility.detail.noActivations'))}{:else}
          <div class="divide-y divide-border rounded-md border border-border">
            {#each detail.activations as activation}
              <article class="flex flex-wrap items-center justify-between gap-3 px-3 py-3 text-xs"><div><div class="font-medium text-foreground">{activation.cause}</div><div class="mt-1 text-muted-foreground">{t('responsibility.detail.notBefore', { time: formatDate(activation.not_before) })}</div>{#if activation.last_error}<div class="mt-1 text-destructive">{activation.last_error}</div>{/if}</div><Badge tone={toneForStatus(activation.status)}>{activation.status}</Badge></article>
            {/each}
          </div>
        {/if}
      {:else if detailTab === 'blockers'}
        {#if detail.blockers.length === 0}{@render EmptyLine(ShieldAlert, t('responsibility.detail.noBlockers'))}{:else}
          <div class="space-y-3">
            {#each detail.blockers as blocker}
              <article class="rounded-md border border-border p-3 text-xs">
                <div class="flex flex-wrap items-center justify-between gap-2"><div class="font-medium text-foreground">{blocker.kind} · {blocker.needed}</div><Badge tone={toneForStatus(blocker.status)}>{blocker.status}</Badge></div>
                <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground"><span>{t('responsibility.blocker.requestedFrom', { actor: blocker.requested_from })}</span><span>{t('responsibility.blocker.recheck', { time: formatDate(blocker.recheck_at) })}</span></div>
                {#if blocker.resolution}<p class="mt-2 text-foreground">{blocker.resolution}</p>{/if}
                {#if canManage && blocker.status === 'open'}
                  <div class="mt-3 flex gap-2"><Input value={blockerResolutions[blocker.blocker_id] ?? ''} placeholder={t('responsibility.blocker.resolution')} oninput={(event) => (blockerResolutions = { ...blockerResolutions, [blocker.blocker_id]: event.currentTarget.value })} /><Button size="sm" disabled={pending || !(blockerResolutions[blocker.blocker_id] ?? '').trim()} onclick={() => resolveBlocker(blocker.blocker_id, blocker.version)}>{t('responsibility.blocker.resolve')}</Button></div>
                {/if}
              </article>
            {/each}
          </div>
        {/if}
      {:else if detailTab === 'alerts'}
        {#if detail.alerts.length === 0}{@render EmptyLine(BellRing, t('responsibility.detail.noAlerts'))}{:else}
          <div class="divide-y divide-border rounded-md border border-border">
            {#each detail.alerts as alert}
              <article class="px-3 py-3 text-xs"><div class="flex flex-wrap items-center justify-between gap-2"><div class="font-medium text-foreground">{alert.kind} · {alert.severity}</div><Badge tone={toneForStatus(alert.status === 'open' ? alert.kind : alert.status)}>{alert.status}</Badge></div><div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground"><span>{t('responsibility.due', { time: formatDate(alert.due_at) })}</span><span>{t('responsibility.alert.escalation', { level: alert.escalation_level })}</span><span>{alert.requested_from}</span></div>{#if alert.resolution}<p class="mt-2 text-foreground">{alert.resolution}</p>{/if}</article>
            {/each}
          </div>
        {/if}
      {:else}
        {#if detail.audit.length === 0}{@render EmptyLine(FileClock, t('responsibility.detail.noAudit'))}{:else}
          <ol class="relative ml-2 border-l border-border pl-5">
            {#each detail.audit as record}
              <li class="relative pb-4 text-xs before:absolute before:-left-[23px] before:top-1 before:h-2 before:w-2 before:rounded-full before:bg-brand"><div class="font-medium text-foreground">{record.action}</div><div class="mt-1 text-muted-foreground">{formatDate(record.occurred_at)}{record.actor_id ? ` · ${record.actor_id}` : ''}</div>{#if record.from_state || record.to_state}<div class="mt-1 text-muted-foreground">{record.from_state || '—'} → {record.to_state || '—'}</div>{/if}{#if record.details}<p class="mt-1 text-foreground">{record.details}</p>{/if}</li>
            {/each}
          </ol>
        {/if}
      {/if}
    </div>
  </section>
</section>

{#snippet EmptyLine(icon: typeof FileClock, text: string)}
  {@const Icon = icon}
  <div class="flex min-h-52 flex-col items-center justify-center gap-2 text-center text-sm text-muted-foreground">
    <Icon class="h-8 w-8 opacity-35" /><span>{text}</span>
  </div>
{/snippet}
