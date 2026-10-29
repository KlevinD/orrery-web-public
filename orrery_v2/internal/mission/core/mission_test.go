package core

import (
	"testing"
	"time"

	contracterrors "orrery_v2/internal/contract/errors"
)

func TestValidateMissionAcceptsCompleteMission(t *testing.T) {
	mission := Mission{
		ID:                MissionID("mission-001"),
		Title:             "Launch agent mission control",
		FinalGoal:         "Coordinate an agent company around a user goal",
		BusinessDirection: "software delivery",
		RequesterRef:      RequesterRef{ID: "user-001", Kind: RequesterKindHuman},
		Status:            MissionStatusDraft,
		CreatedAt:         time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC),
	}

	if errs := ValidateMission(mission); len(errs) != 0 {
		t.Fatalf("ValidateMission returned errors: %#v", errs)
	}
}

func TestValidateMissionRequiresIdentityGoalAndStatus(t *testing.T) {
	mission := Mission{}

	errs := ValidateMission(mission)

	assertValidationError(t, errs, CodeMissingRequiredField, "mission_id")
	assertValidationError(t, errs, CodeMissingRequiredField, "final_goal")
	assertValidationError(t, errs, CodeInvalidState, "status")
}

func TestValidateMissionRejectsUnknownStatus(t *testing.T) {
	mission := Mission{
		ID:                MissionID("mission-001"),
		FinalGoal:         "Coordinate an agent company",
		BusinessDirection: "operations",
		Status:            MissionStatus("suspended"),
	}

	errs := ValidateMission(mission)

	assertValidationError(t, errs, CodeInvalidState, "status")
}

func TestConflictErrorCarriesStableCodeAndFields(t *testing.T) {
	err := NewConflictError("mission_status_conflict", "status", "mission status changed")

	if err.Code != CodeConflict || err.ConflictCode != "mission_status_conflict" || err.Field != "status" {
		t.Fatalf("unexpected conflict error: %#v", err)
	}
}

func TestMissionValidationErrorsConvertToSharedContractError(t *testing.T) {
	err := validationError(CodeInvalidState, "status", "invalid status")
	contractErr := err.ContractError()

	if contractErr.Code != contracterrors.CodeInvalidState || contractErr.Field != "status" {
		t.Fatalf("unexpected shared contract error: %#v", contractErr)
	}
}
