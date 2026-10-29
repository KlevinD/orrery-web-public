package core

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestMemoryRecordNormalizedDefaultsLegacyRecordToFactActive(t *testing.T) {
	record := MemoryRecord{
		ID:        "memory-legacy",
		ProfileID: "profile-1",
		Scope:     MemoryScope{Kind: ScopeAgent, ID: "agent-1"},
		Summary:   "legacy summary",
	}

	normalized := record.Normalized()

	if normalized.Layer != MemoryLayerFact {
		t.Fatalf("Layer = %q, want fact", normalized.Layer)
	}
	if normalized.LifecycleStatus != MemoryLifecycleActive {
		t.Fatalf("LifecycleStatus = %q, want active", normalized.LifecycleStatus)
	}
	if normalized.Sensitivity != SensitivityNormal {
		t.Fatalf("Sensitivity = %q, want normal", normalized.Sensitivity)
	}
	if normalized.DeIDLevel != DeIDRaw {
		t.Fatalf("DeIDLevel = %q, want raw", normalized.DeIDLevel)
	}
}

func TestValidateMemoryRecordRequiresLineageForExperienceAndWorld(t *testing.T) {
	record := MemoryRecord{
		ID:        "memory-exp",
		ProfileID: "profile-1",
		Scope:     MemoryScope{Kind: ScopeOrganization, ID: "org-1"},
		Layer:     MemoryLayerExperience,
		Summary:   "lesson",
		CreatedBy: agentcore.AgentID("agent-1"),
	}

	errs := ValidateMemoryRecord(record)

	if len(errs) == 0 {
		t.Fatalf("ValidateMemoryRecord returned no errors for experience without lineage")
	}
}

func TestValidateMemoryRecordAllowsEvidenceBackedExperienceAndWorld(t *testing.T) {
	records := []MemoryRecord{
		{
			ID:                 "memory-exp",
			ProfileID:          "profile-1",
			Scope:              MemoryScope{Kind: ScopeOrganization, ID: "org-1"},
			Layer:              MemoryLayerExperience,
			Summary:            "lesson",
			SourceEvidenceRefs: []string{"evidence-1"},
			CreatedBy:          agentcore.AgentID("agent-1"),
		},
		{
			ID:                 "memory-world",
			ProfileID:          "profile-1",
			Scope:              MemoryScope{Kind: ScopeOrganization, ID: "org-1"},
			Layer:              MemoryLayerWorld,
			Summary:            "organization belief",
			SourceEvidenceRefs: []string{"evidence-1"},
			CreatedBy:          agentcore.AgentID("agent-1"),
		},
	}

	for _, record := range records {
		if errs := ValidateMemoryRecord(record); len(errs) != 0 {
			t.Fatalf("ValidateMemoryRecord(%s) = %+v, want no errors", record.ID, errs)
		}
	}
}

func TestValidateMemoryRecordRejectsRawSensitiveOrganizationWorld(t *testing.T) {
	record := MemoryRecord{
		ID:                 "memory-world",
		ProfileID:          "profile-1",
		Scope:              MemoryScope{Kind: ScopeOrganization, ID: "org-1"},
		Layer:              MemoryLayerWorld,
		Summary:            "organization belief",
		LineageRefs:        []string{"memory-exp-1"},
		Sensitivity:        SensitivityPersonal,
		DeIDLevel:          DeIDRaw,
		LifecycleStatus:    MemoryLifecycleActive,
		SourceEvidenceRefs: []string{"evidence-1"},
		CreatedBy:          agentcore.AgentID("agent-1"),
	}

	errs := ValidateMemoryRecord(record)

	if len(errs) == 0 {
		t.Fatalf("ValidateMemoryRecord returned no errors for raw sensitive organization world memory")
	}
}

func TestMemoryRecordCloneCopiesNewSlicesAndMaps(t *testing.T) {
	record := MemoryRecord{
		ID:              "memory-1",
		Layer:           MemoryLayerWorld,
		Lessons:         []string{"lesson"},
		LineageRefs:     []string{"lineage-1"},
		SupersedesRefs:  []string{"old-1"},
		ContradictsRefs: []string{"conflict-1"},
		Entities:        map[string]string{"repo": "orrery"},
	}

	clone := record.Clone()
	clone.Lessons[0] = "changed"
	clone.LineageRefs[0] = "changed"
	clone.SupersedesRefs[0] = "changed"
	clone.ContradictsRefs[0] = "changed"
	clone.Entities["repo"] = "changed"

	if record.Lessons[0] != "lesson" ||
		record.LineageRefs[0] != "lineage-1" ||
		record.SupersedesRefs[0] != "old-1" ||
		record.ContradictsRefs[0] != "conflict-1" ||
		record.Entities["repo"] != "orrery" {
		t.Fatalf("Clone shared mutable fields with original: %+v", record)
	}
}

func TestMemoryCandidateCloneCopiesPensieveFields(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	candidate := MemoryCandidate{
		ID:              "candidate-1",
		ProposedLayer:   MemoryLayerExperience,
		Lessons:         []string{"lesson"},
		Warnings:        []string{"warning"},
		Tags:            []string{"tag"},
		EvidenceRefs:    []string{"evidence-1"},
		LineageRefs:     []string{"memory-1"},
		SupersedesRefs:  []string{"memory-old"},
		ContradictsRefs: []string{"memory-conflict"},
		Entities:        map[string]string{"repo": "orrery"},
		Sensitivity:     SensitivityPersonal,
		DeIDLevel:       DeIDTeamSafe,
		Consent:         ConsentRequested,
		ReviewRequired:  true,
		ReviewReason:    "sensitive lesson",
		Confidence:      0.73,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	clone := candidate.Clone()
	clone.Lessons[0] = "changed"
	clone.Warnings[0] = "changed"
	clone.Tags[0] = "changed"
	clone.EvidenceRefs[0] = "changed"
	clone.LineageRefs[0] = "changed"
	clone.SupersedesRefs[0] = "changed"
	clone.ContradictsRefs[0] = "changed"
	clone.Entities["repo"] = "changed"

	if candidate.NormalizedLayer() != MemoryLayerExperience {
		t.Fatalf("NormalizedLayer = %q, want experience", candidate.NormalizedLayer())
	}
	if candidate.Lessons[0] != "lesson" ||
		candidate.Warnings[0] != "warning" ||
		candidate.Tags[0] != "tag" ||
		candidate.EvidenceRefs[0] != "evidence-1" ||
		candidate.LineageRefs[0] != "memory-1" ||
		candidate.SupersedesRefs[0] != "memory-old" ||
		candidate.ContradictsRefs[0] != "memory-conflict" ||
		candidate.Entities["repo"] != "orrery" {
		t.Fatalf("Clone shared mutable fields with original: %+v", candidate)
	}
	if clone.CreatedAt != now || clone.UpdatedAt != now {
		t.Fatalf("clone timestamps = %v/%v, want %v", clone.CreatedAt, clone.UpdatedAt, now)
	}
	if clone.Confidence != 0.73 {
		t.Fatalf("clone confidence = %v", clone.Confidence)
	}
}
