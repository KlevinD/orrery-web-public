package pageindex

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/knowledge/provider"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestLayaMemoryLiveCanary(t *testing.T) {
	keyFile := os.Getenv("ORRERY_LAYA_CANARY_KEY_FILE")
	if keyFile == "" {
		t.Skip("requires explicit local Laya canary credential path")
	}
	key, err := laya.ReadKeyFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	client, err := laya.NewClient("http://127.0.0.1:18490", key, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	assessments := make(chan laya.Record, 1)
	observer := laya.NewObserver(ctx, client, func(record laya.Record) { assessments <- record })
	defer observer.Close()
	memory := memtestkit.NewFakeStore()
	profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "memory-canary-agent"}, memcore.OwnerRef{AgentID: "memory-canary-agent"})
	record := seedMemoryRecord(t, memory, profile, "memory-canary-record", "The user prefers concise answers with conclusions first.")
	pageIndex := newFakeProvider()
	service := NewService(ServiceOptions{Memory: memory, State: NewMemoryState(), Provider: func() Provider { return pageIndex }, ObserveRecall: NewLayaRecallObserver(observer)})
	if _, _, err := service.SyncRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	pageIndex.searchResults = []provider.SearchResult{{DocumentID: record.ID, Score: 1}}
	request := RecallRequest{Query: "What answer format does the user prefer?", Subject: memcore.AccessSubject{AgentID: "memory-canary-agent"},
		ProfileRefs: []agentcore.MemoryProfileRef{memcore.MemoryProfileRefForScope(profile)}}
	got, err := service.Recall(ctx, request)
	if err != nil || !reflect.DeepEqual(got, []memcore.MemoryRecord{record.Normalized()}) {
		t.Fatal("live memory shadow changed the canonical authorized recall")
	}
	select {
	case assessment := <-assessments:
		if assessment.Fallback != "" || assessment.Kind != "memory" || len(assessment.Decisions) != 1 || len(assessment.ProposedOrder) != 1 {
			t.Fatalf("live memory assessment failed: %+v", assessment)
		}
		t.Logf("real Laya memory canary: choice=%s elapsed_ms=%d mode=%s", assessment.Decisions[0].Choice, assessment.ElapsedMS, assessment.Mode)
	case <-time.After(8 * time.Second):
		t.Fatal("live memory assessment timed out")
	}
}
