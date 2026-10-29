<script lang="ts">
  import {
    ArrowLeft,
    CheckCircle2,
    CircleDot,
    Download,
    FileJson,
    ListChecks,
    Monitor,
    Play,
    ShieldCheck,
    Sparkles,
    Square,
    Upload
  } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import {
    buildSkillDraft,
    createDemoRecording,
    importRecordingFile,
    probeSkillRecorder,
    RECORDER_TOOL_OPTIONS,
    startNativeRecording,
    stopNativeRecording,
    type RecorderEvent,
    type RecordingSession,
    type SkillRecorderDraft,
    type SkillRecorderForm
  } from '#lib/recorder/skillRecorder'
  import type { ResourceSkillCommandFormViewModel } from '#lib/types/view-models'

  interface Props {
    open?: boolean
    busy?: boolean
    oncreate?: (form: ResourceSkillCommandFormViewModel) => void | Promise<void>
    onclose?: () => void
  }

  let {
    open = false,
    busy = false,
    oncreate,
    onclose
  }: Props = $props()

  type Stage = 'source' | 'recording' | 'review' | 'result'

  let stage = $state<Stage>('source')
  let session = $state<RecordingSession | undefined>()
  let draft = $state<SkillRecorderDraft | undefined>()
  let goal = $state('客户信息整理流程')
  let toolOverrides = $state<Record<string, string>>({})
  let bridge = $state({ available: false, label: '检测中', detail: '正在检查本地录制器桥接' })
  let error = $state('')
  let processing = $state(false)
  let creating = $state(false)
  let created = $state(false)
  let recordingStartedAt = $state<number | undefined>()
  let elapsedSeconds = $state(0)
  let nativeSessionId = $state<string | undefined>()
  let fileInput = $state<HTMLInputElement>()
  let resetKey = $state('')

  const generatedDraft = $derived(session ? buildSkillDraft(session, goal, toolOverrides) : undefined)

  $effect(() => {
    const nextKey = open ? 'open' : 'closed'
    if (nextKey === resetKey) return
    resetKey = nextKey
    if (!open) return
    stage = 'source'
    session = undefined
    draft = undefined
    goal = '客户信息整理流程'
    toolOverrides = {}
    error = ''
    processing = false
    creating = false
    created = false
    recordingStartedAt = undefined
    elapsedSeconds = 0
    nativeSessionId = undefined
    void probeSkillRecorder().then((result) => (bridge = result))
  })

  $effect(() => {
    if (stage !== 'recording' || !recordingStartedAt) return
    const timer = window.setInterval(() => {
      elapsedSeconds = Math.floor((Date.now() - recordingStartedAt!) / 1000)
    }, 1000)
    return () => window.clearInterval(timer)
  })

  function handleClose() {
    if (stage === 'recording') {
      error = '请先停止录制，再关闭窗口'
      return
    }
    onclose?.()
  }

  async function startNative() {
    processing = true
    error = ''
    try {
      const probe = await probeSkillRecorder()
      bridge = probe
      if (!probe.available) {
        error = '未检测到本地录制器桥接。可以导入 Skill Recorder JSON，或先加载演示会话。'
        return
      }
      const nextSession = await startNativeRecording(goal.trim() || '未命名录制流程')
      session = nextSession
      nativeSessionId = nextSession.id
      recordingStartedAt = Date.now()
      stage = 'recording'
    } catch (startError) {
      error = startError instanceof Error ? startError.message : '本地录制器启动失败'
    } finally {
      processing = false
    }
  }

  async function stopNative() {
    if (!session) return
    processing = true
    error = ''
    try {
      session = await stopNativeRecording(nativeSessionId ?? session.id)
      recordingStartedAt = undefined
      stage = 'review'
    } catch (stopError) {
      error = stopError instanceof Error ? stopError.message : '本地录制器停止失败'
    } finally {
      processing = false
    }
  }

  function useDemo() {
    error = ''
    session = createDemoRecording()
    stage = 'review'
  }

  async function importFile(file: File) {
    processing = true
    error = ''
    try {
      session = await importRecordingFile(file)
      stage = 'review'
    } catch (fileError) {
      error = fileError instanceof Error ? fileError.message : '录制文件导入失败'
    } finally {
      processing = false
      if (fileInput) fileInput.value = ''
    }
  }

  function handleFileChange(event: Event & { currentTarget: HTMLInputElement }) {
    const file = event.currentTarget.files?.[0]
    if (file) void importFile(file)
  }

  function setTool(stepId: string, tool: string) {
    toolOverrides = { ...toolOverrides, [stepId]: tool }
  }

  function generateDraft() {
    if (!generatedDraft) return
    draft = generatedDraft
    stage = 'result'
  }

  async function createSkill() {
    if (!draft || busy || creating) return
    creating = true
    error = ''
    try {
      const form: ResourceSkillCommandFormViewModel = {
        name: draft.name,
        description: draft.description,
        version: draft.version,
        taskTypeTags: draft.taskTypeTags,
        prompt: draft.prompt
      }
      await oncreate?.(form)
      created = true
    } catch (createError) {
      error = createError instanceof Error ? createError.message : '技能创建失败'
    } finally {
      creating = false
    }
  }

  function downloadDraft() {
    if (!draft) return
    const blob = new Blob([draft.markdown], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${draft.name.replace(/[^\w\u4e00-\u9fff-]+/g, '-') || 'skill'}.md`
    anchor.click()
    URL.revokeObjectURL(url)
  }

  function eventLabel(event: RecorderEvent) {
    if (event.kind === 'app.activate') return '应用激活'
    if (event.kind === 'app.title-change') return '窗口变化'
    if (event.kind === 'browser.url') return '浏览器地址'
    if (event.kind === 'clipboard.change') return '剪贴板变化'
    if (event.kind === 'narration') return '语音旁白'
    if (event.kind === 'frame') return '页面画面'
    return '操作事件'
  }

  function eventSummary(event: RecorderEvent) {
    if (event.redacted) return '剪贴板内容已脱敏'
    return event.text ?? event.detail ?? event.title ?? event.url ?? event.app ?? '已记录事件'
  }

  function formatTime(value: string) {
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return '--:--:--'
    return new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
  }
</script>

<Dialog open={open} title="录制生成技能" size="wide" onClose={handleClose}>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4 rounded-lg border border-border bg-muted/30 px-4 py-3">
      <div>
        <div class="flex items-center gap-2 text-sm font-medium text-foreground">
          <Sparkles class="h-4 w-4 text-brand" />
          Skill Recorder · {stage === 'source' ? '选择录制来源' : stage === 'recording' ? '正在录制' : stage === 'review' ? '复核录制步骤' : '技能草稿已生成'}
        </div>
        <div class="mt-1 text-xs text-muted-foreground">事件时间线 → 工具映射 → Orrery 技能包</div>
      </div>
      <span class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-background px-2.5 py-1 text-[11px] text-muted-foreground">
        <CircleDot class={bridge.available ? 'h-3 w-3 text-emerald-500' : 'h-3 w-3 text-muted-foreground/60'} />
        {bridge.label}
      </span>
    </div>

    {#if stage === 'source'}
      <div class="space-y-4">
        <div class="grid gap-3 sm:grid-cols-2">
          <section class="rounded-lg border border-border bg-card p-4">
            <div class="flex items-start justify-between gap-3">
              <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-brand/10 text-brand"><Monitor class="h-4 w-4" /></div>
              <span class="text-[11px] text-muted-foreground">{bridge.label}</span>
            </div>
            <h3 class="mt-3 text-sm font-medium text-foreground">本地录制器</h3>
            <p class="mt-1 min-h-9 text-xs leading-5 text-muted-foreground">{bridge.detail}</p>
            <Button class="mt-4 w-full" onclick={() => void startNative()} disabled={processing}>
              <Play class="h-3.5 w-3.5" />
              连接并开始
            </Button>
          </section>

          <section class="rounded-lg border border-border bg-card p-4">
            <div class="flex items-start justify-between gap-3">
              <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-muted text-muted-foreground"><FileJson class="h-4 w-4" /></div>
              <span class="text-[11px] text-muted-foreground">离线可用</span>
            </div>
            <h3 class="mt-3 text-sm font-medium text-foreground">导入录制事件</h3>
            <p class="mt-1 min-h-9 text-xs leading-5 text-muted-foreground">导入包含 events 或 timeline 的 Skill Recorder JSON。</p>
            <Button variant="outline" class="mt-4 w-full" onclick={() => fileInput?.click()} disabled={processing}>
              <Upload class="h-3.5 w-3.5" />
              选择 JSON 文件
            </Button>
            <input bind:this={fileInput} type="file" class="hidden" accept=".json,application/json" aria-label="选择录制 JSON 文件" onchange={handleFileChange} />
          </section>
        </div>

        <div class="flex items-center justify-between gap-3 rounded-lg border border-dashed border-border bg-muted/20 px-4 py-3">
          <div class="flex min-w-0 items-center gap-2.5">
            <Play class="h-4 w-4 shrink-0 text-muted-foreground" />
            <div class="min-w-0">
              <div class="text-xs text-foreground">先用演示会话查看完整链路</div>
              <div class="truncate text-[11px] text-muted-foreground">包含浏览器、旁白、页面状态和审批动作</div>
            </div>
          </div>
          <Button variant="outline" size="sm" onclick={useDemo}>加载演示</Button>
        </div>

        <div class="flex items-start gap-2 rounded-lg bg-emerald-500/5 px-3.5 py-3 text-xs leading-5 text-muted-foreground">
          <ShieldCheck class="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" />
          <span>剪贴板内容进入 Orrery 前自动脱敏；外部录制器只通过本地桥接提供事件，不在前端保存原始视频。</span>
        </div>
      </div>
    {:else if stage === 'recording' && session}
      <div class="flex min-h-[360px] flex-col items-center justify-center text-center">
        <div class="flex h-16 w-16 items-center justify-center rounded-full bg-destructive/10 text-destructive"><CircleDot class="h-8 w-8 animate-pulse" /></div>
        <div class="mt-5 text-lg font-medium text-foreground">录制中</div>
        <div class="mt-1 font-mono text-2xl tracking-wide text-muted-foreground">
          {String(Math.floor(elapsedSeconds / 60)).padStart(2, '0')}:{String(elapsedSeconds % 60).padStart(2, '0')}
        </div>
        <div class="mt-3 text-xs text-muted-foreground">{session.name} · 本地录制器</div>
        <Button class="mt-7" onclick={() => void stopNative()} disabled={processing}>
          <Square class="h-3.5 w-3.5 fill-current" />
          停止并分析
        </Button>
      </div>
    {:else if stage === 'review' && session}
      <div class="space-y-4">
        <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_220px]">
          <label class="grid gap-1.5 text-xs font-medium text-foreground">
            <span>技能目标</span>
            <Input value={goal} ariaLabel="技能目标" oninput={(event) => (goal = event.currentTarget.value)} />
          </label>
          <div class="rounded-lg border border-border bg-muted/20 px-3 py-2">
            <div class="text-[11px] text-muted-foreground">录制摘要</div>
            <div class="mt-1 text-sm text-foreground">{session.events.length} 个事件</div>
            <div class="mt-0.5 text-[11px] text-muted-foreground">{session.source === 'demo' ? '演示会话' : session.source === 'imported' ? '导入会话' : '本地录制器'}</div>
          </div>
        </div>

        <section class="overflow-hidden rounded-lg border border-border bg-card">
          <div class="flex items-center justify-between border-b border-border/70 px-4 py-3">
            <div class="flex items-center gap-2 text-sm font-medium text-foreground"><ListChecks class="h-4 w-4 text-muted-foreground" />时间线与工具映射</div>
            <span class="text-[11px] text-muted-foreground">需人工复核</span>
          </div>
          <div class="divide-y divide-border/60">
            {#if generatedDraft}
              {#each generatedDraft.steps as step, index (step.id)}
                {@const event = session.events[index]}
                <div class="grid gap-2.5 px-4 py-3 sm:grid-cols-[28px_minmax(0,1fr)_188px] sm:items-center">
                  <div class="flex h-6 w-6 items-center justify-center rounded-full bg-muted font-mono text-[10px] text-muted-foreground">{index + 1}</div>
                  <div class="min-w-0">
                    <div class="truncate text-xs text-foreground">{step.intent}</div>
                    <div class="mt-0.5 flex min-w-0 items-center gap-2 text-[10.5px] text-muted-foreground">
                      <span>{eventLabel(event)}</span>
                      <span class="truncate">{eventSummary(event)}</span>
                      {#if step.requiresApproval}<span class="shrink-0 text-amber-600">需审批</span>{/if}
                      <span class="hidden shrink-0 sm:inline">{formatTime(event.timestamp)}</span>
                    </div>
                  </div>
                  <select
                    value={toolOverrides[step.id] ?? step.recommendedTool}
                    aria-label={`工具映射 ${index + 1}`}
                    onchange={(event) => setTool(step.id, event.currentTarget.value)}
                    class="w-full rounded-md border border-border bg-input-background px-2 py-1.5 font-mono text-[10.5px] text-foreground outline-none focus-visible:border-foreground/30"
                  >
                    {#each RECORDER_TOOL_OPTIONS as tool}<option value={tool}>{tool}</option>{/each}
                  </select>
                </div>
              {/each}
            {/if}
          </div>
        </section>

        <div class="flex items-start gap-2 rounded-lg bg-amber-500/5 px-3.5 py-3 text-xs text-muted-foreground">
          <ShieldCheck class="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
          <span>导出、提交等副作用步骤会在技能执行时请求 Orrery 运行时审批。</span>
        </div>
      </div>
    {:else if stage === 'result' && draft}
      <div class="space-y-4">
        <div class="flex items-start gap-3 rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-4 py-3">
          <CheckCircle2 class="mt-0.5 h-5 w-5 shrink-0 text-emerald-600" />
          <div>
            <div class="text-sm font-medium text-foreground">{draft.name}</div>
            <div class="mt-0.5 text-xs text-muted-foreground">{draft.version} · {draft.steps.length} 个步骤 · {created ? '已写入技能注册表' : '待写入技能注册表'}</div>
          </div>
        </div>
        <section class="overflow-hidden rounded-lg border border-border bg-[#1f1f1f]">
          <div class="flex items-center justify-between border-b border-white/10 px-4 py-2.5 text-[11px] text-white/60">
            <span>SKILL.md</span>
            <button type="button" class="flex items-center gap-1 text-white/70 hover:text-white" onclick={downloadDraft}><Download class="h-3.5 w-3.5" />下载</button>
          </div>
          <pre class="max-h-[310px] overflow-auto whitespace-pre-wrap px-4 py-3 font-mono text-[11px] leading-5 text-white/80">{draft.markdown}</pre>
        </section>
      </div>
    {/if}

    {#if error}
      <div class="rounded-lg border border-destructive/20 bg-destructive/5 px-3.5 py-2.5 text-xs text-destructive">{error}</div>
    {/if}

    <div class="flex items-center justify-between gap-3 border-t border-border/70 pt-4">
      {#if stage === 'source'}
        <span class="text-[11px] text-muted-foreground">输入：本地桥接 / Skill Recorder JSON · 输出：Orrery 技能包</span>
      {:else if stage === 'recording'}
        <span class="text-[11px] text-muted-foreground">录制权限由本机录制器控制</span>
      {:else if stage === 'review'}
        <Button variant="ghost" onclick={() => (stage = 'source')}><ArrowLeft class="h-3.5 w-3.5" />返回</Button>
        <Button onclick={generateDraft} disabled={!generatedDraft}><Sparkles class="h-3.5 w-3.5" />生成技能草稿</Button>
      {:else if stage === 'result'}
        <Button variant="ghost" onclick={() => (stage = 'review')}><ArrowLeft class="h-3.5 w-3.5" />返回复核</Button>
        <Button onclick={() => void createSkill()} disabled={busy || creating || created}>
          <CheckCircle2 class="h-3.5 w-3.5" />
          {created ? '已写入' : creating ? '写入中…' : '创建技能'}
        </Button>
      {/if}
    </div>
  </div>
</Dialog>
