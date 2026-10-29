package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var ErrUnavailable = ontologycontract.ErrUnavailable

const (
	projectionScanBatch            = contextgraph.MaxQueryPageSize
	projectionCandidateScanBatches = 8
)

var auditSummaryFields = []string{"observed_at", "status"}

type CursorSet struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

type Service struct {
	gate        *ontologyscope.ScopeAccessGate
	audits      AuditReader
	visibility  VisibilityPolicy
	cursors     CursorSet
	budgets     *ontologycontract.BudgetPolicy
	concurrency *ontologycontract.ConcurrencyLimiter
	now         func() time.Time
}

func NewService(
	gate *ontologyscope.ScopeAccessGate,
	audits AuditReader,
	visibility VisibilityPolicy,
	cursors CursorSet,
	budgets *ontologycontract.BudgetPolicy,
	concurrency *ontologycontract.ConcurrencyLimiter,
) (*Service, error) {
	if gate == nil || audits == nil || visibility == nil || cursors.Stateful == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	var err error
	if budgets == nil {
		budgets, err = ontologycontract.NewBudgetPolicy(nil)
		if err != nil {
			return nil, err
		}
	}
	if concurrency == nil {
		concurrency, err = ontologycontract.NewConcurrencyLimiter(nil)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		gate: gate, audits: audits, visibility: visibility, cursors: cursors,
		budgets: budgets, concurrency: concurrency, now: time.Now,
	}, nil
}

func (s *Service) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[SummaryData], error) {
	tenantID = strings.TrimSpace(tenantID)
	if !validID(tenantID) {
		return ontologycontract.Summary[SummaryData]{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySummary, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer cancel()
	state, err := s.resolveVisibility(bounded, principal, authorized, budget)
	if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		return unavailableSummary(s.now(), authorized), nil
	}
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}

	unresolvedExact, latestExact := true, true
	var unresolved int64
	var latest *time.Time
	seek := contextgraph.ProjectionAuditSeek{}
	for {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return unavailableSummary(s.now(), authorized), nil
		}
		page, err := s.audits.ListVisibleProjectionAudits(bounded, contextgraph.ProjectionAuditCursorQuery{
			TenantID: tenantID, Visibility: state.constraint, Limit: projectionScanBatch, After: seek,
		})
		if err != nil {
			return ontologycontract.Summary[SummaryData]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Items), page.HasMore)); err != nil {
			return unavailableSummary(s.now(), authorized), nil
		}
		if len(page.Items) == 0 {
			if page.HasMore {
				return ontologycontract.Summary[SummaryData]{}, dependencyError(errors.New("projection audit reader returned an empty truncated page"))
			}
			break
		}
		rows, err := s.visibleAudits(bounded, principal, state, page.Items, auditSummaryFields)
		if err != nil {
			return ontologycontract.Summary[SummaryData]{}, queryError(bounded, budget, err)
		}
		for _, row := range rows {
			allowed := allowedFields(row.decision)
			if allowed["status"] {
				if row.audit.Status == "skipped" {
					unresolved++
				}
			} else {
				unresolvedExact = false
			}
			if allowed["observed_at"] {
				if latest == nil || row.audit.ObservedAt.After(*latest) {
					latest = visibleTime(row.audit.ObservedAt)
				}
			} else {
				latestExact = false
			}
		}
		if !page.HasMore {
			break
		}
		last := page.Items[len(page.Items)-1]
		seek = contextgraph.ProjectionAuditSeek{ObservedAt: last.ObservedAt, ID: last.ID}
	}

	data := SummaryData{
		UnresolvedAudits: ontologycontract.UnavailableCount(),
		LatestActivity:   LatestActivityMetric{Status: ontologycontract.MetricUnavailable},
	}
	if unresolvedExact {
		data.UnresolvedAudits = ontologycontract.ExactCount(unresolved)
	}
	if latestExact {
		data.LatestActivity = LatestActivityMetric{Status: ontologycontract.MetricExact, ObservedAt: cloneTime(latest)}
	}
	revision := projectionDataRevision(unresolvedExact, unresolved, latestExact, latest)
	return ontologycontract.Summary[SummaryData]{Data: data, Meta: summaryMeta(s.now(), authorized, revision)}, nil
}

func (s *Service) authorize(ctx context.Context, principal identity.Principal, tenantID string) (ontologyscope.AuthorizedScopeContext, error) {
	if s == nil || s.gate == nil {
		return ontologyscope.AuthorizedScopeContext{}, ontologyscope.ErrDependencyUnavailable
	}
	authorized, err := s.gate.AuthorizeRead(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	if ontologyscope.IsNonDisclosingReadError(err) {
		return ontologyscope.AuthorizedScopeContext{}, ErrUnavailable
	}
	return authorized, err
}

func unavailableSummary(now time.Time, scope ontologyscope.AuthorizedScopeContext) ontologycontract.Summary[SummaryData] {
	return ontologycontract.Summary[SummaryData]{
		Data: SummaryData{
			UnresolvedAudits: ontologycontract.UnavailableCount(),
			LatestActivity:   LatestActivityMetric{Status: ontologycontract.MetricUnavailable},
		},
		Meta: summaryMeta(now, scope, projectionDataRevision(false, 0, false, nil)),
	}
}

func summaryMeta(now time.Time, scope ontologyscope.AuthorizedScopeContext, revision string) ontologycontract.SummaryMeta {
	meta := ontologycontract.SummaryMeta{AsOf: now.UTC(), DataRevision: revision}
	if value := strings.TrimSpace(scope.Snapshot().AuthorizationRevision); value != "" {
		meta.AuthorizationRevision = &value
	}
	return meta
}

func projectionDataRevision(unresolvedExact bool, unresolved int64, latestExact bool, latest *time.Time) string {
	if !unresolvedExact {
		unresolved = 0
	}
	if !latestExact {
		latest = nil
	}
	latestNanos := int64(0)
	if latest != nil {
		latestNanos = latest.UTC().UnixNano()
	}
	payload := strings.Join([]string{
		strconv.FormatBool(unresolvedExact), strconv.FormatInt(unresolved, 10),
		strconv.FormatBool(latestExact), strconv.FormatInt(latestNanos, 10),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func dependencyError(err error) error {
	if errors.Is(err, ontologycontract.ErrInvalidRequest) || errors.Is(err, ontologycontract.ErrInvalidCursor) || errors.Is(err, ontologycontract.ErrCursorStale) || errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ontologyscope.ErrDependencyUnavailable, err)
}

func queryError(ctx context.Context, budget *ontologycontract.BudgetTracker, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return budget.CheckContext(ctx)
	}
	if errors.Is(err, contextgraph.ErrNotFound) {
		return ErrUnavailable
	}
	return dependencyError(err)
}

func exactQueryError(ctx context.Context, budget *ontologycontract.BudgetTracker, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return budget.CheckContext(ctx)
	}
	if errors.Is(err, contextgraph.ErrNotFound) {
		return ErrUnavailable
	}
	return dependencyError(err)
}

func validID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func scannedCount(returned int, hasMore bool) int {
	if hasMore {
		return returned + 1
	}
	return returned
}

func visibleTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
