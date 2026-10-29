package ontologyreasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/ontology"
)

// Atom is a positive, function-free statement. Object IDs are opaque and
// case-sensitive. Only subject/object positions accept variables ("?name").
type Atom struct {
	RelationCode string `json:"relation_code,omitempty"`
	Kind         string `json:"kind"`
	Type         string `json:"type,omitempty"`
	Subject      string `json:"subject"`
	Object       string `json:"object,omitempty"`
	Predicate    string `json:"predicate,omitempty"`
	Value        any    `json:"value,omitempty"`
}

type Rule struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	When    []Atom `json:"when"`
	Then    Atom   `json:"then"`
}

type Premise struct {
	ID           string `json:"id"`
	AssertionRef string `json:"assertion_ref"`
	Atom         Atom   `json:"atom"`
}

type Derivation struct {
	RuleID      string   `json:"rule_id"`
	RuleVersion string   `json:"rule_version"`
	PremiseIDs  []string `json:"premise_ids"`
}

type Conclusion struct {
	ID          string       `json:"id"`
	Atom        Atom         `json:"atom"`
	Derivations []Derivation `json:"derivations"`
}

type Closure struct {
	Premises    []Premise    `json:"premises"`
	Conclusions []Conclusion `json:"conclusions"`
	RuleHash    string       `json:"rule_hash"`
}

const maxRuleWork = 200000

// Include this protocol in observation and approval fences when premise
// interpretation changes, even if the source rows themselves have not changed.
const InferenceSemanticsVersion = "positive-rules-temporal-premises-v3"

func atomKey(a Atom) string { b, _ := json.Marshal(a); return string(b) }
func stableID(prefix string, value any) string {
	b, _ := json.Marshal(value)
	return prefix + hashString(string(b))
}
func variable(s string) bool { return strings.HasPrefix(s, "?") && len(s) > 1 }

