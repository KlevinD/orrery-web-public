package retrieval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/retrievalevidence"
)

type changingRetrievalStore struct {
	*memtestkit.FakeStore
	afterFind func()
}

func (s *changingRetrievalStore) FindRecords(ctx context.Context, q memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	r, err := s.FakeStore.FindRecords(ctx, q)
	if s.afterFind != nil {
		s.afterFind()
	}
	return r, err
}

func retrievalFixture(t *testing.T, now time.Time) (*memtestkit.FakeStore, memcore.MemoryProfile, memcore.MemoryRecord) {
	t.Helper()
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	p := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent"}, memcore.OwnerRef{AgentID: "agent"})
	p.RetrievalPolicy.MaxRecords = 1
	if err := store.SaveProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "accepted", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatal(err)
	}
	r := memcore.MemoryRecord{ID: "valid", ProfileID: p.ID, CandidateID: "accepted", Summary: "usable history", Facts: []string{"source statement"}, UpdatedAt: now, Confidence: 0.5}
	if err := store.SaveRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, err := store.GetRecord(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, p, r
}

func TestRetrieverFiltersBeforeLimitAndDirectCompression(t *testing.T) {
	now := time.Now().UTC()
	ctx := context.Background()
	store, p, r := retrievalFixture(t, now)
	for _, kind := range []string{"disabled", "future", "expired"} {
		bad := r
		bad.ID = kind
		bad.Summary = kind + " must not enter context"
		bad.Confidence = 1
		switch kind {
		case "disabled":
			bad.DisabledAt = &now
		case "future":
			bad.ValidFrom = now.Add(time.Hour)
		case "expired":
			bad.ExpiresAt = now
		}
		if err := store.SaveRecord(ctx, bad); err != nil {
			t.Fatal(err)
		}
	}
	compressor := NewCompressor(CompressionOptions{Now: func() time.Time { return now }})
	digest, err := NewRetriever(store, compressor).Retrieve(ctx, RetrieveRequest{AgentID: "agent", Subject: memcore.AccessSubject{AgentID: "agent"}, ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: p.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.SourceMemoryRefs) != 1 || digest.SourceMemoryRefs[0] != r.ID || len(digest.Evidence) != 1 || digest.Evidence[0].AccessHash == "" {
		t.Fatalf("digest=%+v", digest)
	}
	all, _ := store.FindRecords(ctx, memstore.RecordQuery{})
	direct, err := compressor.Compress(CompressRequest{Records: all})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.SourceMemoryRefs) != 1 || direct.Confidence != r.Confidence || strings.Contains(direct.Summary, "must not") {
		t.Fatalf("direct=%+v", direct)
	}
}

func TestRetrieverRejectsSourceAndGrantChangesDuringRetrieval(t *testing.T) {
	for _, kind := range []string{"source rewrite", "disable", "profile revoke"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC()
			ctx := context.Background()
			store, p, r := retrievalFixture(t, now)
			changing := &changingRetrievalStore{FakeStore: store, afterFind: func() {
				switch kind {
				case "source rewrite":
					r.Summary = "replacement"
				case "disable":
					r.DisabledAt = &now
				case "profile revoke":
					p.Enabled = false
					if err := store.SaveProfile(ctx, p); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err := store.SaveRecord(ctx, r); err != nil {
					t.Fatal(err)
				}
			}}
			digest, err := NewRetriever(changing, NewCompressor(CompressionOptions{Now: func() time.Time { return now }})).Retrieve(ctx, RetrieveRequest{Subject: memcore.AccessSubject{AgentID: "agent"}, ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: p.ID}}})
			if !errors.Is(err, retrievalevidence.ErrInputChanged) || digest.Summary != "" {
				t.Fatalf("digest=%+v err=%v", digest, err)
			}
		})
	}
}
