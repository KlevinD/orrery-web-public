import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { afterEach, describe, expect, it } from 'vitest'

const temporaryDirectories: string[] = []

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { recursive: true, force: true })
  }
})

describe('static asset deployment', () => {
  it('wires a persistent immutable asset volume into the web container', () => {
    const dockerfile = readFileSync('Dockerfile', 'utf8')
    const compose = readFileSync('../../docker-compose.yml', 'utf8')

    expect(dockerfile).toContain('COPY --from=build /src/web/build/_app/immutable /opt/orrery-web-immutable')
    expect(dockerfile).toContain('/docker-entrypoint.d/40-sync-immutable-assets.sh')
    expect(compose).toContain('orrery-web-immutable-assets:/usr/share/nginx/html/_app/immutable')
  })

  const unixIt = process.platform === 'win32' ? it.skip : it
  unixIt('keeps recent prior chunks while pruning expired chunks', () => {
    const root = mkdtempSync(join(tmpdir(), 'orrery-assets-'))
    temporaryDirectories.push(root)
    const source = join(root, 'source')
    const target = join(root, 'target')
    mkdirSync(join(source, 'nodes'), { recursive: true })
    mkdirSync(join(target, 'nodes'), { recursive: true })
    writeFileSync(join(source, 'nodes', 'current.js'), 'current')
    writeFileSync(join(target, 'nodes', 'previous.js'), 'previous')
    const stale = join(target, 'nodes', 'stale.js')
    writeFileSync(stale, 'stale')
    const staleAt = new Date(Date.now() - 10 * 24 * 60 * 60 * 1000)
    utimesSync(stale, staleAt, staleAt)

    const result = spawnSync('sh', ['docker-entrypoint.d/40-sync-immutable-assets.sh'], {
      cwd: process.cwd(),
      encoding: 'utf8',
      env: {
        ...process.env,
        ORRERY_WEB_IMMUTABLE_SOURCE_DIR: source,
        ORRERY_WEB_IMMUTABLE_TARGET_DIR: target,
        ORRERY_WEB_ASSET_RETENTION_DAYS: '7'
      }
    })

    expect(result.status, result.stderr).toBe(0)
    expect(readdirSync(join(target, 'nodes')).sort()).toEqual(['current.js', 'previous.js'])
  })
})
