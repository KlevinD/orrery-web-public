// @vitest-environment node

import { describe, expect, it } from 'vitest'
import config, { resolveApiProxyTarget } from '../../../vite.config'

describe('vite development API proxy', () => {
  it('routes browser API calls to the Go v2 backend during local development', () => {
    const developmentConfig = typeof config === 'function'
      ? config({ command: 'serve', mode: 'development', isSsrBuild: false, isPreview: false })
      : config

    expect(developmentConfig.server?.proxy?.['/api']).toMatchObject({
      target: 'http://127.0.0.1:8080',
      changeOrigin: true,
      xfwd: true,
      ws: true
    })
    expect(developmentConfig.server?.proxy?.['/oauth']).toMatchObject({
      target: 'http://127.0.0.1:8080',
      changeOrigin: false,
      xfwd: true
    })
  })

  it('allows overriding the local API proxy target for parallel previews', () => {
    expect(resolveApiProxyTarget({ VITE_API_PROXY_TARGET: 'http://127.0.0.1:18081' })).toBe('http://127.0.0.1:18081')
  })
})
