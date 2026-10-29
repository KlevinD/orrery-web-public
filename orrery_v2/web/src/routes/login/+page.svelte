<script lang="ts">
  import { Eye, EyeOff, KeyRound, LogIn, RefreshCw, ShieldCheck, UserRound } from 'lucide-svelte'
  import { page } from '$app/stores'
  import { ApiClientError } from '#lib/api/client'
  import {
    authApi,
    authSessionFromDTO,
    loginWithPassword,
    type AuthProviderDTO,
    type LoginSessionDTO
  } from '#lib/api/auth'
  import { kratosApi, type KratosLoginFlowDTO } from '#lib/api/kratos'
  import KratosLoginForm from '#lib/components/auth/KratosLoginForm.svelte'
  import { navigateToIdentityLogin, navigateToLoginReturnTo } from '#lib/routes/identityLoginNavigation'
  import {
    identityLoginReturnToFromURL,
    passwordChangeURLWithReturnTo,
    prepareIdentityLoginRetryReturnTo,
    safeReturnToFromURL
  } from '#lib/routes/returnTo'
  import { applySession, session } from '#lib/stores/session.svelte'
  import { onDestroy, onMount } from 'svelte'

  const LOGIN_TIMEOUT_MS = 10_000

  let username = $state('')
  let password = $state('')
  let mfaCode = $state('')
  let mfaRequired = $state(false)
  let passwordChangeRequired = $state(
    session.mustChangePassword || $page.url.searchParams.get('change_password') === '1'
  )
  let newPassword = $state('')
  let confirmPassword = $state('')
  let submitting = $state(false)
  let showPassword = $state(false)
  let error = $state('')
  let providersLoading = $state(true)
  let providerError = $state('')
  let providersEndpointMissing = $state(false)
  let providers = $state<AuthProviderDTO[]>([])
  let loginFlow = $state<KratosLoginFlowDTO | undefined>()
  let flowLoading = $state(false)
  let flowError = $state('')
  let authAbortController: AbortController | undefined
  let loginAbortController: AbortController | undefined
  let loginTimeout: ReturnType<typeof setTimeout> | undefined
  let returnTo = $state(safeReturnToFromURL($page.url))
  const flowID = $derived($page.url.searchParams.get('flow')?.trim() ?? '')
  const passwordChangeRequested = $derived($page.url.searchParams.get('change_password') === '1')
  const browserProviders = $derived(providers.filter((provider) => provider.enabled && provider.kind === 'browser'))
  const kratosProvider = $derived(browserProviders.find((provider) => provider.name === 'kratos'))
  const showLocalLogin = $derived(
    passwordChangeRequired || providersEndpointMissing || providers.some((provider) => provider.enabled && provider.kind === 'local')
  )

  $effect(() => {
    if (session.mustChangePassword || passwordChangeRequested) passwordChangeRequired = true
  })

  async function submit() {
    if (submitting || !showLocalLogin) return
    error = ''
    submitting = true
    clearPendingLogin()
    const controller = new AbortController()
    loginAbortController = controller
    loginTimeout = setTimeout(() => {
      controller.abort(new DOMException('Login request timed out', 'TimeoutError'))
    }, LOGIN_TIMEOUT_MS)
    try {
      if (passwordChangeRequired) {
        if (newPassword.length < 12) {
          error = '新密码至少需要 12 个字符'
          return
        }
        if (newPassword !== confirmPassword) {
          error = '两次输入的新密码不一致'
          return
        }
        const dto = await authApi.changePassword(newPassword, { signal: controller.signal })
        applySession(authSessionFromDTO(dto))
        navigateToLoginReturnTo(returnTo)
        return
      }
      const dto = await loginWithPassword(username, password, { signal: controller.signal, mfaCode })
      if (dto.must_change_password) {
        applySession(authSessionFromDTO(dto))
        password = ''
        passwordChangeRequired = true
        return
      }
      applySession(authSessionFromDTO(dto))
      navigateToLoginReturnTo(returnTo)
    } catch (err) {
      if (isLoginTimeout(err)) {
        error = '登录请求超时，请重试'
      } else if (err instanceof ApiClientError && err.status === 401) {
        error = mfaRequired ? '动态验证码不正确或已使用' : '用户名或密码不正确'
      } else if (err instanceof ApiClientError && err.status === 429) {
        error = '登录请求过于频繁，请稍后重试'
      } else if (err instanceof ApiClientError && err.status === 428) {
        mfaRequired = true
        mfaCode = ''
        error = '请输入动态验证码'
      } else if (err instanceof Error) {
        error = err.message
      } else {
        error = '登录失败'
      }
    } finally {
      clearPendingLogin()
      submitting = false
    }
  }

  function clearPendingLogin() {
    if (loginTimeout) {
      clearTimeout(loginTimeout)
      loginTimeout = undefined
    }
    loginAbortController = undefined
  }

  function isLoginTimeout(err: unknown) {
    return (
      err instanceof DOMException &&
      (err.name === 'AbortError' || err.name === 'TimeoutError') &&
      loginAbortController?.signal.aborted
    )
  }

  function providerBeginURL(provider: AuthProviderDTO) {
    const beginURL = provider.begin_url ?? `/api/auth/identity/${encodeURIComponent(provider.name)}/begin`
    const separator = beginURL.includes('?') ? '&' : '?'
    return `${beginURL}${separator}return_to=${encodeURIComponent(returnTo)}`
  }

  function beginProvider(provider: AuthProviderDTO) {
    navigateToIdentityLogin(providerBeginURL(provider))
  }

  function completeBrowserLogin(dto: LoginSessionDTO) {
    applySession(authSessionFromDTO(dto))
    if (dto.must_change_password) {
      passwordChangeRequired = true
      navigateToLoginReturnTo(passwordChangeURLWithReturnTo($page.url))
      return
    }
    navigateToLoginReturnTo(returnTo)
  }

  function loginServiceErrorMessage(err: unknown, fallback: string) {
    if (err instanceof ApiClientError && err.status === 404) {
      return '登录服务尚未连接，请确认后端服务已启动。'
    }
    if (err instanceof TypeError && /fetch|network/i.test(err.message)) {
      return '无法连接登录服务，请检查网络连接后重试'
    }
    return err instanceof Error ? err.message : fallback
  }

  async function loadLoginOptions() {
    authAbortController?.abort()
    const controller = new AbortController()
    authAbortController = controller
    providersLoading = true
    providerError = ''
    providersEndpointMissing = false
    flowError = ''
    flowLoading = Boolean(flowID)
    loginFlow = undefined
    try {
      const page = await authApi.listProviders({ signal: controller.signal })
      const enabledProviders = (page.providers ?? []).filter((provider) => provider.enabled)
      if (controller.signal.aborted) return
      providers = enabledProviders
      providersLoading = false

      if (passwordChangeRequired || passwordChangeRequested || session.mustChangePassword) return

      const enabledBrowserProviders = enabledProviders.filter((provider) => provider.kind === 'browser')
      const localEnabled = enabledProviders.some((provider) => provider.kind === 'local')
      if (flowID) {
        const kratos = enabledBrowserProviders.find((provider) => provider.name === 'kratos')
        if (!kratos) {
          flowError = 'Kratos 登录方式不可用'
          return
        }

        try {
          loginFlow = await kratosApi.loginFlow(kratos.name, flowID, { signal: controller.signal })
        } catch (err) {
          if (controller.signal.aborted) return
          if (err instanceof ApiClientError && ['flow_cookie_invalid', 'flow_not_found', 'flow_expired'].includes(err.code)) {
            prepareIdentityLoginRetryReturnTo(flowID, returnTo, window.sessionStorage)
            beginProvider(kratos)
            return
          }
          flowError = loginServiceErrorMessage(err, '登录流程加载失败')
        }
        return
      }

      const primaryBrowserProviders = enabledBrowserProviders.filter((provider) => provider.primary)
      if (!localEnabled && primaryBrowserProviders.length === 1) {
        beginProvider(primaryBrowserProviders[0])
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        providers = []
        providersEndpointMissing = err instanceof ApiClientError && err.status === 404
        providerError = loginServiceErrorMessage(err, '登录方式加载失败')
      }
    } finally {
      if (!controller.signal.aborted) {
        providersLoading = false
        flowLoading = false
      }
    }
  }

  onMount(() => {
    returnTo = identityLoginReturnToFromURL($page.url, window.sessionStorage)
    void loadLoginOptions()
    return () => authAbortController?.abort()
  })

  onDestroy(() => {
    authAbortController?.abort()
    loginAbortController?.abort()
    clearPendingLogin()
  })
