package responsibility

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const autoplanTable = "responsibility_autoplans"

// ClaimAutoplan creates the planning lease for a deduplicated responsibility
// snapshot. A second caller receives the existing record and claimed=false;
// this makes scheduler retries converge without creating another plan.
func (r *Repository) ClaimAutoplan(ctx context.Context, candidate AutoplanRecord, maxOpenPlans int, now time.Time) (AutoplanRecord, bool, error) {
	if r == nil || r.unit == nil || maxOpenPlans < 1 || now.IsZero() {
		return AutoplanRecord{}, false, ErrInvalidContract
	}
	candidate = normalizeAutoplanRecord(candidate)
	now = now.UTC()
	if candidate.Status != AutoplanStatusPlanning {
		return AutoplanRecord{}, false, fmt.Errorf("%w: an autoplan can only be claimed in planning state", ErrInvalidContract)
	}
	if candidate.Version < 1 {
		candidate.Version = 1
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	if candidate.UpdatedAt.IsZero() {
		candidate.UpdatedAt = now
	}
	if err := candidate.Validate(); err != nil {
		return AutoplanRecord{}, false, err
	}
	claimed := false
	var out AutoplanRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, autoplanCapacityLockKey(candidate.TenantID, candidate.AgentProfileID)); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, candidate.ID); err != nil {
			return err
		}
		if record, found := tx.Get(autoplanTable, candidate.ID); found {
			var existing AutoplanRecord
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			existing = normalizeAutoplanRecord(existing)
			if err := existing.Validate(); err != nil {
				return err
			}
			if !autoplanIdentityEqual(existing, candidate) {
				return ErrConflict
			}
			out = existing
			return nil
		}
		records, err := uow.ListRecords(ctx, tx, autoplanTable)
		if err != nil {
			return err
		}
		openCount := 0
		for _, record := range records {
			var existing AutoplanRecord
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			existing = normalizeAutoplanRecord(existing)
			if existing.TenantID == candidate.TenantID && existing.AgentProfileID == candidate.AgentProfileID && autoplanStatusOpen(existing.Status) {
				openCount++
			}
		}
		if openCount >= maxOpenPlans {
			return ErrAutoplanCapacity
		}
		if err := putRecordTx(ctx, tx, autoplanTable, candidate.ID, candidate); err != nil {
			return err
		}
		out, claimed = candidate, true
		return nil
	})
	return out, claimed, err
}

// RetryAutoplanPlanning reopens a failed plan under the same capacity lock as
// a new claim. A failed record is normally closed, so retrying it must not
// bypass the per-agent MaxOpenPlans limit while another pulse creates work.
func (r *Repository) RetryAutoplanPlanning(ctx context.Context, failed AutoplanRecord, maxOpenPlans int, now time.Time) (AutoplanRecord, bool, error) {
	if r == nil || r.unit == nil || maxOpenPlans < 1 || now.IsZero() {
		return AutoplanRecord{}, false, ErrInvalidContract
	}
	failed = normalizeAutoplanRecord(failed)
	if failed.Status != AutoplanStatusFailed || strings.TrimSpace(failed.ID) == "" || failed.Version < 1 {
		return AutoplanRecord{}, false, ErrInvalidContract
	}
	now = now.UTC()
	updated := false
	var out AutoplanRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, autoplanCapacityLockKey(failed.TenantID, failed.AgentProfileID)); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, failed.ID); err != nil {
			return err
		}
		record, found := tx.Get(autoplanTable, failed.ID)
		if !found {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		out = normalizeAutoplanRecord(out)
		if err := out.Validate(); err != nil {
			return err
		}
		if !autoplanIdentityEqual(out, failed) {
			return ErrConflict
		}
		if out.Version != failed.Version || out.Status != AutoplanStatusFailed {
			return nil
		}
		if !out.NextPlanningAttemptAt.IsZero() && out.NextPlanningAttemptAt.After(now) {
			return nil
		}
		records, err := uow.ListRecords(ctx, tx, autoplanTable)
		if err != nil {
			return err
		}
		openCount := 0
		for _, record := range records {
			var existing AutoplanRecord
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			existing = normalizeAutoplanRecord(existing)
			if existing.TenantID == out.TenantID && existing.AgentProfileID == out.AgentProfileID && autoplanStatusOpen(existing.Status) {
				openCount++
			}
		}
		if openCount >= maxOpenPlans {
			return ErrAutoplanCapacity
		}
		next := resetAutoplanPlanning(out)
		next.Version = out.Version + 1
		next.UpdatedAt = now
		if err := next.Validate(); err != nil {
			return err
		}
		if err := putRecordTx(ctx, tx, autoplanTable, next.ID, next); err != nil {
			return err
		}
		out, updated = next, true
		return nil
	})
	return out, updated, err
}

