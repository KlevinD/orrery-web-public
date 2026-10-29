import { describe, expect, it } from 'vitest'
import {
  buildKnowledgeDocumentTree,
  formatKnowledgeDocumentSize,
  isSupportedKnowledgeFile,
  knowledgeDocumentDisplayName,
  knowledgeDocumentKind,
  knowledgeDocumentSearchText,
  knowledgeDocumentTypeLabel,
  knowledgeSourceContentKind,
  KNOWLEDGE_UPLOAD_ACCEPT
} from './documentPresentation'

describe('knowledge document presentation', () => {
  it('recognizes every format supported by the ingestion service', () => {
    expect(knowledgeDocumentKind({ filename: 'notes.md', content_type: 'text/plain' })).toBe('markdown')
    expect(knowledgeDocumentKind({ filename: 'brief.txt', content_type: '' })).toBe('text')
    expect(knowledgeDocumentKind({ filename: 'page.html', content_type: '' })).toBe('html')
    expect(knowledgeDocumentKind({ filename: 'report.pdf', content_type: '' })).toBe('pdf')
    expect(knowledgeDocumentKind({ filename: 'plan.docx', content_type: '' })).toBe('docx')
    expect(knowledgeDocumentTypeLabel({ filename: 'plan.docx' })).toBe('Word')
    expect(KNOWLEDGE_UPLOAD_ACCEPT).toContain('application/pdf')
  })

  it('keeps unsupported media outside the current upload contract', () => {
    expect(isSupportedKnowledgeFile({ name: 'photo.png', type: 'image/png' })).toBe(false)
    expect(isSupportedKnowledgeFile({ name: 'meeting.mp3', type: 'audio/mpeg' })).toBe(false)
    expect(isSupportedKnowledgeFile({ name: 'brief.pdf', type: '' })).toBe(true)
  })

  it('distinguishes markdown source from extracted plain text and formats sizes', () => {
    expect(knowledgeSourceContentKind({ content_type: 'text/markdown' })).toBe('markdown')
    expect(knowledgeSourceContentKind({ content_type: 'application/pdf' })).toBe('plain')
    expect(formatKnowledgeDocumentSize(8640)).toBe('8.4 KB')
    expect(formatKnowledgeDocumentSize(2 * 1024 * 1024)).toBe('2 MB')
  })

  it('builds a nested folder index and keeps unclassified documents at the root', () => {
    const tree = buildKnowledgeDocumentTree([
      { id: 'one', knowledge_base_id: 'kb', filename: 'report.pdf', folder_path: '02 市场报告/2026' },
      { id: 'two', knowledge_base_id: 'kb', filename: '研究框架/访谈/提纲.md' },
      { id: 'three', knowledge_base_id: 'kb', filename: 'README.md' }
    ])

    expect(tree.documents.map((document) => document.id)).toEqual(['three'])
    expect(tree.folders.map((folder) => folder.name)).toEqual(['02 市场报告', '研究框架'])
    expect(tree.folders[0].folders[0]).toMatchObject({ name: '2026', documentCount: 1 })
    expect(tree.folders[1].folders[0].documents[0].id).toBe('two')
    expect(knowledgeDocumentDisplayName({ filename: '研究框架/访谈/提纲.md' })).toBe('提纲.md')
  })

  it('includes the complete directory path in document search text', () => {
    const text = knowledgeDocumentSearchText({
      filename: 'design.md',
      folder_path: '04-Resources/Plan/Orrery',
      content_type: 'text/markdown'
    }, '已索引')

    expect(text).toContain('04-resources/plan/orrery')
    expect(text).toContain('design.md')
    expect(text).toContain('markdown')
    expect(text).toContain('已索引')
  })
})
