package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func identityMigration() migration.Migration {
	return migration.Migration{Version: "0245_ontology_entity_identity", Description: "qualified identity bindings and scoped candidate recall indexes", Statements: []string{
		`CREATE TABLE IF NOT EXISTS knowledge_instance_identity_keys (tenant_id TEXT NOT NULL,access_hash TEXT NOT NULL,kind TEXT NOT NULL,key_hash TEXT NOT NULL,entity_id TEXT NOT NULL,first_batch_id TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,access_hash,kind,key_hash),FOREIGN KEY(tenant_id,entity_id) REFERENCES knowledge_instances(tenant_id,id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_instance_identity_keys_entity ON knowledge_instance_identity_keys(tenant_id,entity_id)`,
		`CREATE TABLE IF NOT EXISTS knowledge_instance_identity_terms (tenant_id TEXT NOT NULL,access_hash TEXT NOT NULL,entity_id TEXT NOT NULL,kind TEXT NOT NULL,term_kind TEXT NOT NULL,term TEXT NOT NULL,PRIMARY KEY(tenant_id,access_hash,term_kind,term,entity_id),FOREIGN KEY(tenant_id,entity_id) REFERENCES knowledge_instances(tenant_id,id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_instance_identity_terms_entity ON knowledge_instance_identity_terms(tenant_id,entity_id)`,
		`CREATE TABLE IF NOT EXISTS knowledge_instance_identity_index_state (tenant_id TEXT NOT NULL,entity_id TEXT NOT NULL,indexed_version BIGINT NOT NULL,PRIMARY KEY(tenant_id,entity_id),FOREIGN KEY(tenant_id,entity_id) REFERENCES knowledge_instances(tenant_id,id) ON DELETE CASCADE)`,
	}}
}

