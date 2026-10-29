<script lang="ts">
  import { ArrowLeft, Check, Link2, Paperclip, RotateCw, Settings2, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import TodoAttachmentView from './TodoAttachmentView.svelte'
  import { todoApi } from '#lib/api/todos'
  import { useTodoCommandMutations } from '#lib/queries/todos'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { renderSafeMarkdown } from '#lib/markdown'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { session } from '#lib/stores/session.svelte'
  import type { IssueCommentDTO, TodoAttachmentDTO, TodoItemDTO } from '#lib/types/todo'

  let { todo, comments, onmanage, onrefresh, embedded = false }: { todo: TodoItemDTO; comments: IssueCommentDTO[]; onmanage: () => void; onrefresh: () => Promise<unknown>; embedded?: boolean } = $props()
  const t = $derived(createTranslator($locale))
  const commands = useTodoCommandMutations()
  let feedback = $state('')
  let attachments = $state<TodoAttachmentDTO[]>([])
  let uploadInput = $state<HTMLInputElement>()
  let uploading = $state(false)
  let error = $state('')
  let uploadName = $state('')
  let failedFiles = $state<File[]>([])
  let initialized = $state(false)
  const terminal = $derived(todo.status === 'done' || todo.status === 'canceled')
  const canWrite = $derived(hasPermissionScope(session.permissionScope, 'todo.write') && ['user', 'human'].includes(session.viewerKind))
  const pending = $derived(uploading || commands.moveTodo.isPending)
  const submitted = $derived(todo.feedback?.body || todo.source?.result || '')

  $effect(() => {
    if (initialized) return
    attachments = terminal ? [] : (todo.attachments ?? []).filter(a => !todo.feedback?.attachments?.some(f => f.attachment_id === a.attachment_id))
    initialized = true
  })

  async function remove(attachment: TodoAttachmentDTO) {
    if (pending) return
    uploading = true
    error = ''
    try {
      await todoApi.deleteAttachment(todo.todo_id, attachment.attachment_id)
      attachments = attachments.filter(a => a.attachment_id !== attachment.attachment_id)
      await onrefresh()
    } catch (cause) { error = cause instanceof Error ? cause.message : t('workhub.message.operationFailed') }
    finally { uploading = false }
  }

  async function upload(files: File[]) {
    if (pending || terminal || !canWrite) return
    error = ''
    uploading = true
    failedFiles = []
    let uploaded = 0
    try {
      if ((todo.attachments?.length ?? 0) + files.length > 10) throw new Error(t('feedback.limit'))
      for (const file of files) {
        if (file.size === 0 || file.size > 20 * 1024 * 1024) throw new Error(t('feedback.limit'))
        uploadName = file.name
        const attachment = await todoApi.uploadAttachment(todo.todo_id, file)
        attachments = [...attachments, attachment]
        uploaded++
      }
    } catch (cause) {
      failedFiles = files.slice(uploaded)
      error = cause instanceof Error ? cause.message : t('workhub.message.operationFailed')
    }
    finally {
      try { await onrefresh() } catch { error = t('workhub.message.operationFailed') }
      uploading = false
      uploadName = ''
      if (uploadInput) uploadInput.value = ''
    }
  }

  function paste(event: ClipboardEvent) {
    const files = Array.from(event.clipboardData?.files ?? [])
    if (files.length) { event.preventDefault(); void upload(files) }
  }

  async function submit() {
    if (pending || failedFiles.length || terminal || !canWrite || (!feedback.trim() && !attachments.length)) return
    error = ''
    try {
      await commands.moveTodo.mutateAsync({ todoID: todo.todo_id, body: { status: 'done', result: feedback.trim(), result_attachment_ids: attachments.map(a => a.attachment_id), version: todo.version } })
      feedback = ''
      attachments = []
      await onrefresh()
    } catch (cause) { error = cause instanceof Error ? cause.message : t('workhub.message.operationFailed') }
  }
</script>

<div class="min-w-0 {embedded ? 'rounded-md border border-border bg-card p-4 sm:p-5' : 'page-shell max-w-[860px]'}" data-human-todo-detail>
  <header class="border-b border-border pb-4">
    <div class="mb-4 flex items-center justify-between gap-3">
      {#if !embedded}<Button href="/work-items?view=inbox" variant="ghost" size="sm" class="-ml-2"><ArrowLeft class="h-4 w-4" />{t('workhub.action.back')}</Button>{:else}<span></span>{/if}
      <button class="grid h-8 w-8 place-items-center rounded hover:bg-muted" type="button" onclick={onmanage} title={t('feedback.manage')} aria-label={t('feedback.manage')}><Settings2 class="h-4 w-4" /></button>
    </div>
    <h1 class="break-words text-[22px] font-semibold leading-8">{todo.title}</h1>
    <div class="mt-3 flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
      <Badge tone={todo.status === 'done' ? 'success' : 'neutral'}>{t(`workhub.status.${todo.status}` as TranslationKey)}</Badge>
      <span>{todo.assignee_name || todo.assignee_id}</span>
      {#if todo.due_at && !todo.due_at.startsWith('0001-')}<time>{new Date(todo.due_at).toLocaleDateString($locale)}</time>{/if}
      {#if todo.source?.can_open}<a class="inline-flex items-center gap-1 hover:underline" href={`/discussions/${encodeURIComponent(todo.source.conversation_id)}`}><Link2 class="h-3.5 w-3.5" />{t('handoff.source')}</a>{/if}
    </div>
  </header>
  <section class="border-b border-border py-6" aria-label={t('feedback.request')}>
    <h2 class="mb-3 text-base font-semibold">{t('feedback.request')}</h2>
    <div class="prose max-w-none break-words text-sm leading-7 dark:prose-invert [&_img]:max-w-full">{@html renderSafeMarkdown(todo.description || todo.title)}</div>
    {#if todo.acceptance_criteria}<p class="mt-4 whitespace-pre-wrap break-words text-sm leading-6 text-muted-foreground">{todo.acceptance_criteria}</p>{/if}
    {#if todo.blocked_reason}<p class="mt-3 break-words text-sm text-amber-700">{todo.blocked_reason}</p>{/if}
  </section>
  <section class="py-6" aria-label={t('feedback.yours')} data-human-handoff>
    <h2 class="mb-3 text-base font-semibold">{t('feedback.yours')}</h2>
    {#if terminal}
      {#if submitted}<div class="whitespace-pre-wrap break-words text-sm leading-7">{submitted}</div>{/if}
      {#each todo.feedback?.attachments ?? [] as attachment (attachment.attachment_id)}<TodoAttachmentView todoId={todo.todo_id} {attachment} />{/each}
      {#if todo.source}<p class="mt-3 text-xs text-muted-foreground" role="status">{t(todo.source.delivery_status === 'delivered' ? 'handoff.delivered' : 'handoff.pending')}</p>{/if}
    {:else if canWrite}
      <textarea class="min-h-40 w-full resize-y rounded-md border border-border bg-input-background p-3 text-sm leading-6 outline-none focus:ring-1 focus:ring-ring" aria-label={t('feedback.yours')} placeholder={t('feedback.placeholder')} bind:value={feedback} maxlength={32000} disabled={commands.moveTodo.isPending} onpaste={paste}></textarea>
      {#each attachments as attachment (attachment.attachment_id)}<TodoAttachmentView todoId={todo.todo_id} {attachment} onremove={pending ? undefined : () => remove(attachment)} />{/each}
      {#if uploadName}<p class="mt-2 break-all text-xs text-muted-foreground" role="status">{t('feedback.uploading')} {uploadName}</p>{/if}
      {#if failedFiles.length}<div class="mt-2 flex min-w-0 items-center gap-2 text-sm text-destructive"><span class="min-w-0 flex-1 break-all">{failedFiles.map(file => file.name).join(', ')}</span><button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded hover:bg-muted" disabled={pending} onclick={() => upload(failedFiles)} title={t('feedback.retry')} aria-label={t('feedback.retry')}><RotateCw class="h-4 w-4" /></button><button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded hover:bg-muted" disabled={pending} onclick={() => { failedFiles = []; error = '' }} title={t('feedback.remove')} aria-label={t('feedback.remove')}><X class="h-4 w-4" /></button></div>{/if}
      <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
        <input bind:this={uploadInput} class="sr-only" type="file" multiple aria-label={t('feedback.attach')} disabled={pending} onchange={event => upload(Array.from(event.currentTarget.files ?? []))} />
        <button class="grid h-9 w-9 place-items-center rounded hover:bg-muted disabled:opacity-40" type="button" disabled={pending} onclick={() => uploadInput?.click()} title={`${t('feedback.attach')} · ${t('feedback.limit')}`} aria-label={t('feedback.attach')}><Paperclip class="h-4 w-4" /></button>
        <Button disabled={pending || failedFiles.length > 0 || (!feedback.trim() && !attachments.length)} onclick={submit}><Check class="h-4 w-4" />{t('feedback.submit')}</Button>
      </div>
    {/if}
    {#if error}<p class="mt-3 break-words text-sm text-destructive" role="alert">{error}</p>{/if}
    {#if comments.length}<details class="mt-5 border-t border-border pt-3"><summary class="cursor-pointer text-xs text-muted-foreground">{t('feedback.history')} ({comments.length})</summary><div class="divide-y divide-border">{#each comments as comment (comment.comment_id)}<div class="py-3"><p class="mb-1 text-xs text-muted-foreground">{comment.author_name || comment.author_id}</p><div class="prose max-w-none break-words text-sm dark:prose-invert">{@html renderSafeMarkdown(comment.body)}</div></div>{/each}</div></details>{/if}
  </section>
</div>
