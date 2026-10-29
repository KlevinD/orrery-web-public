package responsibility

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func (r *Repository) ClaimPeopleResponsibilityReconcile(ctx context.Context, tenantID, peopleTenantID string, now time.Time, interval time.Duration) (bool, error) {
	tenantID, peopleTenantID = strings.TrimSpace(tenantID), strings.TrimSpace(peopleTenantID)
	if r == nil || r.unit == nil || tenantID == "" || peopleTenantID == "" || now.IsZero() || interval <= 0 {
		return false, ErrInvalidContract
	}
	claimed := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		id := peopleResponsibilityReconcileStateID(tenantID, peopleTenantID)
		if err := db.LockRecordKeyTx(ctx, tx, peopleReconcileStateTable, id); err != nil {
			return err
		}
		state := PeopleResponsibilityReconcileState{ID: id, TenantID: tenantID, PeopleTenantID: peopleTenantID}
		if record, ok := tx.Get(peopleReconcileStateTable, id); ok {
			if err := decodeRecord(record, &state); err != nil {
				return err
			}
			if !state.LastAttemptAt.IsZero() && now.Before(state.LastAttemptAt.Add(interval)) {
				return nil
			}
		}
		state.LastAttemptAt, state.UpdatedAt = now, now
		state.Version++
		claimed = true
		return putRecordTx(ctx, tx, peopleReconcileStateTable, id, state)
	})
	return claimed, err
}

func (r *Repository) CompletePeopleResponsibilityReconcile(ctx context.Context, tenantID, peopleTenantID string, now time.Time, runErr error) error {
	tenantID, peopleTenantID = strings.TrimSpace(tenantID), strings.TrimSpace(peopleTenantID)
	if r == nil || r.unit == nil || tenantID == "" || peopleTenantID == "" || now.IsZero() {
		return ErrInvalidContract
	}
	return r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		id := peopleResponsibilityReconcileStateID(tenantID, peopleTenantID)
		if err := db.LockRecordKeyTx(ctx, tx, peopleReconcileStateTable, id); err != nil {
			return err
		}
		state := PeopleResponsibilityReconcileState{ID: id, TenantID: tenantID, PeopleTenantID: peopleTenantID}
		if record, ok := tx.Get(peopleReconcileStateTable, id); ok {
			if err := decodeRecord(record, &state); err != nil {
				return err
			}
		}
		state.LastError = errorText(runErr)
		if runErr == nil {
			state.LastSuccessAt = now
		}
		state.UpdatedAt = now
		return putRecordTx(ctx, tx, peopleReconcileStateTable, id, state)
	})
}

func (r *Repository) GetPeopleResponsibilityReconcileState(ctx context.Context, tenantID, peopleTenantID string) (PeopleResponsibilityReconcileState, bool, error) {
	id := peopleResponsibilityReconcileStateID(strings.TrimSpace(tenantID), strings.TrimSpace(peopleTenantID))
	var out PeopleResponsibilityReconcileState
	found := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(peopleReconcileStateTable, id)
		if !ok {
			return nil
		}
		found = true
		return decodeRecord(record, &out)
	})
	return out, found, err
}

func (r *Repository) UpsertPeopleAgentBinding(ctx context.Context, value PeopleAgentBindingRecord, now time.Time) (PeopleAgentBindingRecord, bool, error) {
	value = normalizePeopleAgentBinding(value)
	if r == nil || r.unit == nil || value.ID == "" || value.TenantID == "" || value.PeopleTenantID == "" || value.SubjectID == "" || value.AgentProfileID == "" || now.IsZero() {
		return PeopleAgentBindingRecord{}, false, ErrInvalidContract
	}
	var out PeopleAgentBindingRecord
	changed := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, peopleAgentBindingTable, value.ID); err != nil {
			return err
		}
		fromState := ""
		action := "people_agent_binding_created"
		if record, ok := tx.Get(peopleAgentBindingTable, value.ID); ok {
			var existing PeopleAgentBindingRecord
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			if peopleAgentBindingEqual(existing, value) {
				out = existing
				return nil
			}
			fromState = existing.Status
			action = "people_agent_binding_updated"
			value.Version, value.CreatedAt = existing.Version+1, existing.CreatedAt
		} else {
			value.Version, value.CreatedAt = 1, now
		}
		value.UpdatedAt = now
		out, changed = value, true
		if err := putRecordTx(ctx, tx, peopleAgentBindingTable, value.ID, value); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{
			TenantID: value.TenantID, ResponsibilityID: value.ID, EntityType: "people_agent_binding", EntityID: value.ID,
			Action: action, ActorID: "system:people-responsibility-reconciler", FromState: fromState, ToState: value.Status,
			Details: fmt.Sprintf("%s -> %s", value.SubjectID, value.AgentProfileID), OccurredAt: now,
		})
	})
	return out, changed, err
}

