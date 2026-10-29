package event_test

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/lifecycle"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
)

func TestSignalMapperDoesNotPersistRuntimeStartByDefault(t *testing.T) {
	mapper := missionevent.NewSignalMapper()

	events, err := mapper.Map(signal(lifecycle.BeforeRuntimeStart, lifecycle.RuntimeStartPayload{RuntimeRequestRef: "runtime-1"}))
	if err != nil {
		t.Fatalf("Map returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("mapped %d events, want none", len(events))
	}
}

func TestSignalMapperDoesNotPersistAgentReplyWithoutMessageRef(t *testing.T) {
	mapper := missionevent.NewSignalMapper()

	events, err := mapper.Map(signal(lifecycle.AfterAgentReply, lifecycle.EmptyPayload{}))
	if err != nil {
		t.Fatalf("Map returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("mapped %d events, want none", len(events))
	}
}

func TestSignalMapperRejectsUnknownSignals(t *testing.T) {
	mapper := missionevent.NewSignalMapper()

	_, err := mapper.Map(signal(lifecycle.SignalName("new.unmapped.signal"), lifecycle.EmptyPayload{}))
	if err == nil {
		t.Fatalf("expected unknown signals to fail explicitly")
	}
}

func TestSignalMapperMapsAfterResultCommitToResultDeliverablesAndEvidence(t *testing.T) {
	mapper := missionevent.NewSignalMapper()

	events, err := mapper.Map(signal(lifecycle.AfterResultCommit, missionevent.ResultCommittedPayload{
		Result: agentcore.AgentResult{
			ID:        "result-1",
			MissionID: "mission-1",
			TaskID:    "task-1",
			AgentID:   "agent-1",
			RunID:     "run-1",
			Summary:   agentcore.Summary{Text: "Done"},
			Deliverables: []agentcore.Deliverable{{
				ID:                    "deliverable-1",
				ExpectedDeliverableID: "expected-1",
				Title:                 "Patch",
				ArtifactRef:           agentcore.ArtifactRef{ID: "artifact-1", Kind: agentcore.ArtifactKindCode, URI: "artifact://patch"},
			}},
			Evidence: []agentcore.Evidence{{
				ID:                    "evidence-1",
				EvidenceRequirementID: "requirement-1",
				Kind:                  agentcore.EvidenceKindTestLog,
				ArtifactRef:           agentcore.ArtifactRef{ID: "log-1", Kind: agentcore.ArtifactKindLog, URI: "artifact://log"},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("Map returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("mapped %d events, want result + deliverable + evidence: %+v", len(events), events)
	}
	if events[0].EventType != missionevent.EventAgentResultSubmitted ||
		events[1].EventType != missionevent.EventDeliverableSubmitted ||
		events[2].EventType != missionevent.EventEvidenceSubmitted {
		t.Fatalf("unexpected event types: %s, %s, %s", events[0].EventType, events[1].EventType, events[2].EventType)
	}
	if events[0].MissionID != "mission-1" || events[0].StreamID != "mission-1" || events[0].CorrelationID != "corr-1" {
		t.Fatalf("event did not inherit signal envelope fields: %+v", events[0])
	}
	if events[0].Actor.ID != "profile-1" {
		t.Fatalf("event actor id = %s, want stable agent profile profile-1", events[0].Actor.ID)
	}
	if events[0].IdempotencyKey == "" || events[1].IdempotencyKey == "" || events[2].IdempotencyKey == "" {
		t.Fatalf("mapped events must carry idempotency keys: %+v", events)
	}
	for i, event := range events {
		want := i + 1
		if event.StreamVersion != want {
			t.Fatalf("event %d stream_version = %d, want %d", i, event.StreamVersion, want)
		}
	}
}

func TestSignalMapperMapsTaskAcceptedAndFailures(t *testing.T) {
	mapper := missionevent.NewSignalMapper()

	accepted, err := mapper.Map(signal(lifecycle.AfterTaskAccepted, lifecycle.TaskAcceptedPayload{AcceptedBy: "agent-1"}))
	if err != nil {
		t.Fatalf("Map accepted returned error: %v", err)
	}
	if len(accepted) != 1 || accepted[0].EventType != missionevent.EventTaskAccepted {
		t.Fatalf("accepted mapped to %+v, want TaskAccepted", accepted)
	}
	if accepted[0].StreamVersion != 1 {
		t.Fatalf("accepted stream_version = %d, want 1", accepted[0].StreamVersion)
	}

	failed, err := mapper.Map(signal(lifecycle.AgentFailed, lifecycle.FailurePayload{Code: "runtime_error", Message: "failed"}))
	if err != nil {
		t.Fatalf("Map failed returned error: %v", err)
	}
	if len(failed) != 1 || failed[0].EventType != missionevent.EventAgentRunFailed {
		t.Fatalf("failed mapped to %+v, want AgentRunFailed", failed)
	}
	if failed[0].StreamVersion != 1 {
		t.Fatalf("failed stream_version = %d, want 1", failed[0].StreamVersion)
	}
}

func TestSignalMapperScopesLifecycleEventsToTenantMissionStream(t *testing.T) {
	mapper := missionevent.NewSignalMapper()
	tenantSignal := signal(lifecycle.AfterTaskAccepted, lifecycle.TaskAcceptedPayload{AcceptedBy: "agent-1"})
	tenantSignal.TenantID = "tenant-a"

	events, err := mapper.Map(tenantSignal)
	if err != nil {
		t.Fatalf("Map returned error: %v", err)
	}
	if len(events) != 1 || events[0].TenantID != "tenant-a" || events[0].StreamID != "tenant-a/mission-1" {
		t.Fatalf("tenant-scoped event = %+v", events)
	}
}

func TestSignalMapperRejectsMismatchedPayloadForMappedSignals(t *testing.T) {
	mapper := missionevent.NewSignalMapper()

	_, err := mapper.Map(signal(lifecycle.AfterTaskAccepted, lifecycle.EmptyPayload{}))
	if err == nil {
		t.Fatalf("Map returned nil error for mismatched AfterTaskAccepted payload")
	}

	_, err = mapper.Map(signal(lifecycle.AgentFailed, lifecycle.EmptyPayload{}))
	if err == nil {
		t.Fatalf("Map returned nil error for mismatched AgentFailed payload")
	}
}

func signal(name lifecycle.SignalName, payload lifecycle.SignalPayload) lifecycle.SignalEnvelope {
	return lifecycle.SignalEnvelope{
		SignalID:       "sig-1",
		Name:           name,
		MissionID:      missioncore.MissionID("mission-1"),
		AgentID:        agentcore.AgentID("agent-1"),
		AgentProfileID: agentcore.AgentProfileID("profile-1"),
		TaskID:         missioncore.TaskID("task-1"),
		RunID:          agentcore.AgentRunID("run-1"),
		CorrelationID:  lifecycle.CorrelationID("corr-1"),
		CausationID:    lifecycle.CausationID("cause-1"),
		Attempt:        1,
		EmittedAt:      time.Date(2026, 5, 31, 13, 0, 0, 0, time.UTC),
		Payload:        payload,
	}
}
