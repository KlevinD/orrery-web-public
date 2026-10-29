package core

import "time"

type Mission struct {
	ID                MissionID
	Title             string
	FinalGoal         string
	BusinessDirection string
	RequesterRef      RequesterRef
	Status            MissionStatus
	CreatedAt         time.Time
}

func ValidateMission(m Mission) []ValidationError {
	var errs []ValidationError
	if m.ID.IsZero() {
		errs = append(errs, validationError(CodeMissingRequiredField, "mission_id", "mission_id is required"))
	}
	if m.FinalGoal == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "final_goal", "final_goal is required"))
	}
	if !m.Status.IsValid() {
		errs = append(errs, validationError(CodeInvalidState, "status", "status must be a known mission status"))
	}
	return errs
}
