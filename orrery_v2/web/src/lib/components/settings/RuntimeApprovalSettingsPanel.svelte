<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type {
    RuntimeApprovalEffectView,
    RuntimeApprovalSecurityModeView,
    SettingsRuntimeApprovalExecPolicyViewModel,
    SettingsRuntimeApprovalOverrideViewModel,
    SettingsRuntimeApprovalToolPolicyViewModel,
    SettingsViewModel
  } from '#lib/types/view-models'
  import { Plus, ShieldCheck, Trash2, TriangleAlert } from 'lucide-svelte'

  type ApprovalPreset = 'inherit' | 'human' | 'ai' | 'always_allow'

  interface Props {
    draft: SettingsViewModel
    busy?: boolean
    onsave?: () => void
  }

  let { draft = $bindable(), busy = false, onsave }: Props = $props()
  let t = $derived(createTranslator($locale))
  let approvalPolicy = $state(draft.runtimeApprovals)
  draft.runtimeApprovals = approvalPolicy
  let policyScope = $state<'default' | 'personal_assistant'>('default')
  let nextRuleID = 0
  let personalAssistant = approvalPolicy.conversationDefaults.personal_assistant
  if (!personalAssistant?.exec || !personalAssistant.tools) {
    personalAssistant = {
      ...personalAssistant,
      hilMode: personalAssistant?.hilMode ?? 'auto',
      approvalPolicy: personalAssistant?.approvalPolicy ?? 'on-request',
      approvalsReviewer: personalAssistant?.approvalsReviewer ?? (personalAssistant?.hilMode === 'manual' ? 'human' : 'ai'),
      exec: personalAssistant?.exec ?? { approvalPolicy: 'never', security: 'allowlist', allowlist: [] },
      tools: personalAssistant?.tools ?? { security: 'allowlist', allowlist: [] }
    }
    approvalPolicy.conversationDefaults.personal_assistant = personalAssistant
  }

  const hilOptions = $derived([
    { label: t('settings.runtimeApprovals.manual'), value: 'manual' },
    { label: t('settings.runtimeApprovals.auto'), value: 'auto' }
  ])
  const securityOptions = $derived([
    { label: t('settings.runtimeApprovals.deny'), value: 'deny' },
    { label: t('settings.runtimeApprovals.allowlist'), value: 'allowlist' },
    { label: t('settings.runtimeApprovals.full'), value: 'full' }
  ])
  const execApprovalOptions = $derived([
    { label: t('settings.runtimeApprovals.execApprovalNever'), value: 'never' },
    { label: t('settings.runtimeApprovals.execApprovalOnRequest'), value: 'on-request' }
  ])
  const approvalModeOptions = $derived([
    { label: t('settings.runtimeApprovals.inherit'), value: '' },
    { label: t('settings.runtimeApprovals.humanReview'), value: 'human' },
    { label: t('settings.runtimeApprovals.aiReview'), value: 'ai' },
    { label: t('settings.runtimeApprovals.alwaysAllow'), value: 'always_allow' }
  ])
  const securityOverrideOptions = $derived([
    { label: t('settings.runtimeApprovals.inherit'), value: '' },
    ...securityOptions
  ])
  const riskOptions = $derived([
    { label: t('settings.runtimeApprovals.riskUnspecified'), value: '' },
    { label: t('settings.runtimeApprovals.riskLow'), value: 'low' },
    { label: t('settings.runtimeApprovals.riskMedium'), value: 'medium' },
    { label: t('settings.runtimeApprovals.riskHigh'), value: 'high' },
    { label: t('settings.runtimeApprovals.riskCritical'), value: 'critical' },
    { label: t('settings.runtimeApprovals.riskUnknown'), value: 'unknown' }
  ])
  const effectOptions: Array<{ value: RuntimeApprovalEffectView; label: string }> = [
    { value: 'read', label: 'read' },
    { value: 'write', label: 'write' },
    { value: 'network', label: 'network' },
    { value: 'delete', label: 'delete' },
    { value: 'publish', label: 'publish' },
    { value: 'permission', label: 'permission' },
    { value: 'identity', label: 'identity' },
    { value: 'credential', label: 'credential' },
    { value: 'financial', label: 'financial' },
    { value: 'secret', label: 'secret' },
    { value: 'unknown', label: 'unknown' }
  ]
  let reviewerOptions = $derived([
    { label: t('settings.runtimeApprovals.noReviewer'), value: '' },
    ...draft.llmProfiles
      .filter((profile) => profile.enabled)
      .map((profile) => ({ label: profile.displayName || profile.profileId, value: profile.profileId }))
  ])

  function personalAssistantOverride(): SettingsRuntimeApprovalOverrideViewModel {
    return personalAssistant
  }

  function activeExecPolicy(): SettingsRuntimeApprovalExecPolicyViewModel {
    return policyScope === 'personal_assistant'
      ? personalAssistantOverride().exec!
      : approvalPolicy.exec
  }

  function activeToolPolicy(): SettingsRuntimeApprovalToolPolicyViewModel {
    return policyScope === 'personal_assistant'
      ? personalAssistantOverride().tools!
      : approvalPolicy.tools
  }

  function setExecSecurity(value: string) {
    activeExecPolicy().security = value as RuntimeApprovalSecurityModeView
  }

  function setExecApprovalPolicy(value: string) {
    activeExecPolicy().approvalPolicy = value as 'never' | 'on-request'
  }

  function setPersonalAssistantHIL(value: string) {
    personalAssistantOverride().hilMode = value as 'manual' | 'auto'
    personalAssistantOverride().approvalsReviewer = value === 'auto' ? 'ai' : 'human'
  }

  function setDefaultHIL(value: string) {
    approvalPolicy.defaultHILMode = value as 'manual' | 'auto'
    approvalPolicy.defaultApprovalsReviewer = value === 'auto' ? 'ai' : 'human'
  }

  function setToolSecurity(value: string) {
    activeToolPolicy().security = value as RuntimeApprovalSecurityModeView
  }

  function addExecRule() {
    nextRuleID += 1
    const policy = activeExecPolicy()
    policy.allowlist = [
      ...policy.allowlist,
      { ruleId: `exec-ui-${Date.now()}-${nextRuleID}`, executable: '', subcommands: [], safeFlags: [], workspaceOnly: true }
    ]
  }

  function removeExecRule(index: number) {
    const policy = activeExecPolicy()
    policy.allowlist = policy.allowlist.filter((_, itemIndex) => itemIndex !== index)
  }

  function addToolRule() {
    nextRuleID += 1
    const policy = activeToolPolicy()
    policy.allowlist = [
      ...policy.allowlist,
      { ruleId: `tool-ui-${Date.now()}-${nextRuleID}`, toolId: '', effects: [], risk: '' }
    ]
  }

  function removeToolRule(index: number) {
    const policy = activeToolPolicy()
    policy.allowlist = policy.allowlist.filter((_, itemIndex) => itemIndex !== index)
  }

  function commaList(value: string): string[] {
    return value.split(',').map((item) => item.trim()).filter(Boolean)
  }

  function lineList(value: string): string[] {
    return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean)
  }

  function toggleToolEffect(index: number, effect: RuntimeApprovalEffectView, checked: boolean) {
    const rule = activeToolPolicy().allowlist[index]
    if (!rule) return
    rule.effects = checked
      ? Array.from(new Set([...rule.effects, effect]))
      : rule.effects.filter((item) => item !== effect)
  }

  function addAgentOverride() {
    approvalPolicy.agentOverrides = [
      ...approvalPolicy.agentOverrides,
      { logicalAgentId: '' }
    ]
  }

  function removeAgentOverride(index: number) {
    approvalPolicy.agentOverrides = approvalPolicy.agentOverrides.filter((_, itemIndex) => itemIndex !== index)
  }

  function agentApprovalPreset(override: SettingsRuntimeApprovalOverrideViewModel): ApprovalPreset | '' {
    if (override.approvalPolicy === 'never' || override.approvalBypass) return 'always_allow'
    if (override.approvalsReviewer === 'ai') return 'ai'
    if (override.approvalsReviewer === 'human') return 'human'
    return ''
  }

  function setAgentApprovalPreset(index: number, value: string) {
    const override = approvalPolicy.agentOverrides[index]
    if (!override) return
    delete override.approvalBypass
    delete override.hilMode
    if (!value || value === 'inherit') {
      delete override.approvalPolicy
      delete override.approvalsReviewer
      return
    }
    if (value === 'always_allow') {
      override.approvalPolicy = 'never'
      delete override.approvalsReviewer
      return
    }
    override.approvalPolicy = 'on-request'
    override.approvalsReviewer = value === 'ai' ? 'ai' : 'human'
  }

  function setAgentSecurity(index: number, kind: 'exec' | 'tools', value: string) {
    const override = approvalPolicy.agentOverrides[index]
    if (!override) return
    if (!value) {
      delete override[kind]
      return
    }
    if (kind === 'exec') {
      override.exec = {
        approvalPolicy: override.exec?.approvalPolicy ?? approvalPolicy.exec.approvalPolicy,
        security: value as RuntimeApprovalSecurityModeView,
        allowlist: override.exec?.allowlist ?? []
      }
      return
    }
    override.tools = {
      security: value as RuntimeApprovalSecurityModeView,
      allowlist: override.tools?.allowlist ?? []
    }
  }
