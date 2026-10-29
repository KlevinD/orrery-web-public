<script lang="ts">
  import { ChevronDown, ChevronRight, FileText, Folder, FolderOpen } from 'lucide-svelte'
  import {
    formatKnowledgeDocumentSize,
    knowledgeDocumentDisplayName,
    knowledgeDocumentKind,
    knowledgeDocumentTypeLabel,
    type KnowledgeDocumentFolderNode,
    type KnowledgeDocumentTree
  } from '#lib/knowledge/documentPresentation'
  import type { KnowledgeDocumentDTO } from '#lib/types/api/knowledge'

  interface Props {
    tree: KnowledgeDocumentTree
    selectedDocumentId?: string
    searching?: boolean
    statusLabel: (status?: string) => string
    onSelect: (documentId: string) => void
  }

  let { tree, selectedDocumentId = '', searching = false, statusLabel, onSelect }: Props = $props()
  let expandedFolders = $state<Record<string, boolean>>({})

  function isExpanded(folder: KnowledgeDocumentFolderNode) {
    return searching || expandedFolders[folder.path] === true
  }

  function toggleFolder(folder: KnowledgeDocumentFolderNode) {
    expandedFolders = { ...expandedFolders, [folder.path]: !isExpanded(folder) }
  }
</script>

{#snippet documentNode(document: KnowledgeDocumentDTO, depth: number)}
  {@const kind = knowledgeDocumentKind(document)}
  <li>
    <button
      class="document-row {selectedDocumentId === document.id ? 'selection-wash selected' : ''}"
      style={`padding-left: ${0.4 + depth * 0.75}rem`}
      type="button"
      onclick={() => onSelect(document.id)}
    >
      <span class="document-icon {kind}"><FileText class="h-4 w-4" /></span>
      <span class="min-w-0 flex-1">
        <strong>{knowledgeDocumentDisplayName(document)}</strong>
        <small class:failed={document.status === 'failed'}>{knowledgeDocumentTypeLabel(document)} · {formatKnowledgeDocumentSize(document.size_bytes)} · {statusLabel(document.status)}</small>
      </span>
    </button>
  </li>
{/snippet}

{#snippet folderNode(folder: KnowledgeDocumentFolderNode, depth: number)}
  {@const expanded = isExpanded(folder)}
  <li>
    <button
      class="folder-row"
      style={`padding-left: ${0.35 + depth * 0.75}rem`}
      type="button"
      aria-expanded={expanded}
      title={expanded ? `收起 ${folder.name}` : `展开 ${folder.name}`}
      onclick={() => toggleFolder(folder)}
    >
      {#if expanded}<ChevronDown class="h-3.5 w-3.5" />{:else}<ChevronRight class="h-3.5 w-3.5" />{/if}
      {#if expanded}<FolderOpen class="h-4 w-4 text-brand" />{:else}<Folder class="h-4 w-4 text-muted-foreground" />{/if}
      <span class="min-w-0 flex-1 truncate">{folder.name}</span>
      <span class="text-[11px] text-muted-foreground">{folder.documentCount}</span>
    </button>
    {#if expanded}
      <ul>
        {#each folder.folders as child (child.path)}
          {@render folderNode(child, depth + 1)}
        {/each}
        {#each folder.documents as document (document.id)}
          {@render documentNode(document, depth + 1)}
        {/each}
      </ul>
    {/if}
  </li>
{/snippet}

<nav aria-label="知识库文件夹索引">
  <ul>
    {#each tree.folders as folder (folder.path)}
      {@render folderNode(folder, 0)}
    {/each}
    {#each tree.documents as document (document.id)}
      {@render documentNode(document, 0)}
    {/each}
  </ul>
</nav>

<style>
  .folder-row { display: flex; width: 100%; min-height: 2rem; align-items: center; gap: .35rem; border-radius: .25rem; padding-right: .45rem; text-align: left; font-size: .8125rem; font-weight: 500; color: var(--foreground); }
  .folder-row:hover { background: var(--muted); }
  .document-row { display: flex; width: 100%; gap: .5rem; border: 1px solid transparent; border-radius: .375rem; padding-top: .5rem; padding-right: .4rem; padding-bottom: .5rem; text-align: left; color: var(--muted-foreground); }
  .document-row:not(.selected):hover { background: var(--muted); color: var(--foreground); }
  .document-row.selected { color: var(--foreground); }
  .document-row strong { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: .8125rem; font-weight: 500; }
  .document-row small { display: block; overflow: hidden; margin-top: .1rem; text-overflow: ellipsis; white-space: nowrap; font-size: .6875rem; }
  .document-row small.failed { color: var(--destructive); }
  .document-icon { display: grid; width: 1.6rem; height: 1.6rem; flex: 0 0 auto; place-items: center; border-radius: .375rem; background: var(--muted); color: var(--muted-foreground); }
  .document-icon.markdown { background: color-mix(in srgb, #6b62dc 11%, transparent); color: #5850c8; }
  .document-icon.pdf { background: color-mix(in srgb, #c44f5e 11%, transparent); color: #a83e4b; }
  .document-icon.docx { background: color-mix(in srgb, #2877b8 11%, transparent); color: #23669d; }
  .document-icon.html { background: color-mix(in srgb, #b47722 12%, transparent); color: #91601b; }
</style>
