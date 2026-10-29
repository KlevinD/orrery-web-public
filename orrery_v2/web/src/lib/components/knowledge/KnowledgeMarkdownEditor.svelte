<script lang="ts">
  import { onMount } from 'svelte'
  import { BookOpen, Code2, Columns2 } from 'lucide-svelte'
  import type { EditorView } from '@codemirror/view'
  import { renderSafeMarkdown } from '#lib/markdown'

  interface Props {
    value?: string
    ariaLabel: string
    disabled?: boolean
    readOnly?: boolean
    contentKind?: 'markdown' | 'plain'
    formatLabel?: string
    minHeight?: string
  }

  let {
    value = $bindable(''),
    ariaLabel,
    disabled = false,
    readOnly = false,
    contentKind = 'markdown',
    formatLabel = 'Markdown',
    minHeight = 'min-h-80'
  }: Props = $props()

  type ViewMode = 'edit' | 'split' | 'read'

  let mode = $state<ViewMode>('edit')
  let editorHost = $state<HTMLDivElement>()
  let editorView: EditorView | undefined
  let configuredDisabled: boolean | undefined
  let reconfigureEditable: ((nextDisabled: boolean) => void) | undefined
  let editorReady = $state(false)
  let editorError = $state('')
  const rendered = $derived(renderSafeMarkdown(value))

  onMount(() => {
    if (!editorHost || readOnly) return
    let disposed = false

    void Promise.all([
      import('@codemirror/commands'),
      import('@codemirror/lang-markdown'),
      import('@codemirror/language'),
      import('@codemirror/state'),
      import('@codemirror/view')
    ]).then(([commands, markdownLanguage, language, state, view]) => {
      if (disposed || !editorHost) return
      configuredDisabled = disabled
      const editable = new state.Compartment()

      editorView = new view.EditorView({
        parent: editorHost,
        state: state.EditorState.create({
          doc: value,
          extensions: [
            view.highlightSpecialChars(),
            commands.history(),
            view.drawSelection(),
            view.dropCursor(),
            state.EditorState.allowMultipleSelections.of(true),
            language.indentOnInput(),
            language.syntaxHighlighting(language.defaultHighlightStyle, { fallback: true }),
            language.bracketMatching(),
            view.highlightActiveLine(),
            view.keymap.of([...commands.defaultKeymap, ...commands.historyKeymap, commands.indentWithTab]),
            markdownLanguage.markdown(),
            view.EditorView.lineWrapping,
            editable.of(view.EditorView.editable.of(!disabled)),
            view.EditorView.contentAttributes.of({
              'aria-label': ariaLabel,
              'aria-multiline': 'true',
              spellcheck: 'true'
            }),
            view.EditorView.updateListener.of((update) => {
              if (update.docChanged) value = update.state.doc.toString()
            }),
            view.EditorView.theme({
              '&': {
                height: '100%',
                color: 'var(--foreground)',
                backgroundColor: 'transparent',
                fontSize: '14px'
              },
              '&.cm-focused': { outline: 'none' },
              '.cm-scroller': {
                minHeight: 'inherit',
                fontFamily: 'var(--font-sans, "Segoe UI", "Microsoft YaHei", sans-serif)',
                lineHeight: '1.75'
              },
              '.cm-content': {
                minHeight: 'inherit',
                padding: '20px 24px',
                caretColor: 'var(--brand)'
              },
              '.cm-line': { padding: '0 2px' },
              '.cm-gutters': { display: 'none' },
              '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--muted) 32%, transparent)' },
              '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': {
                backgroundColor: 'color-mix(in srgb, var(--brand) 16%, transparent) !important'
              },
              '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--brand)' },
              '.cm-placeholder': { color: 'color-mix(in srgb, var(--muted-foreground) 72%, transparent)' },
              '.cm-panels': { backgroundColor: 'var(--background)', color: 'var(--foreground)' },
              '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--warning) 18%, transparent)' },
              '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'color-mix(in srgb, var(--brand) 20%, transparent)' }
            })
          ]
        })
      })
      reconfigureEditable = (nextDisabled) => {
        editorView?.dispatch({ effects: editable.reconfigure(view.EditorView.editable.of(!nextDisabled)) })
      }
      editorReady = true
    }).catch((error: unknown) => {
      if (!disposed) editorError = error instanceof Error ? error.message : '编辑器加载失败'
    })

    return () => {
      disposed = true
      editorView?.destroy()
      editorView = undefined
      reconfigureEditable = undefined
    }
  })

  $effect(() => {
    const nextValue = value
    const nextDisabled = disabled
    editorReady
    const view = editorView
    if (!view) return

    if (view.state.doc.toString() !== nextValue) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: nextValue } })
    }
    if (configuredDisabled !== nextDisabled) {
      configuredDisabled = nextDisabled
      reconfigureEditable?.(nextDisabled)
    }
  })

  $effect(() => {
    if (readOnly && mode !== 'read') mode = 'read'
  })

  $effect(() => {
    mode
    queueMicrotask(() => editorView?.requestMeasure())
  })
</script>

