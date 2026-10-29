package lifecycle

import (
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type SignalID string
type CorrelationID string
type CausationID string

type SignalEnvelope struct {
	SignalID       SignalID
	Name           SignalName
	TenantID       string
	MissionID      missioncore.MissionID
	AgentID        agentcore.AgentID
	AgentProfileID agentcore.AgentProfileID
	TaskID         missioncore.TaskID
	RunID          agentcore.AgentRunID
	CorrelationID  CorrelationID
	CausationID    CausationID
	Attempt        int
	EmittedAt      time.Time
	Payload        SignalPayload
	Metadata       map[string]string
}

const MaxMetadataValueBytes = 512

func (s SignalEnvelope) Validate() error {
	var errs []error
	if s.SignalID == "" {
		errs = append(errs, SignalValidationError{Field: "signal_id"})
	}
	if s.Name == "" {
		errs = append(errs, SignalValidationError{Field: "name"})
	} else if !IsKnownSignalName(s.Name) {
		errs = append(errs, SignalValidationError{Field: "name"})
	}
	if s.MissionID.IsZero() {
		errs = append(errs, SignalValidationError{Field: "mission_id"})
	}
	if s.AgentID.IsZero() {
		errs = append(errs, SignalValidationError{Field: "agent_id"})
	}
	if s.AgentProfileID.IsZero() {
		errs = append(errs, SignalValidationError{Field: "agent_profile_id"})
	}
	if s.TaskID.IsZero() {
		errs = append(errs, SignalValidationError{Field: "task_id"})
	}
	if s.RunID.IsZero() {
		errs = append(errs, SignalValidationError{Field: "run_id"})
	}
	if s.CorrelationID == "" {
		errs = append(errs, SignalValidationError{Field: "correlation_id", Cause: ErrMissingCorrelationID})
	}
	if s.Attempt < 1 {
		errs = append(errs, SignalValidationError{Field: "attempt"})
	}
	if s.EmittedAt.IsZero() {
		errs = append(errs, SignalValidationError{Field: "emitted_at"})
	}
	if s.Payload != nil {
		if err := s.Payload.ValidateSignalPayload(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := validateMetadata(s.Metadata); err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(append([]error{ErrInvalidSignal}, errs...)...)
}

func (s SignalEnvelope) WithPayload(payload SignalPayload) SignalEnvelope {
	s.Payload = payload
	return s
}

func validateMetadata(metadata map[string]string) error {
	var errs []error
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" {
			errs = append(errs, SignalValidationError{Field: "metadata"})
			continue
		}
		if isSensitiveText(key) {
			errs = append(errs, SignalValidationError{Field: "metadata." + key, Cause: ErrInvalidSignal})
		}
		if len(value) > MaxMetadataValueBytes {
			errs = append(errs, SignalValidationError{Field: "metadata." + key, Cause: ErrInvalidSignal})
		}
		if isSensitiveText(value) {
			errs = append(errs, SignalValidationError{Field: "metadata." + key, Cause: ErrInvalidSignal})
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func isSensitiveText(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(value, "-", "_"), " ", "_"))
	for _, marker := range []string{
		"secret",
		"token",
		"api_key",
		"apikey",
		"password",
		"authorization",
		"credential",
		"private_key",
		"access_key",
		"refresh",
		"bearer",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
