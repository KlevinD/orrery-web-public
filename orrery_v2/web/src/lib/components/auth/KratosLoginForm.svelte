<script lang="ts">
  import { ExternalLink, Eye, EyeOff, KeyRound, LoaderCircle, LogIn, MessageCircle, QrCode, ScanLine, ShieldCheck, UserRound } from 'lucide-svelte'
  import { onDestroy } from 'svelte'
  import QRCode from 'qrcode'
  import { authApi, type LoginSessionDTO } from '#lib/api/auth'
  import type { KratosLoginFlowDTO, KratosLoginFlowMessageDTO, KratosLoginFlowNodeDTO } from '#lib/api/kratos'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import { flowNodeLabel, flowNodeValue, hiddenFields, loginFlowMessageText, messageTone, submitNodes, visibleFields } from '#lib/kratos/loginFlow'

  const SESSION_POLL_MS = 1_500
  const SESSION_TIMEOUT_MS = 5 * 60 * 1_000
  type SocialProviderID = 'feishu' | 'wechat' | 'dingtalk' | 'orrery'

  const socialProviderLabels: Record<SocialProviderID, string> = {
    feishu: '飞书登录',
    wechat: '微信登录',
    dingtalk: '钉钉登录',
    orrery: 'Orrery 扫码登录'
  }

  let {
    flow,
    providerName = 'Orrery SSO',
    onAuthenticated = () => window.location.reload()
  } = $props<{ flow: KratosLoginFlowDTO; providerName?: string; onAuthenticated?: (session: LoginSessionDTO) => void }>()
  let visiblePasswords = $state<Record<string, boolean>>({})
  let selectedProviderID = $state<SocialProviderID | undefined>()
  let previewQRCode = $state('')
  let socialError = $state('')
  let socialPending = $state(false)
  let popupWindow: Window | null = null
  let sessionPollTimer: ReturnType<typeof setTimeout> | undefined
  let sessionPollStartedAt = 0
  const hidden = $derived(hiddenFields(flow))
  const fields = $derived(visibleFields(flow))
  const submits = $derived(submitNodes(flow))
  const credentialSubmits = $derived(submits.filter((node) => node.group !== 'oidc'))
  const socialSubmits = $derived(submits.filter((node) => node.group === 'oidc'))
  const feishuNode = $derived(socialSubmits.find(isFeishu))
  const selectedSocial = $derived(selectedProviderID === 'feishu' ? feishuNode : undefined)
  const selectedProviderLabel = $derived(selectedProviderID ? socialProviderLabels[selectedProviderID] : '')

  $effect(() => {
    const providerID = selectedProviderID
    previewQRCode = ''
    if (!providerID) return

    let active = true
    QRCode.toDataURL(`orrery-login-preview:${providerID}`, {
      width: 220,
      margin: 1,
      errorCorrectionLevel: 'M',
      color: { dark: '#111827', light: '#ffffff' }
    }).then((value) => {
      if (active) previewQRCode = value
    }).catch(() => {
      if (active) previewQRCode = ''
    })
    return () => {
      active = false
    }
  })

  function fieldID(node: KratosLoginFlowNodeDTO, index: number) {
    return `kratos-${node.attributes.name}-${index}`
  }

  function inputType(node: KratosLoginFlowNodeDTO) {
    if (node.attributes.type === 'password' && visiblePasswords[node.attributes.name]) return 'text'
    return node.attributes.type
  }

  function togglePassword(name: string) {
    visiblePasswords[name] = !visiblePasswords[name]
  }

  function autocompleteFor(node: KratosLoginFlowNodeDTO): 'username' | 'current-password' | 'one-time-code' | 'email' | 'tel' | 'off' | undefined {
    switch (node.attributes.autocomplete) {
      case 'username':
      case 'current-password':
      case 'one-time-code':
      case 'email':
      case 'tel':
      case 'off':
        return node.attributes.autocomplete
      default:
        return undefined
    }
  }

  function iconFor(node: KratosLoginFlowNodeDTO) {
    if (node.attributes.type === 'password') return KeyRound
    if (node.attributes.autocomplete === 'one-time-code' || node.group === 'totp') return ShieldCheck
    return UserRound
  }

  function messageClass(message: KratosLoginFlowMessageDTO) {
    switch (messageTone(message.type)) {
      case 'error':
        return 'border-destructive/30 bg-destructive/10 text-destructive'
      case 'warning':
        return 'border-amber-500/30 bg-amber-500/5 text-amber-700'
      default:
        return 'border-border bg-muted/30 text-muted-foreground'
    }
  }

  function isFeishu(node: KratosLoginFlowNodeDTO) {
    const value = String(flowNodeValue(node)).toLowerCase()
    return value === 'feishu' || value === 'lark'
  }

  function openSocial(providerID: SocialProviderID) {
    socialError = ''
    selectedProviderID = providerID
  }

  function closeSocial() {
    socialError = ''
    selectedProviderID = undefined
  }

  function stopSessionPolling() {
    if (sessionPollTimer) {
      clearTimeout(sessionPollTimer)
      sessionPollTimer = undefined
    }
  }

  async function pollSession() {
    if (!popupWindow || popupWindow.closed) {
      stopSessionPolling()
      socialPending = false
      sessionPollStartedAt = 0
      socialError = '登录窗口已关闭，请重试。'
      return
    }
    if (Date.now() - sessionPollStartedAt >= SESSION_TIMEOUT_MS) {
      stopSessionPolling()
      socialPending = false
      sessionPollStartedAt = 0
      popupWindow.close()
      popupWindow = null
      socialError = '扫码登录已超时，请重试。'
      return
    }

    try {
      const currentSession = await authApi.currentSession()
      if (currentSession) {
        stopSessionPolling()
        socialPending = false
        sessionPollStartedAt = 0
        popupWindow.close()
        popupWindow = null
        onAuthenticated(currentSession)
        return
      }
    } catch {
      // Keep polling while the external authorization window is active.
    }

    sessionPollTimer = setTimeout(() => void pollSession(), SESSION_POLL_MS)
  }

  function submitSocialToPopup() {
    if (!selectedSocial) return

    const width = 520
    const height = 720
    const left = Math.max(0, window.screenX + (window.outerWidth - width) / 2)
    const top = Math.max(0, window.screenY + (window.outerHeight - height) / 2)
    const popup = window.open('', '_blank', `popup=yes,width=${width},height=${height},left=${left},top=${top}`)
    if (!popup) {
      socialError = '浏览器阻止了登录窗口，请允许弹出窗口后重试。'
      return
    }

    stopSessionPolling()
    popupWindow = popup
    socialPending = true
    sessionPollStartedAt = Date.now()
    socialError = ''

    try {
      const popupForm = popup.document.createElement('form')
      popupForm.method = flow.method.toLowerCase()
      popupForm.action = new URL(flow.action, window.location.href).href
      popupForm.noValidate = true
      for (const node of hidden) {
        if (node.attributes.disabled) continue
        const input = popup.document.createElement('input')
        input.type = 'hidden'
        input.name = node.attributes.name
        input.value = String(flowNodeValue(node))
        popupForm.append(input)
      }
      const provider = popup.document.createElement('input')
      provider.type = 'hidden'
      provider.name = selectedSocial.attributes.name
      provider.value = String(flowNodeValue(selectedSocial))
      popupForm.append(provider)
      popup.document.body.append(popupForm)
      HTMLFormElement.prototype.submit.call(popupForm)
    } catch {
      popup.close()
      popupWindow = null
      socialPending = false
      sessionPollStartedAt = 0
      socialError = '无法打开飞书登录，请重试。'
      return
    }
    void pollSession()
  }

  onDestroy(() => {
    stopSessionPolling()
    popupWindow?.close()
  })
