import { goto } from '$app/navigation'
import { ontologyScopesApi, type ResolvedScopeDTO } from '#lib/api/ontology-scopes'
import type { ApiClientError } from '#lib/api/client'
import { queryClient } from '#lib/query-client'

export type OntologyScopeMode = 'all' | 'scope'

export const ontologyScopeContext = $state<{
  mode: OntologyScopeMode
  draftTenantId: string
  activeScope: ResolvedScopeDTO | undefined
  resolving: boolean
  error: string
}>({ mode: 'all', draftTenantId: '', activeScope: undefined, resolving: false, error: '' })

let scopeRequestVersion = 0
let scopeSwitchGuard: (() => boolean | Promise<boolean>) | undefined
let forbiddenRevalidation: { tenantId: string; promise: Promise<void> } | undefined

export function setOntologyScopeSwitchGuard(guard?: () => boolean | Promise<boolean>) {
  scopeSwitchGuard = guard
  return () => {
    if (scopeSwitchGuard === guard) scopeSwitchGuard = undefined
  }
}

function clearActiveScope() {
  ontologyScopeContext.activeScope = undefined
  const tenantQuery = (query: { queryKey: readonly unknown[] }) => query.queryKey[0] === 'ontology' && query.queryKey[1] !== 'scopes'
  void queryClient.cancelQueries({ predicate: tenantQuery })
  queryClient.removeQueries({ predicate: tenantQuery })
}

export function setDraftTenantId(value: string) {
  ontologyScopeContext.draftTenantId = value
  ontologyScopeContext.error = ''
}

export async function activateOntologyScope(tenantId: string, pathname = '/resources/ontology') {
  const canonical = tenantId.trim()
  const changesModeOrScope = canonical
    ? ontologyScopeContext.mode !== 'scope' || ontologyScopeContext.activeScope?.tenant_id !== canonical
    : ontologyScopeContext.mode !== 'all'
  if (changesModeOrScope && scopeSwitchGuard && !(await scopeSwitchGuard())) {
    return false
  }
  const requestVersion = ++scopeRequestVersion
  if (!canonical) {
    clearActiveScope()
    ontologyScopeContext.mode = 'all'
    ontologyScopeContext.draftTenantId = ''
    ontologyScopeContext.resolving = false
    ontologyScopeContext.error = ''
    await goto(pathname, { invalidateAll: false })
    return requestVersion === scopeRequestVersion
  }
  if (ontologyScopeContext.activeScope?.tenant_id !== canonical) clearActiveScope()
  ontologyScopeContext.draftTenantId = canonical
  ontologyScopeContext.resolving = true
  ontologyScopeContext.error = ''
  try {
    const resolved = await ontologyScopesApi.resolve(canonical)
    if (requestVersion !== scopeRequestVersion) return false
    ontologyScopeContext.activeScope = resolved
    ontologyScopeContext.mode = 'scope'
    ontologyScopeContext.draftTenantId = resolved.tenant_id
    await goto(`${pathname}?tenant_id=${encodeURIComponent(resolved.tenant_id)}`, { invalidateAll: false })
    return requestVersion === scopeRequestVersion
  } catch (error) {
    if (requestVersion === scopeRequestVersion) {
      clearActiveScope()
      ontologyScopeContext.mode = 'all'
      ontologyScopeContext.draftTenantId = canonical
      ontologyScopeContext.error = error instanceof Error ? error.message : 'Scope unavailable'
    }
    return false
  } finally {
    if (requestVersion === scopeRequestVersion) ontologyScopeContext.resolving = false
  }
}

export async function restoreOntologyScope(tenantId: string, pathname = '/resources/ontology') {
  const canonical = tenantId.trim()
  const activeTenantId = ontologyScopeContext.activeScope?.tenant_id ?? ''
  if (activeTenantId && activeTenantId !== canonical && scopeSwitchGuard && !(await scopeSwitchGuard())) {
    await goto(`${pathname}?tenant_id=${encodeURIComponent(activeTenantId)}`, { replaceState: true, invalidateAll: false })
    return false
  }
  if (!canonical) {
    scopeRequestVersion += 1
    forbiddenRevalidation = undefined
    if (ontologyScopeContext.mode !== 'all' || activeTenantId) clearActiveScope()
    ontologyScopeContext.mode = 'all'
    ontologyScopeContext.draftTenantId = ''
    ontologyScopeContext.resolving = false
    ontologyScopeContext.error = ''
    return true
  }
  if (ontologyScopeContext.mode === 'scope' && activeTenantId === canonical) return true
  const requestVersion = ++scopeRequestVersion
  clearActiveScope()
  ontologyScopeContext.draftTenantId = canonical
  ontologyScopeContext.resolving = true
  ontologyScopeContext.error = ''
  try {
    const resolved = await ontologyScopesApi.resolve(canonical)
    if (requestVersion !== scopeRequestVersion) return false
    ontologyScopeContext.activeScope = resolved
    ontologyScopeContext.mode = 'scope'
    ontologyScopeContext.draftTenantId = resolved.tenant_id
    return true
  } catch (error) {
    if (requestVersion === scopeRequestVersion) {
      ontologyScopeContext.activeScope = undefined
      ontologyScopeContext.mode = 'all'
      ontologyScopeContext.draftTenantId = canonical
      ontologyScopeContext.error = error instanceof Error ? error.message : 'Scope unavailable'
    }
    return false
  } finally {
    if (requestVersion === scopeRequestVersion) ontologyScopeContext.resolving = false
  }
}

