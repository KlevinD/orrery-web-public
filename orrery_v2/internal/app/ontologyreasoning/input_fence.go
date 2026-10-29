package ontologyreasoning

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

var inferenceInputTables = []string{"context_graph_objects", "context_graph_links", "context_graph_facts", "context_graph_fact_assertions", "context_graph_evidence", "ontology_object_types", "ontology_link_types", "ontology_property_types"}

// InputStamp is an internal, bounded digest. No unfiltered payload is returned
// to a caller or used as reasoning input.
func (s *LifecycleStore) InputStamp(ctx context.Context, tenant string) (string, error) {
	return inferenceInputStamp(ctx, func(q string, args ...any) (*sql.Rows, error) { return s.Store.Query(ctx, q, args...) }, tenant)
}

// FenceInput blocks source writes for the short commit transaction. SQLite's
// session write fence already holds its writer lock; PostgreSQL needs explicit
// table locks to cover insertions as well as existing rows.
func (s *LifecycleStore) FenceInput(ctx context.Context, tx db.Tx, tenant, expected string) error {
	if s.Store.Dialect() == db.DialectPostgres {
		for _, table := range inferenceInputTables {
			if _, err := tx.ExecContext(ctx, `LOCK TABLE `+table+` IN SHARE ROW EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
	}
	actual, err := inferenceInputStamp(ctx, func(q string, args ...any) (*sql.Rows, error) { return tx.QueryContext(ctx, q, args...) }, tenant)
	if err != nil {
		return err
	}
	if actual != expected {
		return ErrStateConflict
	}
	return nil
}

func inferenceInputStamp(ctx context.Context, query func(string, ...any) (*sql.Rows, error), tenant string) (string, error) {
	return inferenceInputStampAt(ctx, query, tenant, time.Now().UTC())
}

func inferenceInputStampAt(ctx context.Context, query func(string, ...any) (*sql.Rows, error), tenant string, now time.Time) (string, error) {
	h := sha256.New()
	encoder := json.NewEncoder(h)
	if err := encoder.Encode(InferenceSemanticsVersion); err != nil {
		return "", err
	}
	count := 0
	for _, table := range inferenceInputTables {
		where := "tenant_id=?"
		args := []any{tenant}
		if table[0:8] == "ontology" {
			where = "tenant_id IN (?,?,?)"
			args = []any{tenant, "system", ""}
		}
		order := "tenant_id,id"
		if table == "context_graph_fact_assertions" {
			order = "tenant_id,assertion_id"
		}
		rows, err := query(`SELECT * FROM `+table+` WHERE `+where+` ORDER BY `+order+` LIMIT 20001`, args...)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		if err = encoder.Encode(table); err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			count++
			if count > 20000 {
				rows.Close()
				return "", fmt.Errorf("%w: source fence row budget exceeded", ErrValidationFailed)
			}
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return "", err
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			if err = encoder.Encode(values); err != nil {
				rows.Close()
				return "", err
			}
			// A source window can change the input without a database write.
			// Its clock phase participates in observation AND the commit fence,
			// including when a document watch is paused or a worker is behind.
			for i, col := range cols {
				if col != "temporal_json" {
					continue
				}
				var temporal contextgraph.TemporalMetadata
				text, ok := values[i].(string)
				if !ok {
					rows.Close()
					return "", ErrValidationFailed
				}
				if err := json.Unmarshal([]byte(text), &temporal); err != nil {
					rows.Close()
					return "", err
				}
				if err := encoder.Encode(contextgraph.TemporalEligible(temporal, now, now)); err != nil {
					rows.Close()
					return "", err
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
