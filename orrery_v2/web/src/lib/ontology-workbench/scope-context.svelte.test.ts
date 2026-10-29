import { beforeEach, describe, expect, it, vi } from 'vitest'

const { gotoMock, resolveMock } = vi.hoisted(() => ({
  gotoMock: vi.fn(),
  resolveMock: vi.fn()
}))

vi.mock('$app/navigation', () => ({ goto: gotoMock }))
vi.mock('#lib/api/ontology-scopes', () => ({
  ontologyScopesApi: { resolve: resolveMock }
}))

import {
  activateOntologyScope,
  clearOntologyScope,
  ontologyScopeContext,
  revalidateOntologyScopeAfterForbidden,
  restoreOntologyScope,
  setOntologyScopeSwitchGuard
} from './scope-context.svelte'
import { ApiClientError } from '#lib/api/client'
import { queryClient } from '#lib/query-client'

function resolvedScope(tenantId: string) {
  return {
    tenant_id: tenantId,
    kind: 'organization' as const,
    label: tenantId,
    capabilities: ['ontology.read' as const],
    binding_version: 1
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('ontology scope context', () => {
  beforeEach(() => {
    clearOntologyScope()
    gotoMock.mockReset().mockResolvedValue(undefined)
    resolveMock.mockReset()
  })

  it('clears an authorized scope when exact restore fails', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1'))
    await restoreOntologyScope('tenant-1')
    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-1')

    const unavailable = deferred<ReturnType<typeof resolvedScope>>()
    resolveMock.mockReturnValueOnce(unavailable.promise)
    const restoring = restoreOntologyScope('tenant-2')
    expect(ontologyScopeContext.activeScope).toBeUndefined()
    unavailable.reject(new Error('Scope unavailable'))
    await restoring

    expect(ontologyScopeContext.activeScope).toBeUndefined()
    expect(ontologyScopeContext.draftTenantId).toBe('tenant-2')
    expect(ontologyScopeContext.error).toBe('Scope unavailable')
  })

  it('keeps only the newest in-flight scope resolution', async () => {
    const first = deferred<ReturnType<typeof resolvedScope>>()
    const second = deferred<ReturnType<typeof resolvedScope>>()
    resolveMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const restoringFirst = restoreOntologyScope('tenant-1')
    const restoringSecond = restoreOntologyScope('tenant-2')
    expect(ontologyScopeContext.activeScope).toBeUndefined()
    expect(ontologyScopeContext.draftTenantId).toBe('tenant-2')
    second.resolve(resolvedScope('tenant-2'))
    await restoringSecond
    first.resolve(resolvedScope('tenant-1'))
    await restoringFirst

    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-2')
    expect(ontologyScopeContext.draftTenantId).toBe('tenant-2')
    expect(ontologyScopeContext.resolving).toBe(false)
  })

  it('restores all-visible mode without resolving a tenant', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1'))
    await activateOntologyScope('tenant-1', '/resources/ontology/context')
    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-1')

    await restoreOntologyScope('')

    expect(ontologyScopeContext.activeScope).toBeUndefined()
    expect(ontologyScopeContext.draftTenantId).toBe('')
    expect(ontologyScopeContext.mode).toBe('all')
    expect(resolveMock).toHaveBeenCalledOnce()
  })

  it('does not cancel aggregate queries when all-visible mode is already active', async () => {
    const cancelQueries = vi.spyOn(queryClient, 'cancelQueries')
    const removeQueries = vi.spyOn(queryClient, 'removeQueries')

    await restoreOntologyScope('')

    expect(cancelQueries).not.toHaveBeenCalled()
    expect(removeQueries).not.toHaveBeenCalled()
    cancelQueries.mockRestore()
    removeQueries.mockRestore()
  })

  it('activates all-visible mode and removes the tenant query', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1'))
    await activateOntologyScope('tenant-1', '/resources/ontology/context')

    await expect(activateOntologyScope('', '/resources/ontology/context')).resolves.toBe(true)

    expect(ontologyScopeContext.mode).toBe('all')
    expect(ontologyScopeContext.activeScope).toBeUndefined()
    expect(gotoMock).toHaveBeenLastCalledWith('/resources/ontology/context', { invalidateAll: false })
    expect(resolveMock).toHaveBeenCalledOnce()
  })

  it('adds an explicit scope activation to browser history', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1'))

    await activateOntologyScope('tenant-1', '/resources/ontology/context')

    expect(ontologyScopeContext.mode).toBe('scope')
    expect(gotoMock).toHaveBeenLastCalledWith(
      '/resources/ontology/context?tenant_id=tenant-1',
      { invalidateAll: false }
    )
  })

  it('does not resolve a different scope when a dirty workflow rejects the switch', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1'))
    await activateOntologyScope('tenant-1')
    const guard = vi.fn().mockReturnValue(false)
    setOntologyScopeSwitchGuard(guard)

    await expect(activateOntologyScope('tenant-2')).resolves.toBe(false)

    expect(guard).toHaveBeenCalledOnce()
    expect(resolveMock).toHaveBeenCalledOnce()
    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-1')
  })

  it('restores the active route when a dirty workflow rejects URL-driven scope restore', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1'))
    await restoreOntologyScope('tenant-1')
    const guard = vi.fn().mockReturnValue(false)
    setOntologyScopeSwitchGuard(guard)

    await expect(restoreOntologyScope('tenant-2', '/resources/ontology/governance')).resolves.toBe(false)

    expect(guard).toHaveBeenCalledOnce()
    expect(resolveMock).toHaveBeenCalledOnce()
    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-1')
    expect(gotoMock).toHaveBeenLastCalledWith(
      '/resources/ontology/governance?tenant_id=tenant-1',
      { replaceState: true, invalidateAll: false }
    )
  })

  it('revalidates a forbidden ontology request and retains a still-authorized scope', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1')).mockResolvedValueOnce(resolvedScope('tenant-1'))
    await restoreOntologyScope('tenant-1')
    await revalidateOntologyScopeAfterForbidden('/api/ontology/context/objects?tenant_id=tenant-1', forbiddenError())

    expect(resolveMock).toHaveBeenCalledTimes(2)
    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-1')
  })

  it('clears scope state only after exact revalidation confirms revocation', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1')).mockRejectedValueOnce(forbiddenError())
    await restoreOntologyScope('tenant-1')
    await revalidateOntologyScopeAfterForbidden('/api/ontology/governance/proposals?tenant_id=tenant-1', forbiddenError())

    expect(ontologyScopeContext.activeScope).toBeUndefined()
    expect(ontologyScopeContext.draftTenantId).toBe('tenant-1')
    expect(ontologyScopeContext.error).toBe('scope access denied')

    resolveMock.mockClear()
    await revalidateOntologyScopeAfterForbidden('/api/ontology/context/objects/missing?tenant_id=tenant-2', forbiddenError())
    expect(resolveMock).not.toHaveBeenCalled()
  })

  it('revalidates a non-disclosing ontology 404 without treating an entity miss as scope revocation', async () => {
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1')).mockResolvedValueOnce(resolvedScope('tenant-1'))
    await restoreOntologyScope('tenant-1')

    await revalidateOntologyScopeAfterForbidden(
      '/api/ontology/context/objects/missing?tenant_id=tenant-1',
      new ApiClientError({ status: 404, code: 'unavailable', message: 'resource unavailable' })
    )

    expect(resolveMock).toHaveBeenCalledTimes(2)
    expect(ontologyScopeContext.activeScope?.tenant_id).toBe('tenant-1')
  })

  it('clears scope state when exact revalidation also returns non-disclosing 404', async () => {
    const unavailable = new ApiClientError({ status: 404, code: 'unavailable', message: 'scope unavailable' })
    resolveMock.mockResolvedValueOnce(resolvedScope('tenant-1')).mockRejectedValueOnce(unavailable)
    await restoreOntologyScope('tenant-1')

    await revalidateOntologyScopeAfterForbidden('/api/ontology/context/objects?tenant_id=tenant-1', unavailable)

    expect(ontologyScopeContext.activeScope).toBeUndefined()
    expect(ontologyScopeContext.error).toBe('scope unavailable')
  })
})

function forbiddenError() {
  return new ApiClientError({ status: 403, code: 'forbidden', message: 'scope access denied' })
}
