package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/permissionscope"
)

func resolveManifestDeliverableSource(response agentcore.RuntimeResponse, output agentcore.ArtifactRef, sourceKind, sourceRef string) (agentcore.ArtifactRef, error) {
	switch strings.TrimSpace(sourceKind) {
	case "runtime_output":
		if output.IsZero() {
			return agentcore.ArtifactRef{}, fmt.Errorf("runtime output artifact is unavailable")
		}
		return output, nil
	case "runtime_artifact":
		if artifact, ok := runtimeArtifactByRef(response.Artifacts, sourceRef); ok {
			return artifact, nil
		}
		return agentcore.ArtifactRef{}, fmt.Errorf("runtime artifact %q was not produced by this run", sourceRef)
	case "external_url":
		if err := validateExternalEvidenceURL(sourceRef); err != nil {
			return agentcore.ArtifactRef{}, err
		}
		digest := sha256.Sum256([]byte(strings.TrimSpace(sourceRef)))
		return agentcore.ArtifactRef{ID: "external-" + hex.EncodeToString(digest[:12]), Kind: agentcore.ArtifactKindURL, URI: strings.TrimSpace(sourceRef)}, nil
	default:
		return agentcore.ArtifactRef{}, fmt.Errorf("unsupported deliverable source_kind %q", sourceKind)
	}
}

func resolveManifestEvidenceSource(runID agentcore.AgentRunID, response agentcore.RuntimeResponse, output agentcore.ArtifactRef, requirement agentcore.TaskEvidenceInput, criterion agentcore.TaskAcceptanceInput, entry agentResultManifestEvidence) (agentcore.Evidence, error) {
	kind := agentcore.EvidenceKind(strings.TrimSpace(requirement.Kind))
	sourceKind := strings.TrimSpace(entry.SourceKind)
	sourceRef := strings.TrimSpace(entry.SourceRef)
	switch sourceKind {
	case "runtime_output":
		return agentcore.Evidence{}, fmt.Errorf("runtime_output is Agent-generated explanation and cannot satisfy evidence requirements")
	case "runtime_artifact":
		if kind != agentcore.EvidenceKindArtifact && kind != agentcore.EvidenceKindTestLog {
			return agentcore.Evidence{}, fmt.Errorf("runtime_artifact does not satisfy %s evidence", kind)
		}
		artifact, ok := runtimeArtifactByRef(response.Artifacts, sourceRef)
		if !ok {
			return agentcore.Evidence{}, fmt.Errorf("runtime artifact %q was not produced by this run", sourceRef)
		}
		return artifactEvidence(runID, requirement.ID, criterion.ID, kind, artifact, sourceKind, sourceRef), nil
	case "tool_log":
		if kind != agentcore.EvidenceKindToolLog && kind != agentcore.EvidenceKindTestLog {
			return agentcore.Evidence{}, fmt.Errorf("tool_log does not satisfy %s evidence", kind)
		}
		call, err := runtimeToolCallByEvidenceRef(response.ToolCalls, sourceRef)
		if err != nil {
			return agentcore.Evidence{}, err
		}
		return toolLogEvidence(runID, requirement.ID, criterion.ID, kind, call.ID), nil
	case "external_url":
		if kind != agentcore.EvidenceKindExternalURL {
			return agentcore.Evidence{}, fmt.Errorf("external_url does not satisfy %s evidence", kind)
		}
		if err := validateExternalEvidenceURL(sourceRef); err != nil {
			return agentcore.Evidence{}, err
		}
		return agentcore.Evidence{
			ID: resultEvidenceID(runID, requirement.ID, criterion.ID, sourceKind, sourceRef), EvidenceRequirementID: requirement.ID,
			AcceptanceCriterionID: criterion.ID, Kind: kind, ExternalURL: sourceRef,
		}, nil
	default:
		return agentcore.Evidence{}, fmt.Errorf("unsupported evidence source_kind %q", sourceKind)
	}
}

func artifactEvidence(runID agentcore.AgentRunID, requirementID, criterionID string, kind agentcore.EvidenceKind, artifact agentcore.ArtifactRef, sourceKind, sourceRef string) agentcore.Evidence {
	return agentcore.Evidence{
		ID: resultEvidenceID(runID, requirementID, criterionID, sourceKind, sourceRef), EvidenceRequirementID: requirementID,
		AcceptanceCriterionID: criterionID, Kind: kind, ArtifactRef: artifact,
	}
}

func toolLogEvidence(runID agentcore.AgentRunID, requirementID, criterionID string, kind agentcore.EvidenceKind, callID string) agentcore.Evidence {
	return agentcore.Evidence{
		ID: resultEvidenceID(runID, requirementID, criterionID, "tool_log", callID), EvidenceRequirementID: requirementID,
		AcceptanceCriterionID: criterionID, Kind: kind, ToolLogRef: agentcore.ToolLogRef{ID: agentcore.ToolLogID(callID)},
	}
}

func resultEvidenceID(runID agentcore.AgentRunID, requirementID, criterionID, sourceKind, sourceRef string) agentcore.EvidenceID {
	digest := sha256.Sum256([]byte(runID.String() + "\x00" + requirementID + "\x00" + criterionID + "\x00" + sourceKind + "\x00" + sourceRef))
	return agentcore.EvidenceID("evidence-" + hex.EncodeToString(digest[:12]))
}

