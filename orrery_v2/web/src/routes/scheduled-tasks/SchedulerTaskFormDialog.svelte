<script lang="ts">
  import { CalendarClock } from 'lucide-svelte'
  import {
    buildToolArguments,
    formFromToolArguments,
    formFromToolSchema,
    setRawToolArguments,
    setToolFieldValue,
    type ToolCallFormState,
    type ToolSchemaField
  } from '#lib/components/resources/toolCallForm'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import {
    type ScheduledTaskCronPreset,
    type ScheduledTaskDiscussionMode,
    type ScheduledTaskForm,
    type ScheduledTaskScheduleMode,
    type ScheduledTaskSelectOption,
    type ScheduledTaskTargetMode
  } from './scheduledTaskForm'

  interface Props {
    open: boolean
    editing: boolean
    form: ScheduledTaskForm
    formErrors: string[]
    discussionOptions: ScheduledTaskSelectOption[]
    agentOptions: ScheduledTaskSelectOption[]
    memberOptions: ScheduledTaskSelectOption[]
    toolOptions: ScheduledTaskSelectOption[]
    saving?: boolean
    onClose: () => void
    onSubmit: () => void
  }

  let {
    open,
    editing,
    form = $bindable(),
    formErrors,
    discussionOptions,
    agentOptions,
    memberOptions,
    toolOptions,
    saving = false,
    onClose,
    onSubmit
  }: Props = $props()

  const placeholderOption = (label: string) => [{ label, value: '' }]
  const scheduleTimeModes = ['daily', 'weekly', 'monthly']
  let toolForm = $state<ToolCallFormState>(formFromToolSchema(undefined))
  let currentToolId = $state('')
  let toolArgumentsError = $state('')
  const weekdayOptions: ScheduledTaskSelectOption[] = [
    { label: '周日', value: '0' },
    { label: '周一', value: '1' },
    { label: '周二', value: '2' },
    { label: '周三', value: '3' },
    { label: '周四', value: '4' },
    { label: '周五', value: '5' },
    { label: '周六', value: '6' }
  ]
  const experienceSourceScopeOptions: ScheduledTaskSelectOption[] = [
    { label: '任务（Mission）', value: 'mission' },
    { label: '讨论组', value: 'discussion' },
    { label: '项目', value: 'project' },
    { label: '部门', value: 'department' },
    { label: '组织', value: 'organization' }
  ]
  const worldSourceScopeOptions = experienceSourceScopeOptions.filter((option) =>
    ['project', 'department', 'organization'].includes(option.value)
  )
  const experienceTargetScopeOptions: ScheduledTaskSelectOption[] = [
    { label: '项目', value: 'project' },
    { label: '部门', value: 'department' },
    { label: '组织', value: 'organization' }
  ]
  const lifecycleScopeOptions: ScheduledTaskSelectOption[] = [
    { label: 'Agent', value: 'agent' },
    { label: '讨论组', value: 'discussion' },
    ...experienceSourceScopeOptions.filter((option) => option.value !== 'discussion')
  ]

  const monthDayOptions: ScheduledTaskSelectOption[] = Array.from({ length: 31 }, (_, index) => {
    const day = String(index + 1)
    return { label: `${day} 日`, value: day }
  })

  function updateScheduleMode(value: string) {
    form.scheduleMode = value as ScheduledTaskScheduleMode
  }

  function updateCronPreset(value: string) {
    form.cronPreset = value as ScheduledTaskCronPreset
  }

  function updateTargetMode(value: string) {
    form.targetMode = value as ScheduledTaskTargetMode
    toolArgumentsError = ''
    if (form.targetMode === 'memory_experience_distillation') {
      if (!experienceSourceScopeOptions.some((option) => option.value === form.sourceScopeKind)) form.sourceScopeKind = 'mission'
      if (!experienceTargetScopeOptions.some((option) => option.value === form.targetScopeKind)) form.targetScopeKind = 'project'
    } else if (form.targetMode === 'memory_world_consolidation') {
      if (!worldSourceScopeOptions.some((option) => option.value === form.sourceScopeKind)) form.sourceScopeKind = 'project'
      form.targetScopeKind = 'organization'
    } else if (form.targetMode === 'memory_lifecycle_sweep') {
      if (!lifecycleScopeOptions.some((option) => option.value === form.targetScopeKind)) form.targetScopeKind = 'agent'
    }
  }

  function updateDiscussionMode(value: string) {
    form.discussionMode = value as ScheduledTaskDiscussionMode
    if (form.discussionMode === 'existing') {
      form.initialMemberIds = []
      form.allowMemberAdditions = true
    } else {
      form.conversationId = ''
    }
  }

  function toggleInitialMember(memberId: string, checked: boolean) {
    if (checked) {
      form.initialMemberIds = Array.from(new Set([...form.initialMemberIds, memberId]))
      return
    }
    form.initialMemberIds = form.initialMemberIds.filter((id) => id !== memberId)
  }

  $effect(() => {
    if (form.targetMode !== 'tool_call') return
    if (currentToolId === form.toolId) return
    currentToolId = form.toolId
    const selected = selectedToolOption()
    toolForm = formFromToolArguments(selected?.inputSchema, form.toolArguments)
    toolArgumentsError = ''
  })

  function selectedToolOption() {
    return toolOptions.find((option) => option.value === form.toolId)
  }

  function updateTool(value: string) {
    form.toolId = value
    form.toolArguments = {}
    currentToolId = ''
    toolArgumentsError = ''
  }

  function setToolField(name: string, value: string) {
    toolForm = setToolFieldValue(toolForm, name, value)
    syncToolArguments(false)
  }

  function setRawToolArgumentText(value: string) {
    toolForm = setRawToolArguments(toolForm, value)
    syncToolArguments(false)
  }

  function syncToolArguments(reportErrors: boolean) {
    try {
      form.toolArguments = buildToolArguments(toolForm)
      toolArgumentsError = ''
      return true
    } catch (error) {
      if (reportErrors) {
        toolArgumentsError = error instanceof Error ? error.message : String(error)
      }
      return false
    }
  }

  function submitForm() {
    if (form.targetMode === 'tool_call' && !syncToolArguments(true)) return
    onSubmit()
  }

  function fieldClass() {
    return 'space-y-1.5'
  }
