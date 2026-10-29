package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestStoreTraceRecorderStoresRedactedRuntimeTrace(t *testing.T) {
	store := &capturingTraceStore{}
	recorder := StoreTraceRecorder{Store: store, Redactions: []string{"secret-token"}}
	ref, err := recorder.RecordTrace(context.Background(), validAgentInput(), RuntimeResponse{
		RawOutputRef: "provider://raw",
		Messages:     []RuntimeMessage{{Type: RuntimeMessageText, Content: "token secret-token"}},
	})
	if err != nil {
		t.Fatalf("record trace: %v", err)
	}
	if ref == "" {
		t.Fatalf("trace ref empty")
	}
	if strings.Contains(string(store.data), "secret-token") {
		t.Fatalf("secret leaked into trace: %s", string(store.data))
	}
	if !strings.Contains(string(store.data), "[REDACTED]") {
		t.Fatalf("redaction missing: %s", string(store.data))
	}
}

func TestStoreTraceRecorderScopesDiscussionTraceUnderConversation(t *testing.T) {
	store := &capturingTraceStore{}
	recorder := StoreTraceRecorder{Store: store}
	input := validAgentInput()
	input.RunID = "run-1"
	input.ContextPack.DiscussionGroupID = "conv-1"

	if _, err := recorder.RecordTrace(context.Background(), input, RuntimeResponse{}); err != nil {
		t.Fatalf("record trace: %v", err)
	}

	want := "discussion-conversations/conv-1/runs/run-1/runtime-trace.json"
	if store.key != want {
		t.Fatalf("trace key = %q, want %q", store.key, want)
	}
}

func TestStoreTraceRecorderProjectsResponsibilityContext(t *testing.T) {
	store := &capturingTraceStore{}
	input := validAgentInput()
	input.ContextPack.ResponsibilityContext = &ResponsibilityContext{
		ContextID:                 "responsibility-context-1",
		ResponsibilityID:          "responsibility-1",
		ResponsibilityVersion:     7,
		PolicyID:                  "policy-1",
		PolicyVersion:             3,
		AgentProfileID:            "profile-1",
		MissionID:                 "mission-1",
		TaskID:                    "task-1",
		AllowedTools:              []string{"repo.read", "repo.read", "  repo.write  "},
		AllowedDataScopes:         []string{"mission.read"},
		PolicyToolScopes:          []string{"repo.*"},
		PolicyDataScopes:          []string{"mission.*"},
		TaskPermissionScopes:      []string{"repo.read"},
		RequireExplicitToolScopes: true,
	}

	if _, err := (StoreTraceRecorder{Store: store}).RecordTrace(context.Background(), input, RuntimeResponse{}); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	var payload struct {
		ResponsibilityContext *responsibilityTraceProjection `json:"responsibility_context"`
	}
	if err := json.Unmarshal(store.data, &payload); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if payload.ResponsibilityContext == nil {
		t.Fatal("responsibility context projection missing")
	}
	got := payload.ResponsibilityContext
	if got.ContextID != "responsibility-context-1" || got.ResponsibilityID != "responsibility-1" || got.ResponsibilityVersion != 7 ||
		got.PolicyID != "policy-1" || got.PolicyVersion != 3 || got.AgentProfileID != "profile-1" ||
		got.MissionID != "mission-1" || got.TaskID != "task-1" || !got.RequireExplicitToolScopes {
		t.Fatalf("responsibility projection = %+v", got)
	}
	if want := []string{"repo.read", "repo.write"}; !reflect.DeepEqual(got.AllowedTools, want) {
		t.Fatalf("allowed tools = %#v, want %#v", got.AllowedTools, want)
	}
	if want := []string{"mission.read"}; !reflect.DeepEqual(got.AllowedDataScopes, want) {
		t.Fatalf("allowed data scopes = %#v, want %#v", got.AllowedDataScopes, want)
	}
	if want := []string{"repo.*"}; !reflect.DeepEqual(got.PolicyToolScopes, want) {
		t.Fatalf("policy tool scopes = %#v, want %#v", got.PolicyToolScopes, want)
	}
	if want := []string{"mission.*"}; !reflect.DeepEqual(got.PolicyDataScopes, want) {
		t.Fatalf("policy data scopes = %#v, want %#v", got.PolicyDataScopes, want)
	}
	if want := []string{"repo.read"}; !reflect.DeepEqual(got.TaskPermissionScopes, want) {
		t.Fatalf("task permission scopes = %#v, want %#v", got.TaskPermissionScopes, want)
	}
}

