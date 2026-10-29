package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/operations"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgetestkit "orrery_v2/internal/knowledge/testkit"
	doccore "orrery_v2/internal/platform/document/core"
)

type fakeKnowledgeOntologyBindings struct{ items []knowledgeontology.Binding }

func (f fakeKnowledgeOntologyBindings) ListEnabledBindings(context.Context) ([]knowledgeontology.Binding, error) {
	return f.items, nil
}

type fakeKnowledgeOntologyOwnedBindings struct {
	items      []knowledgeontology.Binding
	reconciled []knowledgeontology.Binding
	failures   map[string]error
}

func (f *fakeKnowledgeOntologyOwnedBindings) ListEnabledBindings(context.Context) ([]knowledgeontology.Binding, error) {
	return append([]knowledgeontology.Binding(nil), f.items...), nil
}

func (f *fakeKnowledgeOntologyOwnedBindings) ReconcileOwnedBinding(_ context.Context, binding knowledgeontology.Binding) error {
	if err := f.failures[binding.KnowledgeBaseID]; err != nil {
		return err
	}
	f.reconciled = append(f.reconciled, binding)
	for index := range f.items {
		if f.items[index].KnowledgeBaseID == binding.KnowledgeBaseID {
			f.items[index] = binding
			return nil
		}
	}
	f.items = append(f.items, binding)
	return nil
}

type fakeKnowledgeOntologyOrganizationCatalog struct {
	graph operations.OrgGraphDTO
	err   error
	calls int
}

func (f *fakeKnowledgeOntologyOrganizationCatalog) ListOrganizations(context.Context, operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	f.calls++
	return f.graph, f.err
}

type fakeKnowledgeOntologyRunner struct {
	mu          sync.Mutex
	failures    map[string]int
	calls       map[string]int
	active      int
	maxActive   int
	concurrency chan struct{}
}

type fakeKnowledgeOntologyCatalogProjector struct {
	mu       sync.Mutex
	calls    map[string]int
	failures map[string]error
}

func (p *fakeKnowledgeOntologyCatalogProjector) EnsureKnowledgeBaseCatalog(_ context.Context, binding knowledgeontology.Binding) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[binding.KnowledgeBaseID]++
	return p.failures[binding.KnowledgeBaseID]
}

func (r *fakeKnowledgeOntologyRunner) Run(_ context.Context, binding knowledgeontology.Binding, _ int) (knowledgeontology.RunReport, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.calls[binding.KnowledgeBaseID]++
	remaining := r.failures[binding.KnowledgeBaseID]
	if remaining > 0 {
		r.failures[binding.KnowledgeBaseID] = remaining - 1
	}
	r.mu.Unlock()
	if r.concurrency != nil {
		r.concurrency <- struct{}{}
		<-r.concurrency
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	if remaining > 0 {
		return knowledgeontology.RunReport{}, errors.New("PageIndex unavailable")
	}
	return knowledgeontology.RunReport{}, nil
}

func TestKnowledgeOntologySweeperRetriesFailedBindingWithoutBlockingOthers(t *testing.T) {
	now := time.Date(2026, 7, 24, 5, 0, 0, 0, time.UTC)
	runner := &fakeKnowledgeOntologyRunner{failures: map[string]int{"kb-1": 1}, calls: map[string]int{}}
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: fakeKnowledgeOntologyBindings{items: []knowledgeontology.Binding{
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-1"},
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-2"},
		}},
		newRunner: func() (knowledgeOntologyRunner, error) { return runner, nil },
		now:       func() time.Time { return now },
		retries:   map[string]knowledgeOntologyRetry{},
	}

	if err := sweeper.Sweep(context.Background()); err == nil {
		t.Fatal("first Sweep error = nil")
	}
	if runner.calls["kb-1"] != 1 || runner.calls["kb-2"] != 1 {
		t.Fatalf("first calls = %+v", runner.calls)
	}
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep during backoff: %v", err)
	}
	if runner.calls["kb-1"] != 1 || runner.calls["kb-2"] != 2 {
		t.Fatalf("backoff calls = %+v", runner.calls)
	}
	now = now.Add(time.Minute)
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("retry Sweep: %v", err)
	}
	if runner.calls["kb-1"] != 2 || runner.calls["kb-2"] != 3 {
		t.Fatalf("retry calls = %+v", runner.calls)
	}
}

