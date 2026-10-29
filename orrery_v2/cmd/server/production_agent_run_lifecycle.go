package main

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func productionAgentRunRuntimeResponse(instance agentcore.AgentInstance, runID agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	if instance == nil || instance.Base() == nil || runID == "" {
		return agentcore.RuntimeResponse{}, false
	}
	reader, ok := instance.Base().Runtime().(agentcore.SessionRuntimeRecordReader)
	if !ok {
		return agentcore.RuntimeResponse{}, false
	}
	return reader.RuntimeResponse(runID)
}

func cloneProductionRuntimeUsage(source map[string]agentcore.TokenUsage) map[string]agentcore.TokenUsage {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]agentcore.TokenUsage, len(source))
	for model, usage := range source {
		out[model] = usage
	}
	return out
}

func productionRuntimeLifecycleEvent(run runtimedaemon.AgentRun) runtimedaemon.RuntimeLifecycleEvent {
	pack := run.Request.ContextPack
	responsibilityContext := agentcore.CloneResponsibilityContext(pack.ResponsibilityContext)
	return runtimedaemon.RuntimeLifecycleEvent{
		TenantID:              run.Request.TenantID,
		MissionID:             missionIDFromRun(run),
		TaskID:                taskIDFromRun(run),
		AgentID:               run.AgentID,
		RunID:                 run.RunID,
		AgentTaskID:           productionAgentTaskOntologyID(run),
		ScheduledTaskID:       run.Request.RuntimeOptions.ProviderSpecific["scheduled_task_id"],
		ScheduledRunID:        scheduledRunIDFromRun(run),
		RuntimeKind:           run.RuntimeID,
		QueueID:               agentRunQueueID(run),
		ContextSnapshotID:     productionContextSnapshotOntologyID(pack.ID, run.RunID),
		ContextSummary:        firstNonBlank(responsibilityContextSummary(responsibilityContext), pack.WorkerPersonalGoal, pack.TaskContractSummary, pack.MissionFinalGoal),
		InputRefs:             contextPackSourceIDs(pack.SourceRefs),
		MemoryRefs:            contextPackMemoryRefs(pack),
		ResponsibilityContext: responsibilityContext,
		TraceID:               "trace:" + run.RunID.String(),
	}
}

func responsibilityContextSummary(value *agentcore.ResponsibilityContext) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.Summary)
}

func productionAgentTaskOntologyID(run runtimedaemon.AgentRun) string {
	if id := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["agent_task_id"]); id != "" {
		return id
	}
	if scheduledTaskID := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["scheduled_task_id"]); scheduledTaskID != "" {
		return schedulerapp.AgentTaskIDForScheduledTask(scheduledTaskID)
	}
	taskID := firstNonBlank(run.Task.ID, run.TaskID, run.Request.TaskID)
	if taskID == "" {
		return "agent-task:run:" + run.RunID.String()
	}
	return "agent-task:mission:" + taskID
}

func productionContextSnapshotOntologyID(contextID agentcore.ContextPackID, runID agentcore.AgentRunID) string {
	id := strings.TrimSpace(string(contextID))
	if id == "" {
		id = runID.String()
	}
	return "context-snapshot:" + id
}

func scheduledRunIDFromRun(run runtimedaemon.AgentRun) string {
	if !isSchedulerAgentRun(run) {
		return ""
	}
	return firstNonBlank(run.Request.RuntimeOptions.ProviderSpecific["scheduled_run_id"], run.RunID.String())
}

func contextPackSourceIDs(refs []agentcore.ContextSourceRef) []string {
	out := make([]string, 0, len(refs))
	for _, source := range refs {
		id := contextSourceID(source)
		if id != "" {
			out = append(out, string(source.Kind)+":"+id)
		}
	}
	return out
}

func contextSourceID(source agentcore.ContextSourceRef) string {
	switch source.Kind {
	case agentcore.ContextSourceKindUserInstruction:
		if source.Ref.UserInstructionRef != nil {
			return string(source.Ref.UserInstructionRef.ID)
		}
	case agentcore.ContextSourceKindTaskContract:
		if source.Ref.TaskContractRef != nil {
			return source.Ref.TaskContractRef.ID
		}
	case agentcore.ContextSourceKindAcceptance:
		if source.Ref.AcceptanceRef != nil {
			return source.Ref.AcceptanceRef.ID
		}
	case agentcore.ContextSourceKindMissionEvent:
		if source.Ref.MissionEventRef != nil {
			return string(source.Ref.MissionEventRef.ID)
		}
	case agentcore.ContextSourceKindMemoryDigest:
		if source.Ref.MemoryDigestRef != nil {
			return string(source.Ref.MemoryDigestRef.ID)
		}
	case agentcore.ContextSourceKindDiscussionDigest:
		if source.Ref.DiscussionDigestRef != nil {
			return string(source.Ref.DiscussionDigestRef.ID)
		}
	case agentcore.ContextSourceKindDecision:
		if source.Ref.DecisionRef != nil {
			return source.Ref.DecisionRef.ID
		}
	case agentcore.ContextSourceKindBlocker:
		if source.Ref.BlockerRef != nil {
			return source.Ref.BlockerRef.ID
		}
	}
	return ""
}

