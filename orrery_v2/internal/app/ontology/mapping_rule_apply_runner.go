package ontology

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

type MappingRuleProjectionRunner struct {
	projections *graphprotocol.ProjectionRegistry
	gate        graphprotocol.ConformanceGate
	graphStore  contextgraph.WriterStore

	refreshMu       sync.Mutex
	refreshRegistry Registry
	revisions       MappingProjectionRevisionReader
	revision        int64
}

type MappingRuleProjectionRunReport struct {
	OK                     bool                              `json:"ok"`
	MatchedProjectionCount int                               `json:"matched_projection_count"`
	AppliedProjectionCount int                               `json:"applied_projection_count"`
	Applied                int                               `json:"applied"`
	ConformanceReports     []graphprotocol.ConformanceReport `json:"conformance_reports,omitempty"`
	Issues                 []graphprotocol.ConformanceIssue  `json:"issues,omitempty"`
}

type MappingRuleProjectionPlan struct {
	Report  MappingRuleProjectionRunReport
	results []graphprotocol.ProjectionResult
}

type StateProjectionEvent struct {
	EntityID                  string
	SnapshotAt                time.Time
	AuthoritativeCurrentState bool
	Event                     graphprotocol.DomainEvent
}

func NewMappingRuleProjectionRunner(projections *graphprotocol.ProjectionRegistry, gate graphprotocol.ConformanceGate, graphStore contextgraph.WriterStore) *MappingRuleProjectionRunner {
	return &MappingRuleProjectionRunner{
		projections: projections,
		gate:        gate,
		graphStore:  graphStore,
	}
}