func (r *Repository) DetachPeopleAgentBinding(ctx context.Context, bindingID string, now time.Time) (bool, error) {
	bindingID = strings.TrimSpace(bindingID)
	if r == nil || r.unit == nil || bindingID == "" || now.IsZero() {
		return false, ErrInvalidContract
	}
	changed := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, peopleAgentBindingTable, bindingID); err != nil {
			return err
		}
		record, ok := tx.Get(peopleAgentBindingTable, bindingID)
		if !ok {
			return nil
		}
		var value PeopleAgentBindingRecord
		if err := decodeRecord(record, &value); err != nil {
			return err
		}
		if value.Status == peopleBindingStatusDetached {
			return nil
		}
		value.Status, value.UpdatedAt = peopleBindingStatusDetached, now
		value.Version++
		changed = true
		if err := putRecordTx(ctx, tx, peopleAgentBindingTable, bindingID, value); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{
			TenantID: value.TenantID, ResponsibilityID: value.ID, EntityType: "people_agent_binding", EntityID: value.ID,
			Action: "people_agent_binding_detached", ActorID: "system:people-responsibility-reconciler", FromState: peopleBindingStatusActive,
			ToState: peopleBindingStatusDetached, Details: fmt.Sprintf("%s -> %s", value.SubjectID, value.AgentProfileID), OccurredAt: now,
		})
	})
	return changed, err
}

func (r *Repository) ListPeopleAgentBindings(ctx context.Context) ([]PeopleAgentBindingRecord, error) {
	var out []PeopleAgentBindingRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, peopleAgentBindingTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var value PeopleAgentBindingRecord
			if err := decodeRecord(record, &value); err != nil {
				return err
			}
			out = append(out, value)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

func (r *Repository) GetPeopleResponsibilityPortfolio(ctx context.Context, id string) (PeopleResponsibilityPortfolio, bool, error) {
	var out PeopleResponsibilityPortfolio
	found := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(peoplePortfolioTable, strings.TrimSpace(id))
		if !ok {
			return nil
		}
		found = true
		return decodeRecord(record, &out)
	})
	return out, found, err
}

func (r *Repository) GetPeopleResponsibilityPortfolioByBindingID(ctx context.Context, bindingID string) (PeopleResponsibilityPortfolio, bool, error) {
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return PeopleResponsibilityPortfolio{}, false, ErrInvalidContract
	}
	return r.GetPeopleResponsibilityPortfolio(ctx, PeopleResponsibilityPortfolioID(bindingID))
}

func (r *Repository) GetPeopleResponsibilityPortfolioVersion(ctx context.Context, id string) (PeopleResponsibilityPortfolioVersion, bool, error) {
	id = strings.TrimSpace(id)
	if r == nil || r.unit == nil || id == "" {
		return PeopleResponsibilityPortfolioVersion{}, false, ErrInvalidContract
	}
	var out PeopleResponsibilityPortfolioVersion
	found := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(peoplePortfolioVersionTable, id)
		if !ok {
			return nil
		}
		found = true
		return decodeRecord(record, &out)
	})
	return out, found, err
}

