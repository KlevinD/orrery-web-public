<script lang="ts">
  import {
    Search,
    FileUp,
    Monitor,
    Plus,
    Download,
    Package,
    List
  } from 'lucide-svelte'
  import { goto } from '$app/navigation'
  import ResourceBindingPanel from '#lib/components/resources/ResourceBindingPanel.svelte'
  import SkillCard from '#lib/components/resources/SkillCard.svelte'
  import SkillBundleCard from '#lib/components/resources/SkillBundleCard.svelte'
  import SkillBundleDeleteDialog from '#lib/components/resources/SkillBundleDeleteDialog.svelte'
  import SkillBundleEditorDialog from '#lib/components/resources/SkillBundleEditorDialog.svelte'
  import SkillRecorderDialog from '#lib/components/resources/SkillRecorderDialog.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import {
    combineSkillPages,
    resourceCatalogFilters,
    useResourceBindMutation,
    useResourceSkillsInfiniteQuery,
    useResourceSkillsQuery,
    useResourceUnbindMutation,
    useSkillBundleCommandMutations,
    useSkillBundlesQuery,
    useSkillCommandMutations
  } from '#lib/queries/resources'
  import { mapSkillListView } from '#lib/mappers/resource'
  import { createSkillBundleSubmissionSession } from '#lib/resources/skillBundleSubmission'
  import type { ApiClientError } from '#lib/api/client'
  import type {
    ResourceBindingFormViewModel,
    ResourceSkillCommandFormViewModel,
    SkillBundleEditorSubmission,
    SkillBundleViewModel,
    SkillViewModel
  } from '#lib/types/view-models'

  let activeView = $state('skills')
  let search = $state('')
  let enabled = $state('')
  let selectedSkill = $state<SkillViewModel | undefined>()
  let showSkillBindingDialog = $state(false)
  let showSkillRecorderDialog = $state(false)
  let showBundleEditorDialog = $state(false)
  let showBundleDeleteDialog = $state(false)
  let bundleEditorMode = $state<'create' | 'add'>('create')
  let selectedBundle = $state<SkillBundleViewModel | undefined>()
  let importInput = $state<HTMLInputElement | undefined>()
  let busyId = $state('')
  let bundleBusyId = $state('')
  let actionMessage = $state('')
  let loadMoreMarker = $state<HTMLElement | undefined>()
  let t = $derived(createTranslator($locale))

  const skillsQuery = useResourceSkillsInfiniteQuery(() => ({
    search,
    enabled: enabled === '' ? undefined : enabled
  }), () => activeView === 'skills')
  const skillCatalogQuery = useResourceSkillsQuery(
    () => resourceCatalogFilters(),
    () => activeView === 'bundles'
  )
  const bundlesQuery = useSkillBundlesQuery(() => activeView === 'bundles')
  const bindMutation = useResourceBindMutation()
  const unbindMutation = useResourceUnbindMutation()
  const skillCommands = useSkillCommandMutations()
  const bundleCommands = useSkillBundleCommandMutations()
  const bundleSubmissionSession = createSkillBundleSubmissionSession()
  const skillsData = $derived(mapSkillListView(combineSkillPages(skillsQuery.data?.pages ?? [])))
  const selectedId = $derived(selectedSkill?.id ?? '')
  const bindingBusy = $derived(bindMutation.isPending || unbindMutation.isPending)
  const commandBusy = $derived(skillCommands.import.isPending || skillCommands.export.isPending || skillCommands.create.isPending || skillCommands.disable.isPending || skillCommands.delete.isPending)
  const bundleCommandBusy = $derived(
    bundleCommands.create.isPending ||
    bundleCommands.addSkills.isPending ||
    bundleCommands.importSkill.isPending ||
    bundleCommands.removeSkill.isPending ||
    bundleCommands.delete.isPending
  )
  const bundles = $derived(bundlesQuery.data?.bundles ?? [])
  const catalogSkills = $derived(skillCatalogQuery.data?.skills ?? [])

  function openImportSkillDialog() {
    importInput?.click()
  }

  function openNewSkill() {
    goto('/resources/skills/new?mode=edit')
  }

  async function createRecordedSkill(form: ResourceSkillCommandFormViewModel) {
    actionMessage = ''
    const detail = await skillCommands.create.mutateAsync(form) as { skill?: { skill_id?: string } }
    actionMessage = '录制流程已写入技能注册表，可继续编辑技能详情。'
    showSkillRecorderDialog = false
    const nextId = detail.skill?.skill_id
    if (nextId) goto(`/resources/skills/${encodeURIComponent(nextId)}?mode=edit`)
  }

  function exportCurrentSkill() {
    const skill = selectedSkill ?? skillsData.skills[0]
    if (skill) exportSkill(skill)
  }

  function openSkillDetail(skillId: string) {
    goto(`/resources/skills/${encodeURIComponent(skillId)}`)
  }

  function openSkillEdit(skillId: string) {
    goto(`/resources/skills/${encodeURIComponent(skillId)}?mode=edit`)
  }

  function openSkillBindingDialog(skill?: SkillViewModel) {
    if (skill) selectedSkill = skill
    if (!selectedSkill) return
    showSkillBindingDialog = true
  }

  function bind(form: ResourceBindingFormViewModel) {
    actionMessage = ''
    bindMutation.mutate(form, {
      onSuccess: () => {
        actionMessage = t('resources.binding.bindSucceeded')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.binding.bindFailed')
      }
    })
  }

  function unbind(form: ResourceBindingFormViewModel) {
    actionMessage = ''
    unbindMutation.mutate(form, {
      onSuccess: () => {
        actionMessage = t('resources.binding.unbindSucceeded')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.binding.unbindFailed')
      }
    })
  }

  function importSkill(file: File) {
    actionMessage = ''
    skillCommands.import.mutate(file, {
      onSuccess: (skill) => {
        const riskLevel = (skill?.risk_level ?? 'low').toLowerCase()
        const riskLabel = tx(`risk.${riskLevel}`) || riskLevel
        actionMessage = t('resources.message.skillImportedWithRisk', { risk: riskLabel })
        if (riskLevel === 'medium' || riskLevel === 'high') {
          actionMessage = `${actionMessage} ${t('resources.message.skillRiskWarning')}`
        }
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.skillImportFailed')
      }
    })
  }

  function exportSkill(skill: SkillViewModel) {
    busyId = skill.id
    actionMessage = ''
    skillCommands.export.mutate(skill.id, {
      onSuccess: (blob) => {
        downloadSkillPackage(skill.id, blob as Blob)
        actionMessage = t('resources.message.skillExported')
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.skillExportFailed')
        busyId = ''
      }
    })
  }

  function disableSkill(skill: SkillViewModel) {
    busyId = skill.id
    selectedSkill = skill
    actionMessage = ''
    skillCommands.disable.mutate(skill.id, {
      onSuccess: () => {
        actionMessage = t('resources.message.skillDisabled')
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.skillDisableFailed')
        busyId = ''
      }
    })
  }

  function deleteSkill(skill: SkillViewModel) {
    busyId = skill.id
    selectedSkill = skill
    actionMessage = ''
    skillCommands.delete.mutate(skill.id, {
      onSuccess: () => {
        actionMessage = t('resources.message.skillDeleted')
        busyId = ''
        selectedSkill = undefined
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.skillDeleteFailed')
        busyId = ''
      }
    })
  }

  function openCreateBundle() {
    bundleSubmissionSession.reset()
    selectedBundle = undefined
    bundleEditorMode = 'create'
    showBundleEditorDialog = true
  }

  function openAddToBundle(bundle: SkillBundleViewModel) {
    bundleSubmissionSession.reset()
    selectedBundle = bundle
    bundleEditorMode = 'add'
    showBundleEditorDialog = true
  }

  function openDeleteBundle(bundle: SkillBundleViewModel) {
    selectedBundle = bundle
    showBundleDeleteDialog = true
  }

  async function submitBundleEditor(submission: SkillBundleEditorSubmission) {
    actionMessage = ''
    bundleBusyId = selectedBundle?.id ?? 'new'
    try {
      await bundleSubmissionSession.submit({
        mode: bundleEditorMode,
        existingBundleId: selectedBundle?.id ?? '',
        submission,
        operations: {
          create: async (input) => {
            const created = await bundleCommands.create.mutateAsync(input)
            return { bundleId: created.bundle_id }
          },
          addSkills: async (bundleId, skillIds) => {
            await bundleCommands.addSkills.mutateAsync({ bundleId, skillIds })
          },
          importSkill: async (bundleId, file) => {
            await bundleCommands.importSkill.mutateAsync({ bundleId, file })
          }
        }
      })
      actionMessage = bundleEditorMode === 'create'
        ? t('resources.skillBundles.created')
        : t('resources.skillBundles.updated')
      showBundleEditorDialog = false
      selectedBundle = undefined
      bundleSubmissionSession.reset()
    } catch (error) {
      actionMessage = error instanceof Error ? error.message : t('resources.skillBundles.commandFailed')
    } finally {
      bundleBusyId = ''
    }
  }

  function removeSkillFromBundle(bundle: SkillBundleViewModel, skill: SkillViewModel) {
    actionMessage = ''
    bundleBusyId = bundle.id
    bundleCommands.removeSkill.mutate(
      { bundleId: bundle.id, skillId: skill.id },
      {
        onSuccess: () => {
          actionMessage = t('resources.skillBundles.skillRemoved')
          bundleBusyId = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.skillBundles.commandFailed')
          bundleBusyId = ''
        }
      }
    )
  }

  function deleteBundle(bundle: SkillBundleViewModel, deleteSkills: boolean) {
    actionMessage = ''
    bundleBusyId = bundle.id
    bundleCommands.delete.mutate(
      { bundleId: bundle.id, deleteSkills },
      {
        onSuccess: () => {
          actionMessage = t('resources.skillBundles.deleted')
          showBundleDeleteDialog = false
          selectedBundle = undefined
          bundleBusyId = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.skillBundles.commandFailed')
          bundleBusyId = ''
        }
      }
    )
  }

  function downloadSkillPackage(skillId: string, blob: Blob) {
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${skillId}.skill.zip`
    anchor.click()
    URL.revokeObjectURL(url)
  }

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function tx(key: string) {
    return t(key as TranslationKey)
  }

  function loadMore() {
    if (skillsQuery.hasNextPage && !skillsQuery.isFetchingNextPage) {
      skillsQuery.fetchNextPage()
    }
  }

  $effect(() => {
    if (!loadMoreMarker || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) loadMore()
    }, { rootMargin: '240px' })
    observer.observe(loadMoreMarker)
    return () => observer.disconnect()
  })
</script>

<svelte:head>
  <title>{t('nav.skills')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1040px]">
  <header class="page-header mb-4">
    <div>
      <h1 class="page-title">{t('nav.skills')}</h1>
      <p class="page-description">{t('nav.skills.description')}</p>
    </div>
    <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
      {#if activeView === 'skills'}
        <Button variant="outline" class="h-9 rounded-lg !px-3 !text-[13px]" onclick={openImportSkillDialog} disabled={commandBusy}>
          <FileUp class="h-3.5 w-3.5" />
          {t('resources.skills.importPackage')}
        </Button>
        <Button variant="outline" class="h-9 rounded-lg !px-3 !text-[13px]" onclick={exportCurrentSkill} disabled={commandBusy || skillsData.skills.length === 0}>
          <Download class="h-3.5 w-3.5" />
          {t('resources.skills.exportShort')}
        </Button>
        <Button variant="outline" class="h-9 rounded-lg !px-3 !text-[13px]" onclick={() => (showSkillRecorderDialog = true)} disabled={commandBusy}>
          <Monitor class="h-3.5 w-3.5" />
          录制生成技能
        </Button>
        <Button class="h-9 rounded-lg !px-3 !text-[13px]" onclick={openNewSkill}>
          <Plus class="h-4 w-4" />
          {t('resources.skills.create')}
        </Button>
        <input
          bind:this={importInput}
          type="file"
          class="hidden"
          accept=".zip,application/zip"
          aria-label={t('resources.skills.choosePackage')}
          onchange={(event) => {
            const file = event.currentTarget.files?.[0]
            event.currentTarget.value = ''
            if (file) importSkill(file)
          }}
        />
      {:else}
        <Button variant="outline" class="h-9 rounded-lg px-3 !text-[13px]" onclick={() => (activeView = 'skills')}>
          <List class="h-3.5 w-3.5" />
          {t('resources.skillBundles.tabSkills')}
        </Button>
        <Button class="h-9 rounded-lg px-3 !text-[13px]" onclick={openCreateBundle} disabled={bundleCommandBusy}>
          <Plus class="h-4 w-4" />
          {t('resources.skillBundles.create')}
        </Button>
      {/if}
    </div>
  </header>

  {#if actionMessage}
    <div class="mb-3 rounded-lg border border-brand/25 bg-brand/10 px-4 py-2.5 text-sm text-brand">
      {actionMessage}
    </div>
  {/if}

  {#if activeView === 'skills'}
    {#if skillsQuery.isLoading}
      <LoadingState title={t('state.loadingSkills')} description={t('state.loadingSkillsDescription')} />
    {:else if skillsQuery.isError}
      {@const error = skillsQuery.error as ApiClientError}
      <ErrorState title={t('state.errorSkills')} message={error.message} correlationId={error.correlationId} />
    {:else if skillsQuery.data}
      <section class="mb-3 flex items-center gap-2">
        <div class="relative min-w-0 flex-1">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground/70" />
          <Input
            value={search}
            placeholder={t('resources.filter.searchSkills')}
            oninput={(event) => (search = event.currentTarget.value)}
            class="h-9 rounded-lg bg-card pl-9 !text-[13px]"
          />
        </div>
        <div class="w-28 shrink-0">
          <Select
            value={enabled}
            options={[
              { label: t('resources.filter.allStatus'), value: '' },
              { label: t('kpi.enabled'), value: 'true' },
              { label: t('kpi.disabled'), value: 'false' }
            ]}
            onchange={(event) => (enabled = event.currentTarget.value)}
            class="h-9 rounded-lg bg-card !text-[12.5px]"
          />
        </div>
      </section>

      {#if skillsData.skills.length === 0}
        <EmptyState title={t('resources.skills.empty')} description={t('resources.skills.empty.description')} />
      {:else}
        <section class="overflow-hidden rounded-xl border border-border bg-card">
          <div class="hidden grid-cols-[minmax(0,1fr)_90px_120px_110px_80px] items-center gap-3 border-b border-border/70 px-4 py-2 text-[11px] font-medium text-muted-foreground lg:grid">
            <span>{t('resources.skills.skill')}</span>
            <span>{t('resources.skills.version')}</span>
            <span>{t('resources.editor.status')}</span>
            <span>{t('resources.skills.owner')}</span>
            <span class="text-right">{t('resources.filter.usage')}</span>
          </div>
          <div class="divide-y divide-border/60">
            {#each skillsData.skills as skill (skill.id)}
              <SkillCard
                {skill}
                selected={selectedId === skill.id}
                busy={busyId === skill.id}
                onopen={openSkillDetail}
                onedit={openSkillEdit}
                onexport={exportSkill}
                onbind={openSkillBindingDialog}
                ondisable={disableSkill}
                ondelete={deleteSkill}
              />
            {/each}
          </div>
         </section>
         <div class="mt-2 flex justify-end">
           <Button variant="ghost" class="h-8 rounded-lg px-2.5 !text-[12px] text-muted-foreground" onclick={() => (activeView = 'bundles')}>
             <Package class="h-3.5 w-3.5" />
             {t('resources.skillBundles.tabBundles')}
           </Button>
         </div>
         <div bind:this={loadMoreMarker} class="h-px"></div>
        {#if skillsQuery.isFetchingNextPage}
          <div class="py-4 text-center text-xs text-muted-foreground">{t('state.loadingSkills')}</div>
        {/if}
      {/if}

      <Dialog open={showSkillBindingDialog} title={selectedSkill ? selectedSkill.name : t('resources.binding.title')} size="wide">
        <ResourceBindingPanel
          resource={selectedSkill}
          resourceType="skill"
          busy={bindingBusy}
          message={actionMessage}
          onbind={bind}
          onunbind={unbind}
        />
        <div class="mt-4 flex justify-end">
          <Button variant="outline" onclick={() => (showSkillBindingDialog = false)}>{t('organization.cancel')}</Button>
        </div>
      </Dialog>

      <SkillRecorderDialog
        open={showSkillRecorderDialog}
        busy={skillCommands.create.isPending}
        oncreate={createRecordedSkill}
        onclose={() => (showSkillRecorderDialog = false)}
      />
    {/if}
  {:else}
    {#if bundlesQuery.isLoading || skillCatalogQuery.isLoading}
      <LoadingState title={t('resources.skillBundles.loading')} description={t('resources.skillBundles.loading')} />
    {:else if bundlesQuery.isError}
      {@const error = bundlesQuery.error as ApiClientError}
      <ErrorState title={t('resources.skillBundles.loadFailed')} message={error.message} correlationId={error.correlationId} />
    {:else if skillCatalogQuery.isError}
      {@const error = skillCatalogQuery.error as ApiClientError}
      <ErrorState title={t('resources.skillBundles.loadFailed')} message={error.message} correlationId={error.correlationId} />
    {:else}
      {#if bundles.length === 0}
        <EmptyState title={t('resources.skillBundles.empty')} description={t('resources.skillBundles.empty.description')} />
      {:else}
        <section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {#each bundles as bundle (bundle.id)}
            <SkillBundleCard
              {bundle}
              busy={bundleBusyId === bundle.id}
              onadd={openAddToBundle}
              onremove={removeSkillFromBundle}
              ondelete={openDeleteBundle}
            />
          {/each}
        </section>
      {/if}

      <SkillBundleEditorDialog
        open={showBundleEditorDialog}
        mode={bundleEditorMode}
        bundle={selectedBundle}
        skills={catalogSkills}
        busy={bundleCommandBusy}
        onsubmit={submitBundleEditor}
        oncancel={() => {
          showBundleEditorDialog = false
          selectedBundle = undefined
          bundleSubmissionSession.reset()
        }}
      />

      <SkillBundleDeleteDialog
        open={showBundleDeleteDialog}
        bundle={selectedBundle}
        busy={bundleCommandBusy}
        onconfirm={deleteBundle}
        oncancel={() => {
          showBundleDeleteDialog = false
          selectedBundle = undefined
        }}
      />
    {/if}
  {/if}
</div>
