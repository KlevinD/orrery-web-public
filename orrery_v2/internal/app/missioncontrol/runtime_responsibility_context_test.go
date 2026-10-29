package missioncontrol_test

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
)

func TestCommandServiceCarriesResponsibilityContextIntoAgentRunAndAuditMetadata(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"run-responsibility", "evt-responsibility"}}, fixedClock{at: at})
	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		RequestID: "corr-responsibility", IdempotencyKey: "start-responsibility", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex",
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "snapshot-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 4, Source: "people.responsibility_context",
			PolicyID: "responsibility-policy-1", PolicyVersion: 7,
			ActorMemberID: "member-1", AgentProfileID: "agent-1", MissionID: "mission-1", TaskID: "task-1", OrganizationName: "Delivery", PositionName: "Owner",
			Summary: "Own delivery quality", Goals: []string{"Close delivery gaps"}, Boundaries: []string{"forbid production deploy"}, EvidenceRequirements: []string{"review record"},
		},
	})
	if err != nil {
		t.Fatalf("StartAgentRun returned error: %v", err)
	}
	if len(runStore.runs) != 1 || len(events.events) != 1 {
		t.Fatalf("runs/events = %d/%d", len(runStore.runs), len(events.events))
	}
	run := runStore.runs[0]
	responsibility := run.Request.ContextPack.ResponsibilityContext
	if responsibility == nil || responsibility.ContextID != "snapshot-1" || responsibility.ResponsibilityID != "responsibility-1" || responsibility.ActorMemberID != "member-1" {
		t.Fatalf("run responsibility context = %+v", responsibility)
	}
	if run.Request.RuntimeOptions.ProviderSpecific["responsibility_actor_member_id"] != "member-1" || run.Request.RuntimeOptions.ProviderSpecific["responsibility_version"] != "4" ||
		run.Request.RuntimeOptions.ProviderSpecific["responsibility_policy_id"] != "responsibility-policy-1" || run.Request.RuntimeOptions.ProviderSpecific["responsibility_policy_version"] != "7" {
		t.Fatalf("provider metadata = %+v", run.Request.RuntimeOptions.ProviderSpecific)
	}
	var responsibilitySourceFound bool
	for _, ref := range run.Request.ContextPack.SourceRefs {
		if ref.Kind == agentcore.ContextSourceKindResponsibility && ref.Ref.ResponsibilityRef != nil && ref.Ref.ResponsibilityRef.ID == "snapshot-1" {
			responsibilitySourceFound = true
		}
	}
	if !responsibilitySourceFound {
		t.Fatalf("context source refs = %+v", run.Request.ContextPack.SourceRefs)
	}
	var promptContainsResponsibility bool
	for _, message := range run.Request.InputMessages {
		if strings.Contains(message.Content, "snapshot-1") && strings.Contains(message.Content, "Close delivery gaps") {
			promptContainsResponsibility = true
		}
	}
	if !promptContainsResponsibility {
		t.Fatalf("run prompt did not include responsibility context: %+v", run.Request.InputMessages)
	}
	metadata := events.events[0].Metadata
	if metadata["responsibility_context_id"] != "snapshot-1" || metadata["responsibility_id"] != "responsibility-1" || metadata["responsibility_actor_member_id"] != "member-1" ||
		metadata["responsibility_policy_id"] != "responsibility-policy-1" || metadata["responsibility_policy_version"] != "7" {
		t.Fatalf("event metadata = %+v", metadata)
	}
}
