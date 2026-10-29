package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"orrery_v2/internal/platform/db"
)

type AssertionSourceQuery struct {
	Kind, ID, Stream string
}

// ReadAssertionSources never repairs old metadata or mutates claim history.
// Missing/legacy/mismatched owned projections cannot attest a current claim.
// Its caller must authorize the graph assertions before submitting locators.
func (s *Store) ReadAssertionSources(ctx context.Context, tenant string, assertions []AssertionSourceQuery) (map[string]Source, error) {
	result := map[string]Source{}
	if s == nil || s.Core == nil || tenant == "" || len(assertions) > 1100 {
		return nil, ErrInvalid
	}
	err := s.Core.WithTx(ctx, func(tx db.Tx) error {
		for _, a := range assertions {
			if (a.Kind != "fact" && a.Kind != "link") || a.ID == "" || a.Stream == "" {
				return ErrInvalid
			}
			var watch, raw string
			err := tx.QueryRowContext(ctx, `SELECT watch_id,body_json FROM knowledge_instance_claims WHERE tenant_id=? AND id=?`, tenant, a.ID).Scan(&watch, &raw)
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			var claim Claim
			if err := json.Unmarshal([]byte(raw), &claim); err != nil {
				return err
			}
			if claim.ID != a.ID || a.Stream != "knowledge-document:"+tenant+":"+watch || claim.Source == nil || claim.Source.TenantID != tenant || (claim.TargetID != "") != (a.Kind == "link") {
				continue
			}
			table := "context_graph_facts"
			if a.Kind == "link" {
				table = "context_graph_links"
			}
			current, err := projectionHash(ctx, tx, table, tenant, a.ID, a.Stream)
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			if current != claim.ProjectionHash {
				continue
			}
			source := *claim.Source
			source.Content = ""
			if source.RootID == "" || source.DocumentID == "" || source.Revision < 1 || source.ContentHash == "" {
				continue
			}
			result[a.Kind+":"+a.ID] = source
		}
		return nil
	})
	return result, err
}
