package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestProductionMemorySearchLabelsResultsWithoutReplacingMisses(t *testing.T) {
	for _, tc := range []struct {
		name, mode, reason string
		query              string
		recall             bool
		hit                bool
		failure            bool
		empty              bool
	}{
		{name: "semantic hit", mode: "semantic", query: "report", recall: true, hit: true},
		{name: "no match with background memory", mode: "none", reason: "no_semantic_match", query: "report", recall: true},
		{name: "recall error with background memory", query: "report", recall: true, failure: true},
		{name: "unavailable with background memory", mode: "none", reason: "semantic_unavailable", query: "report"},
		{name: "profile overview", mode: "profile_digest"},
		{name: "no memory", mode: "none", reason: "no_semantic_match", query: "report", recall: true, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := memtestkit.NewFakeStore()
			agentID := discussionsession.PersonalAssistantAgentID("tenant", "owner")
			profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID}, memcore.OwnerRef{AgentID: agentcore.AgentID(agentID)})
			if err := store.SaveProfile(ctx, profile); err != nil {
				t.Fatal(err)
			}
			record := memcore.MemoryRecord{ID: "record", CandidateID: "candidate", ProfileID: profile.ID, Scope: profile.DefaultScope, Summary: "Use tables in reports", Layer: memcore.MemoryLayerFact, LifecycleStatus: memcore.MemoryLifecycleActive, Visibility: memcore.Visibility{Mode: memcore.VisibilityOwnerOnly}, CreatedAt: time.Now(), Confidence: 0.9}
			if !tc.empty {
				if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate", Status: memcore.CandidateAccepted}); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveRecord(ctx, record); err != nil {
					t.Fatal(err)
				}
			}
			recall := &capturingSemanticMemoryRecall{}
			if tc.hit {
				recall.records = []memcore.MemoryRecord{record}
			}
			if tc.failure {
				recall.err = errors.New("backend details must not be exposed")
			}
			opts := productionDiscussionMemoryProviderOptions{}
			if tc.recall {
				opts.Recall = recall
			}
			provider := newProductionDiscussionMemoryProvider(nil, store, opts)
			memory, err := provider.DiscussionAgentMemoryContextForQuery(ctx, agentID, tc.query)
			if tc.failure {
				if !errors.Is(err, recall.err) {
					t.Fatalf("recall error was replaced with unrelated memory: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			digest := memory.PersonalDigest
			if tc.mode == "none" && (digest.Summary != "" || len(digest.SourceMemory) != 0 || len(digest.SourceEvidence) != 0) {
				t.Fatalf("query miss included background evidence: %+v", digest)
			}
			if digest.RetrievalMode != tc.mode || digest.FallbackReason != tc.reason {
				t.Fatalf("provenance: %+v", digest)
			}
			output := productionMemorySearchDigest(digest)
			if output["retrieval_mode"] != tc.mode || output["fallback_reason"] != tc.reason {
				t.Fatalf("MCP lost provenance: %+v", output)
			}
		})
	}
}
