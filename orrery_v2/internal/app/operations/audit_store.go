package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
)

const auditLogTable = "audit.audit_log"
const authIPLocksTable = "audit.auth_ip_locks"

type AuditStore struct {
	router    *db.Router
	clock     clock.Clock
	generator idgen.ErrorGenerator
}

func NewAuditStore(router *db.Router, clock clock.Clock, generator idgen.ErrorGenerator) *AuditStore {
	return &AuditStore{router: router, clock: clock, generator: generator}
}

func (s *AuditStore) RecordAuditEvent(ctx context.Context, event AuditEventDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	if strings.TrimSpace(event.ActorID) == "" || strings.TrimSpace(event.EntityType) == "" || strings.TrimSpace(event.EntityID) == "" || strings.TrimSpace(event.Action) == "" {
		return fmt.Errorf("%w: actor_id, entity_type, entity_id and action are required", ErrInvalidRequest)
	}
	if event.AuditID == "" {
		id, err := s.newID()
		if err != nil {
			return err
		}
		event.AuditID = id
	}
	if event.Result == "" {
		event.Result = "succeeded"
	}
	if event.Severity == "" {
		event.Severity = "info"
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now()
	}
	store, table, err := s.router.StoreForTable(auditLogTable)
	if err != nil {
		return err
	}
	next := auditRecord(event)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, event.AuditID); err != nil {
			return err
		}
		current, found, err := db.GetRecordTx(ctx, tx, table, event.AuditID)
		if err != nil {
			return err
		}
		if found {
			// Audit rows are append-only. A replay of the producer's stable ID
			// observes the original event and must never replace it.
			_ = current
			return nil
		}
		return db.PutRecordTx(ctx, tx, table, event.AuditID, next)
	})
}

func (s *AuditStore) ListAuditEvents(ctx context.Context, query AuditLogQuery) (AuditEventPageDTO, error) {
	if s == nil || s.router == nil {
		return AuditEventPageDTO{}, ErrServiceDependency
	}
	store, table, err := s.router.StoreForTable(auditLogTable)
	if err != nil {
		return AuditEventPageDTO{}, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return AuditEventPageDTO{}, err
	}
	items := make([]AuditEventDTO, 0)
	for _, record := range records {
		event := auditEventFromRecord(record)
		if !matchesAuditQuery(event, query) {
			continue
		}
		items = append(items, event)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].AuditID < items[j].AuditID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	total := len(items)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	pageItems := items[offset : offset+limit]
	return AuditEventPageDTO{
		Events: pageItems,
		Page:   PageDTO{Limit: query.Limit, Offset: query.Offset, Total: total, Returned: len(pageItems), HasMore: offset+limit < total},
	}, nil
}

func (s *AuditStore) GetIPLock(ctx context.Context, ipAddress string) (userauth.IPBlockRecord, bool, error) {
	if s == nil || s.router == nil {
		return userauth.IPBlockRecord{}, false, ErrServiceDependency
	}
	ipAddress = normalizeAuditIPAddress(ipAddress)
	if ipAddress == "" {
		return userauth.IPBlockRecord{}, false, nil
	}
	record, ok, err := s.router.GetRecord(ctx, authIPLocksTable, ipAddress)
	if err != nil || !ok {
		return userauth.IPBlockRecord{}, ok, err
	}
	return ipLockFromRecord(record), true, nil
}

func (s *AuditStore) SaveIPLock(ctx context.Context, lock userauth.IPBlockRecord) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	lock = normalizeAuditIPLock(lock)
	if lock.IPAddress == "" {
		return fmt.Errorf("%w: ip_address is required", ErrInvalidRequest)
	}
	existing, ok, err := s.GetIPLock(ctx, lock.IPAddress)
	if err != nil {
		return err
	}
	if err := s.router.PutRecord(ctx, authIPLocksTable, lock.IPAddress, ipLockRecord(lock)); err != nil {
		return err
	}
	if lock.Status == "active" && (!ok || existing.Status != "active") {
		return s.RecordAuditEvent(ctx, AuditEventDTO{
			ActorID:    "auth-service",
			EntityType: "auth_ip_lock",
			EntityID:   lock.IPAddress,
			Action:     "auth.ip.lock",
			Result:     "succeeded",
			Severity:   "warning",
			Summary:    "IP locked after repeated failed login attempts",
			Metadata: map[string]string{
				"ip_address":    lock.IPAddress,
				"failure_count": fmt.Sprintf("%d", lock.FailureCount),
				"last_username": lock.LastUsername,
				"last_reason":   lock.LastReason,
			},
			OccurredAt: firstNonZeroTime(lock.LockedAt, lock.UpdatedAt, s.now()),
		})
	}
	return nil
}

