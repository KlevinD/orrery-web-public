package improvement

import (
	"context"
	"reflect"

	"orrery_v2/internal/platform/db"
)

// ReconcileManagedRepairCases retires duplicate observations only after the
// normal acceptance path has created a canonical case for the same receipt
// identity. It never dispatches repairs, infers a diagnosis or replays business.
// Immutable observations/history remain available under the original ACL.
func (s *Store) ReconcileManagedRepairCases(ctx context.Context, limit int) (int, error) {
	if s.ready() != nil {
		return 0, ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	const cursorKey = "managed-repair-case-reconciliation-v1"
	var progress struct {
		Cursor string `json:"cursor"`
	}
	raw, exists, err := s.core.GetRecord(ctx, runtimeDiagnosisControlTable, cursorKey)
	if err != nil {
		return 0, err
	}
	if exists {
		progress, err = decode[struct {
			Cursor string `json:"cursor"`
		}](raw)
		if err != nil {
			return 0, err
		}
	}
	page, err := s.core.ListRecords(ctx, caseTable, progress.Cursor, limit)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, entry := range page.Items {
		c, err := decode[Case](entry.Record)
		if err != nil {
			return changed, err
		}
		if c.State != Observed || c.Observation.Trigger != "managed_repair_needs_attention" || len(c.Observation.EvidenceIDs) == 0 {
			continue
		}
		f, err := s.GetManagedRepairFeedback(ctx, c.TenantID, c.Observation.EvidenceIDs[0])
		if err != nil {
			return changed, err
		}
		index, found, err := s.core.GetRecord(ctx, repairCaseTable, repairCaseKey(f))
		if err != nil {
			return changed, err
		}
		if !found {
			continue
		} // Historical, no current accepted receipt.
		binding, err := decode[repairCaseBinding](index)
		if err != nil {
			return changed, err
		}
		if binding.CaseID == c.ID {
			continue
		}
		canonical, err := s.Get(ctx, c.TenantID, binding.CaseID)
		if err != nil {
			return changed, err
		}
		if canonical.Observation.Task != c.Observation.Task || canonical.Observation.Run != c.Observation.Run || canonical.Observation.SourceAccessRef != c.Observation.SourceAccessRef || canonical.Observation.Trigger != c.Observation.Trigger {
			return changed, ErrConflict
		}
		err = s.core.WithTx(ctx, func(tx db.Tx) error {
			if err := db.LockRecordKeyTx(ctx, tx, caseTable, entry.Key); err != nil {
				return err
			}
			current, found, err := db.GetRecordTx(ctx, tx, caseTable, entry.Key)
			if err != nil {
				return err
			}
			if !found || !reflect.DeepEqual(current, entry.Record) || c.Lease != nil && c.Lease.ExpiresAt.After(s.now()) {
				return nil
			}
			c.State, c.SupersededBy, c.Reason = Superseded, canonical.ID, "duplicate_repair_feedback"
			c.Revision++
			c.Lease, c.UpdatedAt = nil, s.now().UTC()
			if err := persistCase(ctx, tx, c, true); err != nil {
				return err
			}
			changed++
			return nil
		})
		if err != nil {
			return changed, err
		}
	}
	progress.Cursor = ""
	if page.HasMore && len(page.Items) > 0 {
		progress.Cursor = page.Items[len(page.Items)-1].Key
	}
	if err := s.core.PutRecord(ctx, runtimeDiagnosisControlTable, cursorKey, encode(progress)); err != nil {
		return changed, err
	}
	return changed, nil
}
