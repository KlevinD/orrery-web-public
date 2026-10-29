import type { ObsidianGraphLink, ObsidianGraphNode } from '#lib/components/graph/ObsidianForceGraph.svelte'
import type { KnowledgeBaseDTO, KnowledgeDocumentDTO } from '#lib/types/api/knowledge'
import type { ContextNeighborhoodDTO } from '#lib/api/ontology-context'
import type { MemoryRecordDTO } from '#lib/types/memory'

export interface ResourceGraphDetail {
  label: string
  value: string
}

export interface ResourceGraphNodeMeta extends Record<string, unknown> {
  kindLabel: string
  description?: string
  href?: string
  actionId?: string
  details?: ResourceGraphDetail[]
}

export interface ResourceGraphNode extends ObsidianGraphNode {
  meta: ResourceGraphNodeMeta
}

export interface ResourceRelationshipGraph {
  nodes: ResourceGraphNode[]
  links: ObsidianGraphLink[]
}

interface MemoryGraphSubject {
  id: string
  title: string
  owner: 'organization' | 'agent'
}

const memoryLayerLabels: Record<string, string> = {
  run: '运行记录',
  fact: '事实',
  experience: '经验',
  world: '制度与共识'
}

export function memoryRecordOntologyTenantId(record: MemoryRecordDTO) {
  const scopeId = record.scope.id.trim()
  if (record.scope.kind === 'organization' && scopeId) return scopeId
  if (['department', 'project', 'mission', 'discussion', 'agent'].includes(record.scope.kind) && scopeId) {
    return `${record.scope.kind}:${scopeId}`
  }
  return scopeId || record.profile_id || record.memory_id
}

export function buildKnowledgeRelationshipGraph(
  bases: KnowledgeBaseDTO[],
  documentsByBase: Record<string, KnowledgeDocumentDTO[]>
): ResourceRelationshipGraph {
  const nodes = new Map<string, ResourceGraphNode>()
  const links: ObsidianGraphLink[] = []

  for (const base of bases) {
    const ownerNodeId = `owner:${base.owner_type}:${base.owner_id}`
    if (!nodes.has(ownerNodeId)) {
      nodes.set(ownerNodeId, {
        id: ownerNodeId,
        label: base.owner_id,
        group: 'knowledgeOwner',
        value: 7,
        meta: {
          kindLabel: base.owner_type === 'organization' ? '组织' : '智能体',
          description: '知识库归属者',
          details: [{ label: '标识', value: base.owner_id }]
        }
      })
    }

    const documents = documentsByBase[base.id] ?? []
    const baseNodeId = `knowledge-base:${base.id}`
    nodes.set(baseNodeId, {
      id: baseNodeId,
      label: base.name,
      group: 'knowledgeBase',
      value: Math.max(6, 6 + documents.length),
      disabled: base.status === 'disabled' || base.status === 'deleting',
      meta: {
        kindLabel: '知识库',
        description: base.description,
        href: `/resources/knowledge/${encodeURIComponent(base.id)}`,
        details: [
          { label: '文档', value: String(documents.length) },
          { label: '可见性', value: base.visibility ?? '-' },
          { label: 'Provider', value: base.provider_display_name || base.provider || 'local' },
          { label: '状态', value: base.status ?? 'active' }
        ]
      }
    })
    links.push({ id: `${ownerNodeId}->${baseNodeId}`, source: ownerNodeId, target: baseNodeId, label: '拥有', width: 1 })

    for (const document of documents) {
      const documentNodeId = `knowledge-document:${document.id}`
      nodes.set(documentNodeId, {
        id: documentNodeId,
        label: document.filename || document.id,
        group: 'knowledgeDocument',
        value: document.status === 'indexed' ? 3 : 2,
        disabled: document.status === 'failed',
        meta: {
          kindLabel: '文档',
          description: document.folder_path || document.content_type,
          href: `/resources/knowledge/${encodeURIComponent(base.id)}/documents/${encodeURIComponent(document.id)}`,
          details: [
            { label: '知识库', value: base.name },
            { label: '目录', value: document.folder_path || '/' },
            { label: '类型', value: document.content_type || '-' },
            { label: '状态', value: document.status || '-' }
          ]
        }
      })
      links.push({ id: `${baseNodeId}->${documentNodeId}`, source: baseNodeId, target: documentNodeId, label: '包含' })
    }
  }

  return { nodes: [...nodes.values()], links }
}

