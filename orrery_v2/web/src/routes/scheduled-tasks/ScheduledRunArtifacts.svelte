<script lang="ts">
  import { onDestroy } from 'svelte'
  import { Download, Eye, FileText, LoaderCircle } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import { schedulerApi } from '#lib/api/scheduler'
  import { renderSafeMarkdown } from '#lib/markdown'
  import { locale } from '#lib/i18n'
  import type { ScheduledRunArtifact } from '#lib/types/scheduler'

  interface Props {
    scheduledTaskID: string
    scheduledRunID: string
    artifacts?: ScheduledRunArtifact[]
  }

  let { scheduledTaskID, scheduledRunID, artifacts = [] }: Props = $props()
  let previewArtifact = $state<ScheduledRunArtifact | undefined>()
  let previewKind = $state<'markdown' | 'text' | 'image' | 'pdf'>('text')
  let previewText = $state('')
  let previewURL = $state('')
  let previewLoading = $state(false)
  let busyArtifactID = $state('')
  let actionError = $state('')
  let previewRequestID = 0
  let renderedMarkdown = $derived(previewKind === 'markdown' ? renderSafeMarkdown(previewText) : '')

  const text = (zh: string, en: string) => $locale.startsWith('zh') ? zh : en

  async function openPreview(artifact: ScheduledRunArtifact) {
	const requestID = ++previewRequestID
    releasePreviewURL()
    previewArtifact = artifact
    previewText = ''
    actionError = ''
    previewLoading = true
    try {
      const blob = await schedulerApi.readRunArtifact(scheduledTaskID, scheduledRunID, artifact.id, 'inline')
	  if (requestID !== previewRequestID || previewArtifact?.id !== artifact.id) return
      const extension = fileExtension(artifact.file_name)
      const mediaType = blob.type.split(';', 1)[0].toLowerCase()
      if (['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(mediaType)) {
        previewKind = 'image'
        previewURL = URL.createObjectURL(blob)
      } else if (mediaType === 'application/pdf' || extension === 'pdf') {
        previewKind = 'pdf'
        previewURL = URL.createObjectURL(blob)
      } else if (mediaType.startsWith('image/')) {
        throw new Error('unsupported preview media type')
      } else {
		const body = await blob.text()
		if (requestID !== previewRequestID || previewArtifact?.id !== artifact.id) return
		previewText = body
        if (extension === 'json') {
          previewText = prettyJSON(previewText)
        }
        previewKind = extension === 'md' || extension === 'markdown' ? 'markdown' : 'text'
      }
    } catch {
	  if (requestID === previewRequestID) {
		actionError = text('产出物暂时无法预览。', 'The artifact could not be previewed.')
	  }
    } finally {
	  if (requestID === previewRequestID) previewLoading = false
    }
  }

  async function downloadArtifact(artifact: ScheduledRunArtifact) {
    actionError = ''
    busyArtifactID = artifact.id
    try {
      const blob = await schedulerApi.readRunArtifact(scheduledTaskID, scheduledRunID, artifact.id, 'attachment')
      const downloadURL = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = downloadURL
      anchor.download = artifact.file_name || 'artifact'
      document.body.appendChild(anchor)
      anchor.click()
      anchor.remove()
      URL.revokeObjectURL(downloadURL)
    } catch {
      actionError = text('产出物暂时无法下载。', 'The artifact could not be downloaded.')
    } finally {
      if (busyArtifactID === artifact.id) busyArtifactID = ''
    }
  }

  function closePreview() {
	previewRequestID += 1
    previewArtifact = undefined
    previewLoading = false
    previewText = ''
    actionError = ''
    releasePreviewURL()
  }

  function releasePreviewURL() {
    if (!previewURL) return
    URL.revokeObjectURL(previewURL)
    previewURL = ''
  }

  function fileExtension(fileName: string) {
    const match = /\.([^.]+)$/.exec(fileName.toLowerCase())
    return match?.[1] ?? ''
  }

  function prettyJSON(value: string) {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }

  function formatBytes(value: number | undefined) {
	if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return '-'
    if (value < 1024) return `${value} B`
    const units = ['KB', 'MB', 'GB']
    let size = value / 1024
    let unit = units[0]
    for (let index = 1; index < units.length && size >= 1024; index += 1) {
      size /= 1024
      unit = units[index]
    }
    return `${size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${unit}`
  }

  onDestroy(() => {
	previewRequestID += 1
	releasePreviewURL()
  })
</script>

{#if artifacts.length > 0}
  <div class="mt-3 flex items-center justify-between text-xs font-medium text-foreground">
    <span>{text('产出物', 'Artifacts')}</span>
    <span class="tabular-nums text-muted-foreground">{artifacts.length}</span>
  </div>
  <div class="mt-2 divide-y divide-border/70 border-y border-border/70" data-scheduled-run-artifacts>
    {#each artifacts as artifact (artifact.id)}
      <div class="grid min-h-12 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-2" data-scheduled-run-artifact>
        <FileText class="h-4 w-4 shrink-0 text-muted-foreground" />
        <div class="min-w-0">
          <div class="truncate text-sm font-medium text-foreground" title={artifact.file_name}>{artifact.file_name}</div>
          <div class="mt-0.5 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
            <span class="uppercase">{artifact.kind}</span>
            <span>{formatBytes(artifact.size_bytes)}</span>
            {#if artifact.content_availability_checked && !artifact.content_available}
              <span>{text('内容不可用', 'Content unavailable')}</span>
            {/if}
          </div>
        </div>
        <div class="flex h-9 items-center gap-1">
          {#if (!artifact.content_availability_checked || artifact.content_available) && artifact.previewable}
            <Button
              variant="ghost"
              size="icon"
              class="!h-8 !w-8"
              ariaLabel={text(`预览 ${artifact.file_name}`, `Preview ${artifact.file_name}`)}
              title={text('预览', 'Preview')}
              onclick={() => openPreview(artifact)}
            >
              <Eye class="h-4 w-4" />
            </Button>
          {/if}
          {#if !artifact.content_availability_checked || artifact.content_available}
            <Button
              variant="ghost"
              size="icon"
              class="!h-8 !w-8"
              disabled={busyArtifactID === artifact.id}
              ariaLabel={text(`下载 ${artifact.file_name}`, `Download ${artifact.file_name}`)}
              title={text('下载', 'Download')}
              onclick={() => downloadArtifact(artifact)}
            >
              {#if busyArtifactID === artifact.id}
                <LoaderCircle class="h-4 w-4 animate-spin" />
              {:else}
                <Download class="h-4 w-4" />
              {/if}
            </Button>
          {/if}
        </div>
      </div>
    {/each}
  </div>
  {#if actionError && !previewArtifact}
    <div class="mt-2 text-xs text-destructive" role="alert">{actionError}</div>
  {/if}
{/if}

<Dialog
  open={Boolean(previewArtifact)}
  title={previewArtifact?.file_name ?? text('产出物预览', 'Artifact preview')}
  size="wide"
  onClose={closePreview}
>
  <div class="min-h-72" aria-busy={previewLoading} data-scheduled-artifact-preview>
    {#if previewLoading}
      <div class="flex min-h-72 items-center justify-center text-muted-foreground">
        <LoaderCircle class="h-5 w-5 animate-spin" />
      </div>
    {:else if actionError}
      <div class="flex min-h-72 items-center justify-center text-sm text-destructive" role="alert">{actionError}</div>
    {:else if previewKind === 'image' && previewURL}
      <div class="flex min-h-72 items-center justify-center bg-muted/30 p-3">
        <img class="max-h-[70vh] max-w-full object-contain" src={previewURL} alt={previewArtifact?.file_name ?? ''} />
      </div>
    {:else if previewKind === 'pdf' && previewURL}
      <iframe class="h-[70vh] w-full border-0" src={previewURL} title={previewArtifact?.file_name ?? 'PDF'}></iframe>
    {:else if previewKind === 'markdown'}
      <div class="prose prose-slate max-w-none text-sm leading-7 dark:prose-invert">{@html renderedMarkdown}</div>
    {:else}
      <pre class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words bg-muted/30 p-4 text-xs leading-6 text-foreground">{previewText}</pre>
    {/if}
  </div>
</Dialog>
