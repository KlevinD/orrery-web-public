package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestMissionTaskAgentResultNormalizerPersistsStructuredRuntimeOutputAndEvidence(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed the report with findings and risks.",
			`{"version":1,"summary":"Report completed and ready for review.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-findings","acceptance_criterion_id":"acceptance-1","source_kind":"runtime_artifact","source_ref":"artifact-report"},{"evidence_requirement_id":"evidence-risks","acceptance_criterion_id":"acceptance-2","source_kind":"runtime_artifact","source_ref":"artifact-report"}]}`,
		),
		}},
		Artifacts: []agentcore.ArtifactRef{{ID: "artifact-report", Kind: agentcore.ArtifactKindReport, URI: "artifact://agent-runs/run-materializer-1/report"}},
	}
	normalizer := missionTaskAgentResultNormalizer{store: env.FileStore}

	result, err := normalizer.NormalizeContext(ctx, input, response, time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if result.Summary.Text != "Report completed and ready for review." || len(result.Deliverables) != 1 || len(result.Evidence) != 2 {
		t.Fatalf("materialized result = %+v", result)
	}
	artifactRef := result.Deliverables[0].ArtifactRef
	if artifactRef.IsZero() || strings.HasPrefix(artifactRef.URI, "agent-run://") {
		t.Fatalf("deliverable artifact = %+v", artifactRef)
	}
	artifact, err := env.FileStore.Get(ctx, artifactRef.ID)
	if err != nil {
		t.Fatalf("Get persisted result artifact: %v", err)
	}
	if !strings.Contains(string(artifact.Content), "Completed the report") {
		t.Fatalf("persisted artifact content = %q", artifact.Content)
	}
	for index, evidence := range result.Evidence {
		if evidence.ArtifactRef.ID != "artifact-report" || evidence.EvidenceRequirementID == "" || evidence.AcceptanceCriterionID == "" {
			t.Fatalf("evidence[%d] = %+v", index, evidence)
		}
	}
	result.InternalTraceRef = "artifact://trace-1"
	for name, validator := range map[string]agentcore.ResultValidator{
		"evidence": productionEvidenceResultValidator{},
		"qa":       productionQAResultValidator{},
		"policy":   productionPolicyResultValidator{},
	} {
		if err := validator.Validate(input, result); err != nil {
			t.Fatalf("%s validator: %v", name, err)
		}
	}
}

func TestMissionTaskAgentResultNormalizerRejectsAgentGeneratedRuntimeOutputEvidence(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	input.Task.EvidenceRequirements[0].Description = "External system record was updated."
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Drafted a report.",
			`{"version":1,"summary":"Drafted report.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-findings","acceptance_criterion_id":"acceptance-1","source_kind":"runtime_output","source_ref":""},{"evidence_requirement_id":"evidence-risks","acceptance_criterion_id":"acceptance-2","source_kind":"runtime_output","source_ref":""}]}`,
		),
		}},
	}

	_, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now())
	if err == nil || !strings.Contains(err.Error(), "Agent-generated explanation") {
		t.Fatalf("NormalizeContext error = %v, want generated explanation evidence rejection", err)
	}
}

func TestMissionTaskAgentResultNormalizerIgnoresRuntimeOutputSourceRef(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed the report.",
			`{"version":1,"summary":"Report completed.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_output","source_ref":"redundant-output-ref"}],"evidence":[{"evidence_requirement_id":"evidence-findings","acceptance_criterion_id":"acceptance-1","source_kind":"runtime_artifact","source_ref":"artifact-report"},{"evidence_requirement_id":"evidence-risks","acceptance_criterion_id":"acceptance-2","source_kind":"runtime_artifact","source_ref":"artifact-report"}]}`,
		)}},
		Artifacts: []agentcore.ArtifactRef{{ID: "artifact-report", Kind: agentcore.ArtifactKindReport, URI: "artifact://report"}},
	}

	result, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Deliverables) != 1 || result.Deliverables[0].ArtifactRef.IsZero() {
		t.Fatalf("materialized deliverables = %+v", result.Deliverables)
	}
}