func (s *Store) checkIdentityAssignments(ctx context.Context, tx db.Tx, p Plan, entities map[string]Entity) error {
	if len(p.IdentityAssignments) == 0 { // Immutable plans from before this protocol.
		for _, e := range p.Entities {
			if p.Before[e.ID] == 0 && len(e.IdentityKeys) > 0 {
				return ErrInvalid
			}
		}
		return nil
	}
	if len(p.IdentityAssignments) != len(p.Entities) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, a := range p.IdentityAssignments {
		e, ok := entities[a.EntityID]
		if !ok || seen[e.ID] || strings.TrimSpace(a.Name) == "" || !slices.ContainsFunc(p.Entities, func(item Entity) bool { return item.ID == a.EntityID }) || a.New != (e.Version == 0) || !quoted(p.Source, a.Quote) || !strings.Contains(canonical(a.Quote), canonical(a.Name)) {
			return ErrInvalid
		}
		if p.CandidateRecall != nil && ((a.New && p.CandidateRecall.Truncated) || (!a.New && !slices.Contains(p.CandidateRecall.CandidateIDs, e.ID))) {
			return ErrInvalid
		}
		seen[e.ID] = true
		keys, err := validateIdentifiers(p.Source, EntityInput{Kind: e.Kind, Identifiers: a.Identifiers})
		if err != nil {
			return err
		}
		e.IdentityKeys, err = mergeIdentityKeys(e.IdentityKeys, keys)
		if err != nil {
			return err
		}
		for _, key := range e.IdentityKeys {
			var owner, raw string
			err := tx.QueryRowContext(ctx, `SELECT entity_id,body_json FROM knowledge_instance_identity_keys WHERE tenant_id=? AND access_hash=? AND kind=? AND key_hash=?`, e.TenantID, Digest(e.Access), e.Kind, Digest(key)).Scan(&owner, &raw)
			if err == nil && (owner != e.ID || raw != body(key)) {
				return ErrConflict
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		entities[e.ID] = e
	}
	return nil
}

// Bindings survive source withdrawal as identity history. Only source-owned
// claims are active assertions; these bindings cannot independently prove one.
func syncIdentityIndexTx(ctx context.Context, tx db.Tx, e Entity, batchID string) error {
	for _, key := range e.IdentityKeys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instance_identity_keys(tenant_id,access_hash,kind,key_hash,entity_id,first_batch_id,body_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,access_hash,kind,key_hash) DO NOTHING`, e.TenantID, Digest(e.Access), e.Kind, Digest(key), e.ID, batchID, body(key)); err != nil {
			return err
		}
		var owner, raw string
		if err := tx.QueryRowContext(ctx, `SELECT entity_id,body_json FROM knowledge_instance_identity_keys WHERE tenant_id=? AND access_hash=? AND kind=? AND key_hash=?`, e.TenantID, Digest(e.Access), e.Kind, Digest(key)).Scan(&owner, &raw); err != nil {
			return err
		}
		if owner != e.ID || raw != body(key) {
			return ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_instance_identity_terms WHERE tenant_id=? AND entity_id=?`, e.TenantID, e.ID); err != nil {
		return err
	}
	terms := entityTerms(e)
	for _, kind := range []string{"label", "identifier", "strong", "token"} {
		values := make([]string, 0, len(terms[kind]))
		for term := range terms[kind] {
			values = append(values, term)
		}
		sort.Strings(values)
		for _, term := range values {
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instance_identity_terms(tenant_id,access_hash,entity_id,kind,term_kind,term) VALUES(?,?,?,?,?,?)`, e.TenantID, Digest(e.Access), e.ID, e.Kind, kind, term); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instance_identity_index_state(tenant_id,entity_id,indexed_version) VALUES(?,?,?) ON CONFLICT(tenant_id,entity_id) DO UPDATE SET indexed_version=excluded.indexed_version`, e.TenantID, e.ID, e.Version)
	return err
}

type identityTerms map[string]map[string]bool

func newIdentityTerms() identityTerms {
	return identityTerms{"label": {}, "identifier": {}, "strong": {}, "token": {}}
}

func (t identityTerms) add(kind, value string) {
	if value = canonical(value); value != "" && len(value) <= 256 {
		t[kind][value] = true
	}
}

func (t identityTerms) label(value string) {
	t.add("label", value)
	for _, token := range identityTokens(value) {
		t.add("token", token)
	}
}

func entityTerms(e Entity) identityTerms {
	t := newIdentityTerms()
	t.label(e.Name)
	t.label(e.Key)
	for _, alias := range stringValues(e.Properties["aliases"]) {
		t.label(alias)
	}
	if value, ok := e.Properties["registration_id"].(string); ok {
		t.add("identifier", value) // Unqualified numbers recall; they never bind identity.
	}
	for _, key := range e.IdentityKeys {
		t.add("identifier", key.Value)
		t.add("strong", Digest(key))
	}
	return t
}

func stringValues(value any) []string {
	var result []string
	switch value := value.(type) {
	case []string:
		return value
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func identityTokens(value string) []string {
	words := strings.FieldsFunc(canonical(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var result []string
	for _, word := range words {
		if len(word) < 2 || strings.Contains("|the|company|limited|ltd|inc|corp|co|", "|"+word+"|") {
			continue
		}
		result = append(result, word)
		runes := []rune(word)
		if len(runes) > 2 && unicode.Is(unicode.Han, runes[0]) {
			for i := 0; i+1 < len(runes) && i < 24; i++ {
				result = append(result, string(runes[i:i+2]))
			}
		}
		if len(result) >= 32 {
			return result[:32]
		}
	}
	return result
}

// Repair legacy indexes in bounded pages. New commits and withdrawals update
// them in their own transaction. Existing IDs and business projections are intact.
func (s *Store) repairIdentityIndex(ctx context.Context, tenant string, access contextgraph.AccessRef) error {
	for {
		count := 0
		err := s.Core.WithTx(ctx, func(tx db.Tx) error {
			if s.Core.Dialect() == db.DialectPostgres {
				if _, err := tx.ExecContext(ctx, `LOCK TABLE context_graph_objects,knowledge_instances IN SHARE ROW EXCLUSIVE MODE`); err != nil {
					return err
				}
			}
			rows, err := tx.QueryContext(ctx, `SELECT e.body_json FROM knowledge_instances e LEFT JOIN knowledge_instance_identity_index_state s ON s.tenant_id=e.tenant_id AND s.entity_id=e.id WHERE e.tenant_id=? AND e.access_hash=? AND (s.indexed_version IS NULL OR s.indexed_version<>e.version) ORDER BY e.id LIMIT 100`, tenant, Digest(access))
			if err != nil {
				return err
			}
			var page []Entity
			for rows.Next() {
				var raw string
				var e Entity
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				if err := json.Unmarshal([]byte(raw), &e); err != nil || e.TenantID != tenant || e.Access != access {
					rows.Close()
					return ErrConflict
				}
				page = append(page, e)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			count = len(page)
			for _, e := range page {
				if err := syncIdentityIndexTx(ctx, tx, e, ""); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || count < 100 {
			return err
		}
	}
}
