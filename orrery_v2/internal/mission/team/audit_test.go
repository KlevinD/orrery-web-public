package team

import "testing"

func TestStaffingAuditStoresSelectedRationaleAndRejectedCandidates(t *testing.T) {
	audit := StaffingAudit{
		ID:                "audit-001",
		MissionID:         "mission-001",
		RequestID:         "request-001",
		DecisionID:        "decision-001",
		SelectedRationale: "balanced quality risk and cost",
		RejectedEvaluations: []CandidateEvaluation{{
			CandidateProfileID: "profile-busy",
			RejectionReasons:   []PolicyCode{CodeAvailabilityDenied},
		}},
	}
	if audit.SelectedRationale == "" || len(audit.RejectedEvaluations) != 1 {
		t.Fatalf("audit lacks staffing evidence: %#v", audit)
	}
}
