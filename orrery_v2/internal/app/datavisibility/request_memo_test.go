package datavisibility

import (
	"context"
	"errors"
	"testing"
)

func TestRequestMemoIsRequestScopedAndPreservesStoredValues(t *testing.T) {
	base := context.Background()
	first := WithRequestMemo(base)
	second := WithRequestMemo(base)
	firstMemo := RequestMemoFromContext(first)
	secondMemo := RequestMemoFromContext(second)
	if firstMemo == nil || secondMemo == nil || firstMemo == secondMemo {
		t.Fatal("request memos were not independently scoped")
	}
	firstMemo.Store("allowed", true)
	value, ok := firstMemo.Load("allowed")
	if !ok || value != true {
		t.Fatalf("stored memo value = %#v, found=%t", value, ok)
	}
	if _, ok := secondMemo.Load("allowed"); ok {
		t.Fatal("memo value leaked into another request")
	}
	if WithRequestMemo(first) != first {
		t.Fatal("nested request memo replaced the active memo")
	}
}

func TestFreshRequestMemoReplacesExistingConsistencyBaseline(t *testing.T) {
	first := WithRequestMemo(context.Background())
	second := WithFreshRequestMemo(first)
	if RequestMemoFromContext(first) == nil || RequestMemoFromContext(second) == nil {
		t.Fatal("request memo is missing")
	}
	if RequestMemoFromContext(first) == RequestMemoFromContext(second) {
		t.Fatal("fresh request memo reused the previous consistency baseline")
	}
}

func TestRequestMemoValidatesEachConsistencyBaselineOnce(t *testing.T) {
	ctx := WithRequestMemo(context.Background())
	memo := RequestMemoFromContext(ctx)
	firstCalls, secondCalls := 0, 0
	changed := errors.New("changed")
	memo.RegisterValidator("b", func(context.Context) error {
		secondCalls++
		return changed
	})
	memo.RegisterValidator("a", func(context.Context) error {
		firstCalls++
		return nil
	})
	memo.RegisterValidator("a", func(context.Context) error {
		firstCalls += 100
		return nil
	})
	if err := memo.Validate(ctx); !errors.Is(err, changed) {
		t.Fatalf("Validate error = %v, want changed", err)
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("validator calls = %d/%d, want 1/1", firstCalls, secondCalls)
	}
}
