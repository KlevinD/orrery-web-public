package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/hil"
)

func TestProductionAsyncHILResolutionObserverReturnsBeforeRuntimeContinuation(t *testing.T) {
	observed := make(chan hil.DecisionResolution, 1)
	observedContext := make(chan context.Context, 1)
	release := make(chan struct{})
	observer := hil.ResolutionObserverFunc(func(ctx context.Context, resolution hil.DecisionResolution) {
		observedContext <- ctx
		observed <- resolution
		<-release
	})
	wrapped := newProductionAsyncHILResolutionObserver(observer, nil)
	want := hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "decision-1", Status: hil.DecisionResolved},
		Receipt:  hil.DecisionReceipt{ID: "receipt-1", Effect: hil.EffectAuthorize},
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		wrapped.DecisionResolved(requestCtx, want)
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("async observer blocked the HIL caller")
	}

	var observerCtx context.Context
	select {
	case observerCtx = <-observedContext:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("runtime observer was not invoked")
	}
	cancel()
	if observerCtx.Err() != nil {
		close(release)
		t.Fatalf("observer context inherited request cancellation: %v", observerCtx.Err())
	}
	got := <-observed
	if got.Decision.ID != want.Decision.ID || got.Receipt.ID != want.Receipt.ID {
		close(release)
		t.Fatalf("resolution = %+v, want %+v", got, want)
	}
	close(release)
}

func TestProductionAsyncHILResolutionObserverReportsPanics(t *testing.T) {
	reported := make(chan any, 1)
	wrapped := newProductionAsyncHILResolutionObserver(
		hil.ResolutionObserverFunc(func(context.Context, hil.DecisionResolution) { panic("runtime observer panic") }),
		func(value any) { reported <- value },
	)

	wrapped.DecisionResolved(context.Background(), hil.DecisionResolution{})

	select {
	case got := <-reported:
		if got != "runtime observer panic" {
			t.Fatalf("reported panic = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("observer panic was not reported")
	}
}

func TestProductionHILContextProviderBuildsVisibleOntologySnapshot(t *testing.T) {
	graph := &recordingHILContextGraph{bundle: contextgraph.ContextBundle{
		BundleID:   "bundle-1",
		Schema:     contextgraph.SchemaDictionary{VersionHash: "schema-1"},
		Confidence: 0.87,
		Objects: []contextgraph.Object{
			{ID: "customer-1", ObjectType: "customer"},
			{ID: "person:ding", ObjectType: "person", Label: "Ding", Properties: map[string]any{"role": "owner", "status": "active"}, EvidenceRefs: []contextgraph.Ref{{Kind: "evidence", ID: "evidence-person"}}},
			{ID: "agent:jordan", ObjectType: "agent", Label: "Jordan", Properties: map[string]any{"runtime_kind": "pi", "status": "active"}},
		},
		Facts: []contextgraph.Fact{
			{ID: "fact-conflict", Subject: contextgraph.Ref{Kind: "customer", ID: "customer-1"}, Predicate: "decision_conflict", Value: true, EvidenceRefs: []contextgraph.Ref{{Kind: "evidence", ID: "evidence-conflict"}}},
		},
		Evidence: []contextgraph.Evidence{{ID: "evidence-direct"}},
	}}
	provider := productionHILContextProvider{graph: graph}
	snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID: "tenant-1",
		Actor:    hil.ActorRef{Kind: hil.ActorAgent, ID: "requester"},
		Purpose:  "review change",
		Subjects: []hil.ObjectRef{{Kind: "customer", ID: "customer-1"}},
		Query:    "decision stakeholders",
	})
	if err != nil {
		t.Fatalf("BuildDecisionContext: %v", err)
	}
	if graph.request.Consumer != "orrery-hil" || graph.request.Purpose != "decision-control" || graph.request.ActorRef.ID != "requester" || graph.request.MaxDepth != 2 {
		t.Fatalf("context request = %+v", graph.request)
	}
	if snapshot.BundleID != "bundle-1" || snapshot.SchemaHash != "schema-1" || !snapshot.Complete || !snapshot.Conflict {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(snapshot.RecommendedParticipants) != 2 {
		t.Fatalf("participants = %+v", snapshot.RecommendedParticipants)
	}
	if got := snapshot.RecommendedParticipants[0]; got.Actor != (hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan"}) || got.DisplayName != "Jordan" {
		t.Fatalf("first participant = %+v", got)
	}
	if got := snapshot.RecommendedParticipants[1]; got.Actor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "ding"}) || !got.Required || got.Role != "owner" {
		t.Fatalf("required human = %+v", got)
	}
	for _, want := range []string{"evidence-conflict", "evidence-direct", "evidence-person"} {
		if !containsHILString(snapshot.EvidenceRefs, want) {
			t.Fatalf("evidence refs = %+v, missing %q", snapshot.EvidenceRefs, want)
		}
	}
}

