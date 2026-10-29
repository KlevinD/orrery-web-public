import type { CitationEvidenceDTO } from '#lib/types/memory'

export function citationEvidenceKindLabel(kind: string) {
  switch (kind) {
    case 'knowledge_document':
      return 'Document'
    case 'knowledge_chunk':
      return 'Chunk'
    case 'knowledge_anchor':
      return 'Anchor'
    default:
      return kind || 'Evidence'
  }
}

export function formatCitationEvidenceTitle(evidence: CitationEvidenceDTO) {
  return evidence.title || evidence.id || evidence.document_id || evidence.page_id || evidence.provider_node_id || 'Evidence'
}

export function formatCitationEvidence(evidence: CitationEvidenceDTO) {
  return [
    evidence.knowledge_base_id ? `Knowledge base ${evidence.knowledge_base_id}` : '',
    evidence.id && evidence.title ? evidence.id : '',
    evidence.document_id ? `Document ${evidence.document_id}` : '',
    evidence.page_id ? `Page ${evidence.page_id}` : '',
    evidence.page_number ? `Page ${evidence.page_number}` : '',
    evidence.provider ? `Provider ${evidence.provider}` : '',
    evidence.provider_document_id ? `Provider doc ${evidence.provider_document_id}` : '',
    evidence.provider_node_id ? `Node ${evidence.provider_node_id}` : '',
    evidence.heading_path?.length ? evidence.heading_path.join(' / ') : '',
    evidence.structure_path?.length ? evidence.structure_path.join(' / ') : '',
    evidence.source_uri ?? '',
    evidence.content_hash ? `Hash ${shortHash(evidence.content_hash)}` : '',
    evidence.snippet_hash ? `Snippet ${shortHash(evidence.snippet_hash)}` : '',
    evidence.image_ref ? `Image ${evidence.image_ref}` : ''
  ].filter(Boolean)
}

export function citationEvidencePreviewHref(evidence: CitationEvidenceDTO) {
  const knowledgeBaseId = evidence.knowledge_base_id?.trim()
  const documentId = citationEvidenceDocumentId(evidence)
  if (!knowledgeBaseId || !documentId) return ''
  const params = new URLSearchParams()
  params.set('pages', String(evidence.page_number && evidence.page_number > 0 ? evidence.page_number : 1))
  if (evidence.provider_node_id) {
    params.set('anchor', evidence.provider_node_id)
  }
  return `/resources/knowledge/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}?${params.toString()}`
}

function citationEvidenceDocumentId(evidence: CitationEvidenceDTO) {
  if (evidence.document_id?.trim()) return evidence.document_id.trim()
  if (evidence.kind === 'knowledge_document' && evidence.id.trim()) return evidence.id.trim()
  const pagePrefix = ':page:'
  const pageIndex = evidence.id.indexOf(pagePrefix)
  if (pageIndex > 0) return evidence.id.slice(0, pageIndex)
  const chunkIndex = evidence.id.lastIndexOf(':')
  if (chunkIndex > 0) return evidence.id.slice(0, chunkIndex)
  return ''
}

function shortHash(value: string) {
  const [prefix, body] = value.includes(':') ? value.split(':', 2) : ['', value]
  const maxLength = prefix ? 8 : 12
  const short = body.length > maxLength ? body.slice(0, maxLength) : body
  return prefix ? `${prefix}:${short}` : short
}
