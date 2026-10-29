package distillation

import (
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func TestWorldConsolidatorBuildsOrganizationCandidateFromExperience(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	consolidator := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		experienceRecord("memory-exp-1", "quality", "Quality review repeatedly blocks releases when acceptance evidence is missing.", []string{"quality", "qa"}, now),
		experienceRecord("memory-exp-2", "quality", "Release work is faster when teams attach regression evidence before QA.", []string{"quality", "qa"}, now),
	}

	result, err := consolidator.Consolidate(records, WorldRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "quality",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	candidate := result.Candidates[0]
	if candidate.ProposedLayer != memcore.MemoryLayerWorld || candidate.ProposedScope.Kind != memcore.ScopeOrganization || candidate.ProposedScope.ID != "org-1" {
		t.Fatalf("candidate layer/scope = %+v", candidate)
	}
	if candidate.ProposedKind != agentcore.MemoryKindOrganization || candidate.Topic != "quality" {
		t.Fatalf("candidate kind/topic = %+v", candidate)
	}
	if len(candidate.LineageRefs) != 2 || candidate.LineageRefs[0] != "memory:memory-exp-1" || candidate.LineageRefs[1] != "memory:memory-exp-2" {
		t.Fatalf("lineage refs = %+v", candidate.LineageRefs)
	}
	if len(result.SourceMemoryIDs) != 2 || result.SourceMemoryIDs[0] != "memory-exp-1" || result.SourceMemoryIDs[1] != "memory-exp-2" {
		t.Fatalf("source ids = %+v", result.SourceMemoryIDs)
	}
	if candidate.Summary == "" || candidate.Narrative == "" || len(candidate.Lessons) == 0 {
		t.Fatalf("candidate content = %+v", candidate)
	}
	if candidate.ReviewRequired || candidate.ReviewReason != "" || candidate.Status != memcore.CandidateAccepted {
		t.Fatalf("candidate review/status = %+v", candidate)
	}
	if candidate.Sensitivity != memcore.SensitivityNormal || candidate.DeIDLevel != memcore.DeIDTeamSafe {
		t.Fatalf("candidate sensitivity/de-id = %+v", candidate)
	}
	if candidate.Confidence <= 0.7 || candidate.Confidence > 1 {
		t.Fatalf("candidate confidence = %v", candidate.Confidence)
	}
}

func TestWorldConsolidatorSupersedesOlderWorldMemory(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	consolidator := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }})
	older := worldRecord("memory-world-old", "quality", "Quality review is mostly manual and undocumented.", now.Add(-30*24*time.Hour))
	records := []memcore.MemoryRecord{
		experienceRecord("memory-exp-1", "quality", "Quality review now requires automated acceptance evidence.", []string{"quality"}, now),
		experienceRecord("memory-exp-2", "quality", "Teams now attach regression evidence before QA approval.", []string{"quality"}, now),
		older,
	}

	result, err := consolidator.Consolidate(records, WorldRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "quality",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	candidate := result.Candidates[0]
	if len(candidate.SupersedesRefs) != 1 || candidate.SupersedesRefs[0] != "memory:memory-world-old" {
		t.Fatalf("supersedes refs = %+v", candidate.SupersedesRefs)
	}
	if len(candidate.ContradictsRefs) != 0 {
		t.Fatalf("contradicts refs = %+v", candidate.ContradictsRefs)
	}
}

func TestWorldConsolidatorRecordsContradictingWorldMemory(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	consolidator := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }})
	conflict := worldRecord("memory-world-conflict", "quality", "Quality review must not require automated evidence.", now.Add(-5*24*time.Hour))
	records := []memcore.MemoryRecord{
		experienceRecord("memory-exp-1", "quality", "Quality review must require automated evidence before release.", []string{"quality"}, now),
		experienceRecord("memory-exp-2", "quality", "QA must require automated regression evidence for release approval.", []string{"quality"}, now),
		conflict,
	}

	result, err := consolidator.Consolidate(records, WorldRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "quality",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	candidate := result.Candidates[0]
	if len(candidate.ContradictsRefs) != 1 || candidate.ContradictsRefs[0] != "memory:memory-world-conflict" {
		t.Fatalf("contradicts refs = %+v", candidate.ContradictsRefs)
	}
	if len(candidate.SupersedesRefs) != 0 {
		t.Fatalf("supersedes refs = %+v", candidate.SupersedesRefs)
	}
	if len(candidate.Warnings) == 0 {
		t.Fatalf("candidate warnings = %+v", candidate.Warnings)
	}
}

