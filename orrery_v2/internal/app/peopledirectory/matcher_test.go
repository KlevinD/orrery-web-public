package peopledirectory

import (
	"errors"
	"testing"
)

func TestMatchSnapshotUsesEmployeeEmailThenKratosRelation(t *testing.T) {
	snapshot := Snapshot{ID: "complete", Source: SourceFeishu, TenantID: "tenant-1", Complete: true, Status: SnapshotStatusComplete}
	people := []PersonCandidate{
		{PersonID: "human-employee", EmployeeNumber: "E-001", EnterpriseEmail: "employee@example.com", FeishuSubject: "ou_other", Enabled: true},
		{PersonID: "human-email", EmployeeNumber: "E-002", EnterpriseEmail: "email@example.com", Enabled: true},
		{PersonID: "human-relation", EmployeeNumber: "E-003", EnterpriseEmail: "relation@example.com", FeishuSubject: "ou_relation", Enabled: true},
	}
	users := []DirectoryUser{
		{SourceRecordID: "ou_new_1", EmployeeNumber: " e-001 ", EnterpriseEmail: "UNUSED@EXAMPLE.COM", Active: true},
		{SourceRecordID: "ou_new_2", EmployeeNumber: "missing", EnterpriseEmail: "EMAIL@EXAMPLE.COM", Active: true},
		{SourceRecordID: "ou_relation", Active: true},
	}

	results, err := MatchSnapshot(snapshot, users, people)
	if err != nil {
		t.Fatalf("MatchSnapshot: %v", err)
	}
	assertMatch(t, results, "ou_new_1", MatchActionUpdate, MatchByEmployeeNumber, "human-employee")
	assertMatch(t, results, "ou_new_2", MatchActionUpdate, MatchByEnterpriseEmail, "human-email")
	assertMatch(t, results, "ou_relation", MatchActionUpdate, MatchByFeishuRelation, "human-relation")
}

func TestMatchSnapshotCreatesActiveUnmatchedAndConflictsOnContradiction(t *testing.T) {
	snapshot := Snapshot{ID: "complete", Source: SourceFeishu, TenantID: "tenant-1", Complete: true, Status: SnapshotStatusComplete}
	people := []PersonCandidate{
		{PersonID: "human-a", EmployeeNumber: "E-001", Enabled: true},
		{PersonID: "human-b", EnterpriseEmail: "b@example.com", Enabled: true},
		{PersonID: "human-duplicate-1", EmployeeNumber: "E-009", Enabled: true},
		{PersonID: "human-duplicate-2", EmployeeNumber: "e-009", Enabled: true},
	}
	users := []DirectoryUser{
		{SourceRecordID: "ou_new", EmployeeNumber: "E-100", EnterpriseEmail: "new@example.com", Active: true},
		{SourceRecordID: "ou_conflict", EmployeeNumber: "E-001", EnterpriseEmail: "b@example.com", Active: true},
		{SourceRecordID: "ou_ambiguous", EmployeeNumber: "E-009", Active: true},
		{SourceRecordID: "ou_unidentified", Active: true},
		{SourceRecordID: "ou_inactive_new", EmployeeNumber: "E-101", Active: false},
	}

	results, err := MatchSnapshot(snapshot, users, people)
	if err != nil {
		t.Fatalf("MatchSnapshot: %v", err)
	}
	assertMatch(t, results, "ou_new", MatchActionCreate, MatchByNewPerson, "")
	assertMatch(t, results, "ou_conflict", MatchActionConflict, MatchByConflict, "")
	assertMatch(t, results, "ou_ambiguous", MatchActionConflict, MatchByConflict, "")
	assertMatch(t, results, "ou_unidentified", MatchActionConflict, MatchByConflict, "")
	if result, ok := resultFor(results, "ou_unidentified"); !ok || result.ApplyStatus != ApplyStatusPendingReview {
		t.Fatalf("unidentified active user result = %+v, want pending review", result)
	}
	if result, ok := resultFor(results, "ou_inactive_new"); ok && result.Action != MatchActionIgnore {
		t.Fatalf("unmatched inactive result = %+v", result)
	}
}

func TestMatchSnapshotOnlyDisablesMissingPeopleFromCompleteSnapshot(t *testing.T) {
	people := []PersonCandidate{
		{PersonID: "human-present", Source: SourceFeishu, SourceRecordID: "ou_present", SourceTenantID: "tenant-1", EmployeeNumber: "E-001", Enabled: true},
		{PersonID: "human-missing", Source: SourceFeishu, SourceRecordID: "ou_missing", SourceTenantID: "tenant-1", EmployeeNumber: "E-002", Enabled: true},
		{PersonID: "human-other-tenant", Source: SourceFeishu, SourceRecordID: "ou_other", SourceTenantID: "tenant-2", Enabled: true},
	}
	users := []DirectoryUser{{SourceRecordID: "ou_present", EmployeeNumber: "E-001", Active: true}}

	partial := Snapshot{ID: "partial", Source: SourceFeishu, TenantID: "tenant-1", Status: SnapshotStatusScanning}
	if results, err := MatchSnapshot(partial, users, people); !errors.Is(err, ErrSnapshotIncomplete) || len(results) != 0 {
		t.Fatalf("partial results = %+v, err=%v", results, err)
	}

	complete := partial
	complete.Complete = true
	complete.Status = SnapshotStatusComplete
	results, err := MatchSnapshot(complete, users, people)
	if err != nil {
		t.Fatalf("complete MatchSnapshot: %v", err)
	}
	assertMatch(t, results, "ou_missing", MatchActionDisable, MatchByMissingFromSnapshot, "human-missing")
	if result, ok := resultFor(results, "ou_other"); ok && result.Action == MatchActionDisable {
		t.Fatalf("other tenant was disabled: %+v", result)
	}
}

func assertMatch(t *testing.T, results []MatchResult, sourceRecordID string, action MatchAction, by MatchKind, personID string) {
	t.Helper()
	result, ok := resultFor(results, sourceRecordID)
	if !ok {
		t.Fatalf("result for %s not found in %+v", sourceRecordID, results)
	}
	if result.Action != action || result.MatchKind != by || result.ProposedPersonID != personID {
		t.Fatalf("result for %s = %+v, want action=%s by=%s person=%s", sourceRecordID, result, action, by, personID)
	}
	if action == MatchActionConflict && result.ConflictReason == "" {
		t.Fatalf("conflict for %s lacks reason: %+v", sourceRecordID, result)
	}
}

func resultFor(results []MatchResult, sourceRecordID string) (MatchResult, bool) {
	for _, result := range results {
		if result.SourceRecordID == sourceRecordID {
			return result, true
		}
	}
	return MatchResult{}, false
}