func TestMaterializeMissionTaskDeliverablesNormalizesSingleUnknownAlias(t *testing.T) {
	input := materializerTestInput()
	manifest := agentResultManifest{Deliverables: []agentResultManifestDeliverable{{
		ExpectedDeliverableID: "deliverable-status-record",
		SourceKind:            "runtime_output",
	}}}
	output := agentcore.ArtifactRef{ID: "runtime-output", Kind: agentcore.ArtifactKindReport, URI: "artifact://runtime-output"}

	deliverables, err := materializeMissionTaskDeliverables(input, agentcore.RuntimeResponse{}, output, manifest, true)
	if err != nil {
		t.Fatalf("materializeMissionTaskDeliverables: %v", err)
	}
	if len(deliverables) != 1 || deliverables[0].ExpectedDeliverableID != "deliverable-report" || deliverables[0].ArtifactRef.ID != output.ID {
		t.Fatalf("materialized deliverables = %+v", deliverables)
	}
}

func TestMaterializeMissionTaskDeliverablesUsesRuntimeOutputForSingleTextContractWithSemanticSubdeliverables(t *testing.T) {
	input := materializerTestInput()
	output := agentcore.ArtifactRef{ID: "runtime-output", Kind: agentcore.ArtifactKindReport, URI: "artifact://runtime-output"}

	for _, entries := range [][]agentResultManifestDeliverable{
		{},
		{
			{ExpectedDeliverableID: "deliverable-article", SourceKind: "tool_log", SourceRef: "article-call"},
			{ExpectedDeliverableID: "deliverable-approval", SourceKind: "tool_log", SourceRef: "approval-call"},
		},
	} {
		deliverables, err := materializeMissionTaskDeliverables(
			input,
			agentcore.RuntimeResponse{},
			output,
			agentResultManifest{Deliverables: entries},
			true,
		)
		if err != nil {
			t.Fatalf("materializeMissionTaskDeliverables entries=%d: %v", len(entries), err)
		}
		if len(deliverables) != 1 || deliverables[0].ExpectedDeliverableID != "deliverable-report" || deliverables[0].ArtifactRef.ID != output.ID {
			t.Fatalf("materialized deliverables entries=%d: %+v", len(entries), deliverables)
		}
	}
}

func TestMaterializeMissionTaskDeliverablesRejectsBlankSingleAlias(t *testing.T) {
	input := materializerTestInput()
	manifest := agentResultManifest{Deliverables: []agentResultManifestDeliverable{{
		ExpectedDeliverableID: "",
		SourceKind:            "runtime_output",
	}}}
	output := agentcore.ArtifactRef{ID: "runtime-output", Kind: agentcore.ArtifactKindReport, URI: "artifact://runtime-output"}

	_, err := materializeMissionTaskDeliverables(input, agentcore.RuntimeResponse{}, output, manifest, true)
	if err == nil || !strings.Contains(err.Error(), "unknown or duplicate expected deliverable") {
		t.Fatalf("materializeMissionTaskDeliverables error = %v", err)
	}
}

func TestMaterializeMissionTaskDeliverablesKeepsMultipleContractsStrict(t *testing.T) {
	input := materializerTestInput()
	input.Task.ExpectedDeliverables = append(input.Task.ExpectedDeliverables, agentcore.TaskDeliverableInput{ID: "deliverable-risks", Title: "Risks"})
	manifest := agentResultManifest{Deliverables: []agentResultManifestDeliverable{{
		ExpectedDeliverableID: "deliverable-status-record",
		SourceKind:            "runtime_output",
	}}}
	output := agentcore.ArtifactRef{ID: "runtime-output", Kind: agentcore.ArtifactKindReport, URI: "artifact://runtime-output"}

	_, err := materializeMissionTaskDeliverables(input, agentcore.RuntimeResponse{}, output, manifest, true)
	if err == nil || !strings.Contains(err.Error(), "unknown or duplicate expected deliverable") {
		t.Fatalf("materializeMissionTaskDeliverables error = %v", err)
	}
}

func TestParseAgentResultManifestAcceptsCompleteJSONWithoutClosingFence(t *testing.T) {
	manifest, found, err := parseAgentResultManifest("Completed.\n\n```orrery-agent-result\n" +
		`{"version":1,"summary":"Report completed.","deliverables":[],"evidence":[]}`)
	if err != nil {
		t.Fatalf("parseAgentResultManifest: %v", err)
	}
	if !found || manifest.Summary != "Report completed." {
		t.Fatalf("manifest = %+v, found = %t", manifest, found)
	}
}

