import { describe, expect, it } from 'vitest'
import { citationEvidencePreviewHref, formatCitationEvidence, formatCitationEvidenceTitle } from './citationEvidence'
import type { CitationEvidenceDTO } from '#lib/types/memory'

describe('memory citation evidence helpers', () => {
  it('formats ontology citation evidence without exposing source content', () => {
    const evidence: CitationEvidenceDTO[] = [
      {
        kind: 'knowledge_document',
        id: 'doc-1',
        title: 'Orrery ontology plan',
        knowledge_base_id: 'kb-1',
        source_uri: 'obsidian://Orrery/Plan.md'
      },
      {
        kind: 'knowledge_chunk',
        id: 'chunk-1',
        knowledge_base_id: 'kb-1',
        document_id: 'doc-1',
        content_hash: 'sha256:abcdef1234567890'
      },
      {
        kind: 'knowledge_anchor',
        id: 'anchor-1',
        knowledge_base_id: 'kb-1',
        document_id: 'doc-1',
        page_number: 7,
        provider_node_id: 'node-42',
        snippet_hash: 'sha256:123456abcdef'
      }
    ]

    expect(formatCitationEvidenceTitle(evidence[0])).toBe('Orrery ontology plan')
    expect(formatCitationEvidence(evidence[0])).toEqual(['Knowledge base kb-1', 'doc-1', 'obsidian://Orrery/Plan.md'])
    expect(formatCitationEvidenceTitle(evidence[1])).toBe('chunk-1')
    expect(formatCitationEvidence(evidence[1])).toEqual(['Knowledge base kb-1', 'Document doc-1', 'Hash sha256:abcdef12'])
    expect(formatCitationEvidenceTitle(evidence[2])).toBe('anchor-1')
    expect(formatCitationEvidence(evidence[2])).toEqual(['Knowledge base kb-1', 'Document doc-1', 'Page 7', 'Node node-42', 'Snippet sha256:123456ab'])
    expect(citationEvidencePreviewHref(evidence[0])).toBe('/resources/knowledge/kb-1/documents/doc-1?pages=1')
    expect(citationEvidencePreviewHref(evidence[1])).toBe('/resources/knowledge/kb-1/documents/doc-1?pages=1')
    expect(citationEvidencePreviewHref(evidence[2])).toBe('/resources/knowledge/kb-1/documents/doc-1?pages=7&anchor=node-42')
  })
})
