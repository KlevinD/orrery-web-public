package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/retrievalevidence"
)

func TestRetrievalEligibilitySeparatesRetentionFromBusinessTruth(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		edit          func(*MemoryRecord)
		eligible, due bool
	}{
		{"active", func(*MemoryRecord) {}, true, false},
		{"disabled", func(r *MemoryRecord) { r.DisabledAt = &now }, false, false},
		{"future", func(r *MemoryRecord) { r.ValidFrom = now.Add(time.Nanosecond) }, false, false},
		{"start boundary", func(r *MemoryRecord) { r.ValidFrom = now }, true, false},
		{"retention boundary", func(r *MemoryRecord) { r.ExpiresAt = now }, false, true},
		{"retained reference", func(r *MemoryRecord) { r.ExpiresAt = now; r.ReferenceCount = 1 }, true, false},
		{"retained sensitive", func(r *MemoryRecord) { r.ExpiresAt = now; r.Sensitivity = SensitivitySecret }, true, false},
		{"archived", func(r *MemoryRecord) { r.LifecycleStatus = MemoryLifecycleArchived }, false, false},
		{"deleted", func(r *MemoryRecord) { r.LifecycleStatus = MemoryLifecycleDeleted }, false, false},
		{"anonymized", func(r *MemoryRecord) { r.LifecycleStatus = MemoryLifecycleAnonymized }, false, false},
		{"proposed", func(r *MemoryRecord) { r.LifecycleStatus = MemoryLifecycleProposed }, false, false},
		{"provenance is not dependency", func(r *MemoryRecord) {
			r.LineageRefs = []string{"expired-source"}
			r.SourceEvidenceRefs = []string{"withdrawn-assertion"}
		}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := MemoryRecord{ID: "memory", LifecycleStatus: MemoryLifecycleActive, Sensitivity: SensitivityNormal}
			tc.edit(&r)
			if got := RetrievalEligible(r, now); got != tc.eligible {
				t.Fatalf("eligible=%v, want %v", got, tc.eligible)
			}
			if got := RetentionDue(r, now); got != tc.due {
				t.Fatalf("retention due=%v, want %v", got, tc.due)
			}
		})
	}
}

func TestMemoryEvidenceNeverPromotesProvenanceToTruth(t *testing.T) {
	now := time.Now().UTC()
	r := MemoryRecord{ID: "experience", ProfileID: "profile", Facts: []string{"worked together last year"}, ReferenceCount: 1, ExpiresAt: now,
		ContradictsRefs: []string{"private-other-memory"}, LineageRefs: []string{"deleted-provenance"}}
	item := RetrievalEvidence(r, now)
	if item.Purpose != retrievalevidence.PurposeHistorical || item.TruthState != retrievalevidence.TruthDisputed || len(item.Assertions) != 1 {
		t.Fatalf("evidence=%+v", item)
	}
	body, _ := json.Marshal(item)
	if strings.Contains(string(body), "private-other-memory") || strings.Contains(string(body), "worked together last year") {
		t.Fatalf("manifest leaked IDs or duplicated source text: %s", body)
	}
	changed := r
	changed.Facts = []string{"different"}
	if RetrievalEvidence(changed, now).Source.Version == item.Source.Version {
		t.Fatal("changed content reused source version")
	}
	if RetrievalEvidence(r, now.Add(time.Second)).InputHash != item.InputHash {
		t.Fatal("checking unchanged input invented a new version")
	}
}
