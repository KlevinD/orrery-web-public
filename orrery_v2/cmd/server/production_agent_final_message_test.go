package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
)

// Exercise the production registry and owned HTTP model adapter. Only the model
// endpoint is a fixture; no message gateway or external sender is involved.
func TestProductionAgentCoreFinalMessageSurvivesResponsePersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const answer = "  Agent 原文：已核对资料，仍有一项等待确认。\n"
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": answer},
		}}})
	}))
	defer model.Close()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "runtime.db"))})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal, err := agentkernel.NewSQLToolJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		KernelJournal: journal,
		ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			return runtimecore.RuntimeToolCallResult{}, errors.New("this fixture must not execute tools")
		}),
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "final-fixture", Provider: "openai", BaseURL: model.URL + "/v1", Model: "final-fixture", APIKey: "fixture", Enabled: true,
		}}},
	})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	var events []agentcore.SessionEnvelope
	response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{
		TenantID: "tenant-final", AgentID: "agent-final", RunID: "run-final",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "owner-final", TenantID: "tenant-final"},
		ContextPack:        agentcore.ContextPack{AgentID: "agent-final"},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "user", Content: "Return the fixture answer."}},
		RuntimeOptions:     agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": "final-fixture"}},
	}, func(_ context.Context, envelope agentcore.SessionEnvelope) error {
		events = append(events, envelope)
		return envelope.Validate()
	})
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("managed runtime = %+v, %v", response, err)
	}
	finals, finalIndex, resultIndex := 0, -1, -1
	for index, envelope := range events {
		switch envelope.Event.Kind {
		case agentcore.EventMessageFinalized:
			finals++
			finalIndex = index
			if envelope.Event.MessageFinalized.Message.Content != answer {
				t.Fatal("adapter changed the Agent's text")
			}
		case agentcore.EventTurnResult:
			resultIndex = index
		}
	}
	if finals != 1 || finalIndex >= resultIndex {
		t.Fatalf("final/terminal ordering = %d/%d, finals=%d", finalIndex, resultIndex, finals)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutRecord(ctx, "runtime-response-fixture", "run-final", db.Record{"payload": string(payload)}); err != nil {
		t.Fatal(err)
	}
	record, found, err := store.GetRecord(ctx, "runtime-response-fixture", "run-final")
	if err != nil || !found {
		t.Fatalf("persisted response missing: %v", err)
	}
	var recovered agentcore.RuntimeResponse
	if err := json.Unmarshal([]byte(record["payload"].(string)), &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Messages) != 1 || recovered.Messages[0].Content != answer || recovered.Messages[0].Status != agentcore.RuntimeMessageStatusFinal || calls.Load() != 1 {
		t.Fatalf("lost, changed or regenerated final: %+v, model calls=%d", recovered, calls.Load())
	}
}
