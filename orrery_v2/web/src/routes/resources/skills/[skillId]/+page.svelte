<script lang="ts">
  import { ArrowLeft, CheckCircle2, Download, Pencil } from 'lucide-svelte'
  import { goto } from '$app/navigation'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import SkillMarkdownPanel from '#lib/components/resources/SkillMarkdownPanel.svelte'
  import SkillResourceBrowser from '#lib/components/resources/SkillResourceBrowser.svelte'
  import { onDestroy } from 'svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { useSkillCommandMutations, useSkillDetailQuery } from '#lib/queries/resources'
  import { buildSkillMarkdown, parseSkillMarkdownForm, type SkillMarkdownForm } from '#lib/skillMarkdown'
  import type { ApiClientError } from '#lib/api/client'
  import type { SkillDTO, SkillFileDTO } from '#lib/types/api'

  interface Props {
    data: { skillId: string; mode: string }
  }

  let { data }: Props = $props()
  let t = $derived(createTranslator($locale))
  const skillId = $derived(data.skillId)
  const createMode = $derived(skillId === 'new')
  const editMode = $derived(data.mode === 'edit')
  const detailQuery = useSkillDetailQuery(() => createMode ? '' : skillId)
  const skillCommands = useSkillCommandMutations()
  let actionMessage = $state('')
  let selectedFile = $state<SkillFileDTO | undefined>()
  let imagePreviewUrl = $state<string | undefined>()
  let busyPath = $state('')
  let form = $state<SkillMarkdownForm>({
    name: '',
    description: '',
    version: '1.0.0',
    taskTypeTags: '',
    prompt: '# New skill\n\nDescribe when and how this skill should be used.'
  })
  let lastParsedRaw = $state<string | undefined>()

  const savingSkill = $derived(skillCommands.create.isPending || skillCommands.saveMarkdown.isPending)
  const currentSkillMarkdown = $derived(detailQuery.data?.skill_md?.raw ?? '')
  const assembledSkillMarkdown = $derived(buildSkillMarkdown(form))
  const skillFormDirty = $derived(createMode || assembledSkillMarkdown !== currentSkillMarkdown)

  function displayField(value?: string) {
    return !value || value === 'Not provided' ? t('workspace.notProvided') : value
  }

  function statusTone(skill?: SkillDTO) {
    return skill?.enabled === false ? 'neutral' : 'success'
  }

  function statusLabel(skill?: SkillDTO) {
    return skill?.enabled === false ? t('kpi.disabled') : t('kpi.enabled')
  }

  function tx(key: string) {
    return t(key as TranslationKey)
  }

  function skillRiskLevel(skill?: SkillDTO) {
    return (skill?.risk_level ?? 'low').toLowerCase()
  }

  function skillRiskTone(skill?: SkillDTO) {
    const riskLevel = skillRiskLevel(skill)
    if (riskLevel === 'high') return 'danger'
    if (riskLevel === 'medium') return 'warning'
    return 'success'
  }

  $effect(() => {
    const raw = detailQuery.data?.skill_md?.raw
    if (createMode || raw === undefined || raw === lastParsedRaw) return
    form = parseSkillMarkdownForm(raw)
    lastParsedRaw = raw
  })

  function saveMarkdown(prompt: string) {
    actionMessage = ''
    const nextForm = { ...form, prompt }
    form = nextForm
    if (createMode) {
      skillCommands.create.mutate(nextForm, {
        onSuccess: (detail) => {
          const created = detail as { skill?: { skill_id?: string } }
          actionMessage = t('resources.message.skillCreated')
          const nextId = created.skill?.skill_id
          if (nextId) goto(`/resources/skills/${encodeURIComponent(nextId)}?mode=edit`)
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillCreateFailed')
        }
      })
      return
    }
    skillCommands.saveMarkdown.mutate(
      { skillId, raw: buildSkillMarkdown(nextForm) },
      {
        onSuccess: () => {
          actionMessage = t('resources.message.skillMarkdownSaved')
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillMarkdownSaveFailed')
        }
      }
    )
  }

  function updateFormField<K extends keyof SkillMarkdownForm>(key: K, value: SkillMarkdownForm[K]) {
    form = { ...form, [key]: value }
  }

  function switchToEdit() {
    goto(`/resources/skills/${encodeURIComponent(skillId)}?mode=edit`)
  }

  function exportSkillPackage() {
    actionMessage = ''
    skillCommands.export.mutate(skillId, {
      onSuccess: (blob) => {
        downloadSkillPackage(skillId, blob as Blob)
        actionMessage = t('resources.message.skillExported')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.skillExportFailed')
      }
    })
  }

  function previewResourceFile(path: string) {
    actionMessage = ''
    busyPath = path
    skillCommands.getFile.mutate(
      { skillId, path },
      {
        onSuccess: (file) => {
          const resourceFile = file as SkillFileDTO
          selectedFile = resourceFile
          if (resourceFile.preview_kind === 'image') {
            clearImagePreviewUrl()
            loadImagePreview(resourceFile.path)
            return
          }
          clearImagePreviewUrl()
          busyPath = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillFilePreviewFailed')
          busyPath = ''
        }
      }
    )
  }

  function loadImagePreview(path: string) {
    skillCommands.downloadFile.mutate(
      { skillId, path },
      {
        onSuccess: (blob) => {
          const url = URL.createObjectURL(blob as Blob)
          if (selectedFile?.path === path && selectedFile.preview_kind === 'image') {
            clearImagePreviewUrl()
            imagePreviewUrl = url
          } else {
            URL.revokeObjectURL(url)
          }
          busyPath = ''
        },
        onError: (error) => {
          clearImagePreviewUrl()
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillFilePreviewFailed')
          busyPath = ''
        }
      }
    )
  }

  function downloadResourceFile(path: string) {
    actionMessage = ''
    busyPath = path
    skillCommands.downloadFile.mutate(
      { skillId, path },
      {
        onSuccess: (blob) => {
          downloadBlob(path, blob as Blob)
          busyPath = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillFileDownloadFailed')
          busyPath = ''
        }
      }
    )
  }

  function saveResourceFile(path: string, content: string) {
    actionMessage = ''
    busyPath = path
    skillCommands.saveFile.mutate(
      { skillId, path, content },
      {
        onSuccess: (file) => {
          selectedFile = file as SkillFileDTO
          clearImagePreviewUrl()
          actionMessage = t('resources.message.skillFileSaved')
          busyPath = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillFileSaveFailed')
          busyPath = ''
        }
      }
    )
  }

  function safeUploadFileName(fileName: string) {
    const normalized = fileName.replaceAll('\\', '/').split('/').pop()?.trim() ?? ''
    return normalized || 'upload.bin'
  }

  function uploadTargetPath(root: string, targetName: string) {
    return root === 'other' ? targetName : `${root}/${targetName}`
  }

  function uploadResourceFile(root: string, file: File) {
    actionMessage = ''
    busyPath = root
    const body = new FormData()
    const targetName = safeUploadFileName(file.name)
    body.set('file', file)
    skillCommands.uploadFile.mutate(
      { skillId, path: uploadTargetPath(root, targetName), body },
      {
        onSuccess: (uploaded) => {
          selectedFile = uploaded as SkillFileDTO
          clearImagePreviewUrl()
          actionMessage = t('resources.message.skillFileUploaded')
          busyPath = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillFileUploadFailed')
          busyPath = ''
        }
      }
    )
  }

  function createResourceFolder(root: string, name: string) {
    actionMessage = ''
    busyPath = root
    const safeName = name.replaceAll('\\', '/').split('/').filter(Boolean).join('/')
    const path = uploadTargetPath(root, safeName)
    skillCommands.createFolder.mutate(
      { skillId, path },
      {
        onSuccess: () => {
          actionMessage = t('resources.message.skillFolderCreated')
          busyPath = ''
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('resources.message.skillFolderCreateFailed')
          busyPath = ''
        }
      }
    )
  }

  function downloadBlob(path: string, blob: Blob) {
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = path.split('/').pop() ?? 'skill-file'
    anchor.click()
    URL.revokeObjectURL(url)
  }

  function downloadSkillPackage(skillId: string, blob: Blob) {
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${skillId}.skill.zip`
    anchor.click()
    URL.revokeObjectURL(url)
  }

  function clearImagePreviewUrl() {
    if (!imagePreviewUrl) return
    URL.revokeObjectURL(imagePreviewUrl)
    imagePreviewUrl = undefined
  }

  onDestroy(() => {
    clearImagePreviewUrl()
  })
</script>

<svelte:head>
  <title>{createMode ? t('resources.skills.create') : detailQuery.data?.skill.name ?? t('nav.skills')} | Orrery</title>
</svelte:head>

{#if !createMode && detailQuery.isLoading}
  <LoadingState title={t('state.loadingSkills')} description={t('state.loadingSkillsDescription')} />
{:else if !createMode && detailQuery.isError}
  {@const error = detailQuery.error as ApiClientError}
  <ErrorState title={t('state.errorSkills')} message={error.message} correlationId={error.correlationId} />
{:else if createMode || detailQuery.data}
  {@const detail = detailQuery.data}
  {@const skill = detail?.skill}
  <div class="page-shell max-w-[1040px] space-y-5">
    <header class="flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-end lg:justify-between">
      <div class="min-w-0">
        <Button variant="ghost" size="sm" onclick={() => goto('/resources/skills')} class="mb-2">
          <ArrowLeft class="h-3.5 w-3.5" />
          {t('nav.skills')}
        </Button>
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <h1 class="truncate text-[22px] font-semibold leading-[33px] text-foreground">{createMode ? t('resources.skills.create') : displayField(skill?.name)}</h1>
          {#if skill}
            <Badge tone={statusTone(skill)}>{statusLabel(skill)}</Badge>
            <Badge tone={skillRiskTone(skill)}>{tx(`risk.${skillRiskLevel(skill)}`) || skillRiskLevel(skill)}</Badge>
          {/if}
          <span class="text-sm font-medium text-muted-foreground">v{displayField(form.version)}</span>
        </div>
        <p class="mt-1 max-w-3xl text-[13px] leading-5 text-muted-foreground">{displayField(form.description)}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if !createMode}
          <Button variant="outline" size="sm" disabled={skillCommands.export.isPending} onclick={exportSkillPackage} ariaLabel={t('resources.action.export')}>
            <Download class="h-3.5 w-3.5" />
            {t('resources.action.export')}
          </Button>
        {/if}
        {#if !createMode && !editMode}
          <Button variant="outline" size="sm" onclick={switchToEdit}>
            <Pencil class="h-3.5 w-3.5" />
            {t('resources.skills.editSelected')}
          </Button>
        {/if}
        {#if skill}
          <div class="inline-flex items-center gap-1.5 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
            <CheckCircle2 class="h-3.5 w-3.5" />
            {t('resources.skills.updated')}: {displayField(skill.updated_at)}
          </div>
        {/if}
      </div>
    </header>

    {#if actionMessage}
      <div class="rounded-lg border border-brand/25 bg-brand/10 px-4 py-2.5 text-sm text-brand">
        {actionMessage}
      </div>
    {/if}

    <div class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_22rem]">
      <main class="min-w-0 space-y-4">
        <section class="rounded-xl border border-border bg-card p-4">
          <div class="grid gap-3 md:grid-cols-2">
            <label class="space-y-1">
              <span class="text-xs font-medium text-muted-foreground">{t('resources.editor.name')}</span>
              <Input value={form.name} disabled={!editMode} oninput={(event) => updateFormField('name', event.currentTarget.value)} />
            </label>
            <label class="space-y-1">
              <span class="text-xs font-medium text-muted-foreground">{t('resources.skills.version')}</span>
              <Input value={form.version} disabled={!editMode} oninput={(event) => updateFormField('version', event.currentTarget.value)} />
            </label>
            <label class="space-y-1 md:col-span-2">
              <span class="text-xs font-medium text-muted-foreground">{t('resources.editor.description')}</span>
              <Input value={form.description} disabled={!editMode} oninput={(event) => updateFormField('description', event.currentTarget.value)} />
            </label>
            <label class="space-y-1 md:col-span-2">
              <span class="text-xs font-medium text-muted-foreground">{t('resources.skills.tasks')}</span>
              <Input value={form.taskTypeTags} disabled={!editMode} placeholder={t('resources.skills.tagsPlaceholder')} oninput={(event) => updateFormField('taskTypeTags', event.currentTarget.value)} />
            </label>
          </div>
        </section>

        <SkillMarkdownPanel
          raw={form.prompt}
          editable={editMode}
          busy={savingSkill}
          saveEnabled={skillFormDirty}
          onsave={saveMarkdown}
        />
      </main>

      <aside class="min-w-0 space-y-3">
        {#if detail}
          <SkillResourceBrowser
            roots={detail.roots}
            selectedFile={selectedFile}
            {imagePreviewUrl}
            editable={editMode}
            {busyPath}
            onpreview={previewResourceFile}
            ondownload={downloadResourceFile}
            onsave={saveResourceFile}
            onupload={uploadResourceFile}
            oncreatefolder={createResourceFolder}
          />
        {:else}
          <section class="rounded-xl border border-border bg-card p-4">
            <h2 class="text-sm font-semibold text-foreground">{t('resources.skills.packageResources')}</h2>
            <p class="mt-2 text-xs leading-5 text-muted-foreground">{t('resources.skills.createResourcesHint')}</p>
          </section>
        {/if}
      </aside>
    </div>
  </div>
{/if}
