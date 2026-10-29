package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/resultreview"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/filestore"
)

const (
	agentResultManifestFence                  = "```orrery-agent-result"
	redactedLocalPathRef                      = "[local-path-redacted]"
	maxMissionResultWorkspaceArtifactFileSize = 64 << 20
)

type missionTaskAgentResultNormalizer struct {
	store              filestore.Store
	scheduledArtifacts *productionSchedulerArtifactStore
	workspaceFiles     func(context.Context) []missionResultWorkspaceFile
	resultReviewer     resultreview.Service
}

type missionResultWorkspaceFile struct {
	relativePath string
	size         int64
	open         func() (io.ReadCloser, error)
}

type agentResultManifest struct {
	Version      agentResultManifestVersion       `json:"version"`
	Outcome      string                           `json:"outcome"`
	Summary      string                           `json:"summary"`
	Deliverables []agentResultManifestDeliverable `json:"deliverables"`
	Evidence     []agentResultManifestEvidence    `json:"evidence"`
}

const (
	agentResultOutcomeCompleted = "completed"
	agentResultOutcomeBlocked   = "blocked"
)

type agentResultManifestVersion int

func (version *agentResultManifestVersion) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if strings.HasPrefix(raw, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		raw = text
	}
	if raw != "1" && raw != "1.0" {
		return fmt.Errorf("orrery-agent-result manifest version must be 1")
	}
	*version = 1
	return nil
}

type agentResultManifestDeliverable struct {
	ExpectedDeliverableID string `json:"expected_deliverable_id"`
	SourceKind            string `json:"source_kind"`
	SourceRef             string `json:"source_ref"`
}

type agentResultManifestEvidence struct {
	EvidenceRequirementID string `json:"evidence_requirement_id"`
	AcceptanceCriterionID string `json:"acceptance_criterion_id"`
	SourceKind            string `json:"source_kind"`
	SourceRef             string `json:"source_ref"`
}

