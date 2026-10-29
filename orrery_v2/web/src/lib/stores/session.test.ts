import { beforeEach, describe, expect, it } from 'vitest'
import { queryClient } from '#lib/query-client'
import { applySession, clearSession, currentSessionUserIdentityIds, isCurrentSessionUserIdentity, session } from './session.svelte'

describe('session store', () => {
  beforeEach(() => {
    clearSession()
  })

  it('starts without a local fallback user', () => {
    expect(session.authenticated).toBe(false)
    expect(session.actorId).toBe('')
    expect(session.displayName).toBe('')
    expect(session.subjectProvider).toBe('')
    expect(session.permissionScope).toEqual([])
  })

  it('retains and clears the authenticated identity provider', () => {
    applySession({
      subjectProvider: ' Kratos ',
      expiresAt: '',
      permissionScope: [],
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: 'human-operator',
        role: 'member'
      }
    })

    expect(session.subjectProvider).toBe('kratos')

    clearSession()
    expect(session.subjectProvider).toBe('')
  })

  it('applies authenticated superadmin identity and backend scopes', () => {
    applySession({
      accessToken: 'token-1',
      expiresAt: '',
      permissionScope: ['resource.read', 'resource.manage', 'operations.read', 'settings.read', 'settings.manage'],
      user: {
        userId: 'superadmin',
        username: 'superadmin',
        displayName: 'superadmin',
        nickname: 'superadmin',
        email: '',
        agentProfileId: 'human-superadmin',
        role: 'superadmin'
      }
    })

    expect(session.authenticated).toBe(true)
    expect(session.accessToken).toBe('')
    expect(session.actorId).toBe('superadmin')
    expect(session.displayName).toBe('superadmin')
    expect(session.permissionScope).toContain('resource.read')
    expect(session.permissionScope).toContain('resource.manage')
    expect(session.permissionScope).toContain('operations.read')
    expect(session.permissionScope).toContain('settings.read')
    expect(session.permissionScope).toContain('settings.manage')
    expect(session.permissionScope).not.toContain('resources.manage')
    expect(localStorage.getItem('orrery.auth.session.v1')).toBeNull()
  })

  it('recognizes the authenticated account and People profile as the same user', () => {
    applySession({
      expiresAt: '',
      permissionScope: [],
      user: {
        userId: 'user-ding',
        username: 'ding',
        displayName: 'Ding',
        nickname: 'Ding',
        email: '',
        agentProfileId: 'human-ding',
        role: 'member'
      }
    })

    expect(currentSessionUserIdentityIds()).toEqual(['ding', 'user-ding', 'human-ding'])
    expect(isCurrentSessionUserIdentity('ding')).toBe(true)
    expect(isCurrentSessionUserIdentity(' user-ding ')).toBe(true)
    expect(isCurrentSessionUserIdentity('human-ding')).toBe(true)
    expect(isCurrentSessionUserIdentity('human-other')).toBe(false)
    expect(isCurrentSessionUserIdentity('')).toBe(false)
  })

  it('does not persist a restricted password-change session in browser storage', () => {
    applySession({
      expiresAt: '',
      mustChangePassword: true,
      permissionScope: [],
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: 'human-operator',
        role: 'member'
      }
    })

    expect(session.authenticated).toBe(true)
    expect(session.mustChangePassword).toBe(true)
    expect(localStorage.getItem('orrery.auth.session.v1')).toBeNull()
  })

  it('clears high-privilege query data before applying another security context', () => {
    applySession({
      expiresAt: '',
      permissionScope: ['audit.read', 'people.manage'],
      user: {
        userId: 'superadmin',
        username: 'superadmin',
        displayName: 'Superadmin',
        nickname: 'Superadmin',
        email: '',
        agentProfileId: 'human-superadmin',
        role: 'superadmin'
      }
    })
    queryClient.setQueryData(['operations', 'audit'], { items: [{ secret: 'high-privilege-data' }] })

    applySession({
      expiresAt: '',
      permissionScope: ['mission.read'],
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: 'human-operator',
        role: 'member'
      }
    })

    expect(queryClient.getQueryData(['operations', 'audit'])).toBeUndefined()
    expect(session.username).toBe('operator')
    expect(session.permissionScope).toEqual(['mission.read'])
  })

  it('prevents an in-flight query from restoring data after a security context change', async () => {
    let resolveQuery: ((value: { secret: string }) => void) | undefined
    const pending = queryClient.fetchQuery({
      queryKey: ['operations', 'permissions'],
      queryFn: () => new Promise<{ secret: string }>((resolve) => {
        resolveQuery = resolve
      })
    }).catch(() => undefined)

    applySession({
      expiresAt: '',
      permissionScope: ['mission.read'],
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: 'human-operator',
        role: 'member'
      }
    })
    resolveQuery?.({ secret: 'late-high-privilege-data' })
    await pending

    expect(queryClient.getQueryData(['operations', 'permissions'])).toBeUndefined()
  })
})
