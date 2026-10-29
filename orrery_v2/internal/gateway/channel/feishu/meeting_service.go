package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

const (
	maxReservationWindow = 30 * 24 * time.Hour
	maxRawMediaRetention = 24 * time.Hour
	maxReconcileSessions = 100
)

type MeetingServiceOptions struct {
	Repository  MeetingRepositoryStore
	Providers   MeetingProviderRegistry
	Queue       MeetingRecordingQueue
	Clock       clock.Clock
	IDGenerator idgen.ErrorGenerator
}

type MeetingService struct {
	repository MeetingRepositoryStore
	providers  MeetingProviderRegistry
	queue      MeetingRecordingQueue
	clock      clock.Clock
	ids        idgen.ErrorGenerator
}

func NewMeetingService(options MeetingServiceOptions) *MeetingService {
	serviceClock := options.Clock
	if serviceClock == nil {
		serviceClock = clock.Real{}
	}
	ids := options.IDGenerator
	if ids == nil {
		ids = idgen.NewRandom("meeting")
	}
	return &MeetingService{
		repository: options.Repository,
		providers:  options.Providers,
		queue:      options.Queue,
		clock:      serviceClock,
		ids:        ids,
	}
}

func (s *MeetingService) CreateManagedMeeting(ctx context.Context, request CreateMeetingRequest) (MeetingSession, error) {
	request = normalizeCreateRequest(request)
	if err := s.validateCreateRequest(request); err != nil {
		return MeetingSession{}, err
	}
	policy, found, err := s.repository.GetPolicy(ctx, request.TenantID, request.Provider, request.AccountID)
	if err != nil {
		return MeetingSession{}, err
	}
	if !found || !policy.Enabled {
		return MeetingSession{}, ErrAutomationDisabled
	}
	policy = normalizePolicy(policy)
	if err := validatePolicy(policy); err != nil {
		return MeetingSession{}, err
	}
	if policy.TenantID != request.TenantID || policy.Provider != request.Provider || policy.AccountID != request.AccountID {
		return MeetingSession{}, ErrForbidden
	}
	if !policy.allowsChat(request.SourceChatID) || !policy.allowsActor(request.RequestedBy) {
		return MeetingSession{}, ErrForbidden
	}
	provider, ok := s.providers.ResolveProvider(request.Provider, request.AccountID)
	if !ok || provider == nil {
		return MeetingSession{}, ErrDependency
	}

	now := s.clock.Now().UTC()
	sessionID, err := s.ids.NewIDContext()
	if err != nil || strings.TrimSpace(sessionID) == "" {
		return MeetingSession{}, fmt.Errorf("generate meeting session id: %w", errors.Join(ErrDependency, err))
	}
	session := MeetingSession{
		ID: sessionID, TenantID: request.TenantID, Provider: request.Provider, AccountID: request.AccountID,
		ProviderTenantKey: request.ProviderTenantKey, SourceChatID: request.SourceChatID,
		RequestedBy: request.RequestedBy, OwnerExternalID: request.OwnerExternalID, Topic: request.Topic,
		SourceContextKind: request.SourceContextKind, SourceContextID: request.SourceContextID,
		IdempotencyKey: request.IdempotencyKey, ExpiresAt: request.ExpiresAt,
		Status: StatusCreating, Policy: snapshotPolicy(policy), CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	stored, created, err := s.repository.CreateSession(ctx, session)
	if err != nil {
		return MeetingSession{}, err
	}
	if !created {
		return stored, nil
	}

	reservation, err := provider.CreateReservation(ctx, MeetingReservationRequest{
		AccountID: request.AccountID, OwnerExternalID: request.OwnerExternalID,
		Topic: request.Topic, ExpiresAt: request.ExpiresAt, AutoRecord: true,
	})
	if err != nil {
		stored.Status = StatusReservationFailed
		stored.LastErrorCode = safeProviderErrorCode(err)
		stored.UpdatedAt = s.clock.Now().UTC()
		stored.Version++
		_, _ = s.repository.CompareAndSaveSession(context.WithoutCancel(ctx), stored, stored.Version-1)
		return stored, err
	}
	reservation.ID = strings.TrimSpace(reservation.ID)
	reservation.MeetingNo = strings.TrimSpace(reservation.MeetingNo)
	reservation.MeetingURL = strings.TrimSpace(reservation.MeetingURL)
	if reservation.ID == "" || reservation.MeetingURL == "" {
		return s.failReservation(ctx, stored, "incomplete_provider_response", ErrDependency)
	}
	stored.ReservationID = reservation.ID
	stored.MeetingNo = reservation.MeetingNo
	stored.MeetingURL = reservation.MeetingURL
	stored.Status = StatusReserved
	stored.LastErrorCode = ""
	stored.UpdatedAt = s.clock.Now().UTC()
	stored.Version++
	updated, err := s.repository.CompareAndSaveSession(ctx, stored, stored.Version-1)
	if err != nil {
		return MeetingSession{}, err
	}
	if !updated {
		return MeetingSession{}, ErrConflict
	}
	return stored, nil
}

func (s *MeetingService) HandleProviderEvent(ctx context.Context, event MeetingEvent) (MeetingSession, bool, error) {
	event = normalizeProviderEvent(event)
	if err := validateProviderEvent(event); err != nil {
		return MeetingSession{}, false, err
	}
	if s == nil || s.repository == nil || s.providers == nil || s.clock == nil {
		return MeetingSession{}, false, ErrDependency
	}
	session, found, err := s.repository.FindSessionByMeeting(ctx, event.Provider, event.AccountID, event.ProviderTenantKey, event.MeetingID)
	if err != nil {
		return MeetingSession{}, false, err
	}
	if !found && event.MeetingNo != "" {
		session, found, err = s.repository.FindSessionByMeetingNo(ctx, event.Provider, event.AccountID, event.ProviderTenantKey, event.MeetingNo)
		if err != nil {
			return MeetingSession{}, false, err
		}
	}
	if !found && event.Type == ProviderEventStarted {
		session, found, err = s.reconcileStartedMeeting(ctx, event)
		if err != nil {
			return MeetingSession{}, false, err
		}
	}
	if !found {
		return MeetingSession{}, false, nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		next, changed, err := applyProviderEvent(session, event, s.clock.Now().UTC())
		if err != nil {
			return MeetingSession{}, true, err
		}
		if changed {
			updated, err := s.repository.CompareAndSaveSession(ctx, next, session.Version)
			if err != nil {
				return MeetingSession{}, true, err
			}
			if !updated {
				session, found, err = s.repository.FindSessionByMeeting(ctx, event.Provider, event.AccountID, event.ProviderTenantKey, event.MeetingID)
				if err != nil {
					return MeetingSession{}, true, err
				}
				if !found {
					return MeetingSession{}, false, nil
				}
				continue
			}
			session = next
		}
		if event.Type == ProviderEventRecordingReady {
			if s.queue == nil {
				return MeetingSession{}, true, ErrDependency
			}
			_, err := s.queue.EnqueueRecording(ctx, recordingJobForSession(session))
			if err != nil {
				return MeetingSession{}, true, err
			}
		}
		return session, true, nil
	}
	return MeetingSession{}, true, ErrConflict
}

func (s *MeetingService) reconcileStartedMeeting(ctx context.Context, event MeetingEvent) (MeetingSession, bool, error) {
	provider, ok := s.providers.ResolveProvider(event.Provider, event.AccountID)
	if !ok || provider == nil {
		return MeetingSession{}, false, ErrDependency
	}
	candidates, err := s.repository.ListUnboundSessions(ctx, event.Provider, event.AccountID, event.ProviderTenantKey, maxReconcileSessions)
	if err != nil {
		return MeetingSession{}, false, err
	}
	for _, candidate := range candidates {
		active, found, err := provider.LookupActiveMeeting(ctx, candidate.AccountID, candidate.ReservationID)
		if err != nil {
			return MeetingSession{}, false, err
		}
		if !found || strings.TrimSpace(active.ID) != event.MeetingID {
			continue
		}
		candidate.MeetingID = event.MeetingID
		candidate.Status = StatusInProgress
		candidate.StartedAt = firstTime(event.OccurredAt, active.StartedAt)
		candidate.UpdatedAt = s.clock.Now().UTC()
		candidate.Version++
		updated, err := s.repository.CompareAndSaveSession(ctx, candidate, candidate.Version-1)
		if err != nil {
			return MeetingSession{}, false, err
		}
		if updated {
			return candidate, true, nil
		}
		bound, found, err := s.repository.FindSessionByMeeting(ctx, event.Provider, event.AccountID, event.ProviderTenantKey, event.MeetingID)
		return bound, found, err
	}
	return MeetingSession{}, false, nil
}

func (s *MeetingService) validateCreateRequest(request CreateMeetingRequest) error {
	if s == nil || s.repository == nil || s.providers == nil || s.clock == nil || s.ids == nil {
		return ErrDependency
	}
	if request.TenantID == "" || request.Provider == "" || request.AccountID == "" || request.ProviderTenantKey == "" ||
		request.SourceChatID == "" || request.RequestedBy == "" || request.OwnerExternalID == "" || request.Topic == "" || request.IdempotencyKey == "" {
		return ErrInvalidRequest
	}
	now := s.clock.Now().UTC()
	if !request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(maxReservationWindow)) {
		return ErrInvalidRequest
	}
	return nil
}

