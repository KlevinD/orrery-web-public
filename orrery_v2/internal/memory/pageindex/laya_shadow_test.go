package pageindex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/knowledge/provider"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
)

type recallJudgeFunc func(context.Context, []string, laya.Question) ([]laya.Decision, error)

func (f recallJudgeFunc) Choose(ctx context.Context, states []string, question laya.Question) ([]laya.Decision, error) {
	return f(ctx, states, question)
}

func TestLayaRecallObservesOnlyAuthorizedActiveRecordsWithoutWaiting(t *testing.T) {
	ctx := context.Background()
	memory := memtestkit.NewFakeStore()
	owned := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-a"}, memcore.OwnerRef{AgentID: "agent-a"})
	other := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-b"}, memcore.OwnerRef{AgentID: "agent-b"})
	first := seedMemoryRecord(t, memory, owned, "first", "Use concise answers.")
	second := seedMemoryRecord(t, memory, owned, "second", "Always preserve source evidence.")
	private := seedMemoryRecord(t, memory, other, "private", "Private secret must never reach the judge.")
	archived := seedMemoryRecord(t, memory, owned, "archived", "Archived secret must never reach the judge.")
	pageIndex := newFakeProvider()
	baseline := NewService(ServiceOptions{Memory: memory, State: NewMemoryState(), Provider: func() Provider { return pageIndex }})
	for _, record := range []memcore.MemoryRecord{first, second, private, archived} {
		if _, _, err := baseline.SyncRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := memory.UpdateRecordLifecycle(ctx, archived.ID, memcore.MemoryLifecycleArchived); err != nil {
		t.Fatal(err)
	}
	// The external provider deliberately returns private, stale and duplicate
	// references. The observer must see the canonical authorized result only.
	pageIndex.searchResults = []provider.SearchResult{
		{DocumentID: private.ID, Score: 1}, {DocumentID: archived.ID, Score: .95},
		{DocumentID: second.ID, Score: .9}, {DocumentID: first.ID, Score: .8},
		{DocumentID: second.ID, Score: .7}, {DocumentID: "missing", Score: .6},
	}
	request := RecallRequest{Query: "How should I answer?", Subject: memcore.AccessSubject{AgentID: "agent-a"},
		ProfileRefs: []agentcore.MemoryProfileRef{memcore.MemoryProfileRefForScope(owned), memcore.MemoryProfileRefForScope(other)}}
	want, err := baseline.Recall(ctx, request)
	if err != nil || len(want) != 2 {
		t.Fatalf("baseline recall: records=%d err=%v", len(want), err)
	}
	entered := make(chan []string, 1)
	observer := laya.NewObserver(ctx, recallJudgeFunc(func(ctx context.Context, states []string, _ laya.Question) ([]laya.Decision, error) {
		entered <- states
		<-ctx.Done()
		return nil, ctx.Err()
	}), nil)
	defer observer.Close()
	baseline.observeRecall = NewLayaRecallObserver(observer)
	type response struct {
		records []memcore.MemoryRecord
		err     error
	}
	done := make(chan response, 1)
	go func() { got, err := baseline.Recall(ctx, request); done <- response{got, err} }()
	select {
	case got := <-done:
		if got.err != nil || !reflect.DeepEqual(got.records, want) {
			t.Fatal("shadow changed memory content, evidence or order")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("memory recall waited for blocked inference")
	}
	select {
	case states := <-entered:
		if len(states) != 2 || strings.Contains(strings.Join(states, " "), "secret") || !strings.Contains(states[0], second.Summary) {
			t.Fatalf("wrong authorized candidate set: %v", states)
		}
	case <-time.After(time.Second):
		t.Fatal("missing memory observation")
	}
	observer.Close()
	got, err := baseline.Recall(ctx, request)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("closed observer changed retrieval")
	}
}

func TestLayaRecallJobBoundsInputsAndSeparatesFullEvidenceAndAccess(t *testing.T) {
	request := RecallRequest{Query: strings.Repeat("查询偏好", 30), Subject: memcore.AccessSubject{AgentID: "agent-a", OrganizationIDs: []string{"tenant-a"}},
		ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: "private-profile"}}}
	record := memcore.MemoryRecord{ID: "private-memory-id", ProfileID: "private-profile", Content: strings.Repeat("中文记忆", 100), SourceEvidenceRefs: []string{"private-evidence"}}
	one := BuildLayaRecallJob(request, []memcore.MemoryRecord{record})
	if len(one.States) != 1 || !one.InputTruncated || !utf8.ValidString(one.States[0]) {
		t.Fatal("invalid bounded memory input")
	}
	body, _ := json.Marshal(map[string]any{"model": "multilingual", "state": one.States[0], "questions": map[string]laya.Question{"judgment": one.Question}})
	if len(body) > laya.MaxRequestBytes {
		t.Fatalf("request exceeds deployed budget: %d", len(body))
	}
	for _, secret := range []string{record.ID, string(record.ProfileID), record.SourceEvidenceRefs[0], "tenant-a"} {
		if strings.Contains(string(body), secret) || strings.Contains(strings.Join(one.Sources, " "), secret) {
			t.Fatal("private identity leaked into model input or log reference")
		}
	}
	record.Content += "changed beyond excerpt"
	two := BuildLayaRecallJob(request, []memcore.MemoryRecord{record})
	if one.States[0] != two.States[0] || one.InputHashes[0] == two.InputHashes[0] {
		t.Fatal("full memory content is absent from cache identity")
	}
	record.SourceEvidenceRefs = []string{"changed-evidence"}
	three := BuildLayaRecallJob(request, []memcore.MemoryRecord{record})
	if two.InputHashes[0] == three.InputHashes[0] {
		t.Fatal("evidence change reused old judgment")
	}
	request.Subject.OrganizationIDs = []string{"tenant-b"}
	four := BuildLayaRecallJob(request, []memcore.MemoryRecord{record})
	if three.Scope == four.Scope {
		t.Fatal("access scopes share inference cache")
	}
	request.Query += "changed query tail"
	five := BuildLayaRecallJob(request, []memcore.MemoryRecord{record})
	if four.InputHashes[0] == five.InputHashes[0] {
		t.Fatal("query tail is absent from cache identity")
	}
	request.Query = " "
	if len(BuildLayaRecallJob(request, []memcore.MemoryRecord{record}).States) != 0 {
		t.Fatal("empty query reached inference")
	}
	if NewLayaRecallObserver(nil) != nil {
		t.Fatal("disabled mode registered an observer")
	}
}