func matchingAcceptanceCriterion(requirement agentcore.TaskEvidenceInput, criteria []agentcore.TaskAcceptanceInput) (agentcore.TaskAcceptanceInput, bool) {
	for _, criterion := range criteria {
		if evidenceDescriptionMatchesCriterion(requirement.Description, criterion.Description) {
			return criterion, true
		}
	}
	return agentcore.TaskAcceptanceInput{}, false
}

func evidenceDescriptionMatchesCriterion(requirement, criterion string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(requirement), " "), strings.Join(strings.Fields(criterion), " "))
}

func runtimeArtifactByRef(artifacts []agentcore.ArtifactRef, ref string) (agentcore.ArtifactRef, bool) {
	ref = strings.TrimSpace(ref)
	if isRedactedLocalPathRef(ref) {
		if len(artifacts) == 1 && !artifacts[0].IsZero() {
			return artifacts[0], true
		}
		return agentcore.ArtifactRef{}, false
	}
	for _, artifact := range artifacts {
		if ref != "" && (strings.TrimSpace(artifact.ID) == ref || strings.TrimSpace(artifact.URI) == ref) {
			return artifact, true
		}
	}
	return agentcore.ArtifactRef{}, false
}

func runtimeToolCallByID(calls []agentcore.RuntimeToolCall, id string) (agentcore.RuntimeToolCall, bool) {
	id = strings.TrimSpace(id)
	for _, call := range calls {
		if id != "" && strings.TrimSpace(call.ID) == id {
			return call, true
		}
	}
	return agentcore.RuntimeToolCall{}, false
}

func lastSuccessfulRuntimeToolCall(calls []agentcore.RuntimeToolCall, bindings []agentcore.RuntimeToolBinding, taskScopes []string) (agentcore.RuntimeToolCall, bool) {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || strings.TrimSpace(call.Output) == "" {
			continue
		}
		binding, bound := runtimeToolBindingForCall(call.Name, bindings)
		if !bound || !runtimeToolBindingAllowedByTaskScopes(binding, taskScopes) {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(strings.TrimSpace(call.Output)), &payload); err != nil || payload == nil || runtimeToolOutputFailed(payload) {
			continue
		}
		return call, true
	}
	return agentcore.RuntimeToolCall{}, false
}

func runtimeToolBindingAllowedByTaskScopes(binding agentcore.RuntimeToolBinding, taskScopes []string) bool {
	found := false
	for _, permission := range binding.GrantedPermissions {
		if strings.TrimSpace(permission) == "" {
			continue
		}
		found = true
		if !permissionscope.AnyAllows(taskScopes, permission) {
			return false
		}
	}
	return found
}

func permissionScopesAllowAll(granted, required []string) bool {
	found := false
	for _, scope := range required {
		if strings.TrimSpace(scope) == "" {
			continue
		}
		found = true
		if !permissionscope.AnyAllows(granted, scope) {
			return false
		}
	}
	return found
}

func runtimeToolCallByEvidenceRef(calls []agentcore.RuntimeToolCall, ref string) (agentcore.RuntimeToolCall, error) {
	ref = strings.TrimSpace(ref)
	if call, ok := runtimeToolCallByID(calls, ref); ok {
		return call, nil
	}
	var matched agentcore.RuntimeToolCall
	for _, call := range calls {
		if !runtimeToolCallOutputHasEvidenceRef(call.Output, ref) {
			continue
		}
		if strings.TrimSpace(matched.ID) != "" && strings.TrimSpace(matched.ID) != strings.TrimSpace(call.ID) {
			return agentcore.RuntimeToolCall{}, fmt.Errorf("tool output reference %q matches multiple tool calls from this run", ref)
		}
		matched = call
	}
	if strings.TrimSpace(matched.ID) == "" {
		return agentcore.RuntimeToolCall{}, fmt.Errorf("tool call %q was not produced by this run", ref)
	}
	return matched, nil
}

func runtimeToolCallOutputHasEvidenceRef(output, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	var payload any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil || runtimeToolOutputFailed(payload) {
		return false
	}
	return runtimeToolOutputReferences(payload, ref)
}

func runtimeToolOutputFailed(payload any) bool {
	object, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	status, _ := object["status"].(string)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "blocked", "cancelled", "canceled", "timeout", "timed_out":
		return true
	}
	for _, key := range []string{"error_code", "error"} {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func runtimeToolOutputReferences(value any, ref string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if runtimeToolOutputReferenceKey(key) && runtimeToolOutputReferenceValueMatches(nested, ref) {
				return true
			}
			if runtimeToolOutputReferences(nested, ref) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if runtimeToolOutputReferences(nested, ref) {
				return true
			}
		}
	}
	return false
}

func runtimeToolOutputReferenceValueMatches(value any, ref string) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ref
	case []any:
		for _, nested := range typed {
			if runtimeToolOutputReferenceValueMatches(nested, ref) {
				return true
			}
		}
	}
	return false
}

func runtimeToolOutputReferenceKey(raw string) bool {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "id", "ids", "ref", "refs", "uri", "uris", "url", "urls":
		return true
	}
	for _, suffix := range []string{"_id", "_ids", "_ref", "_refs", "_uri", "_uris", "_url", "_urls"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func validateExternalEvidenceURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil {
		return fmt.Errorf("external evidence URL must be an absolute http(s) URL without credentials")
	}
	return nil
}
