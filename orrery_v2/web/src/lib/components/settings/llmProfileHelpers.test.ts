import { describe, expect, it } from 'vitest'
import {
  cleanLLMText,
  createLLMProfile,
  normalizeLLMProfile,
  normalizeLLMProfileDefaults,
  updateLLMProvider
} from './llmProfileHelpers'

describe('llmProfileHelpers', () => {
  it('creates provider defaults without embedding panel state', () => {
    expect(createLLMProfile('openai', 'profile-1', (provider) => `Provider ${provider}`)).toEqual(
      expect.objectContaining({
        profileId: 'profile-1',
        displayName: 'Provider openai',
        provider: 'openai',
        baseUrl: 'https://api.openai.com/v1',
        enabled: true,
        globalDefault: false
      })
    )
  })

  it('updates provider defaults only when existing values still match the old defaults', () => {
    const profile = createLLMProfile('openai', 'profile-1', (provider) => provider)

    updateLLMProvider(profile, 'anthropic', (provider) => provider)

    expect(profile.provider).toBe('anthropic')
    expect(profile.displayName).toBe('anthropic')
    expect(profile.baseUrl).toBe('https://api.anthropic.com/v1')
    expect(profile.model).toContain('claude')
  })

  it('normalizes profile text and preserves a single enabled global default', () => {
    const profile = normalizeLLMProfile(
      {
        ...createLLMProfile('', '', (provider) => provider || 'fallback'),
        displayName: '  ',
        baseUrl: ' https://example.test ',
        apiKey: ' key ',
        apiKeyRef: ' secret://llm/example ',
        model: ' model ',
        temperature: ' 0.3 ',
        contextLength: ' 8192 ',
        enabled: false,
        globalDefault: true
      },
      (provider) => `llm-${provider}`,
      (provider) => `Provider ${provider}`
    )

    expect(profile).toEqual(
      expect.objectContaining({
        profileId: 'llm-openai',
        displayName: 'Provider openai',
        provider: 'openai',
        baseUrl: 'https://example.test',
        apiKey: '',
        apiKeyRef: 'secret://llm/example',
        model: 'model',
        temperature: '0.3',
        contextLength: '8192',
        enabled: true,
        globalDefault: true
      })
    )
    expect(normalizeLLMProfileDefaults([
      { ...profile, profileId: 'first', globalDefault: true },
      { ...profile, profileId: 'second', globalDefault: true }
    ])).toEqual([
      expect.objectContaining({ profileId: 'first', globalDefault: false }),
      expect.objectContaining({ profileId: 'second', globalDefault: true })
    ])
  })

  it('cleans optional LLM text values consistently', () => {
    expect(cleanLLMText(42)).toBe('42')
    expect(cleanLLMText(null)).toBe('')
  })
})