func TestStoreTraceRecorderOmitsResponsibilityContextWithoutInjection(t *testing.T) {
	store := &capturingTraceStore{}
	if _, err := (StoreTraceRecorder{Store: store}).RecordTrace(context.Background(), validAgentInput(), RuntimeResponse{}); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(store.data, &payload); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if _, exists := payload["responsibility_context"]; exists {
		t.Fatalf("unexpected responsibility context projection: %s", store.data)
	}
}

func TestStoreTraceRecorderResponsibilityProjectionExcludesSensitiveContextAndPromptValues(t *testing.T) {
	store := &capturingTraceStore{}
	input := validAgentInput()
	input.InputMessages = []RuntimeChatMessage{{Role: "user", Content: "full-user-prompt-secret"}}
	input.RuntimeOptions = RuntimeOptions{
		SystemPrompt:        "full-system-prompt-secret",
		SystemContextPrompt: "full-system-context-prompt-secret",
		PromptBlocks:        []RuntimePromptBlock{{BlockID: "private", Content: "full-prompt-block-secret"}},
	}
	input.ContextPack.ResponsibilityContext = &ResponsibilityContext{
		ContextID:             "context-1",
		ResponsibilityID:      "responsibility-1",
		ResponsibilityVersion: 1,
		PolicyID:              "policy-1",
		PolicyVersion:         1,
		AgentProfileID:        "profile-1",
		MissionID:             "mission-1",
		TaskID:                "task-1",
		Summary:               "responsibility-summary-secret",
		Goals:                 []string{"responsibility-goal-secret"},
		Boundaries:            []string{"responsibility-boundary-secret"},
		ForbiddenActions:      []string{"responsibility-forbidden-secret"},
		HumanInLoopTriggers:   []string{"responsibility-hil-secret"},
		EvidenceRequirements:  []string{"responsibility-evidence-secret"},
		SourceRefs:            []string{"responsibility-source-ref-secret"},
		ActorMemberID:         "actor-member-secret",
		OrganizationID:        "organization-id-secret",
		OrganizationName:      "organization-name-secret",
		PositionID:            "position-id-secret",
		PositionName:          "position-name-secret",
	}

	if _, err := (StoreTraceRecorder{Store: store}).RecordTrace(context.Background(), input, RuntimeResponse{}); err != nil {
		t.Fatalf("record trace: %v", err)
	}
	for _, sensitive := range []string{
		"full-user-prompt-secret", "full-system-prompt-secret", "full-system-context-prompt-secret", "full-prompt-block-secret",
		"responsibility-summary-secret", "responsibility-goal-secret", "responsibility-boundary-secret", "responsibility-forbidden-secret",
		"responsibility-hil-secret", "responsibility-evidence-secret", "responsibility-source-ref-secret", "actor-member-secret",
		"organization-id-secret", "organization-name-secret", "position-id-secret", "position-name-secret",
	} {
		if strings.Contains(string(store.data), sensitive) {
			t.Fatalf("sensitive value leaked into responsibility trace: %q in %s", sensitive, store.data)
		}
	}
}

type capturingTraceStore struct {
	key  string
	data []byte
}

func (s *capturingTraceStore) PutTrace(_ context.Context, key string, data []byte) (string, error) {
	s.key = key
	s.data = append([]byte(nil), data...)
	return "store://" + key, nil
}