func TestParseAgentResultManifestAcceptsStringVersionOne(t *testing.T) {
	for _, version := range []string{`"1"`, `"1.0"`} {
		manifest, found, err := parseAgentResultManifest("```orrery-agent-result\n" +
			`{"version":` + version + `,"summary":"Report completed.","deliverables":[],"evidence":[]}` + "\n```")
		if err != nil {
			t.Fatalf("parseAgentResultManifest version %s: %v", version, err)
		}
		if !found || manifest.Version != 1 {
			t.Fatalf("manifest = %+v, found = %t", manifest, found)
		}
	}
}

func TestParseAgentResultManifestRejectsUnsupportedStringVersion(t *testing.T) {
	_, found, err := parseAgentResultManifest("```orrery-agent-result\n" +
		`{"version":"2","summary":"Report completed.","deliverables":[],"evidence":[]}` + "\n```")
	if !found || err == nil || !strings.Contains(err.Error(), "manifest version") {
		t.Fatalf("found = %t, error = %v", found, err)
	}
}

func TestParseAgentResultManifestRejectsTruncatedJSONWithoutClosingFence(t *testing.T) {
	_, found, err := parseAgentResultManifest("Completed.\n\n```orrery-agent-result\n" +
		`{"version":1,"summary":"Report completed."`)
	if !found || err == nil || !strings.Contains(err.Error(), "decode orrery-agent-result manifest") {
		t.Fatalf("found = %t, error = %v", found, err)
	}
}

func TestMissionTaskAgentResultNormalizerBindsRedactedPathToOnlyGeneratedWorkspaceFile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Artifacts: []agentcore.ArtifactRef{{}},
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed the playable game.",
			`{"version":1,"summary":"Playable game completed.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}],"evidence":[{"evidence_requirement_id":"evidence-findings","acceptance_criterion_id":"acceptance-1","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"},{"evidence_requirement_id":"evidence-risks","acceptance_criterion_id":"acceptance-2","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}]}`,
		)}},
	}
	normalizer := missionTaskAgentResultNormalizer{
		store: env.FileStore,
		workspaceFiles: func(context.Context) []missionResultWorkspaceFile {
			return []missionResultWorkspaceFile{materializerWorkspaceFile("index.html", "<canvas></canvas>")}
		},
	}

	result, err := normalizer.NormalizeContext(ctx, input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Deliverables) != 1 || len(result.Evidence) != 2 {
		t.Fatalf("materialized result = %+v", result)
	}
	artifact := result.Deliverables[0].ArtifactRef
	if artifact.IsZero() || artifact.Kind != agentcore.ArtifactKindCode {
		t.Fatalf("workspace artifact = %+v", artifact)
	}
	for _, evidence := range result.Evidence {
		if evidence.ArtifactRef.ID != artifact.ID {
			t.Fatalf("evidence artifact = %+v, want %q", evidence.ArtifactRef, artifact.ID)
		}
	}
	persisted, err := env.FileStore.Get(ctx, artifact.ID)
	if err != nil || string(persisted.Content) != "<canvas></canvas>" {
		t.Fatalf("persisted workspace artifact = %+v err=%v", persisted.Ref, err)
	}
}