func TestLayaRecallRecordsSuggestionAndInferenceFailure(t *testing.T) {
	request := RecallRequest{Query: "order status", Subject: memcore.AccessSubject{AgentID: "agent-a"}, ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: "profile"}}}
	records := []memcore.MemoryRecord{{ID: "irrelevant", ProfileID: "profile", Summary: "Weather"}, {ID: "relevant", ProfileID: "profile", Summary: "Order status"}}
	job := BuildLayaRecallJob(request, records)
	assessments := make(chan laya.Record, 1)
	observer := laya.NewObserver(context.Background(), recallJudgeFunc(func(context.Context, []string, laya.Question) ([]laya.Decision, error) {
		return []laya.Decision{{Choice: "unrelated"}, {Choice: "direct"}}, nil
	}), func(record laya.Record) { assessments <- record })
	defer observer.Close()
	NewLayaRecallObserver(observer)(context.Background(), request, records)
	select {
	case assessment := <-assessments:
		if assessment.Kind != "memory" || assessment.Mode != "shadow" || assessment.Fallback != "" || !reflect.DeepEqual(assessment.ProposedOrder, []string{job.Sources[1], job.Sources[0]}) {
			t.Fatalf("bad suggested order: %+v", assessment)
		}
	case <-time.After(time.Second):
		t.Fatal("missing suggested order")
	}
	failing := laya.NewObserver(context.Background(), recallJudgeFunc(func(context.Context, []string, laya.Question) ([]laya.Decision, error) {
		return nil, errors.New("unavailable")
	}), func(record laya.Record) { assessments <- record })
	defer failing.Close()
	NewLayaRecallObserver(failing)(context.Background(), request, records)
	select {
	case assessment := <-assessments:
		if assessment.Fallback != "inference_unavailable" || len(assessment.ProposedOrder) != 0 {
			t.Fatal("inference failure produced a usable suggestion")
		}
	case <-time.After(time.Second):
		t.Fatal("missing fallback")
	}
}