func (n missionTaskAgentResultNormalizer) Normalize(input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (agentcore.AgentResult, error) {
	result, err := (agentcore.DefaultResultNormalizer{}).Normalize(input, response, now)
	if err != nil {
		return agentcore.AgentResult{}, err
	}
	return ensureMissionTaskResultSummary(result, response), nil
}

func (n missionTaskAgentResultNormalizer) NormalizeContext(ctx context.Context, input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (result agentcore.AgentResult, err error) {
	// Capture the executing Agent's chosen public text before the result is
	// committed. Recovery must not ask another model to invent a replacement.
	defer func() {
		if err == nil && strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["run_source"]) == schedulerAgentTaskRunSource {
			result.UserReport = scheduledAgentFinalReport(response)
			if result.Blocker != nil && strings.HasPrefix(result.Blocker.Summary, "Independent AI result review") {
				result.UserReport = ""
			}
		}
	}()
	result, err = n.Normalize(input, response, now)
	if err != nil {
		return agentcore.AgentResult{}, err
	}
	output := runtimeResponseText(response.Messages)
	if submitted, handled, err := n.materializeMarketingWechatSubmission(ctx, input, response, result); handled {
		return submitted, err
	}
	manifest, found, manifestErr := parseAgentResultManifest(output)
	if manifestErr == nil && strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["run_source"]) == schedulerAgentTaskRunSource {
		if !found {
			return agentcore.AgentResult{}, fmt.Errorf("scheduled Mission Task result requires an orrery-agent-result manifest with an outcome")
		}
		switch strings.ToLower(strings.TrimSpace(manifest.Outcome)) {
		case agentResultOutcomeCompleted:
		case agentResultOutcomeBlocked:
			if strings.TrimSpace(manifest.Summary) == "" {
				return agentcore.AgentResult{}, fmt.Errorf("blocked orrery-agent-result manifest summary is required")
			}
			reason := strings.TrimSpace(manifest.Summary)
			result.Summary = agentcore.Summary{Text: reason}
			result.Blocker = &agentcore.Blocker{Summary: reason}
			// A blocked outcome is itself the durable result. It must not be
			// rejected for the deliverables or evidence that remain incomplete.
			result.Deliverables = nil
			result.Evidence = nil
			return n.archiveScheduledBlockedReport(ctx, input, result, output)
		default:
			return agentcore.AgentResult{}, fmt.Errorf("scheduled orrery-agent-result outcome must be completed or blocked")
		}
	}
	formalResult := len(input.Task.ExpectedDeliverables) > 0 || len(input.Task.EvidenceRequirements) > 0 || len(input.Task.AcceptanceCriteria) > 0
	if !formalResult {
		if manifestErr != nil {
			return agentcore.AgentResult{}, manifestErr
		}
		if found && strings.TrimSpace(manifest.Summary) == "" {
			return agentcore.AgentResult{}, fmt.Errorf("orrery-agent-result manifest summary is required")
		}
		if found {
			result.Summary = agentcore.Summary{Text: strings.TrimSpace(manifest.Summary)}
		}
		return n.finalizeScheduledResultArtifacts(ctx, input, result, output)
	}
	if manifestErr == nil {
		if deliverables, evidence, scheduled, scheduledErr := materializeHostVerifiedScheduledResult(input, response); scheduled {
			if scheduledErr != nil {
				return agentcore.AgentResult{}, scheduledErr
			}
			if found && strings.TrimSpace(manifest.Summary) == "" {
				return agentcore.AgentResult{}, fmt.Errorf("orrery-agent-result manifest summary is required")
			}
			if found {
				result.Summary = agentcore.Summary{Text: strings.TrimSpace(manifest.Summary)}
			}
			result.Deliverables = deliverables
			result.Evidence = evidence
			return n.finalizeScheduledResultArtifacts(ctx, input, result, output)
		}
	}
	if responsibilityResult, handled, responsibilityErr := n.materializeHostVerifiedResponsibilityResult(ctx, input, response, result, output); handled {
		return responsibilityResult, responsibilityErr
	}
	if manifestErr != nil {
		return agentcore.AgentResult{}, manifestErr
	}
	if found && strings.TrimSpace(manifest.Summary) == "" {
		return agentcore.AgentResult{}, fmt.Errorf("orrery-agent-result manifest summary is required")
	}
	if found {
		result.Summary = agentcore.Summary{Text: strings.TrimSpace(manifest.Summary)}
	}
	if !found {
		return agentcore.AgentResult{}, fmt.Errorf("formal Mission Task result requires an orrery-agent-result manifest")
	}
	response, workspaceArtifact, err := n.bindRedactedWorkspaceArtifact(ctx, input, response, manifest)
	if err != nil {
		return agentcore.AgentResult{}, err
	}
	if strings.TrimSpace(output) == "" && len(response.Artifacts) == 0 {
		return agentcore.AgentResult{}, fmt.Errorf("formal Mission Task result requires runtime output or a runtime artifact")
	}

	var outputArtifact agentcore.ArtifactRef
	if strings.TrimSpace(output) != "" {
		outputArtifact, err = n.persistRuntimeOutput(ctx, input, output)
		if err != nil {
			return agentcore.AgentResult{}, n.cleanupPersistedArtifacts(ctx, err, workspaceArtifact)
		}
	}
	result.Deliverables, err = materializeMissionTaskDeliverables(input, response, outputArtifact, manifest, found)
	if err != nil {
		return agentcore.AgentResult{}, n.cleanupPersistedArtifacts(ctx, err, outputArtifact, workspaceArtifact)
	}
	result.Evidence, err = materializeMissionTaskEvidence(input, response, outputArtifact, manifest, found)
	if err != nil {
		return agentcore.AgentResult{}, n.cleanupPersistedArtifacts(ctx, err, outputArtifact, workspaceArtifact)
	}
	return n.finalizeScheduledResultArtifacts(ctx, input, result, output)
}

