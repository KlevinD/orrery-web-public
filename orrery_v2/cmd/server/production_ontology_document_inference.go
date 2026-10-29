package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/knowledge/provider"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

type documentExtractionInput struct {
	Phase              string                             `json:"phase,omitempty"`
	Mentions           []knowledgeinstances.EntityInput   `json:"mentions,omitempty"`
	ExtractedRelations []knowledgeinstances.RelationInput `json:"extracted_relations,omitempty"`
	Recall             *knowledgeinstances.RecallSummary  `json:"candidate_recall,omitempty"`
	RelationCodes      []string                           `json:"relation_codes"`
	Source             knowledgeinstances.Source          `json:"source"`
	Candidates         []knowledgeinstances.Entity        `json:"identity_candidates"`
	Previous           []knowledgeinstances.Claim         `json:"previous_document_claims"`
	Attributes         map[string]string                  `json:"attribute_types"`
}

type documentInferenceModel interface {
	Extract(context.Context, documentExtractionInput) (knowledgeinstances.Extraction, error)
	Review(context.Context, knowledgeinstances.Plan) (knowledgeinstances.Decision, error)
	ModelID() string
}

type productionOntologyDocumentInference struct {
	api     *productionOntologyInferenceAPI
	store   *knowledgeinstances.Store
	watches *knowledgeinstances.WatchStore
	sources ontologyDocumentSource
	model   documentInferenceModel
}

func registerProductionDocumentInference(rt *platform.Runtime, core *db.Store, api *productionOntologyInferenceAPI, repo knowledgestore.Repository, providers provider.Registry, settings productionSettingsStore, secrets platformSecretResolver) error {
	if rt == nil || core == nil || api == nil || repo == nil || providers == nil {
		return fmt.Errorf("document inference dependencies are required")
	}
	worker := &productionOntologyDocumentInference{api: api, store: &knowledgeinstances.Store{Core: core}, watches: &knowledgeinstances.WatchStore{Store: core}, model: newProductionDocumentInferenceModel(settings, secrets)}
	worker.sources = &productionOntologyDocumentSource{api: api, core: core, repo: repo, providers: providers}
	api.documents = worker
	if err := rt.RegisterOutboxHandler(knowledgeinstances.DocumentTopic, worker.Process); err != nil {
		return err
	}
	if err := rt.RegisterOutboxHandler(knowledgeinstances.BoundaryTopic, worker.ProcessBoundary); err != nil {
		return err
	}
	return rt.RegisterSweeper("ontology.document.source_changes", newProductionCadencedSweeper(platform.SweeperFunc(worker.Observe), rt.Clock.Now, 30*time.Second))
}

func (a *productionOntologyInferenceAPI) documentInferencePrincipal(ctx context.Context, tenant, agent string) (identity.Principal, error) {
	p, err := a.continuousInferencePrincipal(ctx, tenant, agent)
	if err != nil {
		return p, err
	}
	profile, err := a.profiles.GetProfile(ctx, agentcore.AgentProfileID(agent))
	if err != nil {
		return p, err
	}
	if _, err := documentProfileHash(profile, agent); err != nil {
		return p, err
	}
	return p, nil
}

type documentInputContext struct {
	p                 identity.Principal
	authorized        ontologyscope.AuthorizedScopeContext
	source            knowledgeinstances.Source
	schemaHash        string
	authorizationHash string
	profileHash       string
}

func (w *productionOntologyDocumentInference) current(ctx context.Context, watch knowledgeinstances.Watch) (documentInputContext, error) {
	var current documentInputContext
	p, err := w.api.documentInferencePrincipal(ctx, watch.TenantID, watch.AgentID)
	if err != nil {
		return current, err
	}
	profile, err := w.api.profiles.GetProfile(ctx, agentcore.AgentProfileID(watch.AgentID))
	if err != nil {
		return current, err
	}
	profileHash, err := documentProfileHash(profile, watch.AgentID)
	if err != nil {
		return current, err
	}
	authorized, err := w.api.gate.AuthorizeManage(ctx, p, watch.TenantID)
	if err != nil {
		return current, err
	}
	if authorized.Binding().TenantID != watch.TenantID {
		return current, ontologyscope.ErrAccessDenied
	}
	source, err := w.sources.Load(ctx, p, watch)
	if err != nil {
		return current, err
	}
	schema, err := w.documentSchema(ctx, watch.TenantID)
	if err != nil {
		return current, err
	}
	binding, snapshot := authorized.Binding(), authorized.Snapshot()
	current = documentInputContext{p: p, authorized: authorized, source: source, schemaHash: schema, profileHash: profileHash, authorizationHash: inferenceDigest([]any{snapshot.PrincipalKey, snapshot.AuthorizationRevision, snapshot.PolicyRevision, binding.BindingVersion, binding.PersistedBindingVersion, source.Access, profileHash})}
	return current, nil
}

