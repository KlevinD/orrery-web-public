package lifecycle

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

type AgentRunState struct {
	TenantID       string
	MissionID      missioncore.MissionID
	AgentID        agentcore.AgentID
	AgentProfileID agentcore.AgentProfileID
	TaskID         missioncore.TaskID
	RunID          agentcore.AgentRunID
	Attempt        int

	TaskContract missiontask.TaskContract
	ContextPack  agentcore.ContextPack

	RuntimeRequestRef  string
	RuntimeResponseRef string
	AgentResult        agentcore.AgentResult

	Errors               []error
	Warnings             []string
	DegradedCapabilities []string
	Metadata             map[string]string

	StartedAt  time.Time
	DeadlineAt time.Time
}

func (s *AgentRunState) AddError(err error) {
	if err == nil {
		return
	}
	s.Errors = append(s.Errors, err)
}

func (s *AgentRunState) AddWarning(warning string) {
	if warning == "" {
		return
	}
	s.Warnings = append(s.Warnings, warning)
}

func (s *AgentRunState) MarkDegraded(capability string) {
	if capability == "" {
		return
	}
	s.DegradedCapabilities = append(s.DegradedCapabilities, capability)
}

func (s *AgentRunState) SetMetadata(key, value string) {
	if key == "" {
		return
	}
	if s.Metadata == nil {
		s.Metadata = map[string]string{}
	}
	s.Metadata[key] = value
}