func (n missionTaskAgentResultNormalizer) materializeHostVerifiedResponsibilityResult(
	ctx context.Context,
	input agentcore.AgentInput,
	response agentcore.RuntimeResponse,
	result agentcore.AgentResult,
	output string,
) (agentcore.AgentResult, bool, error) {
	responsibility := input.ContextPack.ResponsibilityContext
	if responsibility == nil || response.Status != agentcore.RuntimeStatusCompleted || strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["run_source"]) == schedulerAgentTaskRunSource ||
		strings.TrimSpace(responsibility.ContextID) == "" || strings.TrimSpace(responsibility.ResponsibilityID) == "" || responsibility.ResponsibilityVersion < 1 ||
		strings.TrimSpace(responsibility.AgentProfileID) != strings.TrimSpace(string(input.ContextPack.AgentID)) ||
		strings.TrimSpace(responsibility.MissionID) != strings.TrimSpace(input.Task.MissionID) ||
		strings.TrimSpace(responsibility.TaskID) != strings.TrimSpace(input.Task.ID) ||
		strings.TrimSpace(responsibility.MissionID) != strings.TrimSpace(string(input.ContextPack.MissionID)) ||
		strings.TrimSpace(responsibility.TaskID) != strings.TrimSpace(string(input.ContextPack.TaskID)) ||
		!responsibility.RequireExplicitToolScopes || !permissionScopesAllowAll(responsibility.PolicyToolScopes, responsibility.TaskPermissionScopes) ||
		len(input.Task.ExpectedDeliverables) != 1 || len(input.Task.AcceptanceCriteria) != 1 || len(input.Task.EvidenceRequirements) != 1 ||
		strings.TrimSpace(output) == "" {
		return agentcore.AgentResult{}, false, nil
	}

	deliverable := input.Task.ExpectedDeliverables[0]
	criterion := input.Task.AcceptanceCriteria[0]
	requirement := input.Task.EvidenceRequirements[0]
	evidenceKind := agentcore.EvidenceKind(strings.TrimSpace(requirement.Kind))
	if strings.TrimSpace(deliverable.ID) == "" || strings.TrimSpace(deliverable.Title) == "" ||
		strings.TrimSpace(criterion.ID) == "" || strings.TrimSpace(requirement.ID) == "" || !requirement.Required ||
		(evidenceKind != agentcore.EvidenceKindToolLog && evidenceKind != agentcore.EvidenceKindTestLog) ||
		!evidenceDescriptionMatchesCriterion(requirement.Description, criterion.Description) {
		return agentcore.AgentResult{}, false, nil
	}
	call, ok := lastSuccessfulRuntimeToolCall(response.ToolCalls, input.RuntimeToolBindings, responsibility.TaskPermissionScopes)
	if !ok {
		return agentcore.AgentResult{}, false, nil
	}

	artifact, err := n.persistRuntimeOutput(ctx, input, output)
	if err != nil {
		return agentcore.AgentResult{}, true, err
	}
	result.Deliverables = []agentcore.Deliverable{{
		ID:                    agentcore.DeliverableID("deliverable-" + deliverable.ID),
		ExpectedDeliverableID: deliverable.ID,
		Title:                 deliverable.Title,
		ArtifactRef:           artifact,
	}}
	result.Evidence = []agentcore.Evidence{
		toolLogEvidence(input.RunID, requirement.ID, criterion.ID, evidenceKind, call.ID),
	}
	return result, true, nil
}