func (r *Repository) ListPeopleResponsibilityPortfolios(ctx context.Context, tenantID string) ([]PeopleResponsibilityPortfolio, error) {
	tenantID = strings.TrimSpace(tenantID)
	var out []PeopleResponsibilityPortfolio
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, peoplePortfolioTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var value PeopleResponsibilityPortfolio
			if err := decodeRecord(record, &value); err != nil {
				return err
			}
			if tenantID == "" || value.TenantID == tenantID {
				out = append(out, value)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

type PeopleResponsibilityPortfolioPage struct {
	Items      []PeopleResponsibilityPortfolio
	NextCursor string
	HasMore    bool
}

// ListActivePeopleResponsibilityPortfoliosPage keeps discovery bounded while
// advancing by the underlying record key, including pages with no matches.
func (r *Repository) ListActivePeopleResponsibilityPortfoliosPage(
	ctx context.Context,
	tenantID string,
	peopleTenantID string,
	cursor string,
	limit int,
) (PeopleResponsibilityPortfolioPage, error) {
	if r == nil || r.router == nil {
		return PeopleResponsibilityPortfolioPage{}, ErrInvalidContract
	}
	tenantID = strings.TrimSpace(tenantID)
	peopleTenantID = strings.TrimSpace(peopleTenantID)
	cursor = strings.TrimSpace(cursor)
	if tenantID == "" || peopleTenantID == "" || limit <= 0 || limit > db.MaxRecordPageLimit {
		return PeopleResponsibilityPortfolioPage{}, ErrInvalidContract
	}
	page, err := r.router.ListRecords(ctx, peoplePortfolioTable, cursor, limit)
	if err != nil {
		return PeopleResponsibilityPortfolioPage{}, err
	}
	out := PeopleResponsibilityPortfolioPage{Items: make([]PeopleResponsibilityPortfolio, 0, len(page.Items)), HasMore: page.HasMore}
	for _, keyed := range page.Items {
		var portfolio PeopleResponsibilityPortfolio
		if err := decodeRecord(keyed.Record, &portfolio); err != nil {
			return PeopleResponsibilityPortfolioPage{}, err
		}
		if portfolio.TenantID == tenantID && portfolio.PeopleTenantID == peopleTenantID && portfolio.Status == peoplePortfolioStatusActive {
			out.Items = append(out.Items, portfolio)
		}
	}
	if page.HasMore && len(page.Items) > 0 {
		out.NextCursor = page.Items[len(page.Items)-1].Key
	}
	return out, nil
}

func (r *Repository) ListPeopleResponsibilityPortfolioVersions(ctx context.Context, portfolioID string, limit int) ([]PeopleResponsibilityPortfolioVersion, error) {
	portfolioID = strings.TrimSpace(portfolioID)
	versions, err := r.ListPeopleResponsibilityPortfolioVersionsForPortfolios(ctx, []string{portfolioID}, limit)
	return versions[portfolioID], err
}

func (r *Repository) ListPeopleResponsibilityPortfolioVersionsForPortfolios(ctx context.Context, portfolioIDs []string, limit int) (map[string][]PeopleResponsibilityPortfolioVersion, error) {
	if limit <= 0 {
		limit = 20
	}
	wanted := make(map[string]struct{}, len(portfolioIDs))
	for _, portfolioID := range portfolioIDs {
		if portfolioID = strings.TrimSpace(portfolioID); portfolioID != "" {
			wanted[portfolioID] = struct{}{}
		}
	}
	out := make(map[string][]PeopleResponsibilityPortfolioVersion, len(wanted))
	if len(wanted) == 0 {
		return out, nil
	}
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, peoplePortfolioVersionTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var value PeopleResponsibilityPortfolioVersion
			if err := decodeRecord(record, &value); err != nil {
				return err
			}
			if _, ok := wanted[value.PortfolioID]; ok {
				out[value.PortfolioID] = append(out[value.PortfolioID], value)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for portfolioID, versions := range out {
		sort.Slice(versions, func(i, j int) bool { return versions[i].RecordedAt.After(versions[j].RecordedAt) })
		if len(versions) > limit {
			versions = versions[:limit]
		}
		out[portfolioID] = versions
	}
	return out, nil
}

func (r *Repository) SavePeopleResponsibilityPortfolio(ctx context.Context, value PeopleResponsibilityPortfolio, now time.Time) (PeopleResponsibilityPortfolio, bool, error) {
	value = normalizePeoplePortfolio(value)
	if r == nil || r.unit == nil || value.ID == "" || value.BindingID == "" || value.TenantID == "" || value.PeopleTenantID == "" || value.SubjectID == "" || value.AgentProfileID == "" || value.SourceDigest == "" || now.IsZero() {
		return PeopleResponsibilityPortfolio{}, false, ErrInvalidContract
	}
	var out PeopleResponsibilityPortfolio
	changed := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, peoplePortfolioTable, value.ID); err != nil {
			return err
		}
		fromState := ""
		action := "people_responsibility_portfolio_created"
		if record, ok := tx.Get(peoplePortfolioTable, value.ID); ok {
			var existing PeopleResponsibilityPortfolio
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			if peoplePortfolioEqual(existing, value) {
				out = existing
				return nil
			}
			fromState = existing.Status
			action = "people_responsibility_portfolio_updated"
			value.Version, value.CreatedAt = existing.Version+1, existing.CreatedAt
		} else {
			value.Version, value.CreatedAt = 1, now
		}
		value.SyncedAt, value.UpdatedAt = now, now
		if err := putRecordTx(ctx, tx, peoplePortfolioTable, value.ID, value); err != nil {
			return err
		}
		version := PeopleResponsibilityPortfolioVersion{
			ID: PeopleResponsibilityPortfolioVersionID(value.BindingID, value.SourceDigest), PortfolioID: value.ID,
			TenantID: value.TenantID, BindingID: value.BindingID, PeopleTenantID: value.PeopleTenantID,
			SubjectID: value.SubjectID, AgentProfileID: value.AgentProfileID, SourceRevision: value.SourceRevision,
			SourceDigest: value.SourceDigest, Responsibilities: value.Responsibilities, SourceAsOf: value.SourceAsOf, RecordedAt: now,
		}
		versionCreated := false
		if _, ok := tx.Get(peoplePortfolioVersionTable, version.ID); !ok {
			if err := putRecordTx(ctx, tx, peoplePortfolioVersionTable, version.ID, version); err != nil {
				return err
			}
			versionCreated = true
		}
		details := fmt.Sprintf("subject=%s source_revision=%s responsibilities=%d", value.SubjectID, value.SourceRevision, len(value.Responsibilities))
		if err := putAuditTx(ctx, tx, AuditRecord{
			TenantID: value.TenantID, ResponsibilityID: value.BindingID, EntityType: "people_responsibility_portfolio", EntityID: value.ID,
			Action: action, ActorID: "system:people-responsibility-reconciler", FromState: fromState, ToState: value.Status, Details: details, OccurredAt: now,
		}); err != nil {
			return err
		}
		if versionCreated {
			if err := putAuditTx(ctx, tx, AuditRecord{
				TenantID: value.TenantID, ResponsibilityID: value.BindingID, EntityType: "people_responsibility_portfolio_version", EntityID: version.ID,
				Action: "people_responsibility_portfolio_version_recorded", ActorID: "system:people-responsibility-reconciler",
				ToState: value.SourceRevision, Details: details, OccurredAt: now,
			}); err != nil {
				return err
			}
		}
		out, changed = value, true
		return nil
	})
	return out, changed, err
}

