package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ConflictCase contains the complete set of live, source-owned assertions for
// one disputed attribute AFTER the proposed document replacement.
type ConflictCase struct {
	EntityID   string        `json:"entity_id"`
	Predicate  string        `json:"predicate"`
	Assertions []SourceClaim `json:"assertions"`
}

type SourceClaim struct {
	WatchID string `json:"watch_id"`
	Claim   Claim  `json:"claim"`
}

// Choices identify offered evidence, never an arbitrary model-generated value.
type ResolutionChoice struct {
	EntityID  string `json:"entity_id"`
	Predicate string `json:"predicate"`
	Decision  string `json:"decision"`
	ClaimID   string `json:"claim_id,omitempty"`
	Reason    string `json:"reason"`
}

type ResolutionRecord struct {
	ResolutionChoice
	BusinessValidFrom  time.Time  `json:"business_valid_from,omitempty"`
	BusinessValidTo    *time.Time `json:"business_valid_to,omitempty"`
	BatchID            string     `json:"batch_id"`
	ModelID            string     `json:"model_id"`
	InputHash          string     `json:"input_hash"`
	SupportingClaimIDs []string   `json:"supporting_claim_ids"`
}

type claimQuery func(context.Context, string, ...any) (*sql.Rows, error)

func ownedClaims(ctx context.Context, query claimQuery, tenant string, before map[string]int64) ([]SourceClaim, error) {
	ids := make([]string, 0, len(before))
	for id := range before {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result []SourceClaim
	for _, id := range ids {
		rows, err := query(ctx, `SELECT watch_id,body_json FROM knowledge_instance_claims WHERE tenant_id=? AND entity_id=? ORDER BY id LIMIT 2001`, tenant, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c SourceClaim
			var raw string
			if err := rows.Scan(&c.WatchID, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal([]byte(raw), &c.Claim); err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, c)
			if len(result) > 2000 {
				rows.Close()
				return nil, fmt.Errorf("%w: source assertion budget exceeded", ErrInvalid)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func replacementClaims(p Plan, old []SourceClaim) []SourceClaim {
	var result []SourceClaim
	for _, c := range old {
		if c.WatchID != p.Watch.ID {
			result = append(result, c)
		}
	}
	for _, c := range p.Claims {
		result = append(result, SourceClaim{WatchID: p.Watch.ID, Claim: c})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Claim.ID < result[j].Claim.ID })
	return result
}

func conflictCases(claims []SourceClaim) []ConflictCase {
	groups := map[string]*ConflictCase{}
	values := map[string]map[string]bool{}
	for _, c := range claims {
		if c.Claim.TargetID != "" {
			continue
		}
		key := c.Claim.EntityID + ":" + c.Claim.Predicate
		if groups[key] == nil {
			groups[key] = &ConflictCase{EntityID: c.Claim.EntityID, Predicate: c.Claim.Predicate}
			values[key] = map[string]bool{}
		}
		groups[key].Assertions = append(groups[key].Assertions, c)
		values[key][Digest(c.Claim.Value)] = true
	}
	var result []ConflictCase
	for key, group := range groups {
		if len(values[key]) > 1 {
			sort.Slice(group.Assertions, func(i, j int) bool { return group.Assertions[i].Claim.ID < group.Assertions[j].Claim.ID })
			result = append(result, *group)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EntityID != result[j].EntityID {
			return result[i].EntityID < result[j].EntityID
		}
		return result[i].Predicate < result[j].Predicate
	})
	return result
}

// PreviewConflicts freezes the entire affected claim set, even claims hidden
// from the model. The host must policy-filter each complete case before review.
// Oversized cases stay disputed and cannot be partially offered to the model.
func (s *Store) PreviewConflicts(ctx context.Context, p Plan) (Plan, error) {
	old, err := ownedClaims(ctx, s.Core.Query, p.Source.TenantID, p.Before)
	if err != nil {
		return p, err
	}
	p.ClaimSetHash = Digest(old)
	cases := conflictCases(activeSourceClaimsAt(replacementClaims(p, old), p.CreatedAt))
	sources := map[string]*Source{}
	p.Conflicts = nil
	for _, c := range cases {
		if len(c.Assertions) > 40 || len(p.Conflicts) >= 40 {
			continue
		}
		complete := true
		for i := range c.Assertions {
			a := &c.Assertions[i]
			if a.Claim.Source != nil {
				continue
			}
			// Upgrade legacy claims from their last applied immutable batch.
			if _, ok := sources[a.WatchID]; !ok {
				var raw string
				err := s.Core.QueryRow(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND watch_id=? AND status='applied' ORDER BY created_at DESC,id DESC LIMIT 1`, p.Source.TenantID, a.WatchID).Scan(&raw)
				if err == sql.ErrNoRows {
					sources[a.WatchID] = nil
					complete = false
					continue
				}
				if err != nil {
					return p, err
				}
				var b Batch
				if err := json.Unmarshal([]byte(raw), &b); err != nil {
					return p, err
				}
				source := b.Plan.Source
				source.Content = ""
				sources[a.WatchID] = &source
			}
			a.Claim.Source = sources[a.WatchID]
			complete = complete && a.Claim.Source != nil
		}
		if complete && len(body(append(p.Conflicts, c))) <= 64*1024 {
			p.Conflicts = append(p.Conflicts, c)
		}
	}
	return p, nil
}

// SealPlan runs after policy filtering, so persisted IDs cover the exact input.
func SealPlan(p Plan) Plan {
	p.ID = ""
	p.ID = stableID("knowledge-plan:", p)
	return p
}
