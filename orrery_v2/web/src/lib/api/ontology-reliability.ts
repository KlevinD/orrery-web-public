import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'

type Schemas = components['schemas']
export type ReliabilityIncidentDetailDTO = Schemas['OntologyReliabilityIncidentDetailDTO']
export type ReliabilityEvidenceDTO = Schemas['OntologyReliabilityEvidenceDTO']
export type ReliabilityDiagnosisDTO = Schemas['OntologyReliabilityDiagnosisDTO']
export type ReliabilityRepairRuleDTO = Schemas['OntologyReliabilityRepairRuleDTO']
export type ReliabilityAuthorizationDTO = Schemas['OntologyReliabilityAuthorizationDTO']
export type ReliabilityAuthorizationResolutionDTO = Schemas['OntologyReliabilityAuthorizationResolutionDTO']
export type ReliabilityActivationReviewDTO = Schemas['OntologyReliabilityActivationReviewDTO']
export type ReliabilityActivationReviewResolutionDTO = Schemas['OntologyReliabilityActivationReviewResolutionDTO']
export type ReliabilityActionRunDTO = Schemas['OntologyActionRunDTO']
export type ReliabilityExecutionMode = Schemas['OntologyReliabilityAuthorizationRequest']['execution_mode']
export type ReliabilityDecision = Schemas['OntologyReliabilityAuthorizationDecisionRequest']['decision']
export type ReliabilityDiagnosisDecision = Schemas['OntologyReliabilityDiagnosisDecisionRequest']['decision']
export type ReliabilityRootCauseCategory = Schemas['OntologyReliabilityDiagnosisCreateRequest']['root_cause_category']
export type ReliabilityRuleStrategy = Schemas['OntologyReliabilityRuleProposalRequest']['strategy']

function basePath(tenantId: string, incidentId: string, ruleId?: string) {
  const incident = `/api/ontology/reliability/incidents/${encodeURIComponent(incidentId.trim())}`
  const rule = ruleId ? `${incident}/rules/${encodeURIComponent(ruleId.trim())}` : incident
  return `${rule}?tenant_id=${encodeURIComponent(tenantId.trim())}`
}

function authorizationPath(tenantId: string, incidentId: string, ruleId: string, authorizationId?: string) {
  const path = basePath(tenantId, incidentId, ruleId).replace(/\?tenant_id=.*$/, '')
  const authorization = `${path}/authorizations${authorizationId ? `/${encodeURIComponent(authorizationId.trim())}` : ''}`
  return `${authorization}?tenant_id=${encodeURIComponent(tenantId.trim())}`
}

function diagnosisPath(tenantId: string, incidentId: string, diagnosisId?: string, suffix?: string) {
  const incident = basePath(tenantId, incidentId).replace(/\?tenant_id=.*$/, '')
  const diagnosis = `${incident}/diagnoses${diagnosisId ? `/${encodeURIComponent(diagnosisId.trim())}` : ''}${suffix ? `/${suffix}` : ''}`
  return `${diagnosis}?tenant_id=${encodeURIComponent(tenantId.trim())}`
}

function activationReviewPath(tenantId: string, ruleId: string, reviewId?: string, suffix?: 'decision' | 'activate') {
  const rule = `/api/ontology/reliability/rules/${encodeURIComponent(ruleId.trim())}/activation-reviews`
  const review = reviewId ? `${rule}/${encodeURIComponent(reviewId.trim())}` : rule
  return `${review}${suffix ? `/${suffix}` : ''}?tenant_id=${encodeURIComponent(tenantId.trim())}`
}

export const ontologyReliabilityApi = {
  incident: (tenantId: string, incidentId: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityIncidentDetailDTO>(basePath(tenantId, incidentId), options),
  createDiagnosis: (tenantId: string, incidentId: string, rootCauseCategory: ReliabilityRootCauseCategory, redactedSummary: string, confidence: number, evidenceIds: string[], idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityDiagnosisDTO>(diagnosisPath(tenantId, incidentId), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ root_cause_category: rootCauseCategory, redacted_summary: redactedSummary.trim(), confidence, evidence_ids: evidenceIds })
    }),
  decideDiagnosis: (tenantId: string, incidentId: string, diagnosisId: string, decision: ReliabilityDiagnosisDecision, reason: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityDiagnosisDTO>(diagnosisPath(tenantId, incidentId, diagnosisId, 'decision'), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ decision, reason: reason.trim() })
    }),
  proposeRule: (tenantId: string, incidentId: string, diagnosisId: string, strategy: ReliabilityRuleStrategy, reversible: boolean, idempotent: boolean, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityRepairRuleDTO>(diagnosisPath(tenantId, incidentId, diagnosisId, 'rules'), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ strategy, reversible, idempotent })
    }),
  requestActivationReview: (tenantId: string, ruleId: string, purpose: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityActivationReviewDTO>(activationReviewPath(tenantId, ruleId), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ purpose: purpose.trim() || undefined })
    }),
  decideActivationReview: (tenantId: string, ruleId: string, reviewId: string, decision: ReliabilityDecision, reason: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityActivationReviewResolutionDTO>(activationReviewPath(tenantId, ruleId, reviewId, 'decision'), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ decision, reason: reason.trim() || undefined })
    }),
  activateRule: (tenantId: string, ruleId: string, reviewId: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityRepairRuleDTO>(activationReviewPath(tenantId, ruleId, reviewId, 'activate'), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: '{}'
    }),
  requestAuthorization: (tenantId: string, incidentId: string, ruleId: string, executionMode: ReliabilityExecutionMode, purpose: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityAuthorizationDTO>(authorizationPath(tenantId, incidentId, ruleId), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ execution_mode: executionMode, purpose: purpose.trim() || undefined })
    }),
  decideAuthorization: (tenantId: string, incidentId: string, ruleId: string, authorizationId: string, decision: ReliabilityDecision, reason: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityAuthorizationResolutionDTO>(authorizationPath(tenantId, incidentId, ruleId, authorizationId).replace('?', '/decision?'), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: JSON.stringify({ decision, reason: reason.trim() || undefined })
    }),
  invokeAuthorization: (tenantId: string, incidentId: string, ruleId: string, authorizationId: string, idempotencyKey: string, options?: ApiFetchOptions) =>
    apiFetch<ReliabilityActionRunDTO>(authorizationPath(tenantId, incidentId, ruleId, authorizationId).replace('?', '/invoke?'), {
      ...options,
      method: 'POST',
      idempotencyKey,
      body: '{}'
    })
}