func (w *productionOntologyDocumentInference) documentSchema(ctx context.Context, tenant string) (string, error) {
	pack, err := w.api.schemaPack(ctx, tenant)
	if err != nil {
		return "", err
	}
	var definitions []any
	for _, object := range pack.AllowedObjectTypes {
		if object.ID == knowledgeinstances.EntityType {
			if object.OwnerDomain != knowledgeinstances.Domain || object.Lifecycle != "active" || object.TemporalMode != ontology.TemporalModeStatic || !slices.Contains(object.AllowedActions, knowledgeinstances.ActionID) {
				return "", knowledgeinstances.ErrInvalid
			}
			identityField := false
			for _, p := range object.Properties {
				if p.Required || p.TemporalMode != documentPropertyTemporalMode(p.PropertyID) {
					return "", knowledgeinstances.ErrInvalid
				}
				if p.PropertyID == "identity_keys" {
					if p.ValueType != ontology.ValueTypeArray {
						return "", knowledgeinstances.ErrInvalid
					}
					identityField = true
				}
			}
			if !identityField {
				return "", knowledgeinstances.ErrInvalid
			}
			definitions = append(definitions, object)
		}
	}
	if len(definitions) != 1 {
		return "", knowledgeinstances.ErrInvalid
	}
	for _, id := range []string{knowledgeinstances.DocumentLink, knowledgeinstances.RelatedLink} {
		found := false
		for _, link := range pack.AllowedLinkTypes {
			if link.ID == id {
				from := knowledgeinstances.EntityType
				if id == knowledgeinstances.DocumentLink {
					from = "knowledge_document"
				}
				if link.FromObjectType != from || link.ToObjectType != knowledgeinstances.EntityType || link.TemporalMode != ontology.TemporalModeStateInterval || link.Cardinality != ontology.CardinalityManyToMany || !link.EvidenceRequired || !link.SuggestedAllowed || !slices.Contains(link.AllowedActions, knowledgeinstances.ActionID) {
					return "", knowledgeinstances.ErrInvalid
				}
				definitions = append(definitions, link)
				found = true
			}
		}
		if !found {
			return "", knowledgeinstances.ErrInvalid
		}
	}
	expected := map[string]string{"conflicting_properties": "array"}
	for id, t := range knowledgeinstances.AttributeTypes {
		expected[id] = t
	}
	for id, valueType := range expected {
		found := false
		for _, prop := range pack.AllowedPropertyTypes {
			if prop.ObjectTypeID == knowledgeinstances.EntityType && prop.PropertyID == id {
				if string(prop.ValueType) != valueType || prop.TemporalMode != documentPropertyTemporalMode(prop.PropertyID) || prop.ConflictPolicy != ontology.ConflictPolicyAuthorityWins || prop.AuthoritySource != knowledgeinstances.Domain || !slices.Contains(prop.AllowedActions, knowledgeinstances.ActionID) {
					return "", knowledgeinstances.ErrInvalid
				}
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("%w: missing owned property %s", knowledgeinstances.ErrInvalid, id)
		}
	}
	// A semantic protocol change invalidates pending plans and re-observes
	// existing watches, so old free-text-only relations are reviewed again.
	definitions = append(definitions, "document-conflict-adjudication-v3", ontology.KnowledgeRelationCodes())
	definitions = append(definitions, "document-source-lifecycle-v4")
	definitions = append(definitions, "document-entity-identity-v5")
	definitions = append(definitions, knowledgeinstances.TemporalProtocol)
	// Registry snapshots sort their definitions; a map hash is deterministic too.
	for _, prop := range pack.AllowedPropertyTypes {
		if prop.ObjectTypeID == knowledgeinstances.EntityType {
			definitions = append(definitions, prop)
		}
	}
	return inferenceDigest(definitions), nil
}

func (w *productionOntologyDocumentInference) Observe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := w.store.ObserveBoundaries(ctx, time.Now().UTC(), 20); err != nil {
		return err
	}
	return w.watches.Observe(ctx, time.Now().UTC(), 20, func(ctx context.Context, watch knowledgeinstances.Watch) (string, error) {
		current, err := w.current(ctx, watch)
		if err != nil {
			return "", err
		}
		return inferenceDigest([]string{current.source.Stamp(), current.schemaHash, current.authorizationHash}), nil
	})
}