func (n missionTaskAgentResultNormalizer) finalizeScheduledResultArtifacts(ctx context.Context, input agentcore.AgentInput, result agentcore.AgentResult, publicOutput ...string) (agentcore.AgentResult, error) {
	metadata := input.RuntimeOptions.ProviderSpecific
	if n.resultReviewer != nil && strings.TrimSpace(metadata["run_source"]) == schedulerAgentTaskRunSource && result.Blocker == nil {
		result = reviewScheduledAgentResult(ctx, input, result, n.resultReviewer, publicOutput...)
	}
	// A held result is durable as a blocker and must not archive or expose
	// completion artifacts as if the independent review had approved them.
	if result.Blocker != nil {
		return result, nil
	}
	if strings.TrimSpace(metadata["run_source"]) != schedulerAgentTaskRunSource || len(result.Deliverables) == 0 {
		return result, nil
	}
	if n.scheduledArtifacts == nil {
		return agentcore.AgentResult{}, fmt.Errorf("scheduled artifact archiver is not configured")
	}

	refs := make([]agentcore.ArtifactRef, 0, len(result.Deliverables))
	seen := make(map[string]bool, len(result.Deliverables))
	seenIDs := make(map[string]bool, len(result.Deliverables))
	for _, deliverable := range result.Deliverables {
		ref := deliverable.ArtifactRef
		id := strings.TrimSpace(ref.ID)
		if id == "" || seenIDs[id] {
			return agentcore.AgentResult{}, fmt.Errorf("scheduled deliverable artifacts require unique IDs")
		}
		key := strings.TrimSpace(ref.ID) + "\x00" + strings.TrimSpace(ref.URI)
		if ref.IsZero() || seen[key] {
			continue
		}
		seenIDs[id] = true
		seen[key] = true
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return result, nil
	}
	run := runtimedaemon.AgentRun{
		RunID:          input.RunID,
		AgentID:        result.AgentID,
		AgentProfileID: agentcore.AgentProfileID(result.AgentID),
		MissionID:      input.Task.MissionID,
		TaskID:         input.Task.ID,
		Request: agentcore.RuntimeRequest{
			TenantID:          input.TenantID,
			RunID:             input.RunID,
			AgentID:           result.AgentID,
			ExecutionEnvelope: agentcore.CloneExecutionEnvelope(input.ExecutionEnvelope),
			MissionID:         input.Task.MissionID,
			TaskID:            input.Task.ID,
			RuntimeOptions:    input.RuntimeOptions,
		},
	}
	scheduledRunID := firstNonBlank(metadata["scheduled_run_id"], input.RunID.String())
	archived, err := n.scheduledArtifacts.ArchiveRunArtifacts(ctx, run, scheduledRunID, schedulerapp.RunResult{Artifacts: schedulerRunArtifacts(refs)})
	if err != nil {
		return agentcore.AgentResult{}, err
	}
	if len(archived.Artifacts) != len(refs) {
		return agentcore.AgentResult{}, fmt.Errorf("scheduled artifact archiver returned an incomplete result")
	}
	replacements := make(map[string]agentcore.ArtifactRef, len(refs))
	for index, ref := range refs {
		artifact := archived.Artifacts[index]
		replacements[strings.TrimSpace(ref.ID)+"\x00"+strings.TrimSpace(ref.URI)] = agentcore.ArtifactRef{
			ID: ref.ID, Kind: ref.Kind, URI: artifact.URI, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
		}
	}
	for index := range result.Deliverables {
		ref := result.Deliverables[index].ArtifactRef
		if replacement, ok := replacements[strings.TrimSpace(ref.ID)+"\x00"+strings.TrimSpace(ref.URI)]; ok {
			result.Deliverables[index].ArtifactRef = replacement
		}
	}
	for index := range result.Evidence {
		ref := result.Evidence[index].ArtifactRef
		if replacement, ok := replacements[strings.TrimSpace(ref.ID)+"\x00"+strings.TrimSpace(ref.URI)]; ok {
			result.Evidence[index].ArtifactRef = replacement
		}
	}
	return result, nil
}

