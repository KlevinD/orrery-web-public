package skill

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type AuditEventType string

const AuditSkillResolved AuditEventType = "skill_resolved"

type AuditEvent struct {
	Type       AuditEventType
	SkillID    agentcore.SkillID
	Reason     string
	OccurredAt time.Time
}

type AuditRecorder interface {
	RecordSkillAudit(ctx context.Context, event AuditEvent) error
}
