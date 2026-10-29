package retrieval

import (
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func TestCompressorBuildsDigestWithSourcesAndNoRawPrivateContent(t *testing.T) {
	compressor := NewCompressor(CompressionOptions{MaxSummaryBytes: 64})
	records := []memcore.MemoryRecord{{
		ID:                 "mem-1",
		ProfileID:          agentcore.MemoryProfileID("mem.personal.agent-a"),
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-a"},
		SourceRefs:         []string{"message-1"},
		SourceEvidenceRefs: []string{"evidence-1"},
		Content:            "raw private conversation should not leak into digest",
		Summary:            "Prefers concise implementation plans with tests.",
		Facts:              []string{"Uses Go for backend modules."},
		Warnings:           []string{"Do not log private memory content."},
		Sensitivity:        memcore.SensitivitySecret,
		Confidence:         0.9,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}}

	digest, err := compressor.Compress(CompressRequest{
		AgentID: agentcore.AgentID("agent-a"),
		Scope:   memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-a"},
		Records: records,
	})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(digest.SourceMemoryRefs) != 1 || digest.SourceMemoryRefs[0] != "mem-1" {
		t.Fatalf("source memory refs = %+v", digest.SourceMemoryRefs)
	}
	if len(digest.SourceEvidenceRefs) != 1 || digest.SourceEvidenceRefs[0] != "evidence-1" {
		t.Fatalf("source evidence refs = %+v", digest.SourceEvidenceRefs)
	}
	if strings.Contains(digest.Summary, "raw private conversation") {
		t.Fatalf("digest leaked raw content: %q", digest.Summary)
	}
	if digest.Visibility.Mode != memcore.VisibilityOwnerOnly {
		t.Fatalf("visibility = %+v", digest.Visibility)
	}
	if digest.Sensitivity != memcore.SensitivitySecret {
		t.Fatalf("sensitivity = %q, want secret", digest.Sensitivity)
	}
}

func TestCompressorBuildsLayerDigestsAndCompatibilitySummary(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	compressor := NewCompressor(CompressionOptions{Now: func() time.Time { return now }})
	digest, err := compressor.Compress(CompressRequest{
		AgentID: agentcore.AgentID("agent-1"),
		Scope:   memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Records: []memcore.MemoryRecord{
			{
				ID:                 "memory-fact",
				Layer:              memcore.MemoryLayerFact,
				Topic:              "codebase",
				Summary:            "Fact summary",
				Facts:              []string{"Fact one"},
				SourceEvidenceRefs: []string{"evidence-1"},
				Confidence:         0.8,
			},
			{
				ID:          "memory-world",
				Layer:       memcore.MemoryLayerWorld,
				Topic:       "codebase",
				Summary:     "World summary",
				Lessons:     []string{"World lesson"},
				LineageRefs: []string{"memory-fact"},
				Confidence:  0.9,
				Visibility:  memcore.Visibility{Mode: memcore.VisibilityScopedMembers},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(digest.Layers) != 2 {
		t.Fatalf("Layers = %+v, want 2 layer digests", digest.Layers)
	}
	if digest.Layers[0].Layer != memcore.MemoryLayerFact || digest.Layers[1].Layer != memcore.MemoryLayerWorld {
		t.Fatalf("layer order = %+v, want fact then world", digest.Layers)
	}
	if !strings.Contains(digest.Summary, "Facts:") || !strings.Contains(digest.Summary, "Organization World:") {
		t.Fatalf("Summary = %q, want sectioned compatibility summary", digest.Summary)
	}
	if len(digest.SourceMemoryRefs) != 2 || len(digest.SourceEvidenceRefs) != 1 {
		t.Fatalf("source refs = memory %+v evidence %+v", digest.SourceMemoryRefs, digest.SourceEvidenceRefs)
	}
	if digest.CreatedAt != now {
		t.Fatalf("CreatedAt = %v, want %v", digest.CreatedAt, now)
	}
}