// AcquireAutoplanPlanningLease grants one caller the right to invoke the
// planner for a durable planning record. A held lease is deliberately a
// deferred result rather than an error: another scheduler instance may be
// working on exactly the same source snapshot.
func (r *Repository) AcquireAutoplanPlanningLease(ctx context.Context, plan AutoplanRecord, owner string, leaseFor time.Duration, now time.Time) (AutoplanRecord, bool, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(plan.ID) == "" || plan.Version < 1 || strings.TrimSpace(owner) == "" || leaseFor <= 0 || now.IsZero() {
		return AutoplanRecord{}, false, ErrInvalidContract
	}
	plan = normalizeAutoplanRecord(plan)
	owner, now = strings.TrimSpace(owner), now.UTC()
	var out AutoplanRecord
	acquired := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, plan.ID); err != nil {
			return err
		}
		record, found := tx.Get(autoplanTable, plan.ID)
		if !found {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		out = normalizeAutoplanRecord(out)
		if err := out.Validate(); err != nil {
			return err
		}
		if !autoplanIdentityEqual(out, plan) {
			return ErrConflict
		}
		if out.Version != plan.Version || out.Status != AutoplanStatusPlanning {
			return nil
		}
		if !out.PlanningLeaseExpiresAt.IsZero() && out.PlanningLeaseExpiresAt.After(now) {
			return nil
		}
		next := out
		next.PlanningAttempt++
		next.PlanningLeaseOwner = owner
		next.PlanningLeaseExpiresAt = now.Add(leaseFor).UTC()
		next.NextPlanningAttemptAt = time.Time{}
		next.Version++
		next.UpdatedAt = now
		if err := next.Validate(); err != nil {
			return err
		}
		if err := putRecordTx(ctx, tx, autoplanTable, next.ID, next); err != nil {
			return err
		}
		out, acquired = next, true
		return nil
	})
	return out, acquired, err
}

func autoplanCapacityLockKey(tenantID, agentProfileID string) string {
	return "capacity:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(agentProfileID)
}

func resetAutoplanPlanning(plan AutoplanRecord) AutoplanRecord {
	next := plan
	next.Status = AutoplanStatusPlanning
	next.Proposal = AutoplanProposal{}
	next.ProposalDigest = ""
	next.ApprovedProposalDigest = ""
	next.ApprovalID = ""
	next.ApprovalAttempt = 0
	next.ApprovalRequestedAt = time.Time{}
	next.HILDecisionID = ""
	next.HILReceiptID = ""
	next.HILReceiptConsumedAt = time.Time{}
	next.Materializations = nil
	next.RejectionReason = ""
	next.FailureReason = ""
	next.PlanningLeaseOwner = ""
	next.PlanningLeaseExpiresAt = time.Time{}
	next.NextPlanningAttemptAt = time.Time{}
	return next
}

func (r *Repository) GetAutoplan(ctx context.Context, planID string) (AutoplanRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(planID) == "" {
		return AutoplanRecord{}, ErrInvalidContract
	}
	planID = strings.TrimSpace(planID)
	var out AutoplanRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, found := tx.Get(autoplanTable, planID)
		if !found {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		out = normalizeAutoplanRecord(out)
		return out.Validate()
	})
	return out, err
}

