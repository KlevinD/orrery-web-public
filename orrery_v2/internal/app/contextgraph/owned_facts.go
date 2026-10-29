package contextgraph

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// DeleteFact removes the current assertion of exactly one source stream.
// Temporal resource versions and external action receipts remain historical.
func (s *PlatformStore) DeleteFact(ctx context.Context, tenant, id, stream string) error {
	if !s.ready() || strings.TrimSpace(tenant) == "" || strings.TrimSpace(id) == "" || strings.TrimSpace(stream) == "" {
		return fmt.Errorf("fact tenant, id and source stream are required")
	}
	if s.tx == nil {
		return s.store.WithTx(ctx, func(tx db.Tx) error { return NewPlatformStoreTx(tx).DeleteFact(ctx, tenant, id, stream) })
	}
	if _, err := s.exec(ctx, `DELETE FROM context_graph_fact_assertions WHERE tenant_id=? AND fact_id=? AND source_stream_id=?`, tenant, id, stream); err != nil {
		return err
	}
	_, err := s.exec(ctx, `DELETE FROM context_graph_facts WHERE tenant_id=? AND id=? AND source_stream_id=?`, tenant, id, stream)
	return err
}
