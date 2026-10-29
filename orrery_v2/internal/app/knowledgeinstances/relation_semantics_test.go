package knowledgeinstances

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestKnowledgeRelationCodeSurvivesAtomicReplacementAndRetry(t *testing.T) {
	store := &Store{Core: instanceDatabase(t, false)}
	watch := publishWatch(t, store, "semantic-source")
	source := instanceSource()
	source.Content = "Acme supplies Beta."
	extraction := Extraction{Reason: "Explicit supplier relationship.", Entities: []EntityInput{
		{Ref: "a", Kind: "company", Name: "Acme", Quote: source.Content},
		{Ref: "b", Kind: "company", Name: "Beta", Quote: source.Content},
	}, Relations: []RelationInput{{From: "a", To: "b", Relation: "supplies", RelationCode: "supplies", Quote: source.Content}}}
	batch := preparedBatch(t, store, watch, source, extraction, "semantic-1")
	receipt := applyBatch(t, store, batch)
	applyBatch(t, store, batch)
	claims, err := store.Claims(context.Background(), source.TenantID, watch.ID)
	if err != nil || len(claims) != 5 || receipt.ClaimCount != 5 {
		t.Fatalf("claims: %+v %v", claims, err)
	}
	found := false
	for _, claim := range claims {
		if claim.TargetID == "" {
			continue
		}
		found = true
		var raw string
		err := store.Core.QueryRow(context.Background(), `SELECT properties_json FROM context_graph_links WHERE tenant_id=? AND id=?`, source.TenantID, claim.ID).Scan(&raw)
		var properties map[string]any
		if err == nil {
			err = json.Unmarshal([]byte(raw), &properties)
		}
		if err != nil || claim.RelationCode != "supplies" || properties["relation_code"] != "supplies" || properties["relation"] != "supplies" {
			t.Fatalf("semantic projection: %+v %+v %v", claim, properties, err)
		}
	}
	if !found {
		t.Fatal("relationship was not persisted")
	}
	// Resolve by name: receipt ordering is not an extraction-order contract.
	entities, _ := store.Candidates(context.Background(), source.TenantID, source.Access)
	for i := range extraction.Entities {
		for _, entity := range entities {
			if entity.Name == extraction.Entities[i].Name {
				extraction.Entities[i].ExistingID = entity.ID
			}
		}
	}
	extraction.Relations[0].RelationCode = "made_up"
	_, err = Prepare("invalid", watch, source, "schema", "authorization", extraction, entities, claims, batch.Plan.CreatedAt)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown code accepted: %v", err)
	}
	source.Revision++
	source.ContentHash = "sha256:semantic-v2"
	source.Content = "Acme is a subsidiary of Beta."
	for i := range extraction.Entities {
		extraction.Entities[i].Quote = source.Content
	}
	extraction.Relations[0].Relation = "is a subsidiary of"
	extraction.Relations[0].RelationCode = "subsidiary_of"
	extraction.Relations[0].Quote = source.Content
	applyBatch(t, store, preparedBatch(t, store, watch, source, extraction, "semantic-2"))
	updated, err := store.Claims(context.Background(), source.TenantID, watch.ID)
	if err != nil || len(updated) != 5 {
		t.Fatalf("replacement claims: %+v %v", updated, err)
	}
	for _, claim := range updated {
		if claim.TargetID != "" && claim.RelationCode != "subsidiary_of" {
			t.Fatal("old semantic code survived replacement")
		}
	}
}