func (r *MappingRuleProjectionRunner) RefreshProjections(ctx context.Context, registry Registry) error {
	if r == nil || r.projections == nil {
		return fmt.Errorf("%w: projection registry is required", ErrInvalidDefinition)
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if r.refreshRegistry != nil && r.revisions != nil {
		return r.refreshToCurrentRevisionLocked(ctx)
	}
	return SyncMappingRuleProjections(ctx, registry, r.projections)
}

// EnableRevisionRefresh makes projection selection fail closed and self-refresh
// against the durable mapping-change epoch. The initial stable read closes the
// race between loading rules and observing the current epoch.
func (r *MappingRuleProjectionRunner) EnableRevisionRefresh(ctx context.Context, registry Registry, revisions MappingProjectionRevisionReader) error {
	if r == nil || r.projections == nil || registry == nil || revisions == nil {
		return fmt.Errorf("%w: projection refresh registry and revision reader are required", ErrInvalidDefinition)
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.refreshRegistry = registry
	r.revisions = revisions
	return r.refreshToCurrentRevisionLocked(ctx)
}

func (r *MappingRuleProjectionRunner) ensureCurrentProjections(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if r.refreshRegistry == nil || r.revisions == nil {
		return nil
	}
	current, err := r.revisions.MappingProjectionRevision(ctx)
	if err != nil {
		return err
	}
	if current == r.revision {
		return nil
	}
	return r.refreshToCurrentRevisionLocked(ctx)
}

func (r *MappingRuleProjectionRunner) refreshToCurrentRevisionLocked(ctx context.Context) error {
	for {
		before, err := r.revisions.MappingProjectionRevision(ctx)
		if err != nil {
			return err
		}
		if err := SyncMappingRuleProjections(ctx, r.refreshRegistry, r.projections); err != nil {
			return err
		}
		after, err := r.revisions.MappingProjectionRevision(ctx)
		if err != nil {
			return err
		}
		if before == after {
			r.revision = after
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (r *MappingRuleProjectionRunner) ApplyEvent(ctx context.Context, event graphprotocol.DomainEvent) (MappingRuleProjectionRunReport, error) {
	plan, err := r.PrepareEvent(ctx, event)
	if err != nil {
		return plan.Report, err
	}
	if err := r.ApplyPlan(ctx, plan); err != nil {
		plan.Report.addIssue("context_graph_apply_error", err.Error())
		return plan.Report, err
	}
	plan.Report.AppliedProjectionCount = len(plan.results)
	plan.Report.Applied = plan.Report.AppliedProjectionCount
	return plan.Report, nil
}

func (r *MappingRuleProjectionRunner) ApplyStateEvent(ctx context.Context, store *PlatformStateProjectionStore, input StateProjectionEvent) (StateProjectionReport, error) {
	stateInput := StateProjectionInput{
		TenantID:              input.Event.TenantID,
		EventType:             input.Event.Type,
		EntityID:              input.EntityID,
		Payload:               input.Event.Payload,
		SourceVersion:         input.Event.Version,
		SourceUpdatedAt:       input.Event.OccurredAt,
		SnapshotAt:            input.SnapshotAt,
		AllowEqualOrderRepair: input.AuthoritativeCurrentState,
	}
	inspection, err := store.Inspect(ctx, stateInput)
	if err != nil {
		return StateProjectionReport{}, err
	}
	if inspection.Outcome != StateProjectionChanged {
		return store.Apply(ctx, stateInput, nil)
	}
	plan, err := r.PrepareEvent(ctx, input.Event)
	if err != nil {
		return StateProjectionReport{}, err
	}
	return store.Apply(ctx, stateInput, func(ctx context.Context, tx db.Tx) error {
		return plan.Apply(ctx, contextgraph.NewPlatformStoreTx(tx))
	})
}

func (r *MappingRuleProjectionRunner) PrepareEvent(ctx context.Context, event graphprotocol.DomainEvent) (MappingRuleProjectionPlan, error) {
	report := MappingRuleProjectionRunReport{OK: true}
	if r == nil || r.projections == nil {
		report.addIssue("projection_registry_missing", "projection registry is required")
		return MappingRuleProjectionPlan{Report: report}, fmt.Errorf("%w: projection registry is required", ErrInvalidDefinition)
	}
	if err := r.ensureCurrentProjections(ctx); err != nil {
		report.addIssue("projection_refresh_error", err.Error())
		return MappingRuleProjectionPlan{Report: report}, err
	}
	plan := MappingRuleProjectionPlan{Report: report}
	projections := r.projections.ListBySourceType(event.Type)
	for _, projection := range projections {
		if !projectionAppliesToTenant(projection, event.TenantID) {
			continue
		}
		plan.Report.MatchedProjectionCount++
		result, err := projection.Project(ctx, event)
		if err != nil {
			conformance := graphprotocol.ConformanceReport{
				OK:             false,
				ProjectionName: projection.Spec().Name,
				Issues: []graphprotocol.ConformanceIssue{{
					Code:    "projection_error",
					Message: err.Error(),
				}},
			}
			plan.Report.ConformanceReports = append(plan.Report.ConformanceReports, conformance)
			plan.Report.OK = false
			plan.Report.Issues = append(plan.Report.Issues, conformance.Issues...)
			return plan, graphprotocol.ConformanceError{Report: conformance}
		}
		conformance := r.gate.CheckResult(ctx, projection, event, result)
		plan.Report.ConformanceReports = append(plan.Report.ConformanceReports, conformance)
		if !conformance.OK {
			plan.Report.OK = false
			plan.Report.Issues = append(plan.Report.Issues, conformance.Issues...)
			return plan, graphprotocol.ConformanceError{Report: conformance}
		}
		plan.results = append(plan.results, result)
	}
	return plan, nil
}

func (r *MappingRuleProjectionRunner) ApplyPlan(ctx context.Context, plan MappingRuleProjectionPlan) error {
	if r == nil || r.graphStore == nil {
		return fmt.Errorf("%w: context graph store is required", ErrInvalidDefinition)
	}
	return plan.Apply(ctx, r.graphStore)
}

func (p MappingRuleProjectionPlan) Apply(ctx context.Context, store contextgraph.WriterStore) error {
	return contextgraph.ApplyProjectionResults(ctx, store, p.results)
}

func projectionAppliesToTenant(projection graphprotocol.GraphProjection, tenantID string) bool {
	if projection == nil {
		return false
	}
	projectionTenantID := projection.Spec().TenantID
	return projectionTenantID == "" || projectionTenantID == tenantID
}

func (r *MappingRuleProjectionRunReport) addIssue(code string, message string) {
	r.OK = false
	r.Issues = append(r.Issues, graphprotocol.ConformanceIssue{Code: code, Message: message})
}
