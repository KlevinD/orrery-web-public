package responsibility

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// ContextRequest identifies the responsibility assignment whose source facts
// must be frozen before an Agent run is queued.
type ContextRequest struct {
	TenantID           string
	ActorMemberID      string
	AgentProfileID     string
	MissionID          string
	TaskID             string
	ResponsibilityID   string
	AssignmentVersion int64
}

// ContextProvider is deliberately read-only. People owns responsibility facts;
// Orrery only consumes the resolved snapshot for execution and audit.
type ContextProvider interface {
	ResolveResponsibilityContext(context.Context, ContextRequest) (agentcore.ResponsibilityContext, error)
}

type RuntimeContext = agentcore.ResponsibilityContext

func fallbackRuntimeContext(request ContextRequest) agentcore.ResponsibilityContext {
	responsibilityID := strings.TrimSpace(request.ResponsibilityID)
	version := request.AssignmentVersion
	if version < 1 {
		version = 1
	}
	return agentcore.ResponsibilityContext{
		ContextID:             fmt.Sprintf("orrery-responsibility-context:%s:v%d", responsibilityID, version),
		ResponsibilityID:      responsibilityID,
		ResponsibilityVersion: version,
		Source:                "orrery.responsibility_assignment",
		ActorMemberID:         strings.TrimSpace(request.ActorMemberID),
		AgentProfileID:        strings.TrimSpace(request.AgentProfileID),
		MissionID:             strings.TrimSpace(request.MissionID),
		TaskID:                strings.TrimSpace(request.TaskID),
		SourceRefs: []string{
			"orrery:responsibility-assignment:" + responsibilityID,
		},
	}
}

func normalizeRuntimeContext(value agentcore.ResponsibilityContext, request ContextRequest) agentcore.ResponsibilityContext {
	fallback := fallbackRuntimeContext(request)
	if strings.TrimSpace(value.ContextID) == "" {
		value.ContextID = fallback.ContextID
	}
	if strings.TrimSpace(value.ResponsibilityID) == "" {
		value.ResponsibilityID = fallback.ResponsibilityID
	}
	if value.ResponsibilityVersion < 1 {
		value.ResponsibilityVersion = fallback.ResponsibilityVersion
	}
	if strings.TrimSpace(value.Source) == "" {
		value.Source = fallback.Source
	}
	if strings.TrimSpace(value.ActorMemberID) == "" {
		value.ActorMemberID = fallback.ActorMemberID
	}
	if strings.TrimSpace(value.AgentProfileID) == "" {
		value.AgentProfileID = fallback.AgentProfileID
	}
	if strings.TrimSpace(value.MissionID) == "" {
		value.MissionID = fallback.MissionID
	}
	if strings.TrimSpace(value.TaskID) == "" {
		value.TaskID = fallback.TaskID
	}
	if len(value.SourceRefs) == 0 {
		value.SourceRefs = append([]string(nil), fallback.SourceRefs...)
	}
	return value
}
