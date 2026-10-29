package pageindex

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/knowledge/provider"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/retrievalevidence"
)

type boundedRecallProvider struct {
	*fakeProvider
	onSearch func()
}

func (p *boundedRecallProvider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	if p.onSearch != nil {
		p.onSearch()
	}
	page, err := p.fakeProvider.Search(ctx, req)
	if len(page.Results) > req.Limit {
		page.Results = page.Results[:req.Limit]
	}
	return page, err
}

func TestRecallFiltersEligibilityBeforeTopKWithoutDeletingHistory(t *testing.T) {
	now := time.Now().UTC()
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent"}, memcore.OwnerRef{AgentID: "agent"})
	remote := &boundedRecallProvider{fakeProvider: newFakeProvider()}
	service := NewService(ServiceOptions{Memory: store, State: NewMemoryState(), Provider: func() Provider { return remote }, Now: func() time.Time { return now }})
	for i, kind := range []string{"disabled", "future", "expired", "valid", "retained"} {
		r := seedMemoryRecord(t, store, profile, kind, kind+" source")
		if _, _, err := service.SyncRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "disabled":
			r.DisabledAt = &now
		case "future":
			r.ValidFrom = now.Add(time.Hour)
		case "expired":
			r.ExpiresAt = now
		case "retained":
			r.ExpiresAt = now
			r.ReferenceCount = 1
		}
		if err := store.SaveRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
		remote.searchResults = append(remote.searchResults, provider.SearchResult{DocumentID: r.ID, Score: float32(1) - float32(i)*0.1, Snippet: "untrusted stale snippet"})
	}
	req := RecallRequest{Query: "source", Subject: memcore.AccessSubject{AgentID: "agent"}, ProfileRefs: []agentcore.MemoryProfileRef{memcore.MemoryProfileRefForScope(profile)}, Limit: 1}
	got, err := service.Recall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "valid" || got[0].Summary != "valid source" || remote.lastSearch.Limit <= req.Limit {
		t.Fatalf("recall=%+v provider limit=%d", got, remote.lastSearch.Limit)
	}
	req.Limit = 10
	got, err = service.Recall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != "retained" {
		t.Fatalf("historical memory not retained: %+v", got)
	}
	for _, id := range []string{"expired", "retained"} {
		if _, err := store.GetRecord(ctx, id); err != nil {
			t.Fatal("retrieval deleted source", err)
		}
	}
}

func TestRecallRejectsProfileRevocationDuringProviderCall(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent"}, memcore.OwnerRef{AgentID: "agent"})
	r := seedMemoryRecord(t, store, profile, "record", "source")
	remote := &boundedRecallProvider{fakeProvider: newFakeProvider()}
	service := NewService(ServiceOptions{Memory: store, State: NewMemoryState(), Provider: func() Provider { return remote }})
	if _, _, err := service.SyncRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	remote.searchResults = []provider.SearchResult{{DocumentID: r.ID, Score: 1}}
	remote.onSearch = func() {
		profile.Enabled = false
		if err := store.SaveProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
	}
	got, err := service.Recall(ctx, RecallRequest{Query: "source", Subject: memcore.AccessSubject{AgentID: "agent"}, ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: profile.ID}}})
	if !errors.Is(err, retrievalevidence.ErrInputChanged) || len(got) != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
