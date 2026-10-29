import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('knowledge nginx routing', () => {
  it('prefers prerendered html before same-named directories', () => {
    const config = readFileSync('nginx.conf', 'utf8')
    expect(config).toContain('try_files ${uri}.html $uri $uri/ /index.html;')
  })

  it('never serves the SPA fallback for immutable assets or service workers', () => {
    const config = readFileSync('nginx.conf', 'utf8')
    expect(config).toContain('location ^~ /_app/immutable/')
    expect(config).toContain('~^/_app/immutable/ "public, max-age=31536000, immutable"')
    expect(config).toContain('location = /service-worker.js')
  })

  it('compresses textual application assets and varies caches by encoding', () => {
    const config = readFileSync('nginx.conf', 'utf8')
    expect(config).toContain('gzip on;')
    expect(config).toContain('gzip_vary on;')
    expect(config).toContain('application/javascript')
    expect(config).toContain('text/css')
  })
})
