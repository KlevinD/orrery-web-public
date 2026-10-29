package improvement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const (
	caseTable    = "ontology_improvement_cases"
	historyTable = "ontology_improvement_revisions"
	commandTable = "ontology_improvement_commands"
	outboxTable  = "ontology_improvement_projection_outbox"
)

type Store struct {
	core *db.Store
	now  func() time.Time
}

func NewStore(core *db.Store) *Store { return &Store{core: core, now: time.Now} }

func caseID(o Observation) string       { return "improvement-case:" + Digest([]string{o.TenantID, o.ID}) }
func tenantPrefix(tenant string) string { return Digest(tenant) + "/" }
func caseKey(tenant, id string) string  { return tenantPrefix(tenant) + Digest(id) }
func revisionKey(c Case) string         { return caseKey(c.TenantID, c.ID) + fmt.Sprintf("/%020d", c.Revision) }

func encode(v any) db.Record {
	raw, _ := json.Marshal(v)
	return db.Record{"payload": string(raw), "sha256": Digest(v)}
}

func decode[T any](record db.Record) (T, error) {
	var v T
	raw, ok := record["payload"].(string)
	if !ok || json.Unmarshal([]byte(raw), &v) != nil || record["sha256"] != Digest(v) {
		return v, fmt.Errorf("%w: stored material integrity check failed", ErrInvalid)
	}
	return v, nil
}

func privateCopy[T any](v T) T {
	raw, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Store) ready() error {
	if s == nil || s.core == nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Observe(ctx context.Context, observation Observation) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	if err := validateObservation(observation); err != nil {
		return Case{}, err
	}
	o := privateCopy(observation)
	var result Case
	err := s.core.WithTx(ctx, func(tx db.Tx) error {
		var err error
		result, err = s.observeTx(ctx, tx, o, s.now().UTC())
		return err
	})
	return result, err
}

// observeTx also lets runtime terminal evidence and its observation share one
// commit, including the immutable history and graph projection outbox.
func (s *Store) observeTx(ctx context.Context, tx db.Tx, o Observation, now time.Time) (Case, error) {
	if err := validateObservation(o); err != nil {
		return Case{}, err
	}
	candidate := Case{ID: caseID(o), TenantID: o.TenantID, Revision: 1, State: Observed, Observation: o, CreatedAt: now, UpdatedAt: now}
	switch o.Trigger {
	case "managed_repair_needs_attention", "captured_runtime_failed", "captured_runtime_timed_out", "captured_runtime_blocked":
		candidate.State = PendingDiagnosis
	case "runtime_failed", "runtime_timed_out", "runtime_blocked":
		candidate.State, candidate.Reason = Blocked, "captured_runtime_evidence_required"
	}
	key := caseKey(o.TenantID, candidate.ID)
	if err := db.LockRecordKeyTx(ctx, tx, caseTable, key); err != nil {
		return Case{}, err
	}
	record, exists, err := db.GetRecordTx(ctx, tx, caseTable, key)
	if err != nil {
		return Case{}, err
	}
	if exists {
		result, err := decode[Case](record)
		if err != nil {
			return Case{}, err
		}
		if !reflect.DeepEqual(result.Observation, o) {
			return Case{}, ErrConflict
		}
		return result, nil
	}
	if o.ParentCaseID != "" {
		parentRecord, exists, err := db.GetRecordTx(ctx, tx, caseTable, caseKey(o.TenantID, o.ParentCaseID))
		if err != nil {
			return Case{}, err
		}
		if !exists {
			return Case{}, ErrNotFound
		}
		parent, err := decode[Case](parentRecord)
		if err != nil {
			return Case{}, err
		}
		if !terminal(parent.State) || parent.Observation.Task != o.Task || parent.Observation.Run == o.Run || parent.TenantID != o.TenantID {
			return Case{}, ErrConflict
		}
	}
	return candidate, persistCase(ctx, tx, candidate, true)
}

