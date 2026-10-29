package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/platform/db"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type documentInferenceWorkflow interface {
	DocumentInference(context.Context, identity.Principal, knowledgeinstances.WatchRequest) (map[string]any, error)
}

func (a *productionOntologyInferenceAPI) DocumentInference(ctx context.Context, p identity.Principal, req knowledgeinstances.WatchRequest) (map[string]any, error) {
	if a == nil || a.documents == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	p = identity.NormalizePrincipal(p)
	if p.ViewerKind != identity.ViewerKindAgent || p.AgentProfileID == "" || !permissionscope.AnyAllows(p.CredentialScopes, permissionscope.OntologyManage) {
		return nil, ontologyscope.ErrAccessDenied
	}
	if _, err := a.documentInferencePrincipal(ctx, req.TenantID, p.AgentProfileID); err != nil {
		return nil, err
	}
	authorized, err := a.gate.AuthorizeManage(ctx, p, req.TenantID)
	if err != nil {
		return nil, err
	}
	tenant := authorized.Binding().TenantID
	w := a.documents
	if req.Operation == "list" {
		sets, err := w.watches.List(ctx, tenant, p.AgentProfileID)
		return map[string]any{"watches": sets}, err
	}
	watch := knowledgeinstances.Watch{TenantID: tenant, ID: req.ID, AgentID: p.AgentProfileID, RootID: req.RootID, Active: true, PublishedAt: time.Now().UTC()}
	if req.Operation == "pause" || req.Operation == "status" || req.Operation == "retry" {
		watch, err = w.watches.Get(ctx, tenant, req.ID)
		if err != nil {
			return nil, err
		}
		if watch.AgentID != p.AgentProfileID {
			return nil, ontologyscope.ErrAccessDenied
		}
		if req.Operation == "status" {
			current, err := w.current(ctx, watch)
			if err != nil {
				return nil, err
			}
			batches, err := w.store.ListBatches(ctx, tenant, watch.ID)
			// Status never returns source text or historical candidate properties.
			// Old authorization snapshots expose only task metadata.
			var summaries []map[string]any
			for _, b := range batches {
				item := map[string]any{"id": b.ID, "status": b.Status, "created_at": b.Plan.CreatedAt}
				if b.Plan.AuthorizationHash == current.authorizationHash {
					item["source_revision"] = b.Plan.Source.Revision
					item["source_hash"] = b.Plan.Source.ContentHash
					item["plan_id"] = b.Plan.ID
					item["plan_hash"] = knowledgeinstances.Digest(b.Plan)
					if _, visibilityErr := w.conflictSources(ctx, current, b.Plan, b.Plan.Conflicts, true); visibilityErr == nil {
						item["decision"] = b.Decision
					} else if b.Decision != nil {
						item["decision"] = map[string]any{"decision": b.Decision.Decision, "model_id": b.Decision.ModelID, "plan_hash": b.Decision.PlanHash}
					}
					item["receipt"] = b.Receipt
				}
				summaries = append(summaries, item)
			}
			return map[string]any{"watch": watch, "batches": summaries}, err
		}
		if req.Operation == "retry" {
			if !watch.Active || watch.Version != req.ExpectedVersion {
				return nil, knowledgeinstances.ErrConflict
			}
			if _, err := w.current(ctx, watch); err != nil {
				return nil, err
			}
			stamp, err := a.lifecycle.InputStamp(ctx, tenant)
			if err != nil {
				return nil, err
			}
			err = w.store.Core.WithTx(ctx, func(tx db.Tx) error {
				if err := a.inferenceCommitGuard(authorized, stamp)(ctx, tx); err != nil {
					return err
				}
				if err := w.watches.CheckActiveTx(ctx, tx, tenant, watch.ID, p.AgentProfileID, watch.Version); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `UPDATE ontology_document_watches SET observed_stamp='',next_check_at=? WHERE tenant_id=? AND id=?`, time.Now().UTC().UnixNano(), tenant, watch.ID)
				return err
			})
			return map[string]any{"watch": watch, "retry_scheduled": err == nil}, err
		}
		watch.Active = false
		watch.PublishedAt = time.Now().UTC()
	} else if req.Operation != "publish" {
		return nil, knowledgeinstances.ErrInvalid
	}
	stamp, err := a.lifecycle.InputStamp(ctx, tenant)
	if err != nil {
		return nil, err
	}
	guard := a.inferenceCommitGuard(authorized, stamp)
	if watch.Active {
		current, err := w.current(ctx, watch)
		if err != nil {
			return nil, err
		}
		watch.InitialStamp = inferenceDigest([]string{current.source.Stamp(), current.schemaHash, current.authorizationHash})
		base := guard
		guard = func(ctx context.Context, tx db.Tx) error {
			if err := w.fenceProfile(ctx, tx, watch.AgentID, current.profileHash); err != nil {
				return err
			}
			if err := base(ctx, tx); err != nil {
				return err
			}
			return w.sources.Fence(ctx, tx, current.source)
		}
	}
	watch, err = w.watches.Publish(ctx, watch, req.ExpectedVersion, guard)
	return map[string]any{"watch": watch}, err
}

func (e productionOntologyStewardToolExecutor) executeDocumentInference(ctx context.Context, p identity.Principal, req mcptransport.OntologyToolRequest) (mcptransport.OntologyToolResult, error) {
	workflow, ok := e.inference.(documentInferenceWorkflow)
	if !ok || len(req.InferenceJSON) > 16*1024 {
		return mcptransport.OntologyToolResult{}, ontologyscope.ErrDependencyUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(req.InferenceJSON))
	decoder.DisallowUnknownFields()
	var input knowledgeinstances.WatchRequest
	if err := decoder.Decode(&input); err != nil {
		return mcptransport.OntologyToolResult{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return mcptransport.OntologyToolResult{}, knowledgeinstances.ErrInvalid
	}
	input.TenantID = req.TenantID
	result, err := workflow.DocumentInference(ctx, p, input)
	if err != nil {
		return mcptransport.OntologyToolResult{}, err
	}
	value, err := ontologyToolJSONValue(result)
	return mcptransport.OntologyToolResult{Output: map[string]any{"document_inference": value}}, err
}