func TestMissionTaskAgentResultNormalizerRejectsAmbiguousRedactedWorkspaceFiles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	input.Task.AcceptanceCriteria = nil
	input.Task.EvidenceRequirements = nil
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed.",
			`{"version":1,"summary":"Completed.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}],"evidence":[]}`,
		)}},
	}
	normalizer := missionTaskAgentResultNormalizer{
		store: env.FileStore,
		workspaceFiles: func(context.Context) []missionResultWorkspaceFile {
			return []missionResultWorkspaceFile{
				materializerWorkspaceFile("index.html", "game"),
				materializerWorkspaceFile("readme.md", "instructions"),
			}
		},
	}

	_, err := normalizer.NormalizeContext(ctx, input, response, time.Now())
	if err == nil || !strings.Contains(err.Error(), "ambiguous across 2 host-verified files") {
		t.Fatalf("NormalizeContext error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerDoesNotBindForgedArtifactRefToWorkspaceFile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	input.Task.AcceptanceCriteria = nil
	input.Task.EvidenceRequirements = nil
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed.",
			`{"version":1,"summary":"Completed.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_artifact","source_ref":"/tmp/invented.html"}],"evidence":[]}`,
		)}},
	}
	normalizer := missionTaskAgentResultNormalizer{
		store: env.FileStore,
		workspaceFiles: func(context.Context) []missionResultWorkspaceFile {
			return []missionResultWorkspaceFile{materializerWorkspaceFile("index.html", "game")}
		},
	}

	_, err := normalizer.NormalizeContext(ctx, input, response, time.Now())
	if err == nil || !strings.Contains(err.Error(), "was not produced by this run") {
		t.Fatalf("NormalizeContext error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerMapsOneRuntimeArtifactToMultipleCriteria(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	input.Task.EvidenceRequirements = []agentcore.TaskEvidenceInput{{ID: "evidence-report", Description: "The report proves both checks.", Kind: string(agentcore.EvidenceKindArtifact), Required: true}}
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed the report.",
			`{"version":1,"summary":"Report completed.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_artifact","source_ref":"artifact-report"}],"evidence":[{"evidence_requirement_id":"evidence-report","acceptance_criterion_id":"acceptance-1","source_kind":"runtime_artifact","source_ref":"artifact-report"},{"evidence_requirement_id":"evidence-report","acceptance_criterion_id":"acceptance-2","source_kind":"runtime_artifact","source_ref":"artifact-report"}]}`,
		)}},
		Artifacts: []agentcore.ArtifactRef{{ID: "artifact-report", Kind: agentcore.ArtifactKindReport, URI: "artifact://report"}},
	}

	result, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Evidence) != 2 || result.Evidence[0].EvidenceRequirementID != "evidence-report" || result.Evidence[1].EvidenceRequirementID != "evidence-report" || result.Evidence[0].AcceptanceCriterionID == result.Evidence[1].AcceptanceCriterionID {
		t.Fatalf("materialized evidence = %+v", result.Evidence)
	}
}

