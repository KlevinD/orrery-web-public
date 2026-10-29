import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('root layout session bootstrap', () => {
  it('treats the server cookie session as authoritative', () => {
    const source = readFileSync('src/routes/+layout.svelte', 'utf-8')

    expect(source).toContain('await authApi.currentSession()')
    expect(source).toContain('if (dto)')
    expect(source).toContain('applySession(authSessionFromDTO(')
    expect(source).toContain('clearSession()')
    expect(source).not.toContain('loadSession')
    expect(source.indexOf('clearSession()')).toBeLessThan(source.indexOf('await authApi.currentSession()'))
    expect(source).toContain('{#key sessionRenderKey}')
    expect(source).toContain('session.securityContextVersion')
  })

  it('keeps restricted sessions on the password-change screen', () => {
    const source = readFileSync('src/routes/+layout.svelte', 'utf-8')

    expect(source).toContain('if (session.mustChangePassword)')
    expect(source).toContain("$page.url.searchParams.get('change_password') !== '1'")
    expect(source).toContain('passwordChangeURLWithReturnTo($page.url)')
    expect(source).toContain('session.authenticated && !session.mustChangePassword && isLoginPage')
    expect(source).toContain('session.authenticated && !session.mustChangePassword')
    expect(source).toContain('navigateToLoginReturnTo(safeReturnToFromURL($page.url))')
  })

  it('renders the compliance footer globally without route-specific hiding', () => {
    const source = readFileSync('src/routes/+layout.svelte', 'utf-8')

    expect(source).toContain("import ComplianceFooter from '#lib/components/layout/ComplianceFooter.svelte'")
    expect(source).toContain('<ComplianceFooter />')
    expect(source).not.toContain('hideFixedFooterLink')
  })
})
