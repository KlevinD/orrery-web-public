import { describe, expect, it } from 'vitest'
import { ontologyFeatureContextForPath } from './feature-context'

describe('ontologyFeatureContextForPath', () => {
  it.each([
    ['/', 'planning'],
    ['/todos/todo-1', 'planning'],
    ['/missions/mission-1', 'mission_control'],
    ['/people/employment', 'operations'],
    ['/resources/tools', 'resource_admin'],
    ['/resources/knowledge/base-1', 'knowledge'],
    ['/resources/memory', 'memory']
  ])('maps %s to the ontology domain %s', (pathname, search) => {
    expect(ontologyFeatureContextForPath(pathname).search).toBe(search)
  })

  it('keeps the whole ontology visible for features without a governed schema domain', () => {
    expect(ontologyFeatureContextForPath('/settings').search).toBeUndefined()
  })
})
