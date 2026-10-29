package ontologyreasoning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

const defaultContextPackLimit = 25

func (s *Service) BuildContextPack(ctx context.Context, req BuildContextPackRequest) (ContextPack, error) {
	if err := ctx.Err(); err != nil {
		return ContextPack{}, err
	}
	req.TenantID = strings.TrimSpace(req.TenantID)
	if req.TenantID == "" {
		return ContextPack{}, fmt.Errorf("%w: tenant_id is required", ErrInvalidRequest)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultContextPackLimit
	}

	pack := ContextPack{
		TenantID:       req.TenantID,
		IntentHash:     hashString(strings.TrimSpace(req.Intent)),
		EvidenceRefs:   cloneRefs(req.EvidenceRefs),
		CreatedAt:      s.clock(),
		KnownConflicts: []ValidationIssue{},
	}
	if s.registry != nil {
		if err := s.populateRegistryContext(ctx, req.TenantID, &pack); err != nil {
			return ContextPack{}, err
		}
	}
	if s.context != nil {
		if err := s.populatePolicyFilteredContext(ctx, req, limit, &pack); err != nil {
			return ContextPack{}, err
		}
	} else if s.graph != nil {
		if err := s.populateGraphContext(ctx, req, limit, &pack); err != nil {
			return ContextPack{}, err
		}
	}
	if s.actions != nil {
		actions, err := s.actions.ListActionTypes(ctx)
		if err != nil {
			return ContextPack{}, err
		}
		for _, actionType := range actions {
			if definitionVisibleToTenant(actionType.TenantID, req.TenantID) {
				pack.AllowedActions = append(pack.AllowedActions, actionType)
			}
		}
	}
	pack.Hash = hashContextPack(pack)
	return pack, nil
}

func (s *Service) populatePolicyFilteredContext(ctx context.Context, req BuildContextPackRequest, limit int, pack *ContextPack) error {
	if req.ActorRef.ID == "" || len(req.SubjectRefs) == 0 {
		return fmt.Errorf("%w: actor_ref and subject_refs are required", ErrInvalidRequest)
	}
	bundle, err := s.context.BuildContextBundle(ctx, contextgraph.BuildContextBundleRequest{
		TenantID:    req.TenantID,
		Consumer:    "ontology_reasoning",
		Purpose:     "reasoning_context",
		ActorRef:    req.ActorRef,
		SubjectRefs: req.SubjectRefs,
		MaxDepth:    1,
		TokenBudget: limit,
	})
	if err != nil {
		return err
	}
	pack.CandidateObjects = append(pack.CandidateObjects, limitObjects(bundle.Objects, limit)...)
	selected := map[string]bool{}
	for _, object := range pack.CandidateObjects {
		selected[object.ID] = true
		for _, ref := range object.EvidenceRefs {
			appendContextPackEvidenceRef(pack, ref)
		}
	}
	for _, link := range bundle.Links {
		if selected[link.FromObjectID] && selected[link.ToObjectID] {
			pack.NeighborLinks = append(pack.NeighborLinks, link)
		}
	}
	for _, fact := range bundle.Facts {
		if selected[fact.Subject.ID] {
			pack.Facts = append(pack.Facts, fact)
			for _, ref := range fact.EvidenceRefs {
				appendContextPackEvidenceRef(pack, ref)
			}
		}
	}
	for _, evidence := range bundle.Evidence {
		appendContextPackEvidenceRef(pack, contextgraph.Ref{Kind: "evidence", ID: evidence.ID})
	}
	return nil
}

func limitObjects(objects []contextgraph.Object, limit int) []contextgraph.Object {
	if limit <= 0 || len(objects) <= limit {
		return append([]contextgraph.Object(nil), objects...)
	}
	return append([]contextgraph.Object(nil), objects[:limit]...)
}

func appendContextPackEvidenceRef(pack *ContextPack, ref contextgraph.Ref) {
	key := refKey(ref)
	if key == "" {
		return
	}
	for _, existing := range pack.EvidenceRefs {
		if refKey(existing) == key {
			return
		}
	}
	pack.EvidenceRefs = append(pack.EvidenceRefs, ref)
}