</script>

<section class="space-y-6 rounded-xl border border-border bg-card p-4 sm:p-6">
  <div>
    <h2 class="text-sm font-semibold text-foreground">{t('settings.runtimeApprovals.title')}</h2>
    <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimeApprovals.desc')}</p>
  </div>

  <div class="grid gap-4 md:grid-cols-2">
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.defaultHIL')}</span>
      <Select value={approvalPolicy.defaultHILMode} options={hilOptions} onchange={(event) => setDefaultHIL(event.currentTarget.value)} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.personalAssistantHIL')}</span>
      <Select value={personalAssistantOverride().hilMode} options={hilOptions} onchange={(event) => setPersonalAssistantHIL(event.currentTarget.value)} />
    </label>
  </div>

  <div class="space-y-3 border-t border-border pt-5">
    <div>
      <h3 class="text-sm font-medium text-foreground">{t('settings.runtimeApprovals.reviewer')}</h3>
      <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimeApprovals.reviewerDesc')}</p>
    </div>
    <div class="grid gap-4 md:grid-cols-3">
      <label class="space-y-1.5 md:col-span-1">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.reviewerModel')}</span>
        <Select value={approvalPolicy.reviewer.llmProfileId} options={reviewerOptions} onchange={(event) => (approvalPolicy.reviewer.llmProfileId = event.currentTarget.value)} />
      </label>
      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.timeout')}</span>
        <Input type="number" value={approvalPolicy.reviewer.timeoutMs} oninput={(event) => (approvalPolicy.reviewer.timeoutMs = event.currentTarget.value)} />
      </label>
      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.maxTokens')}</span>
        <Input type="number" value={approvalPolicy.reviewer.maxTokens} oninput={(event) => (approvalPolicy.reviewer.maxTokens = event.currentTarget.value)} />
      </label>
    </div>
  </div>

  <div class="space-y-5 border-t border-border pt-5">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h3 class="text-sm font-medium text-foreground">{t('settings.runtimeApprovals.policy')}</h3>
        <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimeApprovals.policyDesc')}</p>
      </div>
      <label class="min-w-52 space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.policyScope')}</span>
        <select
          class="flex h-9 w-full rounded-md border border-border bg-input-background px-3 text-sm"
          bind:value={policyScope}
        >
          <option value="default">{t('settings.runtimeApprovals.globalDefault')}</option>
          <option value="personal_assistant">{t('settings.runtimeApprovals.personalAssistant')}</option>
        </select>
      </label>
    </div>

    <div class="space-y-3">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div class="flex flex-wrap gap-3">
          <label class="min-w-48 space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.execApprovalPolicy')}</span>
            <Select value={activeExecPolicy().approvalPolicy} options={execApprovalOptions} onchange={(event) => setExecApprovalPolicy(event.currentTarget.value)} />
          </label>
          <label class="min-w-48 space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.execSecurity')}</span>
            <Select value={activeExecPolicy().security} options={securityOptions} onchange={(event) => setExecSecurity(event.currentTarget.value)} />
          </label>
        </div>
        <Button variant="outline" size="sm" onclick={addExecRule} ariaLabel={t('settings.runtimeApprovals.addExec')}>
          <Plus class="h-4 w-4" />{t('settings.runtimeApprovals.addExec')}
        </Button>
      </div>
      {#if activeExecPolicy().allowlist.length === 0}
        <p class="rounded-md border border-dashed border-border px-3 py-4 text-xs text-muted-foreground">{t('settings.runtimeApprovals.noExecRules')}</p>
      {:else}
        <div class="space-y-3">
          {#each activeExecPolicy().allowlist as rule, index (rule.ruleId)}
            <div class="grid gap-3 rounded-md border border-border p-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
              <label class="space-y-1.5">
                <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.executable')}</span>
                <Input ariaLabel={`${t('settings.runtimeApprovals.executable')} ${index + 1}`} value={rule.executable} oninput={(event) => (rule.executable = event.currentTarget.value)} />
              </label>
              <label class="space-y-1.5">
                <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.subcommands')}</span>
                <Input ariaLabel={`${t('settings.runtimeApprovals.subcommands')} ${index + 1}`} value={rule.subcommands.join(', ')} oninput={(event) => (rule.subcommands = commaList(event.currentTarget.value))} />
              </label>
              <label class="space-y-1.5">
                <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.safeFlags')}</span>
                <Input ariaLabel={`${t('settings.runtimeApprovals.safeFlags')} ${index + 1}`} value={rule.safeFlags.join(', ')} oninput={(event) => (rule.safeFlags = commaList(event.currentTarget.value))} />
              </label>
              <div class="flex items-end gap-2">
                <label class="flex h-9 items-center gap-2 whitespace-nowrap text-xs text-muted-foreground">
                  <input type="checkbox" checked={rule.workspaceOnly} onchange={(event) => (rule.workspaceOnly = event.currentTarget.checked)} />
                  {t('settings.runtimeApprovals.workspaceOnly')}
                </label>
                <Button variant="ghost" size="icon" onclick={() => removeExecRule(index)} ariaLabel={`${t('settings.delete')} ${index + 1}`}>
                  <Trash2 class="h-4 w-4" />
                </Button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <div class="space-y-3 border-t border-border pt-4">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <label class="min-w-48 space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.toolSecurity')}</span>
          <Select value={activeToolPolicy().security} options={securityOptions} onchange={(event) => setToolSecurity(event.currentTarget.value)} />
        </label>
        <Button variant="outline" size="sm" onclick={addToolRule} ariaLabel={t('settings.runtimeApprovals.addTool')}>
          <Plus class="h-4 w-4" />{t('settings.runtimeApprovals.addTool')}
        </Button>
      </div>
      {#if activeToolPolicy().allowlist.length === 0}
        <p class="rounded-md border border-dashed border-border px-3 py-4 text-xs text-muted-foreground">{t('settings.runtimeApprovals.noToolRules')}</p>
      {:else}
        <div class="space-y-3">
          {#each activeToolPolicy().allowlist as rule, index (rule.ruleId)}
            <div class="grid gap-3 rounded-md border border-border p-3 lg:grid-cols-[minmax(12rem,1fr)_minmax(16rem,2fr)_minmax(10rem,1fr)_auto]">
              <label class="space-y-1.5">
                <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.toolID')}</span>
                <Input ariaLabel={`${t('settings.runtimeApprovals.toolID')} ${index + 1}`} value={rule.toolId} oninput={(event) => (rule.toolId = event.currentTarget.value)} />
              </label>
              <fieldset class="space-y-1.5">
                <legend class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.effects')}</legend>
                <div class="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1">
                  {#each effectOptions as effect}
                    <label class="flex items-center gap-1 text-xs text-muted-foreground">
                      <input
                        type="checkbox"
                        checked={rule.effects.includes(effect.value)}
                        aria-label={`${effect.label[0].toUpperCase()}${effect.label.slice(1)} effect ${index + 1}`}
                        onchange={(event) => toggleToolEffect(index, effect.value, event.currentTarget.checked)}
                      />
                      {effect.label}
                    </label>
                  {/each}
                </div>
              </fieldset>
              <label class="space-y-1.5">
                <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.risk')}</span>
                <Select value={rule.risk} options={riskOptions} onchange={(event) => (rule.risk = event.currentTarget.value as typeof rule.risk)} />
              </label>
              <div class="flex items-end">
                <Button variant="ghost" size="icon" onclick={() => removeToolRule(index)} ariaLabel={`${t('settings.delete')} ${index + 1}`}>
                  <Trash2 class="h-4 w-4" />
                </Button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>

  <div class="space-y-4 border-t border-border pt-5">
    <div class="flex items-start gap-3 rounded-md border border-amber-500/35 bg-amber-500/5 p-3">
      <ShieldCheck class="mt-0.5 h-4 w-4 shrink-0 text-amber-700" />
      <div>
        <h3 class="text-sm font-medium text-foreground">{t('settings.runtimeApprovals.forcedHuman')}</h3>
        <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimeApprovals.forcedHumanDesc')}</p>
      </div>
    </div>
    <div class="grid gap-4 md:grid-cols-2">
      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.forcedToolIDs')}</span>
        <textarea
          class="min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm focus-visible:outline-none"
          aria-label={t('settings.runtimeApprovals.forcedToolIDs')}
          value={approvalPolicy.forcedHuman.toolIds.join('\n')}
          oninput={(event) => (approvalPolicy.forcedHuman.toolIds = lineList(event.currentTarget.value))}
        ></textarea>
      </label>
      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.runtimeApprovals.forcedExec')}</span>
        <textarea
          class="min-h-24 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm focus-visible:outline-none"
          value={approvalPolicy.forcedHuman.execExecutables.join('\n')}
          oninput={(event) => (approvalPolicy.forcedHuman.execExecutables = lineList(event.currentTarget.value))}
        ></textarea>
      </label>
    </div>
  </div>

  <div class="space-y-3 border-t border-border pt-5">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="text-sm font-medium text-foreground">{t('settings.runtimeApprovals.agentOverrides')}</h3>
        <p class="mt-1 text-xs text-muted-foreground">{t('settings.runtimeApprovals.agentOverridesDesc')}</p>
      </div>
      <Button variant="outline" size="sm" onclick={addAgentOverride} ariaLabel={t('settings.runtimeApprovals.addAgent')}>
        <Plus class="h-4 w-4" />{t('settings.runtimeApprovals.addAgent')}
      </Button>
    </div>
    {#each approvalPolicy.agentOverrides as override, index}
      <div class="grid gap-3 rounded-md border border-border p-3 md:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
        <label class="space-y-1.5">
          <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.logicalAgentID')}</span>
          <Input ariaLabel={`${t('settings.runtimeApprovals.logicalAgentID')} ${index + 1}`} value={override.logicalAgentId} oninput={(event) => (override.logicalAgentId = event.currentTarget.value)} />
        </label>
        <label class="space-y-1.5">
          <span class="text-xs text-muted-foreground">{t('settings.runtimeApprovals.approvalMode')}</span>
          <Select value={agentApprovalPreset(override)} options={approvalModeOptions} onchange={(event) => setAgentApprovalPreset(index, event.currentTarget.value)} />
        </label>
        <label class="space-y-1.5">
          <span class="text-xs text-muted-foreground">Exec</span>
          <Select value={override.exec?.security ?? ''} options={securityOverrideOptions} onchange={(event) => setAgentSecurity(index, 'exec', event.currentTarget.value)} />
        </label>
        <label class="space-y-1.5">
          <span class="text-xs text-muted-foreground">Tool</span>
          <Select value={override.tools?.security ?? ''} options={securityOverrideOptions} onchange={(event) => setAgentSecurity(index, 'tools', event.currentTarget.value)} />
        </label>
        <div class="flex items-end">
          <Button variant="ghost" size="icon" onclick={() => removeAgentOverride(index)} ariaLabel={`${t('settings.delete')} ${index + 1}`}>
            <Trash2 class="h-4 w-4" />
          </Button>
        </div>
        {#if agentApprovalPreset(override) === 'always_allow'}
          <div class="flex items-start gap-2 border-t border-amber-500/30 pt-2 text-xs text-amber-800 md:col-span-full dark:text-amber-300">
            <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
            <span>{t('settings.runtimeApprovals.alwaysAllowWarning')}</span>
          </div>
        {/if}
      </div>
    {/each}
  </div>

  <div class="flex justify-end border-t border-border pt-4">
    <Button disabled={busy} onclick={onsave} ariaLabel={t('settings.save')}>{t('settings.save')}</Button>
  </div>
</section>
