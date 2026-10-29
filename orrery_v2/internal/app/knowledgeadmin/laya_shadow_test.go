package knowledgeadmin

import (
	"context"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/knowledge/provider"
)

type blockedLayaJudge struct{ entered chan []string }

func (j blockedLayaJudge) Choose(ctx context.Context, s []string, q laya.Question) ([]laya.Decision, error) {
	j.entered <- s
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLayaShadowReturnsExactBaselineWithoutWaiting(t *testing.T) {
	entered := make(chan []string, 1)
	observer := laya.NewObserver(context.Background(), blockedLayaJudge{entered}, nil)
	defer observer.Close()
	input := []KnowledgeSearchResultDTO{
		{KnowledgeBaseID: "allowed", DocumentID: "d", ChunkID: "low", Score: 0.1, Snippet: "Relevant", HeadingPath: []string{"A"}},
		{KnowledgeBaseID: "allowed", DocumentID: "d", ChunkID: "high", Score: 0.9, Snippet: "Other", HeadingPath: []string{"B"}},
	}
	ctx := provider.WithRequestMetadata(context.Background(), provider.RequestMetadata{Actor: "viewer:authorized"})
	want, _ := (scoreReranker{}).Rerank(ctx, "query", input, 1)
	got, err := NewLayaShadowReranker(observer).Rerank(ctx, "query", input, 1)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("changed baseline: %v %v", got, err)
	}
	select {
	case states := <-entered:
		if len(states) != 2 {
			t.Fatal("lost candidate set")
		}
	case <-time.After(time.Second):
		t.Fatal("shadow not submitted")
	}
	got[0].HeadingPath[0] = "mutated"
	if input[1].HeadingPath[0] != "B" {
		t.Fatal("mutated original evidence")
	}
}

func TestLayaJobHashesFullEvidenceOutsideExcerpt(t *testing.T) {
	a := KnowledgeSearchResultDTO{KnowledgeBaseID: "kb", DocumentID: "doc", ChunkID: "chunk", Snippet: string(make([]byte, 400))}
	one := BuildLayaSearchJob("scope", "query", []KnowledgeSearchResultDTO{a})
	a.Snippet += "changed tail"
	two := BuildLayaSearchJob("scope", "query", []KnowledgeSearchResultDTO{a})
	if one.States[0] != two.States[0] || one.InputHashes[0] == two.InputHashes[0] {
		t.Fatal("cache ignores full source revision")
	}
}
