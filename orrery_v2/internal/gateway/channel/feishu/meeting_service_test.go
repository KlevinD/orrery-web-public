package feishu

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/migration"
)

type fakeProvider struct {
	reservation MeetingReservation
	active      ActiveMeeting
	activeFound bool
	createCalls int
	lookupCalls int
	createErr   error
}

func (p *fakeProvider) CreateReservation(context.Context, MeetingReservationRequest) (MeetingReservation, error) {
	p.createCalls++
	return p.reservation, p.createErr
}

func (p *fakeProvider) LookupActiveMeeting(context.Context, string, string) (ActiveMeeting, bool, error) {
	p.lookupCalls++
	return p.active, p.activeFound, nil
}

type fakeRecordingQueue struct {
	jobs map[string]MeetingRecordingJob
	err  error
}

func (q *fakeRecordingQueue) EnqueueRecording(_ context.Context, job MeetingRecordingJob) (bool, error) {
	if q.err != nil {
		return false, q.err
	}
	if q.jobs == nil {
		q.jobs = map[string]MeetingRecordingJob{}
	}
	if _, exists := q.jobs[job.ID]; exists {
		return false, nil
	}
	q.jobs[job.ID] = job
	return true, nil
}

func TestManagedMeetingLifecycleIsPolicyScopedAndMonotonic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	repository := newTestRepository(t)
	policy := MeetingPolicy{
		TenantID: "tenant-a", Provider: ProviderFeishu, AccountID: "feishu:app-a", Enabled: true,
		AllowedChatIDs: []string{"chat-a"}, AllowedActorIDs: []string{"user-a"},
		SummaryAgentID: "summary-agent", PublishTarget: PublishSourceChat,
		RawMediaRetention: time.Hour, MaxMeetingDuration: 4 * time.Hour, DailyTranscriptionTime: 8 * time.Hour,
		Version: 3, UpdatedAt: now,
	}
	if err := repository.SavePolicy(ctx, policy); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	provider := &fakeProvider{
		reservation: MeetingReservation{ID: "reserve-1", MeetingURL: "https://vc.feishu.cn/j/123"},
		active:      ActiveMeeting{ID: "meeting-1", Topic: "weekly", StartedAt: now.Add(time.Minute)}, activeFound: true,
	}
	queue := &fakeRecordingQueue{}
	service := NewMeetingService(MeetingServiceOptions{
		Repository: repository,
		Providers:  StaticMeetingProviderRegistry{MeetingProviderRegistryKey(ProviderFeishu, "feishu:app-a"): provider},
		Queue:      queue, Clock: clock.NewFake(now), IDGenerator: idgen.NewDeterministic("meeting"),
	})
	request := CreateMeetingRequest{
		TenantID: "tenant-a", Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a",
		SourceChatID: "chat-a", RequestedBy: "user-a", OwnerExternalID: "ou_owner", Topic: "weekly",
		ExpiresAt: now.Add(24 * time.Hour), IdempotencyKey: "command-1",
	}
	session, err := service.CreateManagedMeeting(ctx, request)
	if err != nil {
		t.Fatalf("CreateManagedMeeting: %v", err)
	}
	if session.Status != StatusReserved || session.ReservationID != "reserve-1" || session.Policy.PolicyVersion != 3 {
		t.Fatalf("reserved session = %+v", session)
	}
	if provider.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", provider.createCalls)
	}

	repeated, err := service.CreateManagedMeeting(ctx, request)
	if err != nil {
		t.Fatalf("repeat CreateManagedMeeting: %v", err)
	}
	if repeated.ID != session.ID || provider.createCalls != 1 {
		t.Fatalf("repeat session = %+v, create calls = %d", repeated, provider.createCalls)
	}

	startedAt := now.Add(time.Minute)
	started, handled, err := service.HandleProviderEvent(ctx, MeetingEvent{
		Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a",
		EventID: "event-started", Type: ProviderEventStarted, MeetingID: "meeting-1", OccurredAt: startedAt,
	})
	if err != nil || !handled {
		t.Fatalf("HandleProviderEvent(started): handled=%v err=%v", handled, err)
	}
	if started.Status != StatusInProgress || started.MeetingID != "meeting-1" || provider.lookupCalls != 1 {
		t.Fatalf("started session = %+v, lookups = %d", started, provider.lookupCalls)
	}

	endedAt := now.Add(time.Hour)
	ended, handled, err := service.HandleProviderEvent(ctx, MeetingEvent{
		Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a",
		EventID: "event-ended", Type: ProviderEventEnded, MeetingID: "meeting-1", OccurredAt: endedAt,
	})
	if err != nil || !handled || ended.Status != StatusAwaitingRecording {
		t.Fatalf("ended session = %+v handled=%v err=%v", ended, handled, err)
	}

	ready, handled, err := service.HandleProviderEvent(ctx, MeetingEvent{
		Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a",
		EventID: "event-ready", Type: ProviderEventRecordingReady, MeetingID: "meeting-1",
		MinuteToken: "testminute00000000000000", RecordingDuration: time.Hour, OccurredAt: endedAt.Add(time.Minute),
	})
	if err != nil || !handled || ready.Status != StatusRecordingReady {
		t.Fatalf("ready session = %+v handled=%v err=%v", ready, handled, err)
	}
	if len(queue.jobs) != 1 {
		t.Fatalf("recording jobs = %+v", queue.jobs)
	}
	for _, job := range queue.jobs {
		if job.SummaryAgentID != "summary-agent" || job.RawMediaRetention != time.Hour || job.MinuteToken == "" {
			t.Fatalf("recording job = %+v", job)
		}
	}

	late, handled, err := service.HandleProviderEvent(ctx, MeetingEvent{
		Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a",
		EventID: "event-ended-late", Type: ProviderEventEnded, MeetingID: "meeting-1", OccurredAt: endedAt,
	})
	if err != nil || !handled || late.Status != StatusRecordingReady {
		t.Fatalf("late ended event regressed session: %+v handled=%v err=%v", late, handled, err)
	}
}

