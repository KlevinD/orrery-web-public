import { describe, expect, it } from 'vitest'
import {
  availableOptions,
  buildInitialForm,
  optionsWithCurrent,
  optionsWithSelected,
  selectedOptions,
  selectedValues,
  transferIn,
  transferOut
} from './workforceMemberEditorHelpers'

const options = [
  { label: 'Planning', value: 'skill.plan' },
  { label: 'Review', value: 'skill.review' },
  { label: 'Git', value: 'tool.git' }
]

describe('workforceMemberEditorHelpers', () => {
  it('builds initial form defaults by member kind', () => {
    expect(buildInitialForm({}, 'agent')).toEqual(
      expect.objectContaining({
        agentKind: 'ai',
        employmentType: 'ai_agent',
        qaEnabled: 'false',
        enabled: 'true',
        runtimeConfig: {}
      })
    )
    expect(buildInitialForm({ realName: 'Lin', enabled: 'false' }, 'human')).toEqual(
      expect.objectContaining({
        realName: 'Lin',
        agentKind: 'human',
        employmentType: 'human',
        enabled: 'false'
      })
    )
    expect(buildInitialForm({ personalAssistant: true }, 'agent').personalAssistant).toBe(true)
  })

  it('keeps selected and available option lists stable for transfer controls', () => {
    expect(selectedValues(' skill.plan, tool.git ')).toEqual(['skill.plan', 'tool.git'])
    expect(availableOptions(options, 'skill.plan')).toEqual([
      { label: 'Review', value: 'skill.review' },
      { label: 'Git', value: 'tool.git' }
    ])
    expect(selectedOptions(options, 'skill.plan, missing.ref')).toEqual([
      { label: 'Planning', value: 'skill.plan' },
      { label: 'missing.ref', value: 'missing.ref' }
    ])
  })

  it('moves refs in and out of comma-separated editor fields', () => {
    expect(transferIn('skill.plan', ['tool.git', 'skill.plan'])).toBe('skill.plan, tool.git')
    expect(transferOut('skill.plan, tool.git', ['skill.plan'])).toBe('tool.git')
  })

  it('preserves current values for select controls', () => {
    expect(optionsWithCurrent(options, 'custom.ref')).toEqual([{ label: 'custom.ref', value: 'custom.ref' }, ...options])
    expect(optionsWithSelected(options, 'custom.ref')).toEqual([...options, { label: 'custom.ref', value: 'custom.ref' }])
  })
})
