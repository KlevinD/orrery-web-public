package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

type catalogSweeperRepository struct {
	page knowledgecore.KnowledgeBasePage
	err  error
}

func (r catalogSweeperRepository) ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error) {
	return r.page, r.err
}

type catalogSweeperReconciler struct {
	bases []knowledgecore.KnowledgeBase
	fail  map[knowledgecore.KnowledgeBaseID]error
}

func (r *catalogSweeperReconciler) Reconcile(_ context.Context, base knowledgecore.KnowledgeBase) (knowledgeadmin.CatalogReconcileResult, error) {
	r.bases = append(r.bases, base)
	return knowledgeadmin.CatalogReconcileResult{}, r.fail[base.ID]
}

func TestProductionKnowledgeCatalogSweeperFiltersContinuesAndThrottles(t *testing.T) {
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	repository := catalogSweeperRepository{page: knowledgecore.KnowledgeBasePage{Items: []knowledgecore.KnowledgeBase{
		{ID: "pageindex-failed", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-1", Status: knowledgecore.KnowledgeActive},
		{ID: "local", Provider: knowledgecore.ProviderLocal, ProviderResourceID: "folder-local", Status: knowledgecore.KnowledgeActive},
		{ID: "disabled", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-disabled", Status: knowledgecore.KnowledgeDisabled},
		{ID: "empty-folder", Provider: knowledgecore.ProviderPageIndex, Status: knowledgecore.KnowledgeActive},
		{ID: "pageindex-success", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-2", Status: knowledgecore.KnowledgeActive},
	}}}
	reconciler := &catalogSweeperReconciler{fail: map[knowledgecore.KnowledgeBaseID]error{"pageindex-failed": errors.New("pageindex unavailable")}}
	sweeper := newProductionKnowledgeCatalogSweeper(repository, reconciler, func() time.Time { return now }, 5*time.Minute)

	err := sweeper.Sweep(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pageindex-failed") || !strings.Contains(err.Error(), "pageindex unavailable") {
		t.Fatalf("Sweep error = %v", err)
	}
	if len(reconciler.bases) != 2 || reconciler.bases[0].ID != "pageindex-failed" || reconciler.bases[1].ID != "pageindex-success" {
		t.Fatalf("reconciled bases = %+v", reconciler.bases)
	}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("throttled Sweep: %v", err)
	}
	if len(reconciler.bases) != 2 {
		t.Fatalf("throttled sweep reconciled again: %+v", reconciler.bases)
	}

	now = now.Add(5 * time.Minute)
	reconciler.fail = nil
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("next Sweep: %v", err)
	}
	if len(reconciler.bases) != 4 {
		t.Fatalf("next sweep calls = %+v", reconciler.bases)
	}
}

func TestProductionKnowledgeCatalogSweeperProcessesBoundedRoundRobinBatches(t *testing.T) {
	now := time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)
	repository := catalogSweeperRepository{page: knowledgecore.KnowledgeBasePage{Items: []knowledgecore.KnowledgeBase{
		{ID: "kb-4", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-4", Status: knowledgecore.KnowledgeActive},
		{ID: "kb-2", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-2", Status: knowledgecore.KnowledgeActive},
		{ID: "kb-5", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-5", Status: knowledgecore.KnowledgeActive},
		{ID: "kb-1", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-1", Status: knowledgecore.KnowledgeActive},
		{ID: "kb-3", Provider: knowledgecore.ProviderPageIndex, ProviderResourceID: "folder-3", Status: knowledgecore.KnowledgeActive},
	}}}
	reconciler := &catalogSweeperReconciler{}
	sweeper := newProductionKnowledgeCatalogSweeper(repository, reconciler, func() time.Time { return now }, 30*time.Second)

	wantIDs := [][]knowledgecore.KnowledgeBaseID{
		{"kb-1", "kb-2"},
		{"kb-1", "kb-2", "kb-3", "kb-4"},
		{"kb-1", "kb-2", "kb-3", "kb-4", "kb-5", "kb-1"},
	}
	for index, want := range wantIDs {
		if err := sweeper.Sweep(context.Background()); err != nil {
			t.Fatalf("Sweep %d: %v", index+1, err)
		}
		if len(reconciler.bases) != len(want) {
			t.Fatalf("Sweep %d bases = %+v, want %+v", index+1, reconciler.bases, want)
		}
		for baseIndex, base := range reconciler.bases {
			if base.ID != want[baseIndex] {
				t.Fatalf("Sweep %d bases = %+v, want %+v", index+1, reconciler.bases, want)
			}
		}
		now = now.Add(30 * time.Second)
	}
}

func TestProductionKnowledgeCatalogSweeperRequiresDependencies(t *testing.T) {
	if sweeper := newProductionKnowledgeCatalogSweeper(nil, nil, time.Now, time.Minute); sweeper != nil {
		t.Fatalf("sweeper = %#v", sweeper)
	}
}
