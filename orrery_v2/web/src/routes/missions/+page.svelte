<script lang="ts">
  import { AlertCircle, Bot, CheckCircle2, Rocket, Search, TrendingUp } from 'lucide-svelte'
  import MissionList from '#lib/components/mission/MissionList.svelte'
  import KpiCard from '#lib/components/shared/KpiCard.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import { useMissionCreateMutation, useMissionDeleteMutation, useMissionListQuery } from '#lib/queries/missions'
  import type { ApiClientError } from '#lib/api/client'
  import type { MissionCreateFormViewModel, MissionListItemViewModel } from '#lib/types/view-models'

  let search = $state('')
  let status = $state('')
  let title = $state('')
  let finalGoal = $state('')
  let businessDirection = $state('')
  let busyId = $state('')
  let actionMessage = $state('')
  let showCreateForm = $state(false)

  const missionsQuery = useMissionListQuery(() => ({ search, status }))
  const createMission = useMissionCreateMutation()
  const deleteMission = useMissionDeleteMutation()
  const canCreate = $derived(title.trim().length > 0 && finalGoal.trim().length > 0)
  let t = $derived(createTranslator($locale))

  function submitMission() {
    if (!canCreate) return
    actionMessage = ''
    const mission: MissionCreateFormViewModel = {
      title,
      finalGoal,
      businessDirection
    }
    createMission.mutate(
      mission,
      {
        onSuccess: () => {
          actionMessage = t('mission.create')
          title = ''
          finalGoal = ''
          businessDirection = ''
          showCreateForm = false
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('mission.createFailed')
        }
      }
    )
  }

  function removeMission(mission: MissionListItemViewModel) {
    busyId = mission.id
    actionMessage = ''
    deleteMission.mutate(mission.id, {
      onSuccess: () => {
        actionMessage = t('mission.deleted', { title: mission.title })
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('mission.deleteFailed')
        busyId = ''
      }
    })
  }
</script>

<svelte:head>
  <title>{t('nav.missionList')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[960px] space-y-5">
  <!-- Page header -->
  <header class="page-header">
    <div>
      <h1 class="page-title">{t('nav.missionList')}</h1>
      <p class="page-description">{t('nav.missionList.description')}</p>
    </div>
    <Button onclick={() => { showCreateForm = !showCreateForm; if (showCreateForm) setTimeout(() => document.getElementById('create-mission-title')?.focus(), 50) }}>
      <Rocket class="h-4 w-4" />
      {t('mission.create')}
    </Button>
  </header>

  {#if missionsQuery.isLoading}
    <LoadingState title={t('state.loadingMissions')} description="" />
  {:else if missionsQuery.isError}
    {@const error = missionsQuery.error as ApiClientError}
    <ErrorState title={t('state.errorMissions')} message={error.message} correlationId={error.correlationId} />
  {:else if missionsQuery.data}
    <!-- KPI Cards -->
    <section class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <KpiCard label={t('mission.kpi.active')} value={missionsQuery.data.summary.active} icon={Rocket} />
      <KpiCard label={t('mission.kpi.agents')} value={missionsQuery.data.missions.reduce((sum, mission) => sum + mission.teamSize, 0)} icon={Bot} />
      <KpiCard label={t('mission.kpi.approvals')} value={missionsQuery.data.summary.pendingApprovals} icon={AlertCircle} />
      <KpiCard label={t('mission.kpi.delivery')} value={`${Math.round(missionsQuery.data.missions.reduce((sum, mission) => sum + mission.deliveryPercent, 0) / Math.max(1, missionsQuery.data.missions.length))}%`} icon={TrendingUp} />
    </section>

    <!-- Status tabs + search -->
    <section class="flex flex-col gap-3 border-b border-border sm:flex-row sm:items-end sm:justify-between">
      <div class="flex min-h-[33px] items-center gap-5 overflow-x-auto overflow-y-hidden subtle-scrollbar">
        {#each [
          { value: '', label: t('mission.allStatuses') },
          { value: 'active', label: t('mission.active') },
          { value: 'completed', label: t('mission.done') },
          { value: 'blocked', label: t('mission.blocked') }
        ] as filter}
          <button
            type="button"
            class="-mb-px shrink-0 border-b-2 pb-2.5 text-[13.5px] transition-colors {status === filter.value ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
            onclick={() => (status = filter.value)}
          >
            {filter.label}
          </button>
        {/each}
      </div>
      <div class="relative mb-2 flex-1 sm:max-w-xs">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/60" />
        <Input bind:value={search} placeholder={t('filter.search')} class="h-8 pl-9 text-[13px]" />
      </div>
    </section>

    <!-- Create Mission Form (collapsible) -->
    {#if showCreateForm}
      <section class="rounded-xl border border-border bg-card p-5 animate-fade-in-up">
        <h3 class="text-sm font-semibold text-foreground">{t('mission.create')}</h3>
        <p class="mt-0.5 text-xs text-muted-foreground">{t('mission.createDesc')}</p>
        <div class="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('mission.title')}</span>
            <Input id="create-mission-title" bind:value={title} />
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('mission.finalGoal')}</span>
            <Input bind:value={finalGoal} />
          </label>
          <label class="space-y-1.5">
            <span class="text-xs font-medium text-muted-foreground">{t('mission.businessDirection')}</span>
            <Input bind:value={businessDirection} />
          </label>
          <div class="flex items-end">
            <Button class="w-full" disabled={!canCreate || createMission.isPending} onclick={submitMission}>
              <Rocket class="h-4 w-4" />
              {t('mission.create')}
            </Button>
          </div>
        </div>
      </section>
    {/if}

    <!-- Action message -->
    {#if actionMessage}
      <div class="flex items-center gap-2 rounded-lg border border-brand/20 bg-brand/5 px-4 py-2.5 text-sm font-medium text-brand animate-fade-in">
        <CheckCircle2 class="h-4 w-4" />
        {actionMessage}
      </div>
    {/if}

    <!-- Mission List -->
    <MissionList missions={missionsQuery.data.missions} {busyId} ondelete={removeMission} />
  {/if}
</div>
