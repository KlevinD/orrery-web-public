package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func TestProductionEnterpriseGraphSearchCursorBindsQuery(t *testing.T) {
	now := time.Date(2026, 8, 22, 3, 0, 0, 0, time.UTC)
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: "test", Keys: []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{7}, 32)}},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	cursors := productionEnterpriseGraphSearchCursor{codec: codec}
	input := contextconsumer.SearchObjectsInput{
		TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"},
		Query: "mission", IncludeKinds: []string{"mission"}, Limit: 20,
		PrincipalKey: "agent-1", AuthorizationRevision: "auth-1", PolicyRevision: "policy-1",
	}
	token, err := cursors.EncodeSearchCursor(input, contextgraph.ObjectSeek{UpdatedAt: now, ID: "mission-1"})
	if err != nil {
		t.Fatal(err)
	}
	input.Cursor = token
	seek, err := cursors.DecodeSearchCursor(input)
	if err != nil || seek.ID != "mission-1" || !seek.UpdatedAt.Equal(now) {
		t.Fatalf("seek=%+v err=%v", seek, err)
	}
	input.Query = "different"
	if _, err := cursors.DecodeSearchCursor(input); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("query-mismatched cursor error = %v", err)
	}
}

func TestProductionEnterpriseGraphRuntimeBoundsConcurrency(t *testing.T) {
	limiter, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := &blockingEnterpriseGraphRuntime{entered: make(chan string, 5), release: make(chan struct{})}
	runtime := &productionEnterpriseGraphRuntime{
		bundles: inner, search: inner, limiter: limiter, snapshots: derivedEnterpriseGraphSnapshots{},
		timeout: time.Second, now: time.Now,
	}
	var group sync.WaitGroup
	errorsFound := make(chan error, 4)
	for index := range 4 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, err := runtime.BuildContextBundle(context.Background(), enterpriseGraphRuntimeTestInput(index))
			errorsFound <- err
		}(index)
	}
	for range 4 {
		select {
		case <-inner.entered:
		case <-time.After(time.Second):
			t.Fatal("four bounded requests did not enter")
		}
	}
	if _, err := runtime.BuildContextBundle(context.Background(), enterpriseGraphRuntimeTestInput(5)); !errors.Is(err, ontologycontract.ErrRateLimited) {
		t.Fatalf("fifth request error = %v, want rate limited", err)
	}
	select {
	case value := <-inner.entered:
		t.Fatalf("fifth request reached inner runtime: %s", value)
	default:
	}
	close(inner.release)
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Errorf("bounded request error: %v", err)
		}
	}
}

func TestProductionEnterpriseGraphRuntimeAppliesDeadlineAndBoundedMetric(t *testing.T) {
	limiter, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := &blockingEnterpriseGraphRuntime{entered: make(chan string, 1), release: make(chan struct{})}
	metrics := &capturingOntologyMetricRecorder{}
	now := time.Date(2026, 8, 22, 5, 0, 0, 0, time.UTC)
	runtime := &productionEnterpriseGraphRuntime{
		bundles: inner, search: inner, limiter: limiter, snapshots: derivedEnterpriseGraphSnapshots{},
		metrics: metrics, timeout: 20 * time.Millisecond, now: func() time.Time { return now },
	}
	_, err = runtime.BuildContextBundle(context.Background(), enterpriseGraphRuntimeTestInput(1))
	if !errors.Is(err, contextgraph.ErrQueryBudgetExceeded) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	metric, found := metrics.last("ontology.context_bundle")
	if !found {
		t.Fatal("context bundle metric missing")
	}
	allowed := map[string]bool{
		"operation": true, "status": true, "objects": true, "links": true, "facts": true,
		"evidence": true, "schema_definitions": true, "response_bytes": true,
		"estimated_tokens": true, "depth": true, "duration_ms": true,
	}
	if len(metric.fields) != len(allowed) {
		t.Fatalf("metric fields = %+v", metric.fields)
	}
	for field := range metric.fields {
		if !allowed[field] {
			t.Fatalf("unbounded metric field %q", field)
		}
	}
	if metric.fields["status"] != "budget" || metric.fields["operation"] != "bundle" {
		t.Fatalf("metric = %+v", metric.fields)
	}
}

