import { describe, expect, it } from 'vitest'
import { isActiveTaskStatus, isDoneTaskStatus } from './taskStatus'

describe('mission task status helpers', () => {
  it('treats backend in-flight task statuses as active', () => {
    expect(['active', 'started', 'created', 'assigned', 'todo', 'in_progress'].every(isActiveTaskStatus)).toBe(true)
    expect(isActiveTaskStatus('approved')).toBe(false)
    expect(isActiveTaskStatus('blocked')).toBe(false)
  })

  it('treats backend approved tasks as done', () => {
    expect(['done', 'completed', 'approved'].every(isDoneTaskStatus)).toBe(true)
    expect(isDoneTaskStatus('started')).toBe(false)
  })
})
