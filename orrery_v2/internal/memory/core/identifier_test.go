package core

import (
	"strings"
	"testing"
)

func TestValidIdentifierEnforcesAuthorizationCompatibleLimit(t *testing.T) {
	if !ValidIdentifier("memory-1") {
		t.Fatal("expected ordinary memory identifier to be valid")
	}
	if ValidIdentifier("") || ValidIdentifier(strings.Repeat("x", MaxIdentifierBytes+1)) {
		t.Fatal("expected empty and oversized memory identifiers to be invalid")
	}
}

func TestRecordIdentifierForCandidateIsStableAndBounded(t *testing.T) {
	if got := RecordIdentifierForCandidate("candidate-1"); got != "memory.record.candidate-1" {
		t.Fatalf("short candidate record ID = %q", got)
	}
	candidateID := strings.Repeat("x", MaxIdentifierBytes)
	first := RecordIdentifierForCandidate(candidateID)
	second := RecordIdentifierForCandidate(candidateID)
	if first != second || !strings.HasPrefix(first, "memory.record.sha256.") || !ValidIdentifier(first) {
		t.Fatalf("bounded record ID = %q / %q", first, second)
	}
}

func TestBoundedIdentifierHashesOversizedValue(t *testing.T) {
	value := strings.Repeat("source-memory.", 60)
	first := BoundedIdentifier(value, "candidate.experience")
	second := BoundedIdentifier(value, "candidate.experience")
	if first != second || !strings.HasPrefix(first, "candidate.experience.sha256.") || !ValidIdentifier(first) {
		t.Fatalf("bounded identifier = %q / %q", first, second)
	}
}