func (w *productionOntologyDocumentInference) Process(ctx context.Context, job outbox.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var input knowledgeinstances.DocumentJob
	if err := json.Unmarshal(job.Payload, &input); err != nil {
		return err
	}
	if input.TenantID == "" || input.WatchID == "" || input.Version < 1 || job.ID == "" {
		return knowledgeinstances.ErrInvalid
	}
	watch, err := w.watches.Get(ctx, input.TenantID, input.WatchID)
	if errors.Is(err, knowledgeinstances.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !watch.Active || watch.Version != input.Version {
		return nil
	}
	current, err := w.current(ctx, watch)
	if err != nil {
		return err
	}
	release, err := w.api.concurrency.Acquire(ctx, ontologycontract.ConcurrencyInference, current.authorized.Snapshot().PrincipalKey)
	if err != nil {
		return err
	}
	defer release()
	batch, err := w.store.GetBatch(ctx, watch.TenantID, job.ID)
	if errors.Is(err, knowledgeinstances.ErrNotFound) {
		previous, err := w.store.Claims(ctx, watch.TenantID, watch.ID)
		if err != nil {
			return err
		}
		extraction, candidates, recall, err := w.extractWithIdentityRecall(ctx, current, previous)
		if err != nil {
			return err
		}
		deferred := extraction.Defer
		reason := extraction.Reason
		if deferred {
			extraction = knowledgeinstances.Extraction{Reason: reason}
		}
		plan, err := knowledgeinstances.Prepare(job.ID, watch, current.source, current.schemaHash, current.authorizationHash, extraction, candidates, previous, time.Now().UTC())
		if err != nil {
			return err
		}
		plan.ExtractorModelID = w.model.ModelID()
		plan.CandidateRecall = &recall
		if !deferred {
			plan, err = w.prepareConflictReview(ctx, current, plan)
			if err != nil {
				return err
			}
		} else {
			plan = knowledgeinstances.SealPlan(plan)
		}
		batch, err = w.store.SavePrepared(ctx, knowledgeinstances.Batch{ID: job.ID, TenantID: watch.TenantID, WatchID: watch.ID, Plan: plan})
		if err != nil {
			return err
		}
		if deferred && batch.Status == "prepared" {
			_, err = w.store.SaveDecision(ctx, batch, knowledgeinstances.Decision{Decision: "defer", Reason: reason, ModelID: w.model.ModelID(), PlanHash: knowledgeinstances.Digest(batch.Plan)})
			return err
		}
	} else if err != nil {
		return err
	}
	if batch.Status != "prepared" && batch.Status != "reviewed" {
		return nil
	}
	current, err = w.current(ctx, watch)
	if err != nil {
		return err
	}
	if !matchesDocumentContext(batch.Plan, current) {
		return w.store.Supersede(ctx, batch)
	}
	if batch.Decision == nil {
		if _, err := w.conflictSources(ctx, current, batch.Plan, batch.Plan.Conflicts, false); err != nil {
			if conflictReviewUnavailable(err) {
				return w.store.Supersede(ctx, batch)
			}
			return err
		}
		decision, err := w.model.Review(ctx, batch.Plan)
		if err != nil {
			return err
		}
		decision.PlanHash = knowledgeinstances.Digest(batch.Plan)
		decision.ModelID = w.model.ModelID()
		batch, err = w.store.SaveDecision(ctx, batch, decision)
		if err != nil {
			return err
		}
	}
	if batch.Status != "reviewed" || batch.Decision.Decision != "approve" {
		return nil
	}
	// Re-resolve the current runtime profile, permissions, source and schema
	// after model latency. No queued credential snapshot can authorize writes.
	// Capture the input fence before reading schema, so a concurrent schema
	// change cannot be accepted as the baseline for a stale schema snapshot.
	stamp, err := w.api.lifecycle.InputStamp(ctx, watch.TenantID)
	if err != nil {
		return err
	}
	current, err = w.current(ctx, watch)
	if err != nil {
		return err
	}
	if !matchesDocumentContext(batch.Plan, current) {
		return w.store.Supersede(ctx, batch)
	}
	conflictSources, err := w.conflictSources(ctx, current, batch.Plan, batch.Plan.Conflicts, false)
	if err != nil {
		if conflictReviewUnavailable(err) {
			return w.store.Supersede(ctx, batch)
		}
		return err
	}
	guard := func(ctx context.Context, tx db.Tx) error {
		if err := w.fenceProfile(ctx, tx, watch.AgentID, current.profileHash); err != nil {
			return err
		}
		if err := w.api.inferenceCommitGuard(current.authorized, stamp)(ctx, tx); err != nil {
			return err
		}
		if err := w.sources.Fence(ctx, tx, batch.Plan.Source); err != nil {
			return err
		}
		for _, source := range conflictSources {
			if err := w.sources.Fence(ctx, tx, source); err != nil {
				return err
			}
		}
		return nil
	}
	_, err = w.store.Apply(ctx, batch, knowledgeinstances.CommitContext{AgentID: watch.AgentID, Now: time.Now().UTC(), Guard: guard})
	if errors.Is(err, knowledgeinstances.ErrConflict) || errors.Is(err, ontologyreasoning.ErrStateConflict) {
		return w.store.Supersede(ctx, batch)
	}
	return err
}

func matchesDocumentContext(p knowledgeinstances.Plan, c documentInputContext) bool {
	return p.Source.Stamp() == c.source.Stamp() && p.SchemaHash == c.schemaHash && p.AuthorizationHash == c.authorizationHash
}

func documentPropertyTemporalMode(predicate string) ontology.TemporalMode {
	if predicate == "identity_keys" || predicate == "conflicting_properties" {
		return ontology.TemporalModeStatic
	}
	return ontology.TemporalModeStateInterval
}
