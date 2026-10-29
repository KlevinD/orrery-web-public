package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memorystore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

type productionEnterpriseGraphRuntime struct {
	bundles interface {
		BuildContextBundle(context.Context, contextconsumer.BuildContextBundleInput) (contextgraph.ContextBundle, error)
	}
	search interface {
		SearchObjects(context.Context, contextconsumer.SearchObjectsInput) (contextconsumer.SearchObjectsResult, error)
	}
	limiter   *ontologycontract.ConcurrencyLimiter
	snapshots authorization.SnapshotProvider
	metrics   ontologyMetricRecorder
	timeout   time.Duration
	now       func() time.Time
}

func newProductionEnterpriseGraphRuntimeForServer(
	core *db.Store,
	rt *platform.Runtime,
	security *productionOntologySecurityState,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memorystore.PlatformStore,
) (*productionEnterpriseGraphRuntime, error) {
	access := productionEnterpriseGraphAccess{policy: newProductionContextAccessPolicy(
		core, rt.DBRouter, security.Scopes, authorizer, knowledge, memory,
	)}
	return newProductionEnterpriseGraphRuntime(
		core, access, security.Cursors, security.Snapshots, security.Concurrency, rt.MetricRecorder, rt.Clock.Now,
	)
}

func newProductionEnterpriseGraphRuntime(
	core *db.Store,
	visibility contextgraph.ObjectVisibilityProvider,
	cursors *productionOntologyCursorState,
	snapshots authorization.SnapshotProvider,
	limiter *ontologycontract.ConcurrencyLimiter,
	metrics ontologyMetricRecorder,
	now func() time.Time,
) (*productionEnterpriseGraphRuntime, error) {
	if cursors == nil || snapshots == nil || limiter == nil {
		return nil, fmt.Errorf("enterprise graph runtime security dependencies are required")
	}
	search, err := contextconsumer.NewSearchService(
		contextgraph.NewPlatformStore(core), visibility, productionEnterpriseGraphSearchCursor{codec: cursors.Encrypted, stateful: cursors.Stateful},
	)
	if err != nil {
		return nil, err
	}
	return &productionEnterpriseGraphRuntime{
		bundles: newProductionEnterpriseGraphConsumer(core, visibility),
		search:  search,
		limiter: limiter, snapshots: snapshots, metrics: metrics, timeout: 5 * time.Second, now: now,
	}, nil
}