func (r *Repository) BackfillPeopleResponsibilityPortfolioVersions(ctx context.Context, now time.Time) (int, error) {
	portfolios, err := r.ListPeopleResponsibilityPortfolios(ctx, "")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, portfolio := range portfolios {
		versions, err := r.ListPeopleResponsibilityPortfolioVersions(ctx, portfolio.ID, 1)
		if err != nil {
			return count, err
		}
		if len(versions) != 0 {
			continue
		}
		err = r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			version := PeopleResponsibilityPortfolioVersion{
				ID: PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest), PortfolioID: portfolio.ID,
				TenantID: portfolio.TenantID, BindingID: portfolio.BindingID, PeopleTenantID: portfolio.PeopleTenantID,
				SubjectID: portfolio.SubjectID, AgentProfileID: portfolio.AgentProfileID, SourceRevision: portfolio.SourceRevision,
				SourceDigest: portfolio.SourceDigest, Responsibilities: portfolio.Responsibilities, SourceAsOf: portfolio.SourceAsOf, RecordedAt: now,
			}
			return putRecordTx(ctx, tx, peoplePortfolioVersionTable, version.ID, version)
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (r *Repository) OpenPeopleResponsibilityGap(ctx context.Context, value PeopleResponsibilityGap, now time.Time) (bool, error) {
	value.ID, value.TenantID, value.BindingID = strings.TrimSpace(value.ID), strings.TrimSpace(value.TenantID), strings.TrimSpace(value.BindingID)
	value.SubjectID, value.AgentProfileID, value.Code, value.Message = strings.TrimSpace(value.SubjectID), strings.TrimSpace(value.AgentProfileID), strings.TrimSpace(value.Code), strings.TrimSpace(value.Message)
	if r == nil || r.unit == nil || value.ID == "" || value.TenantID == "" || value.BindingID == "" || value.SubjectID == "" || value.AgentProfileID == "" || value.Code == "" || now.IsZero() {
		return false, ErrInvalidContract
	}
	changed := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, peopleGapTable, value.ID); err != nil {
			return err
		}
		fromState := ""
		action := "people_responsibility_gap_opened"
		if record, ok := tx.Get(peopleGapTable, value.ID); ok {
			var existing PeopleResponsibilityGap
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			value.FirstSeenAt = existing.FirstSeenAt
			if existing.Open && existing.Code == value.Code && existing.Message == value.Message {
				return nil
			}
			fromState = existing.Code
			action = "people_responsibility_gap_updated"
		} else {
			value.FirstSeenAt = now
		}
		value.Open, value.LastSeenAt, value.ResolvedAt = true, now, time.Time{}
		changed = true
		if err := putRecordTx(ctx, tx, peopleGapTable, value.ID, value); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{
			TenantID: value.TenantID, ResponsibilityID: value.BindingID, EntityType: "people_responsibility_gap", EntityID: value.ID,
			Action: action, ActorID: "system:people-responsibility-reconciler", FromState: fromState, ToState: value.Code, Details: value.Message, OccurredAt: now,
		})
	})
	return changed, err
}

