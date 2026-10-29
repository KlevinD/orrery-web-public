import { describe, expect, it } from 'vitest'
import { buildKnowledgeRelationshipGraph, buildMemoryOntologyRelationshipGraph, buildMemoryRelationshipGraph, memoryRecordOntologyTenantId } from './resourceRelationships'
import type { KnowledgeBaseDTO, KnowledgeDocumentDTO } from '#lib/types/api/knowledge'
import type { MemoryRecordDTO } from '#lib/types/memory'

describe('resource relationship graphs', () => {
  it('maps memory scopes to ontology tenant ids outside the page component', () => {
    const record = (kind: string, id: string, profileId = 'profile-1') => ({
      memory_id: 'memory-1', profile_id: profileId, scope: { kind, id }
    }) as MemoryRecordDTO

    expect(memoryRecordOntologyTenantId(record('organization', 'org-1'))).toBe('org-1')
    expect(memoryRecordOntologyTenantId(record('agent', 'agent-1'))).toBe('agent:agent-1')
    expect(memoryRecordOntologyTenantId(record('', ''))).toBe('profile-1')
  })

  it('connects knowledge owners, bases, and documents', () => {
    const base = { id: 'kb-1', name: 'Research', owner_type: 'agent', owner_id: 'agent-1' } as KnowledgeBaseDTO
    const document = { id: 'doc-1', knowledge_base_id: 'kb-1', filename: 'brief.md', status: 'indexed' } as KnowledgeDocumentDTO
    const graph = buildKnowledgeRelationshipGraph([base], { 'kb-1': [document] })

    expect(graph.nodes.map((node) => node.id)).toEqual([
      'owner:agent:agent-1',
      'knowledge-base:kb-1',
      'knowledge-document:doc-1'
    ])
    expect(graph.links).toEqual(expect.arrayContaining([
      expect.objectContaining({ source: 'owner:agent:agent-1', target: 'knowledge-base:kb-1' }),
      expect.objectContaining({ source: 'knowledge-base:kb-1', target: 'knowledge-document:doc-1' })
    ]))
  })

  it('connects memory layers, lineage, and cited knowledge evidence', () => {
    const record = {
      memory_id: 'memory-2',
      layer: 'experience',
      summary: 'Prefer concise status updates',
      topic: 'Communication preference',
      source_memory_refs: ['memory-1'],
      citation_evidence: [{ kind: 'knowledge_document', id: 'evidence-1', document_id: 'doc-1', knowledge_base_id: 'kb-1' }],
      confidence: 0.9,
      lifecycle_status: 'active',
      updated_at: '2026-08-14T08:00:00Z'
    } as MemoryRecordDTO
    const graph = buildMemoryRelationshipGraph({ id: 'agent-1', title: 'Agent One', owner: 'agent' }, [record])

    expect(graph.nodes.map((node) => node.id)).toEqual(expect.arrayContaining([
      'memory-subject:agent-1',
      'memory-layer:experience',
      'memory:memory-2',
      'memory:memory-1',
      'evidence:doc-1'
    ]))
    expect(graph.links).toEqual(expect.arrayContaining([
      expect.objectContaining({ target: 'memory:memory-2', label: '引用' }),
      expect.objectContaining({ source: 'evidence:doc-1', label: '支撑' })
    ]))
  })

  it('maps the persisted ontology neighborhood without synthetic memory layers', () => {
    const graph = buildMemoryOntologyRelationshipGraph({
      root_id: 'memory-1',
      nodes: [
        { id: 'memory-1', object_type: 'memory_record', label: 'Release lesson', degree: 1 },
        { id: 'project-1', object_type: 'project', label: 'Orrery', degree: 1 }
      ],
      edges: [
        { id: 'link-1', link_type: 'memory_mentions_project', link_type_label: '记忆提及项目', source_id: 'memory-1', target_id: 'project-1', status: 'confirmed' }
      ],
      visible_nodes: 2,
      visible_edges: 1,
      applied_depth: 1,
      applied_max_nodes: 100,
      applied_max_edges: 200,
      truncated: false
    })

    expect(graph.nodes.map((node) => node.id)).toEqual(['memory-1', 'project-1'])
    expect(graph.nodes.some((node) => node.group === 'memoryLayer')).toBe(false)
    expect(graph.links).toEqual([expect.objectContaining({
      source: 'memory-1', target: 'project-1', label: '记忆提及项目'
    })])
  })
})
