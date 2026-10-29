package contextview

import (
	"context"
	"errors"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type InferenceSnapshotQuery struct {
	TenantID, RootID      string
	BusinessAsOf, KnownAt time.Time
}

type InferenceSnapshot struct {
	Objects     []contextgraph.Object
	Links       []contextgraph.Link
	Facts       []contextgraph.Fact
	Evidence    []contextgraph.Evidence
	Limitations []string
	// Host-only source locators are not part of the projected model payload.
	SourceAssertions []knowledgeinstances.AssertionSourceQuery
}

// Historical coordinates select data versions, never authorization versions.
// Both each current parent and each selected historical resource pass today's
// scope/access/data policies. Only policy-projected fields leave this reader.
func (s *Service) InferenceSnapshot(ctx context.Context, p identity.Principal, q InferenceSnapshotQuery) (InferenceSnapshot, error) {
	var result InferenceSnapshot
	if !validID(q.RootID) || !validID(q.TenantID) || !contextgraph.TemporalCoordinateValid(q.BusinessAsOf) || !contextgraph.TemporalCoordinateValid(q.KnownAt) {
		return result, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, p, q.TenantID)
	if err != nil {
		return result, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryInferenceHistory)
	if err != nil {
		return result, err
	}
	defer cancel()
	state, err := s.resolveVisibility(bounded, p, authorized, budget, datavisibility.ResourceObject, datavisibility.ResourceLink, datavisibility.ResourceFact, datavisibility.ResourceEvidence)
	if err != nil {
		return result, err
	}
	reader, ok := s.graph.(contextgraph.TemporalSubgraphStore)
	if !ok {
		return result, ErrUnavailable
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return result, err
	}
	objects, links, facts, evidence, err := reader.SnapshotSubgraphAsOf(bounded, authorized.Binding().TenantID, []contextgraph.Ref{{ID: q.RootID}}, 2, contextgraph.CurrentSubgraphLimits{Objects: 25, Links: 100, Facts: 1000, Evidence: 400}, q.BusinessAsOf, q.KnownAt)
	if err != nil {
		return result, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(len(objects) + len(links) + len(facts) + len(evidence)); err != nil {
		return result, err
	}
	var readable []contextgraph.Object
	for _, o := range objects {
		// A historical access ref cannot restore access to a currently hidden
		// parent or bypass a current ownership/policy change.
		if _, err := s.getVisibleParent(bounded, p, q.TenantID, o.ID, state, budget); err != nil {
			if errors.Is(err, ErrUnavailable) {
				continue
			}
			return result, err
		}
		readable = append(readable, o)
	}
	rows, err := s.visibleObjectRows(bounded, p, state, readable, true, "temporal")
	if err != nil {
		return result, err
	}
	nodes := map[string]ObjectItem{}
	parents := map[string]contextgraph.Object{}
	for _, row := range rows {
		masked := maskedFieldSet(row.decision)
		if masked["object_type"] || masked["status"] || temporalMasked(masked) {
			continue
		}
		d := row.detail
		o := contextgraph.Object{ID: d.ID, TenantID: q.TenantID, ObjectType: d.ObjectType, Label: d.Label, CanonicalKey: d.CanonicalKey, Status: d.Status, Properties: d.Properties, Temporal: row.raw.Temporal, SourceStreamID: row.raw.SourceStreamID, SourceVersion: row.raw.SourceVersion}
		if d.UpdatedAt != nil {
			o.UpdatedAt = *d.UpdatedAt
		}
		parents[o.ID], nodes[o.ID] = row.raw, d.ObjectItem
		result.Objects = append(result.Objects, o)
	}
	if _, ok := nodes[q.RootID]; !ok {
		return InferenceSnapshot{}, ErrUnavailable
	}
	readableSources := map[string]bool{}
	for id := range parents {
		readableSources[id] = true
	}
	canReadSource := func(props map[string]any, provenance contextgraph.Ref) (bool, error) {
		if provenance.Kind != "knowledge_instances" {
			return true, nil
		}
		root, _ := props["source_root_id"].(string)
		if root == "" {
			return false, nil
		}
		if allowed, ok := readableSources[root]; ok {
			return allowed, nil
		}
		_, err := s.getVisibleParent(bounded, p, q.TenantID, root, state, budget)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return false, err
		}
		readableSources[root] = err == nil
		return err == nil, nil
	}
	edges, err := s.visibleNeighborhoodEdges(bounded, p, state, links, readable, nodes)
	if err != nil {
		return result, err
	}
	rawLinks := map[string]contextgraph.Link{}
	for _, l := range links {
		rawLinks[l.ID] = l
	}
	var traversable []NeighborhoodEdge
	legacy, unspecified, inaccessible := false, false, false
	for _, edge := range edges {
		if edge.Redacted || strings.HasPrefix(edge.ID, "inference-link:") {
			continue
		}
		raw := rawLinks[edge.ID]
		if raw.Provenance.Kind == "knowledge_instances" && (raw.Temporal.Mode == "" || raw.Temporal.Mode == "static") {
			legacy = true
			continue
		}
		if allowed, err := canReadSource(raw.Properties, raw.Provenance); err != nil {
			return result, err
		} else if !allowed {
			inaccessible = true
			continue
		}
		l := contextgraph.Link{ID: edge.ID, TenantID: q.TenantID, LinkType: edge.LinkType, FromObjectID: edge.SourceID, ToObjectID: edge.TargetID, Status: edge.Status, Confidence: edge.Confidence, Temporal: raw.Temporal, SourceStreamID: raw.SourceStreamID, SourceVersion: raw.SourceVersion, EvidenceRefs: raw.EvidenceRefs}
		if edge.RelationCode != "" {
			l.Properties = map[string]any{"relation_code": edge.RelationCode}
		}
		if edge.UpdatedAt != nil {
			l.UpdatedAt = *edge.UpdatedAt
		}
		result.Links = append(result.Links, l)
		if raw.Provenance.Kind == knowledgeinstances.Domain {
			result.SourceAssertions = append(result.SourceAssertions, knowledgeinstances.AssertionSourceQuery{Kind: "link", ID: raw.ID, Stream: raw.SourceStreamID})
		}
		traversable = append(traversable, edge)
	}
	reachable := reachableNeighborhood(q.RootID, 2, traversable)
	var selectedObjects []contextgraph.Object
	for _, o := range result.Objects {
		if reachable[o.ID] {
			selectedObjects = append(selectedObjects, o)
		}
	}
	result.Objects = selectedObjects
	for _, o := range selectedObjects {
		var rawFacts []contextgraph.Fact
		byID := map[string]contextgraph.Fact{}
		for _, f := range facts {
			if f.Subject.ID == o.ID {
				rawFacts = append(rawFacts, f)
				byID[f.ID] = f
			}
		}
		visible, err := s.visibleFacts(bounded, p, state, parents[o.ID], rawFacts)
		if err != nil {
			return result, err
		}
		for _, f := range visible {
			if f.Redacted {
				continue
			}
			raw := byID[f.ID]
			if raw.Provenance.Kind == "knowledge_instances" && (raw.Temporal.Mode == "" || raw.Temporal.Mode == "static") {
				legacy = true
				continue
			}
			if raw.Temporal.BusinessTimeUnspecified {
				unspecified = true
			}
			if allowed, err := canReadSource(raw.Properties, raw.Provenance); err != nil {
				return result, err
			} else if !allowed {
				inaccessible = true
				continue
			}
			f.Status = contextgraph.AssertionStatusAt(raw, q.BusinessAsOf)
			safe := contextgraph.Fact{ID: f.ID, TenantID: q.TenantID, Subject: contextgraph.Ref{Kind: o.ObjectType, ID: o.ID}, Predicate: f.Predicate, Value: f.Value, Status: f.Status, Confidence: f.Confidence, Temporal: raw.Temporal, SourceStreamID: raw.SourceStreamID, SourceVersion: raw.SourceVersion, EvidenceRefs: raw.EvidenceRefs}
			if f.UpdatedAt != nil {
				safe.UpdatedAt = *f.UpdatedAt
			}
			result.Facts = append(result.Facts, safe)
			if raw.Provenance.Kind == knowledgeinstances.Domain {
				result.SourceAssertions = append(result.SourceAssertions, knowledgeinstances.AssertionSourceQuery{Kind: "fact", ID: raw.ID, Stream: raw.SourceStreamID})
			}
		}
	}
	visibleEvidence, err := s.visibleEvidence(bounded, p, state, evidence)
	if err != nil {
		return result, err
	}
	allowedEvidence := map[string]bool{}
	for _, e := range visibleEvidence {
		if strings.HasPrefix(e.SourceKind, "ontology_inference") {
			continue
		}
		allowedEvidence[e.ID] = true
		safe := contextgraph.Evidence{ID: e.ID, TenantID: q.TenantID, SourceRef: contextgraph.Ref{Kind: e.SourceKind, ID: e.SourceID}, Summary: e.Summary, Confidence: e.Confidence}
		if e.ObservedAt != nil {
			safe.ObservedAt = *e.ObservedAt
		}
		result.Evidence = append(result.Evidence, safe)
	}
	filterRefs := func(refs []contextgraph.Ref) []contextgraph.Ref {
		var result []contextgraph.Ref
		for _, ref := range refs {
			if ref.Kind == "evidence" && allowedEvidence[ref.ID] {
				result = append(result, ref)
			}
		}
		return result
	}
	for i := range result.Facts {
		result.Facts[i].EvidenceRefs = filterRefs(result.Facts[i].EvidenceRefs)
	}
	var selectedLinks []contextgraph.Link
	for _, l := range result.Links {
		if reachable[l.FromObjectID] && reachable[l.ToObjectID] {
			l.EvidenceRefs = filterRefs(l.EvidenceRefs)
			selectedLinks = append(selectedLinks, l)
		}
	}
	result.Links = selectedLinks
	for i := range result.Objects {
		for _, f := range result.Facts {
			if f.Subject.ID == result.Objects[i].ID {
				result.Objects[i].EvidenceRefs = append(result.Objects[i].EvidenceRefs, f.EvidenceRefs...)
			}
		}
		for _, l := range result.Links {
			if l.FromObjectID == result.Objects[i].ID || l.ToObjectID == result.Objects[i].ID {
				result.Objects[i].EvidenceRefs = append(result.Objects[i].EvidenceRefs, l.EvidenceRefs...)
			}
		}
	}
	if legacy {
		result.Limitations = append(result.Limitations, "unversioned_legacy_assertions_excluded")
	}
	if inaccessible {
		result.Limitations = append(result.Limitations, "source_access_unavailable_assertions_excluded")
	}
	if unspecified {
		result.Limitations = append(result.Limitations, "business_start_unspecified")
	}
	for _, o := range result.Objects {
		if o.Temporal.Mode == "" || o.Temporal.Mode == "static" {
			result.Limitations = append(result.Limitations, "static_resources_have_no_version_history")
			break
		}
	}
	return result, nil
}
