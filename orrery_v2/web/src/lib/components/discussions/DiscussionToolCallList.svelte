<script lang="ts">
  import { ChevronRight, ListChecks } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { DiscussionToolCallViewModel } from '#lib/types/discussions'

  type DisplayToolCall = DiscussionToolCallViewModel & {
    displayText?: string
    statusText?: string
    running?: boolean
    failed?: boolean
    showDetails?: boolean
  }

  interface Props {
    drawerId?: string
    toolCalls: DisplayToolCall[]
    isStreaming?: boolean
    /** Drawer mode: collapse all tools into a single collapsible group */
    collapsible?: boolean
    /** Default expanded state for drawer mode (undefined = auto-detect based on running state) */
    defaultExpanded?: boolean
  }

  let { drawerId = '', toolCalls, isStreaming = false, collapsible = false, defaultExpanded }: Props = $props()
  let expandedIds = $state<Record<string, boolean>>({})
  let drawerExpanded = $state(false)
  let initializedDrawerID = $state('')
  let appliedDefaultExpanded = $state<boolean | undefined>(undefined)
  let t = $derived(createTranslator($locale))

  $effect(() => {
    const currentDrawerID = drawerId || toolCalls[0]?.id || ''
    if (!collapsible) {
      initializedDrawerID = ''
      appliedDefaultExpanded = undefined
      return
    }
    if (initializedDrawerID === currentDrawerID && appliedDefaultExpanded === defaultExpanded) return
    drawerExpanded = defaultExpanded ?? toolCalls.some(isRunning)
    initializedDrawerID = currentDrawerID
    appliedDefaultExpanded = defaultExpanded
  })

  function isExpanded(id: string): boolean {
    return expandedIds[id] ?? false
  }

  function toggleExpanded(id: string) {
    expandedIds = { ...expandedIds, [id]: !expandedIds[id] }
  }

  function toggleDrawer() {
    drawerExpanded = !drawerExpanded
  }

  function isRunning(tool: DisplayToolCall) {
    if (tool.running !== undefined) return tool.running
    const status = tool.status.trim().toLowerCase()
    if (status === 'completed' || status === 'succeeded' || status === 'done') return false
    return status === 'running' || status === 'started' || status === 'pending' || status === 'todo' || (isStreaming && !tool.result)
  }

  function isFailed(tool: DisplayToolCall) {
    if (tool.failed !== undefined) return tool.failed
    const status = tool.status.trim().toLowerCase()
    return status === 'failed' || status === 'error' || status === 'timeout' || status === 'cancelled'
  }

  function argsPreview(tool: DisplayToolCall): string {
    const progress = streamingInputProgress(tool)
    if (progress) return `(${progress.characters} chars)`
    const args = visibleArguments(tool)
    if (!args || Object.keys(args).length === 0) return ''
    const entries = Object.entries(args)
      .slice(0, 3)
      .map(([key, value]) => `${key}=${quotePreview(value)}`)
      .join(', ')
    return `(${entries}${Object.keys(args).length > 3 ? ', ...' : ''})`
  }

  function quotePreview(value: string) {
    const display = String(value)
    const clipped = display.length > 24 ? `${display.slice(0, 24)}...` : display
    return `"${clipped}"`
  }

  function formatRecord(record: Record<string, string> | undefined): string {
    if (!record || Object.keys(record).length === 0) return ''
    return JSON.stringify(record, null, 2)
  }

  function streamingInputProgress(tool: DisplayToolCall) {
    const args = tool.arguments
    if (!args || args.__orrery_streaming_input !== 'true') return undefined
    return {
      characters: Number.parseInt(args.__orrery_input_characters || '0', 10) || 0,
      preview: args.__orrery_input_preview || ''
    }
  }

  function visibleArguments(tool: DisplayToolCall) {
    if (!tool.arguments) return undefined
    return Object.fromEntries(Object.entries(tool.arguments).filter(([key]) => !key.startsWith('__orrery_')))
  }

  function toolText(tool: DisplayToolCall) {
    return tool.displayText?.trim() || tool.name || 'tool'
  }

  function toolStatusText(tool: DisplayToolCall) {
    return tool.statusText?.trim() || ''
  }

  function lineClass(index: number) {
    if (toolCalls.length <= 1) return ''
    if (index === 0) return 'top-[calc(50%+5px)] bottom-0'
    if (index === toolCalls.length - 1) return 'top-0 bottom-[calc(50%+5px)]'
    return 'top-0 bottom-0'
  }

  // Count tools by status
  const runningCount = $derived(toolCalls.filter(isRunning).length)
  const failedCount = $derived(toolCalls.filter(isFailed).length)
  const completedCount = $derived(toolCalls.length - runningCount - failedCount)
</script>

