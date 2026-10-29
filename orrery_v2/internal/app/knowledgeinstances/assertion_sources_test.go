package knowledgeinstances

import (
	"context"
	"testing"
)

func TestGroundedAssertionSourcesRequireOwnedUnchangedProjections(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			store := &Store{Core: instanceDatabase(t, postgres)}
			watch := publishWatch(t, store, "source-a")
			source := instanceSource()
			source.Generation = 1
			batch := preparedBatch(t, store, watch, source, instanceExtraction("Acme", "", 100), "read-source-batch")
			applyBatch(t, store, batch)
			claims, err := store.Claims(context.Background(), watch.TenantID, watch.ID)
			if err != nil {
				t.Fatal(err)
			}
			queries := []AssertionSourceQuery{}
			for _, c := range claims {
				queries = append(queries, AssertionSourceQuery{Kind: "fact", ID: c.ID, Stream: claimStream(batch.Plan)})
			}
			first, err := store.ReadAssertionSources(context.Background(), watch.TenantID, queries)
			if err != nil || len(first) != len(claims) {
				t.Fatalf("sources: %+v %v", first, err)
			}
			for _, actual := range first {
				if actual.Content != "" || actual.Stamp() != source.Stamp() {
					t.Fatal("source text leaked or version changed")
				}
			}
			if _, err := store.Core.Exec(context.Background(), `UPDATE context_graph_facts SET value_json='"foreign"' WHERE tenant_id=? AND id=?`, watch.TenantID, claims[0].ID); err != nil {
				t.Fatal(err)
			}
			second, err := store.ReadAssertionSources(context.Background(), watch.TenantID, queries)
			if err != nil || len(second) != len(claims)-1 {
				t.Fatalf("foreign projection attested: %+v %v", second, err)
			}
			queries[0].Stream = "forged-stream"
			third, err := store.ReadAssertionSources(context.Background(), watch.TenantID, queries[:1])
			if err != nil || len(third) != 0 {
				t.Fatal("foreign stream read as owned support")
			}
		})
	}
}