<section class="flex {minHeight} min-h-0 flex-col overflow-hidden rounded-md border border-border bg-background">
  <header class="flex h-11 shrink-0 items-center justify-between gap-3 border-b border-border bg-muted/15 px-3">
    {#if readOnly}
      <div class="inline-flex h-7 items-center gap-1.5 px-2 text-xs font-medium text-foreground">
        <BookOpen class="h-3.5 w-3.5" />
        <span>阅读模式</span>
      </div>
    {:else}
      <div class="inline-flex items-center rounded-md bg-muted/60 p-0.5" aria-label="文档视图">
        <button type="button" class={mode === 'edit' ? 'selection-wash active' : ''} onclick={() => (mode = 'edit')} title="编辑视图" aria-label="编辑视图">
          <Code2 class="h-3.5 w-3.5" />
          <span>编辑</span>
        </button>
        <button type="button" class={mode === 'split' ? 'selection-wash active' : ''} onclick={() => (mode = 'split')} title="分屏视图" aria-label="分屏视图">
          <Columns2 class="h-3.5 w-3.5" />
          <span>分屏</span>
        </button>
        <button type="button" class={mode === 'read' ? 'selection-wash active' : ''} onclick={() => (mode = 'read')} title="阅读视图" aria-label="阅读视图">
          <BookOpen class="h-3.5 w-3.5" />
          <span>阅读</span>
        </button>
      </div>
    {/if}
    <span class="text-[11px] text-muted-foreground">{readOnly ? '只读 · ' : ''}{formatLabel}</span>
  </header>

  <div class="grid min-h-0 flex-1 {mode === 'split' ? 'md:grid-cols-2' : ''}">
    <div class:hidden={mode === 'read'} class="editor-pane min-h-0 overflow-hidden {mode === 'split' ? 'border-r border-border' : ''}">
      {#if editorError}
        <div class="grid h-full min-h-48 place-items-center p-6 text-sm text-destructive">{editorError}</div>
      {:else if !editorReady}
        <div class="absolute inset-0 grid place-items-center bg-background text-sm text-muted-foreground">正在加载编辑器...</div>
      {/if}
      <div bind:this={editorHost} class="cm-host h-full min-h-[inherit]"></div>
    </div>

    <article
      class:hidden={mode === 'edit'}
      class:compact-preview={mode === 'split'}
      class="knowledge-markdown-preview min-h-0 overflow-auto px-7 py-6 text-sm leading-7 text-foreground"
      aria-label="文档阅读视图"
    >
      {#if contentKind === 'markdown'}
        {@html rendered}
      {:else}
        <div class="knowledge-plain-text">{value}</div>
      {/if}
    </article>
  </div>
</section>

<style>
  header button {
    display: inline-flex;
    height: 1.75rem;
    align-items: center;
    gap: 0.35rem;
    border-radius: 0.25rem;
    padding: 0 0.55rem;
    font-size: 0.72rem;
    color: var(--muted-foreground);
  }
  header button:hover { color: var(--foreground); }
  header button.active {
    color: var(--foreground);
    font-weight: 500;
  }
  .editor-pane, .cm-host { min-height: inherit; }
  .editor-pane { position: relative; }
  .hidden { display: none; }
  .knowledge-markdown-preview :global(h1) {
    margin: 0 0 1.1rem;
    border-bottom: 1px solid var(--border);
    padding-bottom: 0.65rem;
    font-size: 1.45rem;
    font-weight: 650;
    line-height: 1.3;
  }
  .knowledge-markdown-preview :global(h2) {
    margin: 1.65rem 0 0.7rem;
    font-size: 1.08rem;
    font-weight: 650;
    line-height: 1.4;
  }
  .knowledge-markdown-preview :global(h3) {
    margin: 1.35rem 0 0.55rem;
    font-size: 0.95rem;
    font-weight: 650;
  }
  .knowledge-markdown-preview :global(p) { margin: 0.65rem 0; }
  .knowledge-markdown-preview :global(ul),
  .knowledge-markdown-preview :global(ol) {
    margin: 0.65rem 0;
    padding-left: 1.35rem;
  }
  .knowledge-markdown-preview :global(ul) { list-style: disc; }
  .knowledge-markdown-preview :global(ol) { list-style: decimal; }
  .knowledge-markdown-preview :global(li) { margin: 0.25rem 0; }
  .knowledge-markdown-preview :global(blockquote) {
    margin: 1rem 0;
    border-left: 3px solid color-mix(in srgb, var(--brand) 38%, transparent);
    padding-left: 0.9rem;
    color: var(--muted-foreground);
  }
  .knowledge-markdown-preview :global(code) {
    border-radius: 0.25rem;
    background: var(--muted);
    padding: 0.12rem 0.3rem;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 0.85em;
  }
  .knowledge-markdown-preview :global(pre) {
    margin: 1rem 0;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: 0.375rem;
    background: color-mix(in srgb, var(--muted) 50%, transparent);
    padding: 0.9rem;
  }
  .knowledge-markdown-preview :global(pre code) { background: transparent; padding: 0; }
  .knowledge-markdown-preview :global(a) { color: var(--brand); text-decoration: underline; }
  .knowledge-plain-text { white-space: pre-wrap; overflow-wrap: anywhere; }
  .compact-preview :global(h1) { font-size: 1.08rem; }
  .compact-preview :global(h2) { margin-top: 1.25rem; font-size: 0.92rem; }
</style>
