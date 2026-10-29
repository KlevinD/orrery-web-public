export type ToolSchemaFieldType = 'string' | 'number' | 'integer' | 'boolean' | 'array' | 'object'

export interface ToolSchemaEnumOption {
  label: string
  value: string
  schemaValue: unknown
}

export interface ToolSchemaField {
  name: string
  label: string
  type: ToolSchemaFieldType
  required: boolean
  description: string
  enumValues: ToolSchemaEnumOption[]
  defaultValue?: unknown
}

export interface ToolCallFormState {
  fields: ToolSchemaField[]
  values: Record<string, string>
  usesRawArguments: boolean
  rawArguments: string
}

type JsonObject = Record<string, unknown>

export function toolSchemaFields(schema: unknown): ToolSchemaField[] {
  if (!isRecord(schema) || schema.type !== 'object' || !isRecord(schema.properties)) return []
  const required = Array.isArray(schema.required) ? schema.required.filter((value): value is string => typeof value === 'string') : []

  return Object.entries(schema.properties)
    .filter((entry): entry is [string, JsonObject] => isRecord(entry[1]))
    .map(([name, property]) => ({
      name,
      label: typeof property.title === 'string' && property.title.trim() ? property.title : name,
      type: fieldType(property),
      required: required.includes(name),
      description: typeof property.description === 'string' ? property.description : '',
      enumValues: Array.isArray(property.enum) ? property.enum.map((value) => enumOption(value)) : [],
      defaultValue: property.default
    }))
}

export function formFromToolSchema(schema: unknown): ToolCallFormState {
  const fields = toolSchemaFields(schema)
  const usesRawArguments = fields.length === 0
  return {
    fields,
    values: Object.fromEntries(fields.map((field) => [field.name, displayValue(field.defaultValue, field.type)])),
    usesRawArguments,
    rawArguments: usesRawArguments ? '{}' : ''
  }
}

export function formFromToolArguments(schema: unknown, argumentsValue: Record<string, unknown> | undefined): ToolCallFormState {
  const form = formFromToolSchema(schema)
  const args = argumentsValue ?? {}
  if (form.usesRawArguments) {
    return {
      ...form,
      rawArguments: Object.keys(args).length > 0 ? JSON.stringify(args, null, 2) : form.rawArguments
    }
  }
  return {
    ...form,
    values: Object.fromEntries(
      form.fields.map((field) => [
        field.name,
        Object.prototype.hasOwnProperty.call(args, field.name)
          ? displayValue(args[field.name], field.type)
          : form.values[field.name]
      ])
    )
  }
}

export function setToolFieldValue(form: ToolCallFormState, name: string, value: string): ToolCallFormState {
  return {
    fields: form.fields,
    values: {
      ...form.values,
      [name]: value
    },
    usesRawArguments: form.usesRawArguments,
    rawArguments: form.rawArguments
  }
}

export function setRawToolArguments(form: ToolCallFormState, value: string): ToolCallFormState {
  return {
    fields: form.fields,
    values: form.values,
    usesRawArguments: form.usesRawArguments,
    rawArguments: value
  }
}

export function buildToolArguments(form: ToolCallFormState): Record<string, unknown> {
  if (form.usesRawArguments) {
    const raw = form.rawArguments.trim()
    if (raw === '') return {}
    const parsed = parseJsonValue('arguments', raw)
    if (!isRecord(parsed)) throw new Error('arguments must be a JSON object')
    return parsed
  }
  const entries: Array<[string, unknown]> = []
  for (const field of form.fields) {
    const rawValue = form.values[field.name] ?? ''
    if (rawValue !== '') entries.push([field.name, argumentValue(field, rawValue)])
  }
  for (const field of form.fields) {
    if (field.required && (form.values[field.name] ?? '') === '') throw new Error(`${field.name} is required`)
  }
  return Object.fromEntries(entries)
}

function argumentValue(field: ToolSchemaField, value: string): unknown {
  if (value === '') {
    if (field.required) throw new Error(`${field.name} is required`)
    return undefined
  }
  if (field.type === 'integer') {
    if (!/^-?\d+$/.test(value.trim())) throw new Error(`${field.name} must be an integer`)
    return Number.parseInt(value, 10)
  }
  if (field.type === 'number') {
    const parsed = Number(value)
    if (Number.isNaN(parsed)) throw new Error(`${field.name} must be a number`)
    return parsed
  }
  if (field.type === 'boolean') return value === 'true'
  if (field.type === 'array') {
    const parsed = parseJsonValue(field.name, value)
    if (!Array.isArray(parsed)) throw new Error(`${field.name} must be a JSON array`)
    return parsed
  }
  if (field.type === 'object') {
    const parsed = parseJsonValue(field.name, value)
    if (!isRecord(parsed)) throw new Error(`${field.name} must be a JSON object`)
    return parsed
  }
  const enumOption = field.enumValues.find((option) => option.value === value)
  if (enumOption) return enumOption.schemaValue
  return value
}

function parseJsonValue(name: string, value: string): unknown {
  try {
    return JSON.parse(value)
  } catch {
    throw new Error(`${name} must be valid JSON`)
  }
}

function displayValue(value: unknown, type: ToolSchemaFieldType): string {
  if (value === undefined || value === null) return ''
  if (type === 'array' || type === 'object') return JSON.stringify(value, null, 2)
  return String(value)
}

function enumOption(value: unknown): ToolSchemaEnumOption {
  return {
    label: String(value),
    value: String(value),
    schemaValue: value
  }
}

function fieldType(property: JsonObject): ToolSchemaFieldType {
  const type = Array.isArray(property.type) ? property.type.find((value) => value !== 'null') : property.type
  if (type === 'number' || type === 'integer' || type === 'boolean' || type === 'array' || type === 'object') return type
  return 'string'
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