</script>

<svelte:head>
  <title>登录 | Orrery</title>
</svelte:head>

<main class="flex min-h-full items-center justify-center bg-muted/40 p-4 text-foreground sm:p-6">
  <div class="grid w-full max-w-[960px] overflow-hidden rounded-2xl border border-border bg-card shadow-[0_12px_40px_rgba(15,23,42,0.08)] md:min-h-[640px] md:grid-cols-[46%_54%]">
    <section class="flex items-center px-6 py-9 sm:px-9 md:px-10 md:py-12">
      <div class="mx-auto w-full max-w-[320px]">
        <div class="flex items-center justify-between gap-3">
          <div class="flex min-w-0 items-center gap-2.5">
            <span class="text-sm font-semibold leading-tight text-foreground">Minvia 明微知径</span>
            <span class="h-4 w-px bg-border"></span>
            <span class="text-sm font-semibold text-foreground">Orrery</span>
          </div>
          <span class="hidden shrink-0 rounded-md bg-muted px-2 py-1 text-[11px] font-medium text-muted-foreground sm:inline-flex">Minvia Workspace</span>
        </div>

        <div class="mb-7 mt-9">
          <h1 class="text-2xl font-semibold text-foreground">安全登录</h1>
          <p class="mt-2 text-sm leading-6 text-muted-foreground">使用企业账号进入 Minvia 工作台。</p>
        </div>

        <div class="space-y-4">
          {#if loginFlow}
            <KratosLoginForm
              flow={loginFlow}
              providerName={kratosProvider?.display_name || 'Orrery SSO'}
              onAuthenticated={completeBrowserLogin}
            />
          {:else if flowLoading}
            <p class="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">正在加载登录流程</p>
          {:else}
            {#if !passwordChangeRequired && browserProviders.length}
            <div class="grid gap-2">
              {#each browserProviders as provider (provider.name)}
                <a
                  class="inline-flex h-10 w-full items-center justify-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20"
                  href={providerBeginURL(provider)}
                >
                  <LogIn class="h-4 w-4" />
                  <span>{provider.display_name || provider.name}</span>
                </a>
              {/each}
            </div>
            {/if}

            {#if !passwordChangeRequired && browserProviders.length && showLocalLogin}
            <div class="flex items-center gap-3 text-xs text-muted-foreground">
              <span class="h-px flex-1 bg-border"></span>
              <span>本地账号</span>
              <span class="h-px flex-1 bg-border"></span>
            </div>
            {/if}

            <form
              class="space-y-4"
              autocomplete="off"
              onsubmit={(event) => { event.preventDefault(); void submit() }}
            >
            {#if showLocalLogin}
            {#if passwordChangeRequired}
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-muted-foreground">新密码</span>
                <div class="flex h-10 items-center gap-2 rounded-md border border-input bg-input-background px-3 focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
                  <KeyRound class="h-4 w-4 shrink-0 text-muted-foreground" />
                  <input class="min-w-0 flex-1 bg-transparent text-sm outline-none" type="password" autocomplete="new-password" bind:value={newPassword} disabled={submitting} />
                </div>
              </label>
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-muted-foreground">确认新密码</span>
                <div class="flex h-10 items-center gap-2 rounded-md border border-input bg-input-background px-3 focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
                  <KeyRound class="h-4 w-4 shrink-0 text-muted-foreground" />
                  <input class="min-w-0 flex-1 bg-transparent text-sm outline-none" type="password" autocomplete="new-password" bind:value={confirmPassword} disabled={submitting} />
                </div>
              </label>
            {:else}
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-muted-foreground">用户名</span>
                <div class="flex h-10 items-center gap-2 rounded-md border border-input bg-input-background px-3 focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
                  <UserRound class="h-4 w-4 shrink-0 text-muted-foreground" />
                  <input
                    class="min-w-0 flex-1 bg-transparent text-sm outline-none"
                    name="orrery-local-user"
                    autocomplete="username"
                    autocapitalize="none"
                    spellcheck="false"
                    bind:value={username}
                    disabled={submitting}
                  />
                </div>
              </label>

              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-muted-foreground">密码</span>
                <div class="flex h-10 items-center gap-2 rounded-md border border-input bg-input-background px-3 focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
                  <KeyRound class="h-4 w-4 shrink-0 text-muted-foreground" />
                  <input
                    class="min-w-0 flex-1 bg-transparent text-sm outline-none"
                    type={showPassword ? 'text' : 'password'}
                    name="orrery-local-passphrase"
                    autocomplete="current-password"
                    bind:value={password}
                    disabled={submitting}
                  />
                  <button
                    class="grid h-7 w-7 shrink-0 place-items-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    type="button"
                    aria-label={showPassword ? '隐藏密码' : '显示密码'}
                    title={showPassword ? '隐藏密码' : '显示密码'}
                    onclick={() => (showPassword = !showPassword)}
                  >
                    {#if showPassword}<EyeOff class="h-4 w-4" />{:else}<Eye class="h-4 w-4" />{/if}
                  </button>
                </div>
              </label>

              {#if mfaRequired}
                <label class="block">
                  <span class="mb-1.5 block text-xs font-medium text-muted-foreground">动态验证码</span>
                  <div class="flex h-10 items-center gap-2 rounded-md border border-input bg-input-background px-3 focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
                    <ShieldCheck class="h-4 w-4 shrink-0 text-muted-foreground" />
                    <input class="min-w-0 flex-1 bg-transparent text-sm outline-none" name="orrery-local-mfa" inputmode="numeric" autocomplete="one-time-code" maxlength="6" bind:value={mfaCode} disabled={submitting} />
                  </div>
                </label>
              {/if}
            {/if}
            {:else if providersLoading}
            <p class="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">正在加载登录方式</p>
            {:else if !browserProviders.length}
            <p class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">当前没有可用登录方式</p>
            {/if}

            {#if error}
            <p class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
            {/if}

            {#if showLocalLogin}
            <button
              class="inline-flex h-10 w-full items-center justify-center gap-2 rounded-md bg-brand px-4 text-sm font-medium text-brand-foreground transition-colors hover:bg-brand/90 disabled:cursor-not-allowed disabled:opacity-60"
              type="submit"
              disabled={submitting || (passwordChangeRequired ? !newPassword || !confirmPassword : !username.trim() || !password)}
            >
              <LogIn class="h-4 w-4" />
              <span>{submitting ? '处理中' : passwordChangeRequired ? '更新密码并登录' : '登录'}</span>
            </button>
            {/if}
            </form>

            {#if providerError || flowError}
              <p class="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs leading-5 text-amber-700">{flowError || providerError}</p>
              <button
                class="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md border border-border bg-background px-3 text-sm font-medium text-foreground hover:bg-muted"
                type="button"
                onclick={() => void loadLoginOptions()}
              >
                <RefreshCw class="h-4 w-4" />
                <span>重试</span>
              </button>
            {/if}
          {/if}
        </div>

        {#if showLocalLogin}
          <div class="mt-6 border-t border-border pt-4 text-xs leading-5 text-muted-foreground">
            管理员账号由部署配置提供，首次登录后请及时更新凭据。
          </div>
        {/if}
      </div>
    </section>

    <section class="relative hidden min-h-[640px] overflow-hidden bg-neutral-900 md:block">
      <div class="login-art absolute inset-0" aria-hidden="true">
        <span class="login-orbit orbit-outer"></span>
        <span class="login-orbit orbit-middle"></span>
        <span class="login-orbit orbit-inner"></span>
        <span class="login-orbit-center"></span>
      </div>
      <div class="absolute inset-x-8 bottom-8 text-white [text-shadow:0_1px_12px_rgba(0,0,0,0.8)]">
        <div class="mb-3 flex items-center gap-2 text-xs font-medium">
          <ShieldCheck class="h-4 w-4" />
          <span>受控访问</span>
          <span aria-hidden="true">/</span>
          <span>统一运营入口</span>
        </div>
        <h2 class="text-2xl font-semibold">企业智能运营工作台</h2>
        <p class="mt-2 max-w-md text-sm leading-6 text-white/85">为团队、Agent 与资源协作提供一个清晰稳定的入口。</p>
      </div>
    </section>
  </div>
</main>
