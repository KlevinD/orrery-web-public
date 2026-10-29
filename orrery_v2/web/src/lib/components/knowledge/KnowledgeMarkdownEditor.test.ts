import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('KnowledgeMarkdownEditor', () => {
  it('uses CodeMirror markdown with edit, split, and read views', () => {
    const source = readFileSync('src/lib/components/knowledge/KnowledgeMarkdownEditor.svelte', 'utf-8')

    expect(source).toContain("import('@codemirror/commands')")
    expect(source).toContain("import('@codemirror/lang-markdown')")
    expect(source).not.toContain('basicSetup')
    expect(source).toContain('EditorView.updateListener')
    expect(source).toContain("type ViewMode = 'edit' | 'split' | 'read'")
    expect(source).toContain("contentKind?: 'markdown' | 'plain'")
    expect(source).toContain("readOnly?: boolean")
    expect(source).toContain('编辑视图')
    expect(source).toContain('分屏视图')
    expect(source).toContain('阅读视图')
    expect(source).toContain('renderSafeMarkdown(value)')
    expect(source).toContain('knowledge-plain-text')
    expect(source).toContain("if (readOnly && mode !== 'read') mode = 'read'")
    expect(source).toContain("? 'selection-wash active' : ''")
    expect(source).toContain('color-mix(in srgb, var(--brand) 16%, transparent)')
    expect(source).not.toContain('hsl(var(')
  })
})
