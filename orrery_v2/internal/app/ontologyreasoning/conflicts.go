package ontologyreasoning

import (
	"sort"

	"orrery_v2/internal/app/contextgraph"
)

// FactConflict records withheld input without choosing a winning source. The
// original assertions remain in ContextPack for authorized model inspection.
type FactConflict struct {
	SubjectID    string   `json:"subject_id"`
	Predicate    string   `json:"predicate"`
	AssertionIDs []string `json:"assertion_ids,omitempty"`
	Status       string   `json:"status"`
}

type factSlot struct{ subject, predicate string }

func conflictedFacts(pack ContextPack) (map[factSlot]bool, []FactConflict) {
	blocked := map[factSlot]bool{}
	values := map[factSlot]map[string]bool{}
	ids := map[factSlot][]string{}
	for _, object := range pack.CandidateObjects {
		// This property is produced by the knowledge_instances domain, not a
		// generic business field that may happen to share its name.
		if object.ObjectType != "knowledge_entity" {
			continue
		}
		mark := func(predicate string) { blocked[factSlot{object.ID, predicate}] = true }
		switch properties := object.Properties["conflicting_properties"].(type) {
		case []string:
			for _, p := range properties {
				mark(p)
			}
		case []any:
			for _, p := range properties {
				if name, ok := p.(string); ok {
					mark(name)
				}
			}
		}
	}
	for _, fact := range pack.Facts {
		if !assertionEligible(fact.Status) {
			continue
		}
		key := factSlot{fact.Subject.ID, fact.Predicate}
		if values[key] == nil {
			values[key] = map[string]bool{}
		}
		values[key][atomValueKey(fact.Value)] = true
		ids[key] = append(ids[key], fact.ID)
		if len(values[key]) > 1 {
			blocked[key] = true
		}
	}
	var conflicts []FactConflict
	for key := range blocked {
		sort.Strings(ids[key])
		conflicts = append(conflicts, FactConflict{SubjectID: key.subject, Predicate: key.predicate, AssertionIDs: ids[key], Status: "disputed"})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].SubjectID != conflicts[j].SubjectID {
			return conflicts[i].SubjectID < conflicts[j].SubjectID
		}
		return conflicts[i].Predicate < conflicts[j].Predicate
	})
	return blocked, conflicts
}

func relationCode(link contextgraph.Link) string {
	if link.LinkType != "knowledge_entity_related_to" {
		return ""
	}
	code, _ := link.Properties["relation_code"].(string)
	return code
}
