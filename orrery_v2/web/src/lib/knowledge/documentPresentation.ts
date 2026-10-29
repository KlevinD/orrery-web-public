import type { KnowledgeDocumentDTO, KnowledgeSemanticSourceDTO } from '#lib/types/api/knowledge'

export type KnowledgeDocumentKind = 'markdown' | 'text' | 'html' | 'pdf' | 'docx' | 'unknown'
export type KnowledgeContentKind = 'markdown' | 'plain'

export interface KnowledgeDocumentFolderNode {
  name: string
  path: string
  folders: KnowledgeDocumentFolderNode[]
  documents: KnowledgeDocumentDTO[]
  documentCount: number
}

export interface KnowledgeDocumentTree {
  folders: KnowledgeDocumentFolderNode[]
  documents: KnowledgeDocumentDTO[]
}

export const KNOWLEDGE_UPLOAD_ACCEPT = [
  '.md',
  '.markdown',
  '.txt',
  '.html',
  '.htm',
  '.pdf',
  '.docx',
  'text/markdown',
  'text/plain',
  'text/html',
  'application/pdf',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
].join(',')

const kindLabels: Record<KnowledgeDocumentKind, string> = {
  markdown: 'Markdown',
  text: '文本',
  html: 'HTML',
  pdf: 'PDF',
  docx: 'Word',
  unknown: '文件'
}

export function knowledgeDocumentKind(document?: Pick<KnowledgeDocumentDTO, 'filename' | 'content_type'>): KnowledgeDocumentKind {
  const extension = fileExtension(document?.filename)
  if (extension === 'md' || extension === 'markdown') return 'markdown'
  if (extension === 'txt') return 'text'
  if (extension === 'html' || extension === 'htm') return 'html'
  if (extension === 'pdf') return 'pdf'
  if (extension === 'docx') return 'docx'

  const contentType = normalizeContentType(document?.content_type)
  if (contentType === 'text/markdown' || contentType === 'text/x-markdown') return 'markdown'
  if (contentType === 'text/plain') return 'text'
  if (contentType === 'text/html' || contentType === 'application/xhtml+xml') return 'html'
  if (contentType === 'application/pdf') return 'pdf'
  if (contentType === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document') return 'docx'
  return 'unknown'
}

export function knowledgeDocumentTypeLabel(document?: Pick<KnowledgeDocumentDTO, 'filename' | 'content_type'>) {
  return kindLabels[knowledgeDocumentKind(document)]
}

export function knowledgeDocumentDisplayName(document?: Pick<KnowledgeDocumentDTO, 'filename'>) {
  const filename = document?.filename?.trim().replaceAll('\\', '/') ?? ''
  return filename.split('/').filter(Boolean).at(-1) || '未命名文档'
}

export function knowledgeDocumentSearchText(
  document: Pick<KnowledgeDocumentDTO, 'filename' | 'folder_path' | 'content_type'>,
  statusLabel = ''
) {
  return `${document.filename ?? ''} ${document.folder_path ?? ''} ${knowledgeDocumentTypeLabel(document)} ${statusLabel}`
    .toLocaleLowerCase()
}

export function buildKnowledgeDocumentTree(documents: KnowledgeDocumentDTO[]): KnowledgeDocumentTree {
  const root: MutableFolderNode = { name: '', path: '', folders: new Map(), documents: [] }
  for (const document of documents) {
    let node = root
    for (const segment of documentFolderSegments(document)) {
      const path = node.path ? `${node.path}/${segment}` : segment
      let child = node.folders.get(segment)
      if (!child) {
        child = { name: segment, path, folders: new Map(), documents: [] }
        node.folders.set(segment, child)
      }
      node = child
    }
    node.documents.push(document)
  }
  return {
    folders: [...root.folders.values()].map(finalizeFolder).sort(compareFolders),
    documents: root.documents.sort(compareDocuments)
  }
}

export function knowledgeSourceContentKind(source?: Pick<KnowledgeSemanticSourceDTO, 'content_type'>): KnowledgeContentKind {
  const contentType = normalizeContentType(source?.content_type)
  return contentType === 'text/markdown' || contentType === 'text/x-markdown' ? 'markdown' : 'plain'
}

export function knowledgeSourceTypeLabel(source?: Pick<KnowledgeSemanticSourceDTO, 'content_type'>) {
  return knowledgeSourceContentKind(source) === 'markdown' ? 'Markdown 正文' : '提取文本'
}

export function formatKnowledgeDocumentSize(size?: number) {
  if (!Number.isFinite(size) || !size || size < 1) return '大小未知'
  if (size < 1024) return `${Math.round(size)} B`
  const kilobytes = size / 1024
  if (kilobytes < 1024) return `${formatUnit(kilobytes)} KB`
  return `${formatUnit(kilobytes / 1024)} MB`
}

export function isSupportedKnowledgeFile(file: { name: string; type?: string }) {
  return knowledgeDocumentKind({ filename: file.name, content_type: file.type }) !== 'unknown'
}

function normalizeContentType(contentType?: string) {
  return contentType?.split(';')[0]?.trim().toLowerCase() ?? ''
}

interface MutableFolderNode {
  name: string
  path: string
  folders: Map<string, MutableFolderNode>
  documents: KnowledgeDocumentDTO[]
}

function documentFolderSegments(document: KnowledgeDocumentDTO) {
  const explicit = document.folder_path?.trim()
  const filename = document.filename?.trim().replaceAll('\\', '/') ?? ''
  const inferred = filename.includes('/') ? filename.slice(0, filename.lastIndexOf('/')) : ''
  return (explicit || inferred)
    .replaceAll('\\', '/')
    .split('/')
    .map((segment) => segment.trim())
    .filter((segment) => segment !== '' && segment !== '.' && segment !== '..')
}

function finalizeFolder(folder: MutableFolderNode): KnowledgeDocumentFolderNode {
  const folders = [...folder.folders.values()].map(finalizeFolder).sort(compareFolders)
  const documents = folder.documents.sort(compareDocuments)
  return {
    name: folder.name,
    path: folder.path,
    folders,
    documents,
    documentCount: documents.length + folders.reduce((total, child) => total + child.documentCount, 0)
  }
}

function compareFolders(left: KnowledgeDocumentFolderNode, right: KnowledgeDocumentFolderNode) {
  return compareNames(left.name, right.name)
}

function compareDocuments(left: KnowledgeDocumentDTO, right: KnowledgeDocumentDTO) {
  return compareNames(knowledgeDocumentDisplayName(left), knowledgeDocumentDisplayName(right))
}

function compareNames(left: string, right: string) {
  return left.localeCompare(right, 'zh-CN', { numeric: true, sensitivity: 'base' })
}

function fileExtension(filename?: string) {
  const value = filename?.trim().toLowerCase() ?? ''
  const dot = value.lastIndexOf('.')
  return dot >= 0 ? value.slice(dot + 1) : ''
}

function formatUnit(value: number) {
  return value >= 10 ? Math.round(value).toString() : value.toFixed(1).replace(/\.0$/, '')
}
