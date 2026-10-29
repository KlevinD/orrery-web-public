import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import KnowledgeSettingsPanelTestHost from './KnowledgeSettingsPanel.test-host.svelte'

describe('KnowledgeSettingsPanel', () => {
  it('renders PageIndex connection testing beside the API URL field', async () => {
    setLocale('zh-CN')
    const ontestpageindex = vi.fn()

    render(KnowledgeSettingsPanelTestHost, {
      props: {
        testResult: {
          status: 'ok',
          apiUrl: 'http://127.0.0.1:8091',
          endpoint: 'http://127.0.0.1:8091/healthz',
          httpStatus: 200,
          latencyMs: 12,
          message: 'PageIndex server is reachable.'
        },
        ontestpageindex
      }
    })

    const button = screen.getByRole('button', { name: '测试连接' })
    expect(button).toBeTruthy()
    expect(screen.getByDisplayValue('http://127.0.0.1:8091')).toBeTruthy()
    expect(screen.getByText('PageIndex 可连接')).toBeTruthy()
    expect(screen.getByText(/HTTP 200/)).toBeTruthy()
    expect(screen.getByText(/http:\/\/127\.0\.0\.1:8091\/healthz/)).toBeTruthy()

    await fireEvent.click(button)

    expect(ontestpageindex).toHaveBeenCalledTimes(1)
  })
})
