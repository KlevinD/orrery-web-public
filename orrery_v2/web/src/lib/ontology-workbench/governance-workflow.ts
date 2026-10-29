export interface GovernanceWorkflowIdentity {
  tenantId: string
  proposalId: string
  revision: number
}

export function governanceWorkflowKey(identity: GovernanceWorkflowIdentity) {
  return `${identity.tenantId.trim()}\u0000${identity.proposalId.trim()}\u0000${identity.revision}`
}

export function createGovernanceIdempotencyKey() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `ontology-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