func (r *productionEnterpriseGraphRuntime) BuildContextBundle(ctx context.Context, input contextconsumer.BuildContextBundleInput) (contextgraph.ContextBundle, error) {
	if r == nil || r.bundles == nil {
		return contextgraph.ContextBundle{}, fmt.Errorf("enterprise graph bundle runtime is required")
	}
	startedAt := r.nowTime()
	input, err := contextconsumer.NormalizeBuildContextBundleInput(input)
	if err != nil {
		r.captureBundleMetric(ctx, enterpriseGraphMetricStatus(err), input.MaxDepth, contextgraph.ContextBundle{}, startedAt)
		return contextgraph.ContextBundle{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, r.requestTimeout())
	defer cancel()
	snapshot, err := r.captureSnapshot(bounded, input.ActorRef, input.TenantID, input.PermissionScopes)
	err = enterpriseGraphBoundedError(err, bounded)
	if err != nil {
		r.captureBundleMetric(ctx, enterpriseGraphMetricStatus(err), input.MaxDepth, contextgraph.ContextBundle{}, startedAt)
		return contextgraph.ContextBundle{}, err
	}
	release, err := r.limiter.Acquire(bounded, ontologycontract.ConcurrencyNeighborhood, snapshot.PrincipalKey)
	err = enterpriseGraphBoundedError(err, bounded)
	if err != nil {
		r.captureBundleMetric(ctx, enterpriseGraphMetricStatus(err), input.MaxDepth, contextgraph.ContextBundle{}, startedAt)
		return contextgraph.ContextBundle{}, err
	}
	defer release()
	bundle, err := r.bundles.BuildContextBundle(bounded, input)
	err = enterpriseGraphBoundedError(err, bounded)
	r.captureBundleMetric(ctx, enterpriseGraphMetricStatus(err), input.MaxDepth, bundle, startedAt)
	return bundle, err
}

func (r *productionEnterpriseGraphRuntime) SearchObjects(ctx context.Context, input contextconsumer.SearchObjectsInput) (contextconsumer.SearchObjectsResult, error) {
	if r == nil || r.search == nil {
		return contextconsumer.SearchObjectsResult{}, fmt.Errorf("enterprise graph search runtime is required")
	}
	startedAt := r.nowTime()
	input, err := contextconsumer.NormalizeSearchObjectsInput(input)
	if err != nil {
		r.captureSearchMetric(ctx, enterpriseGraphMetricStatus(err), contextconsumer.SearchObjectsResult{}, startedAt)
		return contextconsumer.SearchObjectsResult{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, r.requestTimeout())
	defer cancel()
	snapshot, err := r.captureSnapshot(bounded, input.ActorRef, input.TenantID, input.PermissionScopes)
	err = enterpriseGraphBoundedError(err, bounded)
	if err != nil {
		r.captureSearchMetric(ctx, enterpriseGraphMetricStatus(err), contextconsumer.SearchObjectsResult{}, startedAt)
		return contextconsumer.SearchObjectsResult{}, err
	}
	input.PrincipalKey = snapshot.PrincipalKey
	input.AuthorizationRevision = snapshot.AuthorizationRevision
	input.PolicyRevision = snapshot.PolicyRevision
	release, err := r.limiter.Acquire(bounded, ontologycontract.ConcurrencyNeighborhood, snapshot.PrincipalKey)
	err = enterpriseGraphBoundedError(err, bounded)
	if err != nil {
		r.captureSearchMetric(ctx, enterpriseGraphMetricStatus(err), contextconsumer.SearchObjectsResult{}, startedAt)
		return contextconsumer.SearchObjectsResult{}, err
	}
	defer release()
	result, err := r.search.SearchObjects(bounded, input)
	err = enterpriseGraphBoundedError(err, bounded)
	r.captureSearchMetric(ctx, enterpriseGraphMetricStatus(err), result, startedAt)
	return result, err
}

func enterpriseGraphBoundedError(err error, bounded context.Context) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", contextgraph.ErrQueryBudgetExceeded, context.DeadlineExceeded)
	}
	return err
}

func (r *productionEnterpriseGraphRuntime) captureSnapshot(ctx context.Context, actor contextgraph.Ref, tenantID string, scopes []string) (authorization.AuthorizationSnapshot, error) {
	principal := identity.Principal{
		SubjectProvider: "orrery", Subject: strings.TrimSpace(actor.ID), ViewerKind: strings.ToLower(strings.TrimSpace(actor.Kind)),
		TenantID: strings.TrimSpace(tenantID), CoarseScopes: append([]string(nil), scopes...),
	}
	if principal.ViewerKind == identity.ViewerKindAgent {
		principal.AgentProfileID = principal.Subject
	} else {
		principal.UserID = principal.Subject
	}
	return r.snapshots.Capture(ctx, principal)
}

func (r *productionEnterpriseGraphRuntime) requestTimeout() time.Duration {
	if r.timeout <= 0 {
		return 5 * time.Second
	}
	return r.timeout
}

func (r *productionEnterpriseGraphRuntime) nowTime() time.Time {
	if r.now == nil {
		return time.Now().UTC()
	}
	return r.now().UTC()
}

func (r *productionEnterpriseGraphRuntime) captureBundleMetric(ctx context.Context, status string, depth int, bundle contextgraph.ContextBundle, startedAt time.Time) {
	if r.metrics == nil {
		return
	}
	raw, _ := json.Marshal(bundle)
	schemaDefinitions := len(bundle.Schema.ObjectTypes) + len(bundle.Schema.LinkTypes) + len(bundle.Schema.PropertyTypes) + len(bundle.Schema.ActionTypes)
	r.metrics.CaptureContext(ctx, "ontology.context_bundle", map[string]any{
		"operation": "bundle", "status": status,
		"objects": len(bundle.Objects), "links": len(bundle.Links), "facts": len(bundle.Facts), "evidence": len(bundle.Evidence),
		"schema_definitions": schemaDefinitions, "response_bytes": len(raw), "estimated_tokens": (len(raw) + 3) / 4,
		"depth": depth, "duration_ms": max(0, int(r.nowTime().Sub(startedAt).Milliseconds())),
	})
}

