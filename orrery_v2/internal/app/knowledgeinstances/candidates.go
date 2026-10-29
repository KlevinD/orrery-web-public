package knowledgeinstances

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
)

const MaxIdentityCandidates = 200

type RecallQuery struct {
	Mentions []EntityInput
	Previous []Claim
}

type RecallSummary struct {
	QueryHash    string   `json:"query_hash"`
	Truncated    bool     `json:"truncated"`
	Reasons      []string `json:"reasons,omitempty"`
	CandidateIDs []string `json:"candidate_ids"`
}

type CandidateSet struct {
	Entities []Entity
	Summary  RecallSummary
}

func mentionTerms(mentions []EntityInput) identityTerms {
	t := newIdentityTerms()
	for _, input := range mentions {
		t.label(input.Name)
		for _, p := range input.Properties {
			if p.Predicate == "aliases" {
				for _, alias := range stringValues(p.Value) {
					t.label(alias)
				}
			}
			if p.Predicate == "registration_id" {
				if value, ok := p.Value.(string); ok {
					t.add("identifier", value)
				}
			}
		}
		for _, key := range input.Identifiers {
			if normalized, err := normalizeIdentifier(key.Identifier); err == nil {
				t.add("identifier", normalized.Value)
				t.add("strong", Digest(normalized))
			}
		}
	}
	return t
}

// MatchesVisibleCandidate is applied again AFTER object/field policy filtering.
// A hidden alias or registration may not become a model-visible recall signal.
func MatchesVisibleCandidate(query RecallQuery, e Entity) bool {
	for _, input := range query.Mentions {
		if input.Kind != e.Kind {
			continue
		}
		wanted, available := mentionTerms([]EntityInput{input}), entityTerms(e)
		for kind, values := range wanted {
			for value := range values {
				if available[kind][value] {
					return true
				}
			}
		}
	}
	return false
}

// RecallCandidates selects bounded, relevant candidates inside one exact access
// boundary. Historical entities remain searchable without exhausting a global
// listing budget. Every previous endpoint is mandatory, even without a name hit.
func (s *Store) RecallCandidates(ctx context.Context, tenant string, access contextgraph.AccessRef, query RecallQuery) (CandidateSet, error) {
	result := CandidateSet{Summary: RecallSummary{QueryHash: Digest(query)}}
	if tenant == "" || access.Kind == "" || access.ID == "" || access.TenantID == "" || len(query.Mentions) > 40 || len(query.Previous) > 2000 {
		return result, ErrInvalid
	}
	kinds := map[string]bool{}
	for _, input := range query.Mentions {
		if len(input.Name) > 256 || len(input.Properties) > 20 || len(input.Identifiers) > 4 || !slices.Contains([]string{"company", "person", "contract", "product", "project", "other"}, input.Kind) {
			return result, ErrInvalid
		}
		kinds[input.Kind] = true
	}
	if err := s.repairIdentityIndex(ctx, tenant, access); err != nil {
		return result, err
	}
	required := map[string]bool{}
	for _, c := range query.Previous {
		if c.EntityID != "" {
			required[c.EntityID] = true
		}
		if c.TargetID != "" {
			required[c.TargetID] = true
		}
	}
	if len(required) > MaxIdentityCandidates {
		return result, fmt.Errorf("%w: previous identity endpoint budget exceeded", ErrInvalid)
	}
	ids := make([]string, 0, len(required))
	for id := range required {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seen := map[string]bool{}
	for _, id := range ids {
		var raw string
		if err := s.Core.QueryRow(ctx, `SELECT body_json FROM knowledge_instances WHERE tenant_id=? AND access_hash=? AND id=?`, tenant, Digest(access), id).Scan(&raw); err != nil {
			return result, ErrConflict
		}
		var e Entity
		if err := json.Unmarshal([]byte(raw), &e); err != nil || e.TenantID != tenant || e.Access != access {
			return result, ErrConflict
		}
		result.Entities = append(result.Entities, e)
		seen[e.ID] = true
	}
	terms := mentionTerms(query.Mentions)
	var clauses []string
	args := []any{tenant, Digest(access)}
	kindValues := make([]string, 0, len(kinds))
	for kind := range kinds {
		kindValues = append(kindValues, kind)
	}
	sort.Strings(kindValues)
	for _, kind := range kindValues {
		args = append(args, kind)
	}
	kindFilter := strings.TrimSuffix(strings.Repeat("?,", len(kindValues)), ",")
	for _, kind := range []string{"strong", "identifier", "label", "token"} {
		values := make([]string, 0, len(terms[kind]))
		for term := range terms[kind] {
			values = append(values, term)
		}
		sort.Strings(values)
		if len(values) > 160 {
			values = values[:160]
			result.Summary.Truncated = true
			result.Summary.Reasons = append(result.Summary.Reasons, "recall hint budget exceeded")
		}
		if len(values) == 0 {
			continue
		}
		clauses = append(clauses, "(t.term_kind=? AND t.term IN ("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+"))")
		args = append(args, kind)
		for _, value := range values {
			args = append(args, value)
		}
	}
	if len(clauses) > 0 {
		args = append(args, MaxIdentityCandidates+1)
		rows, err := s.Core.Query(ctx, `SELECT e.body_json,SUM(CASE t.term_kind WHEN 'strong' THEN 5000 WHEN 'identifier' THEN 2000 WHEN 'label' THEN 1000 ELSE 10 END) AS score FROM knowledge_instance_identity_terms t JOIN knowledge_instances e ON e.tenant_id=t.tenant_id AND e.id=t.entity_id AND e.access_hash=t.access_hash WHERE t.tenant_id=? AND t.access_hash=? AND t.kind IN (`+kindFilter+`) AND (`+strings.Join(clauses, " OR ")+`) GROUP BY e.id,e.body_json ORDER BY score DESC,e.id LIMIT ?`, args...)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var raw string
			var score int64
			var e Entity
			if err := rows.Scan(&raw, &score); err != nil {
				rows.Close()
				return result, err
			}
			if err := json.Unmarshal([]byte(raw), &e); err != nil || e.TenantID != tenant || e.Access != access {
				rows.Close()
				return result, ErrConflict
			}
			if seen[e.ID] {
				continue
			}
			if len(result.Entities) == MaxIdentityCandidates {
				result.Summary.Truncated = true
				result.Summary.Reasons = append(result.Summary.Reasons, "relevant candidate budget exceeded")
				break
			}
			result.Entities = append(result.Entities, e)
			seen[e.ID] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	for _, e := range result.Entities {
		result.Summary.CandidateIDs = append(result.Summary.CandidateIDs, e.ID)
	}
	result.Summary.Reasons = slices.Compact(result.Summary.Reasons)
	return result, nil
}