export function clearOntologyScope() {
  scopeRequestVersion += 1
  forbiddenRevalidation = undefined
  clearActiveScope()
  ontologyScopeContext.mode = 'all'
  ontologyScopeContext.draftTenantId = ''
  ontologyScopeContext.resolving = false
  ontologyScopeContext.error = ''
  scopeSwitchGuard = undefined
}

export function revalidateOntologyScopeAfterForbidden(path: string, error: ApiClientError) {
  const active = ontologyScopeContext.activeScope
  if (!active || error.status !== 403 && error.status !== 404) return Promise.resolve()
  const request = ontologyRequest(path)
  if (!request || request.tenantId && request.tenantId !== active.tenant_id) return Promise.resolve()
  if (request.exactResolve) {
    revokeOntologyScope(active.tenant_id, error.message)
    return Promise.resolve()
  }
  if (forbiddenRevalidation?.tenantId === active.tenant_id) return forbiddenRevalidation.promise
  const tenantId = active.tenant_id
  const requestVersion = ++scopeRequestVersion
  const promise = ontologyScopesApi.resolve(tenantId)
    .then((resolved) => {
      if (requestVersion !== scopeRequestVersion || ontologyScopeContext.activeScope?.tenant_id !== tenantId) return
      if (resolved.tenant_id !== tenantId) {
        revokeOntologyScope(tenantId, 'Scope authorization changed')
        return
      }
      if (!sameResolvedScope(active, resolved)) {
        clearActiveScope()
        ontologyScopeContext.activeScope = resolved
        ontologyScopeContext.draftTenantId = resolved.tenant_id
      }
    })
    .catch((resolveError) => {
      if (requestVersion !== scopeRequestVersion || ontologyScopeContext.activeScope?.tenant_id !== tenantId) return
      const status = apiErrorStatus(resolveError)
      if (status === 403 || status === 404) revokeOntologyScope(tenantId, resolveError instanceof Error ? resolveError.message : error.message)
    })
    .finally(() => {
      if (forbiddenRevalidation?.tenantId === tenantId) forbiddenRevalidation = undefined
    })
  forbiddenRevalidation = { tenantId, promise }
  return promise
}

function ontologyRequest(path: string) {
  try {
    const url = new URL(path, 'http://ontology.local')
    if (!url.pathname.startsWith('/api/ontology/') || url.pathname === '/api/ontology/scopes') return undefined
    return {
      tenantId: (url.searchParams.get('tenant_id') ?? '').trim(),
      exactResolve: url.pathname === '/api/ontology/scopes/resolve'
    }
  } catch {
    return undefined
  }
}

function revokeOntologyScope(tenantId: string, message: string) {
  if (ontologyScopeContext.activeScope?.tenant_id !== tenantId) return
  scopeRequestVersion += 1
  clearActiveScope()
  ontologyScopeContext.mode = 'all'
  ontologyScopeContext.draftTenantId = tenantId
  ontologyScopeContext.resolving = false
  ontologyScopeContext.error = message.trim() || 'Scope unavailable'
  scopeSwitchGuard = undefined
}

function sameResolvedScope(left: ResolvedScopeDTO, right: ResolvedScopeDTO) {
  return left.tenant_id === right.tenant_id && left.kind === right.kind && left.binding_version === right.binding_version &&
    left.capabilities.length === right.capabilities.length && left.capabilities.every((capability) => right.capabilities.includes(capability))
}

function apiErrorStatus(error: unknown) {
  return typeof error === 'object' && error !== null && 'status' in error && typeof error.status === 'number' ? error.status : 0
}