func TestMissionTaskAgentResultNormalizerBindsOnlyObservedToolLog(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	input.Task.AcceptanceCriteria = []agentcore.TaskAcceptanceInput{{ID: "acceptance-1", Description: "Validation command completed."}}
	input.Task.EvidenceRequirements = []agentcore.TaskEvidenceInput{{ID: "evidence-command", Description: "Validation command completed.", Kind: string(agentcore.EvidenceKindTestLog), Required: true}}
	manifest := func(callID string) string {
		return materializerManifestOutput("Completed validation.",
			`{"version":1,"summary":"Validation completed.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-command","acceptance_criterion_id":"acceptance-1","source_kind":"tool_log","source_ref":"`+callID+`"}]}`)
	}
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages:  []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: manifest("call-1")}},
		ToolCalls: []agentcore.RuntimeToolCall{{ID: "call-1", Name: "validate", Output: "ok"}},
	}
	normalizer := missionTaskAgentResultNormalizer{store: env.FileStore}
	result, err := normalizer.NormalizeContext(ctx, input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].ToolLogRef.ID != "call-1" || result.Evidence[0].Kind != agentcore.EvidenceKindTestLog {
		t.Fatalf("tool evidence = %+v", result.Evidence)
	}

	response.Messages[0].Content = manifest("call-invented")
	if _, err := normalizer.NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "was not produced by this run") {
		t.Fatalf("invented tool reference error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerResolvesUniqueStructuredToolOutputReference(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	input.Task.AcceptanceCriteria = []agentcore.TaskAcceptanceInput{{ID: "acceptance-1", Description: "Knowledge record was stored."}}
	input.Task.EvidenceRequirements = []agentcore.TaskEvidenceInput{{ID: "evidence-command", Description: "Knowledge record was stored.", Kind: string(agentcore.EvidenceKindToolLog), Required: true}}
	manifest := func(sourceRef string) string {
		return materializerManifestOutput("Stored the knowledge record.",
			`{"version":1,"summary":"Knowledge record stored.","deliverables":[{"expected_deliverable_id":"deliverable-report","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-command","acceptance_criterion_id":"acceptance-1","source_kind":"tool_log","source_ref":"`+sourceRef+`"}]}`)
	}
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: manifest("kdoc-1")}},
		ToolCalls: []agentcore.RuntimeToolCall{{
			ID: "call-knowledge-add", Name: "knowledge_add", Output: `{"status":"parsing","document_id":"kdoc-1"}`,
		}},
	}
	normalizer := missionTaskAgentResultNormalizer{store: env.FileStore}

	result, err := normalizer.NormalizeContext(ctx, input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].ToolLogRef.ID != "call-knowledge-add" {
		t.Fatalf("tool evidence = %+v", result.Evidence)
	}

	response.ToolCalls = append(response.ToolCalls, agentcore.RuntimeToolCall{
		ID: "call-knowledge-read", Name: "knowledge_get", Output: `{"status":"completed","document_id":"kdoc-1"}`,
	})
	if _, err := normalizer.NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "matches multiple tool calls") {
		t.Fatalf("ambiguous output reference error = %v", err)
	}

	response.ToolCalls = []agentcore.RuntimeToolCall{{
		ID: "call-knowledge-failed", Name: "knowledge_add", Output: `{"status":"failed","error_code":"tool_execution_failed","document_id":"kdoc-1"}`,
	}}
	if _, err := normalizer.NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "was not produced by this run") {
		t.Fatalf("failed output reference error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerRequiresStructuredManifest(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "I am done."}},
	}
	if _, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "requires an orrery-agent-result manifest") {
		t.Fatalf("NormalizeContext error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerDerivesSimpleResponsibilityResultFromTaskContract(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := responsibilityMaterializerTestInput()
	output := "Assessment completed from current responsibility evidence.\n\n```orrery-agent-result\n" +
		`{"version":"1.0","summary":"Model-authored receipt is intentionally not trusted.","deliverables":[{"expected_deliverable_id":"invented-deliverable","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"invented-evidence","acceptance_criterion_id":"invented-criterion","source_kind":"tool_log","source_ref":"invented-call"}]}` +
		"\n```"
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted, RawOutputRef: "artifact://trace-responsibility-1",
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: output}},
		ToolCalls: []agentcore.RuntimeToolCall{
			{ID: "call-knowledge-1", Name: "knowledge_search", Output: `{"status":"completed","matches":1}`},
			{ID: "call-memory-failed", Name: "memory_search", Output: `{"status":"failed","error":"unavailable"}`},
			{ID: "call-graph-2", Name: "enterprise_graph_search_objects", Output: `{"status":"completed","matches":2}`},
			{ID: "call-plan-complete", Name: "plan_complete", Output: `{"status":"completed","plan_id":"plan-1"}`},
		},
	}

	result, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Deliverables) != 1 || result.Deliverables[0].ExpectedDeliverableID != "deliverable-step-1" || result.Deliverables[0].Title != "Responsibility assessment" {
		t.Fatalf("derived deliverables = %+v", result.Deliverables)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].EvidenceRequirementID != "evidence-step-1" ||
		result.Evidence[0].AcceptanceCriterionID != "acceptance-step-1" || result.Evidence[0].Kind != agentcore.EvidenceKindToolLog ||
		result.Evidence[0].ToolLogRef.ID != "call-graph-2" {
		t.Fatalf("derived evidence = %+v", result.Evidence)
	}
	artifact, err := env.FileStore.Get(ctx, result.Deliverables[0].ArtifactRef.ID)
	if err != nil {
		t.Fatalf("Get persisted responsibility result: %v", err)
	}
	if string(artifact.Content) != output {
		t.Fatalf("persisted responsibility result = %q", artifact.Content)
	}
	for name, validator := range map[string]agentcore.ResultValidator{
		"evidence": productionEvidenceResultValidator{},
		"qa":       productionQAResultValidator{},
		"policy":   productionPolicyResultValidator{},
	} {
		if err := validator.Validate(input, result); err != nil {
			t.Fatalf("%s validator: %v", name, err)
		}
	}
}

