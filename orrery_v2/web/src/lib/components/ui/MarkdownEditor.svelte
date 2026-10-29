<script lang="ts">
  import {
    AtSign,
    Bold,
    CheckSquare,
    Code2,
    Eye,
    FileUp,
    Heading1,
    Image,
    Italic,
    Link,
    List,
    ListOrdered,
    Smile
  } from 'lucide-svelte'
  import { renderSafeMarkdown } from '#lib/markdown'
  import Button from './Button.svelte'

  type ToolbarCommand =
    | 'bold'
    | 'italic'
    | 'code'
    | 'heading'
    | 'unorderedList'
    | 'orderedList'
    | 'taskList'
    | 'link'
    | 'image'
    | 'mention'
    | 'emoji'
    | 'attach'

  interface Props {
    value?: string
    ariaLabel: string
    placeholder?: string
    disabled?: boolean
    minHeight?: string
    writeLabel?: string
    previewLabel?: string
    variant?: 'default' | 'composer'
    toolbarLabels?: Partial<Record<ToolbarCommand, string>>
  }

  let {
    value = $bindable(''),
    ariaLabel,
    placeholder = '',
    disabled = false,
    minHeight = 'min-h-40',
    writeLabel = '编辑',
    previewLabel = '预览',
    variant = 'default',
    toolbarLabels = {}
  }: Props = $props()

  type Mode = 'write' | 'preview'

  let mode = $state<Mode>('write')
  let textareaRef: HTMLTextAreaElement | undefined = $state()
  const rendered = $derived(renderSafeMarkdown(value))
  const labels = $derived({
    bold: toolbarLabels.bold ?? '加粗',
    italic: toolbarLabels.italic ?? '斜体',
    code: toolbarLabels.code ?? '代码',
    heading: toolbarLabels.heading ?? '标题',
    unorderedList: toolbarLabels.unorderedList ?? '无序列表',
    orderedList: toolbarLabels.orderedList ?? '有序列表',
    taskList: toolbarLabels.taskList ?? '任务列表',
    link: toolbarLabels.link ?? '链接',
    image: toolbarLabels.image ?? '图片',
    mention: toolbarLabels.mention ?? '提及',
    emoji: toolbarLabels.emoji ?? '表情',
    attach: toolbarLabels.attach ?? '附件'
  })

  function applyInline(prefix: string, suffix = prefix, fallback = 'text') {
    if (disabled) return
    replaceSelection((selected) => `${prefix}${selected || fallback}${suffix}`, prefix.length, suffix.length, fallback)
  }

  function applyLine(prefix: string, fallback = 'text') {
    if (disabled) return
    replaceSelection((selected) => {
      const body = selected || fallback
      return body
        .split('\n')
        .map((line) => `${prefix}${line || fallback}`)
        .join('\n')
    }, prefix.length, 0, fallback)
  }

  function replaceSelection(format: (selected: string) => string, selectOffsetStart: number, selectOffsetEnd: number, fallback: string) {
    const textarea = textareaRef
    if (!textarea) {
      value += format(fallback)
      return
    }
    const start = textarea.selectionStart
    const end = textarea.selectionEnd
    const selected = value.slice(start, end)
    const inserted = format(selected)
    value = `${value.slice(0, start)}${inserted}${value.slice(end)}`
    queueMicrotask(() => {
      textarea.focus()
      if (selected) {
        textarea.setSelectionRange(start + selectOffsetStart, start + inserted.length - selectOffsetEnd)
        return
      }
      textarea.setSelectionRange(start + selectOffsetStart, start + selectOffsetStart + fallback.length)
    })
  }

  function insertText(text: string, cursorOffset = text.length) {
    if (disabled) return
    const textarea = textareaRef
    if (!textarea) {
      value += text
      return
    }
    const start = textarea.selectionStart
    const end = textarea.selectionEnd
    value = `${value.slice(0, start)}${text}${value.slice(end)}`
    queueMicrotask(() => {
      textarea.focus()
      textarea.setSelectionRange(start + cursorOffset, start + cursorOffset)
    })
  }
</script>

<div class="overflow-hidden rounded-md border border-border bg-background {variant === 'composer' ? 'shadow-inner' : ''}">
  <div class="flex items-center gap-1 border-b border-border {variant === 'composer' ? 'bg-background px-3 py-2' : 'bg-muted/20 px-2 py-1.5'}">
    <Button type="button" size="sm" variant={mode === 'write' ? 'secondary' : 'ghost'} onclick={() => (mode = 'write')} ariaLabel={writeLabel}>
      <Code2 class="h-3.5 w-3.5" />
      {writeLabel}
    </Button>
    <Button type="button" size="sm" variant={mode === 'preview' ? 'secondary' : 'ghost'} onclick={() => (mode = 'preview')} ariaLabel={previewLabel}>
      <Eye class="h-3.5 w-3.5" />
      {previewLabel}
    </Button>
  </div>

  {#if mode === 'write'}
    {#if variant === 'composer'}
      <div class="flex flex-wrap items-center gap-1 border-b border-border bg-background px-3 py-2">
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.bold} title={labels.bold} {disabled} onclick={() => applyInline('**', '**', '文本')}>
          <Bold class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.italic} title={labels.italic} {disabled} onclick={() => applyInline('_', '_', '文本')}>
          <Italic class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.code} title={labels.code} {disabled} onclick={() => applyInline('`', '`', 'code')}>
          <Code2 class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.heading} title={labels.heading} {disabled} onclick={() => applyLine('## ', '标题')}>
          <Heading1 class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.unorderedList} title={labels.unorderedList} {disabled} onclick={() => applyLine('- ', '列表项')}>
          <List class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.orderedList} title={labels.orderedList} {disabled} onclick={() => applyLine('1. ', '列表项')}>
          <ListOrdered class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.taskList} title={labels.taskList} {disabled} onclick={() => applyLine('- [ ] ', '待办项')}>
          <CheckSquare class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.link} title={labels.link} {disabled} onclick={() => insertText('[链接文本](https://)', 7)}>
          <Link class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.image} title={labels.image} {disabled} onclick={() => insertText('![图片描述](https://)', 8)}>
          <Image class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.mention} title={labels.mention} {disabled} onclick={() => insertText('@')}>
          <AtSign class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.emoji} title={labels.emoji} {disabled} onclick={() => insertText(':smile:')}>
          <Smile class="h-4 w-4" />
        </button>
        <button class="grid h-7 w-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" aria-label={labels.attach} title={labels.attach} {disabled} onclick={() => insertText('[附件](url)', 5)}>
          <FileUp class="h-4 w-4" />
        </button>
      </div>
    {/if}
    <textarea
      bind:this={textareaRef}
      class="{minHeight} w-full resize-y border-0 bg-background px-3 py-2 text-sm leading-6 text-foreground outline-none placeholder:text-muted-foreground disabled:bg-muted/25"
      aria-label={ariaLabel}
      {placeholder}
      {disabled}
      bind:value
    ></textarea>
  {:else}
    <article class="markdown-editor-preview {minHeight} overflow-auto px-3 py-2 text-sm leading-6 text-foreground">
      {@html rendered}
    </article>
  {/if}
</div>
