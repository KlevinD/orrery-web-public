package distillation

import (
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func TestExperienceExtractorBuildsCandidateFromRepeatedFactPattern(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	extractor := NewExperienceExtractor(ExperienceExtractorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		factRecord("memory-qa-1", "qa", "QA rejected because acceptance evidence was missing.", []string{"qa", "rejected"}, now),
		factRecord("memory-qa-2", "qa", "QA rejected again for missing regression evidence.", []string{"qa", "rejected"}, now),
	}

	result, err := extractor.Extract(records, ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	candidate := result.Candidates[0]
	if candidate.ProposedLayer != memcore.MemoryLayerExperience || candidate.ProposedScope.Kind != memcore.ScopeProject {
		t.Fatalf("candidate layer/scope = %+v", candidate)
	}
	if len(candidate.LineageRefs) != 2 || candidate.LineageRefs[0] != "memory:memory-qa-1" || candidate.LineageRefs[1] != "memory:memory-qa-2" {
		t.Fatalf("lineage refs = %+v", candidate.LineageRefs)
	}
	if len(candidate.Lessons) == 0 || candidate.Summary == "" || candidate.Status != memcore.CandidateAccepted {
		t.Fatalf("candidate content/status = %+v", candidate)
	}
	if len(candidate.Warnings) == 0 {
		t.Fatalf("candidate warnings = %+v", candidate.Warnings)
	}
	if candidate.ReviewRequired || candidate.ReviewReason != "" || candidate.Sensitivity != memcore.SensitivityNormal {
		t.Fatalf("candidate review/sensitivity = %+v", candidate)
	}
	if candidate.Confidence != 0.7 {
		t.Fatalf("candidate confidence = %v", candidate.Confidence)
	}
	if result.SourceMemoryIDs[0] != "memory-qa-1" || result.SourceMemoryIDs[1] != "memory-qa-2" {
		t.Fatalf("source ids = %+v", result.SourceMemoryIDs)
	}
}

func TestExperienceExtractorAcceptsFactsMatchedByTag(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	extractor := NewExperienceExtractor(ExperienceExtractorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		factRecord("memory-tag-1", "", "QA rejected because evidence was missing.", []string{"qa"}, now),
		factRecord("memory-tag-2", "", "QA rejected again because regression proof was missing.", []string{"qa"}, now),
	}

	result, err := extractor.Extract(records, ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err != nil {
		t.Fatalf("Extract tag-only facts: %v", err)
	}
	if len(result.Candidates) != 1 || len(result.Candidates[0].LineageRefs) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestExperienceExtractorBoundsCandidateIDForLargeSourceSets(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	extractor := NewExperienceExtractor(ExperienceExtractorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		factRecord("memory-"+strings.Repeat("a", 300), "qa", "First repeated fact.", []string{"qa"}, now),
		factRecord("memory-"+strings.Repeat("b", 300), "qa", "Second repeated fact.", []string{"qa"}, now),
	}
	req := ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	}

	first, err := extractor.Extract(records, req)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	second, err := extractor.Extract(records, req)
	if err != nil {
		t.Fatalf("Extract replay: %v", err)
	}
	got := first.Candidates[0].ID
	if !strings.HasPrefix(got, "candidate.experience.sha256.") || !memcore.ValidIdentifier(got) {
		t.Fatalf("bounded candidate ID = %q", got)
	}
	if second.Candidates[0].ID != got {
		t.Fatalf("candidate ID is not stable: %q != %q", second.Candidates[0].ID, got)
	}
}

func TestExperienceExtractorRejectsSingleFactAndSensitiveBroadScope(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	extractor := NewExperienceExtractor(ExperienceExtractorOptions{Now: func() time.Time { return now }})

	_, err := extractor.Extract([]memcore.MemoryRecord{factRecord("memory-1", "qa", "One fact only.", []string{"qa"}, now)}, ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err == nil {
		t.Fatalf("Extract single fact returned nil error")
	}

	sensitive := factRecord("memory-sensitive-1", "qa", "Legal issue.", []string{"qa"}, now)
	sensitive.Sensitivity = memcore.SensitivityLegal
	_, err = extractor.Extract([]memcore.MemoryRecord{sensitive, factRecord("memory-sensitive-2", "qa", "Legal issue repeated.", []string{"qa"}, now)}, ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err == nil {
		t.Fatalf("Extract sensitive broad scope returned nil error")
	}
}

func TestExperienceExtractorRejectsUnsupportedTargetScope(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	extractor := NewExperienceExtractor(ExperienceExtractorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		factRecord("memory-qa-1", "qa", "QA rejected because acceptance evidence was missing.", []string{"qa"}, now),
		factRecord("memory-qa-2", "qa", "QA rejected again for missing regression evidence.", []string{"qa"}, now),
	}

	_, err := extractor.Extract(records, ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err == nil {
		t.Fatalf("Extract mission-scope experience returned nil error")
	}
}

func TestExperienceExtractorIgnoresUnsupportedSourceScope(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	extractor := NewExperienceExtractor(ExperienceExtractorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		factRecord("memory-agent-1", "qa", "Private agent fact.", []string{"qa"}, now),
		factRecord("memory-agent-2", "qa", "Another private agent fact.", []string{"qa"}, now),
	}
	for i := range records {
		records[i].Scope = memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"}
	}

	_, err := extractor.Extract(records, ExperienceRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:       "qa",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err == nil {
		t.Fatalf("Extract agent-source facts returned nil error")
	}
}

func factRecord(id, topic, summary string, tags []string, now time.Time) memcore.MemoryRecord {
	return memcore.MemoryRecord{
		ID:                 id,
		ProfileID:          agentcore.MemoryProfileID("profile-1"),
		CandidateID:        "candidate-" + id,
		Layer:              memcore.MemoryLayerFact,
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Topic:              topic,
		Summary:            summary,
		Facts:              []string{summary},
		Tags:               tags,
		SourceRefs:         []string{"qa_result:" + id},
		SourceEvidenceRefs: []string{"evidence:" + id},
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDRaw,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         1,
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}