</script>

<form class="space-y-4" method={flow.method.toLowerCase()} action={flow.action} aria-label={`${providerName} 登录`}>
  {#each flow.messages ?? [] as message (message.id)}
    <p class={`rounded-md border px-3 py-2 text-sm ${messageClass(message)}`}>{loginFlowMessageText(message)}</p>
  {/each}

  {#each hidden as node (`${node.attributes.name}-${flowNodeValue(node)}`)}
    <input
      type="hidden"
      name={node.attributes.name}
      value={flowNodeValue(node)}
      disabled={node.attributes.disabled}
    />
  {/each}

  {#each fields as node, index (`${node.attributes.name}-${index}`)}
    {@const id = fieldID(node, index)}
    {@const FieldIcon = iconFor(node)}
    <label class="block" for={id}>
      <span class="mb-1.5 block text-xs font-medium text-muted-foreground">{flowNodeLabel(node)}</span>
      <div class="flex h-9 items-center gap-2 rounded-md border border-border bg-input-background px-3 transition-colors hover:border-foreground/15 hover:bg-card focus-within:border-[#d8d8d8] focus-within:bg-card dark:focus-within:border-[#3a3a3a]">
        <FieldIcon class="h-4 w-4 shrink-0 text-muted-foreground" />
        <input
          class="min-w-0 flex-1 bg-transparent text-sm outline-none"
          {id}
          name={node.attributes.name}
          type={inputType(node)}
          value={flowNodeValue(node)}
          required={node.attributes.required}
          disabled={node.attributes.disabled}
          autocomplete={autocompleteFor(node)}
          inputmode={node.attributes.autocomplete === 'one-time-code' ? 'numeric' : undefined}
        />
        {#if node.attributes.type === 'password'}
          <button
            class="grid h-7 w-7 shrink-0 place-items-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            type="button"
            aria-label={visiblePasswords[node.attributes.name] ? '隐藏密码' : '显示密码'}
            title={visiblePasswords[node.attributes.name] ? '隐藏密码' : '显示密码'}
            onclick={() => togglePassword(node.attributes.name)}
          >
            {#if visiblePasswords[node.attributes.name]}
              <EyeOff class="h-4 w-4" />
            {:else}
              <Eye class="h-4 w-4" />
            {/if}
          </button>
        {/if}
      </div>
    </label>
    {#each node.messages ?? [] as message (message.id)}
      <p class={`rounded-md border px-3 py-2 text-xs ${messageClass(message)}`}>{loginFlowMessageText(message)}</p>
    {/each}
  {/each}

  {#each credentialSubmits as node (`${node.attributes.name}-${flowNodeValue(node)}`)}
    <button
      class="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md bg-brand px-4 text-sm font-medium text-brand-foreground transition-colors hover:bg-brand-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60"
      type="submit"
      name={node.attributes.name}
      value={flowNodeValue(node)}
      disabled={node.attributes.disabled}
    >
      <LogIn class="h-4 w-4" />
      <span>{flowNodeLabel(node) || '登录'}</span>
    </button>
  {/each}

  {#if credentialSubmits.length > 0}
    <div class="flex items-center gap-3 py-1" aria-hidden="true">
      <span class="h-px flex-1 bg-border"></span>
      <span class="text-xs text-muted-foreground">其他方式</span>
      <span class="h-px flex-1 bg-border"></span>
    </div>
  {/if}

  <div class="flex flex-wrap justify-center gap-2">
    <Tooltip text="飞书登录">
      <button
        class="inline-flex h-9 w-9 items-center justify-center rounded-md border border-border bg-card transition-colors hover:border-foreground/15 hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60"
        type="button"
        aria-label="飞书登录"
        title="飞书登录"
        disabled={feishuNode?.attributes.disabled}
        onclick={() => openSocial('feishu')}
      >
        <span aria-hidden="true" class="text-sm font-semibold">飞</span>
      </button>
    </Tooltip>
    <Tooltip text="微信登录">
      <button
        class="inline-flex h-9 w-9 items-center justify-center rounded-md border border-border bg-card text-[#07c160] transition-colors hover:border-foreground/15 hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        type="button"
        aria-label="微信登录"
        title="微信登录"
        onclick={() => openSocial('wechat')}
      >
        <MessageCircle class="h-5 w-5" strokeWidth={2.2} />
      </button>
    </Tooltip>
    <Tooltip text="钉钉登录">
      <button
        class="inline-flex h-9 w-9 items-center justify-center rounded-md border border-border bg-card text-[#1677ff] transition-colors hover:border-foreground/15 hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        type="button"
        aria-label="钉钉登录"
        title="钉钉登录"
        onclick={() => openSocial('dingtalk')}
      >
        <span aria-hidden="true" class="text-sm font-semibold">钉</span>
      </button>
    </Tooltip>
    <Tooltip text="Orrery 扫码登录">
      <button
        class="inline-flex h-9 w-9 items-center justify-center rounded-md border border-border bg-card text-foreground transition-colors hover:border-foreground/15 hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        type="button"
        aria-label="Orrery 扫码登录"
        title="Orrery 扫码登录"
        onclick={() => openSocial('orrery')}
      >
        <ScanLine class="h-5 w-5" strokeWidth={2} />
      </button>
    </Tooltip>
  </div>

  <Dialog open={Boolean(selectedProviderID)} title={selectedProviderLabel} onClose={closeSocial} class="!max-w-sm">
    <div class="grid justify-items-center gap-4">
      <div class="grid aspect-square w-[220px] max-w-full place-items-center overflow-hidden rounded-md border border-border bg-white p-2">
        {#if previewQRCode}
          <img src={previewQRCode} alt={`${selectedProviderLabel}二维码预览`} class="h-full w-full object-contain" />
        {:else}
          <LoaderCircle class="h-6 w-6 animate-spin text-muted-foreground" aria-label="正在生成二维码" />
        {/if}
      </div>

      {#if selectedProviderID === 'feishu' && selectedSocial}
        <div class="grid justify-items-center gap-2">
          <button
            class="inline-flex h-8 items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium text-brand transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            type="button"
            onclick={submitSocialToPopup}
          >
            <QrCode class="h-4 w-4" />
            <span>打开飞书扫码页</span>
          </button>
        <button
          class="inline-flex h-8 items-center justify-center gap-1.5 rounded-md px-3 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          type="submit"
          name={selectedSocial.attributes.name}
          value={flowNodeValue(selectedSocial)}
          formnovalidate
        >
          <ExternalLink class="h-4 w-4" />
          <span>在本机授权</span>
        </button>
        </div>
      {:else if selectedProviderID}
        <p class="inline-flex h-7 items-center rounded-md border border-border bg-muted/40 px-2.5 text-xs text-muted-foreground" role="status">暂未连接</p>
      {/if}

      {#if socialError}
        <p class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{socialError}</p>
      {:else if socialPending}
        <p class="text-center text-xs text-muted-foreground" aria-live="polite">等待飞书扫码确认</p>
      {/if}
    </div>
  </Dialog>
</form>
