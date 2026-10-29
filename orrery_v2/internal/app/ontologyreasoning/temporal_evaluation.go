package ontologyreasoning

import (
	"time"

	"orrery_v2/internal/app/contextgraph"
)

// Time filtering precedes conflicts and joins: conjunction needs every live
// premise, while independently supported derivations can survive separately.
func temporalEvaluationPack(pack ContextPack, record EvaluationRecord) (ContextPack, *time.Time) {
	business, known := record.BusinessAsOf, record.KnownAt
	pack = cloneContextPack(pack)
	pack.RetractionEndpoints = nil
	if !record.Historical {
		for _, o := range pack.CandidateObjects {
			pack.RetractionEndpoints = append(pack.RetractionEndpoints, ObjectRef{ID: o.ID, Type: o.ObjectType})
		}
	}
	var next *time.Time
	eligible := func(t contextgraph.TemporalMetadata) bool {
		for _, at := range []*time.Time{&t.BusinessValidFrom, t.BusinessValidTo} {
			if at != nil && at.After(business) && (next == nil || at.Before(*next)) {
				copy := *at
				next = &copy
			}
		}
		return contextgraph.TemporalEligible(t, business, known)
	}
	var objects []contextgraph.Object
	ids := map[string]bool{}
	for _, o := range pack.CandidateObjects {
		if eligible(o.Temporal) {
			objects = append(objects, o)
			ids[o.ID] = true
		}
	}
	var links []contextgraph.Link
	for _, l := range pack.NeighborLinks {
		if eligible(l.Temporal) && ids[l.FromObjectID] && ids[l.ToObjectID] {
			links = append(links, l)
		}
	}
	var facts []contextgraph.Fact
	for _, f := range pack.Facts {
		f.Status = contextgraph.AssertionStatusAt(f, business)
		if eligible(f.Temporal) && ids[f.Subject.ID] {
			facts = append(facts, f)
		}
	}
	if record.RootID != "" {
		reachable := map[string]bool{record.RootID: true}
		for depth := 0; depth < 2; depth++ {
			next := map[string]bool{}
			for _, l := range links {
				if !assertionEligible(l.Status) {
					continue
				}
				if reachable[l.FromObjectID] {
					next[l.ToObjectID] = true
				}
				if reachable[l.ToObjectID] {
					next[l.FromObjectID] = true
				}
			}
			for id := range next {
				reachable[id] = true
			}
		}
		selectedObjects := objects[:0]
		for _, o := range objects {
			if reachable[o.ID] {
				selectedObjects = append(selectedObjects, o)
			}
		}
		objects = selectedObjects
		selectedFacts := facts[:0]
		for _, f := range facts {
			if reachable[f.Subject.ID] {
				selectedFacts = append(selectedFacts, f)
			}
		}
		facts = selectedFacts
		selectedLinks := links[:0]
		for _, l := range links {
			if reachable[l.FromObjectID] && reachable[l.ToObjectID] {
				selectedLinks = append(selectedLinks, l)
			}
		}
		links = selectedLinks
	}
	// Aggregated knowledge-entity properties are a display projection, never
	// another source assertion. Rebuild them from live accepted facts so both
	// historical validation and current dependencies see the selected values.
	for i := range objects {
		if objects[i].ObjectType != "knowledge_entity" {
			continue
		}
		objects[i].Properties = map[string]any{}
		values := map[string]map[string]any{}
		for _, f := range facts {
			if f.Subject.ID != objects[i].ID || !assertionEligible(f.Status) || f.Predicate == "identity_keys" {
				continue
			}
			if values[f.Predicate] == nil {
				values[f.Predicate] = map[string]any{}
			}
			values[f.Predicate][stableID("value:", f.Value)] = f.Value
		}
		for p, distinct := range values {
			if len(distinct) == 1 {
				for _, value := range distinct {
					objects[i].Properties[p] = value
				}
			}
		}
		if name, ok := objects[i].Properties["name"].(string); ok {
			objects[i].Label = name
		} else {
			objects[i].Label = objects[i].ID
		}
		objects[i].Status = "withdrawn"
		if len(objects[i].Properties) > 0 {
			objects[i].Status = "active"
		}
	}
	if record.Historical {
		selected := objects[:0]
		for _, o := range objects {
			if o.ObjectType == "knowledge_entity" && len(o.Properties) == 0 {
				continue
			}
			selected = append(selected, o)
		}
		objects = selected
		ids = map[string]bool{}
		for _, o := range objects {
			ids[o.ID] = true
		}
		selectedFacts := facts[:0]
		for _, f := range facts {
			if ids[f.Subject.ID] {
				selectedFacts = append(selectedFacts, f)
			}
		}
		facts = selectedFacts
		selectedLinks := links[:0]
		for _, l := range links {
			if ids[l.FromObjectID] && ids[l.ToObjectID] {
				selectedLinks = append(selectedLinks, l)
			}
		}
		links = selectedLinks
	}
	pack.CandidateObjects, pack.NeighborLinks, pack.Facts = objects, links, facts
	return pack, next
}