func persistCase(ctx context.Context, tx db.Tx, c Case, project bool) error {
	record := encode(c)
	if err := db.PutRecordTx(ctx, tx, caseTable, caseKey(c.TenantID, c.ID), record); err != nil {
		return err
	}
	created, err := db.InsertRecordTx(ctx, tx, historyTable, revisionKey(c), record)
	if err != nil {
		return err
	}
	if !created {
		return ErrConflict
	}
	if project {
		// An immutable snapshot and its outbox entry commit with the aggregate.
		// Projection errors never roll back an already committed task observation.
		created, err = db.InsertRecordTx(ctx, tx, outboxTable, revisionKey(c), record)
		if err != nil {
			return err
		}
		if !created {
			return ErrConflict
		}
	}
	return nil
}

func validScope(tenant, id string) bool { return textOK(tenant, 256) && textOK(id, 256) }

func (s *Store) Get(ctx context.Context, tenant, id string) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	if !validScope(tenant, id) {
		return Case{}, ErrInvalid
	}
	record, exists, err := s.core.GetRecord(ctx, caseTable, caseKey(tenant, id))
	if err != nil {
		return Case{}, err
	}
	if !exists {
		return Case{}, ErrNotFound
	}
	c, err := decode[Case](record)
	if err == nil && (c.TenantID != tenant || c.ID != id || c.ID != caseID(c.Observation)) {
		return Case{}, ErrConflict
	}
	return c, err
}

func (s *Store) List(ctx context.Context, tenant, after string, limit int) (Page, error) {
	if err := s.ready(); err != nil {
		return Page{}, err
	}
	prefix := tenantPrefix(tenant)
	if !textOK(tenant, 256) || limit < 1 || limit > 100 || (after != "" && (!strings.HasPrefix(after, prefix) || len(after) != len(prefix)+64 || !hashOK(strings.TrimPrefix(after, prefix)))) {
		return Page{}, ErrInvalid
	}
	if after == "" {
		after = prefix
	}
	page, err := s.core.ListRecords(ctx, caseTable, after, limit+1)
	if err != nil {
		return Page{}, err
	}
	result := Page{Items: []Case{}}
	for _, entry := range page.Items {
		if !strings.HasPrefix(entry.Key, prefix) {
			break
		}
		if len(result.Items) == limit {
			result.NextCursor = page.Items[limit-1].Key
			break
		}
		c, err := decode[Case](entry.Record)
		if err != nil {
			return Page{}, err
		}
		if c.TenantID != tenant {
			return Page{}, ErrConflict
		}
		result.Items = append(result.Items, c)
	}
	return result, nil
}

func (s *Store) Claim(ctx context.Context, tenant, id, owner string, expected int64, duration time.Duration) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	if !validScope(tenant, id) || !textOK(owner, 512) || duration < time.Second || duration > 10*time.Minute {
		return Case{}, ErrInvalid
	}
	var result Case
	err := s.core.WithTx(ctx, func(tx db.Tx) error {
		key := caseKey(tenant, id)
		if err := db.LockRecordKeyTx(ctx, tx, caseTable, key); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, caseTable, key)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		c, err := decode[Case](record)
		if err != nil {
			return err
		}
		if c.Revision != expected || terminal(c.State) {
			return ErrConflict
		}
		now := s.now().UTC()
		// Renewals also rotate the fencing token. The previous worker cannot
		// complete a write after a renewal or lease takeover.
		if c.Lease != nil && c.Lease.ExpiresAt.After(now) && c.Lease.Owner != owner {
			return ErrLease
		}
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return err
		}
		c.LeaseEpoch++
		c.Revision++
		c.UpdatedAt = now
		c.Lease = &Lease{Owner: owner, Token: hex.EncodeToString(token), Epoch: c.LeaseEpoch, ExpiresAt: now.Add(duration)}
		result = c
		return persistCase(ctx, tx, c, false)
	})
	return result, err
}

type commandReceipt struct {
	RequestHash string     `json:"request_hash"`
	Request     Transition `json:"request"`
	Result      Case       `json:"result"`
}

func commandKey(t Transition) string { return caseKey(t.TenantID, t.CaseID) + "/" + Digest(t.ID) }

