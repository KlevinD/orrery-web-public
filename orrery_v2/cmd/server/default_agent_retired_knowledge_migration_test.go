package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

func TestDisableRetiredEmptyKnowledgeBasesPreservesReferencedContent(t *testing.T) {
	at := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	profiles := retiredKnowledgeDirectoryFake{deleted: []agentcore.AgentProfileID{"retired-agent"}}
	knowledge := &retiredKnowledgeStoreFake{
		bases: []knowledgecore.KnowledgeBase{
			{ID: "kb-empty", OwnerType: knowledgecore.OwnerAgent, OwnerID: "retired-agent", Status: knowledgecore.KnowledgeActive},
			{ID: "kb-document", OwnerType: knowledgecore.OwnerAgent, OwnerID: "retired-agent", Status: knowledgecore.KnowledgeActive},
			{ID: "kb-binding", OwnerType: knowledgecore.OwnerAgent, OwnerID: "retired-agent", Status: knowledgecore.KnowledgeActive},
			{ID: "kb-live", OwnerType: knowledgecore.OwnerAgent, OwnerID: "live-agent", Status: knowledgecore.KnowledgeActive},
		},
		documents: map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeDocument{
			"kb-document": {{ID: "doc-1", KnowledgeBaseID: "kb-document"}},
		},
		bindings: map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeBinding{
			"kb-binding": {{ID: "binding-1", KnowledgeBaseID: "kb-binding"}},
		},
	}

	report, err := disableRetiredEmptyKnowledgeBases(context.Background(), profiles, knowledge, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 4 || report.Disabled != 1 {
		t.Fatalf("report = %+v", report)
	}
	if got := knowledge.saved["kb-empty"]; got.Status != knowledgecore.KnowledgeDisabled || !got.UpdatedAt.Equal(at) {
		t.Fatalf("disabled base = %+v", got)
	}
	for _, id := range []knowledgecore.KnowledgeBaseID{"kb-document", "kb-binding", "kb-live"} {
		if _, changed := knowledge.saved[id]; changed {
			t.Fatalf("knowledge base %s was modified", id)
		}
	}
}

type retiredKnowledgeDirectoryFake struct {
	deleted []agentcore.AgentProfileID
}

func (f retiredKnowledgeDirectoryFake) ListDeletedProfileIDs(context.Context) ([]agentcore.AgentProfileID, error) {
	return append([]agentcore.AgentProfileID(nil), f.deleted...), nil
}

type retiredKnowledgeStoreFake struct {
	bases     []knowledgecore.KnowledgeBase
	documents map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeDocument
	bindings  map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeBinding
	saved     map[knowledgecore.KnowledgeBaseID]knowledgecore.KnowledgeBase
}

func (f *retiredKnowledgeStoreFake) ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error) {
	return knowledgecore.KnowledgeBasePage{Items: append([]knowledgecore.KnowledgeBase(nil), f.bases...), Total: len(f.bases)}, nil
}

func (f *retiredKnowledgeStoreFake) ListDocuments(_ context.Context, query knowledgecore.DocumentQuery) (knowledgecore.DocumentPage, error) {
	items := append([]knowledgecore.KnowledgeDocument(nil), f.documents[query.KnowledgeBaseID]...)
	return knowledgecore.DocumentPage{Items: items, Total: len(items)}, nil
}

func (f *retiredKnowledgeStoreFake) ListBindings(_ context.Context, query knowledgecore.BindingQuery) ([]knowledgecore.KnowledgeBinding, error) {
	return append([]knowledgecore.KnowledgeBinding(nil), f.bindings[query.KnowledgeBaseID]...), nil
}

func (f *retiredKnowledgeStoreFake) SaveKnowledgeBase(_ context.Context, base knowledgecore.KnowledgeBase) error {
	if f.saved == nil {
		f.saved = make(map[knowledgecore.KnowledgeBaseID]knowledgecore.KnowledgeBase)
	}
	f.saved[base.ID] = base
	return nil
}
