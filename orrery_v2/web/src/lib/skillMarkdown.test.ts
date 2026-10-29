import { describe, expect, it } from 'vitest'
import { buildSkillMarkdown, parseSkillMarkdownForm } from './skillMarkdown'

describe('skill markdown form helpers', () => {
  it('moves editable skill metadata into form fields and keeps the prompt body separate', () => {
    const parsed = parseSkillMarkdownForm(`---
name: docx-writer
description: Build DOCX files
version: 1.2.0
task_type_tags:
  - writing
  - document
---
# Prompt

Use the DOCX API.`)

    expect(parsed).toEqual({
      name: 'docx-writer',
      description: 'Build DOCX files',
      version: '1.2.0',
      taskTypeTags: 'writing, document',
      prompt: '# Prompt\n\nUse the DOCX API.'
    })
  })

  it('builds SKILL.md frontmatter from form fields before saving', () => {
    expect(buildSkillMarkdown({
      name: 'docx-writer',
      description: 'Build DOCX files',
      version: '1.2.0',
      taskTypeTags: 'writing, document',
      prompt: '# Prompt'
    })).toBe(`---
name: docx-writer
description: Build DOCX files
version: 1.2.0
task_type_tags:
  - writing
  - document
---
# Prompt
`)
  })

  it('preserves frontmatter fields that are not edited through the form', () => {
    const parsed = parseSkillMarkdownForm(`---
name: docx-writer
description: Build DOCX files
version: 1.2.0
required_tools:
  - docx.export
evidence_refs:
  - docs/style.md
task_type_tags: [writing, document]
---
# Prompt`)

    expect(parsed).toEqual({
      name: 'docx-writer',
      description: 'Build DOCX files',
      version: '1.2.0',
      taskTypeTags: 'writing, document',
      prompt: '# Prompt',
      extraFrontmatter: `required_tools:
  - docx.export
evidence_refs:
  - docs/style.md`
    })

    expect(buildSkillMarkdown({ ...parsed, name: 'docx-editor' })).toContain(`required_tools:
  - docx.export
evidence_refs:
  - docs/style.md`)
  })
})
