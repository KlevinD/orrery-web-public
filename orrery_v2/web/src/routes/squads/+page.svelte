<script lang="ts">
  import { Bot, ClipboardList, MessageSquare, Plus, Trash2, User, UserPlus } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { mapSquadDirectoryFromOrgGraphView } from '#lib/mappers/workforce'
  import { useOrgGraphQuery, useOrganizationUnitDeleteMutation, useWorkforceReleaseMutation } from '#lib/queries/workforce'
  import type { ApiClientError } from '#lib/api/client'
  import type { SquadMemberViewModel, SquadViewModel } from '#lib/types/view-models'

  const orgQuery = useOrgGraphQuery(() => ({ includeMembers: true }))
  const releaseMemberMutation = useWorkforceReleaseMutation()
  const deleteSquadMutation = useOrganizationUnitDeleteMutation()
  let t = $derived(createTranslator($locale))
  let squadView = $derived(orgQuery.data ? mapSquadDirectoryFromOrgGraphView(orgQuery.data) : undefined)
  let actionError = $state('')
  let busyMemberId = $state('')
  let busySquadId = $state('')
  let selectedSquadId = $state('')
  let activeSquad = $derived(
    squadView?.squads.find((squad) => squad.id === selectedSquadId) ?? squadView?.squads[0]
  )

  $effect(() => {
    const squads = squadView?.squads ?? []
    if (!squads.some((squad) => squad.id === selectedSquadId)) {
      selectedSquadId = squads[0]?.id ?? ''
    }
  })

  async function deleteMember(member: SquadMemberViewModel) {
    if (!member.profileId || releaseMemberMutation.isPending) return
    if (!confirm(t('squads.confirmDeleteMember', { name: member.name }))) return
    actionError = ''
    busyMemberId = member.profileId
    try {
      await releaseMemberMutation.mutateAsync(member.profileId)
    } catch (error) {
      actionError = error instanceof Error ? error.message : t('squads.deleteMemberFailed')
    } finally {
      busyMemberId = ''
    }
  }

  async function deleteSquad(squad: SquadViewModel) {
    if (!squad.id || deleteSquadMutation.isPending) return
    if (!confirm(t('squads.confirmDeleteSquad', { name: squad.name }))) return
    actionError = ''
    busySquadId = squad.id
    try {
      await deleteSquadMutation.mutateAsync(squad.id)
    } catch (error) {
      actionError = error instanceof Error ? error.message : t('squads.deleteSquadFailed')
    } finally {
      busySquadId = ''
    }
  }
</script>

<svelte:head>
  <title>{t('nav.squads')} | Orrery</title>
</svelte:head>

