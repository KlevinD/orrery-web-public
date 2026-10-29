<script lang="ts">
  import { Plus, Search } from 'lucide-svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { visibleNavGroups } from '#lib/routes/navigation'
  import { createTranslator, locale } from '#lib/i18n'
  import { session } from '#lib/stores/session.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'

  interface Props {
    currentPath?: string
    onNavigate?: () => void
    mode?: 'desktop' | 'mobile'
  }

  let { currentPath = '/discussions', onNavigate = () => {}, mode = 'desktop' }: Props = $props()
  let t = $derived(createTranslator($locale))
  let searchQuery = $state('')
  let searchInput: HTMLInputElement | undefined = $state()
  let userLabel = $derived(session.displayName || session.username || session.actorId)
  let canCreateTodo = $derived(
    hasPermissionScope(session.permissionScope, 'todo.write') && hasPermissionScope(session.permissionScope, 'todo.read')
  )
  const routeOwnerPath = $derived(primaryNavigationPath(currentPath))
  const activeNavPath = $derived(
    visibleNavGroups(session.permissionScope)
      .flatMap((group) => group.items)
      .map((item) => item.path)
      .filter((path) => routeOwnerPath === path || (path !== '/' && routeOwnerPath.startsWith(`${path}/`)))
      .sort((left, right) => right.length - left.length)[0]
  )

  const filteredGroups = $derived.by(() => {
    const query = searchQuery.trim().toLocaleLowerCase($locale)
    return visibleNavGroups(session.permissionScope)
      .map((group) => ({
        ...group,
        items: group.items
          .filter((item) => {
            if (!query) return true
            return `${t(item.title)} ${t(item.description)}`.toLocaleLowerCase($locale).includes(query)
          })
      }))
      .filter((group) => group.items.length > 0)
  })

  function focusSearch(event: KeyboardEvent) {
    if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'k') return
    if (!searchInput || searchInput.getClientRects().length === 0) return
    event.preventDefault()
    searchInput.focus()
  }

  function revealActive(node: HTMLAnchorElement, active: boolean) {
    function reveal(next: boolean) {
      if (!next || typeof node.scrollIntoView !== 'function') return
      queueMicrotask(() => {
        if (!node.isConnected || node.getClientRects().length === 0) return
        node.scrollIntoView({ block: 'nearest' })
      })
    }

    reveal(active)
    return { update: reveal }
  }

  function primaryNavigationPath(pathname: string) {
    if (/^\/(todos|ideas|missions|approvals)(\/|$)/.test(pathname)) return '/work-items'
    return pathname
  }
</script>

<svelte:window onkeydown={focusSearch} />

<aside
  class="flex h-full w-[236px] max-w-[86vw] flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground {mode === 'mobile' ? 'shadow-2xl' : ''}"
  data-app-sidebar
>
  <div class="flex shrink-0 items-center px-4 pb-2 pt-4">
    <a
      href="/"
      class="flex min-w-0 items-center gap-2.5 rounded-md focus-visible:ring-2 focus-visible:ring-sidebar-ring"
      aria-label="Orrery"
      onclick={onNavigate}
    >
      <span class="grid h-7 w-7 shrink-0 place-items-center rounded-md bg-brand text-xs font-semibold text-white">ID</span>
      <span class="min-w-0 leading-tight">
        <span class="block truncate text-sm font-medium text-sidebar-foreground">Orrery</span>
        <span class="block truncate text-xs text-muted-foreground">Minvia</span>
      </span>
    </a>
  </div>

  <div class="space-y-2 px-3 pb-2 pt-2">
    {#if canCreateTodo}
      <a
        href="/?tab=team&create=task"
        class="flex h-9 w-full items-center gap-2.5 rounded-lg bg-brand px-3 text-[13px] font-medium text-white transition-colors hover:bg-brand-hover"
        aria-label={t('sidebar.newTask')}
        onclick={onNavigate}
      >
        <Plus class="h-4 w-4 shrink-0" />
        <span class="truncate">{t('sidebar.newTask')}</span>
      </a>
    {/if}

      <label class="relative block">
        <span class="sr-only">{t('sidebar.search')}</span>
        <Search class="pointer-events-none absolute left-2.5 top-[9px] h-3.5 w-3.5 text-muted-foreground" />
        <input
          bind:this={searchInput}
          bind:value={searchQuery}
          class="h-8 w-full rounded-lg border border-border bg-input-background pl-8 pr-12 text-[13px] text-foreground outline-none placeholder:text-muted-foreground focus-visible:border-ring"
          placeholder={t('sidebar.search')}
          aria-label={t('sidebar.search')}
          type="search"
        />
        <kbd class="pointer-events-none absolute right-2 top-1.5 rounded border border-border bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">Ctrl K</kbd>
      </label>
  </div>

  <nav class="subtle-scrollbar flex-1 overflow-y-auto px-2 pb-3">
    {#each filteredGroups as group, groupIndex}
      {#if groupIndex > 0}
        <div class="px-2.5 pb-1 pt-3 text-[11px] font-medium uppercase tracking-normal text-muted-foreground">
          {t(group.title)}
        </div>
      {:else}
        <div class="px-2.5 pb-1 pt-1 text-[11px] font-medium uppercase tracking-normal text-muted-foreground">
          {t(group.title)}
        </div>
      {/if}

      <div class="space-y-0.5">
        {#each group.items as item}
          {@const Icon = item.icon}
          {@const isActive = activeNavPath === item.path}
          <a
            class="relative flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px] transition-colors {isActive
              ? 'selection-wash font-medium text-sidebar-accent-foreground'
              : 'text-sidebar-foreground/80 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground'}"
            href={item.path}
            aria-current={isActive ? 'page' : undefined}
            onclick={onNavigate}
            use:revealActive={isActive}
          >
            <Icon class="h-4 w-4 shrink-0 {isActive ? 'text-sidebar-accent-foreground' : 'text-sidebar-foreground/70'}" />
            <span class="truncate">{t(item.title)}</span>
          </a>
        {/each}
      </div>
    {/each}
    {#if searchQuery.trim() && filteredGroups.length === 0}
      <p class="px-3 py-8 text-center text-xs text-muted-foreground">{t('sidebar.noResults')}</p>
    {/if}
  </nav>

  <div class="border-t border-sidebar-border p-2">
    <div class="flex min-w-0 items-center gap-2.5 rounded-md px-2 py-1.5">
      <WorkforceAvatar profileId={session.user.agentProfileId} name={userLabel} human class="h-7 w-7 rounded-full" textClass="text-xs" />
      <span class="min-w-0 leading-tight">
        <span class="block truncate text-sm font-medium text-sidebar-foreground">{userLabel}</span>
        <span class="block truncate text-xs text-muted-foreground">{session.username}</span>
      </span>
    </div>
  </div>
</aside>
