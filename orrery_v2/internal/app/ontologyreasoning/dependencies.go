package ontologyreasoning

import (
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

// Dependency includes both positive proof inputs and validation constraints.
// Empty constraint sets are recorded too, so a new competing assertion changes
// the fingerprint. Raw tenant-wide source stamps are only transaction fences.
type Dependency struct {
	Ref  string `json:"ref"`
	Hash string `json:"hash"`
}

// CandidateDependencyValues is also the model's input: an approval cannot rely
// on surrounding context that was excluded from its support fingerprint.
func CandidateDependencyValues(run ReasoningRun, candidateID string) map[string]any {
	values := map[string]any{"scope": []string{run.TenantID, candidateID, InferenceSemanticsVersion}}
	e := run.Request.Evaluation
	if e == nil {
		return values
	}
	premises := map[string]Premise{}
	conclusions := map[string]Conclusion{}
	for _, p := range e.Closure.Premises {
		premises[p.ID] = p
	}
	for _, c := range e.Closure.Conclusions {
		conclusions[c.ID] = c
	}
	if _, ok := conclusions[candidateID]; !ok {
		// Submitted document claims retain the conservative input dependency
		// until their source revisions can be attested by the host.
		values["submitted_document_input"] = run.InputHash
		return values
	}
	objects, rules, statements := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var atoms []Atom
	var visit func(string)
	visit = func(id string) {
		if statements[id] {
			return
		}
		statements[id] = true
		if p, ok := premises[id]; ok {
			values[p.AssertionRef] = p
			atoms = append(atoms, p.Atom)
			return
		}
		c, ok := conclusions[id]
		if !ok {
			return
		}
		values["conclusion:"+id] = c
		atoms = append(atoms, c.Atom)
		for _, proof := range c.Derivations {
			rules[proof.RuleID] = true
			for _, p := range proof.PremiseIDs {
				visit(p)
			}
		}
	}
	visit(candidateID)
	for _, rule := range e.Rules {
		if rules[rule.ID] {
			values["rule:"+rule.ID] = rule
		}
	}
	for _, a := range atoms {
		objects[a.Subject] = true
		if a.Object != "" {
			objects[a.Object] = true
		}
	}
	pack := run.ContextPack
	objectTypes := map[string]string{}
	evidence := map[string]bool{}
	for _, object := range pack.CandidateObjects {
		if !objects[object.ID] {
			continue
		}
		objectTypes[object.ID] = object.ObjectType
		values["object:"+object.ID] = object
		for _, ref := range object.EvidenceRefs {
			evidence[ref.ID] = true
		}
		for _, definition := range pack.AllowedObjectTypes {
			if definition.ID == object.ObjectType {
				values["schema:object:"+definition.ID] = definition
			}
		}
	}
	for _, atom := range atoms {
		switch atom.Kind {
		case "link":
			for _, definition := range pack.AllowedLinkTypes {
				if definition.ID != atom.Type {
					continue
				}
				values["schema:link:"+definition.ID] = definition
				var conflicts []contextgraph.Link
				for _, link := range pack.NeighborLinks {
					if link.LinkType == atom.Type && ((definition.Cardinality != ontology.CardinalityManyToMany && (link.FromObjectID == atom.Subject || link.ToObjectID == atom.Object)) || statementsForAssertion(premises, statements, "link:"+link.ID)) {
						conflicts = append(conflicts, link)
						for _, ref := range link.EvidenceRefs {
							evidence[ref.ID] = true
						}
					}
				}
				values["constraint:links:"+atom.Type+":"+atom.Subject+":"+atom.Object] = conflicts
			}
		case "fact":
			key := objectTypes[atom.Subject] + "." + atom.Predicate
			for _, definition := range pack.AllowedPropertyTypes {
				if definition.ObjectTypeID+"."+definition.PropertyID == key {
					values["schema:property:"+key] = definition
				}
			}
			var facts []contextgraph.Fact
			for _, fact := range pack.Facts {
				if fact.Subject.ID == atom.Subject && fact.Predicate == atom.Predicate {
					facts = append(facts, fact)
					for _, ref := range fact.EvidenceRefs {
						evidence[ref.ID] = true
					}
				}
			}
			values["constraint:facts:"+atom.Subject+":"+atom.Predicate] = facts
		}
	}
	for _, item := range pack.Evidence {
		if evidence[item.ID] && !strings.HasPrefix(item.SourceRef.Kind, "ontology_inference") {
			values["evidence:"+item.ID] = item
		}
	}
	return values
}

func statementsForAssertion(premises map[string]Premise, used map[string]bool, ref string) bool {
	for id, p := range premises {
		if used[id] && p.AssertionRef == ref {
			return true
		}
	}
	return false
}
