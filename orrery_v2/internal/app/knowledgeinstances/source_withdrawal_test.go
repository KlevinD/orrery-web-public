package knowledgeinstances

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func testSourceWithdrawal(t *testing.T, postgres bool) {
	s := &Store{Core: instanceDatabase(t, postgres)}
	ctx := context.Background()
	a, b := publishWatch(t, s, "source-a"), publishWatch(t, s, "source-b")
	source := instanceSource()
	source.Generation = 1
	r := applyBatch(t, s, preparedBatch(t, s, a, source, instanceExtraction("Acme", "", 100), "a-original"))
	other := amountSource("200", 1)
	other.DocumentID = "other-doc"
	other.Generation = 1
	applyBatch(t, s, conflictBatch(t, s, b, other, instanceExtraction("Acme", r.EntityIDs[0], 200), "b-conflict", a.ID))
	// A paused watch is still subject to a real source invalidation; pause alone
	// does not withdraw any claims or authorize a maintenance command.
	paused := a
	paused.Active = false
	paused.PublishedAt = time.Now().UTC()
	if _, err := (&WatchStore{Store: s.Core}).Publish(ctx, paused, a.Version, allowCommit); err != nil {
		t.Fatal(err)
	}
	input := SourceWithdrawal{EventID: "source-deleted", EventType: "document.deleted", Sequence: 12, Source: source}
	input.Source.Content = ""
	apply := func() (WithdrawalReceipt, error) {
		var receipt WithdrawalReceipt
		err := s.Core.WithTx(ctx, func(tx db.Tx) error {
			var err error
			receipt, err = s.WithdrawSourceTx(ctx, tx, input, allowCommit)
			return err
		})
		return receipt, err
	}
	var wg sync.WaitGroup
	results := make(chan WithdrawalReceipt, 4)
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := apply(); results <- r; failures <- err }()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first WithdrawalReceipt
	for receipt := range results {
		if first.ID == "" {
			first = receipt
		}
		if Digest(receipt) != Digest(first) {
			t.Fatal("duplicate withdrawals returned different durable receipts")
		}
	}
	if first.ClaimCount != 4 || len(first.WatchIDs) != 1 || first.WatchIDs[0] != a.ID {
		t.Fatalf("receipt: %+v", first)
	}
	entities, err := s.Candidates(ctx, source.TenantID, source.Access)
	if err != nil || len(entities) != 1 || entities[0].Status != "active" || entities[0].Properties["amount"] != float64(200) || len(entities[0].Resolutions) != 0 || len(entities[0].Conflicts) != 0 {
		t.Fatalf("surviving source/resolution: %+v %v", entities, err)
	}
	assertAmounts(t, s, 1, 0)
	for table, want := range map[string]int{"ontology_document_withdrawals": 1, "ontology_document_receipts": 2, "action_runs": 3} {
		var got int
		if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want %d: %v", table, got, want, err)
		}
	}
	var assertionCount int
	if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_fact_assertions WHERE source_stream_id=?`, claimStream(Plan{Source: source, Watch: a})).Scan(&assertionCount); err != nil || assertionCount != 0 {
		t.Fatalf("invalid inference premises survived: %d %v", assertionCount, err)
	}
	// A receipt is bound to the exact source attestation, not just an event ID.
	input.Source.ContentHash = "changed"
	if _, err := apply(); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency key reused with different input: %v", err)
	}
	input.Source = other
	input.Source.Content = ""
	input.EventID = "last-source-unpublished"
	input.EventType = "document.unpublished"
	if _, err := apply(); err != nil {
		t.Fatal(err)
	}
	entities, _ = s.Candidates(ctx, source.TenantID, source.Access)
	if entities[0].Status != "withdrawn" || len(entities[0].Resolutions) != 0 {
		t.Fatalf("unsupported identity active: %+v", entities)
	}
}

func TestDocumentInstancesTrustedSourceWithdrawal(t *testing.T) { testSourceWithdrawal(t, false) }
func TestPostgresDocumentInstancesTrustedSourceWithdrawal(t *testing.T) {
	testSourceWithdrawal(t, true)
}

func TestDocumentInstancesSourceWithdrawalRollsBackForeignAndUntrustedWrites(t *testing.T) {
	for _, scenario := range []string{"guard", "foreign-fact", "newer-generation", "transaction-failure", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			s := &Store{Core: instanceDatabase(t, false)}
			ctx := context.Background()
			w := publishWatch(t, s, "source-a")
			source := instanceSource()
			source.Generation = 2
			b := preparedBatch(t, s, w, source, instanceExtraction("Acme", "", 100), "initial")
			applyBatch(t, s, b)
			input := SourceWithdrawal{EventID: "invalidated", EventType: "document.deleted", Sequence: 12, Source: source}
			input.Source.Content = ""
			guard := allowCommit
			switch scenario {
			case "guard":
				guard = func(context.Context, db.Tx) error { return ErrConflict }
			case "foreign-fact":
				if _, err := s.Core.Exec(ctx, `UPDATE context_graph_facts SET source_stream_id='foreign-domain' WHERE tenant_id=? AND id=?`, w.TenantID, b.Plan.Claims[0].ID); err != nil {
					t.Fatal(err)
				}
			case "newer-generation":
				input.Source.Generation = 1
			case "legacy":
				claims, _ := s.Claims(ctx, w.TenantID, w.ID)
				for _, c := range claims {
					c.Source = nil
					if _, err := s.Core.Exec(ctx, `UPDATE knowledge_instance_claims SET body_json=? WHERE tenant_id=? AND id=?`, body(c), w.TenantID, c.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := s.Core.WithTx(ctx, func(tx db.Tx) error {
				_, err := s.WithdrawSourceTx(ctx, tx, input, guard)
				if err == nil && scenario == "transaction-failure" {
					return ErrConflict
				}
				return err
			})
			if scenario == "legacy" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("untrusted/conflicting source withdrawal committed")
			}
			claims, _ := s.Claims(ctx, w.TenantID, w.ID)
			if len(claims) != 4 {
				t.Fatal("failed withdrawal damaged source claims")
			}
			var receipts int
			if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_document_withdrawals`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("failed command left receipt: %d %v", receipts, err)
			}
		})
	}
}
