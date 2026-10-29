package ontologyreasoning

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/contextgraph"
)

func TestInferenceDistinguishesDocumentRelationMeaningsAndWithdraws(t *testing.T) {
	pack, _ := inferenceFixture()
	pack.NeighborLinks[0].LinkType = "knowledge_entity_related_to"
	pack.NeighborLinks[0].Properties = map[string]any{"relation": "is a subsidiary of", "relation_code": "subsidiary_of"}
	pack.AllowedLinkTypes[0].ID = "knowledge_entity_related_to"
	rules := []Rule{{ID: "subsidiary", Version: "1", When: []Atom{{Kind: "link", Type: "knowledge_entity_related_to", RelationCode: "subsidiary_of", Subject: "?a", Object: "?b"}}, Then: Atom{Kind: "fact", Subject: "?b", Predicate: "risk", Value: true}}}
	service := NewService(ServiceOptions{Store: NewMemoryStore()})
	first, err := service.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || len(first.Candidates) != 1 {
		t.Fatalf("subsidiary rule: %+v %v", first, err)
	}
	pack.NeighborLinks[0].Properties["relation_code"] = "supplies"
	pack.NeighborLinks[0].Properties["relation"] = "supplies components to"
	record := evaluationRecord(rules)
	record.PreviousRunID = first.ID
	second, err := service.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(second.Candidates) != 0 || len(second.Request.Evaluation.WithdrawnIDs) != 1 {
		t.Fatalf("supplier must withdraw subsidiary conclusion: %+v %v", second, err)
	}
	// A broad link rule still deliberately matches any meaning.
	rules[0].When[0].RelationCode = ""
	third, err := service.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || len(third.Candidates) != 1 {
		t.Fatalf("broad relation rule: %+v %v", third, err)
	}
	// Legacy text is evidence, never silently upgraded to a semantic code.
	delete(pack.NeighborLinks[0].Properties, "relation_code")
	pack.NeighborLinks[0].Properties["relation"] = "subsidiary_of"
	rules[0].When[0].RelationCode = "subsidiary_of"
	legacy, err := service.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || len(legacy.Candidates) != 0 {
		t.Fatalf("legacy relation text was promoted: %+v %v", legacy, err)
	}
}

func TestInferenceRejectsUngovernedSemanticTerms(t *testing.T) {
	base := Atom{Kind: "link", Type: "knowledge_entity_related_to", RelationCode: "owns", Subject: "?a", Object: "?b"}
	for _, test := range []struct {
		name string
		atom Atom
		head bool
	}{
		{"unknown", Atom{Kind: "link", Type: base.Type, RelationCode: "invented", Subject: "a", Object: "b"}, false},
		{"other-type", Atom{Kind: "link", Type: "supports", RelationCode: "owns", Subject: "a", Object: "b"}, false},
		{"fact", Atom{Kind: "fact", RelationCode: "owns", Subject: "a", Predicate: "risk", Value: true}, false},
		{"conclusion", base, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !errors.Is(validateAtom(test.atom, test.head), ErrInvalidRequest) {
				t.Fatal("invalid semantic term accepted")
			}
		})
	}
}

func TestInferenceWithholdsConflictsWithdrawsAndRecovers(t *testing.T) {
	pack, rules := inferenceFixture()
	service := NewService(ServiceOptions{Store: NewMemoryStore()})
	first, err := service.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || len(first.Candidates) != 1 {
		t.Fatalf("initial: %+v %v", first, err)
	}
	pack.Facts[1].Value = "on_time"
	rules = append(rules, Rule{ID: "on-time", Version: "1", When: []Atom{{Kind: "fact", Subject: "?p", Predicate: "status", Value: "on_time"}, {Kind: "link", Type: "supports", Subject: "?p", Object: "?m"}}, Then: Atom{Kind: "fact", Subject: "?m", Predicate: "risk", Value: false}})
	record := evaluationRecord(rules)
	record.PreviousRunID = first.ID
	conflict, err := service.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(conflict.Candidates) != 0 || len(conflict.Request.Evaluation.WithdrawnIDs) != 1 {
		t.Fatalf("conflict allowed definite conclusions: %+v %v", conflict, err)
	}
	groups := conflict.Request.Evaluation.FactConflicts
	if len(groups) != 1 || groups[0].Status != "disputed" || len(groups[0].AssertionIDs) != 2 || len(conflict.ContextPack.Facts) != 2 {
		t.Fatalf("conflict evidence lost: %+v", conflict)
	}
	// A source revision can restore agreement without deleting historical runs.
	pack.Facts[1].Value = "delayed"
	record.PreviousRunID = conflict.ID
	recovered, err := service.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(recovered.Candidates) != 1 || len(recovered.Request.Evaluation.FactConflicts) != 0 || recovered.Candidates[0].ID != first.Candidates[0].ID {
		t.Fatalf("agreement failed to recover: %+v %v", recovered, err)
	}
	if len(recovered.Request.Evaluation.Closure.Conclusions[0].Derivations) != 2 {
		t.Fatal("independent equal supports collapsed")
	}
	old, _ := service.GetRun(context.Background(), pack.TenantID, conflict.ID)
	if len(old.Request.Evaluation.FactConflicts) != 1 {
		t.Fatal("historical conflict was mutated")
	}
}

func TestInferenceHonorsKnowledgeConflictMarkerAndIgnoresInactiveValues(t *testing.T) {
	pack, _ := inferenceFixture()
	pack.CandidateObjects[0].ObjectType = "knowledge_entity"
	pack.CandidateObjects[0].Properties = map[string]any{"conflicting_properties": []any{"status"}}
	pack.Facts = pack.Facts[:1]
	premises, _, err := premisesFromPack(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range premises {
		if p.Atom.Kind == "fact" {
			t.Fatal("known conflict became a premise from a partial view")
		}
	}
	delete(pack.CandidateObjects[0].Properties, "conflicting_properties")
	pack.Facts = append(pack.Facts, contextgraph.Fact{ID: "old", TenantID: pack.TenantID, Subject: pack.Facts[0].Subject, Predicate: "status", Value: "on_time", Status: "withdrawn"})
	_, groups := conflictedFacts(pack)
	if len(groups) != 0 {
		t.Fatalf("inactive values caused conflict: %+v", groups)
	}
}