func TestMissionTaskAgentResultNormalizerRequiresSuccessfulToolEvidenceForResponsibilityResult(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := responsibilityMaterializerTestInput()
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "Assessment completed."}},
		ToolCalls: []agentcore.RuntimeToolCall{
			{ID: "call-failed", Name: "knowledge_search", Output: `{"status":"failed","error_code":"tool_execution_failed"}`},
			{ID: "call-unstructured", Name: "memory_search", Output: "not a structured tool receipt"},
			{ID: "call-plan-complete", Name: "plan_complete", Output: `{"status":"completed","plan_id":"plan-1"}`},
		},
	}

	if _, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "requires an orrery-agent-result manifest") {
		t.Fatalf("NormalizeContext error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerRequiresTaskScopesWithinResponsibilityPolicy(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := responsibilityMaterializerTestInput()
	input.ContextPack.ResponsibilityContext.PolicyToolScopes = []string{"knowledge.read"}
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages:  []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "Assessment completed."}},
		ToolCalls: []agentcore.RuntimeToolCall{{ID: "call-knowledge", Name: "knowledge_search", Output: `{"status":"completed","matches":1}`}},
	}

	if _, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "requires an orrery-agent-result manifest") {
		t.Fatalf("NormalizeContext error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerKeepsComplexResponsibilityResultsStrict(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	input := responsibilityMaterializerTestInput()
	input.Task.ExpectedDeliverables = append(input.Task.ExpectedDeliverables, agentcore.TaskDeliverableInput{ID: "deliverable-step-2", Title: "Follow-up plan"})
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages:  []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "Assessment completed."}},
		ToolCalls: []agentcore.RuntimeToolCall{{ID: "call-knowledge", Name: "knowledge_search", Output: `{"status":"completed","matches":1}`}},
	}

	if _, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, time.Now()); err == nil || !strings.Contains(err.Error(), "requires an orrery-agent-result manifest") {
		t.Fatalf("NormalizeContext error = %v", err)
	}
}

func TestMissionTaskAgentResultNormalizerBindsHostVerifiedScheduledArtifact(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	now := time.Date(2026, 8, 6, 4, 30, 0, 0, time.UTC)
	reportPath := filepath.Join(t.TempDir(), "tangdou-github-report.md")
	if err := os.WriteFile(reportPath, []byte("# Located Issues\n\nNo critical findings.\n"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if err := os.Chtimes(reportPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("set report timestamp: %v", err)
	}
	input := scheduledMaterializerTestInput(t, reportPath)
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted, CompletedAt: now, DurationMs: int64((2 * time.Minute) / time.Millisecond),
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed the scheduled security report.",
			`{"version":1,"outcome":"completed","summary":"Security report completed.","deliverables":[{"expected_deliverable_id":"located-issues","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}],"evidence":[{"evidence_requirement_id":"evidence-located-issues","acceptance_criterion_id":"acceptance-scheduled","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}]}`,
		)}},
	}

	normalizer := missionTaskAgentResultNormalizer{store: env.FileStore, scheduledArtifacts: newProductionSchedulerArtifactStore("", env.Config.FileStore, env.FileStore, nil)}
	result, err := normalizer.NormalizeContext(ctx, input, response, now)
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if len(result.Deliverables) != 1 || !strings.HasPrefix(result.Deliverables[0].ArtifactRef.URI, "artifact://scheduled-runs/") {
		t.Fatalf("scheduled deliverables = %+v", result.Deliverables)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].ArtifactRef.URI != result.Deliverables[0].ArtifactRef.URI || result.Evidence[0].EvidenceRequirementID != "evidence-located-issues" {
		t.Fatalf("scheduled evidence = %+v", result.Evidence)
	}
}

func TestMissionTaskAgentResultNormalizerRejectsScheduledArtifactWithoutOutcomeManifest(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	now := time.Date(2026, 8, 6, 4, 30, 0, 0, time.UTC)
	reportPath := filepath.Join(t.TempDir(), "tangdou-github-report.md")
	if err := os.WriteFile(reportPath, []byte("# Located Issues\n\nNo critical findings.\n"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if err := os.Chtimes(reportPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("set report timestamp: %v", err)
	}
	input := scheduledMaterializerTestInput(t, reportPath)
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted, CompletedAt: now, DurationMs: int64((2 * time.Minute) / time.Millisecond),
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "Completed the scheduled security report."}},
	}

	normalizer := missionTaskAgentResultNormalizer{store: env.FileStore, scheduledArtifacts: newProductionSchedulerArtifactStore("", env.Config.FileStore, env.FileStore, nil)}
	_, err := normalizer.NormalizeContext(ctx, input, response, now)
	if err == nil || !strings.Contains(err.Error(), "requires an orrery-agent-result manifest with an outcome") {
		t.Fatalf("NormalizeContext error = %v, want missing scheduled outcome manifest", err)
	}
}

