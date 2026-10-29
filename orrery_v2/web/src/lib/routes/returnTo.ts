export const IDENTITY_LOGIN_RETURN_TO_KEY = 'orrery.identity.return_to'
export const IDENTITY_LOGIN_RETURN_TO_TTL_MS = 5 * 60 * 1000

const IDENTITY_LOGIN_ACTIVE_FLOW_KEY = 'orrery.identity.active_flow'
const IDENTITY_LOGIN_FLOW_RETURN_TO_PREFIX = 'orrery.identity.flow_return_to.'

interface ReturnToStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

export function safeReturnTo(value: string | null | undefined, fallback = '/') {
  const candidate = (value ?? '').trim()
  if (!candidate || !candidate.startsWith('/') || candidate.startsWith('//') || candidate.includes('\\')) {
    return fallback
  }
  if (candidate === '/login' || candidate.startsWith('/login?')) {
    return fallback
  }
  return candidate
}

export function safeReturnToFromURL(url: URL, fallback = '/') {
  return safeReturnTo(url.searchParams.get('return_to'), fallback)
}

export function identityLoginReturnToFromURL(
  url: URL,
  storage: ReturnToStorage,
  fallback = '/',
  now = Date.now()
) {
  const flowID = url.searchParams.get('flow')?.trim() ?? ''
  if (url.searchParams.has('return_to')) {
    const returnTo = safeReturnToFromURL(url, fallback)
    if (flowID) {
      storeFlowReturnTo(storage, flowID, returnTo, now)
      removeStorageItem(storage, IDENTITY_LOGIN_RETURN_TO_KEY)
    } else {
      storeReturnTo(storage, IDENTITY_LOGIN_RETURN_TO_KEY, returnTo, now)
    }
    return returnTo
  }
  if (flowID) {
    const flowRecord = readReturnToRecord(storage, identityLoginFlowKey(flowID), now)
    if (flowRecord) return flowRecord.returnTo

    const pendingRecord = readReturnToRecord(storage, IDENTITY_LOGIN_RETURN_TO_KEY, now)
    removeStorageItem(storage, IDENTITY_LOGIN_RETURN_TO_KEY)
    if (pendingRecord) {
      storeFlowReturnTo(storage, flowID, pendingRecord.returnTo, pendingRecord.createdAt)
      return pendingRecord.returnTo
    }
    return fallback
  }
  clearIdentityLoginReturnTo(storage)
  return fallback
}

export function identityLoginFlowKey(flowID: string) {
  return `${IDENTITY_LOGIN_FLOW_RETURN_TO_PREFIX}${encodeURIComponent(flowID.trim())}`
}

export function prepareIdentityLoginRetryReturnTo(
  flowID: string,
  returnTo: string,
  storage: ReturnToStorage,
  now = Date.now()
) {
  const normalizedFlowID = flowID.trim()
  const flowRecord = normalizedFlowID
    ? readReturnToRecord(storage, identityLoginFlowKey(normalizedFlowID), now)
    : undefined
  if (normalizedFlowID) {
    removeStorageItem(storage, identityLoginFlowKey(normalizedFlowID))
    if (readStorageItem(storage, IDENTITY_LOGIN_ACTIVE_FLOW_KEY) === normalizedFlowID) {
      removeStorageItem(storage, IDENTITY_LOGIN_ACTIVE_FLOW_KEY)
    }
  }
  storeReturnTo(
    storage,
    IDENTITY_LOGIN_RETURN_TO_KEY,
    safeReturnTo(returnTo),
    flowRecord?.createdAt ?? now
  )
}

function storeFlowReturnTo(storage: ReturnToStorage, flowID: string, returnTo: string, now: number) {
  storeReturnTo(storage, identityLoginFlowKey(flowID), returnTo, now)
  setStorageItem(storage, IDENTITY_LOGIN_ACTIVE_FLOW_KEY, flowID)
}

function storeReturnTo(storage: ReturnToStorage, key: string, returnTo: string, now: number) {
  setStorageItem(storage, key, JSON.stringify({ returnTo: safeReturnTo(returnTo), createdAt: now }))
}

function readReturnToRecord(storage: ReturnToStorage, key: string, now: number) {
  const raw = readStorageItem(storage, key)
  if (!raw) return undefined
  try {
    const parsed = JSON.parse(raw) as { returnTo?: unknown; createdAt?: unknown }
    if (
      typeof parsed.returnTo !== 'string' ||
      typeof parsed.createdAt !== 'number' ||
      !Number.isFinite(parsed.createdAt) ||
      parsed.createdAt > now ||
      now - parsed.createdAt > IDENTITY_LOGIN_RETURN_TO_TTL_MS
    ) {
      removeStorageItem(storage, key)
      return undefined
    }
    const returnTo = safeReturnTo(parsed.returnTo, '')
    if (!returnTo) removeStorageItem(storage, key)
    return returnTo ? { returnTo, createdAt: parsed.createdAt } : undefined
  } catch {
    removeStorageItem(storage, key)
    return undefined
  }
}

function clearIdentityLoginReturnTo(storage: ReturnToStorage) {
  removeStorageItem(storage, IDENTITY_LOGIN_RETURN_TO_KEY)
  const activeFlowID = readStorageItem(storage, IDENTITY_LOGIN_ACTIVE_FLOW_KEY)?.trim()
  if (activeFlowID) removeStorageItem(storage, identityLoginFlowKey(activeFlowID))
  removeStorageItem(storage, IDENTITY_LOGIN_ACTIVE_FLOW_KEY)
}

function readStorageItem(storage: ReturnToStorage, key: string) {
  try {
    return storage.getItem(key)
  } catch {
    return null
  }
}

function setStorageItem(storage: ReturnToStorage, key: string, value: string) {
  try {
    storage.setItem(key, value)
  } catch {
    // Login remains usable with the safe fallback when storage is unavailable.
  }
}

function removeStorageItem(storage: ReturnToStorage, key: string) {
  try {
    storage.removeItem(key)
  } catch {
    // Login remains usable with the safe fallback when storage is unavailable.
  }
}

export function loginURLWithReturnTo(url: URL) {
  const returnTo = safeReturnTo(`${url.pathname}${url.search}`, '/')
  const query = new URLSearchParams()
  query.set('return_to', returnTo)
  return `/login?${query.toString()}`
}

export function passwordChangeURLWithReturnTo(url: URL) {
  const returnTo = url.pathname === '/login'
    ? safeReturnToFromURL(url)
    : safeReturnTo(`${url.pathname}${url.search}`, '/')
  const query = new URLSearchParams()
  query.set('change_password', '1')
  query.set('return_to', returnTo)
  return `/login?${query.toString()}`
}