// GetLatestAutoplanForSnapshot finds the current cycle for one immutable
// responsibility snapshot. It intentionally does not impose a tenant-wide
// list limit: an active continuous responsibility must still advance after
// hundreds of historical plans for unrelated responsibilities.
func (r *Repository) GetLatestAutoplanForSnapshot(ctx context.Context, request AutoplanRequest) (AutoplanRecord, bool, error) {
	if r == nil || r.unit == nil {
		return AutoplanRecord{}, false, ErrInvalidContract
	}
	request = request.Normalized()
	if err := request.Validate(); err != nil {
		return AutoplanRecord{}, false, err
	}
	var out AutoplanRecord
	found := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, autoplanTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var candidate AutoplanRecord
			if err := decodeRecord(record, &candidate); err != nil {
				return err
			}
			candidate = normalizeAutoplanRecord(candidate)
			if err := candidate.Validate(); err != nil {
				return err
			}
			if !autoplanSnapshotMatches(candidate, request) {
				continue
			}
			if !found || candidate.PlanningCycle > out.PlanningCycle ||
				(candidate.PlanningCycle == out.PlanningCycle && candidate.UpdatedAt.After(out.UpdatedAt)) {
				out, found = candidate, true
			}
		}
		return nil
	})
	return out, found, err
}

// ListAutoplans returns records for one tenant, optionally restricted to the
// supplied lifecycle states. Results are newest-first and bounded so a caller
// cannot accidentally scan an unbounded platform-record table.
func (r *Repository) ListAutoplans(ctx context.Context, tenantID string, statuses []AutoplanStatus, limit int) ([]AutoplanRecord, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, ErrInvalidContract
	}
	return r.listAutoplans(ctx, tenantID, statuses, limit, false)
}

// ListAutoplansForReconcile is the global work queue used by the periodic
// reconciler. It intentionally crosses tenants because the scheduler request
// has no tenant field; callers must provide the lifecycle states they own.
func (r *Repository) ListAutoplansForReconcile(ctx context.Context, statuses []AutoplanStatus, limit int) ([]AutoplanRecord, error) {
	return r.listAutoplans(ctx, "", statuses, limit, true)
}

func (r *Repository) listAutoplans(ctx context.Context, tenantID string, statuses []AutoplanStatus, limit int, orderByUpdatedAt bool) ([]AutoplanRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	wanted := make(map[AutoplanStatus]struct{}, len(statuses))
	for _, status := range statuses {
		if !status.valid() {
			return nil, ErrInvalidContract
		}
		wanted[status] = struct{}{}
	}
	out := make([]AutoplanRecord, 0)
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, autoplanTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var value AutoplanRecord
			if err := decodeRecord(record, &value); err != nil {
				return err
			}
			value = normalizeAutoplanRecord(value)
			if tenantID != "" && value.TenantID != tenantID {
				continue
			}
			if len(wanted) > 0 {
				if _, ok := wanted[value.Status]; !ok {
					continue
				}
			}
			if err := value.Validate(); err != nil {
				return err
			}
			out = append(out, value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if orderByUpdatedAt {
			if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
				return out[i].ID < out[j].ID
			}
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CompareAndSaveAutoplan applies a versioned state transition. A stale writer
// gets the current record with updated=false; identity or proposal-digest
// changes are hard conflicts and return ErrConflict.
func (r *Repository) CompareAndSaveAutoplan(ctx context.Context, next AutoplanRecord, expectedVersion int64, now time.Time) (bool, AutoplanRecord, error) {
	if r == nil || r.unit == nil || expectedVersion < 1 || now.IsZero() {
		return false, AutoplanRecord{}, ErrInvalidContract
	}
	next = normalizeAutoplanRecord(next)
	if strings.TrimSpace(next.ID) == "" {
		return false, AutoplanRecord{}, ErrInvalidContract
	}
	now = now.UTC()
	updated := false
	var current AutoplanRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, next.ID); err != nil {
			return err
		}
		record, found := tx.Get(autoplanTable, next.ID)
		if !found {
			return ErrNotFound
		}
		if err := decodeRecord(record, &current); err != nil {
			return err
		}
		current = normalizeAutoplanRecord(current)
		if err := current.Validate(); err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return nil
		}
		if !autoplanIdentityEqual(current, next) || current.ID != next.ID || current.DedupeKey != next.DedupeKey {
			return ErrConflict
		}
		if current.ProposalDigest != "" && next.ProposalDigest != current.ProposalDigest {
			return ErrConflict
		}
		approvalReissued := autoplanApprovalReissued(current, next)
		if current.ApprovalID != "" && next.ApprovalID != current.ApprovalID && !approvalReissued {
			return ErrConflict
		}
		if current.HILDecisionID != "" && next.HILDecisionID != current.HILDecisionID && !approvalReissued {
			return ErrConflict
		}
		if current.HILReceiptID != "" && next.HILReceiptID != current.HILReceiptID && !approvalReissued {
			return ErrConflict
		}
		if current.Status != next.Status {
			if err := ValidateAutoplanTransition(current.Status, next.Status); err != nil {
				return err
			}
		}
		next.Version = current.Version + 1
		next.CreatedAt = current.CreatedAt
		next.UpdatedAt = now
		if err := next.Validate(); err != nil {
			return err
		}
		if err := putRecordTx(ctx, tx, autoplanTable, next.ID, next); err != nil {
			return err
		}
		current, updated = next, true
		return nil
	})
	return updated, current, err
}

