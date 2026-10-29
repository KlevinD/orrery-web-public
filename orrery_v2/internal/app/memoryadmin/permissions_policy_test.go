package memoryadmin

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func TestCanReadRecordAppliesPermissionSensitivityAndScope(t *testing.T) {
	record := memcore.MemoryRecord{
		ID:         "memory-1",
		Scope:      memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Visibility: memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
	}
	viewer := ViewerRef{ViewerID: "reader", MissionIDs: []string{"mission-1"}, PermissionScope: []string{PermissionMemoryRead}}
	if !CanReadRecord(viewer, record) {
		t.Fatal("scoped reader was denied")
	}
	viewer.MissionIDs = []string{"mission-2"}
	if CanReadRecord(viewer, record) {
		t.Fatal("wrong-scope reader was allowed")
	}
	viewer.PermissionScope = []string{PermissionMemoryRead, PermissionMemoryAuditRead}
	if !CanReadRecord(viewer, record) {
		t.Fatal("memory auditor was denied cross-scope read")
	}
	viewer.MissionIDs = []string{"mission-1"}
	viewer.PermissionScope = nil
	if CanReadRecord(viewer, record) {
		t.Fatal("reader without memory.read was allowed")
	}
}

func TestCanReadRecordAllowsAuditorToInspectOwnerOnlyMemory(t *testing.T) {
	record := memcore.MemoryRecord{
		ID:         "memory-owner-only",
		Scope:      memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-owner"},
		Visibility: memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}
	reader := ViewerRef{ViewerID: "reader", PermissionScope: []string{PermissionMemoryRead}}
	if CanReadRecord(reader, record) {
		t.Fatal("non-owner reader was allowed to inspect owner-only memory")
	}
	auditor := ViewerRef{ViewerID: "auditor", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryAuditRead}}
	if !CanReadRecord(auditor, record) {
		t.Fatal("memory auditor was denied owner-only memory")
	}
}

func TestCanReadRecordDoesNotLetAuditorBypassPersonalAssistantPrivacy(t *testing.T) {
	assistantID := "A009992"
	if err := agentcore.RegisterPersonalAssistantProfileID("tenant-memory-admin", "owner-memory-admin", assistantID); err != nil {
		t.Fatal(err)
	}
	record := memcore.MemoryRecord{
		ID: "memory-private", ProfileID: agentcore.MemoryProfileID("memory-" + assistantID),
		Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: assistantID}, Visibility: memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}
	auditor := ViewerRef{ViewerID: "auditor", PermissionScope: []string{PermissionMemoryRead, PermissionMemoryAuditRead}}
	if CanReadRecord(auditor, record) {
		t.Fatal("memory auditor was allowed to inspect personal assistant memory")
	}
	owner := ViewerRef{ViewerID: assistantID, AgentID: assistantID, PermissionScope: []string{PermissionMemoryRead}}
	if !CanReadRecord(owner, record) {
		t.Fatal("personal assistant runtime was denied its own memory")
	}
}
