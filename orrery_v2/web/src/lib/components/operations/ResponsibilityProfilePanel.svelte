<script lang="ts">
  import {
    AlertCircle,
    BriefcaseBusiness,
    Building2,
    CheckCircle2,
    ChevronDown,
    Clock3,
    Edit3,
    FileClock,
    History,
    LoaderCircle,
    ShieldCheck
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type {
    PeopleResolvedResponsibilityDTO,
    PeopleResponsibilityPortfolioProfileDTO,
    PeopleResponsibilityPortfolioVersionDTO,
    PeopleResponsibilityProfileDTO
  } from '#lib/types/api'

  interface Props {
    profile?: PeopleResponsibilityProfileDTO
    responsibilityBoundary?: string
    loading?: boolean
    failed?: boolean
    oneditboundary?: () => void
  }

  let {
    profile,
    responsibilityBoundary = '',
    loading = false,
    failed = false,
    oneditboundary
  }: Props = $props()
  let selectedVersionIds = $state<Record<string, string>>({})
  let t = $derived(createTranslator($locale))

  function selectedVersion(portfolio: PeopleResponsibilityPortfolioProfileDTO): PeopleResponsibilityPortfolioVersionDTO | undefined {
    const versionId = selectedVersionIds[portfolio.binding_id] || portfolio.current_version_id
    return portfolio.versions.find((version) => version.version_id === versionId) ?? portfolio.versions[0]
  }

  function selectVersion(bindingId: string, event: Event) {
    selectedVersionIds[bindingId] = (event.currentTarget as HTMLSelectElement).value
  }

  function formatDate(value?: string) {
    if (!value) return t('people.notConfigured')
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat($locale, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
  }

  function scopeLabel(item: PeopleResolvedResponsibilityDTO) {
    if (isPositionResponsibility(item)) return t('people.responsibilityPositionScope')
    if (item.sources.some((source) => source.target_type === 'org_unit')) return t('people.responsibilityOrgScope')
    return t('people.responsibilityOtherScope')
  }

  function isPositionResponsibility(item: PeopleResolvedResponsibilityDTO) {
    return item.sources.some((source) => source.target_type === 'position')
  }

  function targetLabel(targetType: string) {
    if (targetType === 'position') return t('people.position')
    if (targetType === 'org_unit') return t('organization.root')
    return targetType
  }

  function isCurrent(portfolio: PeopleResponsibilityPortfolioProfileDTO, version?: PeopleResponsibilityPortfolioVersionDTO) {
    return !version || version.version_id === portfolio.current_version_id
  }
</script>

<section data-responsibility-profile>
  <section data-agent-responsibility-boundary class="border-b border-border pb-5">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <Edit3 class="h-4 w-4 text-brand" />
          <h3 class="text-sm font-semibold text-foreground">{t('people.executionResponsibilityBoundary')}</h3>
          <Badge tone="blue">Orrery</Badge>
        </div>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('people.executionResponsibilityBoundaryDescription')}</p>
      </div>
      {#if oneditboundary}
        <Button variant="outline" size="sm" onclick={oneditboundary}>
          <Edit3 class="h-3.5 w-3.5" />
          {t('people.editResponsibilityBoundary')}
        </Button>
      {/if}
    </header>
    <p class="mt-4 whitespace-pre-wrap border-l-2 border-brand/55 pl-4 text-[13px] leading-6 text-foreground/85">
      {responsibilityBoundary.trim() || t('people.notConfigured')}
    </p>
  </section>

  <section data-people-formal-responsibilities class="pt-5">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <CheckCircle2 class="h-4 w-4 text-brand" />
          <h3 class="text-sm font-semibold text-foreground">{t('people.peopleFormalResponsibilities')}</h3>
          <Badge tone="teal">People</Badge>
        </div>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('people.peopleFormalResponsibilitiesDescription')}</p>
      </div>
      {#if profile}
        <div class="flex items-center gap-3 text-xs text-muted-foreground">
          <span>{profile.responsibility_count} {t('people.responsibilities')}</span>
          <span>{profile.version_count} {t('people.responsibilityVersions')}</span>
        </div>
      {/if}
    </header>

    {#if loading}
      <div class="flex min-h-52 items-center justify-center gap-2 text-sm text-muted-foreground">
        <LoaderCircle class="h-4 w-4 animate-spin" />
        <span>{t('people.responsibilityLoading')}</span>
      </div>
    {:else if failed}
      <div class="flex min-h-52 flex-col items-center justify-center text-center">
        <AlertCircle class="h-6 w-6 text-destructive" />
        <p class="mt-3 text-sm font-medium text-foreground">{t('people.responsibilityLoadFailed')}</p>
      </div>
    {:else if !profile || profile.portfolios.length === 0}
      <div class="flex min-h-52 flex-col items-center justify-center border border-dashed border-border bg-muted/10 px-6 text-center">
        <ShieldCheck class="h-6 w-6 text-muted-foreground/60" />
        <p class="mt-3 text-sm font-medium text-foreground">{t('people.responsibilityEmpty')}</p>
        <p class="mt-1.5 max-w-md text-xs leading-5 text-muted-foreground">{t('people.responsibilityEmptyDescription')}</p>
      </div>
    {:else}
      <div class="mt-4 divide-y divide-border border-t border-border">
      {#each profile.portfolios as portfolio (portfolio.binding_id)}
        {@const version = selectedVersion(portfolio)}
        {@const responsibilities = version?.responsibilities ?? portfolio.responsibilities}
        <section class="py-5 first:pt-4 last:pb-0" data-responsibility-portfolio={portfolio.binding_id}>
          <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <h4 class="text-[13px] font-semibold text-foreground">{portfolio.agent_display_name}</h4>
                <Badge tone={portfolio.status === 'active' ? 'success' : 'warning'}>{portfolio.status}</Badge>
                {#if isCurrent(portfolio, version)}
                  <Badge tone="info">{t('people.responsibilityCurrent')}</Badge>
                {:else}
                  <Badge tone="neutral">{t('people.responsibilityHistorical')}</Badge>
                {/if}
              </div>
              <p class="mt-1 truncate font-mono text-[11px] text-muted-foreground">{portfolio.subject_id}</p>
            </div>

            {#if portfolio.versions.length > 0}
              <label class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
              <History class="h-3.5 w-3.5 shrink-0" />
              <span class="shrink-0">{t('people.responsibilityVersions')}</span>
              <select
                class="h-8 min-w-0 max-w-64 rounded-md border border-input bg-background px-2 text-xs text-foreground outline-none focus:border-ring"
                value={version?.version_id ?? ''}
                onchange={(event) => selectVersion(portfolio.binding_id, event)}
              >
                {#each portfolio.versions as option (option.version_id)}
                  <option value={option.version_id}>{option.source_revision} / {formatDate(option.recorded_at)}</option>
                {/each}
              </select>
              </label>
            {/if}
          </div>

          <dl class="mt-4 grid gap-x-6 gap-y-3 bg-muted/45 px-4 py-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
            <div class="min-w-0">
              <dt class="text-muted-foreground">{t('people.responsibilitySourceRevision')}</dt>
              <dd class="mt-1 truncate font-mono text-foreground">{version?.source_revision ?? portfolio.current_source_revision}</dd>
            </div>
            <div class="min-w-0">
              <dt class="text-muted-foreground">{t('people.responsibilityRecordedAt')}</dt>
              <dd class="mt-1 truncate text-foreground">{formatDate(version?.recorded_at)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="text-muted-foreground">{t('people.responsibilitySyncedAt')}</dt>
              <dd class="mt-1 truncate text-foreground">{formatDate(portfolio.synced_at)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="text-muted-foreground">{t('people.responsibilitySource')}</dt>
              <dd class="mt-1 truncate text-foreground">{portfolio.people_tenant_id}</dd>
            </div>
          </dl>

          {#if portfolio.gap}
            <div class="mt-4 flex items-start gap-2 border-l-2 px-3 py-2.5 text-xs {portfolio.gap.open ? 'border-status-blocked bg-status-blocked-bg/55 text-status-blocked' : 'border-status-done bg-status-done-bg/45 text-status-done'}">
              {#if portfolio.gap.open}
                <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />
              {:else}
                <CheckCircle2 class="mt-0.5 h-3.5 w-3.5 shrink-0" />
              {/if}
              <div class="min-w-0">
                <p class="font-medium">{portfolio.gap.open ? t('people.responsibilityGapOpen') : t('people.responsibilityGapResolved')}</p>
                <p class="mt-1 break-words opacity-85">{portfolio.gap.code}: {portfolio.gap.message}</p>
              </div>
            </div>
          {/if}

          <div class="mt-4 divide-y divide-border border-y border-border">
            {#each responsibilities as responsibility (responsibility.id)}
              <details class="group py-3" open={responsibilities.length === 1}>
                <summary class="flex cursor-pointer list-none items-start justify-between gap-3">
                  <div class="min-w-0">
                    <div class="flex flex-wrap items-center gap-2">
                      {#if isPositionResponsibility(responsibility)}
                        <BriefcaseBusiness class="h-3.5 w-3.5 text-brand" />
                      {:else}
                        <Building2 class="h-3.5 w-3.5 text-brand" />
                      {/if}
                      <h5 class="text-[13px] font-medium text-foreground">{responsibility.title}</h5>
                      <Badge tone="neutral">{scopeLabel(responsibility)}</Badge>
                    </div>
                    <p class="mt-1 font-mono text-[11px] text-muted-foreground">{responsibility.code} / r{responsibility.revision}</p>
                  </div>
                  <ChevronDown class="mt-1 h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
                </summary>
                <div class="mt-3 pl-5">
                  <p class="whitespace-pre-wrap break-words text-[12.5px] leading-6 text-foreground/85">{responsibility.description}</p>
                  <div class="mt-3 flex flex-wrap gap-2">
                    {#each responsibility.sources as source (source.binding_id)}
                      <span class="inline-flex max-w-full items-center gap-1.5 rounded-md bg-muted px-2 py-1 text-[11px] text-muted-foreground">
                        {#if source.target_type === 'position'}
                          <BriefcaseBusiness class="h-3 w-3 shrink-0" />
                        {:else}
                          <Building2 class="h-3 w-3 shrink-0" />
                        {/if}
                        <span class="shrink-0">{targetLabel(source.target_type)}</span>
                        <span class="truncate font-mono text-foreground">{source.target_id}</span>
                        <span>r{source.revision}</span>
                      </span>
                    {/each}
                  </div>
                  <p class="mt-3 flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                    <Clock3 class="h-3 w-3" />
                    <span>{t('people.responsibilityEffectivePeriod')}:</span>
                    <span>{formatDate(responsibility.valid_from)} - {responsibility.valid_to ? formatDate(responsibility.valid_to) : t('people.responsibilityNoExpiry')}</span>
                  </p>
                </div>
              </details>
            {/each}
          </div>

          <div class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-[11px] text-muted-foreground">
            {#if portfolio.pulse_scheduled_task_id}
              <span class="inline-flex min-w-0 items-center gap-1.5">
                <FileClock class="h-3.5 w-3.5 shrink-0" />
                {t('people.responsibilityPlannerLegacy')}:
                <span class="truncate font-mono text-foreground">{portfolio.pulse_scheduled_task_id}</span>
              </span>
            {/if}
            <details>
              <summary class="inline-flex cursor-pointer list-none items-center gap-1.5 hover:text-foreground">
                <History class="h-3.5 w-3.5" />
                {t('people.responsibilityAudit')} / {t('people.responsibilityAuditCount', { count: portfolio.audit.length })}
              </summary>
              <ol class="mt-3 space-y-2 border-l border-border pl-3">
                {#each portfolio.audit.slice(0, 10) as event (event.audit_id)}
                  <li>
                    <p class="text-xs text-foreground">{event.action}</p>
                    <p class="mt-0.5 text-[11px] text-muted-foreground">{formatDate(event.occurred_at)}{event.to_state ? ` / ${event.to_state}` : ''}</p>
                  </li>
                {/each}
              </ol>
            </details>
          </div>
        </section>
      {/each}
      </div>
    {/if}
  </section>
</section>