func autoplanApprovalReissued(current, next AutoplanRecord) bool {
	if (current.Status != AutoplanStatusPendingApproval && current.Status != AutoplanStatusApproved) ||
		next.Status != AutoplanStatusPendingApproval || current.HILDecisionID == "" || !current.HILReceiptConsumedAt.IsZero() ||
		next.ApprovalAttempt != current.ApprovalAttempt+1 || next.ApprovalRequestedAt.IsZero() ||
		next.HILDecisionID != "" || next.HILReceiptID != "" || !next.HILReceiptConsumedAt.IsZero() || next.ApprovedProposalDigest != "" {
		return false
	}
	return next.ApprovalID == autoplanApprovalID(next)
}

// LinkAutoplanHILDecision writes the common HIL identity independently from
// proposal state transitions. Repeated delivery of the same HIL ID is safe;
// a different ID or stale expected version is rejected.
func (r *Repository) LinkAutoplanHILDecision(ctx context.Context, planID, hilDecisionID string, expectedVersion int64, now time.Time) (AutoplanRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(planID) == "" || strings.TrimSpace(hilDecisionID) == "" || expectedVersion < 1 || now.IsZero() {
		return AutoplanRecord{}, ErrInvalidContract
	}
	planID, hilDecisionID, now = strings.TrimSpace(planID), strings.TrimSpace(hilDecisionID), now.UTC()
	var out AutoplanRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, planID); err != nil {
			return err
		}
		record, found := tx.Get(autoplanTable, planID)
		if !found {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		out = normalizeAutoplanRecord(out)
		if err := out.Validate(); err != nil {
			return err
		}
		if out.HILDecisionID != "" {
			if out.HILDecisionID != hilDecisionID {
				return ErrConflict
			}
			return nil
		}
		if out.Status != AutoplanStatusPendingApproval {
			return nil
		}
		if out.Version != expectedVersion {
			return ErrConflict
		}
		out.HILDecisionID = hilDecisionID
		out.Version++
		out.UpdatedAt = now
		if err := out.Validate(); err != nil {
			return err
		}
		return putRecordTx(ctx, tx, autoplanTable, planID, out)
	})
	return out, err
}

