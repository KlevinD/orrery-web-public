<script lang="ts">
  import { onDestroy } from 'svelte'
  import { Download, Eye, File, X } from 'lucide-svelte'
  import { todoApi } from '#lib/api/todos'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TodoAttachmentDTO } from '#lib/types/todo'

  let { todoId, attachment, onremove }: { todoId: string; attachment: TodoAttachmentDTO; onremove?: () => void } = $props()
  const t = $derived(createTranslator($locale))
  let mediaUrl = $state('')
  let busy = $state(false)
  let error = $state('')
  let disposed = false
  const previewable = $derived(/^(image\/(png|jpeg|gif|webp)|audio\/|video\/)/.test(attachment.content_type))

  async function load(download = false) {
    if (busy) return
    busy = true
    error = ''
    try {
      if (!mediaUrl) {
        const blob = await todoApi.downloadAttachment(todoId, attachment.attachment_id)
        if (disposed) return
        mediaUrl = URL.createObjectURL(blob)
      }
      if (download) {
        const link = document.createElement('a')
        link.href = mediaUrl
        link.download = attachment.file_name
        link.click()
      }
    } catch (cause) { error = cause instanceof Error ? cause.message : t('workhub.message.operationFailed') }
    finally { busy = false }
  }
  onDestroy(() => { disposed = true; if (mediaUrl) URL.revokeObjectURL(mediaUrl) })
</script>

<div class="min-w-0 border-b border-border py-2" data-feedback-attachment>
  <div class="flex min-w-0 items-center gap-2">
    <File class="h-4 w-4 shrink-0 text-muted-foreground" />
    <span class="min-w-0 flex-1 break-all text-sm">{attachment.file_name}</span>
    <span class="shrink-0 text-xs text-muted-foreground">{Math.max(1, Math.ceil(attachment.size_bytes / 1024))} KB</span>
    {#if previewable}<button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded hover:bg-muted" disabled={busy} onclick={() => load()} title={t('feedback.preview')} aria-label={t('feedback.preview')}><Eye class="h-4 w-4" /></button>{/if}
    <button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded hover:bg-muted" disabled={busy} onclick={() => load(true)} title={t('feedback.download')} aria-label={t('feedback.download')}><Download class="h-4 w-4" /></button>
    {#if onremove}<button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded hover:bg-muted" onclick={onremove} title={t('feedback.remove')} aria-label={t('feedback.remove')}><X class="h-4 w-4" /></button>{/if}
  </div>
  {#if mediaUrl && previewable}
    {#if attachment.content_type.startsWith('image/')}<img class="mt-2 max-h-64 max-w-full rounded object-contain" src={mediaUrl} alt={attachment.file_name} />
    {:else if attachment.content_type.startsWith('audio/')}<audio class="mt-2 w-full" controls src={mediaUrl}></audio>
    {:else}<video class="mt-2 max-h-80 w-full" controls src={mediaUrl}><track kind="captions" /></video>{/if}
  {/if}
  {#if error}<p class="mt-1 break-words text-sm text-destructive" role="alert">{error}</p>{/if}
</div>
