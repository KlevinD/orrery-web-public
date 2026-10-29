package outbound

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	platformdb "orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

const Topic = "gateway.message.deliver.v1"

// MaxDeliveryBodyRunes bounds a delivery to a small number of provider API
// calls so the durable attempt can finish inside deliverySendTimeout. Larger
// content must be represented by a link or a separate durable artifact.
const MaxDeliveryBodyRunes = 10000

const (
	deliverySendLease      = 5 * time.Minute
	deliveryBindingTimeout = 5 * time.Second
	deliverySendTimeout    = 30 * time.Second
)

type retryableDeliveryError struct {
	err                error
	delay              time.Duration
	diagnosticCaptured bool
}

func (e *retryableDeliveryError) Error() string             { return e.err.Error() }
func (e *retryableDeliveryError) Unwrap() error             { return e.err }
func (e *retryableDeliveryError) RetryDelay() time.Duration { return e.delay }
func (e *retryableDeliveryError) DiagnosticCaptured() bool  { return e.diagnosticCaptured }

var (
	ErrNotFound            = errors.New("gateway delivery not found")
	ErrInvalidInput        = errors.New("gateway delivery input is invalid")
	ErrInProgress          = errors.New("gateway delivery is already being sent")
	ErrBindingMissing      = errors.New("gateway delivery binding is missing")
	ErrIdempotencyConflict = errors.New("gateway delivery idempotency key was already used for different content")
)

