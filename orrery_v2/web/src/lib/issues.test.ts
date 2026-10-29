import { describe, expect, it } from 'vitest'
import { issueNumber } from './issues'

describe('issueNumber', () => {
  it('renders stable GitHub-style issue numbers without exposing internal ids', () => {
    expect(issueNumber('todo-backlog')).toMatch(/^#\d{5}$/)
    expect(issueNumber('todo-backlog')).toBe(issueNumber('todo-backlog'))
    expect(issueNumber('idea-1')).toMatch(/^#\d{5}$/)
    expect(issueNumber('todo-backlog')).not.toContain('todo')
  })
})