func TestKnowledgeOntologySweeperDoesNotRequireProviderWithoutBindings(t *testing.T) {
	called := false
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: fakeKnowledgeOntologyBindings{},
		newRunner: func() (knowledgeOntologyRunner, error) {
			called = true
			return nil, errors.New("provider is not configured")
		},
	}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if called {
		t.Fatal("runner factory called without enabled bindings")
	}
}

func TestKnowledgeOntologySweeperRepairsCatalogBeforeRunningBinding(t *testing.T) {
	runner := &fakeKnowledgeOntologyRunner{failures: map[string]int{}, calls: map[string]int{}}
	projector := &fakeKnowledgeOntologyCatalogProjector{
		calls: map[string]int{}, failures: map[string]error{"kb-broken": errors.New("catalog projection unavailable")},
	}
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: fakeKnowledgeOntologyBindings{items: []knowledgeontology.Binding{
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-broken"},
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-valid"},
		}},
		catalog:   projector,
		newRunner: func() (knowledgeOntologyRunner, error) { return runner, nil },
		retries:   map[string]knowledgeOntologyRetry{},
	}

	err := sweeper.Sweep(context.Background())
	if err == nil || !strings.Contains(err.Error(), "catalog projection unavailable") {
		t.Fatalf("Sweep error = %v", err)
	}
	if projector.calls["kb-broken"] != 1 || projector.calls["kb-valid"] != 1 {
		t.Fatalf("catalog projection calls = %+v", projector.calls)
	}
	if runner.calls["kb-broken"] != 0 || runner.calls["kb-valid"] != 1 {
		t.Fatalf("runner calls = %+v", runner.calls)
	}
}

func TestKnowledgeOntologySweeperProcessesBindingsInBoundedRoundRobinBatches(t *testing.T) {
	runner := &fakeKnowledgeOntologyRunner{failures: map[string]int{}, calls: map[string]int{}}
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: fakeKnowledgeOntologyBindings{items: []knowledgeontology.Binding{
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-4"},
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-2"},
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-5"},
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-1"},
			{TenantID: "tenant-1", KnowledgeBaseID: "kb-3"},
		}},
		newRunner: func() (knowledgeOntologyRunner, error) { return runner, nil },
		retries:   map[string]knowledgeOntologyRetry{},
	}

	wantCalls := []map[string]int{
		{"kb-1": 1, "kb-2": 1},
		{"kb-1": 1, "kb-2": 1, "kb-3": 1, "kb-4": 1},
		{"kb-1": 2, "kb-2": 1, "kb-3": 1, "kb-4": 1, "kb-5": 1},
	}
	for index, want := range wantCalls {
		if err := sweeper.Sweep(context.Background()); err != nil {
			t.Fatalf("Sweep %d: %v", index+1, err)
		}
		if len(runner.calls) != len(want) {
			t.Fatalf("Sweep %d calls = %+v, want %+v", index+1, runner.calls, want)
		}
		for baseID, count := range want {
			if runner.calls[baseID] != count {
				t.Fatalf("Sweep %d calls = %+v, want %+v", index+1, runner.calls, want)
			}
		}
	}
}

