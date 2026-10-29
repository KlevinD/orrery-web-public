package core

import (
	"strings"
	"testing"
	"time"
)

func TestExecutionEnvelopeSealVerifyAndClone(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	envelope := &ExecutionEnvelope{
		TenantID:         "tenant-a",
		RunID:            "run-a",
		AgentID:          "agent-a",
		AgentProfileID:   "profile-a",
		PermissionScopes: []string{"knowledge.read", "knowledge.read"},
		AllowedToolIDs:   []string{"knowledge_search"},
		IssuedAt:         at,
	}
	if err := envelope.SealAt(at); err != nil {
		t.Fatalf("SealAt: %v", err)
	}
	if !strings.HasPrefix(envelope.EnvelopeDigest, "sha256:") {
		t.Fatalf("digest = %q", envelope.EnvelopeDigest)
	}
	if err := envelope.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	clone := CloneExecutionEnvelope(envelope)
	clone.PermissionScopes[0] = "memory.read"
	if envelope.PermissionScopes[0] != "knowledge.read" {
		t.Fatalf("clone mutated source: %+v", envelope.PermissionScopes)
	}
}

func TestExecutionEnvelopeRejectsTamperingAndFencesTools(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	envelope := &ExecutionEnvelope{
		TenantID:         "tenant-a",
		RunID:            "run-a",
		AgentID:          "agent-a",
		AgentProfileID:   "profile-a",
		PermissionScopes: []string{"knowledge.read"},
		AllowedToolIDs:   []string{"knowledge_search"},
		IssuedAt:         at,
	}
	if err := envelope.SealAt(at); err != nil {
		t.Fatalf("SealAt: %v", err)
	}
	if !envelope.AllowsTool("knowledge_search", []string{"knowledge.read"}) {
		t.Fatal("expected allowed tool")
	}
	if envelope.AllowsTool("scheduled_task_run_now", []string{"scheduler.run"}) {
		t.Fatal("unexpected tool permission")
	}
	envelope.PermissionScopes = append(envelope.PermissionScopes, "scheduler.run")
	if err := envelope.Verify(); err == nil {
		t.Fatal("tampered envelope verified")
	}
}

func TestExecutionEnvelopeAppliesForbiddenActionsAndPolicyFallback(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	envelope := &ExecutionEnvelope{
		TenantID:       "tenant-a",
		RunID:          "run-a",
		AgentID:        "agent-a",
		AgentProfileID: "profile-a",
		ResponsibilityContext: &ResponsibilityContext{
			ContextID:             "ctx-a",
			ResponsibilityID:      "resp-a",
			ResponsibilityVersion: 1,
			PolicyID:              "policy-a",
			PolicyVersion:         1,
			AgentProfileID:        "profile-a",
			PolicyToolScopes:      []string{"knowledge.read"},
			ForbiddenActions:      []string{"knowledge.delete"},
		},
		IssuedAt: at,
	}
	if err := envelope.SealAt(at); err != nil {
		t.Fatalf("SealAt: %v", err)
	}
	if !envelope.AllowsTool("knowledge_search", []string{"knowledge.read"}) {
		t.Fatal("expected policy scope fallback to allow read")
	}
	if envelope.AllowsTool("knowledge.delete", nil) {
		t.Fatal("forbidden tool was allowed")
	}
	if envelope.AllowsTool("knowledge_search", []string{"knowledge.delete"}) {
		t.Fatal("forbidden permission was allowed")
	}
}

func TestCloneExecutionEnvelopeForRunRekeysChildOperation(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	parent := &ExecutionEnvelope{TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a", AgentProfileID: "profile-a", PermissionScopes: []string{"memory.read"}, IssuedAt: at}
	if err := parent.SealAt(at); err != nil {
		t.Fatalf("SealAt: %v", err)
	}
	child, err := CloneExecutionEnvelopeForRun(parent, "run-a-maintenance", at.Add(time.Minute))
	if err != nil {
		t.Fatalf("CloneExecutionEnvelopeForRun: %v", err)
	}
	if child == nil || child.RunID != "run-a-maintenance" || child.EnvelopeID == parent.EnvelopeID || child.EnvelopeDigest == parent.EnvelopeDigest {
		t.Fatalf("child envelope was not rekeyed: parent=%+v child=%+v", parent, child)
	}
	if err := child.Verify(); err != nil {
		t.Fatalf("child Verify: %v", err)
	}

	parent.PermissionScopes = append(parent.PermissionScopes, "admin.*")
	if _, err := CloneExecutionEnvelopeForRun(parent, "run-a-tampered", at.Add(2*time.Minute)); err == nil {
		t.Fatal("expected tampered parent envelope to be rejected")
	}
}
