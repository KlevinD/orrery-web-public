package resourceadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
)

const (
	skillVersionAuditTable        = "resource_skill_version_audit_events"
	skillVersionAuditPendingTable = "resource_skill_version_audit_pending"
)

func (s *CommandService) switchAuditedSkillVersion(ctx context.Context, viewer ViewerRef, registry skillres.VersionedRegistry, active, target skillres.SkillDefinition, rollback bool, action, summary string, metadata map[string]string) error {
	atomic, ok := registry.(skillres.AtomicVersionedRegistry)
	if !ok {
		return fmt.Errorf("%w: atomic skill version registry is required", ErrServiceDependency)
	}
	var auditID string
	if err := s.generateResourceID(&auditID, "skill_audit"); err != nil {
		return err
	}
	if strings.TrimSpace(auditID) == "" || strings.TrimSpace(auditID) != auditID || auditID != db.SanitizeText(auditID) || len(auditID) > 512 {
		return fmt.Errorf("%w: a canonical audit id is required", ErrServiceDependency)
	}
	metadata = cloneStringMap(metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["previous_active_hash"] = skillres.DefinitionHash(active)
	metadata["target_hash"] = skillres.DefinitionHash(target)
	event := AuditEvent{AuditID: auditID, ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, EntityType: "resource", EntityID: string(target.ID), Action: action, Result: "succeeded", Severity: "info", CorrelationID: resourceCorrelationID(viewer), Summary: summary, Metadata: metadata, OccurredAt: time.Now().UTC()}
	err := atomic.SwitchVersion(ctx, skillres.VersionSwitch{SkillID: target.ID, TargetVersion: target.Version, ExpectedActiveHash: skillres.DefinitionHash(active), ExpectedTargetHash: skillres.DefinitionHash(target), Rollback: rollback}, func(ctx context.Context, tx db.Tx) error {
		if tx == nil {
			if s.audit == nil {
				return nil
			}
			return s.audit.RecordAuditEvent(ctx, event)
		}
		// The journal is the authoritative committed audit. Delivery to the
		// separately routed audit database is retried without undoing a commit.
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		created, err := db.InsertRecordTx(ctx, tx, skillVersionAuditTable, auditID, db.Record{"payload": string(payload)})
		if err != nil {
			return err
		}
		if !created {
			return fmt.Errorf("%w: duplicate skill audit id", ErrConflict)
		}
		created, err = db.InsertRecordTx(ctx, tx, skillVersionAuditPendingTable, auditID, db.Record{"audit_id": auditID})
		if err == nil && !created {
			return fmt.Errorf("%w: duplicate pending skill audit id", ErrConflict)
		}
		return err
	})
	if errors.Is(err, skillres.ErrConflict) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return mapSkillPackageStoreError(err)
}

// SkillVersionAuditRelay projects committed events into the audit database.
// Stable IDs make delivery safe after a crash between recording and acking.
// Failed events remain pending; a cursor prevents them starving later events.
type SkillVersionAuditRelay struct {
	store    *db.Store
	recorder AuditRecorderPort
	mu       sync.Mutex
	afterKey string
}

func NewSkillVersionAuditRelay(store *db.Store, recorder AuditRecorderPort) *SkillVersionAuditRelay {
	return &SkillVersionAuditRelay{store: store, recorder: recorder}
}

func (r *SkillVersionAuditRelay) Sweep(ctx context.Context) error {
	if r == nil || r.store == nil || r.recorder == nil {
		return ErrServiceDependency
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	page, err := r.store.ListRecords(ctx, skillVersionAuditPendingTable, r.afterKey, 100)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range page.Items {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := r.deliver(ctx, item); err != nil {
			failures = append(failures, fmt.Errorf("deliver skill audit %s: %w", item.Key, err))
		}
		r.afterKey = item.Key
	}
	if !page.HasMore {
		r.afterKey = ""
	}
	return errors.Join(failures...)
}

func (r *SkillVersionAuditRelay) deliver(ctx context.Context, item db.KeyedRecord) error {
	record, exists, err := r.store.GetRecord(ctx, skillVersionAuditTable, item.Key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("committed audit event missing")
	}
	raw, ok := record["payload"].(string)
	if !ok {
		return fmt.Errorf("committed audit payload missing")
	}
	var event AuditEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return err
	}
	if event.AuditID != item.Key {
		return fmt.Errorf("committed audit id mismatch")
	}
	if err := r.recorder.RecordAuditEvent(ctx, event); err != nil {
		return err
	}
	_, err = r.store.CompareAndDeleteRecord(ctx, skillVersionAuditPendingTable, item.Key, item.Record)
	return err
}