func (s *AuditStore) RecordIPFailure(ctx context.Context, ipAddress string, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	ipAddress = normalizeAuditIPAddress(ipAddress)
	if ipAddress == "" || threshold <= 0 {
		return fmt.Errorf("%w: ip_address and positive threshold are required", ErrInvalidRequest)
	}
	store, table, err := s.router.StoreForTable(authIPLocksTable)
	if err != nil {
		return err
	}
	now = now.UTC()
	var lock userauth.IPBlockRecord
	transitionedToActive := false
	err = store.WithTx(ctx, func(tx db.Tx) error {
		seedPayload, err := json.Marshal(db.SanitizeRecordText(ipLockRecord(userauth.IPBlockRecord{IPAddress: ipAddress, Status: "tracking"})))
		if err != nil {
			return fmt.Errorf("encode IP lock seed: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO NOTHING`, table, ipAddress, string(seedPayload)); err != nil {
			return fmt.Errorf("create IP lock seed: %w", err)
		}
		query := `SELECT payload_json FROM platform_records WHERE table_name = ? AND record_key = ?`
		if dialecter, ok := tx.(interface{ Dialect() db.Dialect }); ok && dialecter.Dialect() == db.DialectPostgres {
			query += ` FOR UPDATE`
		}
		var payload string
		if err := tx.QueryRowContext(ctx, query, table, ipAddress).Scan(&payload); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrServiceDependency
			}
			return fmt.Errorf("get IP lock for update: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(payload), &record); err != nil {
			return fmt.Errorf("decode IP lock for update: %w", err)
		}
		lock = ipLockFromRecord(record)
		previousStatus := lock.Status
		if previousStatus == "active" {
			return nil
		}
		if previousStatus != "tracking" || (!lock.UpdatedAt.IsZero() && !now.Before(lock.UpdatedAt.Add(window))) {
			lock = userauth.IPBlockRecord{IPAddress: ipAddress, Status: "tracking"}
		}
		lock.IPAddress = ipAddress
		lock.FailureCount++
		lock.LastUsername = strings.ToLower(strings.TrimSpace(username))
		lock.LastReason = strings.TrimSpace(reason)
		lock.UpdatedAt = now
		if lock.FailureCount >= threshold {
			lock.Status = "active"
			if lock.LockedAt.IsZero() {
				lock.LockedAt = now
			}
			transitionedToActive = previousStatus != "active"
		}
		updatedPayload, err := json.Marshal(db.SanitizeRecordText(ipLockRecord(lock)))
		if err != nil {
			return fmt.Errorf("encode IP lock update: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE platform_records SET payload_json = ? WHERE table_name = ? AND record_key = ?`, string(updatedPayload), table, ipAddress); err != nil {
			return fmt.Errorf("save IP lock update: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !transitionedToActive {
		return nil
	}
	return s.RecordAuditEvent(ctx, AuditEventDTO{
		ActorID:    "auth-service",
		EntityType: "auth_ip_lock",
		EntityID:   lock.IPAddress,
		Action:     "auth.ip.lock",
		Result:     "succeeded",
		Severity:   "warning",
		Summary:    "IP locked after repeated failed login attempts",
		Metadata: map[string]string{
			"ip_address":    lock.IPAddress,
			"failure_count": fmt.Sprintf("%d", lock.FailureCount),
			"last_username": lock.LastUsername,
			"last_reason":   lock.LastReason,
		},
		OccurredAt: firstNonZeroTime(lock.LockedAt, lock.UpdatedAt, s.now()),
	})
}

func (s *AuditStore) DeleteIPLock(ctx context.Context, ipAddress string) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	ipAddress = normalizeAuditIPAddress(ipAddress)
	if ipAddress == "" {
		return nil
	}
	return s.router.DeleteRecord(ctx, authIPLocksTable, ipAddress)
}

func (s *AuditStore) ListIPLocks(ctx context.Context, query userauth.IPBlockQuery) (userauth.IPBlockPageDTO, error) {
	if s == nil || s.router == nil {
		return userauth.IPBlockPageDTO{}, ErrServiceDependency
	}
	store, table, err := s.router.StoreForTable(authIPLocksTable)
	if err != nil {
		return userauth.IPBlockPageDTO{}, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return userauth.IPBlockPageDTO{}, err
	}
	status := strings.ToLower(strings.TrimSpace(query.Status))
	locks := make([]userauth.IPBlockRecord, 0)
	for _, record := range records {
		lock := ipLockFromRecord(record)
		if status != "" && lock.Status != status {
			continue
		}
		locks = append(locks, lock)
	}
	sort.Slice(locks, func(i, j int) bool {
		left := firstNonZeroTime(locks[i].LockedAt, locks[i].UpdatedAt)
		right := firstNonZeroTime(locks[j].LockedAt, locks[j].UpdatedAt)
		if left.Equal(right) {
			return locks[i].IPAddress < locks[j].IPAddress
		}
		return left.After(right)
	})
	total := len(locks)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	pageLocks := locks[offset : offset+limit]
	return userauth.IPBlockPageDTO{
		Locks: pageLocks,
		Page:  userauth.PageDTO{Limit: query.Limit, Offset: query.Offset, Total: total, Returned: len(pageLocks), HasMore: offset+limit < total},
	}, nil
}

func (s *AuditStore) UnlockIP(ctx context.Context, ipAddress string, actorID string, reason string) error {
	ipAddress = normalizeAuditIPAddress(ipAddress)
	if ipAddress == "" {
		return fmt.Errorf("%w: ip_address is required", ErrInvalidRequest)
	}
	existing, ok, err := s.GetIPLock(ctx, ipAddress)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	now := s.now()
	if err := s.DeleteIPLock(ctx, ipAddress); err != nil {
		return err
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		actorID = "admin"
	}
	reason = strings.TrimSpace(reason)
	return s.RecordAuditEvent(ctx, AuditEventDTO{
		ActorID:    actorID,
		EntityType: "auth_ip_lock",
		EntityID:   ipAddress,
		Action:     "auth.ip.unlock",
		Result:     "succeeded",
		Severity:   "info",
		Summary:    "IP lock removed by administrator",
		Metadata: map[string]string{
			"ip_address":             ipAddress,
			"previous_failure_count": fmt.Sprintf("%d", existing.FailureCount),
			"reason":                 reason,
		},
		OccurredAt: now,
	})
}

func auditRecord(event AuditEventDTO) db.Record {
	return db.Record{
		"audit_id":       event.AuditID,
		"tenant_id":      event.TenantID,
		"actor_id":       event.ActorID,
		"viewer_id":      event.ViewerID,
		"mission_id":     event.MissionID,
		"entity_type":    event.EntityType,
		"entity_id":      event.EntityID,
		"action":         event.Action,
		"result":         event.Result,
		"severity":       event.Severity,
		"correlation_id": event.CorrelationID,
		"summary":        event.Summary,
		"metadata":       auditMetadataPayload(event.Metadata),
		"occurred_at":    event.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
}

func ipLockRecord(lock userauth.IPBlockRecord) db.Record {
	lock = normalizeAuditIPLock(lock)
	return db.Record{
		"ip_address":    lock.IPAddress,
		"status":        lock.Status,
		"failure_count": lock.FailureCount,
		"last_username": lock.LastUsername,
		"last_reason":   lock.LastReason,
		"locked_at":     timeString(lock.LockedAt),
		"updated_at":    timeString(lock.UpdatedAt),
		"unlocked_at":   timeString(lock.UnlockedAt),
		"unlocked_by":   lock.UnlockedBy,
		"unlock_reason": lock.UnlockReason,
	}
}

func ipLockFromRecord(record db.Record) userauth.IPBlockRecord {
	return normalizeAuditIPLock(userauth.IPBlockRecord{
		IPAddress:    recordString(record, "ip_address"),
		Status:       recordString(record, "status"),
		FailureCount: recordIntValue(record, "failure_count"),
		LastUsername: recordString(record, "last_username"),
		LastReason:   recordString(record, "last_reason"),
		LockedAt:     auditRecordTime(record, "locked_at"),
		UpdatedAt:    auditRecordTime(record, "updated_at"),
		UnlockedAt:   auditRecordTime(record, "unlocked_at"),
		UnlockedBy:   recordString(record, "unlocked_by"),
		UnlockReason: recordString(record, "unlock_reason"),
	})
}

func auditEventFromRecord(record db.Record) AuditEventDTO {
	event := AuditEventDTO{
		AuditID:       recordString(record, "audit_id"),
		TenantID:      recordString(record, "tenant_id"),
		ActorID:       recordString(record, "actor_id"),
		ViewerID:      recordString(record, "viewer_id"),
		MissionID:     recordString(record, "mission_id"),
		EntityType:    recordString(record, "entity_type"),
		EntityID:      recordString(record, "entity_id"),
		Action:        recordString(record, "action"),
		Result:        recordString(record, "result"),
		Severity:      recordString(record, "severity"),
		CorrelationID: recordString(record, "correlation_id"),
		Summary:       recordString(record, "summary"),
		Metadata:      auditMetadataFromRecord(record),
	}
	if occurredAt, err := time.Parse(time.RFC3339Nano, recordString(record, "occurred_at")); err == nil {
		event.OccurredAt = occurredAt
	}
	return event
}

func auditMetadataPayload(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(payload)
}

func auditMetadataFromRecord(record db.Record) map[string]string {
	raw := recordString(record, "metadata")
	if raw == "" {
		return nil
	}
	metadata := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil
	}
	return metadata
}

func matchesAuditQuery(event AuditEventDTO, query AuditLogQuery) bool {
	return (query.ActorID == "" || event.ActorID == query.ActorID) &&
		(query.Action == "" || event.Action == query.Action) &&
		(query.Result == "" || event.Result == query.Result) &&
		(query.EntityType == "" || event.EntityType == query.EntityType) &&
		(query.EntityID == "" || event.EntityID == query.EntityID) &&
		(query.MissionID == "" || event.MissionID == query.MissionID) &&
		(query.Severity == "" || event.Severity == query.Severity)
}

func recordString(record db.Record, key string) string {
	if value, ok := record[key].(string); ok {
		return value
	}
	return ""
}

func recordIntValue(record db.Record, key string) int {
	switch value := record[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func auditRecordTime(record db.Record, key string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, recordString(record, key)); err == nil {
		return parsed
	}
	return time.Time{}
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func normalizeAuditIPLock(lock userauth.IPBlockRecord) userauth.IPBlockRecord {
	lock.IPAddress = normalizeAuditIPAddress(lock.IPAddress)
	lock.Status = strings.ToLower(strings.TrimSpace(lock.Status))
	if lock.Status == "" {
		lock.Status = "active"
	}
	if lock.FailureCount < 0 {
		lock.FailureCount = 0
	}
	lock.LastUsername = strings.ToLower(strings.TrimSpace(lock.LastUsername))
	lock.LastReason = strings.TrimSpace(lock.LastReason)
	lock.LockedAt = lock.LockedAt.UTC()
	lock.UpdatedAt = lock.UpdatedAt.UTC()
	lock.UnlockedAt = lock.UnlockedAt.UTC()
	lock.UnlockedBy = strings.TrimSpace(lock.UnlockedBy)
	lock.UnlockReason = strings.TrimSpace(lock.UnlockReason)
	return lock
}

func normalizeAuditIPAddress(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func (s *AuditStore) now() time.Time {
	if s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *AuditStore) newID() (string, error) {
	if s.generator == nil {
		return idgen.NewRandom("audit").NewIDContext()
	}
	return s.generator.NewIDContext()
}