func validateAtom(a Atom, head bool) error {
	if a.RelationCode != "" && (head || a.Kind != "link" || a.Type != "knowledge_entity_related_to" || !ontology.ValidKnowledgeRelationCode(a.RelationCode)) {
		return fmt.Errorf("%w: relation_code requires a registered knowledge relation premise", ErrInvalidRequest)
	}
	if a.Subject == "" || len(a.Subject) > 256 || len(a.Object) > 256 || len(a.Type) > 256 || len(a.Predicate) > 256 {
		return fmt.Errorf("%w: invalid rule term", ErrInvalidRequest)
	}
	switch a.Kind {
	case "object":
		if head || a.Type == "" || a.Object != "" || a.Predicate != "" || a.Value != nil {
			return fmt.Errorf("%w: invalid object atom", ErrInvalidRequest)
		}
	case "link":
		if a.Type == "" || a.Object == "" || a.Predicate != "" || a.Value != nil {
			return fmt.Errorf("%w: invalid link atom", ErrInvalidRequest)
		}
	case "fact":
		if a.Predicate == "" || a.Object != "" || a.Type != "" {
			return fmt.Errorf("%w: invalid fact atom", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported atom kind", ErrInvalidRequest)
	}
	if encoded, err := json.Marshal(a); err != nil || len(encoded) > 65536 {
		return fmt.Errorf("%w: non-JSON rule value", ErrInvalidRequest)
	}
	return nil
}

func ValidateRules(rules []Rule) error {
	if len(rules) > 32 {
		return fmt.Errorf("%w: supply at most 32 rules", ErrInvalidRequest)
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if r.ID == "" || r.Version == "" || len(r.ID) > 128 || len(r.Version) > 128 || seen[r.ID] || len(r.When) == 0 || len(r.When) > 6 {
			return fmt.Errorf("%w: invalid rule identity or body", ErrInvalidRequest)
		}
		seen[r.ID] = true
		bound := map[string]bool{}
		for _, a := range r.When {
			if err := validateAtom(a, false); err != nil {
				return err
			}
			for _, term := range []string{a.Subject, a.Object} {
				if variable(term) {
					bound[term] = true
				}
			}
		}
		if err := validateAtom(r.Then, true); err != nil {
			return err
		}
		for _, term := range []string{r.Then.Subject, r.Then.Object} {
			if variable(term) && !bound[term] {
				return fmt.Errorf("%w: unbound conclusion variable", ErrInvalidRequest)
			}
		}
	}
	return nil
}

// EvaluateRules recomputes the least fixed point from the supplied snapshot.
// Recomputing on withdrawal avoids retaining self-supported recursive cycles.
// Exceeding any budget fails the entire evaluation; partial closure is never returned.
func EvaluateRules(ctx context.Context, tenant string, rules []Rule, premises []Premise) (Closure, error) {
	if err := ctx.Err(); err != nil {
		return Closure{}, err
	}
	if tenant == "" || len(premises) > 2000 {
		return Closure{}, fmt.Errorf("%w: invalid inference scope", ErrInvalidRequest)
	}
	if err := ValidateRules(rules); err != nil {
		return Closure{}, err
	}
	rules = append([]Rule(nil), rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	result := Closure{RuleHash: stableID("rules:", rules)}
	type statement struct {
		id   string
		atom Atom
	}
	statements := map[string]statement{}
	premiseIDs := map[string]bool{}
	for _, p := range premises {
		if err := validateAtom(p.Atom, false); err != nil {
			return Closure{}, err
		}
		if variable(p.Atom.Subject) || variable(p.Atom.Object) || p.ID == "" || strings.HasPrefix(p.ID, "derived:") || premiseIDs[p.ID] {
			return Closure{}, fmt.Errorf("%w: invalid premise identity", ErrInvalidRequest)
		}
		premiseIDs[p.ID] = true
		result.Premises = append(result.Premises, p)
		statements[p.ID] = statement{p.ID, p.Atom}
	}
	conclusions := map[string]*Conclusion{}
	proofs := map[string]bool{}
	work := 0
	for changed := true; changed; {
		changed = false
		ordered := make([]statement, 0, len(statements))
		for _, s := range statements {
			ordered = append(ordered, s)
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
		for _, r := range rules {
			var match func(int, map[string]string, []string) error
			match = func(pos int, bindings map[string]string, ids []string) error {
				work++
				if work > maxRuleWork || len(statements) > 2500 || len(proofs) > 4000 {
					return fmt.Errorf("%w: inference budget exceeded", ErrValidationFailed)
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if pos == len(r.When) {
					a := r.Then
					if variable(a.Subject) {
						a.Subject = bindings[a.Subject]
					}
					if variable(a.Object) {
						a.Object = bindings[a.Object]
					}
					key := atomKey(a)
					id := stableID("derived:", []string{tenant, key})
					c := conclusions[id]
					if c == nil {
						if len(conclusions) >= maxReasoningCandidates {
							return fmt.Errorf("%w: conclusion budget exceeded", ErrValidationFailed)
						}
						c = &Conclusion{ID: id, Atom: a}
						conclusions[id] = c
						statements[id] = statement{id, a}
						changed = true
					}
					proof := Derivation{RuleID: r.ID, RuleVersion: r.Version, PremiseIDs: append([]string(nil), ids...)}
					proofKey := stableID("", struct {
						ID    string
						Proof Derivation
					}{id, proof})
					if !proofs[proofKey] {
						if len(proofs) >= 4000 {
							return fmt.Errorf("%w: derivation budget exceeded", ErrValidationFailed)
						}
						proofs[proofKey] = true
						c.Derivations = append(c.Derivations, proof)
					}
					return nil
				}
				pattern := r.When[pos]
				for _, s := range ordered {
					work++
					if work > maxRuleWork {
						return fmt.Errorf("%w: inference budget exceeded", ErrValidationFailed)
					}
					if pattern.Kind != s.atom.Kind || pattern.Type != s.atom.Type || pattern.Predicate != s.atom.Predicate || atomValueKey(pattern.Value) != atomValueKey(s.atom.Value) || (pattern.RelationCode != "" && pattern.RelationCode != s.atom.RelationCode) {
						continue
					}
					next := make(map[string]string, len(bindings)+2)
					for k, v := range bindings {
						next[k] = v
					}
					bind := func(term, value string) bool {
						if !variable(term) {
							return term == value
						}
						if old, ok := next[term]; ok {
							return old == value
						}
						next[term] = value
						return true
					}
					if bind(pattern.Subject, s.atom.Subject) && bind(pattern.Object, s.atom.Object) {
						if err := match(pos+1, next, append(append([]string(nil), ids...), s.id)); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if err := match(0, map[string]string{}, nil); err != nil {
				return Closure{}, err
			}
		}
	}
	for _, c := range conclusions {
		sort.Slice(c.Derivations, func(i, j int) bool { return stableID("", c.Derivations[i]) < stableID("", c.Derivations[j]) })
		result.Conclusions = append(result.Conclusions, *c)
	}
	sort.Slice(result.Premises, func(i, j int) bool { return result.Premises[i].ID < result.Premises[j].ID })
	sort.Slice(result.Conclusions, func(i, j int) bool { return result.Conclusions[i].ID < result.Conclusions[j].ID })
	return result, nil
}

func atomValueKey(value any) string { b, _ := json.Marshal(value); return string(b) }