func (s *Store) replay(ctx context.Context, t Transition) (Case, bool, error) {
	record, exists, err := s.core.GetRecord(ctx, commandTable, commandKey(t))
	if err != nil || !exists {
		return Case{}, exists, err
	}
	receipt, err := decode[commandReceipt](record)
	if err != nil {
		return Case{}, true, err
	}
	if receipt.RequestHash != Digest(t) {
		return Case{}, true, ErrConflict
	}
	return receipt.Result, true, nil
}

// Advance requires an integration verifier for actual execution evidence. It
// deliberately does not accept model-written pass flags as execution authority.
func (s *Store) Advance(ctx context.Context, command Transition, verifier Verifier) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	if !validScope(command.TenantID, command.CaseID) || !textOK(command.ID, 256) || !hashOK(command.LeaseToken) || Digest(command) == "" {
		return Case{}, ErrInvalid
	}
	t := privateCopy(command)
	if result, exists, err := s.replay(ctx, t); err != nil || exists {
		return result, err
	}
	current, err := s.Get(ctx, t.TenantID, t.CaseID)
	if err != nil {
		return Case{}, err
	}
	if err := checkLease(current, t, s.now()); err != nil {
		return Case{}, err
	}
	if _, err := applyTransition(current, t); err != nil {
		return Case{}, err
	}
	switch t.To {
	case Tested, Evaluated, Monitoring, Verified, RollbackRequired, RolledBack:
		if verifier == nil {
			return Case{}, ErrUnavailable
		}
		if err := s.verifyRuntimeAdmission(ctx, current, t, verifier); err != nil {
			return Case{}, err
		}
		// Never hold a database transaction across provider/network verification.
		if err := verifier.Verify(ctx, privateCopy(current), privateCopy(t)); err != nil {
			return Case{}, err
		}
	}
	var result Case
	err = s.core.WithTx(ctx, func(tx db.Tx) error {
		key := caseKey(t.TenantID, t.CaseID)
		if err := db.LockRecordKeyTx(ctx, tx, caseTable, key); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, commandTable, commandKey(t))
		if err != nil {
			return err
		}
		if exists {
			receipt, err := decode[commandReceipt](record)
			if err != nil {
				return err
			}
			if receipt.RequestHash != Digest(t) {
				return ErrConflict
			}
			result = receipt.Result
			return nil
		}
		record, exists, err = db.GetRecordTx(ctx, tx, caseTable, key)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		c, err := decode[Case](record)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if err := checkLease(c, t, now); err != nil {
			return err
		}
		c, err = applyTransition(c, t)
		if err != nil {
			return err
		}
		c.Revision++
		c.UpdatedAt = now
		if err := persistCase(ctx, tx, c, true); err != nil {
			return err
		}
		created, err := db.InsertRecordTx(ctx, tx, commandTable, commandKey(t), encode(commandReceipt{RequestHash: Digest(t), Request: t, Result: c}))
		if err != nil {
			return err
		}
		if !created {
			return ErrConflict
		}
		result = c
		return nil
	})
	return result, err
}

func checkLease(c Case, t Transition, now time.Time) error {
	if c.Revision != t.ExpectedRevision {
		return ErrConflict
	}
	if c.Lease == nil || c.Lease.Token != t.LeaseToken || !c.Lease.ExpiresAt.After(now) {
		return ErrLease
	}
	return nil
}

type ProjectionSink interface {
	ProjectImprovement(context.Context, Case) error
}

func (s *Store) ProjectPending(ctx context.Context, sink ProjectionSink, limit int) (int, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if sink == nil || limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	page, err := s.core.ListRecords(ctx, outboxTable, "", limit)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range page.Items {
		c, err := decode[Case](entry.Record)
		if err != nil {
			return count, err
		}
		if err := sink.ProjectImprovement(ctx, c); err != nil {
			return count, err
		}
		deleted, err := s.core.CompareAndDeleteRecord(ctx, outboxTable, entry.Key, entry.Record)
		if err != nil {
			return count, err
		}
		if deleted {
			count++
		}
	}
	return count, nil
}
