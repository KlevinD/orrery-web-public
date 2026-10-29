package missioncontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func (s *CommandService) recordAudit(ctx context.Context, event AuditEvent) error {
	if s == nil || s.audit == nil {
		return nil
	}
	if event.AuditID == "" && strings.TrimSpace(event.CorrelationID) != "" {
		event.AuditID = missionCommandAuditID(event)
	}
	return s.audit.RecordAuditEvent(ctx, event)
}

func (s *CommandService) recordViewerAudit(ctx context.Context, viewer ViewerRef, event AuditEvent) error {
	event.TenantID = viewer.Tenant()
	return s.recordAudit(ctx, event)
}

func missionCommandAuditID(event AuditEvent) string {
	payload := strings.Join([]string{
		strings.TrimSpace(event.TenantID), strings.TrimSpace(event.ActorID), strings.TrimSpace(event.EntityType),
		strings.TrimSpace(event.EntityID), strings.TrimSpace(event.Action), strings.TrimSpace(event.CorrelationID),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return "audit-mission-command-" + hex.EncodeToString(sum[:12])
}
