<script lang="ts">
  import { Bell, ChevronDown, ChevronRight, Globe2, LogOut, Menu, Moon, PanelRightClose, PanelRightOpen, Settings, Sun, UserRound } from 'lucide-svelte'
  import { page } from '$app/stores'
  import { goto } from '$app/navigation'
  import { authApi } from '#lib/api/auth'
  import { createTranslator, locale, setLocale, type Locale } from '#lib/i18n'
  import { clearSessionQueryState } from '#lib/query-client'
  import { useWorkActionSources, workActionApprovals } from '#lib/queries/work-actions'
  import { buildWorkActions } from '#lib/components/workhub/work-actions'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useTaskNotificationSummary } from '#lib/queries/task-notifications'
  import { hasRouteAccess, pageForPath } from '#lib/routes/navigation'
  import { navigateToIdentityLogout } from '#lib/routes/identityLoginNavigation'
  import { clearSession, session } from '#lib/stores/session.svelte'
  import { theme, toggleThemeMode } from '#lib/stores/theme.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'

  interface Props {
    onMenuToggle?: () => void
    contextOpen?: boolean
    onContextToggle?: () => void
  }

  let { onMenuToggle = () => {}, contextOpen = true, onContextToggle = () => {} }: Props = $props()
  let t = $derived(createTranslator($locale))

  let showUserMenu = $state(false)
  let userMenuRef: HTMLDivElement | undefined = $state()

  const themeToggleLabel = $derived(theme.mode === 'dark' ? t('topbar.theme.light') : t('topbar.theme.dark'))
  const userLabel = $derived(session.displayName || session.username || session.actorId)
  const canAccessSettings = $derived(hasRouteAccess('/settings', session.permissionScope))
  const workAccess = $derived({ todos: session.authenticated && hasPermissionScope(session.permissionScope, 'todo.read'), approvals: session.authenticated && hasPermissionScope(session.permissionScope, 'approvals.read'), scheduler: session.authenticated && hasPermissionScope(session.permissionScope, 'scheduler.read'), identity: `${session.user.userId}:${session.viewerKind}:${session.securityContextVersion}` })
  const workSources = useWorkActionSources(() => workAccess)
  const workQueries = $derived([...(workAccess.todos ? [workSources.todos] : []), ...(workAccess.approvals ? [workSources.business, workSources.manual, workSources.fixed, workSources.councils] : []), ...(workAccess.scheduler ? [workSources.results] : [])])
  const incomplete = $derived(workQueries.some(query => query.isPending || query.isError))
  const taskNotifications = useTaskNotificationSummary()
  const notificationCount = $derived(buildWorkActions(workAccess.todos ? workSources.todos.data ?? [] : [], workAccess.approvals ? workActionApprovals(workSources) : [], workAccess.approvals ? workSources.councils.data ?? [] : [], workAccess.scheduler ? workSources.results.data ?? [] : []).filter(item => item.pending).length)
  const notificationHref = '/work-items'
  const notificationLabel = $derived($locale.startsWith('zh') ? `需我处理 ${notificationCount}${incomplete ? '+' : ''} 项，未读动态 ${taskNotifications.data?.unread ?? 0} 条` : `${notificationCount}${incomplete ? '+' : ''} pending items, ${taskNotifications.data?.unread ?? 0} unread updates`)

  const activePage = $derived(pageForPath($page.url.pathname))
  const kratosLoginAfterLogout = '/api/auth/identity/kratos/begin?return_to=/'

  function toggleUserMenu() {
    showUserMenu = !showUserMenu
  }

  async function openProfile() {
    showUserMenu = false
    await goto('/profile')
  }

  async function openSettings() {
    showUserMenu = false
    await goto('/settings')
  }

  async function logout() {
    const subjectProvider = session.subjectProvider

    if (subjectProvider === 'kratos') {
      clearSessionQueryState()
    } else {
      clearSession()
    }
    showUserMenu = false
    try {
      await authApi.logout()
    } catch {
      // The local security context is already closed even if the server session expired first.
    } finally {
      if (subjectProvider === 'kratos') {
        navigateToIdentityLogout(
          `/api/auth/identity/kratos/logout?return_to=${encodeURIComponent(kratosLoginAfterLogout)}`
        )
        return
      }
      await goto('/login')
    }
  }

  $effect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (userMenuRef && !userMenuRef.contains(e.target as Node)) {
        showUserMenu = false
      }
    }
    document.addEventListener('click', handleClickOutside)
    return () => document.removeEventListener('click', handleClickOutside)
  })
</script>

