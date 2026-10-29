import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import {
  ontologyReliabilityApi,
  type ReliabilityDecision,
  type ReliabilityDiagnosisDecision,
  type ReliabilityExecutionMode,
  type ReliabilityRootCauseCategory,
  type ReliabilityRuleStrategy
} from '#lib/api/ontology-reliability'
import { session } from '#lib/stores/session.svelte'

const root = () => ['ontology', 'reliability'] as const

export function useOntologyReliabilityIncidentQuery(tenantId: () => string, incidentId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const tenant = tenantId().trim()
    const incident = incidentId().trim()
    return {
      queryKey: [...root(), session.securityContextVersion, tenant, incident],
      queryFn: () => ontologyReliabilityApi.incident(tenant, incident),
      enabled: enabled() && tenant.length > 0 && incident.length > 0
    }
  })
}

function invalidateReliability(client: ReturnType<typeof useQueryClient>) {
  void client.invalidateQueries({ queryKey: root() })
  void client.invalidateQueries({ queryKey: ['ontology', 'context'] })
}

export function useOntologyReliabilityDiagnosisMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; incidentId: string; rootCauseCategory: ReliabilityRootCauseCategory; redactedSummary: string; confidence: number; evidenceIds: string[]; idempotencyKey: string }) =>
      ontologyReliabilityApi.createDiagnosis(input.tenantId, input.incidentId, input.rootCauseCategory, input.redactedSummary, input.confidence, input.evidenceIds, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityDiagnosisDecisionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; incidentId: string; diagnosisId: string; decision: ReliabilityDiagnosisDecision; reason: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.decideDiagnosis(input.tenantId, input.incidentId, input.diagnosisId, input.decision, input.reason, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityRuleProposalMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; incidentId: string; diagnosisId: string; strategy: ReliabilityRuleStrategy; reversible: boolean; idempotent: boolean; idempotencyKey: string }) =>
      ontologyReliabilityApi.proposeRule(input.tenantId, input.incidentId, input.diagnosisId, input.strategy, input.reversible, input.idempotent, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityActivationReviewMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; ruleId: string; purpose: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.requestActivationReview(input.tenantId, input.ruleId, input.purpose, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityActivationDecisionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; ruleId: string; reviewId: string; decision: ReliabilityDecision; reason: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.decideActivationReview(input.tenantId, input.ruleId, input.reviewId, input.decision, input.reason, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityRuleActivationMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; ruleId: string; reviewId: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.activateRule(input.tenantId, input.ruleId, input.reviewId, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityAuthorizationMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; incidentId: string; ruleId: string; executionMode: ReliabilityExecutionMode; purpose: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.requestAuthorization(input.tenantId, input.incidentId, input.ruleId, input.executionMode, input.purpose, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityDecisionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; incidentId: string; ruleId: string; authorizationId: string; decision: ReliabilityDecision; reason: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.decideAuthorization(input.tenantId, input.incidentId, input.ruleId, input.authorizationId, input.decision, input.reason, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}

export function useOntologyReliabilityInvokeMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; incidentId: string; ruleId: string; authorizationId: string; idempotencyKey: string }) =>
      ontologyReliabilityApi.invokeAuthorization(input.tenantId, input.incidentId, input.ruleId, input.authorizationId, input.idempotencyKey),
    onSuccess: () => invalidateReliability(client)
  }))
}