func TestProductionHILContextProviderMarksMissingSubjectsPartial(t *testing.T) {
	provider := productionHILContextProvider{graph: &recordingHILContextGraph{bundle: contextgraph.ContextBundle{BundleID: "bundle-partial"}}}
	snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID: "tenant-1", Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "ding"}, Purpose: "review", Subjects: []hil.ObjectRef{{Kind: "customer", ID: "missing"}},
	})
	if err != nil {
		t.Fatalf("BuildDecisionContext: %v", err)
	}
	if snapshot.Complete {
		t.Fatalf("snapshot = %+v, want partial", snapshot)
	}
}

func TestProductionHILContextProviderSkipsGraphForForcedMarketingWechatDraftGate(t *testing.T) {
	for _, test := range []struct {
		name         string
		action       string
		actionTypeID string
	}{
		{name: "legacy runtime approval", action: "runtime.approval", actionTypeID: "tool_use"},
		{name: "canonical runtime approval", action: "communication.wechat_draft.create", actionTypeID: "communication.wechat_draft.create"},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := &recordingHILContextGraph{}
			provider := productionHILContextProvider{graph: graph}
			snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
				TenantID:     "system",
				Actor:        hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan-belfort"},
				Action:       test.action,
				ActionTypeID: test.actionTypeID,
				Purpose:      "create WeChat draft",
				Subjects:     []hil.ObjectRef{{Kind: "agent", ID: "jordan-belfort"}},
				PayloadPreview: map[string]any{
					"approval_assigned_to": "human-ding",
					"approval_tool_name":   "mcp_marketing_marketing_wechat_draft_create",
				},
			})
			if err != nil {
				t.Fatalf("BuildDecisionContext: %v", err)
			}
			if graph.calls != 0 {
				t.Fatalf("context graph calls = %d, want 0", graph.calls)
			}
			if snapshot.BundleID != hilContextManualGateFallbackBundleID || snapshot.Complete {
				t.Fatalf("manual gate snapshot = %+v", snapshot)
			}
			route := hil.DecideRoute(hil.RouteInput{
				SnapshotValid:   true,
				ContextComplete: snapshot.Complete,
				RiskLevel:       hil.RiskLow,
				HILRequired:     true,
			})
			if route.Mode != hil.ModeHIL {
				t.Fatalf("manual gate route = %+v, want HIL", route)
			}
		})
	}
}

func TestProductionHILContextProviderSkipsGraphForFormalMarketingWechatDraftApproval(t *testing.T) {
	graph := &recordingHILContextGraph{err: errors.New("generation is not projected yet")}
	provider := productionHILContextProvider{graph: graph}
	snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID:     "system",
		Actor:        hil.ActorRef{Kind: hil.ActorService, ID: "marketing"},
		Action:       hil.MarketingWechatDraftApprovalActionType,
		ActionTypeID: hil.MarketingWechatDraftApprovalActionType,
		Purpose:      "approve frozen Marketing WeChat generation",
		Subjects:     []hil.ObjectRef{{Kind: hil.MarketingWechatGenerationResourceKind, ID: "scheduled-run-1"}},
	})
	if err != nil {
		t.Fatalf("BuildDecisionContext: %v", err)
	}
	if graph.calls != 0 || snapshot.BundleID != hilContextManualGateFallbackBundleID || snapshot.Complete {
		t.Fatalf("context graph calls = %d, snapshot = %+v", graph.calls, snapshot)
	}
	route := hil.DecideRoute(hil.RouteInput{
		SnapshotValid: true, ContextComplete: snapshot.Complete, RiskLevel: hil.RiskHigh, ApprovalRequired: true,
	})
	if route.Mode != hil.ModeApproval {
		t.Fatalf("formal Marketing route = %+v, want approval", route)
	}
}

