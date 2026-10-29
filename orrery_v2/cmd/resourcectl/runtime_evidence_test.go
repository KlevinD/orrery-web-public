package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestImprovementsCLIInspectsCapturedAttemptInExplicitTenant(t *testing.T) {
	ctx := context.Background()
	dbURL := "file://" + filepath.Join(t.TempDir(), "evidence.db")
	core, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	req := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a", RuntimeOptions: agentcore.RuntimeOptions{SystemPrompt: "private prompt"}}
	scope := improvement.RuntimeEvidenceScope{TenantID: "tenant-a", Task: improvement.ObjectRef{Kind: "agent_task", ID: "task-a"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "run-a"}, Source: graphprotocol.AccessRef{TenantID: "tenant-a", Kind: "agent_run", ID: "run-a"}}
	e, err := improvement.BuildRuntimeRequestEvidence(req, scope, "attempt-a", "trace://a", time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := improvement.NewRuntimeEvidenceStore(core).Capture(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"improvements", "--db", dbURL, "--tenant", "tenant-a", "--attempt", e.AttemptID}, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Request         improvement.RuntimeRequestEvidence
		Terminal        *improvement.RuntimeTerminalEvidence
		Review          improvement.RuntimeReview
		ReviewPersisted bool `json:"review_persisted"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Request.ID != e.ID || got.Terminal != nil || strings.Contains(out.String(), "private prompt") {
		t.Fatalf("inspection output: %s %v", out.String(), err)
	}
	if got.Review.PipelineStatus != "unknown" || got.Review.DurationMS != nil || got.Review.BaselineReady || got.ReviewPersisted || len(got.Review.Domains) != 9 {
		t.Fatalf("prepared inspection fabricated readiness or wrote evidence: %s", out.String())
	}
	core, err = db.Open(ctx, db.Config{Driver: db.DriverFile, URL: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := improvement.NewRuntimeEvidenceStore(core).Complete(ctx, e, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Unix(1001, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(ctx, []string{"improvements", "--db", dbURL, "--tenant", "tenant-a", "--attempt", e.AttemptID}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Terminal == nil || got.Review.PipelineStatus != "completed" || got.Review.BusinessQuality != "unassessed" || !got.ReviewPersisted {
		t.Fatalf("completed review inspection: %s %v", out.String(), err)
	}
	for _, args := range [][]string{
		{"improvements", "--db", dbURL, "--tenant", "tenant-b", "--attempt", e.AttemptID},
		{"improvements", "--db", dbURL, "--attempt", e.AttemptID},
		{"improvements", "--db", dbURL, "--tenant", "tenant-a", "--attempt", e.AttemptID, "--id", "case-a"},
	} {
		if err := run(ctx, args, &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid inspection accepted: %v", args)
		}
	}
}