</script>

<Dialog
  {open}
  title={editing ? '编辑自动化任务' : '新建自动化任务'}
  size="wide"
  {onClose}
>
  <form class="space-y-5" onsubmit={(event) => { event.preventDefault(); submitForm() }}>
    {#if formErrors.length > 0}
      <div class="rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        {#each formErrors as error}
          <div>{error}</div>
        {/each}
      </div>
    {/if}

    <div class="grid gap-4 lg:grid-cols-2">
      <label class={fieldClass()}>
        <span class="text-xs font-medium text-muted-foreground">标题</span>
        <Input bind:value={form.title} />
      </label>
      <label class={fieldClass()}>
        <span class="text-xs font-medium text-muted-foreground">描述</span>
        <Input bind:value={form.description} />
      </label>
    </div>

    <section class="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)]">
      <label class={fieldClass()}>
        <span class="text-xs font-medium text-muted-foreground">执行方式</span>
        <Select
          value={form.scheduleMode}
          options={[
            { label: '一次性执行', value: 'once' },
            { label: '周期性执行', value: 'cron' }
          ]}
          onchange={(event) => updateScheduleMode(event.currentTarget.value)}
        />
      </label>

      {#if form.scheduleMode === 'once'}
        <label class={fieldClass()}>
          <span class="text-xs font-medium text-muted-foreground">执行时间</span>
          <Input type="datetime-local" bind:value={form.runAtLocal} />
        </label>
      {:else}
        <div class="grid gap-4 md:grid-cols-3">
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">周期</span>
            <Select
              value={form.cronPreset}
              options={[
                { label: '每小时', value: 'hourly' },
                { label: '每天', value: 'daily' },
                { label: '每周', value: 'weekly' },
                { label: '每月', value: 'monthly' },
                { label: '自定义 Cron', value: 'custom' }
              ]}
              onchange={(event) => updateCronPreset(event.currentTarget.value)}
            />
          </label>
          {#if scheduleTimeModes.includes(form.cronPreset)}
            <label class={fieldClass()}>
              <span class="text-xs font-medium text-muted-foreground">执行时间</span>
              <Input type="time" bind:value={form.cronTime} />
            </label>
          {/if}
          {#if form.cronPreset === 'weekly'}
            <label class={fieldClass()}>
              <span class="text-xs font-medium text-muted-foreground">星期</span>
              <Select
                value={form.cronWeekday}
                options={weekdayOptions}
                onchange={(event) => (form.cronWeekday = event.currentTarget.value)}
              />
            </label>
          {/if}
          {#if form.cronPreset === 'monthly'}
            <label class={fieldClass()}>
              <span class="text-xs font-medium text-muted-foreground">日期</span>
              <Select
                value={form.cronMonthDay}
                options={monthDayOptions}
                onchange={(event) => (form.cronMonthDay = event.currentTarget.value)}
              />
            </label>
          {/if}
          {#if form.cronPreset === 'custom'}
            <label class={fieldClass()}>
              <span class="text-xs font-medium text-muted-foreground">Cron (0 9 * * 1)</span>
              <Input bind:value={form.customCron} />
            </label>
          {/if}
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">时区</span>
            <Input bind:value={form.timezone} />
          </label>
        </div>
      {/if}
    </section>

    <section class="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)]">
      <label class={fieldClass()}>
        <span class="text-xs font-medium text-muted-foreground">目标类型</span>
        <Select
          value={form.targetMode}
          options={[
            { label: '聊天工作流', value: 'discussion_workflow' },
            { label: '指定 Agent 任务', value: 'agent_task' },
            { label: '调用工具 / MCP', value: 'tool_call' },
            { label: '经验记忆提炼', value: 'memory_experience_distillation' },
            { label: '世界记忆整合', value: 'memory_world_consolidation' },
            { label: '记忆生命周期清理', value: 'memory_lifecycle_sweep' },
            ...(form.targetMode === 'todo_automation_scan'
              ? [{ label: 'Todo 自动检查', value: 'todo_automation_scan' }]
              : [])
          ]}
          onchange={(event) => updateTargetMode(event.currentTarget.value)}
        />
      </label>

      {#if form.targetMode === 'discussion_workflow'}
        <div class="grid gap-4 md:grid-cols-2">
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">讨论组方式</span>
            <Select
              value={form.discussionMode}
              options={[
                { label: '选择已有讨论组', value: 'existing' },
                { label: '新建讨论组', value: 'new' }
              ]}
              onchange={(event) => updateDiscussionMode(event.currentTarget.value)}
            />
          </label>
          {#if form.discussionMode === 'existing'}
            <label class={fieldClass()}>
              <span class="text-xs font-medium text-muted-foreground">讨论组</span>
              <Select
                value={form.conversationId}
                options={[...placeholderOption('选择讨论组'), ...discussionOptions]}
                onchange={(event) => (form.conversationId = event.currentTarget.value)}
              />
            </label>
          {:else}
            <label class="flex items-center gap-2 self-end rounded-md border border-border bg-card px-3 py-2 text-sm">
              <input type="checkbox" bind:checked={form.allowMemberAdditions} class="h-4 w-4 accent-brand" />
              允许自主加入成员
            </label>
          {/if}
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">主导 Agent</span>
            <Select
              value={form.leadAgentId}
              options={[...placeholderOption('选择主导 Agent'), ...memberOptions]}
              onchange={(event) => (form.leadAgentId = event.currentTarget.value)}
            />
          </label>
          {#if form.discussionMode === 'new'}
            <div class="space-y-2 md:col-span-2">
              <span class="text-xs font-medium text-muted-foreground">初始成员</span>
              <div class="grid max-h-40 gap-2 overflow-auto rounded-md border border-border bg-background p-3 md:grid-cols-2">
                {#each memberOptions as member}
                  <label class="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/60">
                    <input
                      type="checkbox"
                      checked={form.initialMemberIds.includes(member.value)}
                      class="h-4 w-4 shrink-0 accent-brand"
                      onchange={(event) => toggleInitialMember(member.value, event.currentTarget.checked)}
                    />
                    <span class="truncate">{member.label}</span>
                  </label>
                {/each}
              </div>
            </div>
          {/if}
          <label class="space-y-1.5 md:col-span-2">
            <span class="text-xs font-medium text-muted-foreground">消息</span>
            <textarea
              bind:value={form.message}
              rows="4"
              class="min-h-24 w-full rounded-md border border-input bg-input-background px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            ></textarea>
          </label>
        </div>
      {:else if form.targetMode === 'agent_task'}
        <div class="grid gap-4 md:grid-cols-2">
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">Agent</span>
            <Select
              value={form.agentId}
              options={[...placeholderOption('选择 Agent'), ...agentOptions]}
              onchange={(event) => (form.agentId = event.currentTarget.value)}
            />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">任务目标</span>
            <Input bind:value={form.taskGoal} />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">执行提示词</span>
            <Input bind:value={form.prompt} />
          </label>
        </div>
      {:else if form.targetMode === 'todo_automation_scan'}
        <div class="flex items-center text-sm font-medium text-foreground">待办任务自动检查</div>
      {:else if form.targetMode === 'memory_experience_distillation' || form.targetMode === 'memory_world_consolidation'}
        <div class="grid gap-4 md:grid-cols-2">
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">记忆主题</span>
            <Input bind:value={form.memoryTopic} />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">处理记录上限</span>
            <Input bind:value={form.memoryRecordLimit} />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">来源范围</span>
            <Select
              value={form.sourceScopeKind}
              options={form.targetMode === 'memory_world_consolidation' ? worldSourceScopeOptions : experienceSourceScopeOptions}
              onchange={(event) => (form.sourceScopeKind = event.currentTarget.value)}
            />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">来源范围 ID</span>
            <Input bind:value={form.sourceScopeId} />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">目标范围</span>
            <Select
              value={form.targetScopeKind}
              options={form.targetMode === 'memory_world_consolidation'
                ? [{ label: '组织', value: 'organization' }]
                : experienceTargetScopeOptions}
              onchange={(event) => (form.targetScopeKind = event.currentTarget.value)}
            />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">目标范围 ID</span>
            <Input bind:value={form.targetScopeId} />
          </label>
        </div>
      {:else if form.targetMode === 'memory_lifecycle_sweep'}
        <div class="grid gap-4 md:grid-cols-3">
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">清理范围</span>
            <Select
              value={form.targetScopeKind}
              options={lifecycleScopeOptions}
              onchange={(event) => (form.targetScopeKind = event.currentTarget.value)}
            />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">范围 ID</span>
            <Input bind:value={form.targetScopeId} />
          </label>
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">处理记录上限</span>
            <Input bind:value={form.memoryRecordLimit} />
          </label>
        </div>
      {:else}
        <div class="grid gap-4">
          <label class={fieldClass()}>
            <span class="text-xs font-medium text-muted-foreground">工具（含 MCP 工具）</span>
            <Select
              value={form.toolId}
              options={[...placeholderOption('选择工具'), ...toolOptions]}
              onchange={(event) => updateTool(event.currentTarget.value)}
            />
          </label>
          {#if form.toolId}
            <div class="grid gap-3 rounded-md border border-border bg-muted/30 p-3">
              {#if toolForm.usesRawArguments}
                <label class="grid gap-1.5">
                  <span class="text-xs font-medium text-muted-foreground">参数 JSON</span>
                  <textarea
                    aria-label="参数 JSON"
                    value={toolForm.rawArguments}
                    oninput={(event) => setRawToolArgumentText(event.currentTarget.value)}
                    class="min-h-32 rounded-md border border-border bg-input-background px-3 py-2 font-mono text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  ></textarea>
                </label>
              {:else}
                {#each toolForm.fields as field (field.name)}
                  {@render toolField(field)}
                {/each}
              {/if}
              {#if toolArgumentsError}
                <div class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {toolArgumentsError}
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {/if}
    </section>

    <section class="grid gap-4 md:grid-cols-3">
      <label class={fieldClass()}>
        <span class="text-xs font-medium text-muted-foreground">并发策略</span>
        <Select
          value={form.concurrency}
          options={[
            { label: '各轮独立运行（默认）', value: 'allow_parallel' },
            { label: '上一轮未结束时跳过新轮次', value: 'skip_if_running' }
          ]}
          onchange={(event) => (form.concurrency = event.currentTarget.value as never)}
        />
      </label>
      <label class="flex items-center gap-2 self-end rounded-md border border-border bg-card px-3 py-2 text-sm">
        <input type="checkbox" bind:checked={form.enabled} class="h-4 w-4 accent-brand" />
        启用
      </label>
    </section>

    <div class="flex justify-end gap-2">
      <Button variant="outline" onclick={onClose}>取消</Button>
      <Button type="submit" disabled={saving}>
        <CalendarClock class="h-4 w-4" />
        {editing ? '保存' : '创建自动化任务'}
      </Button>
    </div>
  </form>
</Dialog>

{#snippet toolField(field: ToolSchemaField)}
  <label class="grid gap-1.5">
    <span class="text-xs font-medium text-muted-foreground">
      {field.label}{field.required ? ' *' : ''}
    </span>
    {#if field.enumValues.length > 0}
      <Select
        value={toolForm.values[field.name] ?? ''}
        options={[
          ...(field.required ? [] : [{ label: '', value: '' }]),
          ...field.enumValues.map((option) => ({ label: option.label, value: option.value }))
        ]}
        onchange={(event) => setToolField(field.name, event.currentTarget.value)}
      />
    {:else if field.type === 'boolean'}
      <Select
        value={toolForm.values[field.name] ?? ''}
        options={[
          ...(field.required ? [] : [{ label: '', value: '' }]),
          { label: 'true', value: 'true' },
          { label: 'false', value: 'false' }
        ]}
        onchange={(event) => setToolField(field.name, event.currentTarget.value)}
      />
    {:else if field.type === 'array' || field.type === 'object'}
      <textarea
        aria-label={field.label}
        value={toolForm.values[field.name] ?? ''}
        oninput={(event) => setToolField(field.name, event.currentTarget.value)}
        class="min-h-24 rounded-md border border-border bg-input-background px-3 py-2 font-mono text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      ></textarea>
    {:else}
      <Input
        ariaLabel={field.label}
        type={field.type === 'number' || field.type === 'integer' ? 'number' : 'text'}
        value={toolForm.values[field.name] ?? ''}
        oninput={(event) => setToolField(field.name, event.currentTarget.value)}
      />
    {/if}
  </label>
{/snippet}