type Delivery struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id"`
	ChannelKey        string    `json:"channel_key"`
	AccountID         string    `json:"account_id"`
	BindingID         string    `json:"binding_id"`
	HumanProfileID    string    `json:"human_profile_id"`
	ExternalTenantID  string    `json:"external_tenant_id,omitempty"`
	RecipientID       string    `json:"recipient_id"`
	EventType         string    `json:"event_type"`
	Title             string    `json:"title,omitempty"`
	Body              string    `json:"body"`
	Format            string    `json:"format"`
	IdempotencyKey    string    `json:"idempotency_key"`
	Status            string    `json:"status"`
	Attempts          int       `json:"attempts"`
	ExternalMessageID string    `json:"external_message_id,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	DeliveredAt       time.Time `json:"delivered_at,omitempty"`
}

type EnqueueCommand struct {
	TenantID       string
	HumanProfileID string
	ChannelKeys    []string
	AccountID      string
	EventType      string
	Title          string
	Body           string
	Format         string
	IdempotencyKey string
}

type Sender interface {
	Send(context.Context, channel.Delivery) (channel.DeliveryResult, error)
}

type BindingStore interface {
	ListByHuman(ctx context.Context, tenantID, humanProfileID string) ([]gwbindings.Binding, error)
	Resolve(ctx context.Context, identity gwbindings.ExternalIdentity) (gwbindings.Binding, error)
}

type Service struct {
	jobs            *platformdb.Store
	bindings        BindingStore
	sender          Sender
	now             func() time.Time
	schemaMu        sync.Mutex
	schemaReady     bool
	validatorMu     sync.RWMutex
	validator       func(context.Context, Delivery) (bool, error)
	failureObserver func(context.Context, DeliveryFailure) error
}

// DeliveryFailure retains the provider cause for internal diagnostics without
// exposing it through public delivery LastError or the relay's generic error.
type DeliveryFailure struct {
	Delivery Delivery
	Attempt  int
	Code     string
	Cause    error
}

func (s *Service) SetFailureObserver(observe func(context.Context, DeliveryFailure) error) {
	s.validatorMu.Lock()
	defer s.validatorMu.Unlock()
	s.failureObserver = observe
}

func (s *Service) recordDeliveryFailure(ctx context.Context, delivery Delivery, attempt int, code, message, status string, cause error) error {
	s.validatorMu.RLock()
	observe := s.failureObserver
	s.validatorMu.RUnlock()
	if observe != nil {
		if err := observe(ctx, DeliveryFailure{Delivery: delivery, Attempt: attempt, Code: code, Cause: cause}); err != nil {
			return fmt.Errorf("capture gateway delivery failure: %w", err)
		}
	}
	return s.updateClaimedFailure(ctx, delivery.ID, attempt, message, status)
}

func (s *Service) retryableRecordedFailure(err error, attempt int) error {
	result := retryableDeliveryFailure(err, attempt).(*retryableDeliveryError)
	s.validatorMu.RLock()
	result.diagnosticCaptured = s.failureObserver != nil
	s.validatorMu.RUnlock()
	return result
}

// SetDeliveryValidator installs a business eligibility check before channel IO.
// It can be wired safely while the gateway lifecycle is already running.
func (s *Service) SetDeliveryValidator(validate func(context.Context, Delivery) (bool, error)) {
	s.validatorMu.Lock()
	defer s.validatorMu.Unlock()
	s.validator = validate
}

// AddDeliveryValidator preserves existing business guards when another
// notification publisher uses the shared outbound service.
func (s *Service) AddDeliveryValidator(validate func(context.Context, Delivery) (bool, error)) {
	s.validatorMu.Lock()
	defer s.validatorMu.Unlock()
	previous := s.validator
	s.validator = func(ctx context.Context, delivery Delivery) (bool, error) {
		if previous != nil {
			allowed, err := previous(ctx, delivery)
			if err != nil || !allowed {
				return allowed, err
			}
		}
		return validate(ctx, delivery)
	}
}

func NewService(jobs *platformdb.Store, bindings BindingStore, sender Sender) *Service {
	return &Service{jobs: jobs, bindings: bindings, sender: sender, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	if s == nil || s.jobs == nil {
		return fmt.Errorf("gateway delivery store is not configured")
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.schemaReady {
		return nil
	}
	timeType := "INTEGER"
	if s.jobs.Dialect() == platformdb.DialectPostgres {
		timeType = "BIGINT"
	}
	for _, statement := range migrationStatements(timeType) {
		if _, err := s.jobs.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure gateway delivery schema: %w", err)
		}
	}
	s.schemaReady = true
	return nil
}

func (s *Service) Enqueue(ctx context.Context, command EnqueueCommand) ([]Delivery, error) {
	command = normalizeCommand(command)
	if !validCommand(command) {
		return nil, ErrInvalidInput
	}
	if s.bindings == nil {
		return nil, fmt.Errorf("gateway binding store is not configured")
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	if existing, found, err := s.loadIdempotentRequest(ctx, command); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}
	bindings, err := s.bindings.ListByHuman(ctx, command.TenantID, command.HumanProfileID)
	if err != nil {
		return nil, err
	}
	selected := make([]gwbindings.Binding, 0, len(command.ChannelKeys))
	for _, key := range command.ChannelKeys {
		var selectedBinding gwbindings.Binding
		for _, binding := range bindings {
			if binding.ChannelKey == key && (command.AccountID == "" || binding.AccountID == command.AccountID) {
				selectedBinding = binding
				break
			}
		}
		if selectedBinding.ID == "" {
			return nil, fmt.Errorf("%w: %s", ErrBindingMissing, key)
		}
		selected = append(selected, selectedBinding)
	}
	now := s.now().UTC()
	deliveries := make([]Delivery, 0, len(selected))
	for _, binding := range selected {
		deliveryID, err := newDeliveryID()
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, Delivery{
			ID: deliveryID, TenantID: command.TenantID, ChannelKey: binding.ChannelKey, AccountID: binding.AccountID,
			BindingID: binding.ID, HumanProfileID: binding.HumanProfileID, ExternalTenantID: binding.ExternalTenantID,
			RecipientID: binding.ExternalUserID, EventType: command.EventType, Title: command.Title, Body: command.Body,
			Format: command.Format, IdempotencyKey: command.IdempotencyKey, Status: "pending", CreatedAt: now, UpdatedAt: now,
		})
	}
	err = s.jobs.WithTx(ctx, func(tx platformdb.Tx) error {
		txOutbox := outbox.NewTx(tx)
		fingerprint, err := commandFingerprint(command)
		if err != nil {
			return err
		}
		requestInsert, err := tx.ExecContext(ctx, `INSERT INTO gateway_message_delivery_requests (
			tenant_id, human_profile_id, idempotency_key, command_fingerprint, created_at
		) VALUES (?, ?, ?, ?, ?) ON CONFLICT (tenant_id, human_profile_id, idempotency_key) DO NOTHING`,
			command.TenantID, command.HumanProfileID, command.IdempotencyKey, fingerprint, now.UnixNano())
		if err != nil {
			return fmt.Errorf("create gateway delivery request: %w", err)
		}
		requestAffected, err := requestInsert.RowsAffected()
		if err != nil {
			return fmt.Errorf("create gateway delivery request rows affected: %w", err)
		}
		if requestAffected == 0 {
			var existingFingerprint string
			if err := tx.QueryRowContext(ctx, `SELECT command_fingerprint FROM gateway_message_delivery_requests
				WHERE tenant_id = ? AND human_profile_id = ? AND idempotency_key = ?`,
				command.TenantID, command.HumanProfileID, command.IdempotencyKey).Scan(&existingFingerprint); err != nil {
				return fmt.Errorf("load idempotent gateway delivery request: %w", err)
			}
			if existingFingerprint != fingerprint {
				return ErrIdempotencyConflict
			}
		}
		for index := range deliveries {
			delivery := deliveries[index]
			result, err := tx.ExecContext(ctx, `INSERT INTO gateway_message_deliveries (
				id, tenant_id, channel_key, account_id, binding_id, human_profile_id, external_tenant_id, recipient_id,
				event_type, title, body, message_format, idempotency_key, status, attempts, external_message_id,
				last_error, created_at, updated_at, delivered_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, '', '', ?, ?, 0)
			ON CONFLICT (tenant_id, channel_key, binding_id, idempotency_key) DO NOTHING`,
				delivery.ID, delivery.TenantID, delivery.ChannelKey, delivery.AccountID, delivery.BindingID, delivery.HumanProfileID,
				delivery.ExternalTenantID, delivery.RecipientID, delivery.EventType, delivery.Title, delivery.Body, delivery.Format,
				delivery.IdempotencyKey, now.UnixNano(), now.UnixNano())
			if err != nil {
				return fmt.Errorf("create gateway delivery: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("create gateway delivery rows affected: %w", err)
			}
			if affected == 0 {
				var existingID, existingHumanID, existingAccountID, existingTenantID, existingRecipientID string
				var existingEventType, existingTitle, existingBody, existingFormat string
				if err := tx.QueryRowContext(ctx, `SELECT id, human_profile_id, account_id, external_tenant_id, recipient_id,
					event_type, title, body, message_format FROM gateway_message_deliveries
					WHERE tenant_id = ? AND channel_key = ? AND binding_id = ? AND idempotency_key = ?`,
					delivery.TenantID, delivery.ChannelKey, delivery.BindingID, delivery.IdempotencyKey).Scan(
					&existingID, &existingHumanID, &existingAccountID, &existingTenantID, &existingRecipientID,
					&existingEventType, &existingTitle, &existingBody, &existingFormat,
				); err != nil {
					return fmt.Errorf("load idempotent gateway delivery: %w", err)
				}
				if existingHumanID != delivery.HumanProfileID || existingAccountID != delivery.AccountID ||
					existingTenantID != delivery.ExternalTenantID || existingRecipientID != delivery.RecipientID ||
					existingEventType != delivery.EventType || existingTitle != delivery.Title || existingBody != delivery.Body || existingFormat != delivery.Format {
					return ErrIdempotencyConflict
				}
				deliveries[index].ID = existingID
				continue
			}
			payload, _ := json.Marshal(map[string]string{"delivery_id": delivery.ID})
			jobID, err := newDeliveryJobID()
			if err != nil {
				return err
			}
			if err := txOutbox.Enqueue(ctx, outbox.Job{
				ID: jobID, Topic: Topic, Payload: payload,
				IdempotencyKey: "gateway-delivery:" + delivery.ID, MaxAttempts: 12, NextAttemptAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range deliveries {
		stored, loadErr := s.load(ctx, command.TenantID, deliveries[index].ID)
		if loadErr != nil {
			return nil, loadErr
		}
		deliveries[index] = stored
	}
	return deliveries, nil
}

// HasIdempotencyKey reports whether a durable delivery request already owns
// the key, without requiring the caller to regenerate its original content.
func (s *Service) HasIdempotencyKey(ctx context.Context, tenantID, humanProfileID, idempotencyKey string) (bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	humanProfileID = strings.TrimSpace(humanProfileID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || tenantID == "" || humanProfileID == "" || idempotencyKey == "" {
		return false, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return false, err
	}
	var exists int
	err := s.jobs.QueryRow(ctx, `SELECT 1 FROM gateway_message_delivery_requests
		WHERE tenant_id = ? AND human_profile_id = ? AND idempotency_key = ?`,
		tenantID, humanProfileID, idempotencyKey).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check gateway delivery idempotency key: %w", err)
	}
	return exists == 1, nil
}

func (s *Service) Get(ctx context.Context, tenantID, deliveryID string) (Delivery, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return Delivery{}, err
	}
	delivery, err := s.load(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(deliveryID))
	if err != nil {
		return Delivery{}, err
	}
	return s.reconcileTerminalOutboxState(ctx, delivery)
}

func (s *Service) reconcileTerminalOutboxState(ctx context.Context, delivery Delivery) (Delivery, error) {
	switch delivery.Status {
	case "delivered", "failed", "dead_letter":
		return delivery, nil
	}
	var jobStatus string
	err := s.jobs.QueryRow(ctx, `SELECT status FROM platform_outbox_jobs
		WHERE topic = ? AND idempotency_key = ?`, Topic, "gateway-delivery:"+delivery.ID).Scan(&jobStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery, nil
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("load gateway delivery outbox state: %w", err)
	}
	nextStatus := ""
	message := ""
	switch outbox.Status(jobStatus) {
	case outbox.StatusDeadLetter:
		nextStatus = "dead_letter"
		message = "delivery worker exhausted its retry budget"
	case outbox.StatusDone:
		nextStatus = "failed"
		message = "delivery worker completed without a channel receipt"
	default:
		return delivery, nil
	}
	result, err := s.jobs.Exec(ctx, `UPDATE gateway_message_deliveries SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status NOT IN ('delivered', 'failed', 'dead_letter')`,
		nextStatus, message, s.now().UTC().UnixNano(), delivery.ID)
	if err != nil {
		return Delivery{}, fmt.Errorf("reconcile gateway delivery outbox state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Delivery{}, fmt.Errorf("reconcile gateway delivery outbox state rows affected: %w", err)
	}
	if affected == 0 {
		return s.load(ctx, delivery.TenantID, delivery.ID)
	}
	delivery.Status = nextStatus
	delivery.LastError = message
	delivery.UpdatedAt = s.now().UTC()
	return delivery, nil
}

func (s *Service) HandleOutbox(ctx context.Context, job outbox.Job) error {
	if s == nil || s.jobs == nil {
		return errors.New("gateway delivery store is unavailable")
	}
	var payload struct {
		DeliveryID string `json:"delivery_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || strings.TrimSpace(payload.DeliveryID) == "" {
		return errors.New("invalid gateway delivery outbox payload")
	}
	delivery, err := s.load(ctx, "", payload.DeliveryID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("gateway delivery referenced by outbox job is missing: %w", err)
		}
		return err
	}
	if delivery.Status == "delivered" || delivery.Status == "failed" || delivery.Status == "dead_letter" {
		return nil
	}
	attempt := job.Attempts + 1
	claimed, err := s.claimDeliveryAttempt(ctx, delivery.ID, attempt)
	if err != nil {
		return err
	}
	if !claimed {
		current, loadErr := s.load(ctx, "", delivery.ID)
		if loadErr == nil && (current.Status == "delivered" || current.Status == "failed" || current.Status == "dead_letter") {
			return nil
		}
		return retryableDeliveryFailure(ErrInProgress, attempt)
	}
	s.validatorMu.RLock()
	validate := s.validator
	s.validatorMu.RUnlock()
	if validate != nil {
		validationCtx, cancel := context.WithTimeout(ctx, deliveryBindingTimeout)
		eligible, validationErr := validate(validationCtx, delivery)
		cancel()
		if validationErr != nil {
			if err := s.recordDeliveryFailure(ctx, delivery, attempt, "eligibility_unavailable", "notification eligibility is temporarily unavailable", retryStatus(attempt, job.MaxAttempts), validationErr); err != nil {
				return err
			}
			return s.retryableRecordedFailure(errors.New("notification eligibility is temporarily unavailable"), attempt)
		}
		if !eligible {
			return s.updateClaimedFailure(ctx, delivery.ID, attempt, "notification is no longer actionable or allowed", "failed")
		}
	}
	if s.bindings == nil {
		err = errors.New("gateway binding store is unavailable")
		if updateErr := s.recordDeliveryFailure(ctx, delivery, attempt, "binding_unavailable", "binding validation is temporarily unavailable", retryStatus(attempt, job.MaxAttempts), err); updateErr != nil {
			return updateErr
		}
		return s.retryableRecordedFailure(errors.New("gateway delivery binding validation is temporarily unavailable"), attempt)
	}
	bindingCtx, bindingCancel := context.WithTimeout(ctx, deliveryBindingTimeout)
	binding, err := s.bindings.Resolve(bindingCtx, gwbindings.ExternalIdentity{
		ChannelKey: delivery.ChannelKey, AccountID: delivery.AccountID, ExternalTenantID: delivery.ExternalTenantID, ExternalUserID: delivery.RecipientID,
	})
	bindingCancel()
	if err != nil {
		if errors.Is(err, gwbindings.ErrNotFound) {
			return s.recordDeliveryFailure(ctx, delivery, attempt, "binding_missing", "binding is missing, revoked, or outside tenant", "failed", err)
		}
		if updateErr := s.recordDeliveryFailure(ctx, delivery, attempt, "binding_unavailable", "binding validation is temporarily unavailable", retryStatus(attempt, job.MaxAttempts), err); updateErr != nil {
			return updateErr
		}
		return s.retryableRecordedFailure(errors.New("gateway delivery binding validation is temporarily unavailable"), attempt)
	}
	if binding.ID != delivery.BindingID || binding.TenantID != delivery.TenantID || binding.HumanProfileID != delivery.HumanProfileID {
		return s.recordDeliveryFailure(ctx, delivery, attempt, "binding_mismatch", "binding is missing, revoked, or outside tenant", "failed", ErrBindingMissing)
	}
	if s.sender == nil {
		err = errors.New("gateway outbound sender is unavailable")
		if updateErr := s.recordDeliveryFailure(ctx, delivery, attempt, "sender_unavailable", "channel delivery is temporarily unavailable", retryStatus(attempt, job.MaxAttempts), err); updateErr != nil {
			return updateErr
		}
		return s.retryableRecordedFailure(errors.New("gateway channel delivery is temporarily unavailable"), attempt)
	}
	sendCtx, cancel := context.WithTimeout(ctx, deliverySendTimeout)
	result, err := s.sender.Send(sendCtx, channel.Delivery{
		ID: delivery.ID, ChannelKey: delivery.ChannelKey, AccountID: delivery.AccountID, RecipientID: delivery.RecipientID,
		Title: delivery.Title, Text: delivery.Body, Format: delivery.Format, IdempotencyKey: delivery.IdempotencyKey,
	})
	cancel()
	if err != nil {
		if channel.IsPermanentDeliveryError(err) {
			return s.recordDeliveryFailure(ctx, delivery, attempt, "channel_rejected", "the channel rejected this delivery", "failed", err)
		}
		if updateErr := s.recordDeliveryFailure(ctx, delivery, attempt, "channel_send_failed", "channel delivery attempt failed", retryStatus(attempt, job.MaxAttempts), err); updateErr != nil {
			return updateErr
		}
		return s.retryableRecordedFailure(errors.New("gateway channel delivery attempt failed"), attempt)
	}
	if strings.TrimSpace(result.ExternalMessageID) == "" {
		err = errors.New("gateway outbound sender returned no external receipt")
		return s.recordDeliveryFailure(ctx, delivery, attempt, "receipt_missing", "the channel returned no delivery receipt", "failed", err)
	}
	now := s.now().UTC()
	update, err := s.jobs.Exec(ctx, `UPDATE gateway_message_deliveries SET status = 'delivered', external_message_id = ?,
		last_error = '', delivered_at = ?, updated_at = ? WHERE id = ? AND status = 'sending' AND attempts = ?`,
		result.ExternalMessageID, now.UnixNano(), now.UnixNano(), delivery.ID, attempt)
	if err != nil {
		return err
	}
	affected, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("gateway delivery completion lost its sending claim")
	}
	return nil
}