func materializeHostVerifiedScheduledResult(input agentcore.AgentInput, response agentcore.RuntimeResponse) ([]agentcore.Deliverable, []agentcore.Evidence, bool, error) {
	providerSpecific := input.RuntimeOptions.ProviderSpecific
	if strings.TrimSpace(providerSpecific["run_source"]) != schedulerAgentTaskRunSource {
		return nil, nil, false, nil
	}
	rawContract := strings.TrimSpace(providerSpecific[schedulerResultContractProviderKey])
	if rawContract == "" {
		return nil, nil, false, nil
	}
	var contract schedulerapp.AgentResultContract
	if err := json.Unmarshal([]byte(rawContract), &contract); err != nil {
		return nil, nil, true, fmt.Errorf("decode scheduler result contract: %w", err)
	}
	if len(contract.Artifacts) == 0 {
		return nil, nil, false, nil
	}
	if response.CompletedAt.IsZero() || response.DurationMs <= 0 {
		return nil, nil, true, fmt.Errorf("scheduled runtime timing is required to verify artifacts")
	}
	runtimeDuration := time.Duration(response.DurationMs) * time.Millisecond
	if runtimeDuration <= 0 {
		return nil, nil, true, fmt.Errorf("scheduled runtime duration is invalid")
	}
	startedAt := response.CompletedAt.Add(-runtimeDuration)
	if len(input.Task.AcceptanceCriteria) != 1 {
		return nil, nil, true, fmt.Errorf("scheduled artifact contract requires one acceptance criterion")
	}

	expected := make(map[string]agentcore.TaskDeliverableInput, len(input.Task.ExpectedDeliverables))
	for _, item := range input.Task.ExpectedDeliverables {
		expected[strings.TrimSpace(item.ID)] = item
	}
	requirements := make(map[string]agentcore.TaskEvidenceInput, len(input.Task.EvidenceRequirements))
	for _, item := range input.Task.EvidenceRequirements {
		requirements[strings.TrimSpace(item.ID)] = item
	}
	criterion := input.Task.AcceptanceCriteria[0]
	deliverables := make([]agentcore.Deliverable, 0, len(contract.Artifacts))
	evidence := make([]agentcore.Evidence, 0, len(contract.Artifacts))
	for _, requirement := range contract.Artifacts {
		id := strings.TrimSpace(requirement.ID)
		expectedDeliverable, ok := expected[id]
		if !ok {
			return nil, nil, true, fmt.Errorf("scheduler result contract artifact %q has no expected deliverable", id)
		}
		evidenceRequirementID := "evidence-" + id
		evidenceRequirement, ok := requirements[evidenceRequirementID]
		if !ok || agentcore.EvidenceKind(strings.TrimSpace(evidenceRequirement.Kind)) != agentcore.EvidenceKindArtifact {
			return nil, nil, true, fmt.Errorf("scheduler result contract artifact %q has no artifact evidence requirement", id)
		}
		path, _, err := resolveScheduledArtifact(requirement, startedAt)
		if err != nil {
			return nil, nil, true, err
		}
		artifact := agentcore.ArtifactRef{
			ID:   "scheduled-artifact:" + input.RunID.String() + ":" + id,
			Kind: agentcore.ArtifactKind(requirement.Kind),
			URI:  path,
		}
		deliverables = append(deliverables, agentcore.Deliverable{
			ID:                    agentcore.DeliverableID("deliverable-" + id),
			ExpectedDeliverableID: id,
			Title:                 expectedDeliverable.Title,
			ArtifactRef:           artifact,
		})
		evidence = append(evidence, artifactEvidence(
			input.RunID,
			evidenceRequirement.ID,
			criterion.ID,
			agentcore.EvidenceKindArtifact,
			artifact,
			"runtime_artifact",
			artifact.ID,
		))
	}
	if len(deliverables) != len(expected) || len(evidence) != len(requirements) {
		return nil, nil, true, fmt.Errorf("scheduler result contract does not cover every expected deliverable and evidence requirement")
	}
	return deliverables, evidence, true, nil
}

