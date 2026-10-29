package resourceadmin

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
)

func TestPlatformSkillVersionEvaluationStoreIsImmutableAndPersistent(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "skill-evaluation-store-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	evaluations := NewPlatformSkillVersionEvaluationStore(store)
	active := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "1.0.0", Enabled: true}
	candidate := active.Clone()
	candidate.Version = "2.0.0"
	evaluation := SkillVersionEvaluation{
		EvaluationRef: "eval-1", CandidateVersion: candidate.Version, BaselineVersion: active.Version,
		CandidateDefinitionHash: skillres.DefinitionHash(candidate), BaselineDefinitionHash: skillres.DefinitionHash(active),
		DatasetRef: "heldout-v1", PolicyVersion: "policy-v1", EvaluatorRef: "ontology-run-1",
		BaselineScore: 0.90, CandidateScore: 0.93, HeldOutPassed: true, SafetyPassed: true, EvidenceRefs: []string{"artifact-1"},
	}
	if err := evaluations.SaveSkillVersionEvaluation(ctx, agentcore.SkillID("skill.review"), evaluation); err != nil {
		t.Fatalf("save evaluation: %v", err)
	}
	reopened := NewPlatformSkillVersionEvaluationStore(store)
	got, err := reopened.GetSkillVersionEvaluation(ctx, "skill.review", "eval-1")
	if err != nil {
		t.Fatalf("get evaluation: %v", err)
	}
	if got.CandidateVersion != "2.0.0" || got.CandidateDefinitionHash != evaluation.CandidateDefinitionHash {
		t.Fatalf("evaluation = %+v", got)
	}
	changed := evaluation
	changed.CandidateScore = 0.99
	if err := reopened.SaveSkillVersionEvaluation(ctx, "skill.review", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting evaluation error = %v, want ErrConflict", err)
	}
}

func TestRecordSkillVersionEvaluationFromOntologyArtifact(t *testing.T) {
	ctx := context.Background()
	artifacts := ontology.NewMemoryModelRunEvaluationArtifactStore()
	artifact := ontology.ModelRunEvaluationArtifact{
		TenantID: "tenant-1", Engine: "rsi-evaluator", RunID: "run-1",
		ArtifactKind: ontology.ModelRunEvaluationArtifactKindSkillVersionEvaluation,
		ArtifactURI:  "s3://evaluation-bucket/run-1.json",
		Metadata: map[string]string{
			"evaluation_ref": "ontology-eval-1", "candidate_version": "2.0.0", "baseline_version": "1.0.0",
			"candidate_definition_hash": strings.Repeat("a", 64), "baseline_definition_hash": strings.Repeat("b", 64),
			"dataset_ref": "heldout-v1", "policy_version": "policy-v1", "evaluator_ref": "run-1",
			"status": "passed", "baseline_score": "0.90", "candidate_score": "0.93",
			"held_out_passed": "true", "safety_passed": "true", "regression_count": "0",
			"evidence_refs": "trace-1",
		},
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, artifact); err != nil {
		t.Fatalf("save Ontology artifact: %v", err)
	}
	page, err := artifacts.ListModelRunEvaluationArtifacts(ctx, ontology.ModelRunEvaluationArtifactQuery{RunID: "run-1"})
	if err != nil || len(page.Artifacts) != 1 {
		t.Fatalf("list Ontology artifacts = %+v, err=%v", page.Artifacts, err)
	}
	service := NewCommandService(nil, nil)
	evaluations := NewMemorySkillVersionEvaluationStore()
	service.SetSkillVersionEvaluationStore(evaluations)
	if err := service.RecordSkillVersionEvaluationFromOntologyArtifact(ctx, agentcore.SkillID("skill.review"), page.Artifacts[0]); err != nil {
		t.Fatalf("record Ontology evaluation: %v", err)
	}
	evaluation, err := evaluations.GetSkillVersionEvaluation(ctx, "skill.review", "ontology-eval-1")
	if err != nil {
		t.Fatalf("get recorded evaluation: %v", err)
	}
	if evaluation.Status != SkillVersionEvaluationStatusPassed || !containsString(evaluation.EvidenceRefs, page.Artifacts[0].ID) {
		t.Fatalf("recorded evaluation = %+v", evaluation)
	}
}

func TestMemorySkillVersionEvaluationStoreRetainsFailedGates(t *testing.T) {
	store := NewMemorySkillVersionEvaluationStore()
	err := store.SaveSkillVersionEvaluation(context.Background(), "skill.review", SkillVersionEvaluation{
		EvaluationRef: "eval-failed", CandidateVersion: "2.0.0", BaselineVersion: "1.0.0",
		CandidateDefinitionHash: strings.Repeat("a", 64), BaselineDefinitionHash: strings.Repeat("b", 64),
		DatasetRef: "heldout-v1", PolicyVersion: "policy-v1", EvaluatorRef: "eval-1",
		BaselineScore: 0.9, CandidateScore: 0.8, HeldOutPassed: false, SafetyPassed: true, EvidenceRefs: []string{"artifact-1"},
	})
	if err != nil {
		t.Fatalf("failed evaluation save error = %v", err)
	}
	evaluation, err := store.GetSkillVersionEvaluation(context.Background(), "skill.review", "eval-failed")
	if err != nil {
		t.Fatalf("get failed evaluation: %v", err)
	}
	if evaluation.Status != SkillVersionEvaluationStatusFailed || len(evaluation.FailureReasons) == 0 {
		t.Fatalf("failed evaluation = %+v", evaluation)
	}
}
