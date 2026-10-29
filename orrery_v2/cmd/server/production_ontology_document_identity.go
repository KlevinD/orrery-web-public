package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
)

func (w *productionOntologyDocumentInference) candidates(ctx context.Context, current documentInputContext, previous []knowledgeinstances.Claim) ([]knowledgeinstances.Entity, error) {
	set, err := w.recallIdentityCandidates(ctx, current, knowledgeinstances.RecallQuery{Previous: previous})
	return set.Entities, err
}

func (w *productionOntologyDocumentInference) recallIdentityCandidates(ctx context.Context, current documentInputContext, query knowledgeinstances.RecallQuery) (knowledgeinstances.CandidateSet, error) {
	set, err := w.store.RecallCandidates(ctx, current.source.TenantID, current.source.Access, query)
	if err != nil {
		return set, err
	}
	required, visible := map[string]bool{}, map[string]bool{}
	for _, c := range query.Previous {
		required[c.EntityID] = true
		if c.TargetID != "" {
			required[c.TargetID] = true
		}
	}
	var result []knowledgeinstances.Entity
	contextBytes := 0
	for _, item := range set.Entities {
		view, err := w.api.context.GetObject(ctx, current.p, current.source.TenantID, item.ID)
		if err != nil {
			set.Summary.Truncated = true
			continue
		}
		kind, ok := view.Properties["entity_kind"].(string)
		if !ok || kind != item.Kind {
			set.Summary.Truncated = true
			continue
		}
		// No raw index metadata, stale aliases, masked fields or adjudication
		// reasons may bypass the normal graph read policy into either model call.
		item.Properties = view.Properties
		item.Name = view.Label
		item.Key = ""
		item.IdentityKeys = knowledgeinstances.VisibleIdentityKeys(view.Properties)
		item.ProjectionHash = ""
		item.Conflicts = nil
		item.Resolutions = nil
		if !required[item.ID] && !knowledgeinstances.MatchesVisibleCandidate(query, item) {
			set.Summary.Truncated = true
			continue
		}
		encoded, _ := json.Marshal(item)
		if contextBytes+len(encoded) > 64*1024 {
			if required[item.ID] {
				return set, fmt.Errorf("%w: previous identity context exceeds model budget", knowledgeinstances.ErrInvalid)
			}
			set.Summary.Truncated = true
			set.Summary.Reasons = append(set.Summary.Reasons, "policy-filtered model context budget exceeded")
			continue
		}
		contextBytes += len(encoded)
		result = append(result, item)
		visible[item.ID] = true
	}
	for id := range required {
		if !visible[id] {
			return set, ontologyscope.ErrAccessDenied
		}
	}
	set.Entities = result
	if set.Summary.Truncated && len(set.Summary.Reasons) == 0 {
		// Do not disclose hidden IDs or which policy caused a candidate to be
		// omitted. An incomplete visible search cannot justify new identity.
		set.Summary.Reasons = []string{"candidate context was incomplete"}
	}
	set.Summary.CandidateIDs = nil
	for _, e := range result {
		set.Summary.CandidateIDs = append(set.Summary.CandidateIDs, e.ID)
	}
	return set, nil
}

func (w *productionOntologyDocumentInference) extractWithIdentityRecall(ctx context.Context, current documentInputContext, previous []knowledgeinstances.Claim) (knowledgeinstances.Extraction, []knowledgeinstances.Entity, knowledgeinstances.RecallSummary, error) {
	// Previous assertions enter the first model prompt. Check their mandatory
	// endpoints before exposing them, including a changed document access scope.
	// Candidate visibility is checked again after model latency during recall.
	if len(previous) > 0 {
		if _, err := w.candidates(ctx, current, previous); err != nil {
			return knowledgeinstances.Extraction{}, nil, knowledgeinstances.RecallSummary{}, err
		}
	}
	input := documentExtractionInput{Phase: "mentions", Source: current.source, Previous: previous, Attributes: knowledgeinstances.AttributeTypes, RelationCodes: ontology.KnowledgeRelationCodes()}
	draft, err := w.model.Extract(ctx, input)
	if err != nil {
		return draft, nil, knowledgeinstances.RecallSummary{}, err
	}
	if err := knowledgeinstances.ValidateMentions(current.source, draft); err != nil {
		return draft, nil, knowledgeinstances.RecallSummary{}, err
	}
	set, err := w.recallIdentityCandidates(ctx, current, knowledgeinstances.RecallQuery{Mentions: draft.Entities, Previous: previous})
	if err != nil || draft.Defer || len(draft.Entities) == 0 {
		return draft, set.Entities, set.Summary, err
	}
	input.Phase, input.Mentions, input.Candidates, input.Recall = "resolve", draft.Entities, set.Entities, &set.Summary
	input.ExtractedRelations = draft.Relations
	resolved, err := w.model.Extract(ctx, input)
	if err != nil {
		return resolved, set.Entities, set.Summary, err
	}
	if !resolved.Defer {
		if len(resolved.Entities) != len(draft.Entities) || knowledgeinstances.Digest(resolved.Relations) != knowledgeinstances.Digest(draft.Relations) {
			return resolved, set.Entities, set.Summary, fmt.Errorf("%w: identity resolution changed the extracted material", knowledgeinstances.ErrInvalid)
		}
		for i, e := range resolved.Entities {
			e.ExistingID = ""
			if knowledgeinstances.Digest(e) != knowledgeinstances.Digest(draft.Entities[i]) {
				return resolved, set.Entities, set.Summary, fmt.Errorf("%w: identity resolution changed an unsearched mention", knowledgeinstances.ErrInvalid)
			}
		}
		if set.Summary.Truncated && slices.ContainsFunc(resolved.Entities, func(e knowledgeinstances.EntityInput) bool { return e.ExistingID == "" }) {
			resolved = knowledgeinstances.Extraction{Defer: true, Reason: "Candidate recall was incomplete. Creating a new identity cannot be justified by an incomplete search."}
		}
	}
	return resolved, set.Entities, set.Summary, nil
}
