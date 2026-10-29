package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionLayaObserver = decision.Observer

func newProductionHandlerFromRuntimeWithOptions(ctx context.Context, rt *platform.Runtime, opts productionHandlerOptions) (http.Handler, func(), error) {
	observer, closeObserver, err := newProductionDecisionShadow(ctx, rt)
	if err != nil {
		return nil, func() {}, err
	}
	handler, cleanup, err := newProductionHandlerWithLaya(ctx, rt, opts, observer)
	if err != nil {
		closeObserver()
		return nil, func() {}, err
	}
	return handler, func() { closeObserver(); cleanup() }, nil
}

// Compatibility entry point for existing integrations.
func newProductionLayaShadow(ctx context.Context, rt *platform.Runtime) (*decision.Observer, func(), error) {
	return newProductionDecisionShadow(ctx, rt)
}

func productionRSISampleStore(rt *platform.Runtime) (*resourceadmin.PlatformRSILearningSampleStore, error) {
	if rt == nil || rt.Databases == nil {
		return nil, fmt.Errorf("RSI sample database is required in decision shadow mode")
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return nil, err
	}
	return resourceadmin.NewPlatformRSILearningSampleStore(core), nil
}

// External PageIndex search does not pass through SearchService. Observe the
// authorized MCP response too, retaining its exact output for the caller.
func observeLayaMCPResult(ctx context.Context, observer *decision.Observer, req mcptransport.CallToolRequest, result mcptransport.CallToolResult) {
	if observer == nil || (req.ToolID != "mcp.orrery-public-readonly.search_documents" && req.ToolID != "mcp.orrery-customer-readonly.search_documents") {
		return
	}
	query, _ := req.Arguments["query"].(string)
	if query == "" || req.PrincipalID == "" {
		return
	}
	items, ok := result.Output["results"].([]any)
	if !ok {
		return
	}
	results := make([]knowledgeadmin.KnowledgeSearchResultDTO, 0, decision.MaxCandidates)
	for _, raw := range items {
		if len(results) >= decision.MaxCandidates {
			break
		}
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		results = append(results, knowledgeadmin.KnowledgeSearchResultDTO{
			KnowledgeBaseID: req.ToolID,
			DocumentID:      firstNonBlank(outputString(item, "document_id"), outputString(item, "doc_id"), outputString(item, "source_uri")),
			ChunkID:         firstNonBlank(outputString(item, "chunk_id"), outputString(item, "node_id")),
			Snippet:         firstNonBlank(outputString(item, "snippet"), outputString(item, "content"), outputString(item, "summary")),
			DocumentName:    outputString(item, "original_checksum"),
		})
	}
	if len(results) == 0 {
		return
	}
	scope, _ := json.Marshal([]string{req.TenantID, req.PrincipalID, req.ToolID})
	observer.Submit(ctx, knowledgeadmin.BuildDecisionSearchJob(string(scope), query, results))
}

func (d productionRuntimeToolDispatcher) observeLayaFailure(ctx context.Context, call runtimecore.RuntimeToolCallRequest, result runtimecore.RuntimeToolCallResult) {
	if d.layaShadow == nil || call.TenantID == "" || call.RunID == "" || call.CallID == "" || call.AgentID == "" {
		return
	}
	if outputString(result.Output, "status") != "failed" {
		return
	}
	// Unknown/unbound calls are outside the learning evidence boundary.
	if _, ok := runtimeToolBindingForCall(call.Name, call.ToolBindings); !ok && !isProductionSkillTool(call.Name) {
		return
	}
	scope, _ := json.Marshal([]string{call.TenantID, call.AgentID.String()})
	source, _ := json.Marshal([]string{"runtime_tool", call.RunID.String(), call.CallID})
	sample := resourceadmin.RSILearningSample{Scope: string(scope), EvidenceRef: string(source), Tool: call.Name,
		ErrorCode: outputString(result.Output, "error_code"), Summary: outputString(result.Output, "error")}
	for _, binding := range call.SkillBindings {
		ref, _ := json.Marshal(binding)
		sample.SkillRefs = append(sample.SkillRefs, string(ref))
	}
	if d.rsiSamples != nil {
		// Persist authorized input/output for diagnosis. The classifier job uses
		// bounded excerpts; the LLM diagnosis worker rereads complete evidence.
		evidence, err := json.Marshal(map[string]any{"input": call.Input, "output": result.Output, "skill_bindings": call.SkillBindings})
		if err != nil {
			d.logRSISampleFailure("evidence_encoding", "")
			return
		}
		sample.Evidence = evidence
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		_, err = d.rsiSamples.Save(writeCtx, sample)
		cancel()
		if err != nil {
			d.logRSISampleFailure("sample", resourceadmin.BuildDecisionRSISampleJob(sample).ObservationID)
		}
	}
	d.layaShadow.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(sample))
}

func (d productionRuntimeToolDispatcher) logRSISampleFailure(stage, id string) {
	if d.rsiLogger != nil {
		d.rsiLogger.Warn(context.Background(), "decision.rsi_persistence_failed", logging.String("observation_id", id), logging.String("stage", stage))
	}
}
