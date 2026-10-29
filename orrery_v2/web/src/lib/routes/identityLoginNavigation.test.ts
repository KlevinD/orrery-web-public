import { describe, expect, it, vi } from 'vitest'
import { navigateToIdentityLogin, navigateToIdentityLogout, navigateToLoginReturnTo } from './identityLoginNavigation'

describe('identity login navigation', () => {
  it('uses a full browser navigation for the provider begin endpoint', () => {
    const assign = vi.fn()

    navigateToIdentityLogin('/api/auth/identity/kratos/begin?return_to=%2Fmissions', { assign })

    expect(assign).toHaveBeenCalledWith('/api/auth/identity/kratos/begin?return_to=%2Fmissions')
  })

  it('uses a full browser navigation for a server-owned authorization callback', () => {
    const assign = vi.fn()

    navigateToLoginReturnTo('/oauth/device/authorize?state=pairing-state', { assign })

    expect(assign).toHaveBeenCalledWith('/oauth/device/authorize?state=pairing-state')
  })

  it('uses a full browser navigation for provider logout', () => {
    const assign = vi.fn()

    navigateToIdentityLogout('/api/auth/identity/kratos/logout?return_to=%2Flogin', { assign })

    expect(assign).toHaveBeenCalledWith('/api/auth/identity/kratos/logout?return_to=%2Flogin')
  })
})