func TestProductionEnterpriseGraphRuntimeDeadlineIncludesAuthorizationSnapshot(t *testing.T) {
	limiter, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := &blockingEnterpriseGraphRuntime{entered: make(chan string, 1), release: make(chan struct{})}
	runtime := &productionEnterpriseGraphRuntime{
		bundles: inner, search: inner, limiter: limiter, snapshots: blockingEnterpriseGraphSnapshots{},
		timeout: 20 * time.Millisecond, now: time.Now,
	}
	_, err = runtime.BuildContextBundle(context.Background(), enterpriseGraphRuntimeTestInput(1))
	if !errors.Is(err, contextgraph.ErrQueryBudgetExceeded) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("authorization deadline error = %v", err)
	}
	select {
	case actor := <-inner.entered:
		t.Fatalf("authorization timeout reached graph runtime for %q", actor)
	default:
	}
}

func TestProductionEnterpriseGraphRuntimeRejectsInvalidInputBeforeAuthorizationSnapshot(t *testing.T) {
	limiter, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := &recordingEnterpriseGraphSnapshots{}
	release := make(chan struct{})
	close(release)
	inner := &blockingEnterpriseGraphRuntime{entered: make(chan string, 2), release: release}
	runtime := &productionEnterpriseGraphRuntime{
		bundles: inner, search: inner, limiter: limiter, snapshots: snapshots,
		timeout: time.Second, now: time.Now,
	}
	invalidBundle := enterpriseGraphRuntimeTestInput(1)
	invalidBundle.SubjectRefs = nil
	if _, err := runtime.BuildContextBundle(context.Background(), invalidBundle); !errors.Is(err, contextgraph.ErrQueryInvalid) {
		t.Fatalf("invalid bundle error = %v", err)
	}
	if _, err := runtime.SearchObjects(context.Background(), contextconsumer.SearchObjectsInput{
		TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Limit: 20,
	}); !errors.Is(err, contextgraph.ErrQueryInvalid) {
		t.Fatalf("invalid search error = %v", err)
	}
	if snapshots.calls != 0 {
		t.Fatalf("authorization snapshot calls = %d, want 0", snapshots.calls)
	}
	select {
	case actor := <-inner.entered:
		t.Fatalf("invalid request reached graph runtime for %q", actor)
	default:
	}
}

func enterpriseGraphRuntimeTestInput(index int) contextconsumer.BuildContextBundleInput {
	id := fmt.Sprintf("agent-%d", index)
	return contextconsumer.BuildContextBundleInput{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "test",
		ActorRef: contextgraph.Ref{Kind: "agent", ID: id}, SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
		TokenBudget: 8000,
	}
}

type blockingEnterpriseGraphRuntime struct {
	entered chan string
	release chan struct{}
}

func (r *blockingEnterpriseGraphRuntime) BuildContextBundle(ctx context.Context, input contextconsumer.BuildContextBundleInput) (contextgraph.ContextBundle, error) {
	r.entered <- input.ActorRef.ID
	select {
	case <-r.release:
		return contextgraph.ContextBundle{BundleID: "bundle"}, nil
	case <-ctx.Done():
		return contextgraph.ContextBundle{}, ctx.Err()
	}
}

func (r *blockingEnterpriseGraphRuntime) SearchObjects(ctx context.Context, input contextconsumer.SearchObjectsInput) (contextconsumer.SearchObjectsResult, error) {
	r.entered <- input.ActorRef.ID
	select {
	case <-r.release:
		return contextconsumer.SearchObjectsResult{}, nil
	case <-ctx.Done():
		return contextconsumer.SearchObjectsResult{}, ctx.Err()
	}
}

type derivedEnterpriseGraphSnapshots struct{}

func (derivedEnterpriseGraphSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: principal.AgentProfileID, AuthorizationRevision: "auth-1", PolicyRevision: "policy-1", CapturedAt: time.Now().UTC(),
	}, nil
}

type blockingEnterpriseGraphSnapshots struct{}

func (blockingEnterpriseGraphSnapshots) Capture(ctx context.Context, _ identity.Principal) (authorization.AuthorizationSnapshot, error) {
	<-ctx.Done()
	return authorization.AuthorizationSnapshot{}, ctx.Err()
}

type recordingEnterpriseGraphSnapshots struct {
	calls int
}

func (s *recordingEnterpriseGraphSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	s.calls++
	return authorization.AuthorizationSnapshot{
		PrincipalKey: principal.AgentProfileID, AuthorizationRevision: "auth-1", PolicyRevision: "policy-1", CapturedAt: time.Now().UTC(),
	}, nil
}