func TestMissionTaskAgentResultNormalizerPersistsFormalScheduledBlockedOutcome(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	input := scheduledMaterializerTestInput(t, filepath.Join(t.TempDir(), "unused-report.md"))
	input.RuntimeOptions.ProviderSpecific["scheduled_run_id"] = "scheduled-run-blocked-materializer"
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Ontology validation is pending.",
			`{"version":1,"outcome":"blocked","summary":"Ontology association was not confirmed before the run deadline.","deliverables":[],"evidence":[]}`,
		)}},
	}

	archiver := newProductionSchedulerArtifactStore("", env.Config.FileStore, env.FileStore, nil)
	result, err := (missionTaskAgentResultNormalizer{scheduledArtifacts: archiver}).NormalizeContext(context.Background(), input, response, time.Now())
	if err != nil {
		t.Fatalf("NormalizeContext: %v", err)
	}
	if result.Blocker == nil || result.Blocker.Summary != "Ontology association was not confirmed before the run deadline." || result.Summary.Text != result.Blocker.Summary {
		t.Fatalf("blocked result = %+v", result)
	}
	if len(result.Deliverables) != 0 || len(result.Evidence) != 0 {
		t.Fatalf("blocked result must not claim incomplete contract outputs: %+v", result)
	}
	if result.Blocker.Report.IsZero() || !strings.HasPrefix(result.Blocker.Report.URI, "artifact://scheduled-runs/") {
		t.Fatalf("blocked report was not archived: %+v", result.Blocker)
	}
	// BaseAgent attaches the persisted trace after normalization, before validation.
	result.InternalTraceRef = "artifact://blocked-runtime-trace"
	for name, validator := range map[string]agentcore.ResultValidator{
		"evidence": productionEvidenceResultValidator{},
		"qa":       productionQAResultValidator{},
		"policy":   productionPolicyResultValidator{},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validator.Validate(input, result); err != nil {
				t.Fatalf("normalized blocked result must reach the result committer: %#v", err)
			}
		})
	}
}

func TestMissionTaskAgentResultNormalizerRejectsStaleScheduledArtifact(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	now := time.Date(2026, 8, 6, 4, 30, 0, 0, time.UTC)
	reportPath := filepath.Join(t.TempDir(), "tangdou-github-report.md")
	if err := os.WriteFile(reportPath, []byte("# Located Issues\n\nHistorical report.\n"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if err := os.Chtimes(reportPath, now.Add(-10*time.Minute), now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("set report timestamp: %v", err)
	}
	input := scheduledMaterializerTestInput(t, reportPath)
	response := agentcore.RuntimeResponse{
		RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted, CompletedAt: now, DurationMs: int64((2 * time.Minute) / time.Millisecond),
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"Completed the scheduled security report.",
			`{"version":1,"outcome":"completed","summary":"Security report completed.","deliverables":[{"expected_deliverable_id":"located-issues","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}],"evidence":[{"evidence_requirement_id":"evidence-located-issues","acceptance_criterion_id":"acceptance-scheduled","source_kind":"runtime_artifact","source_ref":"[local-path-redacted]"}]}`,
		)}},
	}

	_, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).NormalizeContext(ctx, input, response, now)
	if err == nil || !strings.Contains(err.Error(), "was not produced for this run") {
		t.Fatalf("NormalizeContext error = %v, want stale artifact rejection", err)
	}
}

func scheduledMaterializerTestInput(t *testing.T, reportPath string) agentcore.AgentInput {
	t.Helper()
	contract, err := json.Marshal(schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "located-issues", Kind: "report", PathPattern: reportPath, MinBytes: 16,
	}}})
	if err != nil {
		t.Fatalf("encode result contract: %v", err)
	}
	return agentcore.AgentInput{
		RunID: "run-scheduled-materializer-1", TenantID: "system",
		ContextPack: agentcore.ContextPack{AgentID: "tangdou", MissionID: "mission-scheduled", TaskID: "task-scheduled"},
		RuntimeOptions: agentcore.RuntimeOptions{
			ProviderSpecific: map[string]string{"run_source": schedulerAgentTaskRunSource, schedulerResultContractProviderKey: string(contract)},
			WorkspacePolicy:  agentcore.RuntimeWorkspacePolicy{WorkDir: filepath.Dir(reportPath)},
		},
		Task: agentcore.TaskInput{
			ID: "task-scheduled", MissionID: "mission-scheduled", Goal: "Produce the report", WorkerPersonalGoal: "Produce the report",
			ExpectedDeliverables: []agentcore.TaskDeliverableInput{{ID: "located-issues", Title: "Located issues report"}},
			AcceptanceCriteria:   []agentcore.TaskAcceptanceInput{{ID: "acceptance-scheduled", Description: "Scheduled goal completed."}},
			EvidenceRequirements: []agentcore.TaskEvidenceInput{{ID: "evidence-located-issues", Description: "Verify report exists.", Kind: string(agentcore.EvidenceKindArtifact), Required: true}},
		},
	}
}