func contextPackMemoryRefs(pack agentcore.ContextPack) []runtimedaemon.RuntimeMemoryRef {
	out := make([]runtimedaemon.RuntimeMemoryRef, 0, 1+len(pack.CollectiveMemoryDigestRefs))
	if !pack.PersonalMemoryDigestRef.IsZero() {
		out = append(out, runtimeMemoryRef(pack.PersonalMemoryDigestRef))
	}
	for _, ref := range pack.CollectiveMemoryDigestRefs {
		if !ref.IsZero() {
			out = append(out, runtimeMemoryRef(ref))
		}
	}
	return out
}

func runtimeMemoryRef(ref agentcore.MemoryDigestRef) runtimedaemon.RuntimeMemoryRef {
	return runtimedaemon.RuntimeMemoryRef{
		ID: string(ref.ID), MemoryKind: string(ref.MemoryKind), SummaryRef: ref.SummaryRef, Sensitivity: ref.Sensitivity,
	}
}

func runtimeLifecycleKnowledgeRefs(response agentcore.RuntimeResponse) []runtimedaemon.RuntimeKnowledgeRef {
	refs := make([]runtimedaemon.RuntimeKnowledgeRef, 0)
	seen := map[string]bool{}
	for _, call := range response.ToolCalls {
		if strings.TrimSpace(call.Name) != string(toolstd.KnowledgeSearch) || strings.TrimSpace(call.Output) == "" {
			continue
		}
		var output struct {
			Results []struct {
				DocumentID string `json:"document_id"`
				ChunkID    string `json:"chunk_id"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(call.Output), &output); err != nil {
			continue
		}
		for _, result := range output.Results {
			for _, ref := range []runtimedaemon.RuntimeKnowledgeRef{
				{Kind: "knowledge_document", ID: strings.TrimSpace(result.DocumentID)},
				{Kind: "knowledge_chunk", ID: strings.TrimSpace(result.ChunkID)},
			} {
				key := ref.Kind + ":" + ref.ID
				if ref.ID == "" || seen[key] {
					continue
				}
				seen[key] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func runtimeLifecycleArtifacts(result agentcore.AgentResult, response agentcore.RuntimeResponse) []agentcore.ArtifactRef {
	out := append([]agentcore.ArtifactRef(nil), response.Artifacts...)
	seen := make(map[string]bool, len(out))
	for _, artifact := range out {
		seen[artifact.ID] = true
	}
	for _, deliverable := range result.Deliverables {
		artifact := deliverable.ArtifactRef
		if artifact.IsZero() || seen[artifact.ID] {
			continue
		}
		seen[artifact.ID] = true
		out = append(out, artifact)
	}
	return out
}

func runtimeLifecycleEvidence(result agentcore.AgentResult) []runtimedaemon.RuntimeEvidence {
	out := make([]runtimedaemon.RuntimeEvidence, 0, len(result.Evidence))
	for _, evidence := range result.Evidence {
		if evidence.ID == "" {
			continue
		}
		out = append(out, runtimedaemon.RuntimeEvidence{
			ID: string(evidence.ID), Kind: string(evidence.Kind), Summary: evidence.EvidenceRequirementID,
			SourceRef: runtimeEvidenceSourceRef(evidence), ContentRef: runtimeEvidenceContentRef(evidence), Status: "produced",
		})
	}
	return out
}

func runtimeEvidenceSourceRef(evidence agentcore.Evidence) string {
	return firstNonBlank(string(evidence.ToolLogRef.ID), string(evidence.ApprovalRecordRef.ID), string(evidence.HumanSubmissionRef.ID), evidence.ExternalURL, evidence.ArtifactRef.ID)
}

func runtimeEvidenceContentRef(evidence agentcore.Evidence) string {
	return firstNonBlank(evidence.ArtifactRef.URI, evidence.ExternalURL, evidence.ArtifactRef.ID)
}