func (s *MeetingService) failReservation(ctx context.Context, session MeetingSession, code string, cause error) (MeetingSession, error) {
	session.Status = StatusReservationFailed
	session.LastErrorCode = code
	session.UpdatedAt = s.clock.Now().UTC()
	session.Version++
	_, _ = s.repository.CompareAndSaveSession(context.WithoutCancel(ctx), session, session.Version-1)
	return session, cause
}

func normalizeCreateRequest(request CreateMeetingRequest) CreateMeetingRequest {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.ProviderTenantKey = strings.TrimSpace(request.ProviderTenantKey)
	request.SourceChatID = strings.TrimSpace(request.SourceChatID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	request.OwnerExternalID = strings.TrimSpace(request.OwnerExternalID)
	request.Topic = strings.TrimSpace(request.Topic)
	request.SourceContextKind = strings.TrimSpace(request.SourceContextKind)
	request.SourceContextID = strings.TrimSpace(request.SourceContextID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.ExpiresAt = request.ExpiresAt.UTC()
	return request
}

func normalizePolicy(policy MeetingPolicy) MeetingPolicy {
	policy.TenantID = strings.TrimSpace(policy.TenantID)
	policy.Provider = strings.ToLower(strings.TrimSpace(policy.Provider))
	policy.AccountID = strings.TrimSpace(policy.AccountID)
	policy.SummaryAgentID = strings.TrimSpace(policy.SummaryAgentID)
	policy.PublishTarget = strings.ToLower(strings.TrimSpace(policy.PublishTarget))
	if policy.PublishTarget == "" {
		policy.PublishTarget = PublishSourceChat
	}
	policy.AllowedChatIDs = normalizeIdentities(policy.AllowedChatIDs)
	policy.AllowedActorIDs = normalizeIdentities(policy.AllowedActorIDs)
	return policy
}

func validatePolicy(policy MeetingPolicy) error {
	if policy.TenantID == "" || policy.Provider == "" || policy.AccountID == "" || policy.SummaryAgentID == "" ||
		len(policy.AllowedChatIDs) == 0 || len(policy.AllowedActorIDs) == 0 {
		return ErrInvalidRequest
	}
	if policy.PublishTarget != PublishSourceChat && policy.PublishTarget != PublishOrreryOnly {
		return ErrInvalidRequest
	}
	if policy.RawMediaRetention < 0 || policy.RawMediaRetention > maxRawMediaRetention || policy.MaxMeetingDuration < 0 || policy.DailyTranscriptionTime < 0 {
		return ErrInvalidRequest
	}
	return nil
}

func snapshotPolicy(policy MeetingPolicy) MeetingPolicySnapshot {
	return MeetingPolicySnapshot{
		SummaryAgentID: policy.SummaryAgentID, PublishTarget: policy.PublishTarget,
		RawMediaRetention: policy.RawMediaRetention, MaxMeetingDuration: policy.MaxMeetingDuration,
		DailyTranscriptionTime: policy.DailyTranscriptionTime, PolicyVersion: policy.Version,
	}
}

func normalizeIdentities(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeProviderEvent(event MeetingEvent) MeetingEvent {
	event.Provider = strings.ToLower(strings.TrimSpace(event.Provider))
	event.AccountID = strings.TrimSpace(event.AccountID)
	event.ProviderTenantKey = strings.TrimSpace(event.ProviderTenantKey)
	event.EventID = strings.TrimSpace(event.EventID)
	event.MeetingID = strings.TrimSpace(event.MeetingID)
	event.MeetingNo = strings.TrimSpace(event.MeetingNo)
	event.Topic = strings.TrimSpace(event.Topic)
	event.MinuteToken = strings.TrimSpace(event.MinuteToken)
	event.OccurredAt = event.OccurredAt.UTC()
	return event
}

func validateProviderEvent(event MeetingEvent) error {
	if event.Provider == "" || event.AccountID == "" || event.ProviderTenantKey == "" || event.EventID == "" || event.MeetingID == "" {
		return ErrInvalidRequest
	}
	if event.OccurredAt.IsZero() {
		return ErrInvalidRequest
	}
	switch event.Type {
	case ProviderEventStarted, ProviderEventEnded:
		return nil
	case ProviderEventRecordingReady:
		if event.MinuteToken == "" || event.RecordingDuration < 0 {
			return ErrInvalidRequest
		}
		return nil
	default:
		return ErrInvalidRequest
	}
}

func applyProviderEvent(session MeetingSession, event MeetingEvent, now time.Time) (MeetingSession, bool, error) {
	if session.Provider != event.Provider || session.AccountID != event.AccountID || session.ProviderTenantKey != event.ProviderTenantKey ||
		(session.MeetingID != "" && session.MeetingID != event.MeetingID) ||
		(session.MeetingNo != "" && event.MeetingNo != "" && session.MeetingNo != event.MeetingNo) {
		return MeetingSession{}, false, ErrForbidden
	}
	if session.Status == StatusDeleted || session.Status == StatusReservationFailed || session.Status == StatusFailedTerminal {
		return session, false, nil
	}
	next := session
	if next.MeetingID == "" {
		next.MeetingID = event.MeetingID
	}
	if next.MeetingNo == "" {
		next.MeetingNo = event.MeetingNo
	}
	switch event.Type {
	case ProviderEventStarted:
		if statusRank(next.Status) < statusRank(StatusInProgress) {
			next.Status = StatusInProgress
		}
		if next.StartedAt.IsZero() {
			next.StartedAt = event.OccurredAt
		}
	case ProviderEventEnded:
		if statusRank(next.Status) < statusRank(StatusAwaitingRecording) {
			next.Status = StatusAwaitingRecording
		}
		if next.EndedAt.IsZero() {
			next.EndedAt = event.OccurredAt
		}
	case ProviderEventRecordingReady:
		if next.MinuteToken != "" && next.MinuteToken != event.MinuteToken {
			return MeetingSession{}, false, ErrConflict
		}
		if statusRank(next.Status) < statusRank(StatusRecordingReady) {
			next.Status = StatusRecordingReady
		}
		next.MinuteToken = event.MinuteToken
		next.RecordingDuration = event.RecordingDuration
		if next.RecordingReadyAt.IsZero() {
			next.RecordingReadyAt = event.OccurredAt
		}
	default:
		return MeetingSession{}, false, ErrInvalidRequest
	}
	changed := next != session
	if changed {
		next.UpdatedAt = now
		next.Version++
	}
	return next, changed, nil
}

func statusRank(status MeetingStatus) int {
	switch status {
	case StatusCreating:
		return 0
	case StatusReserved:
		return 1
	case StatusInProgress:
		return 2
	case StatusAwaitingRecording:
		return 3
	case StatusRecordingReady:
		return 4
	case StatusDownloading:
		return 5
	case StatusTranscribing:
		return 6
	case StatusSummarizing:
		return 7
	case StatusDraftReady:
		return 8
	case StatusPublished:
		return 9
	default:
		return -1
	}
}

func recordingJobForSession(session MeetingSession) MeetingRecordingJob {
	sum := sha256.Sum256([]byte(session.TenantID + "\x00" + session.ID + "\x00" + session.MinuteToken))
	return MeetingRecordingJob{
		ID: "meeting_recording_" + hex.EncodeToString(sum[:16]), TenantID: session.TenantID,
		SessionID: session.ID, Provider: session.Provider, AccountID: session.AccountID,
		MinuteToken: session.MinuteToken, SummaryAgentID: session.Policy.SummaryAgentID,
		PublishTarget: session.Policy.PublishTarget, SourceChatID: session.SourceChatID,
		RawMediaRetention: session.Policy.RawMediaRetention,
	}
}

func safeProviderErrorCode(err error) string {
	var coded interface{ ProviderErrorCode() string }
	if errors.As(err, &coded) {
		if code := strings.TrimSpace(coded.ProviderErrorCode()); code != "" {
			return code
		}
	}
	return "provider_request_failed"
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

type StaticMeetingProviderRegistry map[string]MeetingProvider

func (r StaticMeetingProviderRegistry) ResolveProvider(provider, accountID string) (MeetingProvider, bool) {
	value, ok := r[strings.ToLower(strings.TrimSpace(provider))+"\x00"+strings.TrimSpace(accountID)]
	return value, ok
}

func MeetingProviderRegistryKey(provider, accountID string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(accountID)
}