func TestProductionHILContextProviderDoesNotSkipGraphForUnrecognizedWechatAction(t *testing.T) {
	graph := &recordingHILContextGraph{bundle: contextgraph.ContextBundle{BundleID: "bundle-regular"}}
	provider := productionHILContextProvider{graph: graph}
	snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID:     "system",
		Actor:        hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan-belfort"},
		Action:       "customer.update",
		ActionTypeID: "tool_use",
		Purpose:      "unrelated action",
		PayloadPreview: map[string]any{
			"approval_tool_name": "mcp_marketing_marketing_wechat_draft_create",
		},
	})
	if err != nil {
		t.Fatalf("BuildDecisionContext: %v", err)
	}
	if graph.calls != 1 || snapshot.BundleID != "bundle-regular" {
		t.Fatalf("context graph calls = %d, snapshot = %+v", graph.calls, snapshot)
	}
}

func TestProductionHILContextProviderFallsBackToIncompleteContextWhenGraphRunsOutOfSpace(t *testing.T) {
	provider := productionHILContextProvider{graph: &recordingHILContextGraph{err: errors.New(`list context graph temporal resource versions: ERROR: could not write to file "base/pgsql_tmp/pgsql_tmp123.4": No space left on device (SQLSTATE 53100)`)}}
	snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID: "tenant-1", Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan"}, Purpose: "decision-control", Subjects: []hil.ObjectRef{{Kind: "conversation", ID: "conversation-1"}},
	})
	if err != nil {
		t.Fatalf("BuildDecisionContext: %v", err)
	}
	if snapshot.BundleID != "context-unavailable:capacity" || snapshot.Complete || snapshot.Confidence != 0 {
		t.Fatalf("capacity fallback snapshot = %+v", snapshot)
	}
	route := hil.DecideRoute(hil.RouteInput{SnapshotValid: true, ContextComplete: snapshot.Complete, RiskLevel: hil.RiskLow})
	if route.Mode != hil.ModeHIL {
		t.Fatalf("capacity fallback route = %+v, want HIL", route)
	}
}

func TestProductionHILContextProviderFallsBackWhenGraphExceedsCapacityTimeout(t *testing.T) {
	provider := productionHILContextProvider{
		graph:   &recordingHILContextGraph{waitForContext: true},
		timeout: 10 * time.Millisecond,
	}
	snapshot, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID: "tenant-1", Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan"}, Purpose: "decision-control", Subjects: []hil.ObjectRef{{Kind: "conversation", ID: "conversation-1"}},
	})
	if err != nil {
		t.Fatalf("BuildDecisionContext: %v", err)
	}
	if snapshot.BundleID != hilContextCapacityFallbackBundleID || snapshot.Complete {
		t.Fatalf("capacity timeout fallback snapshot = %+v", snapshot)
	}
}

func TestProductionHILContextProviderPropagatesNonCapacityGraphErrors(t *testing.T) {
	graphErr := errors.New("context graph database timeout")
	provider := productionHILContextProvider{graph: &recordingHILContextGraph{err: graphErr}}
	_, err := provider.BuildDecisionContext(context.Background(), hil.DecisionContextRequest{
		TenantID: "tenant-1", Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan"}, Purpose: "decision-control", Subjects: []hil.ObjectRef{{Kind: "conversation", ID: "conversation-1"}},
	})
	if !errors.Is(err, graphErr) {
		t.Fatalf("BuildDecisionContext error = %v, want graph error", err)
	}
}

type recordingHILContextGraph struct {
	calls          int
	request        contextgraph.BuildContextBundleRequest
	bundle         contextgraph.ContextBundle
	err            error
	waitForContext bool
}

func (g *recordingHILContextGraph) BuildContextBundle(ctx context.Context, request contextgraph.BuildContextBundleRequest) (contextgraph.ContextBundle, error) {
	g.calls++
	g.request = request
	if g.waitForContext {
		<-ctx.Done()
		return contextgraph.ContextBundle{}, ctx.Err()
	}
	return g.bundle, g.err
}

func containsHILString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
