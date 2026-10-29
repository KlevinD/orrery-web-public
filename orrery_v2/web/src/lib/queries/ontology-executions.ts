import { createQuery } from '@tanstack/svelte-query'
import { ontologyExecutionsApi } from '#lib/api/ontology-executions'
import { session } from '#lib/stores/session.svelte'

const key = (...parts: unknown[]) => ['ontology', 'executions', session.securityContextVersion, ...parts] as const

export function useOntologyAgentTasksQuery(query: () => { tenantId: string; status?: string; search?: string; limit?: number; cursor?: string }) {
  return createQuery(() => {
    const current = query()
    return {
      queryKey: key('tasks', current),
      queryFn: () => ontologyExecutionsApi.tasks(current),
      enabled: true
    }
  })
}

export function useOntologyAgentTaskQuery(tenantId: () => string, taskId: () => string) {
  return createQuery(() => {
    const tenant = tenantId().trim()
    const id = taskId().trim()
    return { queryKey: key('task', tenant, id), queryFn: () => ontologyExecutionsApi.task(tenant, id), enabled: tenant.length > 0 && id.length > 0 }
  })
}

export function useOntologyAgentRunQuery(tenantId: () => string, runId: () => string) {
  return createQuery(() => {
    const tenant = tenantId().trim()
    const id = runId().trim()
    return { queryKey: key('run', tenant, id), queryFn: () => ontologyExecutionsApi.run(tenant, id), enabled: tenant.length > 0 && id.length > 0 }
  })
}

export function useOntologyAgentRunLineageQuery(tenantId: () => string, runId: () => string) {
  return createQuery(() => {
    const tenant = tenantId().trim()
    const id = runId().trim()
    return { queryKey: key('run-lineage', tenant, id), queryFn: () => ontologyExecutionsApi.runLineage(tenant, id), enabled: tenant.length > 0 && id.length > 0 }
  })
}