func TestKnowledgeOntologySweeperThrottlesOwnedBindingReconciliation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)
	repo := knowledgetestkit.NewFakeRepository()
	base := knowledgeOntologyTestBase("kb-owned", knowledgecore.OwnerOrganization, "minvia")
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("SaveKnowledgeBase: %v", err)
	}
	bindings := &fakeKnowledgeOntologyOwnedBindings{}
	runner := &fakeKnowledgeOntologyRunner{failures: map[string]int{}, calls: map[string]int{}}
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: bindings, repo: repo,
		newRunner: func() (knowledgeOntologyRunner, error) { return runner, nil },
		now:       func() time.Time { return now },
	}

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	now = now.Add(30 * time.Second)
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if len(bindings.reconciled) != 1 {
		t.Fatalf("reconciled before interval = %+v", bindings.reconciled)
	}
	now = now.Add(productionKnowledgeOntologyBindingReconcileInterval)
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("third Sweep: %v", err)
	}
	if len(bindings.reconciled) != 2 {
		t.Fatalf("reconciled after interval = %+v", bindings.reconciled)
	}
}

func TestProductionPageIndexKnowledgeBaseRecognizesExternalPageIndexBinding(t *testing.T) {
	for _, test := range []struct {
		name string
		base knowledgecore.KnowledgeBase
		want bool
	}{
		{name: "native", base: knowledgecore.KnowledgeBase{Provider: knowledgecore.ProviderPageIndex}, want: true},
		{name: "external PageIndex", base: knowledgecore.KnowledgeBase{Provider: knowledgecore.ProviderMCP, ProviderMetadata: map[string]string{"binding_id": "kprovider-pageindex-minvia"}}, want: true},
		{name: "unrelated MCP", base: knowledgecore.KnowledgeBase{Provider: knowledgecore.ProviderMCP, ProviderMetadata: map[string]string{"binding_id": "kprovider-sharepoint"}}, want: false},
		{name: "local", base: knowledgecore.KnowledgeBase{Provider: knowledgecore.ProviderLocal}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isProductionPageIndexKnowledgeBase(test.base); got != test.want {
				t.Fatalf("isProductionPageIndexKnowledgeBase() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestReconcileProductionKnowledgeOntologyBindingsIncludesPositionOwners(t *testing.T) {
	ctx := context.Background()
	repo := knowledgetestkit.NewFakeRepository()
	for _, base := range []knowledgecore.KnowledgeBase{
		knowledgeOntologyTestBase("kb-organization", knowledgecore.OwnerOrganization, "minvia"),
		knowledgeOntologyTestBase("kb-position", knowledgecore.OwnerPosition, "position-marketing"),
		knowledgeOntologyTestBase("kb-agent", knowledgecore.OwnerAgent, "agent-marketing"),
	} {
		if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
			t.Fatalf("SaveKnowledgeBase(%s): %v", base.ID, err)
		}
	}
	catalog := &fakeKnowledgeOntologyOrganizationCatalog{graph: operations.OrgGraphDTO{
		Organizations: []operations.OrganizationDTO{{OrganizationID: "minvia"}},
		Departments:   []operations.DepartmentDTO{{DepartmentID: "department-growth", OrganizationID: "minvia"}},
		Positions:     []operations.PositionDTO{{PositionID: "position-marketing", DepartmentID: "department-growth", KnowledgeBaseID: "kb-position"}},
	}}
	bindings := &fakeKnowledgeOntologyOwnedBindings{}

	if err := reconcileProductionKnowledgeOntologyBindings(ctx, repo, bindings, catalog); err != nil {
		t.Fatalf("reconcileProductionKnowledgeOntologyBindings: %v", err)
	}
	if catalog.calls != 1 {
		t.Fatalf("organization catalog calls = %d, want 1", catalog.calls)
	}
	if len(bindings.reconciled) != 2 {
		t.Fatalf("reconciled bindings = %+v", bindings.reconciled)
	}
	byBase := map[string]knowledgeontology.Binding{}
	for _, binding := range bindings.reconciled {
		byBase[binding.KnowledgeBaseID] = binding
	}
	wantAccessByBase := map[string]contextgraph.AccessRef{
		"kb-organization": {Kind: "organization", ID: "minvia", TenantID: "minvia"},
		"kb-position":     {Kind: "knowledge_base", ID: "kb-position", TenantID: "position-marketing"},
	}
	for _, baseID := range []string{"kb-organization", "kb-position"} {
		binding, ok := byBase[baseID]
		if !ok {
			t.Fatalf("binding for %s is missing: %+v", baseID, bindings.reconciled)
		}
		if binding.TenantID != "minvia" || binding.FolderID != "folder-"+baseID || !binding.Enabled || binding.SourceAccessRef != wantAccessByBase[baseID] {
			t.Fatalf("binding for %s = %+v", baseID, binding)
		}
	}
	if _, ok := byBase["kb-agent"]; ok {
		t.Fatalf("unsupported agent-owned knowledge base was reconciled: %+v", bindings.reconciled)
	}
}

func TestReconcileProductionKnowledgeOntologyBindingsReportsBrokenPositionOwnershipAfterReconcilingValidBases(t *testing.T) {
	ctx := context.Background()
	repo := knowledgetestkit.NewFakeRepository()
	for _, base := range []knowledgecore.KnowledgeBase{
		knowledgeOntologyTestBase("kb-valid", knowledgecore.OwnerOrganization, "minvia"),
		knowledgeOntologyTestBase("kb-broken-position", knowledgecore.OwnerPosition, "position-missing"),
	} {
		if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
			t.Fatalf("SaveKnowledgeBase(%s): %v", base.ID, err)
		}
	}
	bindings := &fakeKnowledgeOntologyOwnedBindings{}
	catalog := &fakeKnowledgeOntologyOrganizationCatalog{graph: operations.OrgGraphDTO{
		Organizations: []operations.OrganizationDTO{{OrganizationID: "minvia"}},
	}}

	err := reconcileProductionKnowledgeOntologyBindings(ctx, repo, bindings, catalog)
	if err == nil || !strings.Contains(err.Error(), "position-missing") {
		t.Fatalf("reconciliation error = %v", err)
	}
	if len(bindings.reconciled) != 1 || bindings.reconciled[0].KnowledgeBaseID != "kb-valid" {
		t.Fatalf("valid bindings were not reconciled: %+v", bindings.reconciled)
	}
}

func TestKnowledgeOntologySweeperOwnershipFailureDoesNotBlockExistingBindings(t *testing.T) {
	ctx := context.Background()
	repo := knowledgetestkit.NewFakeRepository()
	broken := knowledgeOntologyTestBase("kb-broken-position", knowledgecore.OwnerPosition, "position-missing")
	if err := repo.SaveKnowledgeBase(ctx, broken); err != nil {
		t.Fatalf("SaveKnowledgeBase: %v", err)
	}
	bindings := &fakeKnowledgeOntologyOwnedBindings{items: []knowledgeontology.Binding{{
		TenantID: "minvia", KnowledgeBaseID: "kb-existing", FolderID: "folder-existing", Enabled: true,
	}}}
	runner := &fakeKnowledgeOntologyRunner{failures: map[string]int{}, calls: map[string]int{}}
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: bindings, repo: repo,
		organizations: &fakeKnowledgeOntologyOrganizationCatalog{},
		newRunner:     func() (knowledgeOntologyRunner, error) { return runner, nil },
	}

	err := sweeper.Sweep(ctx)
	if err == nil || !strings.Contains(err.Error(), "position-missing") {
		t.Fatalf("Sweep error = %v", err)
	}
	if runner.calls["kb-existing"] != 1 {
		t.Fatalf("existing binding calls = %+v", runner.calls)
	}
}

func knowledgeOntologyTestBase(id string, ownerType knowledgecore.OwnerType, ownerID string) knowledgecore.KnowledgeBase {
	return knowledgecore.KnowledgeBase{
		ID: knowledgecore.KnowledgeBaseID(id), Name: id, OwnerType: ownerType, OwnerID: ownerID,
		Visibility: knowledgecore.VisibilityScoped, Provider: knowledgecore.ProviderPageIndex,
		ProviderResourceID: "folder-" + id,
		ChunkPolicy:        doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:             knowledgecore.KnowledgeActive,
	}
}
