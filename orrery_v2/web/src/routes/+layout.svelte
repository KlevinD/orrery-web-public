<script lang="ts">
  import '../app.css'
  import AppShell from '#lib/components/layout/AppShell.svelte'
  import ComplianceFooter from '#lib/components/layout/ComplianceFooter.svelte'
  import QueryProvider from '#lib/components/providers/QueryProvider.svelte'
  import { onMount } from 'svelte'
  import { bootstrapApp, type AppBootstrapResult } from '#lib/runtime/bootstrap'
  import { installClientErrorReporting } from '#lib/runtime/error-reporting'
  import { page } from '$app/stores'
  import { goto } from '$app/navigation'
  import { applySession, clearSession, hasSession, session } from '#lib/stores/session.svelte'
  import { setUnauthorizedHandler } from '#lib/api/client'
  import { authApi, authSessionFromDTO } from '#lib/api/auth'
  import { navigateToLoginReturnTo } from '#lib/routes/identityLoginNavigation'
  import { loginURLWithReturnTo, passwordChangeURLWithReturnTo, safeReturnToFromURL } from '#lib/routes/returnTo'

  interface Props {
    children: import('svelte').Snippet
  }

  let { children }: Props = $props()
  let bootstrap = $state<AppBootstrapResult | undefined>()
  let bootstrapError = $state('')
  const isLoginPage = $derived($page.url.pathname === '/login')
  const isOAuth2ConsentPage = $derived($page.url.pathname === '/oauth2/consent')
  const isStandaloneAuthPage = $derived(isLoginPage || isOAuth2ConsentPage)
  const sessionRenderKey = $derived(
    isStandaloneAuthPage
      ? `auth:${$page.url.pathname}:${session.securityContextVersion}`
      : session.authenticated
        ? `app:${session.securityContextVersion}`
        : `blank:${session.securityContextVersion}`
  )

  onMount(() => {
    const stopErrorReporting = installClientErrorReporting()
    setUnauthorizedHandler(() => {
      clearSession()
      if ($page.url.pathname !== '/login') {
        void goto(loginURLWithReturnTo($page.url))
      }
    })
    clearSession()
    bootstrapApp()
      .then(async (result) => {
        let dto = await authApi.currentSession()
        if (dto) {
          if (dto.rotation_required) dto = await authApi.rotateSession()
          applySession(authSessionFromDTO(dto))
        }
        bootstrap = result
        if (session.mustChangePassword && !isLoginPage) {
          void goto(passwordChangeURLWithReturnTo($page.url))
        } else if (!hasSession() && !isLoginPage) {
          void goto(loginURLWithReturnTo($page.url))
        }
      })
      .catch((error) => {
        bootstrapError = error instanceof Error ? error.message : 'Application startup failed.'
      })

    return () => {
      stopErrorReporting()
      setUnauthorizedHandler(undefined)
    }
  })

  $effect(() => {
    if (!bootstrap) return
    if (session.mustChangePassword) {
      if (!isLoginPage || $page.url.searchParams.get('change_password') !== '1') {
        void goto(passwordChangeURLWithReturnTo($page.url))
      }
    } else if (!session.authenticated && !isLoginPage) {
      void goto(loginURLWithReturnTo($page.url))
    }
    if (session.authenticated && !session.mustChangePassword && isLoginPage) {
      navigateToLoginReturnTo(safeReturnToFromURL($page.url))
    }
  })
</script>

<div class="flex h-dvh flex-col overflow-hidden bg-background text-foreground">
  <div class="min-h-0 flex-1 overflow-y-auto">
    {#if bootstrap}
      <QueryProvider>
        {#key sessionRenderKey}
          {#if isLoginPage && (!session.authenticated || session.mustChangePassword)}
            {@render children()}
          {:else if isOAuth2ConsentPage && session.authenticated && !session.mustChangePassword}
            {@render children()}
          {:else if !isStandaloneAuthPage && session.authenticated && !session.mustChangePassword}
            <AppShell>
              {@render children()}
            </AppShell>
          {/if}
        {/key}
      </QueryProvider>
    {:else if bootstrapError}
      <main class="flex min-h-full items-center justify-center bg-background p-6 text-foreground">
        <section class="w-full max-w-xl rounded-xl border border-destructive/30 bg-card p-6">
          <p class="text-sm font-medium text-destructive">Application startup failed</p>
          <p class="mt-2 text-sm text-muted-foreground">{bootstrapError}</p>
        </section>
      </main>
    {:else}
      <main class="flex min-h-full items-center justify-center bg-background p-6 text-foreground">
        <section class="w-full max-w-xl rounded-xl border border-border bg-card p-6">
          <p class="text-sm font-medium text-foreground">Starting Orrery</p>
          <p class="mt-2 text-sm text-muted-foreground">Preparing the local workspace.</p>
        </section>
      </main>
    {/if}
  </div>
  <ComplianceFooter />
</div>
