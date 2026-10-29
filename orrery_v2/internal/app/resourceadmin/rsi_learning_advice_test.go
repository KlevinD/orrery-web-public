package resourceadmin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/db"
)

func adviceRecord(o RSILearningObservation, choice string) decision.Record {
	job := BuildDecisionRSISampleJob(o.Sample)
	probabilities := map[string]float64{"skill_defect": .025, "tool_usage": .025, "environment": .025, "insufficient": .025}
	probabilities[choice] = .925
	return decision.Record{ObservationID: o.ID, At: time.Now().UTC(), Kind: job.Kind, ScopeHash: decision.Hash(job.Scope), ModelRevision: "test-model", Metadata: decision.Metadata{Adapter: "laya", Provider: "laya", ProfileID: "decision-laya", Model: "multilingual"}, PromptVersion: job.PromptVersion, Sources: job.Sources, InputHashes: job.InputHashes, Mode: "shadow", Decisions: []decision.Decision{{Choice: choice, AnswerConfidence: .925, Confidence: .7, Probabilities: probabilities}}}
}

func TestLearningPlanPreservesEveryObservationAndScopesRunAdvice(t *testing.T) {
	ctx := context.Background()
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "advice.db")}
	core, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPlatformRSILearningSampleStore(core)
	scope, _ := RSILearningScope("tenant-a", "agent-a")
	samples := []RSILearningSample{
		{Scope: scope, EvidenceRef: `["runtime_tool","run-a","call-1"]`, Tool: "lookup", ErrorCode: "tool_failed", Summary: "Skill v1 reproduces wrong arguments", Evidence: json.RawMessage(`{"private":"SECRET_EVIDENCE"}`)},
		{Scope: scope, EvidenceRef: `["runtime_tool","run-a","call-2"]`, Tool: "lookup", ErrorCode: "tool_failed", Summary: "Repeated failure"},
		{Scope: scope, EvidenceRef: `["runtime_tool","run-b","call-3"]`, Tool: "lookup", ErrorCode: "timeout", Summary: "Network unavailable"},
	}
	for i, sample := range samples {
		observation, err := store.Save(ctx, sample)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 2 {
			choice := "skill_defect"
			if i == 2 {
				choice = "environment"
			}
			if err := store.SaveAssessment(ctx, adviceRecord(observation, choice)); err != nil {
				t.Fatal(err)
			}
		}
	}
	other := samples[0]
	other.Scope, _ = RSILearningScope("tenant-b", "agent-a")
	if _, err := store.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	core, err = db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	store = NewPlatformRSILearningSampleStore(core)
	plan, err := store.LearningPlan(ctx, scope, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ObservationsInPage != 3 || len(plan.Items) != 2 || plan.Items[0].Priority != "high" || plan.Items[0].OccurrencesInPage != 2 || len(plan.Items[0].Advice) != 2 || !plan.AdvisoryOnly {
		t.Fatalf("incomplete plan: %+v", plan)
	}
	if plan.Items[1].Advice[0].Classification != "environment" {
		t.Fatal("environmental sample was discarded")
	}
	advice, err := store.RunAdvice(ctx, scope, "run-a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(advice) != 2 {
		t.Fatalf("run isolation failed: %+v", advice)
	}
	if got, err := store.RunAdvice(ctx, scope, "run-absent", 3); err != nil || len(got) != 0 {
		t.Fatal("run crossed scope")
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "SECRET_EVIDENCE") || strings.Contains(string(raw), "Skill v1 reproduces") {
		t.Fatal("private evidence leaked in learning plan")
	}
	// Every historical row remains reachable through bounded pagination.
	seen, cursor := map[string]bool{}, ""
	for i := 0; i < 4; i++ {
		page, err := store.LearningPlan(ctx, scope, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			for _, a := range item.Advice {
				seen[a.ObservationID] = true
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatal("ranking lost observations between pages")
	}
}

func TestLearningAdviceAbstainsWithoutReliableInputAndProtectsUnknownOutcome(t *testing.T) {
	sample := RSILearningSample{Scope: `["tenant","agent"]`, EvidenceRef: `["runtime_tool","run","call"]`, Tool: "lookup", Summary: "failure"}
	job := BuildDecisionRSISampleJob(sample)
	observation := RSILearningObservation{ID: job.ObservationID, At: time.Now(), Sample: sample}
	record := adviceRecord(observation, "skill_defect")
	observation.Assessment = &record
	if LearningAdvice(observation).NextStep != "reproduce_skill" {
		t.Fatal("model result did not reach advice")
	}
	record.Decisions[0].AnswerConfidence = .5
	if LearningAdvice(observation).NextStep != "collect_evidence" {
		t.Fatal("uncertain label became a diagnosis")
	}
	record.Decisions[0].AnswerConfidence = .925
	record.InputTruncated = true
	if LearningAdvice(observation).NextStep != "collect_evidence" {
		t.Fatal("truncated input trusted")
	}
	record.InputTruncated = false
	record.Fallback = "timeout"
	record.Decisions = nil
	if LearningAdvice(observation).NextStep != "collect_evidence" {
		t.Fatal("failure did not fall back")
	}
	observation.Sample.ErrorCode = "tool_outcome_unrecorded"
	if LearningAdvice(observation).NextStep != "reconcile_outcome" {
		t.Fatal("unknown side effect was retried")
	}
}
