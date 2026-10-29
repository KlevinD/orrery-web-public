package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/platform/db"
	mcptransport "orrery_v2/internal/transport/mcp"
)

// Explicit credentials opt into an isolated synthetic probe; no business
// database, task, message channel, or repair action is used by this test.
func TestLearningRecoveryLiveCanary(t *testing.T) {
	path := os.Getenv("ORRERY_LAYA_CANARY_KEY_FILE")
	if path == "" {
		t.Skip("explicit local model credential required")
	}
	key, err := laya.ReadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := laya.NewClient("http://127.0.0.1:18490", key, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "synthetic.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	store := resourceadmin.NewPlatformRSILearningSampleStore(core)
	scope, _ := resourceadmin.RSILearningScope("synthetic", "probe-agent")
	observation, err := store.Save(ctx, resourceadmin.RSILearningSample{Scope: scope, EvidenceRef: `["runtime_tool","probe-run","probe-call"]`, Tool: "query_order", ErrorCode: "http_503", Summary: "Upstream temporarily unavailable. Retrying the same request succeeded.", Evidence: json.RawMessage(`{"input":{"order_id":"synthetic"},"output":{"status":"failed","http_status":503}}`)})
	if err != nil {
		t.Fatal(err)
	}
	saved := make(chan decision.Record, 1)
	observer := laya.NewObserver(ctx, client, func(record decision.Record) {
		if err := store.SaveAssessment(ctx, record); err != nil {
			t.Error(err)
		}
		saved <- record
	})
	defer observer.Close()
	if !observer.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(observation.Sample)) {
		t.Fatal("job rejected")
	}
	var record decision.Record
	select {
	case record = <-saved:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if record.Fallback != "" || len(record.Decisions) != 1 {
		t.Fatalf("model failed: %s", record.Fallback)
	}
	adapter := productionLearningAdvice{samples: store}
	plan, err := adapter.ExecuteRSILearningTool(ctx, mcptransport.RSILearningToolRequest{TenantID: "synthetic", AgentID: "probe-agent"})
	if err != nil || plan["observations_in_page"] != float64(1) {
		t.Fatalf("plan unavailable: %v", err)
	}
	advice, err := adapter.RecoveryAdvice(ctx, "synthetic", missioncontrol.RuntimeSessionDTO{RunID: "probe-run", AgentID: "probe-agent", Status: "failed"})
	if err != nil || len(advice) != 1 {
		t.Fatalf("recovery advice unavailable: %v", err)
	}
	retained, err := store.Get(ctx, scope, observation.ID)
	if err != nil || string(retained.Sample.Evidence) != string(observation.Sample.Evidence) {
		t.Fatal("source evidence changed")
	}
	result := resourceadmin.LearningAdvice(retained)
	t.Logf("model=%s choice=%s selected_probability=%.4f next_step=%s elapsed_ms=%d advisory_only=true", record.ModelRevision, record.Decisions[0].Choice, record.Decisions[0].AnswerConfidence, result.NextStep, record.ElapsedMS)
}