func TestManagedMeetingFailsClosedOutsideBotPolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	repository := newTestRepository(t)
	if err := repository.SavePolicy(ctx, MeetingPolicy{
		TenantID: "tenant-a", Provider: ProviderFeishu, AccountID: "feishu:app-a", Enabled: true,
		AllowedChatIDs: []string{"chat-allowed"}, AllowedActorIDs: []string{"user-allowed"},
		SummaryAgentID: "summary-agent", PublishTarget: PublishSourceChat, Version: 1,
	}); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	provider := &fakeProvider{reservation: MeetingReservation{ID: "reserve", MeetingURL: "https://vc.feishu.cn/j/1"}}
	service := NewMeetingService(MeetingServiceOptions{
		Repository: repository,
		Providers:  StaticMeetingProviderRegistry{MeetingProviderRegistryKey(ProviderFeishu, "feishu:app-a"): provider},
		Clock:      clock.NewFake(now), IDGenerator: idgen.NewDeterministic("meeting"),
	})
	_, err := service.CreateManagedMeeting(ctx, CreateMeetingRequest{
		TenantID: "tenant-a", Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a",
		SourceChatID: "chat-denied", RequestedBy: "user-allowed", OwnerExternalID: "owner", Topic: "private",
		ExpiresAt: now.Add(time.Hour), IdempotencyKey: "denied",
	})
	if !errors.Is(err, ErrForbidden) || provider.createCalls != 0 {
		t.Fatalf("denied create error = %v, provider calls = %d", err, provider.createCalls)
	}

	_, found, err := repository.GetSession(ctx, "tenant-b", "meeting-000001")
	if err != nil || found {
		t.Fatalf("cross-tenant session lookup found=%v err=%v", found, err)
	}
}

