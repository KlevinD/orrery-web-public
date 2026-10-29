<script lang="ts">
  import { onMount } from 'svelte'
  import { ArrowLeft, BookOpen, FileText, GitBranch, History, Link2, Network, PanelLeft, PanelRight, RefreshCw, Save, Search, Send, Upload, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import KnowledgeDocumentTree from './KnowledgeDocumentTree.svelte'
  import KnowledgeAccessPanel from './KnowledgeAccessPanel.svelte'
  import { websiteKnowledgeAudience } from '#lib/knowledge/accessPresentation'
  import KnowledgeMarkdownEditor from './KnowledgeMarkdownEditor.svelte'
  import ObsidianForceGraph, { type ObsidianGraphLink, type ObsidianGraphNode } from '#lib/components/graph/ObsidianForceGraph.svelte'
  import {
    getKnowledgeBase,
    getKnowledgeDocumentOntology,
    getKnowledgeDocumentSource,
    listKnowledgeDocumentRelations,
    listKnowledgeDocumentRevisions,
    listKnowledgeDocuments,
    publishKnowledgeDocumentRevision,
    syncKnowledgeDocuments,
    updateKnowledgeDocumentSource,
    uploadKnowledgeDocument
  } from '#lib/queries/knowledge'
  import {
    buildKnowledgeDocumentTree,
    formatKnowledgeDocumentSize,
    isSupportedKnowledgeFile,
    knowledgeDocumentTypeLabel,
    knowledgeDocumentSearchText,
    knowledgeSourceContentKind,
    knowledgeSourceTypeLabel,
    KNOWLEDGE_UPLOAD_ACCEPT
  } from '#lib/knowledge/documentPresentation'
  import type {
    KnowledgeBaseDTO,
    KnowledgeDocumentDTO,
    KnowledgeDocumentRelationDTO,
    KnowledgeOntologyProjectionDTO,
    KnowledgeRevisionDTO,
    KnowledgeSemanticSourceDTO
  } from '#lib/types/api/knowledge'

  interface Props { knowledgeBaseId: string }
  let { knowledgeBaseId }: Props = $props()

  type MobilePane = 'documents' | 'editor' | 'inspector'
  type InspectorTab = 'ontology' | 'revisions' | 'index'

  let base = $state<KnowledgeBaseDTO>()
  let documents = $state<KnowledgeDocumentDTO[]>([])
  let selectedDocumentId = $state('')
  let source = $state<KnowledgeSemanticSourceDTO>()
  let content = $state('')
  let revisions = $state<KnowledgeRevisionDTO[]>([])
  let relations = $state<KnowledgeDocumentRelationDTO[]>([])
  let projection = $state<KnowledgeOntologyProjectionDTO>()
  let loading = $state(true)
  let documentLoading = $state(false)
  let saving = $state(false)
  let publishing = $state(false)
  let uploading = $state(false)
	let refreshingCatalog = $state(false)
  let error = $state('')
  let notice = $state('')
  let documentQuery = $state('')
  let mobilePane = $state<MobilePane>('editor')
  let inspectorTab = $state<InspectorTab>('ontology')

  const selectedDocument = $derived(documents.find((item) => item.id === selectedDocumentId))
  const websiteKnowledge = $derived(base ? websiteKnowledgeAudience(base) !== undefined : false)
  const visibleDocuments = $derived(documents.filter((document) => {
    const query = documentQuery.trim().toLocaleLowerCase()
    if (!query) return true
		return knowledgeDocumentSearchText(document, documentStatusLabel(document.status)).includes(query)
  }))
  const visibleDocumentTree = $derived(buildKnowledgeDocumentTree(visibleDocuments))
  const selectedDocumentType = $derived(knowledgeDocumentTypeLabel(selectedDocument))
  const selectedSourceKind = $derived(knowledgeSourceContentKind(source))
  const selectedSourceType = $derived(knowledgeSourceTypeLabel(source))
  const dirty = $derived(source !== undefined && content !== source.content)
  const graphNodes = $derived(buildGraphNodes(projection))
  const graphLinks = $derived(buildGraphLinks(projection))

  onMount(() => { void loadWorkspace() })

  async function loadWorkspace() {
    loading = true
    error = ''
    try {
      const [nextBase, page] = await Promise.all([getKnowledgeBase(knowledgeBaseId), listKnowledgeDocuments(knowledgeBaseId)])
      base = nextBase
      documents = page.items ?? []
      if (documents.length > 0) await selectDocument(documents[0].id)
    } catch (err) {
      error = errorMessage(err)
    } finally {
      loading = false
    }
  }

  async function selectDocument(documentId: string) {
    if (dirty && !confirm('当前修改尚未保存，确认切换文档？')) return
    selectedDocumentId = documentId
    mobilePane = 'editor'
    documentLoading = true
    relations = []
    error = ''
    notice = ''
    try {
      const [nextSource, revisionPage] = await Promise.all([
        getKnowledgeDocumentSource(knowledgeBaseId, documentId),
        listKnowledgeDocumentRevisions(knowledgeBaseId, documentId)
      ])
      source = nextSource
      content = nextSource.content
      revisions = [...(revisionPage.items ?? [])].sort((a, b) => b.revision - a.revision)
      try {
        projection = await getKnowledgeDocumentOntology(knowledgeBaseId, documentId)
      } catch {
        projection = undefined
      }
      try {
        relations = (await listKnowledgeDocumentRelations(knowledgeBaseId, documentId)).items ?? []
      } catch {
        relations = []
      }
    } catch (err) {
      source = undefined
      content = ''
      revisions = []
      projection = undefined
      relations = []
      error = errorMessage(err)
    } finally {
      documentLoading = false
    }
  }

  async function saveDocument() {
    if (!source || !selectedDocumentId || !dirty) return
    saving = true
    error = ''
    notice = ''
    try {
      const revision = await updateKnowledgeDocumentSource(knowledgeBaseId, selectedDocumentId, source.revision, content)
      source = await getKnowledgeDocumentSource(knowledgeBaseId, selectedDocumentId, revision.revision)
      content = source.content
      revisions = (await listKnowledgeDocumentRevisions(knowledgeBaseId, selectedDocumentId)).items ?? []
      notice = `已保存为修订 ${revision.revision}`
    } catch (err) {
      error = isConflict(err) ? '文档已被其他人更新，请重新加载后再合并修改。' : errorMessage(err)
    } finally {
      saving = false
    }
  }

  async function selectDocumentRevision(revision: KnowledgeRevisionDTO) {
    if (!selectedDocumentId || (dirty && !confirm('当前修改尚未保存，确认查看历史修订？'))) return
    documentLoading = true
    error = ''
    try {
      source = await getKnowledgeDocumentSource(knowledgeBaseId, selectedDocumentId, revision.revision)
      content = source.content
      try {
        projection = await getKnowledgeDocumentOntology(knowledgeBaseId, selectedDocumentId, revision.revision)
      } catch {
        projection = undefined
      }
      try {
        relations = (await listKnowledgeDocumentRelations(knowledgeBaseId, selectedDocumentId)).items ?? []
      } catch {
        relations = []
      }
      mobilePane = 'editor'
    } catch (err) {
      error = errorMessage(err)
    } finally {
      documentLoading = false
    }
  }

  async function publishRevision() {
    if (!source || !selectedDocumentId || dirty) return
    publishing = true
    error = ''
    try {
      await publishKnowledgeDocumentRevision(knowledgeBaseId, selectedDocumentId, source.revision)
      revisions = (await listKnowledgeDocumentRevisions(knowledgeBaseId, selectedDocumentId)).items ?? []
      notice = `修订 ${source.revision} 已发布，索引完成后智能体将使用新内容。`
    } catch (err) {
      error = errorMessage(err)
    } finally {
      publishing = false
    }
  }

  async function uploadFile(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    if (!file) return
    if (websiteKnowledge && !/\.(md|markdown)$/i.test(file.name)) {
      error = '网站资料请上传 UTF-8 编码的 Markdown 文档，编辑后发布即可供咨询智能体使用。'
      input.value = ''
      return
    }
    if (!isSupportedKnowledgeFile(file)) {
      error = '暂不支持此文件格式。请选择 Markdown、TXT、HTML、PDF 或 DOCX 文档。'
      input.value = ''
      return
    }
    uploading = true
    error = ''
    try {
      const uploaded = await uploadKnowledgeDocument(knowledgeBaseId, file)
      documents = [...documents, uploaded]
      input.value = ''
      await selectDocument(uploaded.id)
    } catch (err) {
      error = errorMessage(err)
    } finally {
      uploading = false
    }
  }

	async function refreshCatalog() {
		if (refreshingCatalog) return
		refreshingCatalog = true
		error = ''
		notice = ''
		try {
			const result = await syncKnowledgeDocuments(knowledgeBaseId)
			const page = await listKnowledgeDocuments(knowledgeBaseId)
			documents = page.items ?? []
			if (selectedDocumentId && !documents.some((document) => document.id === selectedDocumentId)) {
				selectedDocumentId = ''
				source = undefined
				content = ''
				revisions = []
				relations = []
				projection = undefined
			}
			notice = `目录已刷新：新增 ${result.created}，更新 ${result.updated}，未变化 ${result.unchanged}，移除 ${result.removed}，拒绝 ${result.rejected}`
		} catch (err) {
			error = errorMessage(err)
		} finally {
			refreshingCatalog = false
		}
	}

  function buildGraphNodes(value?: KnowledgeOntologyProjectionDTO): ObsidianGraphNode[] {
    const nodes = new Map<string, ObsidianGraphNode>()
    for (const assertion of value?.assertions ?? []) {
      const fromId = assertion.from_object_id || `${assertion.from.kind}:${assertion.from.id}`
      const toId = assertion.to_object_id || `${assertion.to.kind}:${assertion.to.id}`
      nodes.set(fromId, { id: fromId, label: assertion.from.id, group: assertion.from.kind, muted: assertion.status !== 'confirmed' })
      nodes.set(toId, { id: toId, label: assertion.to.id, group: assertion.to.kind, muted: assertion.status !== 'confirmed' })
    }
    return [...nodes.values()]
  }

  function buildGraphLinks(value?: KnowledgeOntologyProjectionDTO): ObsidianGraphLink[] {
    return (value?.assertions ?? []).map((assertion, index) => ({
      id: `${assertion.link_type}:${index}`,
      source: assertion.from_object_id || `${assertion.from.kind}:${assertion.from.id}`,
      target: assertion.to_object_id || `${assertion.to.kind}:${assertion.to.id}`,
      label: assertion.link_type,
      color: assertion.status === 'confirmed' ? '#2f8f68' : '#9aa3af'
    }))
  }

  function documentStatusLabel(value?: string) {
    return ({
      uploaded: '等待处理',
      parsing: '正在解析',
      chunking: '正在分段',
      embedding: '正在建立索引',
      indexed: '索引就绪',
      active: '可用',
      failed: '处理失败',
      deleting: '正在删除'
    } as Record<string, string>)[value ?? ''] ?? '状态未知'
  }

  function publicationStatusLabel(value?: string) {
    return ({
      draft: '草稿',
      published: '已发布',
      publishing: '发布中',
      failed: '发布失败',
      superseded: '已被替代'
    } as Record<string, string>)[value ?? ''] ?? '未发布'
  }

  function assertionStatusLabel(value?: string) {
    return ({ confirmed: '已确认', proposed: '待确认', rejected: '已拒绝' } as Record<string, string>)[value ?? ''] ?? '待确认'
  }

  function relationLabel(value?: string) {
    return ({
      related_to: '关联',
      belongs_to: '归属',
      depends_on: '依赖',
      derived_from: '来源于',
      supports: '支持',
      mentions: '提及'
    } as Record<string, string>)[value ?? ''] ?? value?.replaceAll('_', ' ') ?? '关联'
  }

  function isConflict(err: unknown) { return typeof err === 'object' && err !== null && 'status' in err && err.status === 409 }
  function errorMessage(err: unknown) {
    const message = err instanceof Error ? err.message : String(err || '')
    if (message.includes('knowledge dependency unavailable')) return '知识服务当前不可用，请检查知识服务配置。'
    if (message.includes('unauthorized')) return '登录状态已失效，请重新登录。'
    return message || '操作失败'
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-background text-foreground">
  <header class="flex min-h-16 flex-wrap items-center gap-2 border-b border-border bg-background px-3 py-2.5 sm:gap-3 sm:px-5">
    <Button href="/resources/knowledge" variant="ghost" size="icon" ariaLabel="返回知识库"><ArrowLeft class="h-4 w-4" /></Button>
    <div class="hidden h-9 w-9 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground sm:grid">
      <BookOpen class="h-[18px] w-[18px]" />
    </div>
    <div class="min-w-0 flex-1">
      <div class="truncate text-[15px] font-semibold">{base?.name ?? '知识工作台'}</div>
      <div class="mt-0.5 truncate text-xs text-muted-foreground">{selectedDocument?.filename ?? '选择一份文档开始编辑'}</div>
    </div>
    {#if source}<span class="rounded-md bg-muted px-2 py-1 text-[11px] text-muted-foreground">修订 {source.revision}{dirty ? ' · 未保存' : ''}</span>{/if}
    <Button variant="outline" size="sm" ariaLabel={saving ? '正在保存' : '保存'} disabled={!dirty || saving} onclick={saveDocument}><Save class="h-4 w-4" /><span class="hidden sm:inline">{saving ? '保存中' : '保存'}</span></Button>
    <Button size="sm" ariaLabel={publishing ? '正在发布' : '发布'} disabled={!source || dirty || publishing} onclick={publishRevision}><Send class="h-4 w-4" /><span class="hidden sm:inline">{publishing ? '发布中' : '发布'}</span></Button>
  </header>

  {#if base}<KnowledgeAccessPanel {base} />{/if}

  <nav class="grid grid-cols-3 border-b border-border xl:hidden" aria-label="工作台面板">
    <button class={mobilePane === 'documents' ? 'selection-wash active' : ''} onclick={() => mobilePane = 'documents'}><PanelLeft class="h-4 w-4" />文档</button>
    <button class={mobilePane === 'editor' ? 'selection-wash active' : ''} onclick={() => mobilePane = 'editor'}><BookOpen class="h-4 w-4" />正文</button>
    <button class={mobilePane === 'inspector' ? 'selection-wash active' : ''} onclick={() => mobilePane = 'inspector'}><PanelRight class="h-4 w-4" />本体</button>
  </nav>

  {#if error}<div class="border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive">{error}</div>{/if}
  {#if notice}<div class="border-b border-emerald-200 bg-emerald-50 px-4 py-2 text-sm text-emerald-800">{notice}</div>{/if}

  <div class="grid min-h-0 flex-1 xl:grid-cols-[15rem_minmax(0,1fr)_18.75rem]">
    <aside
      class:hidden={mobilePane !== 'documents'}
      class="workspace-pane flex flex-col border-r border-border bg-background xl:flex"
      data-knowledge-document-pane
    >
      <div class="flex h-11 items-center justify-between border-b border-border px-3">
        <span class="text-[13px] font-medium text-muted-foreground">文档列表 <span class="ml-1 text-[11px] font-normal">{documents.length}</span></span>
				<div class="flex items-center gap-1">
					<button class="grid h-8 w-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50" type="button" title="刷新目录" aria-label="刷新目录" disabled={refreshingCatalog} onclick={refreshCatalog}>
						<RefreshCw class="h-4 w-4 {refreshingCatalog ? 'animate-spin' : ''}" />
					</button>
					<label class="grid h-8 w-8 cursor-pointer place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground" title="上传文档">
						{#if uploading}<RefreshCw class="h-4 w-4 animate-spin" />{:else}<Upload class="h-4 w-4" />{/if}
						<input class="sr-only" type="file" accept={websiteKnowledge ? '.md,.markdown' : KNOWLEDGE_UPLOAD_ACCEPT} onchange={uploadFile} disabled={uploading} />
					</label>
				</div>
      </div>
      <div class="border-b border-border p-2">
        <div class="flex h-8 items-center gap-2 rounded-md border border-border bg-background px-2 focus-within:border-brand/40 focus-within:ring-1 focus-within:ring-brand/15">
          <Search class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <input class="min-w-0 flex-1 bg-transparent text-xs text-foreground outline-none" type="search" aria-label="搜索知识库文档" bind:value={documentQuery} />
          {#if documentQuery}
            <button class="grid h-6 w-6 shrink-0 place-items-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground" type="button" title="清除搜索" aria-label="清除文档搜索" onclick={() => documentQuery = ''}><X class="h-3.5 w-3.5" /></button>
          {/if}
        </div>
      </div>
      <div class="knowledge-document-scrollbar min-h-0 flex-1 overflow-y-scroll p-2" data-knowledge-document-list-scroller>
        {#if loading}<p class="p-3 text-sm text-muted-foreground">正在加载...</p>
        {:else if documents.length === 0}<p class="p-3 text-sm text-muted-foreground">知识库中还没有文档。</p>
        {:else if visibleDocuments.length === 0}<p class="p-3 text-sm text-muted-foreground">没有匹配的文档。</p>
        {:else}
          <KnowledgeDocumentTree
            tree={visibleDocumentTree}
            {selectedDocumentId}
            searching={documentQuery.trim() !== ''}
            statusLabel={documentStatusLabel}
            onSelect={selectDocument}
          />
        {/if}
      </div>
    </aside>

    <main class:hidden={mobilePane !== 'editor'} class="workspace-pane min-w-0 bg-[#f7f7f8] xl:block">
      {#if documentLoading}<div class="grid h-full place-items-center text-sm text-muted-foreground"><RefreshCw class="mb-2 h-5 w-5 animate-spin" />正在读取修订...</div>
      {:else if source}
        <div class="h-full overflow-y-auto p-3 md:p-5">
          {#key selectedDocumentId}
            <KnowledgeMarkdownEditor
              bind:value={content}
              ariaLabel="文档正文"
              minHeight="min-h-[calc(100vh-13rem)]"
              disabled={!source.editable || saving}
              readOnly={!source.editable}
              contentKind={selectedSourceKind}
              formatLabel={selectedSourceType}
            />
          {/key}
        </div>
      {:else}<div class="grid h-full place-items-center p-8 text-center text-sm text-muted-foreground"><div><FileText class="mx-auto mb-3 h-8 w-8" /><p>从左侧选择一份文档开始编辑。</p></div></div>{/if}
    </main>

    <aside class:hidden={mobilePane !== 'inspector'} class="right-sidebar-shell workspace-pane xl:flex">
      <div class="right-sidebar-header grid grid-cols-3 p-0">
        <button class={inspectorTab === 'ontology' ? 'selection-wash active' : ''} onclick={() => inspectorTab = 'ontology'}><Network class="h-4 w-4" />本体</button>
        <button class={inspectorTab === 'revisions' ? 'selection-wash active' : ''} onclick={() => inspectorTab = 'revisions'}><History class="h-4 w-4" />修订</button>
        <button class={inspectorTab === 'index' ? 'selection-wash active' : ''} onclick={() => inspectorTab = 'index'}><GitBranch class="h-4 w-4" />索引</button>
      </div>
      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar">
        {#if inspectorTab === 'ontology'}
          {#if graphNodes.length > 0}
            <ObsidianForceGraph nodes={graphNodes} links={graphLinks} height={320} compact ariaLabel="文档本体关系图" />
            <div class="space-y-2 border-t border-border p-3">
              {#each projection?.assertions ?? [] as assertion}
                <div class="rounded-md border border-border p-2 text-xs"><strong>{assertion.from.id}</strong> <span class="text-muted-foreground">{relationLabel(assertion.link_type)}</span> <strong>{assertion.to.id}</strong><div class="mt-1 text-muted-foreground">{assertionStatusLabel(assertion.status)} · {assertion.evidence?.anchor || '文档元数据'}</div></div>
              {/each}
            </div>
          {:else}<div class="grid min-h-56 place-items-center p-6 text-center text-sm text-muted-foreground"><div><Network class="mx-auto mb-3 h-7 w-7" /><p>发布文档修订后，本体关系将在这里生成。</p></div></div>{/if}
          <div class="border-t border-border p-3">
            <div class="mb-2 flex items-center justify-between gap-2 text-xs font-medium text-muted-foreground"><span class="flex items-center gap-2"><Link2 class="h-3.5 w-3.5" />文档链接</span><span class="rounded bg-muted px-1.5 py-0.5 text-[10px]">{relations.length}</span></div>
            {#if relations.length > 0}
              <div class="space-y-1.5">
                {#each relations as relation}
                  <button type="button" class="flex w-full items-center gap-2 rounded-md border border-border p-2 text-left text-xs hover:bg-muted" title={relation.document.filename || relation.document.id} onclick={() => selectDocument(relation.document.id)}>
                    <span class="shrink-0 text-[11px] text-muted-foreground">{relation.direction === 'in' ? '反向引用' : '引用'}</span>
                    <span class="min-w-0 flex-1 truncate font-medium">{relation.document.filename || relation.document.id}</span>
                    <span class="shrink-0 text-[10px] text-muted-foreground">{relation.link_type}</span>
                  </button>
                {/each}
              </div>
            {:else}
              <p class="text-xs text-muted-foreground">当前文档暂无文档链接</p>
            {/if}
          </div>
        {:else if inspectorTab === 'revisions'}
          <div class="divide-y divide-border">
            {#each revisions as revision}
              <button class="flex w-full items-start justify-between gap-3 p-3 text-left hover:bg-muted {source?.revision === revision.revision ? 'selection-wash' : ''}" onclick={() => selectedDocumentId && selectDocumentRevision(revision)}>
                <span><strong class="text-sm">修订 {revision.revision}</strong><small class="mt-1 block text-muted-foreground">{revision.created_by || '未知用户'} · {revision.created_at ? new Date(revision.created_at).toLocaleString() : ''}</small></span>
                <span class="rounded bg-muted px-2 py-1 text-[11px]">{publicationStatusLabel(revision.publication_status)}</span>
              </button>
            {/each}
          </div>
        {:else}
          <dl class="grid grid-cols-[5rem_1fr] gap-x-3 gap-y-3 p-4 text-sm">
            <dt class="text-muted-foreground">文件格式</dt><dd>{selectedDocumentType}</dd>
            <dt class="text-muted-foreground">文件大小</dt><dd>{formatKnowledgeDocumentSize(selectedDocument?.size_bytes)}</dd>
            <dt class="text-muted-foreground">处理状态</dt><dd>{documentStatusLabel(selectedDocument?.status)}</dd>
            <dt class="text-muted-foreground">正文形态</dt><dd>{selectedSourceType}</dd>
            <dt class="text-muted-foreground">编辑能力</dt><dd>{source?.editable ? '可编辑并创建修订' : '只读提取内容'}</dd>
            <dt class="text-muted-foreground">内容哈希</dt><dd class="break-all font-mono text-xs">{source?.content_hash ?? '-'}</dd>
            <dt class="text-muted-foreground">发布投影</dt><dd>{projection ? `修订 ${projection.revision}` : '尚未生成'}</dd>
          </dl>
        {/if}
      </div>
    </aside>
  </div>
</div>

<style>
  .workspace-pane { min-height: 0; height: 100%; overflow: hidden; }
  nav button, aside > div:first-child button { display: inline-flex; min-height: 2.75rem; align-items: center; justify-content: center; gap: .4rem; font-size: .8125rem; color: var(--muted-foreground); }
  nav button.active, aside > div:first-child button.active { color: var(--foreground); font-weight: 500; }
  @media (max-width: 1279px) { .workspace-pane.hidden { display: none; } }
</style>