func materializerManifestOutput(body, manifest string) string {
	return body + "\n\n```orrery-agent-result\n" + manifest + "\n```"
}

func materializerWorkspaceFile(relativePath, content string) missionResultWorkspaceFile {
	return missionResultWorkspaceFile{
		relativePath: relativePath,
		size:         int64(len(content)),
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(content)), nil
		},
	}
}

func materializerTestInput() agentcore.AgentInput {
	return agentcore.AgentInput{
		RunID: "run-materializer-1", TenantID: "tenant-1",
		ContextPack: agentcore.ContextPack{AgentID: "agent-1", MissionID: "mission-1", TaskID: "task-1"},
		Task: agentcore.TaskInput{
			ID: "task-1", MissionID: "mission-1", Goal: "Produce the report", WorkerPersonalGoal: "Produce the report",
			ExpectedDeliverables: []agentcore.TaskDeliverableInput{{ID: "deliverable-report", Title: "Report"}},
			AcceptanceCriteria: []agentcore.TaskAcceptanceInput{
				{ID: "acceptance-1", Description: "Report includes findings."},
				{ID: "acceptance-2", Description: "Report includes risks."},
			},
			EvidenceRequirements: []agentcore.TaskEvidenceInput{
				{ID: "evidence-findings", Description: "Report includes findings.", Kind: string(agentcore.EvidenceKindArtifact), Required: true},
				{ID: "evidence-risks", Description: "Report includes risks.", Kind: string(agentcore.EvidenceKindArtifact), Required: true},
			},
		},
	}
}

func responsibilityMaterializerTestInput() agentcore.AgentInput {
	input := materializerTestInput()
	input.RunID = "run-responsibility-materializer-1"
	input.ContextPack.MissionID = "mission-responsibility-1"
	input.ContextPack.TaskID = "autoplan-task-1"
	input.ContextPack.ResponsibilityContext = &agentcore.ResponsibilityContext{
		ContextID: "responsibility-context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 3,
		PolicyID: "responsibility-policy-1", PolicyVersion: 2, Source: "people.responsibility_context",
		AgentProfileID: "agent-1", MissionID: "mission-responsibility-1", TaskID: "autoplan-task-1",
		PolicyToolScopes:     []string{"enterprise_graph.read", "knowledge.read", "memory.read"},
		TaskPermissionScopes: []string{"enterprise_graph.read", "knowledge.read", "memory.read"}, RequireExplicitToolScopes: true,
	}
	input.RuntimeToolBindings = []agentcore.RuntimeToolBinding{
		{ToolID: "knowledge_search", Alias: "knowledge_search", GrantedPermissions: []string{"knowledge.read"}},
		{ToolID: "memory_search", Alias: "memory_search", GrantedPermissions: []string{"memory.read"}},
		{ToolID: "enterprise_graph_search_objects", Alias: "enterprise_graph_search_objects", GrantedPermissions: []string{"enterprise_graph.read"}},
	}
	input.Task = agentcore.TaskInput{
		ID: "autoplan-task-1", MissionID: "mission-responsibility-1", Goal: "Assess responsibility performance", WorkerPersonalGoal: "Assess responsibility performance",
		ExpectedDeliverables: []agentcore.TaskDeliverableInput{{ID: "deliverable-step-1", Title: "Responsibility assessment"}},
		AcceptanceCriteria:   []agentcore.TaskAcceptanceInput{{ID: "acceptance-step-1", Description: "Assessment is backed by current responsibility evidence."}},
		EvidenceRequirements: []agentcore.TaskEvidenceInput{{ID: "evidence-step-1", Description: "Assessment is backed by current responsibility evidence.", Kind: string(agentcore.EvidenceKindToolLog), Required: true}},
	}
	return input
}
