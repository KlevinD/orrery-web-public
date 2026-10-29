package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/platform/db"
)

type inferenceContextReader interface {
	Neighborhood(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error)
	GetObject(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	ListFacts(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.FactItem], error)
	ListEvidence(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.EvidenceItem], error)
}

type productionOntologyInferenceAPI struct {
	documents   *productionOntologyDocumentInference
	gate        *ontologyscope.ScopeAccessGate
	context     inferenceContextReader
	registry    ontology.Registry
	service     *ontologyreasoning.Service
	policy      contextgraph.PolicyProvider
	concurrency *ontologycontract.ConcurrencyLimiter
	lifecycle   *ontologyreasoning.LifecycleStore
	ruleSets    *ontologyreasoning.RuleSetStore
	profiles    interface {
		GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	}
}

func (a *productionOntologyInferenceAPI) Evaluate(ctx context.Context, principal identity.Principal, req ontologyreasoning.EvaluateRequest) (ontologyreasoning.ReasoningRun, error) {
	if principal.CredentialScopeRestricted && !permissionscope.AnyAllows(principal.CredentialScopes, permissionscope.OntologyManage) {
		return ontologyreasoning.ReasoningRun{}, ontologyscope.ErrAccessDenied
	}
	if a == nil || a.gate == nil || a.context == nil || a.service == nil || a.registry == nil || a.policy == nil || a.concurrency == nil {
		return ontologyreasoning.ReasoningRun{}, ontologyscope.ErrDependencyUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	authorized, err := a.gate.AuthorizeManage(ctx, principal, req.TenantID)
	if err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	release, err := a.concurrency.Acquire(ctx, ontologycontract.ConcurrencyInference, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	defer release()
	if err := ontologyreasoning.ValidateRules(req.Rules); err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	tenant := authorized.Binding().TenantID
	if req.RuleSetID != "" {
		if a.ruleSets == nil {
			return ontologyreasoning.ReasoningRun{}, ontologyscope.ErrDependencyUnavailable
		}
		set, err := a.ruleSets.Get(ctx, tenant, req.RuleSetID)
		if err != nil {
			return ontologyreasoning.ReasoningRun{}, err
		}
		if !set.Active || set.AgentID != principal.AgentProfileID || (req.RuleSetVersion != 0 && set.Version != req.RuleSetVersion) || (req.RootID != "" && req.RootID != set.RootID) {
			return ontologyreasoning.ReasoningRun{}, ontologyreasoning.ErrStateConflict
		}
		req.RootID, req.Rules, req.RuleSetVersion = set.RootID, set.Rules, set.Version
	}
	now := time.Now().UTC()
	historical := req.BusinessAsOf != nil || req.KnownAt != nil
	business, known := now, now
	if historical {
		if req.BusinessAsOf == nil || req.KnownAt == nil || req.BusinessAsOf.IsZero() || req.KnownAt.IsZero() || req.KnownAt.After(now) || req.PreviousRunID != "" || len(req.Documents) != 0 {
			return ontologyreasoning.ReasoningRun{}, ontologyreasoning.ErrInvalidRequest
		}
		business, known = req.BusinessAsOf.UTC(), req.KnownAt.UTC()
		if !contextgraph.TemporalCoordinateValid(business) || !contextgraph.TemporalCoordinateValid(known) {
			return ontologyreasoning.ReasoningRun{}, ontologyreasoning.ErrInvalidRequest
		}
	}
	actorHash := authorization.PrincipalCacheKey(identity.NormalizePrincipal(principal))
	queryHash := inferenceDigest([]string{tenant, req.RootID, "depth=2;nodes=25;edges=100"})
	if req.RuleSetID != "" {
		queryHash = inferenceDigest([]string{queryHash, req.RuleSetID})
	}
	if historical {
		queryHash = inferenceDigest([]string{queryHash, "historical", business.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano)})
	}
	var sessionRevision int64
	var inputStamp string
	if a.lifecycle != nil && !historical {
		session, err := a.lifecycle.Get(ctx, ontologyreasoning.SessionKey{Tenant: tenant, Actor: actorHash, Query: queryHash})
		if err != nil {
			return ontologyreasoning.ReasoningRun{}, err
		}
		sessionRevision = session.Revision
		inputStamp, err = a.lifecycle.InputStamp(ctx, tenant)
		if err != nil {
			return ontologyreasoning.ReasoningRun{}, err
		}
	}
	pack, err := a.schemaPack(ctx, tenant)
	if err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	var historyLimitations []string
	if historical {
		reader, ok := a.context.(interface {
			InferenceSnapshot(context.Context, identity.Principal, contextview.InferenceSnapshotQuery) (contextview.InferenceSnapshot, error)
		})
		if !ok {
			return ontologyreasoning.ReasoningRun{}, ontologyscope.ErrDependencyUnavailable
		}
		snapshot, err := reader.InferenceSnapshot(ctx, principal, contextview.InferenceSnapshotQuery{TenantID: tenant, RootID: req.RootID, BusinessAsOf: business, KnownAt: known})
		if err != nil {
			return ontologyreasoning.ReasoningRun{}, err
		}
		pack.CandidateObjects, pack.NeighborLinks, pack.Facts, pack.Evidence = snapshot.Objects, snapshot.Links, snapshot.Facts, snapshot.Evidence
		for _, e := range snapshot.Evidence {
			pack.EvidenceRefs = append(pack.EvidenceRefs, contextgraph.Ref{Kind: "evidence", ID: e.ID})
		}
		historyLimitations = append(snapshot.Limitations, "current_rules_and_permissions")
	} else {
		neighborhood, err := a.context.Neighborhood(ctx, principal, contextview.NeighborhoodQuery{TenantID: tenant, RootID: req.RootID, Depth: 2, MaxNodes: 25, MaxEdges: 100})
		if err != nil {
			return ontologyreasoning.ReasoningRun{}, err
		}
		if neighborhood.Truncated {
			return ontologyreasoning.ReasoningRun{}, ontologycontract.ErrQueryBudgetExceeded
		}
		propertyKeys := map[string]bool{}
		for _, p := range pack.AllowedPropertyTypes {
			propertyKeys[p.ObjectTypeID+"."+p.PropertyID] = true
		}
		for _, o := range pack.AllowedObjectTypes {
			for _, p := range o.Properties {
				propertyKeys[o.ID+"."+p.PropertyID] = true
			}
		}
		for _, node := range neighborhood.Nodes {
			detail, err := a.context.GetObject(ctx, principal, tenant, node.ID)
			if err != nil {
				return ontologyreasoning.ReasoningRun{}, err
			}
			facts, err := a.context.ListFacts(ctx, principal, contextview.ChildQuery{TenantID: tenant, ObjectID: node.ID, Limit: 100})
			if err != nil {
				return ontologyreasoning.ReasoningRun{}, err
			}
			evidence, err := a.context.ListEvidence(ctx, principal, contextview.ChildQuery{TenantID: tenant, ObjectID: node.ID, Limit: 100})
			if err != nil {
				return ontologyreasoning.ReasoningRun{}, err
			}
			if facts.Page.HasMore || evidence.Page.HasMore || len(pack.Facts)+len(facts.Items) > 1000 {
				return ontologyreasoning.ReasoningRun{}, ontologycontract.ErrQueryBudgetExceeded
			}
			// These references are visible supporting material for the object. Exact
			// inference dependencies remain the assertion IDs in the closure ledger.
			var refs []contextgraph.Ref
			for _, e := range evidence.Items {
				if strings.HasPrefix(e.SourceKind, "ontology_inference") {
					continue
				}
				ref := contextgraph.Ref{Kind: "evidence", ID: e.ID}
				refs = append(refs, ref)
				pack.EvidenceRefs = append(pack.EvidenceRefs, ref)
				item := contextgraph.Evidence{ID: e.ID, TenantID: tenant, SourceRef: contextgraph.Ref{Kind: e.SourceKind, ID: e.SourceID}, Summary: e.Summary, Confidence: e.Confidence}
				if e.ObservedAt != nil {
					item.ObservedAt = *e.ObservedAt
				}
				pack.Evidence = append(pack.Evidence, item)
			}
			if detail.Redacted {
				return ontologyreasoning.ReasoningRun{}, contextview.ErrUnavailable
			}
			object := contextgraph.Object{TenantID: tenant, ID: detail.ID, ObjectType: detail.ObjectType, CanonicalKey: detail.CanonicalKey, Label: detail.Label, Properties: detail.Properties, Status: detail.Status, EvidenceRefs: refs}
			if detail.Temporal != nil {
				object.Temporal = *detail.Temporal
			}
			if detail.UpdatedAt != nil {
				object.UpdatedAt = *detail.UpdatedAt
			}
			pack.CandidateObjects = append(pack.CandidateObjects, object)
			for _, f := range facts.Items {
				if f.Redacted || f.Predicate == "" || !propertyKeys[object.ObjectType+"."+f.Predicate] || identityMetadataProperty(object.ObjectType, f.Predicate) {
					continue
				}
				fact := contextgraph.Fact{TenantID: tenant, ID: f.ID, Subject: contextgraph.Ref{Kind: object.ObjectType, ID: object.ID}, Predicate: f.Predicate, Value: f.Value, Status: f.Status, Confidence: f.Confidence}
				fact.EvidenceRefs = f.EvidenceRefs
				if f.Temporal != nil {
					fact.Temporal = *f.Temporal
				}
				if f.UpdatedAt != nil {
					fact.UpdatedAt = *f.UpdatedAt
				}
				pack.Facts = append(pack.Facts, fact)
			}
			for key, value := range detail.Properties {
				if object.ObjectType != "knowledge_entity" && propertyKeys[object.ObjectType+"."+key] && !identityMetadataProperty(object.ObjectType, key) {
					pack.Facts = append(pack.Facts, contextgraph.Fact{TenantID: tenant, ID: inferenceDigest([]string{"property", object.ID, key}), Subject: contextgraph.Ref{Kind: object.ObjectType, ID: object.ID}, Predicate: key, Value: value})
				}
			}
		}
		for _, edge := range neighborhood.Edges {
			// Owned assertions remain visible in the workbench, but cannot become
			// independent premises for their own subsequent inference approval.
			if edge.Redacted || strings.HasPrefix(edge.ID, "inference-link:") {
				continue
			}
			link := contextgraph.Link{TenantID: tenant, ID: edge.ID, LinkType: edge.LinkType, FromObjectID: edge.SourceID, ToObjectID: edge.TargetID, Status: edge.Status, Confidence: edge.Confidence}
			link.EvidenceRefs = edge.EvidenceRefs
			if edge.Temporal != nil {
				link.Temporal = *edge.Temporal
			}
			if edge.RelationCode != "" {
				link.Properties = map[string]any{"relation_code": edge.RelationCode}
			}
			if edge.UpdatedAt != nil {
				link.UpdatedAt = *edge.UpdatedAt
			}
			pack.NeighborLinks = append(pack.NeighborLinks, link)
		}
	}
	if err := a.filterInferencePolicy(ctx, principal, &pack, req.RootID); err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	if err := a.checkFence(ctx, principal, authorized); err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	record := ontologyreasoning.EvaluationRecord{Historical: historical, HistoryLimitations: historyLimitations, RootID: req.RootID, RuleSetID: req.RuleSetID, RuleSetVersion: req.RuleSetVersion, InputStamp: inputStamp, ActorHash: actorHash, QueryHash: queryHash, BusinessAsOf: business, KnownAt: known, Rules: req.Rules, PreviousRunID: req.PreviousRunID}
	run, err := a.service.Evaluate(ctx, pack, record, req.Documents)
	if err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	if err := a.checkFence(ctx, principal, authorized); err != nil {
		return ontologyreasoning.ReasoningRun{}, err
	}
	if a.lifecycle != nil && !historical {
		run.CurrentCandidates, err = a.lifecycle.Track(ctx, run, sessionRevision, a.inferenceRuleSetGuard(authorized, inputStamp, principal, run.Request.Evaluation))
		if err != nil {
			return ontologyreasoning.ReasoningRun{}, err
		}
	}
	return run, nil
}

// Historical registrations support identity resolution, never an independent
// active business premise after the source assertions have been withdrawn.
func identityMetadataProperty(objectType, property string) bool {
	return objectType == "knowledge_entity" && property == "identity_keys"
}

func (a *productionOntologyInferenceAPI) inferenceCommitGuard(authorized ontologyscope.AuthorizedScopeContext, stamp string) func(context.Context, db.Tx) error {
	return func(ctx context.Context, tx db.Tx) error {
		binding, snapshot := authorized.Binding(), authorized.Snapshot()
		if err := ontology.FenceMutationAuthorization(ctx, tx, ontology.MetadataPromotionSecurityRevisions{Authorization: snapshot.AuthorizationRevision, Policy: snapshot.PolicyRevision}, ontology.MetadataPromotionScopeBinding{TenantID: binding.TenantID, BindingVersion: binding.BindingVersion, PersistedBindingVersion: binding.PersistedBindingVersion, PersistenceRequired: binding.PersistenceRequired}); err != nil {
			return err
		}
		return a.lifecycle.FenceInput(ctx, tx, binding.TenantID, stamp)
	}
}

func (a *productionOntologyInferenceAPI) ExportSchema(ctx context.Context, principal identity.Principal, tenant string) (ontologyreasoning.SchemaExport, error) {
	if a == nil || a.gate == nil || a.registry == nil {
		return ontologyreasoning.SchemaExport{}, ontologyscope.ErrDependencyUnavailable
	}
	authorized, err := a.gate.AuthorizeRead(ctx, principal, tenant)
	if err != nil {
		return ontologyreasoning.SchemaExport{}, err
	}
	pack, err := a.schemaPack(ctx, authorized.Binding().TenantID)
	if err != nil {
		return ontologyreasoning.SchemaExport{}, err
	}
	exported, err := ontologyreasoning.ExportSchema(pack)
	if err != nil {
		return ontologyreasoning.SchemaExport{}, err
	}
	if err := a.checkFence(ctx, principal, authorized); err != nil {
		return ontologyreasoning.SchemaExport{}, err
	}
	return exported, nil
}

func (a *productionOntologyInferenceAPI) schemaPack(ctx context.Context, tenant string) (ontologyreasoning.ContextPack, error) {
	snapshot, err := ontology.GetRegistrySnapshot(ctx, a.registry, tenant)
	if err != nil {
		return ontologyreasoning.ContextPack{}, err
	}
	return ontologyreasoning.ContextPack{TenantID: tenant, AllowedObjectTypes: snapshot.ObjectTypes, AllowedLinkTypes: snapshot.LinkTypes, AllowedPropertyTypes: snapshot.PropertyTypes, CreatedAt: time.Now().UTC()}, nil
}

func (a *productionOntologyInferenceAPI) checkFence(ctx context.Context, p identity.Principal, previous ontologyscope.AuthorizedScopeContext) error {
	var current ontologyscope.AuthorizedScopeContext
	var err error
	if previous.Manage() {
		current, err = a.gate.AuthorizeManage(ctx, p, previous.Binding().TenantID)
	} else {
		current, err = a.gate.AuthorizeRead(ctx, p, previous.Binding().TenantID)
	}
	if err != nil {
		return err
	}
	x, y := previous.Snapshot(), current.Snapshot()
	if previous.Binding().BindingVersion != current.Binding().BindingVersion || previous.Binding().PersistedBindingVersion != current.Binding().PersistedBindingVersion || x.PrincipalKey != y.PrincipalKey || x.AuthorizationRevision != y.AuthorizationRevision || x.PolicyRevision != y.PolicyRevision {
		return fmt.Errorf("%w: authorization changed during inference", ontologyscope.ErrAccessDenied)
	}
	return nil
}

func inferenceDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (a *productionOntologyInferenceAPI) filterInferencePolicy(ctx context.Context, principal identity.Principal, pack *ontologyreasoning.ContextPack, root string) error {
	hidden := map[string]bool{}
	masked := map[string]map[string]bool{}
	for _, purpose := range [][2]string{{"ontology_workbench", "ontology.context.read"}, {"ontology_reasoning", "reasoning_context"}} {
		decision, err := a.policy.DecideContext(ctx, contextgraph.BuildContextBundleRequest{TenantID: pack.TenantID, Consumer: purpose[0], Purpose: purpose[1], ActorRef: schemaPolicyActor(principal)}, pack.CandidateObjects)
		if err != nil {
			return err
		}
		for _, id := range decision.HiddenObjectIDs {
			hidden[id] = true
		}
		for id, paths := range decision.MaskedFields {
			if masked[id] == nil {
				masked[id] = map[string]bool{}
			}
			for _, path := range paths {
				path = strings.TrimSpace(path)
				if path == "properties" {
					masked[id]["*"] = true
					continue
				}
				path = strings.TrimPrefix(path, "properties.")
				masked[id][path] = true
				masked[id][strings.SplitN(path, ".", 2)[0]] = true
			}
		}
	}
	if hidden[root] {
		return ontologycontract.ErrUnavailable
	}
	objects := pack.CandidateObjects[:0]
	pack.EvidenceRefs = nil
	for _, o := range pack.CandidateObjects {
		if hidden[o.ID] {
			continue
		}
		for key := range o.Properties {
			if masked[o.ID]["*"] || masked[o.ID][key] {
				delete(o.Properties, key)
			}
		}
		objects = append(objects, o)
		pack.EvidenceRefs = append(pack.EvidenceRefs, o.EvidenceRefs...)
	}
	pack.CandidateObjects = objects
	visibleEvidence := map[string]bool{}
	for _, ref := range pack.EvidenceRefs {
		visibleEvidence[ref.ID] = true
	}
	items := pack.Evidence[:0]
	for _, item := range pack.Evidence {
		if visibleEvidence[item.ID] {
			items = append(items, item)
		}
	}
	pack.Evidence = items
	trimRefs := func(refs []contextgraph.Ref) []contextgraph.Ref {
		var selected []contextgraph.Ref
		for _, ref := range refs {
			if ref.Kind == "evidence" && visibleEvidence[ref.ID] {
				selected = append(selected, ref)
			}
		}
		return selected
	}
	for i := range pack.Facts {
		pack.Facts[i].EvidenceRefs = trimRefs(pack.Facts[i].EvidenceRefs)
	}
	for i := range pack.NeighborLinks {
		pack.NeighborLinks[i].EvidenceRefs = trimRefs(pack.NeighborLinks[i].EvidenceRefs)
	}
	facts := pack.Facts[:0]
	for _, f := range pack.Facts {
		if !hidden[f.Subject.ID] && !masked[f.Subject.ID]["*"] && !masked[f.Subject.ID][strings.TrimSpace(f.Predicate)] {
			facts = append(facts, f)
		}
	}
	pack.Facts = facts
	links := pack.NeighborLinks[:0]
	for _, l := range pack.NeighborLinks {
		if !hidden[l.FromObjectID] && !hidden[l.ToObjectID] {
			links = append(links, l)
		}
	}
	pack.NeighborLinks = links
	return nil
}