func (n missionTaskAgentResultNormalizer) bindRedactedWorkspaceArtifact(ctx context.Context, input agentcore.AgentInput, response agentcore.RuntimeResponse, manifest agentResultManifest) (agentcore.RuntimeResponse, agentcore.ArtifactRef, error) {
	if !manifestReferencesRedactedRuntimeArtifact(manifest) {
		return response, agentcore.ArtifactRef{}, nil
	}
	response.Artifacts = nonZeroRuntimeArtifacts(response.Artifacts)
	if len(response.Artifacts) == 1 {
		return response, agentcore.ArtifactRef{}, nil
	}
	if len(response.Artifacts) > 1 {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("redacted runtime artifact reference is ambiguous across %d runtime artifacts", len(response.Artifacts))
	}
	files := n.generatedMissionWorkspaceFiles(ctx)
	if len(files) == 0 {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("redacted runtime artifact reference has no host-verified file produced by this run")
	}
	if len(files) > 1 {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("redacted runtime artifact reference is ambiguous across %d host-verified files", len(files))
	}
	if n.store == nil {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("result artifact store is not configured")
	}
	candidate := files[0]
	if candidate.open == nil || strings.TrimSpace(candidate.relativePath) == "" || candidate.size < 0 || candidate.size > maxMissionResultWorkspaceArtifactFileSize {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("host-verified runtime artifact is invalid")
	}
	reader, err := candidate.open()
	if err != nil {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("open host-verified runtime artifact: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, candidate.size+1))
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("read host-verified runtime artifact: %w", err)
	}
	if int64(len(content)) != candidate.size {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("host-verified runtime artifact changed before persistence")
	}
	digest := sha256.Sum256(content)
	key := missionResultArtifactPrefix(input.TenantID, input.RunID.String()) + "workspace-artifact-" + hex.EncodeToString(digest[:])
	contentType := mime.TypeByExtension(filepath.Ext(candidate.relativePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	uri, err := n.store.Upload(ctx, key, content, contentType, filepath.Base(candidate.relativePath))
	if err != nil {
		return response, agentcore.ArtifactRef{}, fmt.Errorf("persist host-verified runtime artifact: %w", err)
	}
	artifact := agentcore.ArtifactRef{ID: key, Kind: missionWorkspaceArtifactKind(candidate.relativePath, contentType), URI: uri, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(content))}
	response.Artifacts = append(response.Artifacts, artifact)
	return response, artifact, nil
}

func nonZeroRuntimeArtifacts(artifacts []agentcore.ArtifactRef) []agentcore.ArtifactRef {
	out := make([]agentcore.ArtifactRef, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !artifact.IsZero() {
			out = append(out, artifact)
		}
	}
	return out
}

func (n missionTaskAgentResultNormalizer) generatedMissionWorkspaceFiles(ctx context.Context) []missionResultWorkspaceFile {
	if n.workspaceFiles != nil {
		return n.workspaceFiles(ctx)
	}
	workspace, ok := productionAgentResultWorkspaceFromContext(ctx)
	if !ok {
		return nil
	}
	prepared := workspace.GeneratedFiles()
	out := make([]missionResultWorkspaceFile, 0, len(prepared))
	for _, file := range prepared {
		file := file
		out = append(out, missionResultWorkspaceFile{
			relativePath: file.RelativePath,
			size:         file.Size,
			open: func() (io.ReadCloser, error) {
				return file.Open()
			},
		})
	}
	return out
}

func manifestReferencesRedactedRuntimeArtifact(manifest agentResultManifest) bool {
	for _, entry := range manifest.Deliverables {
		if strings.TrimSpace(entry.SourceKind) == "runtime_artifact" && isRedactedLocalPathRef(entry.SourceRef) {
			return true
		}
	}
	for _, entry := range manifest.Evidence {
		if strings.TrimSpace(entry.SourceKind) == "runtime_artifact" && isRedactedLocalPathRef(entry.SourceRef) {
			return true
		}
	}
	return false
}

func isRedactedLocalPathRef(sourceRef string) bool {
	switch strings.TrimSpace(sourceRef) {
	case redactedLocalPathRef, "[redacted-path]":
		return true
	default:
		return false
	}
}

func missionWorkspaceArtifactKind(relativePath, contentType string) agentcore.ArtifactKind {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(relativePath))) {
	case ".html", ".htm", ".css", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".go", ".py", ".rs", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".json", ".yaml", ".yml", ".toml", ".xml", ".sql", ".sh", ".ps1":
		return agentcore.ArtifactKindCode
	case ".log":
		return agentcore.ArtifactKindLog
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "video/") {
		return agentcore.ArtifactKindVisual
	}
	return agentcore.ArtifactKindReport
}

func (n missionTaskAgentResultNormalizer) cleanupPersistedArtifacts(ctx context.Context, cause error, artifacts ...agentcore.ArtifactRef) error {
	if n.store == nil {
		return cause
	}
	seen := map[string]bool{}
	for _, artifact := range artifacts {
		id := strings.TrimSpace(artifact.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if err := n.store.Delete(ctx, id); err != nil {
			cause = errors.Join(cause, fmt.Errorf("delete rejected Agent result artifact %q: %w", id, err))
		}
	}
	return cause
}

func ensureMissionTaskResultSummary(result agentcore.AgentResult, response agentcore.RuntimeResponse) agentcore.AgentResult {
	if result.Summary.IsZero() {
		result.Summary = agentcore.Summary{Text: runtimeResponseText(response.Messages)}
	}
	if result.Summary.IsZero() {
		result.Summary = agentcore.Summary{Text: "Agent run completed."}
	}
	return result
}

func runtimeResponseText(messages []agentcore.RuntimeMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Type == agentcore.RuntimeMessageText && message.Status == agentcore.RuntimeMessageStatusFinal && strings.TrimSpace(message.Content) != "" {
			return strings.TrimSpace(message.Content)
		}
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if message := messages[index]; message.Type == agentcore.RuntimeMessageText && strings.TrimSpace(message.Content) != "" {
			return strings.TrimSpace(message.Content)
		}
	}
	return ""
}

