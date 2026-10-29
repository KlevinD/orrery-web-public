<script lang="ts">
  import { Download, File as FileIcon, FileCode2, FileImage, FileText, Folder, FolderPlus, Save, Upload } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SkillFileDTO, SkillFileNodeDTO, SkillFileRootDTO } from '#lib/types/api'

  type FlatFileNode = SkillFileNodeDTO & { depth: number }

  interface Props {
    roots: SkillFileRootDTO[]
    selectedFile?: SkillFileDTO
    imagePreviewUrl?: string
    editable?: boolean
    busyPath?: string
    onpreview?: (path: string) => void | Promise<unknown>
    ondownload?: (path: string) => void
    onsave?: (path: string, content: string) => void
    onupload?: (root: string, file: File) => void
    oncreatefolder?: (root: string, name: string) => void
  }

  let {
    roots,
    selectedFile = undefined,
    imagePreviewUrl = undefined,
    editable = false,
    busyPath = '',
    onpreview,
    ondownload,
    onsave,
    onupload,
    oncreatefolder
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  let draft = $state('')
  let folderDrafts = $state<Record<string, string>>({})
  const selectedCanEdit = $derived(editable && selectedFile ? canEditFile(selectedFile.preview_kind) : false)

  $effect(() => {
    draft = selectedFile?.content ?? ''
  })

  function canPreview(fileNode: SkillFileNodeDTO) {
    return fileNode.preview_kind !== 'none'
  }

  function canEditFile(kind: string) {
    return kind === 'markdown' || kind === 'text' || kind === 'json'
  }

  function countFiles(nodes: SkillFileNodeDTO[]): number {
    return nodes.reduce((total, item) => total + (item.type === 'file' ? 1 : 0) + countFiles(item.children ?? []), 0)
  }

  function rootChildren(root: SkillFileRootDTO): SkillFileNodeDTO[] {
    return Array.isArray(root.children) ? root.children : []
  }

  function flattenNodes(nodes: SkillFileNodeDTO[], depth = 0): FlatFileNode[] {
    const flattened: FlatFileNode[] = []
    for (const item of nodes) {
      flattened.push({ ...item, depth })
      if (item.type === 'directory' && item.children?.length) {
        flattened.push(...flattenNodes(item.children, depth + 1))
      }
    }
    return flattened
  }

  function iconFor(fileNode: SkillFileNodeDTO) {
    if (fileNode.type === 'directory') return Folder
    if (fileNode.preview_kind === 'image') return FileImage
    if (fileNode.preview_kind === 'json' || fileNode.preview_kind === 'markdown') return FileCode2
    if (fileNode.preview_kind === 'text') return FileText
    return FileIcon
  }

  function openFile(fileNode: SkillFileNodeDTO) {
    if (fileNode.type !== 'file') return
    if (canPreview(fileNode)) {
      onpreview?.(fileNode.path)
      return
    }
    ondownload?.(fileNode.path)
  }

  function uploadFile(rootName: string, event: Event & { currentTarget: HTMLInputElement }) {
    const file = event.currentTarget.files?.[0]
    event.currentTarget.value = ''
    if (file) onupload?.(rootName, file)
  }

  function createFolder(rootName: string) {
    const name = (folderDrafts[rootName] ?? '').trim()
    if (!name) return
    oncreatefolder?.(rootName, name)
    folderDrafts = { ...folderDrafts, [rootName]: '' }
  }
</script>

<section class="rounded-xl border border-border bg-card">
  <div class="border-b border-border px-4 py-3">
    <h2 class="text-sm font-semibold text-foreground">{t('resources.skills.packageResources')}</h2>
    <p class="text-xs text-muted-foreground">{t('resources.skills.packageResourcesDescription')}</p>
  </div>

  <div class="divide-y divide-border">
    {#each roots as root}
      {@const children = rootChildren(root)}
      <section class="px-4 py-3" aria-label={`${root.label || root.name} root`}>
        <div class="flex items-center justify-between gap-2">
          <div class="flex min-w-0 items-center gap-2">
            <Folder class="h-4 w-4 shrink-0 text-brand" />
            <span class="truncate text-sm font-medium text-foreground">{root.label || root.name}</span>
          </div>
          <span class="text-xs tabular-nums text-muted-foreground">{countFiles(children)}</span>
        </div>

        {#if children.length > 0}
          <div class="mt-2 space-y-1">
            {#each flattenNodes(children) as item}
              {@const Icon = iconFor(item)}
              <div class="min-w-0" style={`padding-left: ${item.depth * 0.75}rem`}>
                {#if item.type === 'directory'}
                  <div class="flex min-w-0 items-center gap-2 py-1 text-xs text-muted-foreground">
                    <Icon class="h-3.5 w-3.5 shrink-0" />
                    <span class="truncate">{item.name}</span>
                  </div>
                {:else}
                  <div class="group flex min-w-0 items-center gap-1">
                    <button
                      type="button"
                      class="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                      aria-label={canPreview(item) ? `Preview ${item.path}` : `Open ${item.path}`}
                      onclick={() => openFile(item)}
                    >
                      <Icon class="h-3.5 w-3.5 shrink-0" />
                      <span class="truncate">{item.name}</span>
                    </button>
                    <Button
                      variant="ghost"
                      size="icon"
                      ariaLabel={`Download ${item.path}`}
                      onclick={() => ondownload?.(item.path)}
                    >
                      <Download class="h-3.5 w-3.5" />
                    </Button>
                  </div>
                {/if}
              </div>
            {/each}
          </div>
        {:else}
          <p class="mt-2 text-xs text-muted-foreground">{t('resources.skills.emptyRoot')}</p>
        {/if}

        {#if editable}
          <div class="mt-3 space-y-2">
            <div class="flex items-center gap-1.5">
              <Input
                value={folderDrafts[root.name] ?? ''}
                placeholder={t('resources.skills.folderName')}
                ariaLabel={`New folder in ${root.name}`}
                oninput={(event) => (folderDrafts = { ...folderDrafts, [root.name]: event.currentTarget.value })}
                class="h-8 text-xs"
              />
              <Button
                variant="outline"
                size="icon"
                ariaLabel={`Create folder in ${root.name}`}
                onclick={() => createFolder(root.name)}
              >
                <FolderPlus class="h-3.5 w-3.5" />
              </Button>
            </div>
            <label class="flex cursor-pointer items-center justify-center gap-2 rounded-md border border-dashed border-border px-3 py-2 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-accent-foreground">
              <Upload class="h-3.5 w-3.5" />
              {t('resources.skills.uploadToRoot')}
              <input type="file" class="hidden" aria-label={`Upload to ${root.name}`} onchange={(event) => uploadFile(root.name, event)} />
            </label>
          </div>
        {/if}
      </section>
    {/each}
  </div>

  {#if selectedFile}
    <section class="border-t border-border px-4 py-3">
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0">
          <h3 class="truncate text-sm font-semibold text-foreground">{selectedFile.name}</h3>
          <p class="text-xs text-muted-foreground">{selectedFile.path}</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          ariaLabel={`Download selected ${selectedFile.path}`}
          onclick={() => ondownload?.(selectedFile.path)}
        >
          <Download class="h-4 w-4" />
        </Button>
      </div>

      {#if selectedFile.preview_kind === 'image' && imagePreviewUrl}
        <img src={imagePreviewUrl} alt={selectedFile.name} class="mt-3 max-h-56 w-full rounded-md object-contain" />
      {:else if selectedFile.content !== undefined}
        <textarea
          aria-label="Resource source"
          class="mt-3 min-h-56 w-full resize-y rounded-md border border-input bg-background p-3 font-mono text-xs leading-5 text-foreground outline-none disabled:bg-muted/25"
          value={draft}
          disabled={!selectedCanEdit || busyPath === selectedFile.path}
          oninput={(event) => (draft = event.currentTarget.value)}
        ></textarea>
        {#if selectedCanEdit}
          <div class="mt-2 flex justify-end">
            <Button size="sm" disabled={busyPath === selectedFile.path} onclick={() => onsave?.(selectedFile.path, draft)} ariaLabel={t('resources.skills.saveResource')}>
              <Save class="h-3.5 w-3.5" />
              {t('resources.skills.saveResource')}
            </Button>
          </div>
        {/if}
      {:else}
        <p class="mt-3 text-xs text-muted-foreground">{t('resources.skills.previewUnavailable')}</p>
      {/if}
    </section>
  {/if}
</section>
