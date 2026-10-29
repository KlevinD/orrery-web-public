package core

import (
	"fmt"
	"time"

	"orrery_v2/internal/retrievalevidence"
)

// RetentionDue mirrors the existing archive policy. A retention exception
// preserves a historical memory; it does not certify a current business fact.
func RetentionDue(record MemoryRecord, now time.Time) bool {
	if record.ExpiresAt.IsZero() || now.Before(record.ExpiresAt) || record.ReferenceCount > 0 || NormalizeSensitivity(record.Sensitivity) != SensitivityNormal {
		return false
	}
	switch record.LifecycleStatus {
	case MemoryLifecycleActive, MemoryLifecycleSettled, MemoryLifecycleDistilled:
		return true
	default:
		return false
	}
}

func RetrievalEligible(record MemoryRecord, now time.Time) bool {
	if record.DisabledAt != nil || (!record.ValidFrom.IsZero() && now.Before(record.ValidFrom)) {
		return false
	}
	switch record.LifecycleStatus {
	case MemoryLifecycleProposed, MemoryLifecycleArchived, MemoryLifecycleDeleted, MemoryLifecycleAnonymized:
		return false
	}
	return !RetentionDue(record.Normalized(), now)
}

func RetrievalEvidence(record MemoryRecord, now time.Time) retrievalevidence.Item {
	item := retrievalevidence.Item{
		Purpose: retrievalevidence.PurposeMemory, TruthState: retrievalevidence.TruthUnknown,
		Source:      retrievalevidence.Source{Kind: "memory_record", ID: record.ID, ContainerID: string(record.ProfileID), Version: retrievalevidence.Fingerprint(record)},
		SourceState: "record_snapshot",
	}
	if !record.ValidFrom.IsZero() {
		value := record.ValidFrom.UTC()
		item.ValidFrom = &value
	}
	if !record.ExpiresAt.IsZero() {
		value := record.ExpiresAt.UTC()
		item.RetentionUntil = &value
		if !now.Before(record.ExpiresAt) {
			item.Purpose = retrievalevidence.PurposeHistorical
		}
	}
	// Contradiction references are a signal to the model, not a host adjudication.
	// Do not expose their IDs, which may identify unreadable memories.
	if len(record.ContradictsRefs) > 0 {
		item.TruthState = retrievalevidence.TruthDisputed
	}
	for _, group := range []struct {
		kind  string
		texts []string
	}{
		{"fact", record.Facts}, {"preference", record.Preferences}, {"decision", record.Decisions},
	} {
		for i, text := range group.texts {
			item.Assertions = append(item.Assertions, retrievalevidence.Assertion{ID: retrievalevidence.Fingerprint([]string{record.ID, group.kind, fmt.Sprint(i)}), Kind: group.kind, TextHash: retrievalevidence.Fingerprint(text), TruthState: item.TruthState})
		}
	}
	return retrievalevidence.Seal(item, now)
}
