import { clearSessionQueryState } from '#lib/query-client'

export interface SessionUser {
  userId: string
  username: string
  displayName: string
  nickname: string
  email: string
  agentProfileId: string
  role: string
}

export interface AuthSession {
  accessToken?: string
  subjectProvider?: string
  user: SessionUser
  permissionScope: string[]
  expiresAt: string
  mustChangePassword?: boolean
}

const STORAGE_KEY = 'orrery.auth.session.v1'

export const session = $state({
  authenticated: false,
  accessToken: '',
  actorId: '',
  username: '',
  displayName: '',
  subjectProvider: '',
  viewerKind: 'user',
  securityContextVersion: 0,
  mustChangePassword: false,
  permissionScope: [] as string[],
  user: {
    userId: '',
    username: '',
    displayName: '',
    nickname: '',
    email: '',
    agentProfileId: '',
    role: ''
  } satisfies SessionUser
})

export function applySession(next: AuthSession) {
  clearSessionQueryState()
  const user = normalizeUser(next.user)
  session.authenticated = Boolean(user.username)
  session.accessToken = ''
  session.actorId = user.username
  session.username = user.username
  session.displayName = user.displayName || user.nickname || user.username
  session.subjectProvider = (next.subjectProvider ?? '').trim().toLowerCase()
  session.viewerKind = 'user'
  session.securityContextVersion += 1
  session.mustChangePassword = Boolean(next.mustChangePassword)
  session.permissionScope = cleanScopes(next.permissionScope)
  session.user = user
  removeStoredSession()
}

export function clearSession() {
  clearSessionQueryState()
  session.authenticated = false
  session.accessToken = ''
  session.actorId = ''
  session.username = ''
  session.displayName = ''
  session.subjectProvider = ''
  session.viewerKind = 'user'
  session.securityContextVersion += 1
  session.mustChangePassword = false
  session.permissionScope = []
  session.user = normalizeUser(undefined)
  removeStoredSession()
}

export function hasSession() {
  return session.authenticated && Boolean(session.actorId)
}

export function currentSessionUserIdentityIds() {
  return Array.from(new Set(
    [session.actorId, session.username, session.user.userId, session.user.username, session.user.agentProfileId]
      .map((value) => value.trim())
      .filter(Boolean)
  ))
}

export function isCurrentSessionUserIdentity(identityId: string | null | undefined) {
  const normalized = (identityId ?? '').trim()
  return normalized !== '' && currentSessionUserIdentityIds().includes(normalized)
}

function removeStoredSession() {
  if (typeof localStorage !== 'undefined' && typeof localStorage.removeItem === 'function') {
    localStorage.removeItem(STORAGE_KEY)
  }
}

function normalizeUser(user: SessionUser | undefined): SessionUser {
  const username = (user?.username ?? '').trim()
  const displayName = (user?.displayName ?? user?.nickname ?? username).trim()
  return {
    userId: (user?.userId ?? username).trim(),
    username,
    displayName,
    nickname: (user?.nickname ?? displayName).trim(),
    email: (user?.email ?? '').trim(),
    agentProfileId: (user?.agentProfileId ?? '').trim(),
    role: (user?.role ?? '').trim()
  }
}

function cleanScopes(scopes: string[] = []) {
  return Array.from(new Set(scopes.map((scope) => scope.trim()).filter(Boolean)))
}
