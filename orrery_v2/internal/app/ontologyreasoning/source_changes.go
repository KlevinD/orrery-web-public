package ontologyreasoning

import (
	"context"
	"orrery_v2/internal/platform/db"
)

// QueueSourceChangeTx makes the invalidation signal durable with source
// withdrawal. Evaluation/model decisions still recheck current agent authority.
func QueueSourceChangeTx(ctx context.Context, tx db.Tx, tenant, eventID string) error {
	if tenant == "" || eventID == "" {
		return ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,version FROM ontology_inference_rule_sets WHERE tenant_id=? AND active=TRUE ORDER BY id`, tenant)
	if err != nil {
		return err
	}
	var jobs []InferenceJob
	for rows.Next() {
		job := InferenceJob{TenantID: tenant}
		if err := rows.Scan(&job.RuleSetID, &job.Version); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := enqueueInferenceJob(ctx, tx, InferenceEvaluationTopic, stableID("inference-source-change:", []any{tenant, eventID, job.RuleSetID, job.Version}), job); err != nil {
			return err
		}
	}
	return nil
}
