import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('KnowledgeDocumentTree', () => {
  it('renders recursive, expandable folders and selectable documents', () => {
    const source = readFileSync('src/lib/components/knowledge/KnowledgeDocumentTree.svelte', 'utf-8')

    expect(source).toContain('{#snippet folderNode(')
    expect(source).toContain('{@render folderNode(child, depth + 1)}')
    expect(source).toContain('aria-expanded={expanded}')
    expect(source).toContain('toggleFolder(folder)')
    expect(source).toContain('onSelect(document.id)')
    expect(source).toContain("? 'selection-wash selected' : ''")
    expect(source).toContain('searching || expandedFolders[folder.path] === true')
  })
})