func TestWorldConsolidatorRejectsSensitiveOrganizationSources(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	consolidator := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }})
	sensitive := experienceRecord("memory-exp-sensitive", "team", "A named employee performance issue should not become org world memory.", []string{"team"}, now)
	sensitive.Sensitivity = memcore.SensitivityPerformance
	records := []memcore.MemoryRecord{
		sensitive,
		experienceRecord("memory-exp-2", "team", "Team coordination patterns improved with async handoff.", []string{"team"}, now),
	}

	_, err := consolidator.Consolidate(records, WorldRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "team",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if !errors.Is(err, ErrSensitiveScope) {
		t.Fatalf("Consolidate sensitive err = %v, want ErrSensitiveScope", err)
	}
}

func TestWorldConsolidatorRejectsNonOrganizationTarget(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	consolidator := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		experienceRecord("memory-exp-1", "quality", "Quality review requires acceptance evidence.", []string{"quality"}, now),
	}

	_, err := consolidator.Consolidate(records, WorldRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:       "quality",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Consolidate project target err = %v, want ErrInvalidRequest", err)
	}
}

func TestWorldConsolidatorDoesNotCopyRawSourceIdentities(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	consolidator := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }})
	records := []memcore.MemoryRecord{
		experienceRecord("memory-exp-alice", "team", "Alice was blocked by handoff confusion.", []string{"team", "Alice"}, now),
		experienceRecord("memory-exp-bob", "team", "Bob resolved the handoff gap with async notes.", []string{"team", "Bob"}, now),
	}

	result, err := consolidator.Consolidate(records, WorldRequest{
		TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:       "team",
		CreatedBy:   agentcore.AgentID("distiller-1"),
	})
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	candidate := result.Candidates[0]
	combined := strings.Join(append([]string{candidate.Summary, candidate.Narrative}, candidate.Lessons...), " ")
	if strings.Contains(combined, "Alice") || strings.Contains(combined, "Bob") {
		t.Fatalf("candidate copied raw source identity: %+v", candidate)
	}
	if containsString(candidate.Tags, "Alice") || containsString(candidate.Tags, "Bob") {
		t.Fatalf("candidate copied raw source identity tag: %+v", candidate.Tags)
	}
}

func experienceRecord(id, topic, summary string, tags []string, now time.Time) memcore.MemoryRecord {
	return memcore.MemoryRecord{
		ID:                 id,
		ProfileID:          agentcore.MemoryProfileID("profile-1"),
		CandidateID:        "candidate-" + id,
		Layer:              memcore.MemoryLayerExperience,
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:              topic,
		Summary:            summary,
		Narrative:          summary,
		Lessons:            []string{summary},
		Tags:               tags,
		LineageRefs:        []string{"memory:fact-" + id},
		SourceEvidenceRefs: []string{"evidence:" + id},
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDTeamSafe,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         0.8,
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func worldRecord(id, topic, summary string, now time.Time) memcore.MemoryRecord {
	return memcore.MemoryRecord{
		ID:              id,
		ProfileID:       agentcore.MemoryProfileID("profile-world"),
		CandidateID:     "candidate-" + id,
		Layer:           memcore.MemoryLayerWorld,
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:           topic,
		Summary:         summary,
		Narrative:       summary,
		Lessons:         []string{summary},
		LineageRefs:     []string{"memory:source-" + id},
		Sensitivity:     memcore.SensitivityNormal,
		DeIDLevel:       memcore.DeIDTeamSafe,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Confidence:      0.7,
		CreatedBy:       agentcore.AgentID("reviewer-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
