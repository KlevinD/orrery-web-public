package knowledgeontology

import (
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

func ProjectPublishedDocument(req ProjectRequest) Projection {
	projection := Projection{
		TenantID: req.TenantID, KnowledgeBaseID: req.KnowledgeBaseID,
		DocumentID: req.DocumentID, Revision: req.Revision, ContentHash: req.ContentHash,
		KnowledgeDocumentObjectID: resolveKnowledgeDocumentObjectID(req),
		Subjects:                  make([]SubjectProjection, 0, len(req.Extraction.Subjects)),
		Assertions:                make([]AssertionProjection, 0, len(req.Extraction.Assertions)),
		ExistingLinks:             append([]contextgraph.Link(nil), req.Links...),
		ExistingEvidence:          append([]contextgraph.Evidence(nil), req.Evidence...),
	}
	for index, ref := range req.Extraction.Subjects {
		objectID, resolution := resolveObject(req.TenantID, ref, req.Objects)
		status := AssertionConfirmed
		if resolution != ResolutionResolved {
			status = AssertionUnresolved
		}
		projection.Subjects = append(projection.Subjects, SubjectProjection{
			Reference: ref, ObjectID: objectID, Resolution: resolution, Status: status,
			Surface: ref.ID, Confidence: 1,
			Evidence: evidence(req, fmt.Sprintf("frontmatter:ontology.subjects[%d]", index), "explicit_frontmatter"),
		})
	}
	projection.Subjects = append(projection.Subjects, extractEntityMentions(req, projection.Subjects)...)
	for index, assertion := range req.Extraction.Assertions {
		fromID, fromResolution := resolveObject(req.TenantID, assertion.From, req.Objects)
		toID, toResolution := resolveObject(req.TenantID, assertion.To, req.Objects)
		status := AssertionConfirmed
		if fromResolution != ResolutionResolved || toResolution != ResolutionResolved {
			status = AssertionUnresolved
		}
		anchor := assertion.Anchor
		if anchor == "" {
			anchor = fmt.Sprintf("frontmatter:ontology.assertions[%d]", index)
		}
		projection.Assertions = append(projection.Assertions, AssertionProjection{
			LinkType: assertion.LinkType, From: assertion.From, To: assertion.To,
			FromObjectID: fromID, ToObjectID: toID, Status: status,
			Evidence: evidence(req, anchor, "explicit_frontmatter"),
		})
	}
	return projection
}

func resolveKnowledgeDocumentObjectID(req ProjectRequest) string {
	facts := make(map[string]map[string]string)
	for _, fact := range req.Facts {
		if fact.TenantID != req.TenantID || !strings.EqualFold(fact.Subject.Kind, "knowledge_document") {
			continue
		}
		value, ok := fact.Value.(string)
		if !ok {
			continue
		}
		if facts[fact.Subject.ID] == nil {
			facts[fact.Subject.ID] = make(map[string]string)
		}
		facts[fact.Subject.ID][strings.ToLower(strings.TrimSpace(fact.Predicate))] = strings.TrimSpace(value)
	}
	candidates := make([]string, 0, 1)
	for _, object := range req.Objects {
		if object.TenantID != req.TenantID || !strings.EqualFold(object.ObjectType, "knowledge_document") {
			continue
		}
		values := facts[object.ID]
		providerID := strings.TrimSpace(values["provider_document_id"])
		knowledgeBaseID := strings.TrimSpace(values["knowledge_base_id"])
		if providerID == "" {
			providerID, _ = object.Properties["provider_document_id"].(string)
		}
		if knowledgeBaseID == "" {
			knowledgeBaseID, _ = object.Properties["knowledge_base_id"].(string)
		}
		providerMatches := strings.EqualFold(strings.TrimSpace(providerID), strings.TrimSpace(req.DocumentID)) ||
			(strings.TrimSpace(providerID) == "" && strings.EqualFold(strings.TrimSpace(object.ID), strings.TrimSpace(req.DocumentID)))
		baseMatches := strings.TrimSpace(knowledgeBaseID) == "" || strings.EqualFold(strings.TrimSpace(knowledgeBaseID), strings.TrimSpace(req.KnowledgeBaseID))
		if providerMatches && baseMatches {
			candidates = appendUnique(candidates, object.ID)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

func resolveObject(tenantID string, ref ObjectRef, objects []contextgraph.Object) (string, Resolution) {
	direct := make([]string, 0, 1)
	aliases := make([]string, 0, 1)
	for _, object := range objects {
		if object.TenantID != tenantID || !strings.EqualFold(object.ObjectType, ref.Kind) {
			continue
		}
		if directObjectMatch(object, ref) {
			direct = appendUnique(direct, object.ID)
			continue
		}
		if objectAliasMatch(object, ref.ID) {
			aliases = appendUnique(aliases, object.ID)
		}
	}
	if len(direct) == 1 {
		return direct[0], ResolutionResolved
	}
	if len(direct) > 1 {
		return "", ResolutionAmbiguous
	}
	if len(aliases) == 1 {
		return aliases[0], ResolutionResolved
	}
	if len(aliases) > 1 {
		return "", ResolutionAmbiguous
	}
	return "", ResolutionUnresolved
}

func directObjectMatch(object contextgraph.Object, ref ObjectRef) bool {
	if strings.EqualFold(strings.TrimSpace(object.ID), ref.ID) ||
		strings.EqualFold(strings.TrimSpace(object.CanonicalKey), ref.ID) ||
		strings.EqualFold(strings.TrimSpace(object.CanonicalKey), ref.Kind+":"+ref.ID) {
		return true
	}
	externalID, _ := object.Properties["external_id"].(string)
	return strings.EqualFold(strings.TrimSpace(externalID), ref.ID)
}

func objectAliasMatch(object contextgraph.Object, id string) bool {
	value, exists := object.Properties["aliases"]
	if !exists {
		return false
	}
	switch aliases := value.(type) {
	case []any:
		for _, candidate := range aliases {
			if text, ok := candidate.(string); ok && strings.EqualFold(strings.TrimSpace(text), id) {
				return true
			}
		}
	case []string:
		for _, candidate := range aliases {
			if strings.EqualFold(strings.TrimSpace(candidate), id) {
				return true
			}
		}
	case string:
		return strings.EqualFold(strings.TrimSpace(aliases), id)
	}
	return false
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func evidence(req ProjectRequest, anchor, method string) Evidence {
	observedAt := req.PublishedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return Evidence{
		KnowledgeBaseID: req.KnowledgeBaseID, DocumentID: req.DocumentID,
		Revision: req.Revision, ContentHash: req.ContentHash, Anchor: anchor,
		ExtractionMethod: method, ObservedAt: observedAt,
	}
}
