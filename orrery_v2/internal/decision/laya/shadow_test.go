package laya

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type judgeFunc func(context.Context, []string, Question) ([]Decision, error)

func (f judgeFunc) Choose(ctx context.Context, s []string, q Question) ([]Decision, error) {
	return f(ctx, s, q)
}

func testJob() Job {
	return Job{Kind: "knowledge", Scope: "tenant:a/agent:a", PromptVersion: "test-v1", Sources: []string{"source:1"}, InputHashes: []string{Hash("complete evidence")}, States: []string{"hello"}, Question: testQuestion()}
}

func receiveRecord(t *testing.T, ch <-chan Record) Record {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("missing assessment")
		return Record{}
	}
}

func TestObserverCacheSeparatesScopeFullContentAndPrompt(t *testing.T) {
	var calls atomic.Int32
	records := make(chan Record, 8)
	o := NewObserver(context.Background(), judgeFunc(func(ctx context.Context, s []string, q Question) ([]Decision, error) {
		calls.Add(1)
		return []Decision{{Choice: "a"}}, nil
	}), func(r Record) { records <- r })
	defer o.Close()
	job := testJob()
	for i := 0; i < 5; i++ {
		if i == 2 {
			job.Scope = "tenant:b/agent:a"
		}
		if i == 3 {
			job.InputHashes[0] = Hash("changed evidence beyond excerpt")
		}
		if i == 4 {
			job.PromptVersion = "test-v2"
		}
		if !o.Submit(context.Background(), job) {
			t.Fatal("job rejected")
		}
		r := receiveRecord(t, records)
		if r.Cached != (i == 1) || r.Mode != "shadow" || r.ScopeHash == job.Scope || r.InputHashes[0] != job.InputHashes[0] {
			t.Fatalf("bad provenance/cache: %+v", r)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("inferences %d, want 4", calls.Load())
	}
}

func TestObserverIsBoundedCopiesInputsAndCancels(t *testing.T) {
	entered := make(chan struct{}, 1)
	records := make(chan Record, 16)
	observed := make(chan string, 1)
	o := NewObserver(context.Background(), judgeFunc(func(ctx context.Context, s []string, q Question) ([]Decision, error) {
		observed <- s[0]
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}), func(r Record) { records <- r })
	job := testJob()
	if !o.Submit(context.Background(), job) {
		t.Fatal("first job rejected")
	}
	<-entered
	job.States[0] = "changed by caller"
	if got := <-observed; got != "hello" {
		t.Fatalf("aliased state %s", got)
	}
	for i := 0; i < 8; i++ {
		if !o.Submit(context.Background(), job) {
			t.Fatal("queue filled early")
		}
	}
	if o.Submit(context.Background(), job) {
		t.Fatal("queue exceeded capacity")
	}
	if receiveRecord(t, records).Fallback != "queue_full" {
		t.Fatal("missing queue fallback")
	}
	o.Close()
	if o.Submit(context.Background(), job) {
		t.Fatal("accepted after shutdown")
	}
}

func TestExcerptPreservesUTF8(t *testing.T) {
	for i := 0; i < 15; i++ {
		s := Excerpt("中文测试abc", i)
		if !utf8.ValidString(s) || len(s) > i {
			t.Fatal("invalid excerpt")
		}
	}
}

func TestObserverFallbacksAndStableProposedOrder(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{context.DeadlineExceeded, "timeout"}, {ErrResponse, "invalid_response"}, {errors.New("upstream detail"), "inference_unavailable"},
	} {
		records := make(chan Record, 1)
		o := NewObserver(context.Background(), judgeFunc(func(context.Context, []string, Question) ([]Decision, error) { return nil, tc.err }), func(r Record) { records <- r })
		o.Submit(context.Background(), testJob())
		r := receiveRecord(t, records)
		o.Close()
		if r.Fallback != tc.reason || len(r.Decisions) != 0 {
			t.Fatalf("bad fallback: %+v", r)
		}
	}
	records := make(chan Record, 1)
	o := NewObserver(context.Background(), judgeFunc(func(context.Context, []string, Question) ([]Decision, error) {
		return []Decision{{Choice: "unrelated"}, {Choice: "direct"}, {Choice: "direct"}}, nil
	}), func(r Record) { records <- r })
	defer o.Close()
	job := testJob()
	job.Sources = []string{"old-first", "direct-second", "direct-third"}
	job.InputHashes = []string{Hash("a"), Hash("b"), Hash("c")}
	job.States = []string{"a", "b", "c"}
	job.Question = Question{Type: "choice", Instructions: "Relevance", Criteria: map[string]string{"direct": "relevant", "unrelated": "irrelevant"}}
	o.Submit(context.Background(), job)
	r := receiveRecord(t, records)
	if len(r.ProposedOrder) != 3 || r.ProposedOrder[0] != "direct-second" || r.ProposedOrder[1] != "direct-third" || job.Sources[0] != "old-first" {
		t.Fatalf("bad stable proposal %+v", r)
	}
}
