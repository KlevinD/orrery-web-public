package main

import (
	"context"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/retrievalevidence"
)

type groundedSnapshotReader interface {
	InferenceSnapshot(context.Context, identity.Principal, contextview.InferenceSnapshotQuery) (contextview.InferenceSnapshot, error)
}

func (a *productionOntologyInferenceAPI) GroundedContext(ctx context.Context, p identity.Principal, req ontologyreasoning.GroundedContextRequest) (ontologyreasoning.GroundedContext, error) {
	var empty ontologyreasoning.GroundedContext
	if err := ontologyreasoning.ValidateGroundedContextRequest(req); err != nil {
		return empty, err
	}
	if p.CredentialScopeRestricted && !permissionscope.AnyAllows(p.CredentialScopes, permissionscope.OntologyRead) {
		return empty, ontologyscope.ErrAccessDenied
	}
	if a == nil || a.gate == nil || a.service == nil || a.registry == nil || a.policy == nil || a.concurrency == nil || a.lifecycle == nil {
		return empty, ontologyscope.ErrDependencyUnavailable
	}
	reader, ok := a.context.(groundedSnapshotReader)
	if !ok {
		return empty, ontologyscope.ErrDependencyUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	authorized, err := a.gate.AuthorizeRead(ctx, p, req.TenantID)
	if err != nil {
		return empty, err
	}
	release, err := a.concurrency.Acquire(ctx, ontologycontract.ConcurrencyInference, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return empty, err
	}
	defer release()
	req.TenantID = authorized.Binding().TenantID
	before, err := a.lifecycle.InputStamp(ctx, req.TenantID)
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC()
	query := contextview.InferenceSnapshotQuery{TenantID: req.TenantID, RootID: req.RootID, BusinessAsOf: now, KnownAt: now}
	snapshot, err := reader.InferenceSnapshot(ctx, p, query)
	if err != nil {
		return empty, err
	}
	// Policy projection may filter slices and maps in place. Freeze the raw
	// authorized snapshot before those transformations for the final reread.
	snapshotHash := inferenceDigest(snapshot)
	pack, err := a.schemaPack(ctx, req.TenantID)
	if err != nil {
		return empty, err
	}
	pack.CandidateObjects, pack.NeighborLinks, pack.Facts, pack.Evidence = snapshot.Objects, snapshot.Links, snapshot.Facts, snapshot.Evidence
	for _, e := range snapshot.Evidence {
		pack.EvidenceRefs = append(pack.EvidenceRefs, contextgraph.Ref{Kind: "evidence", ID: e.ID})
	}
	if err := a.filterInferencePolicy(ctx, p, &pack, req.RootID); err != nil {
		return empty, err
	}
	assertions := selectedSourceAssertions(pack, snapshot.SourceAssertions)
	sources := map[string]ontologyreasoning.GroundedSource{}
	var sourceReader groundedSourceReader
	if len(assertions) > 0 {
		if a.documents == nil {
			return empty, ontologyscope.ErrDependencyUnavailable
		}
		sourceReader, ok = a.documents.sources.(groundedSourceReader)
		if !ok {
			return empty, ontologyscope.ErrDependencyUnavailable
		}
		sources, err = sourceReader.CheckGroundingSources(ctx, p, req.TenantID, assertions)
		if err != nil {
			return empty, err
		}
	}
	pack, omitted := groundedSourcePack(pack, assertions, sources)
	record := ontologyreasoning.EvaluationRecord{RootID: req.RootID, ActorHash: authorized.Snapshot().PrincipalKey, QueryHash: inferenceDigest(req), BusinessAsOf: now, KnownAt: now}
	out, err := a.service.GroundContext(ctx, pack, record, req, sources)
	if err != nil {
		return empty, err
	}
	// Re-read policy-projected fields, including current source ownership, and
	// separately recheck publication/catalog versions across store boundaries.
	fresh, err := reader.InferenceSnapshot(ctx, p, query)
	if err != nil {
		return empty, err
	}
	if snapshotHash != inferenceDigest(fresh) {
		return empty, retrievalevidence.ErrInputChanged
	}
	if sourceReader != nil {
		current, err := sourceReader.CheckGroundingSources(ctx, p, req.TenantID, assertions)
		if err != nil {
			return empty, err
		}
		if inferenceDigest(current) != inferenceDigest(sources) {
			return empty, retrievalevidence.ErrInputChanged
		}
	}
	after, err := a.lifecycle.InputStamp(ctx, req.TenantID)
	if err != nil {
		return empty, err
	}
	if before != after {
		return empty, retrievalevidence.ErrInputChanged
	}
	if err := a.checkFence(ctx, p, authorized); err != nil {
		return empty, err
	}
	out.CheckedAt = time.Now().UTC()
	if out.NextBoundary != nil && !out.CheckedAt.Before(*out.NextBoundary) {
		return empty, retrievalevidence.ErrInputChanged
	}
	authSnapshot := authorized.Snapshot()
	out.AccessHash = inferenceDigest([]any{authorized.Binding(), authSnapshot.PrincipalKey, authSnapshot.AuthorizationRevision, authSnapshot.PolicyRevision})
	out.Limitations = append(out.Limitations, snapshot.Limitations...)
	if omitted {
		out.Limitations = append(out.Limitations, "unattested_document_assertions_excluded")
	}
	return out, nil
}

func selectedSourceAssertions(pack ontologyreasoning.ContextPack, all []knowledgeinstances.AssertionSourceQuery) []knowledgeinstances.AssertionSourceQuery {
	selected := map[string]bool{}
	for _, f := range pack.Facts {
		selected["fact:"+f.ID] = true
	}
	for _, l := range pack.NeighborLinks {
		selected["link:"+l.ID] = true
	}
	var out []knowledgeinstances.AssertionSourceQuery
	for _, a := range all {
		if selected[a.Kind+":"+a.ID] {
			out = append(out, a)
		}
	}
	return out
}

func groundedSourcePack(pack ontologyreasoning.ContextPack, assertions []knowledgeinstances.AssertionSourceQuery, sources map[string]ontologyreasoning.GroundedSource) (ontologyreasoning.ContextPack, bool) {
	document := map[string]bool{}
	for _, a := range assertions {
		document[a.Kind+":"+a.ID] = true
	}
	omitted := false
	keep := func(ref string) bool {
		_, attested := sources[ref]
		if document[ref] && !attested {
			omitted = true
			return false
		}
		return true
	}
	facts := make([]contextgraph.Fact, 0, len(pack.Facts))
	for _, f := range pack.Facts {
		if keep("fact:" + f.ID) {
			facts = append(facts, f)
		}
	}
	pack.Facts = facts
	links := make([]contextgraph.Link, 0, len(pack.NeighborLinks))
	for _, l := range pack.NeighborLinks {
		if keep("link:" + l.ID) {
			links = append(links, l)
		}
	}
	pack.NeighborLinks = links
	return pack, omitted
}
