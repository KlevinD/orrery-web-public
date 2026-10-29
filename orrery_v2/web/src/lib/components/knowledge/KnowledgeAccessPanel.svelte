<script lang="ts">
  import { onMount } from 'svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { apiFetch } from '#lib/api/client'
  import { bindKnowledgeBase, listKnowledgeBindings, unbindKnowledgeBase } from '#lib/queries/knowledge'
  import { knowledgeAccessLabel, websiteKnowledgeConsumers } from '#lib/knowledge/accessPresentation'
  import type { KnowledgeBaseDTO, KnowledgeBindingDTO } from '#lib/types/api/knowledge'
  import type { WorkforceMemberDTO, WorkforcePageDTO } from '#lib/types/api/workforce'

  let { base }: { base: KnowledgeBaseDTO } = $props()
  let bindings = $state<KnowledgeBindingDTO[]>([])
  let members = $state<WorkforceMemberDTO[]>([])
  let selectedAgent = $state('')
  let access = $state('read')
  let loading = $state(true)
  let busy = $state(false)
  let error = $state('')
  let notice = $state('')
  const consumers = $derived(websiteKnowledgeConsumers(base))
  const agents = $derived(members.filter(member => member.agent_kind === 'ai'
    && !member.agent_profile_id.startsWith('website-')))

  onMount(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  })

  async function load(signal?: AbortSignal) {
    loading = true
    error = ''
    const [bindingResult, memberResult] = await Promise.allSettled([
      listKnowledgeBindings(base.id, { signal }),
      apiFetch<WorkforcePageDTO>('/api/workforce/members?limit=200&include_details=false', { signal })
    ])
    if (signal?.aborted) return
    if (bindingResult.status === 'fulfilled') bindings = bindingResult.value.items ?? []
    else error = message(bindingResult.reason)
    if (memberResult.status === 'fulfilled') members = memberResult.value.members ?? []
    loading = false
  }

  function subjectName(binding: KnowledgeBindingDTO) {
    const member = members.find(item => item.agent_profile_id === binding.bound_to_id || item.agent_id === binding.bound_to_id)
    return member?.display_name || member?.real_name || binding.bound_to_id
  }

  function accessLabel(value: string) {
    return ({ read: '只读', write: '读写', manage: '管理' } as Record<string, string>)[value] ?? value
  }

  async function bind(event: SubmitEvent) {
    event.preventDefault()
    if (!selectedAgent || busy) return
    busy = true
    error = ''
    notice = ''
    try {
      if (bindings.some(item => item.bound_to_type === 'agent' && item.bound_to_id === selectedAgent && item.access === access)) {
        notice = '该智能体已具有此权限'
        return
      }
      await bindKnowledgeBase(base.id, { bound_to_type: 'agent', bound_to_id: selectedAgent, access })
      bindings = (await listKnowledgeBindings(base.id)).items ?? []
      notice = '已保存智能体绑定'
    } catch (err) { error = message(err) }
    finally { busy = false }
  }

  async function unbind(binding: KnowledgeBindingDTO) {
    if (busy) return
    busy = true
    error = ''
    notice = ''
    try {
      await unbindKnowledgeBase(base.id, binding.id)
      bindings = bindings.filter(item => item.id !== binding.id)
      notice = '已移除这条访问授权'
    } catch (err) { error = message(err) }
    finally { busy = false }
  }

  function message(err: unknown) { return err instanceof Error ? err.message : String(err) }
</script>

<details class="border-b border-border bg-card px-4 py-3" data-knowledge-access>
  <summary class="cursor-pointer text-sm font-medium">访问范围与智能体绑定 · {knowledgeAccessLabel(base)}</summary>
  <div class="mt-3 grid gap-4 lg:grid-cols-2">
    <div class="space-y-2 text-sm">
      {#if consumers.length}
        <p class="text-xs text-muted-foreground">网站咨询范围由服务器限定，咨询智能体只读取已发布并完成索引的资料。</p>
        <ul class="space-y-1">{#each consumers as consumer}<li>{consumer}</li>{/each}</ul>
      {:else}
        <p class="text-xs text-muted-foreground">通过所有者、组织和以下授权访问。将资料用于网站咨询时，请进入网站公开或客户知识库维护。</p>
      {/if}
      <p class="text-xs text-muted-foreground">保存文档修订后点击「发布」，再查看索引状态。</p>
    </div>
    <div class="space-y-3">
      {#if loading}<p class="text-xs text-muted-foreground">正在加载访问授权…</p>
      {:else}
        {#each bindings as binding (binding.id)}
          <div class="flex items-center gap-2 text-sm">
            <span class="min-w-0 flex-1 truncate">{subjectName(binding)}</span>
            <span class="text-xs text-muted-foreground">{accessLabel(binding.access)}</span>
            <Button variant="ghost" size="sm" disabled={busy} ariaLabel={`移除 ${subjectName(binding)} 的授权`} onclick={() => unbind(binding)}>移除</Button>
          </div>
        {/each}
        {#if !bindings.length}<p class="text-xs text-muted-foreground">暂无额外访问授权</p>{/if}
        {#if agents.length}
          <form class="flex flex-wrap gap-2" onsubmit={bind}>
            <select aria-label="选择智能体" bind:value={selectedAgent} class="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1.5 text-sm" disabled={busy}>
              <option value="">选择内部智能体</option>
              {#each agents as agent}<option value={agent.agent_profile_id}>{agent.display_name || agent.real_name}</option>{/each}
            </select>
            <select aria-label="访问权限" bind:value={access} class="rounded-md border border-border bg-background px-2 py-1.5 text-sm" disabled={busy}>
              <option value="read">只读</option><option value="write">读写</option><option value="manage">管理</option>
            </select>
            <Button type="submit" size="sm" disabled={busy || !selectedAgent}>添加绑定</Button>
          </form>
        {/if}
      {/if}
      {#if error}<p role="alert" class="text-xs text-destructive">{error}</p>{/if}
      {#if notice}<p role="status" class="text-xs text-emerald-700">{notice}</p>{/if}
    </div>
  </div>
</details>