func parseAgentResultManifest(output string) (agentResultManifest, bool, error) {
	start := strings.LastIndex(output, agentResultManifestFence)
	if start < 0 {
		return agentResultManifest{}, false, nil
	}
	raw := output[start+len(agentResultManifestFence):]
	if lineEnd := strings.IndexByte(raw, '\n'); lineEnd >= 0 && strings.TrimSpace(raw[:lineEnd]) == "" {
		raw = raw[lineEnd+1:]
	}
	end := strings.Index(raw, "```")
	if end >= 0 {
		raw = raw[:end]
	}
	raw = strings.TrimSpace(raw)
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest agentResultManifest
	if err := decoder.Decode(&manifest); err != nil {
		return agentResultManifest{}, true, fmt.Errorf("decode orrery-agent-result manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return agentResultManifest{}, true, fmt.Errorf("orrery-agent-result manifest contains trailing JSON")
	}
	if manifest.Version != 1 {
		return agentResultManifest{}, true, fmt.Errorf("orrery-agent-result manifest version must be 1")
	}
	return manifest, true, nil
}

func (n missionTaskAgentResultNormalizer) persistRuntimeOutput(ctx context.Context, input agentcore.AgentInput, output string) (agentcore.ArtifactRef, error) {
	if n.store == nil {
		return agentcore.ArtifactRef{}, fmt.Errorf("result artifact store is not configured")
	}
	digest := sha256.Sum256([]byte(output))
	key := missionResultArtifactPrefix(input.TenantID, input.RunID.String()) + "result-output-" + hex.EncodeToString(digest[:]) + ".md"
	uri, err := n.store.Upload(ctx, key, []byte(output), "text/markdown", "agent-result.md")
	if err != nil {
		return agentcore.ArtifactRef{}, fmt.Errorf("persist Agent result output: %w", err)
	}
	return agentcore.ArtifactRef{ID: key, Kind: agentcore.ArtifactKindReport, URI: uri, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(output))}, nil
}

func materializeMissionTaskDeliverables(input agentcore.AgentInput, response agentcore.RuntimeResponse, output agentcore.ArtifactRef, manifest agentResultManifest, found bool) ([]agentcore.Deliverable, error) {
	expected := make(map[string]agentcore.TaskDeliverableInput, len(input.Task.ExpectedDeliverables))
	for _, item := range input.Task.ExpectedDeliverables {
		expected[strings.TrimSpace(item.ID)] = item
	}
	if len(expected) == 0 {
		return nil, nil
	}
	entries := manifest.Deliverables
	if found && len(input.Task.ExpectedDeliverables) == 1 && len(entries) != 1 {
		if output.IsZero() {
			return nil, fmt.Errorf("expected deliverable has no persisted runtime output")
		}
		item := input.Task.ExpectedDeliverables[0]
		return []agentcore.Deliverable{{ID: agentcore.DeliverableID("deliverable-" + item.ID), ExpectedDeliverableID: item.ID, Title: item.Title, ArtifactRef: output}}, nil
	}
	if !found {
		if len(input.Task.ExpectedDeliverables) == 1 {
			source := output
			if len(response.Artifacts) > 0 {
				source = response.Artifacts[0]
			}
			if source.IsZero() {
				return nil, fmt.Errorf("expected deliverable has no persisted runtime output")
			}
			item := input.Task.ExpectedDeliverables[0]
			return []agentcore.Deliverable{{ID: agentcore.DeliverableID("deliverable-" + item.ID), ExpectedDeliverableID: item.ID, Title: item.Title, ArtifactRef: source}}, nil
		}
		if len(response.Artifacts) < len(input.Task.ExpectedDeliverables) {
			return nil, fmt.Errorf("multiple expected deliverables require explicit orrery-agent-result mappings")
		}
		out := make([]agentcore.Deliverable, 0, len(input.Task.ExpectedDeliverables))
		for index, item := range input.Task.ExpectedDeliverables {
			out = append(out, agentcore.Deliverable{ID: agentcore.DeliverableID("deliverable-" + item.ID), ExpectedDeliverableID: item.ID, Title: item.Title, ArtifactRef: response.Artifacts[index]})
		}
		return out, nil
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("orrery-agent-result manifest must map every expected deliverable")
	}
	seen := map[string]bool{}
	out := make([]agentcore.Deliverable, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ExpectedDeliverableID)
		item, ok := expected[id]
		if !ok && id != "" && len(input.Task.ExpectedDeliverables) == 1 && len(entries) == 1 {
			for canonicalID, canonicalItem := range expected {
				id, item, ok = canonicalID, canonicalItem, canonicalID != ""
			}
		}
		if !ok || seen[id] {
			return nil, fmt.Errorf("orrery-agent-result contains an unknown or duplicate expected deliverable %q", id)
		}
		artifact, err := resolveManifestDeliverableSource(response, output, entry.SourceKind, entry.SourceRef)
		if err != nil {
			return nil, fmt.Errorf("deliverable %s: %w", id, err)
		}
		seen[id] = true
		out = append(out, agentcore.Deliverable{ID: agentcore.DeliverableID("deliverable-" + id), ExpectedDeliverableID: id, Title: item.Title, ArtifactRef: artifact})
	}
	if len(seen) != len(expected) {
		return nil, fmt.Errorf("orrery-agent-result does not map every expected deliverable")
	}
	return out, nil
}