func (s *Service) populateRegistryContext(ctx context.Context, tenantID string, pack *ContextPack) error {
	objectTypes, err := s.registry.ListObjectTypes(ctx)
	if err != nil {
		return err
	}
	for _, objectType := range objectTypes {
		if definitionVisibleToTenant(objectType.TenantID, tenantID) {
			pack.AllowedObjectTypes = append(pack.AllowedObjectTypes, objectType)
		}
	}
	linkTypes, err := s.registry.ListLinkTypes(ctx)
	if err != nil {
		return err
	}
	for _, linkType := range linkTypes {
		if definitionVisibleToTenant(linkType.TenantID, tenantID) {
			pack.AllowedLinkTypes = append(pack.AllowedLinkTypes, linkType)
		}
	}
	for _, objectType := range pack.AllowedObjectTypes {
		propertyTypes, err := s.registry.ListPropertyTypes(ctx, objectType.ID)
		if err != nil {
			return err
		}
		for _, propertyType := range propertyTypes {
			if definitionVisibleToTenant(propertyType.TenantID, tenantID) {
				pack.AllowedPropertyTypes = append(pack.AllowedPropertyTypes, propertyType)
			}
		}
	}
	return nil
}

func (s *Service) populateGraphContext(ctx context.Context, req BuildContextPackRequest, limit int, pack *ContextPack) error {
	objects, links, facts, evidence, err := s.graph.Snapshot(ctx, req.TenantID)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	subjects := subjectRefSet(req.SubjectRefs)
	for _, object := range objects {
		if object.TenantID != req.TenantID {
			continue
		}
		if len(subjects) > 0 && !subjects[object.ObjectType+":"+object.ID] && !subjects[object.ID] {
			continue
		}
		if len(pack.CandidateObjects) >= limit {
			break
		}
		pack.CandidateObjects = append(pack.CandidateObjects, object)
		selected[object.ID] = true
	}
	for _, link := range links {
		if link.TenantID != req.TenantID {
			continue
		}
		if selected[link.FromObjectID] || selected[link.ToObjectID] {
			pack.NeighborLinks = append(pack.NeighborLinks, link)
			selected[link.FromObjectID] = true
			selected[link.ToObjectID] = true
		}
	}
	for _, fact := range facts {
		if fact.TenantID == req.TenantID && selected[fact.Subject.ID] {
			pack.Facts = append(pack.Facts, fact)
		}
	}
	seenEvidence := refSet(pack.EvidenceRefs)
	for _, object := range pack.CandidateObjects {
		for _, ref := range object.EvidenceRefs {
			if key := refKey(ref); key != "" && !seenEvidence[key] {
				pack.EvidenceRefs = append(pack.EvidenceRefs, ref)
				seenEvidence[key] = true
			}
		}
	}
	for _, item := range evidence {
		if item.TenantID == req.TenantID && evidenceReferencedBySelected(item.ID, pack.CandidateObjects, pack.Facts) {
			ref := item.SourceRef
			if ref.Kind == "" {
				ref.Kind = "evidence"
			}
			if ref.ID == "" {
				ref.ID = item.ID
			}
			if key := refKey(ref); key != "" && !seenEvidence[key] {
				pack.EvidenceRefs = append(pack.EvidenceRefs, ref)
				seenEvidence[key] = true
			}
		}
	}
	return nil
}

func evidenceReferencedBySelected(evidenceID string, objects []contextgraph.Object, facts []contextgraph.Fact) bool {
	for _, object := range objects {
		for _, ref := range object.EvidenceRefs {
			if ref.ID == evidenceID {
				return true
			}
		}
	}
	for _, fact := range facts {
		for _, ref := range fact.EvidenceRefs {
			if ref.ID == evidenceID {
				return true
			}
		}
	}
	return false
}

func definitionVisibleToTenant(definitionTenantID string, tenantID string) bool {
	definitionTenantID = strings.TrimSpace(definitionTenantID)
	tenantID = strings.TrimSpace(tenantID)
	return definitionTenantID == "" || definitionTenantID == "system" || definitionTenantID == tenantID
}

func subjectRefSet(refs []contextgraph.Ref) map[string]bool {
	set := map[string]bool{}
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		set[ref.ID] = true
		if ref.Kind != "" {
			set[ref.Kind+":"+ref.ID] = true
		}
	}
	return set
}

func refSet(refs []contextgraph.Ref) map[string]bool {
	set := map[string]bool{}
	for _, ref := range refs {
		if key := refKey(ref); key != "" {
			set[key] = true
		}
	}
	return set
}

func refKey(ref contextgraph.Ref) string {
	if ref.ID == "" {
		return ""
	}
	return ref.Kind + ":" + ref.ID
}

func cloneRefs(refs []contextgraph.Ref) []contextgraph.Ref {
	return append([]contextgraph.Ref(nil), refs...)
}

func hashString(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashContextPack(pack ContextPack) string {
	pack.Hash = ""
	pack.CreatedAt = time.Time{}
	return hashJSON(pack)
}
