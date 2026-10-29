import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'

type Schemas = components['schemas']
export type InferenceRequest = Schemas['OntologyInferenceRequest']
export type InferenceRun = Schemas['OntologyInferenceRun']
export type InferenceWorkflowRequest = Schemas['OntologyInferenceWorkflowRequest']
export type InferenceCandidateState = Schemas['OntologyInferenceCandidateState']
export type GroundedContextRequest = Schemas['OntologyGroundedContextRequest']
export type GroundedContext = Schemas['OntologyGroundedContext']

export const ontologyInferenceApi = {
  context: (body: GroundedContextRequest, options?: ApiFetchOptions) => apiFetch<GroundedContext>('/api/ontology/inference/context', { ...options, method: 'POST', body: JSON.stringify(body) }),
  evaluate: (body: InferenceRequest, options?: ApiFetchOptions) => apiFetch<InferenceRun>('/api/ontology/inference/evaluate', { ...options, method: 'POST', body: JSON.stringify(body) }),
  review: (body: InferenceWorkflowRequest & { decision: 'approve' | 'reject' }, options?: ApiFetchOptions) => apiFetch<InferenceCandidateState>('/api/ontology/inference/review', { ...options, method: 'POST', body: JSON.stringify(body) }),
  plan: (body: InferenceWorkflowRequest, options?: ApiFetchOptions) => apiFetch<InferenceCandidateState>('/api/ontology/inference/plan', { ...options, method: 'POST', body: JSON.stringify(body) }),
  apply: (body: InferenceWorkflowRequest & { plan_id: string; idempotency_key: string; confirmed: true }, options?: ApiFetchOptions) => apiFetch<InferenceCandidateState>('/api/ontology/inference/apply', { ...options, method: 'POST', idempotencyKey: body.idempotency_key, body: JSON.stringify(body) })
}