func materializeMissionTaskEvidence(input agentcore.AgentInput, response agentcore.RuntimeResponse, output agentcore.ArtifactRef, manifest agentResultManifest, found bool) ([]agentcore.Evidence, error) {
	requirements := make(map[string]agentcore.TaskEvidenceInput, len(input.Task.EvidenceRequirements))
	criteria := make(map[string]agentcore.TaskAcceptanceInput, len(input.Task.AcceptanceCriteria))
	for _, item := range input.Task.EvidenceRequirements {
		requirements[strings.TrimSpace(item.ID)] = item
	}
	for _, item := range input.Task.AcceptanceCriteria {
		criteria[strings.TrimSpace(item.ID)] = item
	}
	if len(requirements) == 0 {
		if len(criteria) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("acceptance criteria require explicit evidence requirements")
	}
	if found {
		if len(manifest.Evidence) == 0 {
			return nil, fmt.Errorf("orrery-agent-result manifest must map required evidence")
		}
		seenMappings := map[string]bool{}
		mappedRequirements := map[string]bool{}
		out := make([]agentcore.Evidence, 0, len(manifest.Evidence))
		for _, entry := range manifest.Evidence {
			requirementID := strings.TrimSpace(entry.EvidenceRequirementID)
			criterionID := strings.TrimSpace(entry.AcceptanceCriterionID)
			requirement, requirementOK := requirements[requirementID]
			criterion, criterionOK := criteria[criterionID]
			mappingKey := requirementID + "\x00" + criterionID
			if !requirementOK || !criterionOK || seenMappings[mappingKey] {
				return nil, fmt.Errorf("orrery-agent-result contains an unknown or duplicate evidence mapping %q", requirementID)
			}
			evidence, err := resolveManifestEvidenceSource(input.RunID, response, output, requirement, criterion, entry)
			if err != nil {
				return nil, fmt.Errorf("evidence %s: %w", requirementID, err)
			}
			seenMappings[mappingKey] = true
			mappedRequirements[requirementID] = true
			out = append(out, evidence)
		}
		for id, requirement := range requirements {
			if requirement.Required && !mappedRequirements[id] {
				return nil, fmt.Errorf("orrery-agent-result is missing required evidence %q", id)
			}
		}
		return out, nil
	}

	out := make([]agentcore.Evidence, 0, len(requirements))
	for _, requirement := range input.Task.EvidenceRequirements {
		criterion, ok := matchingAcceptanceCriterion(requirement, input.Task.AcceptanceCriteria)
		if !ok {
			continue
		}
		switch agentcore.EvidenceKind(strings.TrimSpace(requirement.Kind)) {
		case agentcore.EvidenceKindArtifact:
			if !output.IsZero() {
				out = append(out, artifactEvidence(input.RunID, requirement.ID, criterion.ID, agentcore.EvidenceKindArtifact, output, "runtime_output", ""))
			}
		case agentcore.EvidenceKindToolLog, agentcore.EvidenceKindTestLog:
			if call, ok := runtimeToolCallByID(response.ToolCalls, requirement.ID); ok {
				out = append(out, toolLogEvidence(input.RunID, requirement.ID, criterion.ID, agentcore.EvidenceKind(requirement.Kind), call.ID))
			}
		}
	}
	return out, nil
}

var _ agentcore.ContextResultNormalizer = missionTaskAgentResultNormalizer{}
var _ agentcore.ResultNormalizer = missionTaskAgentResultNormalizer{}
