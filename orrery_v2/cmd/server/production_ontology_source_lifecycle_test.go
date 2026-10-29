package main

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform/db"
)

type lifecycleFeed struct {
	provider.RevisionProvider
	change provider.KnowledgeChange
	err    error
}

func (f *lifecycleFeed) ListKnowledgeChanges(context.Context, provider.KnowledgeChangesRequest) (provider.KnowledgeChangePage, error) {
	return provider.KnowledgeChangePage{Items: []provider.KnowledgeChange{f.change}}, f.err
}

func seedLifecycleSource(t *testing.T, bridge *knowledgeontology.PlatformStore, binding knowledgeontology.Binding, source knowledgeinstances.Source, sequence string) {
	t.Helper()
	p := knowledgeontology.Projection{TenantID: source.TenantID, KnowledgeBaseID: source.KnowledgeBaseID, DocumentID: source.DocumentID, KnowledgeDocumentObjectID: source.RootID, Revision: source.Revision, ContentHash: source.ContentHash}
	if err := bridge.SaveProjectionAndCursor(context.Background(), binding, p, sequence); err != nil {
		t.Fatal(err)
	}
}

func TestProductionOntologySourceLifecycleRetractsThroughModelAndRecovers(t *testing.T) {
	w, _, sources, model, a, b := conflictDocumentFixture(t)
	ctx := context.Background()
	core := w.store.Core
	model.selectRoot = a.RootID
	if err := w.Process(ctx, documentJob(b, "accepted-conflict")); err != nil {
		t.Fatal(err)
	}
	bridge := knowledgeontology.NewPlatformStore(core).WithSourceLifecycle(productionSourceWithdrawal(core))
	binding := knowledgeontology.Binding{TenantID: a.TenantID, KnowledgeBaseID: "kb", FolderID: "folder", SourceAccessRef: sources.sources[a.RootID].Access, Enabled: true}
	if err := bridge.SaveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	seedLifecycleSource(t, bridge, binding, sources.sources[a.RootID], "10")
	seedLifecycleSource(t, bridge, binding, sources.sources[b.RootID], "11")
	entities, _ := w.store.Candidates(ctx, a.TenantID, binding.SourceAccessRef)
	id := entities[0].ID
	definition, _ := w.api.registry.GetLinkType(ctx, "workflow_derived")
	definition.FromObjectType, definition.ToObjectType = "knowledge_document", "knowledge_entity"
	if err := ontology.NewPlatformRegistry(core).SaveLinkType(ctx, definition); err != nil {
		t.Fatal(err)
	}
	w.api.ruleSets = &ontologyreasoning.RuleSetStore{Store: core}
	p, _ := w.api.continuousInferencePrincipal(ctx, a.TenantID, a.AgentID)
	rule := ontologyreasoning.Rule{ID: "country-cn", Version: "1", When: []ontologyreasoning.Atom{
		{Kind: "link", Type: knowledgeinstances.DocumentLink, Subject: "?doc", Object: "?entity"},
		{Kind: "fact", Subject: "?entity", Predicate: "country", Value: "CN"},
		{Kind: "fact", Subject: "?doc", Predicate: "provider_document_id", Value: "provider-doc"},
	}, Then: ontologyreasoning.Atom{Kind: "link", Type: "workflow_derived", Subject: "?doc", Object: "?entity"}}
	sets, err := w.api.InferenceRules(ctx, p, ontologyreasoning.RuleSetRequest{Operation: "publish", TenantID: a.TenantID, ID: "source-lifecycle", RootID: id, Rules: []ontologyreasoning.Rule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	judge := &continuousInferenceJudge{}
	continuous := &productionContinuousInference{api: w.api, judge: judge}
	decide := func(calls int, support bool, effect string) {
		t.Helper()
		if err := continuous.Evaluate(ctx, continuousInferenceEvaluationJob(sets[0])); err != nil {
			t.Fatal(err)
		}
		jobs := continuousInferenceJobs(t, continuous, ontologyreasoning.InferenceDecisionTopic)
		if len(jobs) != calls {
			t.Fatalf("model decision tasks %d want %d", len(jobs), calls)
		}
		if err := continuous.Decide(ctx, jobs[len(jobs)-1]); err != nil {
			t.Fatal(err)
		}
		input := judge.inputs[len(judge.inputs)-1]
		if judge.calls != calls || input.CurrentSupport != support || input.Preview.Effect != effect {
			t.Fatalf("model lifecycle: %+v", input)
		}
	}
	decide(1, true, "assert")
	feed := &lifecycleFeed{change: provider.KnowledgeChange{Sequence: 12, FolderID: "folder", DocumentID: "provider-doc", EventType: "document.deleted"}}
	runner := knowledgeontology.NewRunner(feed, contextgraph.NewPlatformStore(core), bridge, nil)
	if _, err := runner.Run(ctx, binding, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, binding, 20); err != nil {
		t.Fatal(err)
	}
	entities, _ = w.store.Candidates(ctx, a.TenantID, binding.SourceAccessRef)
	if entities[0].Status != "active" || entities[0].Properties["country"] != "US" || len(entities[0].Resolutions) != 0 {
		t.Fatalf("independent support lost/stale resolution survived: %+v", entities)
	}
	var receipts int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_document_withdrawals`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("duplicate source event receipts=%d: %v", receipts, err)
	}
	jobs := continuousInferenceJobs(t, continuous, ontologyreasoning.InferenceEvaluationTopic)
	if len(jobs) != 2 {
		t.Fatalf("withdrawal failed to atomically queue reevaluation: %d", len(jobs))
	}
	decide(2, false, "retract")
	feed.change.DocumentID = "other-provider-doc"
	feed.change.Sequence = 13
	feed.change.EventType = "document.unpublished"
	if _, err := runner.Run(ctx, binding, 20); err != nil {
		t.Fatal(err)
	}
	entities, _ = w.store.Candidates(ctx, a.TenantID, binding.SourceAccessRef)
	if entities[0].Status != "withdrawn" {
		t.Fatal("final source loss did not withdraw identity")
	}
	// Republish the exact same revision/hash. A new generation invalidates all
	// old model plans, while existing identity and immutable receipts survive.
	seedLifecycleSource(t, bridge, binding, sources.sources[a.RootID], "20")
	head, err := bridge.SourceHead(ctx, binding, "provider-doc")
	if err != nil || head.Generation != 2 {
		t.Fatalf("republish generation %+v %v", head, err)
	}
	source := sources.sources[a.RootID]
	source.Generation = head.Generation
	source.FolderID = binding.FolderID
	sources.sources[a.RootID] = source
	if err := w.Process(ctx, documentJob(a, "republished-source")); err != nil {
		t.Fatal(err)
	}
	decide(3, true, "reactivate")
	feed.change.DocumentID = "provider-doc"
	feed.change.Sequence = 12
	feed.change.EventType = "document.deleted"
	if _, err := runner.Run(ctx, binding, 20); err != nil {
		t.Fatal(err)
	}
	head, _ = bridge.SourceHead(ctx, binding, "provider-doc")
	if head.Status != "published" || head.Sequence != 20 {
		t.Fatal("old tombstone removed the republished source")
	}
	loader := &productionOntologyDocumentSource{core: core}
	old := source
	old.Generation = 1
	if err := core.WithTx(ctx, func(tx db.Tx) error { return loader.Fence(ctx, tx, old) }); !errors.Is(err, knowledgeinstances.ErrConflict) {
		t.Fatalf("old generation commit accepted: %v", err)
	}
	if err := core.WithTx(ctx, func(tx db.Tx) error { return loader.Fence(ctx, tx, source) }); err != nil {
		t.Fatalf("current generation fenced: %v", err)
	}
}

func TestProductionOntologySourceLifecycleRollsBackAndIgnoresReadFailures(t *testing.T) {
	for _, scenario := range []string{"timeout", "forbidden", "transaction-failure", "foreign-projection", "wrong-folder", "wrong-tenant"} {
		t.Run(scenario, func(t *testing.T) {
			w, _, sources, _, watch := documentInferenceFixture(t)
			ctx := context.Background()
			core := w.store.Core
			if err := w.Process(ctx, documentJob(watch, "initial")); err != nil {
				t.Fatal(err)
			}
			handler := productionSourceWithdrawal(core)
			if scenario == "transaction-failure" {
				original := handler
				handler = func(ctx context.Context, tx db.Tx, event knowledgeontology.SourceWithdrawal) error {
					if err := original(ctx, tx, event); err != nil {
						return err
					}
					return errors.New("forced failure after domain withdrawal and task enqueue")
				}
			}
			bridge := knowledgeontology.NewPlatformStore(core).WithSourceLifecycle(handler)
			binding := knowledgeontology.Binding{TenantID: watch.TenantID, KnowledgeBaseID: "kb", FolderID: "folder", SourceAccessRef: sources.source.Access, Enabled: true}
			if err := bridge.SaveBinding(ctx, binding); err != nil {
				t.Fatal(err)
			}
			seedLifecycleSource(t, bridge, binding, sources.source, "10")
			feed := &lifecycleFeed{change: provider.KnowledgeChange{Sequence: 11, FolderID: "folder", DocumentID: "provider-doc", EventType: "document.deleted"}}
			switch scenario {
			case "timeout":
				feed.err = context.DeadlineExceeded
			case "forbidden":
				feed.err = errors.New("provider 403 forbidden")
			case "foreign-projection":
				if _, err := core.Exec(ctx, `UPDATE context_graph_facts SET source_stream_id='foreign' WHERE tenant_id=? AND predicate='country'`, watch.TenantID); err != nil {
					t.Fatal(err)
				}
			case "wrong-folder":
				feed.change.FolderID = "foreign-folder"
			case "wrong-tenant":
				binding.TenantID = "other-tenant"
			}
			runner := knowledgeontology.NewRunner(feed, contextgraph.NewPlatformStore(core), bridge, nil)
			if _, err := runner.Run(ctx, binding, 20); err == nil {
				t.Fatal("untrusted/failed invalidation accepted")
			}
			claims, _ := w.store.Claims(ctx, watch.TenantID, watch.ID)
			if len(claims) != 3 {
				t.Fatal("failed source event removed claims")
			}
			for _, table := range []string{"ontology_document_withdrawals", "knowledge_ontology_source_events"} {
				var n int
				if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial event transaction %s=%d %v", table, n, err)
				}
			}
		})
	}
}
