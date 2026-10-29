package discussionsession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDiscussionHILModeDefaultsToManual(t *testing.T) {
	dto := mapConversationDTO(ConversationRecordView{ConversationRecord: ConversationRecord{
		ConversationID: "conv-1",
		CreatedBy:      "owner",
	}})
	if dto.HILMode != DiscussionHILModeManual {
		t.Fatalf("HIL mode = %q, want %q", dto.HILMode, DiscussionHILModeManual)
	}
}

func TestServiceSetConversationHILModePersistsOwnerSelection(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-1",
		Title:          "HIL",
		Status:         "completed",
		CreatedBy:      "owner",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "owner")

	updated, err := service.SetConversationHILMode(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser}, SetConversationHILModeCommand{
		ConversationID: "conv-1",
		Mode:           "automatic",
	})
	if err != nil {
		t.Fatalf("SetConversationHILMode: %v", err)
	}
	if updated.HILMode != DiscussionHILModeAuto {
		t.Fatalf("updated HIL mode = %q, want %q", updated.HILMode, DiscussionHILModeAuto)
	}
	persisted := mustConversationView(t, repo, "conv-1")
	if persisted.HILMode != DiscussionHILModeAuto {
		t.Fatalf("persisted HIL mode = %q, want %q", persisted.HILMode, DiscussionHILModeAuto)
	}
	if len(realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)) != 0 {
		t.Fatal("HIL mode update must not invalidate the full discussion snapshot")
	}
	hilEvents := waitForRealtimeEvents(t, realtime, RealtimeEventDiscussionHILModeUpdated, 1)
	if got := realtimePayloadString(t, hilEvents[0], "hil_mode"); got != DiscussionHILModeAuto {
		t.Fatalf("realtime HIL mode = %q, want %q", got, DiscussionHILModeAuto)
	}
	waitForRealtimeEvents(t, realtime, RealtimeEventUserDiscussionListInvalidated, 1)

	updated, err = service.SetConversationHILMode(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindHuman}, SetConversationHILModeCommand{
		ConversationID: "conv-1",
		Mode:           DiscussionHILModeManual,
	})
	if err != nil {
		t.Fatalf("SetConversationHILMode manual: %v", err)
	}
	if updated.HILMode != DiscussionHILModeManual {
		t.Fatalf("updated HIL mode = %q, want %q", updated.HILMode, DiscussionHILModeManual)
	}

	updated, err = service.SetConversationHILMode(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindHuman}, SetConversationHILModeCommand{
		ConversationID: "conv-1",
		Mode:           DiscussionHILModeNone,
	})
	if err != nil {
		t.Fatalf("SetConversationHILMode none: %v", err)
	}
	if updated.HILMode != DiscussionHILModeNone {
		t.Fatalf("updated HIL mode = %q, want %q", updated.HILMode, DiscussionHILModeNone)
	}
	if persisted := mustConversationView(t, repo, "conv-1"); persisted.HILMode != DiscussionHILModeNone {
		t.Fatalf("persisted HIL mode = %q, want %q", persisted.HILMode, DiscussionHILModeNone)
	}
}

func TestServiceSetConversationHILModePersistsExplicitManualOverLegacyEmptyValue(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-empty-hil",
		Title:          "Legacy HIL",
		Status:         "completed",
		CreatedBy:      "owner",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-empty-hil", "owner")

	updated, err := service.SetConversationHILMode(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser}, SetConversationHILModeCommand{
		ConversationID: "conv-empty-hil",
		Mode:           DiscussionHILModeManual,
	})
	if err != nil {
		t.Fatalf("SetConversationHILMode: %v", err)
	}
	if updated.HILMode != DiscussionHILModeManual {
		t.Fatalf("updated HIL mode = %q, want %q", updated.HILMode, DiscussionHILModeManual)
	}
	persisted := mustConversationView(t, repo, "conv-empty-hil")
	if persisted.HILMode != DiscussionHILModeManual {
		t.Fatalf("persisted HIL mode = %q, want explicit %q", persisted.HILMode, DiscussionHILModeManual)
	}
	if len(realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)) != 0 {
		t.Fatal("HIL mode update must not invalidate the full discussion snapshot")
	}
	waitForRealtimeEvents(t, realtime, RealtimeEventDiscussionHILModeUpdated, 1)
}

func TestServiceSetConversationHILModeDoesNotWaitForRealtimeNotifications(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := newBlockingHILRealtimePublisher()
	service := newTestService(ServiceOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-fast-hil",
		Title:          "Fast HIL",
		Status:         "completed",
		CreatedBy:      "owner",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-fast-hil", "owner")
	t.Cleanup(realtime.unblock)

	result := make(chan error, 1)
	go func() {
		_, err := service.SetConversationHILMode(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser}, SetConversationHILModeCommand{
			ConversationID: "conv-fast-hil",
			Mode:           DiscussionHILModeAuto,
		})
		result <- err
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("SetConversationHILMode: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HIL mode update waited for realtime notification")
	}
	select {
	case <-realtime.started:
	case <-time.After(time.Second):
		t.Fatal("realtime notification was not started")
	}
	if persisted := mustConversationView(t, repo, "conv-fast-hil"); persisted.HILMode != DiscussionHILModeAuto {
		t.Fatalf("persisted HIL mode = %q, want %q", persisted.HILMode, DiscussionHILModeAuto)
	}
}

func TestServiceSetConversationHILModeRejectsUnauthorizedOrInvalidSelection(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-1",
		Title:          "HIL",
		Status:         "completed",
		CreatedBy:      "owner",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-1", MemberKind: "user", MemberID: "owner", DisplayName: "Owner", CreatedAt: testTime},
		{ConversationID: "conv-1", MemberKind: "user", MemberID: "member", DisplayName: "Member", CreatedAt: testTime},
		{ConversationID: "conv-1", MemberKind: "agent", MemberID: "agent-1", DisplayName: "Agent", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}

	for _, viewer := range []ViewerRef{
		{ViewerID: "member", ViewerKind: ViewerKindUser},
		{ViewerID: "agent-1", ViewerKind: ViewerKindAgent},
		{ViewerID: "system", ViewerKind: ViewerKindSystem},
	} {
		_, err := service.SetConversationHILMode(ctx, viewer, SetConversationHILModeCommand{ConversationID: "conv-1", Mode: DiscussionHILModeAuto})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("viewer %+v error = %v, want ErrForbidden", viewer, err)
		}
	}

	_, err := service.SetConversationHILMode(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser}, SetConversationHILModeCommand{
		ConversationID: "conv-1",
		Mode:           "sometimes",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid mode error = %v, want ErrInvalidRequest", err)
	}
}

func waitForRealtimeEvents(t *testing.T, publisher *recordingRealtimePublisher, eventType string, count int) []RealtimeEvent {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		events := publisher.eventsByType(eventType)
		if len(events) >= count {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("realtime events %q = %d, want at least %d", eventType, len(events), count)
		}
		time.Sleep(time.Millisecond)
	}
}

type blockingHILRealtimePublisher struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newBlockingHILRealtimePublisher() *blockingHILRealtimePublisher {
	return &blockingHILRealtimePublisher{started: make(chan struct{}), release: make(chan struct{})}
}

func (p *blockingHILRealtimePublisher) AppendRealtimeEvent(ctx context.Context, _ RealtimeEvent) error {
	p.startedOnce.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *blockingHILRealtimePublisher) unblock() {
	p.releaseOnce.Do(func() { close(p.release) })
}
