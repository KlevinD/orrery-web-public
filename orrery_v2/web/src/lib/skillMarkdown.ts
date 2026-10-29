export interface SkillMarkdownForm {
  name: string
  description: string
  version: string
  taskTypeTags: string
  prompt: string
  extraFrontmatter?: string
}

const emptyForm: SkillMarkdownForm = {
  name: '',
  description: '',
  version: '1.0.0',
  taskTypeTags: '',
  prompt: ''
}

export function parseSkillMarkdownForm(raw: string): SkillMarkdownForm {
  const normalized = raw.replace(/\r\n/g, '\n')
  if (!normalized.startsWith('---\n')) return { ...emptyForm, prompt: raw }
  const end = normalized.indexOf('\n---', 4)
  if (end < 0) return { ...emptyForm, prompt: raw }

  const frontmatter = normalized.slice(4, end)
  const prompt = normalized.slice(end + 4).replace(/^\n/, '').trimEnd()
  const form: SkillMarkdownForm = {
    name: firstScalar(frontmatter, 'name'),
    description: firstScalar(frontmatter, 'description'),
    version: firstScalar(frontmatter, 'version') || '1.0.0',
    taskTypeTags: listValues(frontmatter, 'task_type_tags').join(', '),
    prompt
  }
  const extraFrontmatter = remainingFrontmatter(frontmatter)
  if (extraFrontmatter) form.extraFrontmatter = extraFrontmatter
  return form
}

export function buildSkillMarkdown(form: SkillMarkdownForm): string {
  const tags = splitList(form.taskTypeTags)
  const lines = [
    '---',
    `name: ${yamlScalar(form.name)}`,
    `description: ${yamlScalar(form.description)}`,
    `version: ${yamlScalar(form.version || '1.0.0')}`
  ]
  if (tags.length > 0) {
    lines.push('task_type_tags:')
    for (const tag of tags) lines.push(`  - ${yamlScalar(tag)}`)
  }
  if (form.extraFrontmatter?.trim()) {
    lines.push(...form.extraFrontmatter.trim().split('\n'))
  }
  lines.push('---')
  lines.push((form.prompt || '').trimEnd())
  return `${lines.join('\n')}\n`
}

export function skillCreatePayload(form: SkillMarkdownForm) {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    version: (form.version || '1.0.0').trim(),
    task_type_tags: splitList(form.taskTypeTags),
    prompt: form.prompt.trimEnd()
  }
}

function firstScalar(frontmatter: string, key: string) {
  const pattern = new RegExp(`^${escapeRegExp(key)}:\\s*(.*)$`, 'm')
  const match = frontmatter.match(pattern)
  return unquoteScalar(match?.[1]?.trim() ?? '')
}

function listValues(frontmatter: string, key: string) {
  const lines = frontmatter.split('\n')
  const index = lines.findIndex((line) => line.trim() === `${key}:` || line.trim().startsWith(`${key}: [`))
  if (index < 0) return []
  const inline = lines[index].trim().match(/^[^:]+:\s*\[(.*)\]$/)
  if (inline) {
    return inline[1].split(',').map((item) => unquoteScalar(item.trim())).filter(Boolean)
  }
  const values: string[] = []
  for (let i = index + 1; i < lines.length; i += 1) {
    const line = lines[i]
    if (!/^\s+-\s+/.test(line)) break
    values.push(unquoteScalar(line.replace(/^\s+-\s+/, '').trim()))
  }
  return values.filter(Boolean)
}

function remainingFrontmatter(frontmatter: string) {
  const lines = frontmatter.split('\n')
  const remaining: string[] = []
  const scalarKeys = new Set(['name', 'description', 'version'])
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i]
    const key = line.match(/^([A-Za-z0-9_-]+):/)?.[1]
    if (key && scalarKeys.has(key)) continue
    if (key === 'task_type_tags') {
      while (i + 1 < lines.length && /^\s+-\s+/.test(lines[i + 1])) i += 1
      continue
    }
    remaining.push(line)
  }
  return trimBlankLines(remaining).join('\n')
}

function trimBlankLines(lines: string[]) {
  let start = 0
  let end = lines.length
  while (start < end && lines[start].trim() === '') start += 1
  while (end > start && lines[end - 1].trim() === '') end -= 1
  return lines.slice(start, end)
}

function splitList(value: string) {
  return value.split(',').map((item) => item.trim()).filter(Boolean)
}

function yamlScalar(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return '""'
  if (/^[A-Za-z0-9_. -]+$/.test(trimmed) && !/^(true|false|null|~)$/i.test(trimmed)) return trimmed
  return JSON.stringify(trimmed)
}

function unquoteScalar(value: string) {
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return value.slice(1, -1)
  }
  return value
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
