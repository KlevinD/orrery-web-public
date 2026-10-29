package missioncontrol

import "errors"

var (
	ErrProjectionNotFound    = errors.New("mission control projection not found")
	ErrInvalidViewer         = errors.New("invalid mission control viewer")
	ErrServiceDependency     = errors.New("mission control service dependency is not configured")
	ErrInvalidRequest        = errors.New("invalid mission control request")
	ErrConflict              = errors.New("mission control state conflict")
	ErrAgentRunAlreadyActive = errors.New("an agent run is already active for the task")
	ErrAgentRunMissing       = errors.New("agent run does not exist")
	ErrAgentRunTerminal      = errors.New("agent run is already terminal")
)