<div class="workforce-page page-shell max-w-[1040px] space-y-4">
  <header class="page-header">
    <div>
      <h1 class="page-title">{t('nav.squads')}</h1>
      <p class="page-description">{t('nav.squads.description')}</p>
    </div>
    <Button href="/people" class="shrink-0">
      <Plus class="h-4 w-4" />
      {t('squads.create')}
    </Button>
  </header>

  {#if orgQuery.isLoading}
    <LoadingState title={t('nav.squads')} description={t('nav.squads.description')} />
  {:else if orgQuery.isError}
    {@const error = orgQuery.error as ApiClientError}
    <ErrorState title={t('nav.squads')} message={error.message} correlationId={error.correlationId} />
  {:else if squadView}
    {#if actionError}
      <div class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{actionError}</div>
    {/if}

    {#if squadView.squads.length === 0}
      <EmptyState title={t('squads.empty')} description={t('squads.empty.description')} />
    {:else if activeSquad}
      {@const squad = activeSquad}
      <section data-squad-directory="master-detail" class="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)]">
        <aside class="space-y-2 self-start" aria-label={t('nav.squads')}>
          {#each squadView.squads as squad (squad.id)}
            <button
              type="button"
              class="w-full rounded-xl border bg-card p-3 text-left transition-[border-color,box-shadow,background-color] {selectedSquadId === squad.id
                ? 'selection-wash'
                : 'border-border hover:border-foreground/20 hover:bg-muted/20'}"
              onclick={() => (selectedSquadId = squad.id)}
            >
              <span class="block truncate text-[13.5px] font-medium text-foreground">{squad.name}</span>
              <span class="mt-0.5 block truncate text-[11.5px] text-muted-foreground">
                {squad.missionId} · {t('squads.membersCount', { count: squad.members.length })}
              </span>
              <span class="mt-2 flex -space-x-1.5">
                {#each squad.members.slice(0, 5) as member (member.id)}
                  <WorkforceAvatar
                    profileId={member.profileId}
                    name={member.name}
                    human={member.source === 'human'}
                    class="h-7 w-7 rounded-full border-2 border-card"
                    textClass="text-[9px]"
                  />
                {/each}
              </span>
            </button>
          {/each}
        </aside>

        <article class="min-w-0 overflow-hidden rounded-xl border border-border bg-card">
          <header class="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-start sm:justify-between">
            <div class="min-w-0">
              <div class="flex min-w-0 items-center gap-2">
                <h2 class="truncate text-[15px] font-medium text-foreground">{activeSquad.name}</h2>
                <Badge tone={activeSquad.status === 'active' ? 'success' : 'warning'}>{activeSquad.status}</Badge>
              </div>
              <p class="mt-0.5 truncate text-xs text-muted-foreground">
                {activeSquad.humanCount} {t('squads.kpi.humans')} · {activeSquad.agentCount} {t('squads.kpi.agents')} · {t('squads.mission')}: {activeSquad.missionId}
              </p>
            </div>
            <div class="flex shrink-0 items-center gap-1.5">
              <Button href="/discussions" size="sm" variant="outline">
                <MessageSquare class="h-3.5 w-3.5" />
                {t('squads.chat')}
              </Button>
              <Button href="/missions" size="sm" variant="outline">
                <ClipboardList class="h-3.5 w-3.5" />
                {t('squads.assignTask')}
              </Button>
              <Button
                size="icon"
                variant="ghost"
                class="h-8 w-8 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                onclick={() => deleteSquad(activeSquad)}
                disabled={deleteSquadMutation.isPending}
                ariaLabel={t('squads.deleteSquad')}
                title={t('squads.deleteSquad')}
              >
                <Trash2 class="h-4 w-4" />
              </Button>
            </div>
          </header>

          <div class="divide-y divide-border/70">
            {#each squad.members as member}
              <div class="relative flex min-h-20 items-center gap-3 px-4 py-3 pr-12">
                <span class="relative h-12 w-12 shrink-0">
                  <WorkforceAvatar
                    profileId={member.profileId}
                    name={member.name}
                    human={member.source === 'human'}
                    class="h-12 w-12 rounded-full"
                    textClass="text-xs"
                  />
                  <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-card {member.status === 'active' ? 'bg-status-done' : 'bg-muted-foreground/45'}"></span>
                </span>
                <span class="min-w-0 flex-1">
                  <span class="flex min-w-0 items-center gap-1.5">
                    <span class="truncate text-[13.5px] font-medium text-foreground">{member.name}</span>
                    <Badge tone={member.source === 'agent' ? 'source-agent' : 'source-human'} class="text-[10px]">
                      {#if member.source === 'agent'}
                        <Bot class="mr-1 h-3 w-3" />{t('people.identityAgent')}
                      {:else}
                        <User class="mr-1 h-3 w-3" />{t('people.identityHuman')}
                      {/if}
                    </Badge>
                  </span>
                  <span class="mt-0.5 block truncate text-[11.5px] text-muted-foreground">{member.role}</span>
                </span>
                <span class="shrink-0 text-[11.5px] text-muted-foreground">{member.status}</span>
                <button
                  type="button"
                  class="absolute right-3 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
                  onclick={() => deleteMember(member)}
                  disabled={releaseMemberMutation.isPending || busySquadId === activeSquad.id || busyMemberId === member.profileId}
                  aria-label={t('squads.deleteMember')}
                  title={t('squads.deleteMember')}
                >
                  <Trash2 class="h-3.5 w-3.5" />
                </button>
              </div>
            {:else}
              <div class="p-6">
                <EmptyState title={t('squads.empty')} description={t('squads.empty.description')} />
              </div>
            {/each}
          </div>
          <a class="flex items-center justify-center gap-1.5 border-t border-border px-4 py-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted/30 hover:text-foreground" href="/people">
            <UserPlus class="h-3.5 w-3.5" />
            {t('squads.addMember')}
          </a>
        </article>
      </section>
    {/if}
  {/if}
</div>
