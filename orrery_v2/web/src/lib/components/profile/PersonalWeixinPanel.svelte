<script lang="ts">
  import { Bot, Building2, CheckCircle2, Link2Off, QrCode, Save } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { PersonalAssistantProfileDTO, ProfileWeixinDTO } from '#lib/types/api'

  interface Props {
    profile?: ProfileWeixinDTO
    personalAssistant?: PersonalAssistantProfileDTO
    personalAssistantOnly?: boolean
    selectedAgentId?: string
    weixinTargetKind?: 'agent' | 'personal_assistant'
    saving?: boolean
    loginBusy?: boolean
    loginStatus?: string
    loginMessage?: string
    loginQRCodeUrl?: string
    loginQRCodeDataUrl?: string
    verifyCode?: string
    onscan?: () => void
    onunbind?: () => void
    onsavetarget?: () => void
    onsubmitverify?: () => void
  }

  let {
    profile,
    personalAssistant,
    personalAssistantOnly = false,
    selectedAgentId = $bindable(''),
    weixinTargetKind = $bindable('personal_assistant'),
    saving = false,
    loginBusy = false,
    loginStatus = 'idle',
    loginMessage = '',
    loginQRCodeUrl = '',
    loginQRCodeDataUrl = '',
    verifyCode = $bindable(''),
    onscan = () => {},
    onunbind = () => {},
    onsavetarget = () => {},
    onsubmitverify = () => {}
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  const agentOptions = $derived([
    { label: profile?.agents.length ? t('profile.weixin.selectBot') : t('profile.weixin.noBots'), value: '' },
    ...(profile?.agents ?? []).map((agent) => ({
      label: agent.display_name ? `${agent.display_name} (${agent.agent_id})` : agent.agent_id,
      value: agent.agent_id
    }))
  ])
  const currentAgent = $derived((profile?.agents ?? []).find((agent) => agent.agent_id === profile?.current_agent_id))
  const defaultAgent = $derived((profile?.agents ?? []).find((agent) => agent.agent_id === profile?.default_agent_id))
  const personalAssistantProfileId = $derived(personalAssistant?.agent_profile_id?.trim() ?? '')
  const personalAssistantName = $derived(personalAssistant?.display_name || t('profile.personalAssistant.title'))
  const personalAssistantTargetLabel = $derived(
    personalAssistantProfileId ? `${personalAssistantName} (${personalAssistantProfileId})` : personalAssistantName
  )
  const canBind = $derived(
    weixinTargetKind === 'personal_assistant'
      ? Boolean(personalAssistantProfileId)
      : Boolean(selectedAgentId.trim())
  )
</script>

<section class="rounded-xl border border-border bg-card p-5">
  <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
    <div class="min-w-0">
      <h2 class="text-sm font-semibold text-foreground">{t('people.weixinBinding')}</h2>
      <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('profile.weixin.description')}</p>
    </div>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" disabled={loginBusy || !canBind} onclick={onscan}>
        <QrCode class="h-4 w-4" />
        {profile?.weixin_bound ? t('profile.weixin.rebind') : t('people.weixinBinding.scan')}
      </Button>
      {#if profile?.weixin_bound}
        <Button variant="outline" disabled={saving} onclick={onunbind}>
          <Link2Off class="h-4 w-4" />
          {t('profile.weixin.unbind')}
        </Button>
      {/if}
    </div>
  </div>

  <dl class="mt-4 grid gap-x-5 gap-y-3 text-sm md:grid-cols-3">
    <div class="border-t border-border pt-3">
      <dt class="text-xs text-muted-foreground">{t('profile.weixin.status')}</dt>
      <dd class="mt-1 flex items-center gap-2 text-foreground">
        {#if profile?.weixin_bound}
          <CheckCircle2 class="h-4 w-4 text-emerald-600" />
          {t('profile.weixin.bound')}
        {:else}
          {t('people.weixinBinding.notBound')}
        {/if}
      </dd>
    </div>
    <div class="border-t border-border pt-3">
      <dt class="text-xs text-muted-foreground">{t('people.weixinBinding.boundUserId')}</dt>
      <dd class="mt-1 break-all text-foreground">{profile?.weixin_user_id || t('profile.weixin.userIdPending')}</dd>
    </div>
    <div class="border-t border-border pt-3">
      <dt class="text-xs text-muted-foreground">{t('profile.weixin.currentTarget')}</dt>
      <dd class="mt-1 break-all text-foreground">
        {weixinTargetKind === 'personal_assistant'
          ? personalAssistantTargetLabel
          : currentAgent?.display_name || profile?.current_agent_id || t('profile.weixin.noBotSelected')}
      </dd>
    </div>
  </dl>

  {#if !personalAssistantOnly}
  <fieldset class="mt-4">
    <legend class="mb-2 text-xs font-medium text-muted-foreground">{t('profile.weixin.target')}</legend>
    <div class="grid grid-cols-2 rounded-md border border-border bg-muted p-0.5">
      <button
        class="inline-flex min-h-10 items-center justify-center gap-2 rounded-sm px-3 text-xs font-medium transition-colors {weixinTargetKind === 'personal_assistant' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
        type="button"
        aria-pressed={weixinTargetKind === 'personal_assistant'}
        onclick={() => { weixinTargetKind = 'personal_assistant' }}
      >
        <Bot class="h-4 w-4" />
        {personalAssistantName}
      </button>
      <button
        class="inline-flex min-h-10 items-center justify-center gap-2 rounded-sm px-3 text-xs font-medium transition-colors {weixinTargetKind === 'agent' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
        type="button"
        aria-pressed={weixinTargetKind === 'agent'}
        onclick={() => { weixinTargetKind = 'agent' }}
      >
        <Building2 class="h-4 w-4" />
        {t('profile.weixin.organizationBot')}
      </button>
    </div>
  </fieldset>
  {/if}

  <div class="mt-4 grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
    {#if weixinTargetKind === 'agent'}
      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('profile.weixin.defaultBot')}</span>
        <Select bind:value={selectedAgentId} options={agentOptions} disabled={(profile?.agents.length ?? 0) === 0 || saving} />
        <span class="block text-[11px] leading-4 text-muted-foreground">{t('profile.weixin.defaultBotHelp')}</span>
      </label>
    {:else}
      <div class="border-y border-border py-3">
        <p class="text-sm font-medium text-foreground">{personalAssistantName}</p>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('profile.weixin.personalAssistantHelp')}</p>
      </div>
    {/if}
    <Button disabled={saving || !canBind || !profile?.weixin_bound} onclick={onsavetarget}>
      <Save class="h-4 w-4" />
      {t('profile.weixin.saveTarget')}
    </Button>
  </div>

  {#if weixinTargetKind === 'agent' && defaultAgent}
    <p class="mt-3 rounded-md bg-muted/30 px-3 py-2 text-xs leading-5 text-muted-foreground">
      {t('profile.weixin.defaultBot')}: {defaultAgent.display_name} ({defaultAgent.agent_id})
    </p>
  {/if}

  {#if loginMessage}
    <p class="mt-4 rounded-md border border-border bg-muted/30 px-3 py-2 text-xs leading-5 text-muted-foreground" role="status" aria-live="polite" aria-atomic="true">
      {loginMessage}
    </p>
  {/if}

  {#if loginQRCodeDataUrl}
    <div class="mt-4 flex flex-col gap-4 sm:flex-row">
      <img class="h-56 w-56 rounded-md border border-border bg-white p-2" src={loginQRCodeDataUrl} alt={t('people.weixinBinding.qrAlt')} />
      <div class="min-w-0 flex-1 space-y-2 text-xs leading-5 text-muted-foreground">
        <p>{t('profile.weixin.scanHelp')}</p>
        <a class="break-all text-brand hover:underline" href={loginQRCodeUrl} target="_blank" rel="noreferrer">{t('people.weixinBinding.openLink')}</a>
      </div>
    </div>
  {/if}

  {#if loginStatus === 'need_verifycode'}
    <div class="mt-4 flex flex-wrap items-end gap-2">
      <label class="min-w-48 flex-1 space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('people.weixinBinding.verifyCode')}</span>
        <Input
          bind:value={verifyCode}
          name="weixin-verification-code"
          autocomplete="one-time-code"
          inputmode="numeric"
          ariaLabel={t('people.weixinBinding.verifyCode')}
          placeholder={t('people.weixinBinding.verifyCode.placeholder')}
        />
      </label>
      <Button disabled={loginBusy || !verifyCode.trim()} onclick={onsubmitverify}>{t('people.weixinBinding.submitVerifyCode')}</Button>
    </div>
  {/if}
</section>

{#if weixinTargetKind === 'agent'}
  <section class="rounded-xl border border-border bg-card p-5">
    <div class="flex items-start gap-3">
      <div class="grid h-10 w-10 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
        <Bot class="h-5 w-5" />
      </div>
      <div class="min-w-0 text-sm leading-6 text-muted-foreground">
        <h2 class="text-sm font-semibold text-foreground">{t('profile.weixin.commandsTitle')}</h2>
        <p class="mt-1">{t('profile.weixin.commandList')}</p>
        <p>{t('profile.weixin.commandSwitch')}</p>
      </div>
    </div>
  </section>
{/if}
