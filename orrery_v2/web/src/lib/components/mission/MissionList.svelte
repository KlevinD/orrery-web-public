<script lang="ts">
  import { ArrowRight, CalendarDays, Rocket, Trash2, UserRound, Users } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type { MissionListItemViewModel } from '#lib/types/view-models'

  interface Props {
    missions: MissionListItemViewModel[]
    busyId?: string
    ondelete?: (mission: MissionListItemViewModel) => void
  }

  let { missions, busyId = '', ondelete }: Props = $props()
  let t = $derived(createTranslator($locale))

  function tx(key: string) {
    return t(key as TranslationKey)
  }

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }
</script>

{#if missions.length === 0}
  <div class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-border bg-card py-12 text-center">
    <Rocket class="h-9 w-9 text-muted-foreground/30" />
    <p class="text-sm text-muted-foreground">{t('mission.empty')}</p>
  </div>
{:else}
  <div class="grid gap-3 lg:grid-cols-2">
    {#each missions as mission (mission.id)}
      <article class="surface-hover group rounded-xl border border-border bg-card p-4">
        <!-- Header row -->
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="truncate text-sm font-semibold text-foreground">{mission.title}</h3>
              <Badge tone={mission.statusTone}>{tx(`status.${mission.status}`) || mission.status}</Badge>
            </div>
            <p class="mt-1.5 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{displayField(mission.description)}</p>
          </div>
          <Button
            href={`/missions/${mission.id}`}
            size="sm"
            variant="ghost"
          >
            {t('nav.missionWorkspace')}
            <ArrowRight class="h-3.5 w-3.5" />
          </Button>
        </div>

        <!-- Phase & Risk -->
        <div class="mt-3 flex flex-wrap items-center gap-2">
          <span class="rounded-md bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
            {tx(`phase.${mission.phase}`) || mission.phase}
          </span>
          <Badge tone={mission.riskTone} variant="outline">{tx(`risk.${mission.riskLevel}`) || mission.riskLevel}</Badge>
        </div>

        <!-- Stats row -->
        <div class="mt-4 flex items-center gap-5 text-xs text-muted-foreground">
          <span class="flex items-center gap-1.5">
            <Users class="h-3 w-3" />
            {t('workspace.teamCount', { count: mission.teamSize })}
          </span>
          <span class="flex items-center gap-1.5">
            <CalendarDays class="h-3 w-3" />
            {mission.deadline}
          </span>
          <span class="flex items-center gap-1.5">
            <UserRound class="h-3 w-3" />
            {mission.createdBy}
          </span>
        </div>

        <!-- Progress bar -->
        {#if mission.deliveryPercent > 0}
          <div class="mt-4 flex items-center gap-3">
            <div class="h-1.5 flex-1 overflow-hidden rounded-sm bg-muted">
              <div class="h-full rounded-sm bg-brand transition-all" style="width:{mission.deliveryPercent}%"></div>
            </div>
            <span class="shrink-0 text-xs font-semibold text-foreground">{mission.deliveryPercent}%</span>
          </div>
        {/if}

        <!-- Mini stats -->
        <div class="mt-4 grid grid-cols-3 gap-2 border-t border-border pt-3 text-center">
          <div>
            <div class="text-sm font-bold text-status-active">{mission.activeTaskCount}</div>
            <div class="text-[10px] text-muted-foreground">{t('workspace.inProgress')}</div>
          </div>
          <div>
            <div class="text-sm font-bold text-status-blocked">{mission.pendingApprovalCount}</div>
            <div class="text-[10px] text-muted-foreground">{t('mission.kpi.approvals')}</div>
          </div>
          <div>
            <div class="text-sm font-bold text-status-failed">{mission.blockerCount}</div>
            <div class="text-[10px] text-muted-foreground">{t('discussion.blockers')}</div>
          </div>
        </div>

        <!-- Delete button (bottom right, subtle) -->
        {#if ondelete}
          <div class="mt-3 flex justify-end">
            <Button
              variant="ghost"
              size="sm"
              disabled={busyId === mission.id}
              onclick={() => ondelete?.(mission)}
            >
              <Trash2 class="h-3.5 w-3.5" />
              {t('mission.delete')}
            </Button>
          </div>
        {/if}
      </article>
    {/each}
  </div>
{/if}