export function buildMemoryRelationshipGraph(
  subject: MemoryGraphSubject,
  records: MemoryRecordDTO[]
): ResourceRelationshipGraph {
  const nodes = new Map<string, ResourceGraphNode>()
  const links: ObsidianGraphLink[] = []
  const subjectNodeId = `memory-subject:${subject.id}`

  nodes.set(subjectNodeId, {
    id: subjectNodeId,
    label: subject.title,
    group: subject.owner === 'organization' ? 'organization' : 'person',
    value: Math.max(8, 8 + Math.sqrt(records.length)),
    meta: {
      kindLabel: subject.owner === 'organization' ? '组织记忆' : '智能体记忆',
      description: `共 ${records.length} 条已加载记忆`,
      details: [{ label: '记忆记录', value: String(records.length) }]
    }
  })

  const recordIds = new Set(records.map((record) => record.memory_id))
  for (const record of records) {
    const layer = record.layer || 'fact'
    const layerNodeId = `memory-layer:${layer}`
    if (!nodes.has(layerNodeId)) {
      nodes.set(layerNodeId, {
        id: layerNodeId,
        label: memoryLayerLabels[layer] || layer,
        group: 'memoryLayer',
        value: 6,
        meta: { kindLabel: '记忆层', description: memoryLayerLabels[layer] || layer }
      })
      links.push({ id: `${subjectNodeId}->${layerNodeId}`, source: subjectNodeId, target: layerNodeId, label: '沉淀为', width: 1 })
    }

    const recordNodeId = memoryNodeId(record.memory_id)
    nodes.set(recordNodeId, {
      id: recordNodeId,
      label: record.topic || truncate(record.summary, 24) || record.memory_id,
      group: 'memory',
      value: Math.max(2, 2 + Math.min(6, record.reference_count ?? 0)),
      disabled: record.lifecycle_status !== 'active',
      meta: {
        kindLabel: memoryLayerLabels[layer] || '记忆',
        description: record.summary,
        actionId: record.memory_id,
        details: [
          { label: '置信度', value: `${Math.round(normalizeConfidence(record.confidence) * 100)}%` },
          { label: '状态', value: record.lifecycle_status || '-' },
          { label: '引用次数', value: String(record.reference_count ?? 0) },
          { label: '更新时间', value: formatDate(record.updated_at) }
        ]
      }
    })
    links.push({ id: `${layerNodeId}->${recordNodeId}`, source: layerNodeId, target: recordNodeId, label: '包含' })

    for (const sourceRef of unique([...(record.source_memory_refs ?? []), ...(record.lineage_refs ?? [])])) {
      const sourceNodeId = memoryNodeId(sourceRef)
      if (!nodes.has(sourceNodeId)) {
        nodes.set(sourceNodeId, {
          id: sourceNodeId,
          label: sourceRef,
          group: 'memorySource',
          value: 1.5,
          muted: true,
          meta: {
            kindLabel: '引用记忆',
            description: '当前结果集中未展开的来源记忆',
            details: [{ label: 'Memory ID', value: sourceRef }]
          }
        })
      }
      links.push({
        id: `${sourceNodeId}->${recordNodeId}:derived`,
        source: sourceNodeId,
        target: recordNodeId,
        label: recordIds.has(sourceRef) ? '演化为' : '引用'
      })
    }

    for (const item of record.citation_evidence ?? []) {
      const evidenceId = item.document_id || item.id
      if (!evidenceId) continue
      const evidenceNodeId = `evidence:${evidenceId}`
      if (!nodes.has(evidenceNodeId)) {
        const href = item.knowledge_base_id && item.document_id
          ? `/resources/knowledge/${encodeURIComponent(item.knowledge_base_id)}/documents/${encodeURIComponent(item.document_id)}`
          : undefined
        nodes.set(evidenceNodeId, {
          id: evidenceNodeId,
          label: item.title || item.heading_path?.at(-1) || evidenceId,
          group: 'evidence',
          value: 2,
          meta: {
            kindLabel: '知识证据',
            description: item.source_uri || item.heading_path?.join(' / '),
            href,
            details: [
              { label: '类型', value: item.kind || '-' },
              { label: '文档', value: item.document_id || item.id },
              { label: '页码', value: item.page_number ? String(item.page_number) : '-' }
            ]
          }
        })
      }
      links.push({ id: `${evidenceNodeId}->${recordNodeId}:evidence`, source: evidenceNodeId, target: recordNodeId, label: '支撑' })
    }
  }

  return { nodes: [...nodes.values()], links }
}

export interface MemoryOntologyGraphLabels {
  memory: string
  entity: string
  degree: string
  confidence: string
}

const defaultMemoryOntologyGraphLabels: MemoryOntologyGraphLabels = {
  memory: 'Memory',
  entity: 'Ontology entity',
  degree: 'Relationships',
  confidence: 'Confidence'
}

export function buildMemoryOntologyRelationshipGraph(
  graph?: ContextNeighborhoodDTO,
  labels: MemoryOntologyGraphLabels = defaultMemoryOntologyGraphLabels
): ResourceRelationshipGraph {
  if (!graph) return { nodes: [], links: [] }
  return {
    nodes: graph.nodes.map((node) => ({
      id: node.id,
      label: node.label || node.id,
      group: node.object_type,
      value: Math.max(2, node.id === graph.root_id ? 9 : 2 + Math.min(node.degree, 7)),
      disabled: Boolean(node.status && !['active', 'confirmed'].includes(node.status.toLowerCase())),
      meta: {
        kindLabel: node.object_type === 'memory_record' ? labels.memory : labels.entity,
        actionId: node.object_type === 'memory_record' ? node.id : undefined,
        details: [
          { label: labels.degree, value: String(node.degree) },
          ...(node.confidence === undefined ? [] : [{ label: labels.confidence, value: `${Math.round(normalizeConfidence(node.confidence) * 100)}%` }])
        ]
      }
    })),
    links: graph.edges
      .filter((edge) => !edge.status || edge.status.toLowerCase() === 'confirmed')
      .map((edge) => ({
        id: edge.id,
        source: edge.source_id,
        target: edge.target_id,
        label: edge.link_type_label || edge.link_type,
        width: Math.max(1, normalizeConfidence(edge.confidence ?? 1) * 2)
      }))
  }
}

function memoryNodeId(memoryId: string) {
  return `memory:${memoryId}`
}

function normalizeConfidence(value: number) {
  return value > 1 ? value / 100 : value
}

function truncate(value: string, maxLength: number) {
  return value.length > maxLength ? `${value.slice(0, maxLength)}...` : value
}

function formatDate(value: string) {
  const timestamp = Date.parse(value)
  return Number.isNaN(timestamp) ? value : new Date(timestamp).toLocaleDateString()
}

function unique(values: string[]) {
  return [...new Set(values.filter(Boolean))]
}
