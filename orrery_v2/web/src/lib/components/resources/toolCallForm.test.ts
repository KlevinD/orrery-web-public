import { describe, expect, it } from 'vitest'
import {
  buildToolArguments,
  formFromToolArguments,
  formFromToolSchema,
  setToolFieldValue,
  toolSchemaFields
} from './toolCallForm'

describe('tool call form helpers', () => {
  const schema = {
    type: 'object',
    required: ['query'],
    properties: {
      query: { type: 'string', description: 'Search query' },
      limit: { type: 'integer', default: 10 },
      exact: { type: 'boolean', default: false },
      labels: { type: 'array', items: { type: 'string' } }
    }
  }

  it('builds fields from MCP input schema properties', () => {
    expect(toolSchemaFields(schema)).toEqual([
      { name: 'query', label: 'query', type: 'string', required: true, description: 'Search query', enumValues: [], defaultValue: undefined },
      { name: 'limit', label: 'limit', type: 'integer', required: false, description: '', enumValues: [], defaultValue: 10 },
      { name: 'exact', label: 'exact', type: 'boolean', required: false, description: '', enumValues: [], defaultValue: false },
      { name: 'labels', label: 'labels', type: 'array', required: false, description: '', enumValues: [], defaultValue: undefined }
    ])
  })

  it('converts manually entered field values to tool arguments', () => {
    let form = formFromToolSchema(schema)
    form = setToolFieldValue(form, 'query', 'is:open')
    form = setToolFieldValue(form, 'limit', '25')
    form = setToolFieldValue(form, 'exact', 'true')
    form = setToolFieldValue(form, 'labels', '["bug","frontend"]')

    expect(buildToolArguments(form)).toEqual({
      query: 'is:open',
      limit: 25,
      exact: true,
      labels: ['bug', 'frontend']
    })
  })

  it('rejects invalid JSON for array and object fields', () => {
    const form = setToolFieldValue(formFromToolSchema(schema), 'labels', 'bug,frontend')

    expect(() => buildToolArguments(form)).toThrow('labels must be valid JSON')
  })

  it('builds raw JSON arguments when schema has no renderable properties', () => {
    const form = formFromToolSchema(undefined)

    expect(form.usesRawArguments).toBe(true)
    expect(buildToolArguments({ ...form, rawArguments: '{"query":"is:open","limit":5}' })).toEqual({
      query: 'is:open',
      limit: 5
    })
  })

  it('prefills schema fields from existing arguments', () => {
    const form = formFromToolArguments(
      {
        type: 'object',
        required: ['query'],
        properties: {
          query: { type: 'string' },
          limit: { type: 'integer', default: 5 },
          filters: { type: 'object' },
          archived: { type: 'boolean' }
        }
      },
      {
        query: 'customer feedback',
        limit: 10,
        filters: { region: 'cn' },
        archived: false
      }
    )

    expect(form.values.query).toBe('customer feedback')
    expect(form.values.limit).toBe('10')
    expect(form.values.filters).toBe(JSON.stringify({ region: 'cn' }, null, 2))
    expect(form.values.archived).toBe('false')
  })

  it('prefills raw arguments when schema cannot render fields', () => {
    const form = formFromToolArguments(undefined, { title: 'Ship scheduler tool call' })

    expect(form.usesRawArguments).toBe(true)
    expect(form.rawArguments).toBe(JSON.stringify({ title: 'Ship scheduler tool call' }, null, 2))
  })

  it('uses JSON schema enum values with their original non-string types', () => {
    const fields = toolSchemaFields({
      type: 'object',
      properties: {
        priority: { type: 'integer', enum: [1, 2] }
      }
    })

    expect(fields[0].enumValues.map((option) => option.label)).toEqual(['1', '2'])

    let form = formFromToolSchema({
      type: 'object',
      properties: {
        priority: { type: 'integer', enum: [1, 2] }
      }
    })
    form = setToolFieldValue(form, 'priority', fields[0].enumValues[1].value)

    expect(buildToolArguments(form)).toEqual({ priority: 2 })
  })
})