func (s *Service) claimDeliveryAttempt(ctx context.Context, id string, attempt int) (bool, error) {
	now := s.now().UTC()
	staleBefore := now.Add(-deliverySendLease).UnixNano()
	result, err := s.jobs.Exec(ctx, `UPDATE gateway_message_deliveries SET status = 'sending', attempts = ?, last_error = '', updated_at = ?
		WHERE id = ? AND attempts <= ? AND (
			status IN ('pending', 'retrying') OR (status = 'sending' AND updated_at <= ?)
		)`, attempt, now.UnixNano(), id, attempt-1, staleBefore)
	if err != nil {
		return false, fmt.Errorf("claim gateway delivery attempt: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim gateway delivery attempt rows affected: %w", err)
	}
	return affected == 1, nil
}

func (s *Service) load(ctx context.Context, tenantID, deliveryID string) (Delivery, error) {
	query := `SELECT id, tenant_id, channel_key, account_id, binding_id, human_profile_id, external_tenant_id, recipient_id,
		event_type, title, body, message_format, idempotency_key, status, attempts, external_message_id, last_error,
		created_at, updated_at, delivered_at FROM gateway_message_deliveries WHERE id = ?`
	args := []any{deliveryID}
	if tenantID != "" {
		query += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
	delivery, err := scanDelivery(s.jobs.QueryRow(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrNotFound
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("load gateway delivery: %w", err)
	}
	return delivery, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDelivery(row rowScanner) (Delivery, error) {
	var delivery Delivery
	var createdAt, updatedAt, deliveredAt int64
	err := row.Scan(
		&delivery.ID, &delivery.TenantID, &delivery.ChannelKey, &delivery.AccountID, &delivery.BindingID, &delivery.HumanProfileID,
		&delivery.ExternalTenantID, &delivery.RecipientID, &delivery.EventType, &delivery.Title, &delivery.Body, &delivery.Format,
		&delivery.IdempotencyKey, &delivery.Status, &delivery.Attempts, &delivery.ExternalMessageID, &delivery.LastError,
		&createdAt, &updatedAt, &deliveredAt,
	)
	if err != nil {
		return Delivery{}, err
	}
	delivery.CreatedAt = time.Unix(0, createdAt).UTC()
	delivery.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if deliveredAt > 0 {
		delivery.DeliveredAt = time.Unix(0, deliveredAt).UTC()
	}
	return delivery, nil
}

func (s *Service) loadIdempotentRequest(ctx context.Context, command EnqueueCommand) ([]Delivery, bool, error) {
	expectedFingerprint, err := commandFingerprint(command)
	if err != nil {
		return nil, false, err
	}
	var fingerprint string
	err = s.jobs.QueryRow(ctx, `SELECT command_fingerprint FROM gateway_message_delivery_requests
		WHERE tenant_id = ? AND human_profile_id = ? AND idempotency_key = ?`,
		command.TenantID, command.HumanProfileID, command.IdempotencyKey).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load gateway delivery request: %w", err)
	}
	if fingerprint != expectedFingerprint {
		return nil, true, ErrIdempotencyConflict
	}
	rows, err := s.jobs.Query(ctx, `SELECT id, tenant_id, channel_key, account_id, binding_id, human_profile_id,
		external_tenant_id, recipient_id, event_type, title, body, message_format, idempotency_key, status, attempts,
		external_message_id, last_error, created_at, updated_at, delivered_at FROM gateway_message_deliveries
		WHERE tenant_id = ? AND human_profile_id = ? AND idempotency_key = ? ORDER BY channel_key`,
		command.TenantID, command.HumanProfileID, command.IdempotencyKey)
	if err != nil {
		return nil, true, fmt.Errorf("load idempotent gateway deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := make([]Delivery, 0, len(command.ChannelKeys))
	for rows.Next() {
		delivery, scanErr := scanDelivery(rows)
		if scanErr != nil {
			return nil, true, fmt.Errorf("scan idempotent gateway delivery: %w", scanErr)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("iterate idempotent gateway deliveries: %w", err)
	}
	if len(deliveries) != len(command.ChannelKeys) {
		return nil, true, errors.New("gateway delivery request is missing durable channel deliveries")
	}
	for index, delivery := range deliveries {
		if delivery.ChannelKey != command.ChannelKeys[index] {
			return nil, true, errors.New("gateway delivery request channel set is inconsistent")
		}
	}
	return deliveries, true, nil
}

func (s *Service) updateClaimedFailure(ctx context.Context, id string, attempt int, message, status string) error {
	result, err := s.jobs.Exec(ctx, `UPDATE gateway_message_deliveries SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status = 'sending' AND attempts = ?`, status, message, s.now().UTC().UnixNano(), id, attempt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("gateway delivery failure update lost its sending claim")
	}
	return nil
}

func retryStatus(attempt, maxAttempts int) string {
	if maxAttempts > 0 && attempt >= maxAttempts {
		return "dead_letter"
	}
	return "retrying"
}

func retryableDeliveryFailure(err error, attempt int) error {
	if err == nil {
		return nil
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := 5 * time.Second
	for index := 1; index < attempt && delay < 15*time.Minute; index++ {
		delay *= 2
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
	}
	return &retryableDeliveryError{err: err, delay: delay}
}

func normalizeCommand(command EnqueueCommand) EnqueueCommand {
	command.TenantID = strings.TrimSpace(command.TenantID)
	command.HumanProfileID = strings.TrimSpace(command.HumanProfileID)
	command.AccountID = strings.TrimSpace(command.AccountID)
	command.EventType = strings.TrimSpace(command.EventType)
	if command.EventType == "" {
		command.EventType = "manual"
	}
	command.Title = strings.TrimSpace(command.Title)
	command.Body = strings.TrimSpace(command.Body)
	command.Format = strings.ToLower(strings.TrimSpace(command.Format))
	if command.Format == "" {
		command.Format = "text"
	}
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	seen := map[string]struct{}{}
	var keys []string
	for _, key := range command.ChannelKeys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	command.ChannelKeys = keys
	sort.Strings(command.ChannelKeys)
	return command
}

func commandFingerprint(command EnqueueCommand) (string, error) {
	payload, err := json.Marshal(struct {
		Channels  []string `json:"channels"`
		AccountID string   `json:"account_id,omitempty"`
		EventType string   `json:"event_type"`
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Format    string   `json:"format"`
	}{
		Channels: command.ChannelKeys, AccountID: command.AccountID, EventType: command.EventType, Title: command.Title, Body: command.Body, Format: command.Format,
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint gateway delivery request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func validCommand(command EnqueueCommand) bool {
	if command.TenantID == "" || command.HumanProfileID == "" || command.Body == "" || command.IdempotencyKey == "" || len(command.ChannelKeys) == 0 {
		return false
	}
	if command.Format != "text" && command.Format != "markdown" {
		return false
	}
	if len(command.ChannelKeys) > 3 {
		return false
	}
	if command.AccountID != "" && len(command.ChannelKeys) != 1 {
		return false
	}
	for _, key := range command.ChannelKeys {
		if key != "weixin" && key != "feishu" && key != "dingtalk" {
			return false
		}
	}
	switch command.EventType {
	case "manual", "mission_status", "approval", "blocked", "mention", "qa":
	default:
		return false
	}
	return len([]rune(command.TenantID)) <= 256 && len([]rune(command.HumanProfileID)) <= 256 && len([]rune(command.AccountID)) <= 256 &&
		len([]rune(command.Title)) <= 1000 && len([]rune(command.Body)) <= MaxDeliveryBodyRunes && len([]rune(command.IdempotencyKey)) <= 512
}

func newDeliveryID() (string, error)    { return randomID("gwd_") }
func newDeliveryJobID() (string, error) { return randomID("gwjob_") }

func randomID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate gateway delivery id: %w", err)
	}
	return prefix + hex.EncodeToString(raw), nil
}
