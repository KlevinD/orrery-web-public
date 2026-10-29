package inbound

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/channel"
	platformdb "orrery_v2/internal/platform/db"
)

const (
	inboundClaimLease = 5 * time.Minute
	inboundRetention  = 30 * 24 * time.Hour
)

var ErrInvalidInput = errors.New("gateway inbound event input is invalid")

type Store struct {
	db               *platformdb.Store
	now              func() time.Time
	schemaMu         sync.Mutex
	eventSchemaReady bool
	queueSchemaReady bool
}

func NewStore(db *platformdb.Store) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("gateway inbound event store is not configured")
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.eventSchemaReady {
		return nil
	}
	timeType := "INTEGER"
	if s.db.Dialect() == platformdb.DialectPostgres {
		timeType = "BIGINT"
	}
	for _, statement := range migrationStatements(timeType) {
		if _, err := s.db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure gateway inbound event schema: %w", err)
		}
	}
	s.eventSchemaReady = true
	return nil
}

func (s *Store) ClaimInboundEvent(ctx context.Context, channelKey, accountID, eventID string) (channel.InboundEventClaim, error) {
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	eventID = strings.TrimSpace(eventID)
	if !validInboundEventKey(channelKey, accountID, eventID) {
		return channel.InboundEventClaim{}, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return channel.InboundEventClaim{}, err
	}
	token, err := newClaimToken()
	if err != nil {
		return channel.InboundEventClaim{}, err
	}
	now := s.now().UTC()
	claim := channel.InboundEventClaim{
		ChannelKey: channelKey, AccountID: accountID, EventID: eventID, Token: token, State: channel.InboundEventAcquired,
	}
	result, err := s.db.Exec(ctx, `INSERT INTO gateway_inbound_events (
		channel_key, account_id, event_id, status, claim_token, lease_until, created_at, updated_at
	) VALUES (?, ?, ?, 'processing', ?, ?, ?, ?)
	ON CONFLICT (channel_key, account_id, event_id) DO NOTHING`,
		channelKey, accountID, eventID, token, now.Add(inboundClaimLease).UnixNano(), now.UnixNano(), now.UnixNano())
	if err != nil {
		return channel.InboundEventClaim{}, fmt.Errorf("claim gateway inbound event: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return channel.InboundEventClaim{}, fmt.Errorf("claim gateway inbound event rows affected: %w", err)
	}
	if affected == 1 {
		return claim, nil
	}

	result, err = s.db.Exec(ctx, `UPDATE gateway_inbound_events SET status = 'processing', claim_token = ?,
		lease_until = ?, updated_at = ? WHERE channel_key = ? AND account_id = ? AND event_id = ?
		AND status <> 'completed' AND lease_until <= ?`, token, now.Add(inboundClaimLease).UnixNano(), now.UnixNano(),
		channelKey, accountID, eventID, now.UnixNano())
	if err != nil {
		return channel.InboundEventClaim{}, fmt.Errorf("reclaim gateway inbound event: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return channel.InboundEventClaim{}, fmt.Errorf("reclaim gateway inbound event rows affected: %w", err)
	}
	if affected == 1 {
		return claim, nil
	}
	var status string
	if err := s.db.QueryRow(ctx, `SELECT status FROM gateway_inbound_events
		WHERE channel_key = ? AND account_id = ? AND event_id = ?`, channelKey, accountID, eventID).Scan(&status); err != nil {
		return channel.InboundEventClaim{}, fmt.Errorf("load gateway inbound event claim: %w", err)
	}
	claim.Token = ""
	if status == "completed" {
		claim.State = channel.InboundEventCompleted
	} else {
		claim.State = channel.InboundEventBusy
	}
	return claim, nil
}

func (s *Store) CompleteInboundEvent(ctx context.Context, claim channel.InboundEventClaim) error {
	if !validClaim(claim) {
		return ErrInvalidInput
	}
	now := s.now().UTC()
	if _, err := s.db.Exec(ctx, `DELETE FROM gateway_inbound_events WHERE status = 'completed' AND updated_at < ?`, now.Add(-inboundRetention).UnixNano()); err != nil {
		return fmt.Errorf("prune completed gateway inbound events: %w", err)
	}
	result, err := s.db.Exec(ctx, `UPDATE gateway_inbound_events SET status = 'completed', claim_token = '',
		lease_until = 0, updated_at = ? WHERE channel_key = ? AND account_id = ? AND event_id = ?
		AND status = 'processing' AND claim_token = ?`, now.UnixNano(), claim.ChannelKey, claim.AccountID, claim.EventID, claim.Token)
	if err != nil {
		return fmt.Errorf("complete gateway inbound event: %w", err)
	}
	if err := requireSingleAffected(result, "complete gateway inbound event"); err != nil {
		return err
	}
	return nil
}

func (s *Store) RenewInboundEvent(ctx context.Context, claim channel.InboundEventClaim) error {
	if !validClaim(claim) {
		return ErrInvalidInput
	}
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `UPDATE gateway_inbound_events SET lease_until = ?, updated_at = ?
		WHERE channel_key = ? AND account_id = ? AND event_id = ? AND status = 'processing' AND claim_token = ?`,
		now.Add(inboundClaimLease).UnixNano(), now.UnixNano(), claim.ChannelKey, claim.AccountID, claim.EventID, claim.Token)
	if err != nil {
		return fmt.Errorf("renew gateway inbound event: %w", err)
	}
	return requireSingleAffected(result, "renew gateway inbound event")
}

func (s *Store) ReleaseInboundEvent(ctx context.Context, claim channel.InboundEventClaim) error {
	if !validClaim(claim) {
		return ErrInvalidInput
	}
	result, err := s.db.Exec(ctx, `DELETE FROM gateway_inbound_events WHERE channel_key = ? AND account_id = ? AND event_id = ?
		AND status = 'processing' AND claim_token = ?`, claim.ChannelKey, claim.AccountID, claim.EventID, claim.Token)
	if err != nil {
		return fmt.Errorf("release gateway inbound event: %w", err)
	}
	return requireSingleAffected(result, "release gateway inbound event")
}

func validInboundEventKey(channelKey, accountID, eventID string) bool {
	return channelKey != "" && accountID != "" && eventID != "" && len([]rune(channelKey)) <= 64 &&
		len([]rune(accountID)) <= 512 && len([]rune(eventID)) <= 512
}

func validClaim(claim channel.InboundEventClaim) bool {
	return claim.State == channel.InboundEventAcquired && strings.TrimSpace(claim.Token) != "" &&
		validInboundEventKey(strings.TrimSpace(claim.ChannelKey), strings.TrimSpace(claim.AccountID), strings.TrimSpace(claim.EventID))
}

func newClaimToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate gateway inbound event claim token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

type rowsAffectedResult interface {
	RowsAffected() (int64, error)
}

func requireSingleAffected(result rowsAffectedResult, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf("%s lost its claim", operation)
	}
	return nil
}