func TestRecordingReadyQueueFailureIsRetryableWithoutDuplicateState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	repository := newTestRepository(t)
	session := MeetingSession{
		ID: "session-1", TenantID: "tenant-a", Provider: ProviderFeishu, AccountID: "feishu:app-a",
		ProviderTenantKey: "feishu-tenant-a", SourceChatID: "chat-a", RequestedBy: "user-a", OwnerExternalID: "owner",
		Topic: "review", ReservationID: "reserve-1", MeetingID: "meeting-1", Status: StatusAwaitingRecording,
		Policy:         MeetingPolicySnapshot{SummaryAgentID: "summary-agent", PublishTarget: PublishSourceChat},
		IdempotencyKey: "request-1", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if _, created, err := repository.CreateSession(ctx, session); err != nil || !created {
		t.Fatalf("CreateSession: created=%v err=%v", created, err)
	}
	queue := &fakeRecordingQueue{err: errors.New("queue unavailable")}
	service := NewMeetingService(MeetingServiceOptions{
		Repository: repository,
		Providers:  StaticMeetingProviderRegistry{MeetingProviderRegistryKey(ProviderFeishu, "feishu:app-a"): &fakeProvider{}},
		Queue:      queue, Clock: clock.NewFake(now),
	})
	event := MeetingEvent{
		Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a", EventID: "ready-1",
		Type: ProviderEventRecordingReady, MeetingID: "meeting-1", MinuteToken: "testminute00000000000000", OccurredAt: now,
	}
	if _, handled, err := service.HandleProviderEvent(ctx, event); err == nil || !handled {
		t.Fatalf("first ready event handled=%v err=%v", handled, err)
	}
	queue.err = nil
	stored, handled, err := service.HandleProviderEvent(ctx, event)
	if err != nil || !handled || stored.Status != StatusRecordingReady || len(queue.jobs) != 1 {
		t.Fatalf("retried ready event session=%+v handled=%v jobs=%d err=%v", stored, handled, len(queue.jobs), err)
	}
}

func TestRecordingReadyBindsByMeetingNumberWhenStartedEventWasMissed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	repository := newTestRepository(t)
	session := MeetingSession{
		ID: "session-by-number", TenantID: "tenant-a", Provider: ProviderFeishu, AccountID: "feishu:app-a",
		ProviderTenantKey: "feishu-tenant-a", SourceChatID: "chat-a", RequestedBy: "user-a", OwnerExternalID: "owner",
		Topic: "review", ReservationID: "reserve-1", MeetingNo: "123456789", Status: StatusReserved,
		Policy:         MeetingPolicySnapshot{SummaryAgentID: "summary-agent", PublishTarget: PublishSourceChat},
		IdempotencyKey: "request-by-number", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if _, created, err := repository.CreateSession(ctx, session); err != nil || !created {
		t.Fatalf("CreateSession: created=%v err=%v", created, err)
	}
	queue := &fakeRecordingQueue{}
	service := NewMeetingService(MeetingServiceOptions{
		Repository: repository,
		Providers:  StaticMeetingProviderRegistry{MeetingProviderRegistryKey(ProviderFeishu, "feishu:app-a"): &fakeProvider{}},
		Queue:      queue, Clock: clock.NewFake(now),
	})
	stored, handled, err := service.HandleProviderEvent(ctx, MeetingEvent{
		Provider: ProviderFeishu, AccountID: "feishu:app-a", ProviderTenantKey: "feishu-tenant-a", EventID: "ready-without-start",
		Type: ProviderEventRecordingReady, MeetingID: "meeting-1", MeetingNo: "123456789",
		MinuteToken: "testminute00000000000000", OccurredAt: now,
	})
	if err != nil || !handled {
		t.Fatalf("HandleProviderEvent: handled=%v err=%v", handled, err)
	}
	if stored.MeetingID != "meeting-1" || stored.Status != StatusRecordingReady || len(queue.jobs) != 1 {
		t.Fatalf("stored session=%+v jobs=%d", stored, len(queue.jobs))
	}
}

func newTestRepository(t *testing.T) *MeetingRepository {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := migration.NewRunner([]migration.Migration{meetingAutomationMigration()}).Up(context.Background(), store); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return NewMeetingRepository(store)
}
