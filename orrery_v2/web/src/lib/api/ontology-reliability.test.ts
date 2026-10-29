import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyReliabilityApi } from './ontology-reliability'

describe('ontology reliability API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('keeps every operation on the exact tenant, incident, rule, and authorization binding', async () => {
    await ontologyReliabilityApi.incident('tenant 1', 'incident/1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/reliability/incidents/incident%2F1?tenant_id=tenant%201', undefined)

    await ontologyReliabilityApi.createDiagnosis('tenant 1', 'incident/1', 'transient', ' provider timeout ', 0.8, ['evidence/1'], 'diagnosis-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/incidents/incident%2F1/diagnoses?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'diagnosis-key', body: JSON.stringify({ root_cause_category: 'transient', redacted_summary: 'provider timeout', confidence: 0.8, evidence_ids: ['evidence/1'] }) })
    )

    await ontologyReliabilityApi.decideDiagnosis('tenant 1', 'incident/1', 'diagnosis/1', 'confirm', ' evidence checked ', 'diagnosis-decision-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/incidents/incident%2F1/diagnoses/diagnosis%2F1/decision?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'diagnosis-decision-key', body: JSON.stringify({ decision: 'confirm', reason: 'evidence checked' }) })
    )

    await ontologyReliabilityApi.proposeRule('tenant 1', 'incident/1', 'diagnosis/1', 'retry', true, true, 'rule-proposal-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/incidents/incident%2F1/diagnoses/diagnosis%2F1/rules?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'rule-proposal-key', body: JSON.stringify({ strategy: 'retry', reversible: true, idempotent: true }) })
    )

    await ontologyReliabilityApi.requestActivationReview('tenant 1', 'rule/1', 'promote tested rule', 'activation-request-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/rules/rule%2F1/activation-reviews?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'activation-request-key', body: JSON.stringify({ purpose: 'promote tested rule' }) })
    )

    await ontologyReliabilityApi.decideActivationReview('tenant 1', 'rule/1', 'review/1', 'authorize', 'evidence checked', 'activation-decision-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/rules/rule%2F1/activation-reviews/review%2F1/decision?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'activation-decision-key', body: JSON.stringify({ decision: 'authorize', reason: 'evidence checked' }) })
    )

    await ontologyReliabilityApi.activateRule('tenant 1', 'rule/1', 'review/1', 'activate-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/rules/rule%2F1/activation-reviews/review%2F1/activate?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'activate-key', body: '{}' })
    )

    await ontologyReliabilityApi.requestAuthorization('tenant 1', 'incident/1', 'rule/1', 'dry_run', 'verify exact target', 'request-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/incidents/incident%2F1/rules/rule%2F1/authorizations?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'request-key', body: JSON.stringify({ execution_mode: 'dry_run', purpose: 'verify exact target' }) })
    )

    await ontologyReliabilityApi.decideAuthorization('tenant 1', 'incident/1', 'rule/1', 'approval/1', 'authorize', 'checked', 'decision-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/incidents/incident%2F1/rules/rule%2F1/authorizations/approval%2F1/decision?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'decision-key', body: JSON.stringify({ decision: 'authorize', reason: 'checked' }) })
    )

    await ontologyReliabilityApi.invokeAuthorization('tenant 1', 'incident/1', 'rule/1', 'approval/1', 'invoke-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/reliability/incidents/incident%2F1/rules/rule%2F1/authorizations/approval%2F1/invoke?tenant_id=tenant%201',
      expect.objectContaining({ method: 'POST', idempotencyKey: 'invoke-key', body: '{}' })
    )
  })
})