func normalizeAutoplanRecord(value AutoplanRecord) AutoplanRecord {
	value.ID = strings.TrimSpace(value.ID)
	value.TenantID = strings.TrimSpace(value.TenantID)
	value.PortfolioID = strings.TrimSpace(value.PortfolioID)
	value.PortfolioBindingID = strings.TrimSpace(value.PortfolioBindingID)
	value.PortfolioVersionID = strings.TrimSpace(value.PortfolioVersionID)
	value.SourceRevision = strings.TrimSpace(value.SourceRevision)
	value.SourceDigest = strings.TrimSpace(value.SourceDigest)
	value.GoalContextID = strings.TrimSpace(value.GoalContextID)
	value.GoalContextDigest = strings.TrimSpace(value.GoalContextDigest)
	value.AgentProfileID = strings.TrimSpace(value.AgentProfileID)
	value.ActorMemberID = strings.TrimSpace(value.ActorMemberID)
	value.PolicyID = strings.TrimSpace(value.PolicyID)
	if value.PlanningCycle == 0 {
		value.PlanningCycle = 1
	}
	value.TriggerKind = strings.TrimSpace(value.TriggerKind)
	value.TriggerRef = strings.TrimSpace(value.TriggerRef)
	value.ScheduledTaskID = strings.TrimSpace(value.ScheduledTaskID)
	value.ScheduledRunID = strings.TrimSpace(value.ScheduledRunID)
	value.DedupeKey = strings.TrimSpace(value.DedupeKey)
	value.ProposalDigest = strings.TrimSpace(value.ProposalDigest)
	value.ApprovedProposalDigest = strings.TrimSpace(value.ApprovedProposalDigest)
	value.Status = AutoplanStatus(strings.TrimSpace(string(value.Status)))
	value.ApprovalID = strings.TrimSpace(value.ApprovalID)
	if !value.ApprovalRequestedAt.IsZero() {
		value.ApprovalRequestedAt = value.ApprovalRequestedAt.UTC()
	}
	value.HILDecisionID = strings.TrimSpace(value.HILDecisionID)
	value.HILReceiptID = strings.TrimSpace(value.HILReceiptID)
	if !value.HILReceiptConsumedAt.IsZero() {
		value.HILReceiptConsumedAt = value.HILReceiptConsumedAt.UTC()
	}
	value.PlanningLeaseOwner = strings.TrimSpace(value.PlanningLeaseOwner)
	if !value.PlanningLeaseExpiresAt.IsZero() {
		value.PlanningLeaseExpiresAt = value.PlanningLeaseExpiresAt.UTC()
	}
	if !value.NextPlanningAttemptAt.IsZero() {
		value.NextPlanningAttemptAt = value.NextPlanningAttemptAt.UTC()
	}
	value.RejectionReason = strings.TrimSpace(value.RejectionReason)
	value.FailureReason = strings.TrimSpace(value.FailureReason)
	value.SupersededBy = strings.TrimSpace(value.SupersededBy)
	value.Proposal = value.Proposal.Normalized()
	value.Materializations = append([]AutoplanMaterialization(nil), value.Materializations...)
	for index := range value.Materializations {
		value.Materializations[index].StepID = strings.TrimSpace(value.Materializations[index].StepID)
		value.Materializations[index].MissionID = strings.TrimSpace(value.Materializations[index].MissionID)
		value.Materializations[index].TaskID = strings.TrimSpace(value.Materializations[index].TaskID)
		value.Materializations[index].ResponsibilityID = strings.TrimSpace(value.Materializations[index].ResponsibilityID)
		if !value.Materializations[index].MaterializedAt.IsZero() {
			value.Materializations[index].MaterializedAt = value.Materializations[index].MaterializedAt.UTC()
		}
	}
	sort.Slice(value.Materializations, func(i, j int) bool { return value.Materializations[i].StepID < value.Materializations[j].StepID })
	if !value.CreatedAt.IsZero() {
		value.CreatedAt = value.CreatedAt.UTC()
	}
	if !value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.UpdatedAt.UTC()
	}
	return value
}

func autoplanIdentityEqual(left, right AutoplanRecord) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID && left.PortfolioID == right.PortfolioID &&
		left.PortfolioBindingID == right.PortfolioBindingID && left.PortfolioVersionID == right.PortfolioVersionID &&
		left.SourceRevision == right.SourceRevision && left.SourceDigest == right.SourceDigest &&
		left.GoalContextDigest == right.GoalContextDigest &&
		left.AgentProfileID == right.AgentProfileID &&
		left.PolicyID == right.PolicyID && left.PolicyVersion == right.PolicyVersion && left.TriggerKind == right.TriggerKind &&
		left.PlanningCycle == right.PlanningCycle && left.TriggerRef == right.TriggerRef
}
