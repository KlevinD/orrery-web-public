package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
)

// Source reads and the fact read policy BOTH authorize competing assertions.
// An unreadable/stale case is omitted in full and remains disputed; raw SQL
// claim data is never sufficient authorization to put evidence in a prompt.
func (w *productionOntologyDocumentInference) conflictSources(ctx context.Context, current documentInputContext, p knowledgeinstances.Plan, cases []knowledgeinstances.ConflictCase, historical bool) ([]knowledgeinstances.Source, error) {
	sources := map[string]knowledgeinstances.Source{}
	facts := map[string]map[string]contextview.FactItem{}
	for _, c := range cases {
		for _, a := range c.Assertions {
			stamp := a.Claim.Source
			if stamp == nil || stamp.Access != p.Source.Access || stamp.TenantID != p.Source.TenantID {
				return nil, knowledgeinstances.ErrInvalid
			}
			if a.WatchID == p.Watch.ID && slices.ContainsFunc(p.Claims, func(claim knowledgeinstances.Claim) bool { return claim.ID == a.Claim.ID }) {
				if !historical && stamp.Stamp() != current.source.Stamp() {
					return nil, knowledgeinstances.ErrConflict
				}
				continue
			}
			if _, ok := sources[a.WatchID]; !ok {
				watch, err := w.watches.Get(ctx, p.Source.TenantID, a.WatchID)
				if err != nil {
					return nil, err
				}
				if watch.RootID != stamp.RootID {
					return nil, knowledgeinstances.ErrConflict
				}
				if historical {
					if _, err := w.api.context.GetObject(ctx, current.p, p.Source.TenantID, stamp.RootID); err != nil {
						return nil, err
					}
					sources[a.WatchID] = *stamp
				} else {
					source, err := w.sources.Load(ctx, current.p, watch)
					if err != nil {
						return nil, err
					}
					sources[a.WatchID] = source
				}
			}
			source := sources[a.WatchID]
			if !historical && (source.Stamp() != stamp.Stamp() || len(a.Claim.Quote) < 2 || !strings.Contains(source.Content, a.Claim.Quote)) {
				return nil, knowledgeinstances.ErrConflict
			}
			if _, ok := facts[c.EntityID]; !ok {
				facts[c.EntityID] = map[string]contextview.FactItem{}
				cursor := ""
				for page := 0; ; page++ {
					if page >= 20 {
						return nil, knowledgeinstances.ErrInvalid
					}
					visible, err := w.api.context.ListFacts(ctx, current.p, contextview.ChildQuery{TenantID: p.Source.TenantID, ObjectID: c.EntityID, Limit: 100, Cursor: cursor})
					if err != nil {
						return nil, err
					}
					for _, fact := range visible.Items {
						if !fact.Redacted {
							facts[c.EntityID][fact.ID] = fact
						}
					}
					if !visible.Page.HasMore {
						break
					}
					cursor = visible.Page.NextCursor
				}
			}
			fact, ok := facts[c.EntityID][a.Claim.ID]
			if !ok || fact.Predicate != a.Claim.Predicate || knowledgeinstances.Digest(fact.Value) != knowledgeinstances.Digest(a.Claim.Value) {
				return nil, ontologyscope.ErrAccessDenied
			}
		}
	}
	var result []knowledgeinstances.Source
	keys := make([]string, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		result = append(result, sources[key])
	}
	return result, nil
}

func (w *productionOntologyDocumentInference) prepareConflictReview(ctx context.Context, current documentInputContext, p knowledgeinstances.Plan) (knowledgeinstances.Plan, error) {
	p, err := w.store.PreviewConflicts(ctx, p)
	if err != nil {
		return p, err
	}
	var visible []knowledgeinstances.ConflictCase
	for _, c := range p.Conflicts {
		if _, err := w.conflictSources(ctx, current, p, []knowledgeinstances.ConflictCase{c}, false); err != nil {
			if conflictReviewUnavailable(err) {
				continue
			}
			return p, err
		}
		visible = append(visible, c)
	}
	p.Conflicts = visible
	return knowledgeinstances.SealPlan(p), nil
}

func conflictReviewUnavailable(err error) bool {
	return errors.Is(err, knowledgeinstances.ErrConflict) || errors.Is(err, knowledgeinstances.ErrNotFound) || errors.Is(err, ontologyscope.ErrAccessDenied) || errors.Is(err, contextview.ErrUnavailable)
}
