package knowledgeinstances

import (
	"context"
	"encoding/json"
	"orrery_v2/internal/platform/db"
	"sort"
)

// SourceCatalogTx recovers only sources with current claims. The host calls it
// under the same graph/bridge fence used by instance writes, once per binding.
func SourceCatalogTx(ctx context.Context, tx db.Tx, tenant, kb string) ([]Source, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.watch_id,c.body_json FROM knowledge_instance_claims c LEFT JOIN knowledge_instance_claim_sources s ON s.tenant_id=c.tenant_id AND s.claim_id=c.id WHERE c.tenant_id=? AND (s.knowledge_base_id=? OR s.claim_id IS NULL) ORDER BY c.watch_id,c.id`, tenant, kb)
	if err != nil {
		return nil, err
	}
	var claims []SourceClaim
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
		claims = append(claims, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	legacy := map[string]Source{}
	documents := map[string]Source{}
	for _, c := range claims {
		var source Source
		if c.Claim.Source != nil {
			source = *c.Claim.Source
		} else {
			var ok bool
			source, ok = legacy[c.WatchID]
			if !ok {
				var raw string
				if err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND watch_id=? AND status='applied' ORDER BY created_at DESC,id DESC LIMIT 1`, tenant, c.WatchID).Scan(&raw); err != nil {
					return nil, ErrConflict
				}
				var b Batch
				if err := json.Unmarshal([]byte(raw), &b); err != nil {
					return nil, err
				}
				source = b.Plan.Source
				legacy[c.WatchID] = source
			}
		}
		source.Content = ""
		if err := indexClaimSource(ctx, tx, tenant, c.Claim.ID, source); err != nil {
			return nil, err
		}
		if source.KnowledgeBaseID != kb {
			continue
		}
		old, ok := documents[source.DocumentID]
		if ok && (old.RootID != source.RootID || old.Access != source.Access) {
			return nil, ErrConflict
		}
		if !ok || source.Generation > old.Generation || (source.Generation == old.Generation && source.Revision > old.Revision) {
			documents[source.DocumentID] = source
		}
	}
	var result []Source
	for _, source := range documents {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DocumentID < result[j].DocumentID })
	return result, nil
}
