package agentkernel

import (
	"context"
	"time"

	"github.com/google/uuid"
	"orrery_v2/internal/platform/db"
)

func (j *SQLToolJournal) BeginBatch(ctx context.Context, intents []ToolExecution) ([]ToolExecution, error) {
	if len(intents) == 0 || len(intents) > 16 {
		return nil, ErrInput
	}
	claims := append([]ToolExecution(nil), intents...)
	keys := make(map[string]bool, len(claims))
	now := time.Now().UTC()
	for i := range claims {
		claim := &claims[i]
		if !claim.ToolExecutionKey.valid() || claim.AgentID.IsZero() || claim.ToolID == "" || !validDigest(claim.Fingerprint) || claim.TenantID != claims[0].TenantID || claim.RunID != claims[0].RunID || claim.AgentID != claims[0].AgentID {
			return nil, ErrInput
		}
		key := journalKey(claim.ToolExecutionKey)
		if keys[key] {
			return nil, ErrInput
		}
		keys[key] = true
		claim.ClaimID, claim.State, claim.Reason = uuid.NewString(), ToolExecutionStarted, ""
		claim.CreatedAt, claim.UpdatedAt = now, now
	}
	err := j.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, toolJournalTable, journalPrefix(claims[0].TenantID, claims[0].RunID)); err != nil {
			return err
		}
		for _, claim := range claims {
			encoded, err := encodeExecution(claim)
			if err != nil {
				return err
			}
			created, err := db.InsertRecordTx(ctx, tx, toolJournalTable, journalKey(claim.ToolExecutionKey), encoded)
			if err != nil {
				return err
			}
			if !created {
				prior, found, err := db.GetRecordTx(ctx, tx, toolJournalTable, journalKey(claim.ToolExecutionKey))
				if err != nil || !found {
					return ErrJournal
				}
				record, err := decodeExecution(prior)
				if err != nil {
					return err
				}
				if !sameExecution(record, claim) {
					return ErrToolConflict
				}
				return ErrToolRecorded
			}
		}
		// Inserts acquire SQLite's writer lock before the read. Any rejection
		// rolls the whole batch back; PostgreSQL uses the same run-key lock.
		return checkRunExecutionBatch(ctx, tx, claims[0], keys)
	})
	if err != nil {
		return nil, journalError(ctx, err)
	}
	return claims, nil
}

var _ BatchToolJournal = (*SQLToolJournal)(nil)
