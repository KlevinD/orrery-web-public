package tool

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type AuditEventType string

const (
	AuditToolResolved      AuditEventType = "tool_resolved"
	AuditToolBindingDenied AuditEventType = "tool_binding_denied"
)

type AuditEvent struct {
	Type       AuditEventType
	ToolID     agentcore.ToolID
	AgentID    agentcore.AgentID
	Reason     string
	OccurredAt time.Time
}

type AuditRecorder interface {
	RecordToolAudit(ctx context.Context, event AuditEvent) error
}