<header class="flex h-12 shrink-0 items-center justify-between border-b border-border bg-card px-3 sm:px-5">
  <div class="flex min-w-0 items-center gap-2">
    <button
      class="grid h-8 w-8 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground lg:hidden"
      type="button"
      aria-label={t('sidebar.open')}
      title={t('sidebar.open')}
      onclick={onMenuToggle}
    >
      <Menu class="h-4 w-4" />
    </button>
    <div class="hidden min-w-0 items-center gap-1.5 text-[13px] sm:flex">
      <a class="text-muted-foreground transition-colors hover:text-foreground" href="/">Orrery</a>
      <ChevronRight class="h-3.5 w-3.5 shrink-0 text-border" />
      <span class="truncate font-medium text-foreground">{t(activePage.title)}</span>
    </div>
  </div>

  <div class="flex items-center gap-1">
    <label class="relative hidden h-7 items-center sm:flex">
      <span class="sr-only">{t('topbar.language')}</span>
      <Globe2 class="pointer-events-none absolute left-2 h-3.5 w-3.5 text-muted-foreground" />
      <select
        id="language-select"
        class="h-7 appearance-none rounded-lg border-0 bg-transparent py-0 pl-7 pr-2 text-[12.5px] text-muted-foreground outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring"
        value={$locale}
        aria-label={t('topbar.language')}
        onchange={(event) => setLocale((event.currentTarget as HTMLSelectElement).value as Locale)}
      >
        <option value="zh-CN">{t('topbar.language.zh')}</option>
        <option value="en-US">{t('topbar.language.en')}</option>
      </select>
    </label>

    <button
      class="grid h-7 w-7 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      type="button"
      aria-label={themeToggleLabel}
      title={themeToggleLabel}
      onclick={toggleThemeMode}
    >
      {#if theme.mode === 'dark'}
        <Sun class="h-4 w-4" />
      {:else}
        <Moon class="h-4 w-4" />
      {/if}
    </button>

      <!-- Notifications -->
      <a
        href={notificationHref}
        class="relative grid h-7 w-7 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        aria-label={notificationLabel}
        title={notificationLabel}
      >
        <Bell class="h-4 w-4" />
        {#if notificationCount > 0 || incomplete}
          <span class="absolute -right-1 -top-1 grid min-h-3.5 min-w-3.5 place-items-center rounded-full bg-rose-600 px-0.5 text-[8px] font-semibold leading-3.5 text-white">{notificationCount > 99 ? '99+' : `${notificationCount}${incomplete ? '+' : ''}`}</span>
        {/if}
        {#if (taskNotifications.data?.unread ?? 0) > 0}<span class="absolute bottom-0 right-0 h-1.5 w-1.5 rounded-full bg-blue-600" aria-label={$locale.startsWith('zh') ? '有未读动态' : 'Unread updates'}></span>{/if}
      </a>

    <button
      class="grid h-7 w-7 place-items-center rounded-lg transition-colors {contextOpen
        ? 'bg-muted text-foreground'
        : 'text-muted-foreground hover:bg-muted hover:text-foreground'}"
      type="button"
      aria-label={t('context.toggle')}
      title={t('context.toggle')}
      aria-pressed={contextOpen}
      onclick={onContextToggle}
    >
      {#if contextOpen}<PanelRightClose class="h-4 w-4" />{:else}<PanelRightOpen class="h-4 w-4" />{/if}
    </button>

    <!-- User dropdown -->
    <div class="relative ml-1" bind:this={userMenuRef}>
      <button
        class="flex h-7 items-center gap-1 rounded-lg px-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        type="button"
        aria-label={t('topbar.userMenu')}
        onclick={toggleUserMenu}
      >
        <WorkforceAvatar profileId={session.user.agentProfileId} name={userLabel} human class="h-6 w-6 rounded-full" textClass="text-xs" />
        <ChevronDown class="h-3 w-3" />
      </button>
      {#if showUserMenu}
        <div class="absolute right-0 top-full z-20 mt-1 w-56 rounded-lg border border-border bg-popover p-1 shadow-lg">
          <div class="px-2 py-2">
            <p class="truncate text-sm font-medium text-popover-foreground">{userLabel}</p>
            <p class="truncate text-xs text-muted-foreground">{session.username}</p>
          </div>
          <button
            class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-popover-foreground transition-colors hover:bg-muted"
            type="button"
            onclick={() => { void openProfile() }}
          >
            <UserRound class="h-4 w-4" />
            <span>{t('topbar.profile')}</span>
          </button>
          {#if canAccessSettings}
            <button
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-popover-foreground transition-colors hover:bg-muted"
              type="button"
              onclick={() => { void openSettings() }}
            >
              <Settings class="h-4 w-4" />
              <span>{t('topbar.settings')}</span>
            </button>
          {/if}
          <button
            class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-popover-foreground transition-colors hover:bg-muted"
            type="button"
            onclick={() => { void logout() }}
          >
            <LogOut class="h-4 w-4" />
            <span>{t('topbar.logout')}</span>
          </button>
        </div>
      {/if}
    </div>
  </div>
</header>
