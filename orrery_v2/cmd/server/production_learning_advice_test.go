package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/db"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionLearningAdviceConsumesAssessmentWithoutExposingEvidence(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, URL: "memory://" + t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	store := resourceadmin.NewPlatformRSILearningSampleStore(core)
	scope, _ := resourceadmin.RSILearningScope("tenant", "agent")
	o, err := store.Save(ctx, resourceadmin.RSILearningSample{Scope: scope, EvidenceRef: `["runtime_tool","run","call"]`, Tool: "lookup", ErrorCode: "invalid_arguments", Summary: "Missing order ID", Evidence: json.RawMessage(`{"private":"PRIVATE_PAYLOAD"}`)})
	if err != nil {
		t.Fatal(err)
	}
	job := resourceadmin.BuildDecisionRSISampleJob(o.Sample)
	if err := store.SaveAssessment(ctx, decision.Record{ObservationID: o.ID, At: time.Now(), Kind: job.Kind, ScopeHash: decision.Hash(job.Scope), ModelRevision: "test", Metadata: decision.Metadata{Adapter: "laya", Provider: "laya", ProfileID: "test", Model: "multilingual"}, PromptVersion: job.PromptVersion, Sources: job.Sources, InputHashes: job.InputHashes, Mode: "shadow", Decisions: []decision.Decision{{Choice: "tool_usage", AnswerConfidence: .95, Confidence: .8, Probabilities: map[string]float64{"skill_defect": .02, "tool_usage": .95, "environment": .01, "insufficient": .02}}}}); err != nil {
		t.Fatal(err)
	}
	adapter := productionLearningAdvice{samples: store}
	output, err := adapter.ExecuteRSILearningTool(ctx, mcptransport.RSILearningToolRequest{TenantID: "tenant", AgentID: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(output)
	if !strings.Contains(string(raw), "review_arguments") || strings.Contains(string(raw), "PRIVATE_PAYLOAD") {
		t.Fatal("incorrect learning plan")
	}
	advice, err := adapter.RecoveryAdvice(ctx, "tenant", missioncontrol.RuntimeSessionDTO{AgentID: "agent", RunID: "run", TaskID: "task"})
	if err != nil || len(advice) != 1 || advice[0].SourceID != o.ID {
		t.Fatalf("missing Mission advice: %+v %v", advice, err)
	}
	for _, arguments := range []map[string]any{{"agent_id": "other"}, {"tenant_id": "other"}, {"limit": 1.5}, {"after": false}, {"limit": 101}} {
		if _, err := adapter.ExecuteRSILearningTool(ctx, mcptransport.RSILearningToolRequest{TenantID: "tenant", AgentID: "agent", Arguments: arguments}); err == nil {
			t.Fatal("invalid or scope-expanding arguments accepted")
		}
	}
	if _, err := adapter.ExecuteRSILearningTool(ctx, mcptransport.RSILearningToolRequest{AgentID: "agent"}); !errors.Is(err, mcptransport.ErrPermissionDenied) {
		t.Fatal("missing trusted tenant accepted")
	}
	other, err := adapter.ExecuteRSILearningTool(ctx, mcptransport.RSILearningToolRequest{TenantID: "other", AgentID: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if other["observations_in_page"] != float64(0) {
		t.Fatal("cross-tenant observations exposed")
	}
}
