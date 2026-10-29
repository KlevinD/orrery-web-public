<script lang="ts">
  import { CheckCircle2, Loader2, ShieldCheck, XCircle } from 'lucide-svelte'
  import { page } from '$app/stores'
  import { ApiClientError } from '#lib/api/client'
  import { oauth2Api, type OAuth2ConsentPreviewDTO } from '#lib/api/oauth2'
  import { onDestroy, onMount } from 'svelte'

  let consent = $state<OAuth2ConsentPreviewDTO>()
  let loading = $state(true)
  let submitting = $state<'accept' | 'reject' | ''>('')
  let error = $state('')
  let controller: AbortController | undefined
  const challenge = $derived(($page.url.searchParams.get('consent_challenge') ?? '').trim())
  const clientName = $derived(consent?.client.display_name || consent?.client.client_id || 'OAuth2 Client')
  const grantScopes = $derived(consent?.grant_scopes ?? [])
  const grantAudience = $derived(consent?.grant_audience ?? [])

  async function loadConsent() {
    error = ''
    consent = undefined
    if (!challenge) {
      loading = false
      error = '缺少 OAuth2 consent challenge'
      return
    }
    controller?.abort()
    const currentController = new AbortController()
    controller = currentController
    loading = true
    try {
      consent = await oauth2Api.pendingConsent(challenge, { signal: currentController.signal })
    } catch (err) {
      if (!currentController.signal.aborted) {
        error = apiErrorMessage(err, '同意请求加载失败')
      }
    } finally {
      if (!currentController.signal.aborted) {
        loading = false
      }
    }
  }

  async function decide(next: 'accept' | 'reject') {
    if (!challenge || submitting) return
    error = ''
    submitting = next
    try {
      const redirect = next === 'accept' ? await oauth2Api.acceptConsent(challenge) : await oauth2Api.rejectConsent(challenge)
      if (!redirect.redirect_to) {
        error = 'OAuth2 redirect 为空'
        return
      }
      window.location.assign(redirect.redirect_to)
    } catch (err) {
      error = apiErrorMessage(err, next === 'accept' ? '授权失败' : '拒绝授权失败')
    } finally {
      submitting = ''
    }
  }

  function apiErrorMessage(err: unknown, fallback: string) {
    if (err instanceof ApiClientError) return err.message
    if (err instanceof Error) return err.message
    return fallback
  }

  function scopeLabel(scope: string) {
    if (scope.startsWith('orrery:')) return scope.replace('orrery:', '')
    return scope
  }

  onMount(() => {
    void loadConsent()
  })

  onDestroy(() => {
    controller?.abort()
  })
</script>

<svelte:head>
  <title>OAuth2 授权 | Orrery</title>
</svelte:head>

<main class="flex min-h-full items-center justify-center bg-muted/40 p-4 text-foreground sm:p-6">
  <section class="w-full max-w-2xl">
    <div class="w-full overflow-hidden rounded-2xl border border-border bg-card shadow-[0_12px_40px_rgba(15,23,42,0.08)]">
      <header class="border-b border-border px-6 py-5 md:px-7">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <span class="inline-flex h-10 w-10 items-center justify-center rounded-md bg-brand/10 text-brand">
              <ShieldCheck class="h-5 w-5" />
            </span>
            <div>
              <p class="text-xs font-medium text-muted-foreground">Orrery OAuth2</p>
              <h1 class="mt-1 text-[22px] font-semibold leading-[33px] text-foreground">确认外部应用访问</h1>
            </div>
          </div>
          <span class="text-sm font-semibold text-foreground">Minvia 明微知径</span>
        </div>
      </header>

      <div class="px-6 py-6 md:px-7">
        {#if loading}
          <div class="flex min-h-48 items-center justify-center gap-3 text-sm text-muted-foreground">
            <Loader2 class="h-5 w-5 animate-spin" />
            <span>正在读取授权请求</span>
          </div>
        {:else if error}
          <div class="rounded-md border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        {:else if consent}
          <div class="space-y-6">
            <div>
              <p class="text-sm text-muted-foreground">应用</p>
              <p class="mt-1 break-words text-xl font-semibold text-foreground">{clientName}</p>
              <div class="mt-3 flex flex-wrap gap-2">
                <span class="rounded-md border border-border bg-muted/40 px-2.5 py-1 text-xs font-medium text-foreground">
                  {consent.client.kind || 'client'}
                </span>
                <span class="rounded-md border border-border bg-muted/40 px-2.5 py-1 text-xs font-medium text-foreground">
                  {consent.client.client_id}
                </span>
              </div>
            </div>

            <div class="grid gap-4 md:grid-cols-2">
              <section class="rounded-md border border-border">
                <div class="border-b border-border bg-muted/40 px-4 py-3 text-sm font-semibold text-foreground">允许的权限</div>
                <div class="divide-y divide-border">
                  {#each grantScopes as scope (scope)}
                    <div class="flex items-center justify-between gap-3 px-4 py-3 text-sm">
                      <span class="break-words text-foreground">{scopeLabel(scope)}</span>
                      <span class="shrink-0 rounded-md bg-brand/10 px-2 py-1 text-xs font-medium text-brand">scope</span>
                    </div>
                  {:else}
                    <div class="px-4 py-3 text-sm text-muted-foreground">无可授予权限</div>
                  {/each}
                </div>
              </section>

              <section class="rounded-md border border-border">
                <div class="border-b border-border bg-muted/40 px-4 py-3 text-sm font-semibold text-foreground">访问对象</div>
                <div class="divide-y divide-border">
                  {#each grantAudience as audience (audience)}
                    <div class="flex items-center justify-between gap-3 px-4 py-3 text-sm">
                      <span class="break-words text-foreground">{audience}</span>
                      <span class="shrink-0 rounded-md bg-amber-500/10 px-2 py-1 text-xs font-medium text-amber-700">audience</span>
                    </div>
                  {:else}
                    <div class="px-4 py-3 text-sm text-muted-foreground">默认 API 访问范围</div>
                  {/each}
                </div>
              </section>
            </div>

            <div class="flex flex-col-reverse gap-3 border-t border-border pt-5 sm:flex-row sm:justify-end">
              <button
                class="inline-flex h-10 items-center justify-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-60"
                type="button"
                disabled={Boolean(submitting)}
                onclick={() => void decide('reject')}
              >
                {#if submitting === 'reject'}
                  <Loader2 class="h-4 w-4 animate-spin" />
                {:else}
                  <XCircle class="h-4 w-4" />
                {/if}
                <span>拒绝</span>
              </button>
              <button
                class="inline-flex h-10 items-center justify-center gap-2 rounded-md bg-brand px-4 text-sm font-medium text-brand-foreground transition-colors hover:bg-brand/90 disabled:cursor-not-allowed disabled:opacity-60"
                type="button"
                disabled={Boolean(submitting) || grantScopes.length === 0}
                onclick={() => void decide('accept')}
              >
                {#if submitting === 'accept'}
                  <Loader2 class="h-4 w-4 animate-spin" />
                {:else}
                  <CheckCircle2 class="h-4 w-4" />
                {/if}
                <span>同意授权</span>
              </button>
            </div>
          </div>
        {/if}
      </div>
    </div>
  </section>
</main>
