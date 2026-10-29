<script lang="ts">
  import { ArrowLeft, FolderGit2, KeyRound, Plus, RefreshCw, RotateCcw, ShieldOff } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useSCMConnectionListQuery, useSCMConnectionMutations, useSCMConnectionRepositoriesQuery } from '#lib/queries/development-projects'
  import { session } from '#lib/stores/session.svelte'
  import type { SCMConnectionDTO } from '#lib/types/development-project'

  let t = $derived(createTranslator($locale))
  let status = $state('')
  let showCreate = $state(false)
  let rotateConnection = $state<SCMConnectionDTO | undefined>()
  let selectedConnectionId = $state('')
  let localPath = $state('')
  let allowedPatterns = $state('')
  let secretRef = $state('')
  let actionError = $state('')
  let actionMessage = $state('')
  const connectionsQuery = useSCMConnectionListQuery(() => ({ status, limit: 100, offset: 0 }))
  const repositoryQuery = useSCMConnectionRepositoriesQuery(() => selectedConnectionId)
  const mutations = useSCMConnectionMutations()
  const connections = $derived(connectionsQuery.data?.items ?? [])
  const canCreateManagedLocal = $derived(
    hasPermissionScope(session.permissionScope, 'connection.manage') && hasPermissionScope(session.permissionScope, 'settings.manage')
  )
  const busy = $derived(mutations.create.isPending || mutations.verify.isPending || mutations.rotate.isPending || mutations.revoke.isPending)

  function submitCreate() {
    if (!localPath.trim()) return
    resetAction()
    mutations.create.mutate({
      scope: 'platform', provider: 'managed_local', base_url: localPath.trim(),
      allowed_repository_patterns: values(allowedPatterns)
    }, callbacks(() => {
      showCreate = false
      localPath = ''
      allowedPatterns = ''
    }))
  }

  function verify(connection: SCMConnectionDTO) {
    resetAction()
    mutations.verify.mutate({ connectionId: connection.connection_id, version: connection.version }, callbacks())
  }

  function revoke(connection: SCMConnectionDTO) {
    if (!confirm(`${t('development.revoke')}: ${connection.connection_id}?`)) return
    resetAction()
    mutations.revoke.mutate({ connectionId: connection.connection_id, version: connection.version }, callbacks())
  }

  function openRotate(connection: SCMConnectionDTO) {
    rotateConnection = connection
    secretRef = ''
    resetAction()
  }

  function submitRotate() {
    if (!rotateConnection || !secretRef.trim()) return
    mutations.rotate.mutate({ connectionId: rotateConnection.connection_id, version: rotateConnection.version, secretRef: secretRef.trim() }, callbacks(() => { rotateConnection = undefined }))
  }

  function callbacks(after?: () => void) {
    return {
      onSuccess: () => { actionMessage = t('development.actionSucceeded'); after?.() },
      onError: (error: Error) => { actionError = error.message }
    }
  }

  function resetAction() { actionError = ''; actionMessage = '' }
  function values(value: string) { return value.split(/\r?\n|,/).map((item) => item.trim()).filter(Boolean) }
  function formatDate(value?: string) { return value ? new Intl.DateTimeFormat($locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '-' }
</script>

<section class="page-shell max-w-[920px] space-y-5">
  <a href="/settings" class="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft class="h-4 w-4" />{t('development.back')}</a>
  <header class="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-center sm:justify-between">
    <div class="flex items-center gap-3"><div class="flex h-9 w-9 items-center justify-center rounded-md border border-border bg-card"><FolderGit2 class="h-4 w-4" /></div><h1 class="page-title">{t('development.scmTitle')}</h1></div>
    {#if canCreateManagedLocal}<Button onclick={() => { resetAction(); showCreate = true }}><Plus class="h-4 w-4" />{t('development.newConnection')}</Button>{/if}
  </header>

  {#if actionError}<ErrorState message={actionError} />{:else if actionMessage}<div class="rounded-md border border-status-done/30 bg-status-done-bg px-4 py-3 text-sm text-status-done">{actionMessage}</div>{/if}

  <div class="max-w-xs"><Select bind:value={status} options={[{ value: '', label: t('development.allStatuses') }, { value: 'active', label: t('development.active') }, { value: 'error', label: 'Error' }, { value: 'revoked', label: 'Revoked' }, { value: 'expired', label: 'Expired' }]} /></div>

  {#if connectionsQuery.isPending}
    <LoadingState />
  {:else if connectionsQuery.error}
    <ErrorState message={connectionsQuery.error.message} />
  {:else if connections.length === 0}
    <EmptyState title={t('development.noConnections')} description={t('development.noConnectionsDescription')} />
  {:else}
    <div class="space-y-2">
      {#each connections as connection (connection.connection_id)}
        <article class="rounded-xl border border-border bg-card p-4">
          <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2"><h2 class="break-all font-mono text-sm font-semibold">{connection.connection_id}</h2><Badge tone={connection.status === 'active' ? 'success' : connection.status === 'error' ? 'danger' : 'neutral'}>{connection.status}</Badge><Badge tone="info">{connection.provider}</Badge></div>
              <dl class="mt-3 grid gap-3 text-xs sm:grid-cols-3"><div><dt class="text-muted-foreground">{t('development.connectionScope')}</dt><dd class="mt-1">{connection.scope}</dd></div><div><dt class="text-muted-foreground">{t('development.owner')}</dt><dd class="mt-1 break-all font-mono">{connection.owner_id || '-'}</dd></div><div><dt class="text-muted-foreground">{t('development.lastVerified')}</dt><dd class="mt-1">{formatDate(connection.last_verified_at)}</dd></div></dl>
              {#if connection.allowed_repository_patterns?.length}<p class="mt-3 break-all font-mono text-xs text-muted-foreground">{connection.allowed_repository_patterns.join(', ')}</p>{/if}
            </div>
            <div class="flex flex-wrap gap-1">
              {#if connection.capabilities.can_use && connection.status === 'active'}<Button size="icon" variant="ghost" title={t('development.repositories')} onclick={() => (selectedConnectionId = selectedConnectionId === connection.connection_id ? '' : connection.connection_id)}><FolderGit2 class="h-4 w-4" /></Button>{/if}
              {#if connection.capabilities.can_verify}<Button size="icon" variant="ghost" title={t('development.verify')} onclick={() => verify(connection)} disabled={busy}><RefreshCw class="h-4 w-4" /></Button>{/if}
              {#if connection.capabilities.can_rotate && connection.provider !== 'managed_local'}<Button size="icon" variant="ghost" title={t('development.rotateSecret')} onclick={() => openRotate(connection)}><RotateCcw class="h-4 w-4" /></Button>{/if}
              {#if connection.capabilities.can_revoke && connection.status !== 'revoked'}<Button size="icon" variant="ghost" title={t('development.revoke')} onclick={() => revoke(connection)} disabled={busy}><ShieldOff class="h-4 w-4" /></Button>{/if}
            </div>
          </div>
          {#if selectedConnectionId === connection.connection_id}
            <div class="mt-4 border-t border-border pt-4">{#if repositoryQuery.isPending}<LoadingState />{:else if repositoryQuery.error}<ErrorState message={repositoryQuery.error.message} />{:else}<div class="grid gap-2 md:grid-cols-2">{#each repositoryQuery.data?.items ?? [] as repository (repository.repository_locator)}<div class="min-w-0 rounded-md border border-border px-3 py-2"><div class="break-all text-sm font-medium">{repository.display_name}</div><div class="mt-1 break-all font-mono text-xs text-muted-foreground">{repository.default_branch} · {repository.head_commit.slice(0, 12)}</div></div>{/each}</div>{/if}</div>
          {/if}
        </article>
      {/each}
    </div>
  {/if}
</section>

<Dialog open={showCreate} title={t('development.createConnection')} onClose={() => (showCreate = false)}>
  <form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); submitCreate() }}>
    <div class="grid grid-cols-2 gap-3"><div><div class="text-xs text-muted-foreground">{t('development.provider')}</div><div class="mt-1 text-sm font-medium">managed_local</div></div><div><div class="text-xs text-muted-foreground">{t('development.connectionScope')}</div><div class="mt-1 text-sm font-medium">platform</div></div></div>
    <label class="grid gap-1.5 text-sm font-medium">{t('development.localPath')}<Input bind:value={localPath} /></label>
    <label class="grid gap-1.5 text-sm font-medium">{t('development.repository')}<textarea bind:value={allowedPatterns} rows="4" class="rounded-md border border-border bg-input-background px-3 py-2 font-mono text-xs"></textarea></label>
    <div class="flex justify-end gap-2"><Button variant="outline" onclick={() => (showCreate = false)}>{t('development.cancel')}</Button><Button type="submit" disabled={busy || !localPath.trim()}><KeyRound class="h-4 w-4" />{t('development.create')}</Button></div>
  </form>
</Dialog>

<Dialog open={Boolean(rotateConnection)} title={t('development.rotateSecret')} onClose={() => (rotateConnection = undefined)}>
  <form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); submitRotate() }}><label class="grid gap-1.5 text-sm font-medium">{t('development.secretRef')}<Input bind:value={secretRef} /></label><div class="flex justify-end gap-2"><Button variant="outline" onclick={() => (rotateConnection = undefined)}>{t('development.cancel')}</Button><Button type="submit" disabled={busy || !secretRef.trim()}>{t('development.save')}</Button></div></form>
</Dialog>
