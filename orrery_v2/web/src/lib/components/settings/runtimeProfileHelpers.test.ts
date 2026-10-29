import { describe, expect, it } from 'vitest'
import {
  cloneRuntimeConfig,
  runtimeProfileCanSubmit,
  runtimeConfigToFormValues,
  runtimeProfileIDFromDiscovery,
  stringRuntimeConfigValue
} from './runtimeProfileHelpers'

describe('runtimeProfileHelpers', () => {
  it('clones list values without sharing mutable arrays', () => {
    const source = { executable: 'codex', args: ['--model', 'gpt-5'] }
    const clone = cloneRuntimeConfig(source)

    expect(clone).toEqual(source)
    expect(clone.args).not.toBe(source.args)
  })

  it('converts runtime config values into form strings', () => {
    expect(runtimeConfigToFormValues({ executable: 'codex', args: ['--sandbox', 'danger-full-access'] })).toEqual({
      executable: 'codex',
      args: '--sandbox danger-full-access'
    })
  })

  it('normalizes discovery profile ids from config refs with runtime fallback', () => {
    expect(runtimeProfileIDFromDiscovery({ kind: 'codex', configRef: ' codex/local profile ', label: '', executable: '', version: '', status: '', source: '', runtimeConfig: {}, notes: [] })).toBe('codex-local-profile')
    expect(runtimeProfileIDFromDiscovery({ kind: 'openclaw', configRef: '', label: '', executable: '', version: '', status: '', source: '', runtimeConfig: {}, notes: [] })).toBe('runtime-openclaw-local')
  })

  it('formats runtime config values as command text', () => {
    expect(stringRuntimeConfigValue(['codex', '--approval', 'never'])).toBe('codex --approval never')
    expect(stringRuntimeConfigValue(undefined)).toBe('')
  })

  it('allows Claude Agent SDK profiles without a local executable field', () => {
    expect(runtimeProfileCanSubmit({
      profileId: 'runtime-claude-sdk',
      displayName: 'Claude SDK',
      runtimeKind: 'claude',
      runtimeConfigRef: 'runtime-claude-sdk',
      runtimeConfig: {
        mode: 'agent_sdk',
        approval_timeout_ms: '600000'
      },
      enabled: true
    })).toBe(true)
  })

  it('keeps local runtime profiles requiring an executable', () => {
    expect(runtimeProfileCanSubmit({
      profileId: 'runtime-claude-local',
      displayName: 'Claude',
      runtimeKind: 'claude',
      runtimeConfigRef: 'runtime-claude-local',
      runtimeConfig: {
        mode: 'local'
      },
      enabled: true
    })).toBe(false)
  })
})