{#if collapsible && toolCalls.length > 1}
  <div class="discussion-tool-region my-0.5 w-full max-w-xl" data-testid="discussion-tool-call-drawer">
    <button
      type="button"
      class="group flex min-h-7 w-full items-center gap-1.5 rounded-md px-1 text-left transition-colors hover:bg-muted/50"
      aria-expanded={drawerExpanded}
      aria-label="Tool calls"
      onclick={toggleDrawer}
    >
      <ListChecks class="h-3.5 w-3.5 shrink-0 text-[#737373]" />
      <span class="truncate text-[12.5px] font-medium leading-none text-[#404040] dark:text-foreground">
        {t('discussionSession.agentWorkToolCalls')}
      </span>
      <span class="text-[10px] leading-none text-muted-foreground/45">·</span>
      {#if runningCount > 0}
        <span class="min-w-0 truncate text-[10.5px] font-normal leading-none tabular-nums text-blue-600">{t('discussion.toolStatusRunning', { count: runningCount })}</span>
      {/if}
      {#if failedCount > 0}
        {#if runningCount > 0}<span class="text-[10px] leading-none text-muted-foreground/45">·</span>{/if}
        <span class="min-w-0 truncate text-[10.5px] font-normal leading-none tabular-nums text-destructive">{t('discussion.toolStatusFailed', { count: failedCount })}</span>
      {/if}
      {#if completedCount > 0}
        {#if runningCount > 0 || failedCount > 0}<span class="text-[10px] leading-none text-muted-foreground/45">·</span>{/if}
        <span class="min-w-0 truncate text-[10.5px] font-normal leading-none tabular-nums text-[#858585] dark:text-muted-foreground">{t('discussion.toolStatusCompleted', { count: completedCount })}</span>
      {/if}
      <ChevronRight class={`ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-hover:text-foreground ${drawerExpanded ? 'rotate-90' : ''}`} />
    </button>

    {#if drawerExpanded}
      <div class="ml-2 border-l border-border/80 py-1 pl-3" data-tool-call-drawer-content>
        {#each toolCalls as tool, index (tool.id)}
          {@const expanded = isExpanded(tool.id)}
          {@const running = isRunning(tool)}
          {@const failed = isFailed(tool)}
          {@const inputProgress = streamingInputProgress(tool)}
          <div class="flex items-stretch">
            <div class="relative flex w-4 shrink-0 items-center justify-center">
              <span
                class="discussion-tool-dot relative z-10"
                class:discussion-tool-dot-running={running}
                class:discussion-tool-dot-failed={failed}
                class:discussion-tool-dot-done={!running && !failed}
              ></span>
              {#if toolCalls.length > 1}
                <div
                  class={`absolute left-1/2 w-px -translate-x-1/2 bg-border ${lineClass(index)}`}
                ></div>
              {/if}
            </div>

            <div class="min-w-0 flex-1">
              {#if tool.showDetails !== false}
                <button
                  type="button"
                  class="group flex min-h-7 w-full items-center gap-1.5 overflow-hidden rounded-md px-1 text-left text-[12px] transition-colors hover:bg-muted/60"
                  class:text-destructive={failed}
                  class:text-muted-foreground={!failed}
                  aria-expanded={expanded}
                  aria-label={`${toolText(tool)} ${tool.status}`}
                  onclick={() => toggleExpanded(tool.id)}
                >
                  <span class="min-w-0 truncate font-medium text-foreground" class:discussion-tool-text-running={running}>{toolText(tool)}</span>
                  <span class="min-w-0 truncate font-mono text-[11px] text-muted-foreground">{failed ? tool.status || 'failed' : argsPreview(tool)}</span>
                  {#if toolStatusText(tool)}
                    <span class="ml-auto shrink-0 text-[11px] text-muted-foreground" class:text-destructive={failed}>{toolStatusText(tool)}</span>
                  {/if}
                  <ChevronRight class={`h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-hover:text-foreground ${expanded ? 'rotate-90' : ''}`} />
                </button>
              {:else}
                <div class="flex min-h-7 w-full items-center gap-1.5 overflow-hidden px-1 text-[12px]" class:text-destructive={failed} class:text-muted-foreground={!failed}>
                  <span class="min-w-0 truncate font-medium text-foreground">{toolText(tool)}</span>
                  <span class="ml-auto shrink-0 text-[11px] text-muted-foreground" class:text-destructive={failed}>{toolStatusText(tool)}</span>
                </div>
              {/if}

              {#if expanded}
                <div class="space-y-2 pb-2 pl-3">
                  {#if inputProgress}
                    <div data-testid={`discussion-tool-input-progress-${tool.id}`}>
                      <div class="mb-1 flex items-center justify-between gap-2 text-[10px] font-medium uppercase text-muted-foreground">
                        <span>{t('discussion.toolInputStreaming')}</span>
                        <span class="normal-case tabular-nums">{t('discussion.toolInputCharacters', { count: inputProgress.characters })}</span>
                      </div>
                      {#if inputProgress.preview}
                        <pre class="subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2.5 font-mono text-[11px] leading-relaxed text-foreground">{inputProgress.preview}</pre>
                      {/if}
                    </div>
                  {/if}
                  {#if formatRecord(visibleArguments(tool))}
                    <div>
                      <div class="mb-1 text-[10px] font-medium uppercase text-muted-foreground">{t('discussion.toolArguments')}</div>
                      <pre class="subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2.5 font-mono text-[11px] leading-relaxed text-foreground">{formatRecord(visibleArguments(tool))}</pre>
                    </div>
                  {/if}
                  {#if !running || formatRecord(tool.result)}
                    <div>
                      <div class="mb-1 text-[10px] font-medium uppercase" class:text-destructive={failed} class:text-muted-foreground={!failed}>{t('discussion.toolResult')}</div>
                      <pre
                        class={`subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md p-2.5 font-mono text-[11px] leading-relaxed ${failed ? 'bg-destructive/10 text-destructive' : 'bg-muted/40 text-foreground'}`}
                      >{failed ? (formatRecord(tool.result) || t('discussion.toolFailedNoResult')) : (formatRecord(tool.result) || t('discussion.toolNoResult'))}</pre>
                    </div>
                  {/if}
                </div>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{:else}
  <div class="discussion-tool-region my-0.5 w-full" data-testid="discussion-tool-call-list">
    {#each toolCalls as tool, index (tool.id)}
      {@const expanded = isExpanded(tool.id)}
      {@const running = isRunning(tool)}
      {@const failed = isFailed(tool)}
      {@const inputProgress = streamingInputProgress(tool)}
      <div class="flex items-stretch">
        <div class="relative flex w-4 shrink-0 items-center justify-center">
          <span
            class="discussion-tool-dot relative z-10"
            class:discussion-tool-dot-running={running}
            class:discussion-tool-dot-failed={failed}
            class:discussion-tool-dot-done={!running && !failed}
          ></span>
          {#if toolCalls.length > 1}
            <div
              class={`absolute left-1/2 w-px -translate-x-1/2 bg-border ${lineClass(index)}`}
            ></div>
          {/if}
        </div>

        <div class="min-w-0 flex-1">
          {#if tool.showDetails !== false}
            <button
              type="button"
              class="group flex min-h-7 w-full items-center gap-1.5 overflow-hidden rounded-md px-1 text-left text-[12px] transition-colors hover:bg-muted/60"
              class:text-destructive={failed}
              class:text-muted-foreground={!failed}
              aria-expanded={expanded}
              aria-label={`${toolText(tool)} ${tool.status}`}
              onclick={() => toggleExpanded(tool.id)}
            >
              <span class="min-w-0 truncate font-medium text-foreground" class:discussion-tool-text-running={running}>{toolText(tool)}</span>
              <span class="min-w-0 truncate font-mono text-[11px] text-muted-foreground">{failed ? tool.status || 'failed' : argsPreview(tool)}</span>
              {#if toolStatusText(tool)}
                <span class="ml-auto shrink-0 text-[11px] text-muted-foreground" class:text-destructive={failed}>{toolStatusText(tool)}</span>
              {/if}
              <ChevronRight class={`h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-hover:text-foreground ${expanded ? 'rotate-90' : ''}`} />
            </button>
          {:else}
            <div class="flex min-h-7 w-full items-center gap-1.5 overflow-hidden px-1 text-[12px]" class:text-destructive={failed} class:text-muted-foreground={!failed}>
              <span class="min-w-0 truncate font-medium text-foreground">{toolText(tool)}</span>
              <span class="ml-auto shrink-0 text-[11px] text-muted-foreground" class:text-destructive={failed}>{toolStatusText(tool)}</span>
            </div>
          {/if}

          {#if expanded}
            <div class="space-y-2 pb-2 pl-3">
              {#if inputProgress}
                <div data-testid={`discussion-tool-input-progress-${tool.id}`}>
                  <div class="mb-1 flex items-center justify-between gap-2 text-[10px] font-medium uppercase text-muted-foreground">
                    <span>{t('discussion.toolInputStreaming')}</span>
                    <span class="normal-case tabular-nums">{t('discussion.toolInputCharacters', { count: inputProgress.characters })}</span>
                  </div>
                  {#if inputProgress.preview}
                    <pre class="subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2.5 font-mono text-[11px] leading-relaxed text-foreground">{inputProgress.preview}</pre>
                  {/if}
                </div>
              {/if}
              {#if formatRecord(visibleArguments(tool))}
                <div>
                  <div class="mb-1 text-[10px] font-medium uppercase text-muted-foreground">{t('discussion.toolArguments')}</div>
                  <pre class="subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2.5 font-mono text-[11px] leading-relaxed text-foreground">{formatRecord(visibleArguments(tool))}</pre>
                </div>
              {/if}
              {#if !running || formatRecord(tool.result)}
                <div>
                  <div class="mb-1 text-[10px] font-medium uppercase" class:text-destructive={failed} class:text-muted-foreground={!failed}>{t('discussion.toolResult')}</div>
                  <pre
                    class={`subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md p-2.5 font-mono text-[11px] leading-relaxed ${failed ? 'bg-destructive/10 text-destructive' : 'bg-muted/40 text-foreground'}`}
                  >{failed ? (formatRecord(tool.result) || t('discussion.toolFailedNoResult')) : (formatRecord(tool.result) || t('discussion.toolNoResult'))}</pre>
                </div>
              {/if}
            </div>
          {/if}
        </div>
      </div>
    {/each}
  </div>
{/if}