func (r *Repository) ResolvePeopleResponsibilityGap(ctx context.Context, gapID string, now time.Time) (bool, error) {
	gapID = strings.TrimSpace(gapID)
	if r == nil || r.unit == nil || gapID == "" || now.IsZero() {
		return false, ErrInvalidContract
	}
	changed := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, peopleGapTable, gapID); err != nil {
			return err
		}
		record, ok := tx.Get(peopleGapTable, gapID)
		if !ok {
			return nil
		}
		var value PeopleResponsibilityGap
		if err := decodeRecord(record, &value); err != nil {
			return err
		}
		if !value.Open {
			return nil
		}
		value.Open, value.LastSeenAt, value.ResolvedAt = false, now, now
		changed = true
		if err := putRecordTx(ctx, tx, peopleGapTable, gapID, value); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{
			TenantID: value.TenantID, ResponsibilityID: value.BindingID, EntityType: "people_responsibility_gap", EntityID: value.ID,
			Action: "people_responsibility_gap_resolved", ActorID: "system:people-responsibility-reconciler", FromState: value.Code, ToState: "resolved", OccurredAt: now,
		})
	})
	return changed, err
}

func (r *Repository) GetPeopleResponsibilityGap(ctx context.Context, id string) (PeopleResponsibilityGap, bool, error) {
	var out PeopleResponsibilityGap
	found := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(peopleGapTable, strings.TrimSpace(id))
		if !ok {
			return nil
		}
		found = true
		return decodeRecord(record, &out)
	})
	return out, found, err
}

func normalizePeopleAgentBinding(value PeopleAgentBindingRecord) PeopleAgentBindingRecord {
	value.ID, value.TenantID, value.PeopleTenantID = strings.TrimSpace(value.ID), strings.TrimSpace(value.TenantID), strings.TrimSpace(value.PeopleTenantID)
	value.SubjectID, value.AgentProfileID, value.Source, value.Status = strings.TrimSpace(value.SubjectID), strings.TrimSpace(value.AgentProfileID), strings.TrimSpace(value.Source), strings.TrimSpace(value.Status)
	if value.Source == "" {
		value.Source = "people"
	}
	if value.Status == "" {
		value.Status = peopleBindingStatusActive
	}
	return value
}

func normalizePeoplePortfolio(value PeopleResponsibilityPortfolio) PeopleResponsibilityPortfolio {
	value.ID, value.TenantID, value.BindingID = strings.TrimSpace(value.ID), strings.TrimSpace(value.TenantID), strings.TrimSpace(value.BindingID)
	value.PeopleTenantID, value.SubjectID, value.AgentProfileID = strings.TrimSpace(value.PeopleTenantID), strings.TrimSpace(value.SubjectID), strings.TrimSpace(value.AgentProfileID)
	value.SourceRevision, value.SourceDigest, value.Status = strings.TrimSpace(value.SourceRevision), strings.TrimSpace(value.SourceDigest), strings.TrimSpace(value.Status)
	value.PulseScheduledTaskID = strings.TrimSpace(value.PulseScheduledTaskID)
	if value.Status == "" {
		value.Status = peoplePortfolioStatusActive
	}
	return value
}

func peopleAgentBindingEqual(left, right PeopleAgentBindingRecord) bool {
	return left.TenantID == right.TenantID && left.PeopleTenantID == right.PeopleTenantID && left.SubjectID == right.SubjectID && left.AgentProfileID == right.AgentProfileID && left.Source == right.Source && left.Status == right.Status
}

func peoplePortfolioEqual(left, right PeopleResponsibilityPortfolio) bool {
	return left.TenantID == right.TenantID && left.BindingID == right.BindingID && left.PeopleTenantID == right.PeopleTenantID && left.SubjectID == right.SubjectID && left.AgentProfileID == right.AgentProfileID && left.SourceRevision == right.SourceRevision && left.SourceDigest == right.SourceDigest && left.Status == right.Status && left.PulseScheduledTaskID == right.PulseScheduledTaskID && left.SourceAsOf.Equal(right.SourceAsOf) && reflect.DeepEqual(left.Responsibilities, right.Responsibilities)
}
