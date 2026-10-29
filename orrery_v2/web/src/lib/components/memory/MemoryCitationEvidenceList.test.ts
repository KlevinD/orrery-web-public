import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import MemoryCitationEvidenceList from './MemoryCitationEvidenceList.svelte'

describe('MemoryCitationEvidenceList', () => {
  it('renders compact document, chunk, and anchor citation evidence', () => {
    render(MemoryCitationEvidenceList, {
      props: {
        label: '引用证据',
        evidence: [
          {
            kind: 'knowledge_document',
            id: 'doc-1',
            title: 'Ontology plan',
            knowledge_base_id: 'kb-1',
            source_uri: 'obsidian://Orrery/Plan.md'
          },
          {
            kind: 'knowledge_chunk',
            id: 'chunk-1',
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
      }
    })

    expect(screen.getByText('引用证据')).toBeTruthy()
    expect(screen.getByText('Document')).toBeTruthy()
    expect(screen.getByText('Chunk')).toBeTruthy()
    expect(screen.getByText('Anchor')).toBeTruthy()
    expect(screen.getByText('Ontology plan')).toBeTruthy()
    expect(screen.getByText('Hash sha256:abcdef12')).toBeTruthy()
    expect(screen.getByText('Page 7')).toBeTruthy()
    expect(screen.getByText('Node node-42')).toBeTruthy()
    expect(screen.getByRole('link', { name: /source/i }).getAttribute('href')).toBe('obsidian://Orrery/Plan.md')
    expect(screen.getAllByRole('link', { name: /preview/i })[0].getAttribute('href')).toBe('/resources/knowledge/kb-1/documents/doc-1?pages=1')
    expect(screen.getAllByRole('link', { name: /preview/i })[1].getAttribute('href')).toBe('/resources/knowledge/kb-1/documents/doc-1?pages=7&anchor=node-42')
  })
})
