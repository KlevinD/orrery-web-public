import { describe, expect, it } from 'vitest'
import { attributeLabel, attributeValueLabel, definitionKindLabel, objectTypeLabel, schemaRelationLabel } from './display-labels'

describe('ontology display labels', () => {
  it('translates ontology metadata without changing unknown stable identifiers', () => {
    expect(definitionKindLabel('link')).toBe('关系')
    expect(schemaRelationLabel('projects_link')).toBe('投影关系')
    expect(objectTypeLabel('mission')).toBe('使命')
    expect(attributeLabel('from_object_type')).toBe('起点对象类型')
    expect(attributeValueLabel('from_object_type', 'task')).toBe('任务')
    expect(schemaRelationLabel('tenant_custom_relation')).toBe('tenant_custom_relation')
  })
})
