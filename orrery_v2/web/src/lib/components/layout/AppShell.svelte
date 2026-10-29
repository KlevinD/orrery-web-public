<script lang="ts">
  import { page } from '$app/stores'
  import { onMount } from 'svelte'
  import Sidebar from './Sidebar.svelte'
  import TopBar from './TopBar.svelte'
  import LazyWorkspaceContextPanel from './LazyWorkspaceContextPanel.svelte'
  import PermissionDeniedState from '#lib/components/ui/PermissionDeniedState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { hasRouteAccess } from '#lib/routes/navigation'
  import { session } from '#lib/stores/session.svelte'

  interface Props {
    children: import('svelte').Snippet
  }

  let { children }: Props = $props()
  let mobileSidebarOpen = $state(false)
  let desktopContextOpen = $state(false)
  let mobileContextOpen = $state(false)
  let wideContextViewport = $state(false)
  let discussionContextAutoOpened = $state(false)
  let lastPath = $state($page.url.pathname)
  let t = $derived(createTranslator($locale))
  let canAccessCurrentRoute = $derived(hasRouteAccess($page.url.pathname, session.permissionScope))
  let focusedWorkspaceRoute = $derived(
    $page.url.pathname === '/scheduled-tasks' ||
      /^\/resources\/knowledge\/(?!new(?:\/|$))[^/]+\/?$/.test($page.url.pathname)
  )
  let discussionWorkspaceRoute = $derived(/^\/discussions(?:\/[^/]+)?\/?$/.test($page.url.pathname))
  let contextOpen = $derived(
    focusedWorkspaceRoute ? mobileContextOpen : wideContextViewport ? desktopContextOpen : mobileContextOpen
  )
  let edgeToEdgeRoute = $derived(
    $page.url.pathname === '/' ||
      discussionWorkspaceRoute ||
      $page.url.pathname === '/approvals' ||
      focusedWorkspaceRoute
  )

  onMount(() => {
    const media = window.matchMedia('(min-width: 1280px)')
    const updateViewport = () => {
      wideContextViewport = media.matches
      if (media.matches) {
        mobileContextOpen = false
        if (discussionWorkspaceRoute && !discussionContextAutoOpened) {
          desktopContextOpen = true
          discussionContextAutoOpened = true
        }
      }
    }
    updateViewport()
    media.addEventListener('change', updateViewport)
    return () => media.removeEventListener('change', updateViewport)
  })

  $effect(() => {
    if ($page.url.pathname === lastPath) return
    lastPath = $page.url.pathname
    mobileSidebarOpen = false
    mobileContextOpen = false
  })

  $effect(() => {
    if (discussionWorkspaceRoute && wideContextViewport && !discussionContextAutoOpened) {
      desktopContextOpen = true
      discussionContextAutoOpened = true
    }
    if (!discussionWorkspaceRoute) discussionContextAutoOpened = false
  })
</script>

<svelte:window
  onkeydown={(event) => {
    if (event.key !== 'Escape') return
    mobileSidebarOpen = false
    mobileContextOpen = false
  }}
/>

<div class="flex h-full overflow-hidden bg-background text-foreground">
  <div class="hidden h-full lg:block">
    <Sidebar currentPath={$page.url.pathname} />
  </div>

  {#if mobileSidebarOpen}
    <button
      class="fixed inset-0 z-[60] bg-black/35 lg:hidden"
      type="button"
      aria-label={t('sidebar.close')}
      onclick={() => (mobileSidebarOpen = false)}
    ></button>
    <div class="fixed inset-y-0 left-0 z-[70] lg:hidden">
      <Sidebar
        mode="mobile"
        currentPath={$page.url.pathname}
        onNavigate={() => (mobileSidebarOpen = false)}
      />
    </div>
  {/if}

  <div class="flex min-w-0 flex-1 flex-col bg-background">
    <TopBar
      {contextOpen}
      onMenuToggle={() => (mobileSidebarOpen = true)}
      onContextToggle={() => {
        if (focusedWorkspaceRoute) mobileContextOpen = !mobileContextOpen
        else if (wideContextViewport) desktopContextOpen = !desktopContextOpen
        else mobileContextOpen = !mobileContextOpen
      }}
    />
    <main class="subtle-scrollbar min-h-0 flex-1 bg-background {discussionWorkspaceRoute ? 'overflow-hidden' : 'overflow-y-auto'}">
      {#if edgeToEdgeRoute}
        {#if canAccessCurrentRoute}
          {@render children()}
        {:else}
          <PermissionDeniedState />
        {/if}
      {:else}
        <div class="mx-auto w-full max-w-[1488px] px-3 py-4 sm:px-5 sm:py-5 lg:px-6 lg:py-6">
          {#if canAccessCurrentRoute}
            {@render children()}
          {:else}
            <PermissionDeniedState />
          {/if}
        </div>
      {/if}
    </main>
  </div>

  {#if wideContextViewport && desktopContextOpen && !focusedWorkspaceRoute}
    <div class="h-full">
      <LazyWorkspaceContextPanel />
    </div>
  {/if}

  {#if (!wideContextViewport || focusedWorkspaceRoute) && mobileContextOpen}
    <button
      class="fixed inset-0 z-[60] bg-black/35"
      type="button"
      aria-label={t('context.close')}
      onclick={() => (mobileContextOpen = false)}
    ></button>
    <div class="fixed inset-y-0 right-0 z-[70] shadow-2xl">
      <LazyWorkspaceContextPanel showClose onClose={() => (mobileContextOpen = false)} />
    </div>
  {/if}
</div>