func (r *productionEnterpriseGraphRuntime) captureSearchMetric(ctx context.Context, status string, result contextconsumer.SearchObjectsResult, startedAt time.Time) {
	if r.metrics == nil {
		return
	}
	raw, _ := json.Marshal(result)
	r.metrics.CaptureContext(ctx, "ontology.context_search", map[string]any{
		"operation": "search", "status": status,
		"objects": len(result.Items), "links": 0, "facts": 0, "evidence": 0,
		"schema_definitions": 0, "response_bytes": len(raw), "estimated_tokens": (len(raw) + 3) / 4,
		"depth": 0, "duration_ms": max(0, int(r.nowTime().Sub(startedAt).Milliseconds())),
	})
}

func enterpriseGraphMetricStatus(err error) string {
	switch {
	case err == nil:
		return "succeeded"
	case errors.Is(err, ontologycontract.ErrRateLimited):
		return "busy"
	case errors.Is(err, contextgraph.ErrQueryInvalid), errors.Is(err, ontologycontract.ErrInvalidRequest):
		return "invalid"
	case errors.Is(err, contextgraph.ErrQueryBudgetExceeded), errors.Is(err, ontologycontract.ErrQueryBudgetExceeded), errors.Is(err, context.DeadlineExceeded):
		return "budget"
	default:
		return "failed"
	}
}

type productionEnterpriseGraphSearchCursor struct {
	codec    *ontologycontract.CursorCodec
	stateful *ontologycontract.StateCursorStore
}

func (c productionEnterpriseGraphSearchCursor) DecodeSearchCursor(input contextconsumer.SearchObjectsInput) (contextgraph.ObjectSeek, error) {
	binding := enterpriseGraphSearchCursorBinding(input)
	var position ontologycontract.CursorPosition
	var err error
	if strings.HasPrefix(strings.TrimSpace(input.Cursor), "ocs1.") {
		if c.stateful == nil {
			err = ontologycontract.ErrInvalidCursor
		} else {
			position, err = c.stateful.Resolve(input.Cursor, binding)
		}
	} else if c.codec != nil {
		position, err = c.codec.Decode(input.Cursor, binding)
	} else {
		err = ontologycontract.ErrInvalidCursor
	}
	if err != nil {
		return contextgraph.ObjectSeek{}, err
	}
	if len(position.Seek) != 2 {
		return contextgraph.ObjectSeek{}, ontologycontract.ErrInvalidCursor
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, position.Seek[0])
	if err != nil || strings.TrimSpace(position.Seek[1]) == "" {
		return contextgraph.ObjectSeek{}, ontologycontract.ErrInvalidCursor
	}
	return contextgraph.ObjectSeek{UpdatedAt: updatedAt.UTC(), ID: position.Seek[1]}, nil
}

func (c productionEnterpriseGraphSearchCursor) EncodeSearchCursor(input contextconsumer.SearchObjectsInput, seek contextgraph.ObjectSeek) (string, error) {
	if seek.UpdatedAt.IsZero() || strings.TrimSpace(seek.ID) == "" {
		return "", ontologycontract.ErrInvalidRequest
	}
	position := ontologycontract.CursorPosition{
		Binding: enterpriseGraphSearchCursorBinding(input),
		Seek:    []string{seek.UpdatedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(seek.ID)},
	}
	if strings.TrimSpace(input.AuthorizationRevision) != "" && strings.TrimSpace(input.PolicyRevision) != "" && c.codec != nil {
		return c.codec.Encode(position)
	}
	if c.stateful == nil {
		return "", ontologycontract.ErrInvalidRequest
	}
	return c.stateful.Issue(position)
}

func enterpriseGraphSearchCursorBinding(input contextconsumer.SearchObjectsInput) ontologycontract.CursorBinding {
	kinds := append([]string(nil), input.IncludeKinds...)
	sort.Strings(kinds)
	queryHash, _ := ontologycontract.CanonicalQueryHash(struct {
		Query string   `json:"query"`
		Kinds []string `json:"kinds"`
		Limit int      `json:"limit"`
	}{strings.TrimSpace(input.Query), kinds, input.Limit})
	return ontologycontract.CursorBinding{
		PrincipalKey: strings.TrimSpace(input.PrincipalKey), TenantID: strings.TrimSpace(input.TenantID),
		BindingVersion: 1, QueryHash: queryHash,
		AuthorizationRevision: strings.TrimSpace(input.AuthorizationRevision),
		PolicyRevision:        strings.TrimSpace(input.PolicyRevision),
	}
}
