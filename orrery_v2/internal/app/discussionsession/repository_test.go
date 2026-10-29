package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

var repositoryTestDatabaseSequence atomic.Uint64

func TestRepositoryReassignsConversationTenantIdempotently(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(newRepositoryTestRouter(t))
	now := time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC)
	legacy := ConversationRecord{
		ConversationID: "conv-legacy-tenant", TenantID: "legacy-org", PurposeKind: "todo", PurposeID: "todo-1",
		Title: "Legacy", Status: "running", CreatedBy: "owner", CreatedAt: now, UpdatedAt: now,
	}
	current := ConversationRecord{
		ConversationID: "conv-current-tenant", TenantID: "minvia", Title: "Current", Status: "completed",
		CreatedBy: "owner", CreatedAt: now, UpdatedAt: now,
	}
	for _, conversation := range []ConversationRecord{legacy, current} {
		if err := repo.SaveConversation(ctx, conversation); err != nil {
			t.Fatal(err)
		}
	}

	migrated, err := repo.ReassignTenant(ctx, "legacy-org", "minvia")
	if err != nil || migrated != 1 {
		t.Fatalf("ReassignTenant = %d, %v", migrated, err)
	}
	view, err := repo.GetConversation(ctx, legacy.ConversationID)
	if err != nil || view.TenantID != "minvia" || view.PurposeID != legacy.PurposeID || view.Revision != 2 {
		t.Fatalf("migrated conversation = %+v, %v", view.ConversationRecord, err)
	}
	stored, ok, err := repo.router.GetRecord(ctx, conversationTable, legacy.ConversationID)
	if err != nil || !ok || stored["tenant_id"] != "minvia" {
		t.Fatalf("indexed conversation = %+v, ok=%v, err=%v", stored, ok, err)
	}
	migrated, err = repo.ReassignTenant(ctx, "legacy-org", "minvia")
	if err != nil || migrated != 0 {
		t.Fatalf("repeated ReassignTenant = %d, %v", migrated, err)
	}
}

func TestRepositoryManagedMemberSourcesSurviveConcurrentReconcilers(t *testing.T) {
	ctx := context.Background()
	router := newRepositoryTestRouter(t)
	first := NewRepository(router)
	second := NewRepository(router)
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	conversation := ConversationRecord{ConversationID: "conv-managed", TenantID: "tenant-a", Title: "Managed", Status: "running", CreatedBy: "owner", CreatedAt: now, UpdatedAt: now}
	if err := first.SaveConversation(ctx, conversation); err != nil {
		t.Fatal(err)
	}
	member := ConversationMemberRecord{ConversationID: conversation.ConversationID, MemberKind: "agent", MemberID: "agent-1", DisplayName: "Agent One", Role: "Executor", CreatedAt: now}
	if err := first.SaveMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	sources := []string{"todo:tenant-a/todo-1", "todo-mission:tenant-a/todo-1/mission-1", "mission-task-assignee:tenant-a/mission-1", "mission-team:tenant-a/mission-1"}
	var wg sync.WaitGroup
	errs := make(chan error, len(sources))
	for index, source := range sources {
		wg.Add(1)
		go func(repo *Repository, source string) {
			defer wg.Done()
			_, _, err := repo.MutateMemberManagedSource(ctx, member, source, true)
			errs <- err
		}([]*Repository{first, second}[index%2], source)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("grant managed source: %v", err)
		}
	}
	view, err := first.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Members) != 1 {
		t.Fatalf("members = %+v", view.Members)
	}
	for _, source := range sources {
		if !conversationMemberHasManagedSource(view.Members[0], source) {
			t.Fatalf("managed sources = %v, missing %q", conversationMemberManagedSources(view.Members[0]), source)
		}
	}
}

func TestRepositorySaveMemberPreservesConcurrentManagedOwnership(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(newRepositoryTestRouter(t))
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	member := ConversationMemberRecord{ConversationID: "conv-member-save", MemberKind: "agent", MemberID: "agent-1", DisplayName: "Agent One", Role: "Executor", CreatedAt: now}
	if _, _, err := repo.MutateMemberManagedSource(ctx, member, "todo:tenant-a/todo-1", true); err != nil {
		t.Fatal(err)
	}
	member.DisplayName = "Agent One Updated"
	member.Role = "Reviewer"
	if err := repo.SaveMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	record, ok, err := repo.router.GetRecord(ctx, memberTable, conversationMemberKey(member))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("member record is missing")
	}
	var stored ConversationMemberRecord
	if err := decodePayload(record, &stored); err != nil {
		t.Fatal(err)
	}
	if !conversationMemberHasManagedSource(stored, "todo:tenant-a/todo-1") || stored.DisplayName != "Agent One Updated" || stored.Role != "Reviewer" {
		t.Fatalf("stored member = %+v", stored)
	}
}

func TestRepositorySaveMemberExplicitGrantReplacesManagedOwnership(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(newRepositoryTestRouter(t))
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	member := ConversationMemberRecord{ConversationID: "conv-member-explicit", MemberKind: "user", MemberID: "user-1", DisplayName: "User One", CreatedAt: now}
	if _, _, err := repo.MutateMemberManagedSource(ctx, member, "todo:tenant-a/todo-1", true); err != nil {
		t.Fatal(err)
	}
	member.ExplicitGrantorID = "owner-1"
	member.ExplicitGrantorKind = "user"
	member.ExplicitGrantedAt = now.Add(time.Minute)
	if err := repo.SaveMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	record, ok, err := repo.router.GetRecord(ctx, memberTable, conversationMemberKey(member))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("member record is missing")
	}
	var stored ConversationMemberRecord
	if err := decodePayload(record, &stored); err != nil {
		t.Fatal(err)
	}
	if len(conversationMemberManagedSources(stored)) != 0 || stored.ExplicitGrantorID != "owner-1" {
		t.Fatalf("stored member = %+v", stored)
	}
}

func TestRepositoryPersistsConversationMessagesRunsAndMissionLinks(t *testing.T) {
	router := newRepositoryTestRouter(t)
	repo := NewRepository(router)
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	conversation := ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "running", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now}
	message := MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "Plan launch", CreatedAt: now}
	member := ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "reviewer-agent", DisplayName: "秦若兰", Role: "Reviewer", CreatedAt: now}
	run := RunRecord{RunID: "run-1", ConversationID: "conv-1", AgentID: "coordinator-agent", Status: "running", RuntimeKind: "openclaw", CreatedAt: now}
	link := LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: now}
	attachment := AttachmentRecord{AttachmentID: "att-1", ConversationID: "conv-1", FileName: "brief.pdf", ContentType: "application/pdf", SizeBytes: 123, UploadedBy: "user-1", StoragePath: "D:/data/discussions/conv-1/attachments/att-1-brief.pdf", CreatedAt: now}

	if err := repo.SaveConversation(context.Background(), conversation); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), member); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(context.Background(), link); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAttachment(context.Background(), attachment); err != nil {
		t.Fatal(err)
	}

	loaded, err := repo.GetConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || len(loaded.Members) != 1 || len(loaded.Runs) != 1 || len(loaded.LinkedMissions) != 1 || len(loaded.Attachments) != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Messages[0].Content != "Plan launch" || loaded.Members[0].DisplayName != "秦若兰" || loaded.Runs[0].RuntimeKind != "openclaw" || loaded.LinkedMissions[0].MissionID != "mission-1" || loaded.Attachments[0].FileName != "brief.pdf" {
		t.Fatalf("loaded details = %+v", loaded)
	}
}

func TestRepositorySnapshotPagesOnlyTheRequestedLogicalTable(t *testing.T) {
	router := newRepositoryTestRouter(t)
	store, table, err := router.StoreForTable(messageTable)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for i := 0; i < db.MaxRecordPageLimit+1; i++ {
		key := fmt.Sprintf("message-%04d", i)
		if err := db.PutRecordTx(context.Background(), tx, table, key, db.Record{"index": i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true

	records, err := snapshot(context.Background(), router, messageTable)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != db.MaxRecordPageLimit+1 {
		t.Fatalf("records = %d, want %d", len(records), db.MaxRecordPageLimit+1)
	}
}

func TestRepositoryClaimsIdempotencyCommandOnceAndRejectsReplacement(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	command := CommandRecord{
		Operation:      "send_message",
		ViewerID:       "user-1",
		ViewerKind:     "user",
		IdempotencyKey: "send-1",
		Fingerprint:    "fingerprint-a",
		Status:         "pending",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}
	claimed, first, err := repo.ClaimCommand(ctx, command)
	if err != nil || !first || claimed.CommandID == "" {
		t.Fatalf("first claim = %+v, %v, %v", claimed, first, err)
	}
	duplicate := command
	duplicate.Fingerprint = "fingerprint-b"
	existing, second, err := repo.ClaimCommand(ctx, duplicate)
	if err != nil || second {
		t.Fatalf("duplicate claim = %+v, %v, %v", existing, second, err)
	}
	if existing.CommandID != claimed.CommandID || existing.Fingerprint != "fingerprint-a" {
		t.Fatalf("duplicate replaced original command: %+v", existing)
	}
}

func TestRepositoryRetriesFailedCommandWithBoundedCounter(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	command := CommandRecord{
		Operation: "send_message", ViewerID: "user-1", ViewerKind: string(ViewerKindUser),
		IdempotencyKey: "message-1", Fingerprint: "fingerprint-1", Status: discussionCommandPending,
		CreatedAt: testTime, UpdatedAt: testTime,
	}
	claimed, ok, err := repo.ClaimCommand(ctx, command)
	if err != nil || !ok {
		t.Fatalf("claim command = %+v claimed=%v err=%v", claimed, ok, err)
	}
	for retry := 1; retry <= 2; retry++ {
		claimed.Status = discussionCommandFailed
		claimed.ErrorCode = "internal"
		claimed.ErrorMessage = "temporary dependency failure"
		claimed.UpdatedAt = testTime.Add(time.Duration(retry) * time.Minute)
		if err := repo.TransitionCommand(ctx, claimed, discussionCommandPending); err != nil {
			t.Fatalf("fail attempt %d: %v", retry, err)
		}
		retried, reopened, err := repo.RetryFailedCommand(ctx, claimed, 2, claimed.UpdatedAt.Add(time.Second))
		if err != nil || !reopened || retried.Status != discussionCommandPending || retried.RetryCount != retry || retried.ErrorCode != "" {
			t.Fatalf("retry attempt %d = %+v reopened=%v err=%v", retry, retried, reopened, err)
		}
		claimed = retried
	}
	claimed.Status = discussionCommandFailed
	claimed.ErrorCode = "internal"
	claimed.UpdatedAt = testTime.Add(3 * time.Minute)
	if err := repo.TransitionCommand(ctx, claimed, discussionCommandPending); err != nil {
		t.Fatal(err)
	}
	current, reopened, err := repo.RetryFailedCommand(ctx, claimed, 2, testTime.Add(4*time.Minute))
	if err != nil || reopened || current.Status != discussionCommandFailed || current.RetryCount != 2 {
		t.Fatalf("capped retry = %+v reopened=%v err=%v", current, reopened, err)
	}
}

func TestRepositoryReclaimsOnlyStalePendingCommand(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	command := CommandRecord{
		Operation: "send_message", ViewerID: "user-1", ViewerKind: "user", IdempotencyKey: "send-1",
		Fingerprint: "fingerprint-a", Status: discussionCommandPending, CreatedAt: testTime, UpdatedAt: testTime,
	}
	claimed, ok, err := repo.ClaimCommand(ctx, command)
	if err != nil || !ok {
		t.Fatalf("claim = %+v, %v, %v", claimed, ok, err)
	}
	if current, reclaimed, err := repo.ReclaimStalePendingCommand(ctx, claimed, testTime.Add(-time.Minute), testTime.Add(time.Minute)); err != nil || reclaimed || current.UpdatedAt != testTime {
		t.Fatalf("fresh reclaim = %+v, %v, %v", current, reclaimed, err)
	}
	reclaimedAt := testTime.Add(41 * time.Minute)
	current, reclaimed, err := repo.ReclaimStalePendingCommand(ctx, claimed, testTime.Add(time.Minute), reclaimedAt)
	if err != nil || !reclaimed || !current.UpdatedAt.Equal(reclaimedAt) {
		t.Fatalf("stale reclaim = %+v, %v, %v", current, reclaimed, err)
	}
	if _, reclaimedAgain, err := repo.ReclaimStalePendingCommand(ctx, claimed, reclaimedAt.Add(-time.Minute), reclaimedAt.Add(time.Minute)); err != nil || reclaimedAgain {
		t.Fatalf("renewed command reclaimed twice: reclaimed=%v err=%v", reclaimedAgain, err)
	}
}

func TestRepositoryCommandTerminalTransitionRejectsLateOverwrite(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	command := CommandRecord{Operation: "send_message", ViewerID: "user-1", ViewerKind: "user", IdempotencyKey: "send-1", Fingerprint: "fingerprint-a", Status: discussionCommandPending, CreatedAt: testTime, UpdatedAt: testTime}
	claimed, ok, err := repo.ClaimCommand(ctx, command)
	if err != nil || !ok {
		t.Fatalf("claim = %+v, %v, %v", claimed, ok, err)
	}
	completed := claimed
	completed.Status = discussionCommandCompleted
	completed.ConversationID = "conv-1"
	if err := repo.TransitionCommand(ctx, completed, discussionCommandPending); err != nil {
		t.Fatal(err)
	}
	lateFailure := claimed
	lateFailure.Status = discussionCommandFailed
	if err := repo.TransitionCommand(ctx, lateFailure, discussionCommandPending); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("late terminal overwrite error = %v, want ErrRevisionConflict", err)
	}
	stored, err := repo.GetCommand(ctx, command.Operation, ViewerRef{ViewerID: command.ViewerID, ViewerKind: ViewerKindUser}, command.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.ConversationID != "conv-1" {
		t.Fatalf("stored command = %+v, %v", stored, err)
	}
}

func TestRepositoryPersistsConversationRevisionAndTurnCorrelation(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	conversation := ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}
	if err := repo.SaveConversation(ctx, conversation); err != nil {
		t.Fatal(err)
	}
	turn := TurnRecord{TurnID: "turn-1", ConversationID: "conv-1", RootMessageID: "msg-1", Mode: "group", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
	if err := repo.SaveTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", TurnID: "turn-1", Role: "user", AuthorID: "user-1", Content: "Plan", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-1", ConversationID: "conv-1", TurnID: "turn-1", Mode: "group", AgentID: "lead", Status: "running", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 || len(loaded.Turns) != 1 || loaded.Messages[0].TurnID != "turn-1" || loaded.Runs[0].TurnID != "turn-1" {
		t.Fatalf("loaded correlation = %+v", loaded)
	}
}

func TestRepositoryAppliesTurnMutationWithRevisionGuard(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	conversation := ConversationRecord{ConversationID: "conv-atomic", Title: "Launch", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}
	turn := TurnRecord{TurnID: "turn-atomic", ConversationID: conversation.ConversationID, RootMessageID: "msg-atomic", Mode: "group", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
	saved, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation: conversation,
		Turn:         &turn,
		Messages:     []MessageRecord{{MessageID: "msg-atomic", ConversationID: conversation.ConversationID, TurnID: turn.TurnID, Role: "user", AuthorID: "user-1", Content: "Plan", CreatedAt: testTime}},
		Runs:         []RunRecord{{RunID: "run-atomic", ConversationID: conversation.ConversationID, TurnID: turn.TurnID, Mode: "group", AgentID: "lead", Status: "running", CreatedAt: testTime}},
	})
	if err != nil || saved.Revision != 1 {
		t.Fatalf("ApplyTurnMutation = %+v, %v", saved, err)
	}
	conversation.Status = "completed"
	_, err = repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation:     conversation,
		ExpectedRevision: 99,
		Messages:         []MessageRecord{{MessageID: "must-not-exist", ConversationID: conversation.ConversationID, Role: "assistant", Content: "stale", CreatedAt: testTime}},
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale mutation error = %v, want ErrRevisionConflict", err)
	}
	if _, ok, getErr := repo.router.GetRecord(ctx, messageTable, "must-not-exist"); getErr != nil || ok {
		t.Fatalf("stale mutation message exists = %v, %v", ok, getErr)
	}
}

func TestRepositoryCreatesConversationAndInitialMembersAtomically(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	conversation := ConversationRecord{ConversationID: "conv-create-atomic", TenantID: "tenant-a", PurposeKind: "todo", PurposeID: "todo-1", Status: "draft", CreatedBy: "owner-1", CreatedAt: testTime, UpdatedAt: testTime}

	_, err := repo.CreateConversationWithMembers(ctx, conversation, []ConversationMemberRecord{{
		ConversationID: "different-conversation", MemberKind: "user", MemberID: "owner-1", CreatedAt: testTime,
	}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("invalid initial member error = %v, want ErrForbidden", err)
	}
	if _, err := repo.GetConversation(ctx, conversation.ConversationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("conversation survived rolled-back member write: %v", err)
	}

	created, err := repo.CreateConversationWithMembers(ctx, conversation, []ConversationMemberRecord{{
		ConversationID: conversation.ConversationID, MemberKind: "user", MemberID: "owner-1", CreatedAt: testTime,
	}})
	if err != nil || created.Revision != 1 {
		t.Fatalf("retry create = %+v, %v", created, err)
	}
	loaded, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil || len(loaded.Members) != 1 || loaded.Members[0].MemberID != "owner-1" {
		t.Fatalf("atomic aggregate = %+v, %v", loaded, err)
	}
	if _, err := repo.CreateConversationWithMembers(ctx, conversation, nil); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("duplicate create error = %v, want ErrRevisionConflict", err)
	}
}

func TestRepositoryFencesConcurrentActiveTurnsAcrossWorkers(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	conversation := ConversationRecord{ConversationID: "conv-fenced", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}
	turnA := TurnRecord{TurnID: "turn-a", ConversationID: conversation.ConversationID, RootMessageID: "msg-a", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
	startedA, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: conversation, Turn: &turnA})
	if err != nil || startedA.ActiveTurnID != turnA.TurnID {
		t.Fatalf("start turn A = %+v, %v", startedA, err)
	}
	turnB := TurnRecord{TurnID: "turn-b", ConversationID: conversation.ConversationID, RootMessageID: "msg-b", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
	_, err = repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: startedA, ExpectedRevision: startedA.Revision, Turn: &turnB})
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("concurrent turn error = %v, want ErrCommandInProgress", err)
	}
	startedB, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: startedA, ExpectedRevision: startedA.Revision, Turn: &turnB, SupersedeActiveTurn: true})
	if err != nil || startedB.ActiveTurnID != turnB.TurnID {
		t.Fatalf("supersede turn = %+v, %v", startedB, err)
	}
	turnA.Status = "completed"
	_, err = repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: startedB, ExpectedRevision: startedB.Revision, Turn: &turnA, CompleteTurnID: turnA.TurnID})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("late turn completion error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryRejectsGuardedMessageMutationWhileTurnIsActive(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	conversation := ConversationRecord{ConversationID: "conv-message-fenced", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}
	turn := TurnRecord{TurnID: "turn-active", ConversationID: conversation.ConversationID, RootMessageID: "msg-root", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
	started, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: conversation, Turn: &turn})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}

	_, err = repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation:        started,
		ExpectedRevision:    started.Revision,
		RequireNoActiveTurn: true,
		Messages: []MessageRecord{{
			MessageID:      "msg-orphan",
			ConversationID: conversation.ConversationID,
			Role:           "user",
			AuthorID:       "user-1",
			Content:        "must not persist",
			CreatedAt:      testTime,
		}},
		Attachments: []AttachmentRecord{{
			AttachmentID:   "att-orphan",
			ConversationID: conversation.ConversationID,
			MessageID:      "msg-orphan",
			FileName:       "orphan.txt",
			CreatedAt:      testTime,
		}},
	})
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("guarded message mutation error = %v, want ErrCommandInProgress", err)
	}
	for table, id := range map[string]string{messageTable: "msg-orphan", attachmentTable: "att-orphan"} {
		if _, ok, getErr := repo.router.GetRecord(ctx, table, id); getErr != nil || ok {
			t.Fatalf("guarded mutation record %s/%s exists = %v, %v", table, id, ok, getErr)
		}
	}
}

func TestRepositoryRejectsCrossConversationAggregateChildren(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	conversation := ConversationRecord{ConversationID: "conv-a", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}
	_, err := repo.ApplyTurnMutation(context.Background(), TurnAggregateMutation{
		Conversation: conversation,
		Messages:     []MessageRecord{{MessageID: "msg-other", ConversationID: "conv-b", Role: "assistant", Content: "wrong aggregate", CreatedAt: testTime}},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-conversation mutation error = %v, want ErrForbidden", err)
	}
	if _, ok, getErr := repo.router.GetRecord(context.Background(), messageTable, "msg-other"); getErr != nil || ok {
		t.Fatalf("cross-conversation message exists = %v, %v", ok, getErr)
	}
}

func TestRepositoryNormalizesSavedRunsWithDebugSnapshot(t *testing.T) {
	router := newRepositoryTestRouter(t)
	repo := NewRepository(router)
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "running", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-1", ConversationID: "conv-1", AgentID: "lead-agent", Status: "running", RuntimeKind: "codex", CreatedAt: now, StartedAt: now}); err != nil {
		t.Fatal(err)
	}

	loaded, err := repo.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Runs) != 1 || loaded.Runs[0].Debug == nil {
		t.Fatalf("loaded runs = %+v, want debug snapshot on every run", loaded.Runs)
	}
	debug := loaded.Runs[0].Debug
	if debug.ConversationID != "conv-1" || debug.RunID != "run-1" || debug.Selected.AgentID != "lead-agent" || debug.Selected.RuntimeKind != "codex" || debug.CreatedAt.IsZero() {
		t.Fatalf("debug snapshot = %+v", debug)
	}
}

func TestRepositoryListConversationsUsesOnlyLatestMessageSummary(t *testing.T) {
	router := newRepositoryTestRouter(t)
	repo := NewRepository(router)
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)

	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-2", Title: "Second", Status: "completed", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{"old", "middle", "latest"} {
		if err := repo.SaveMessage(ctx, MessageRecord{
			MessageID:      "msg-" + content,
			ConversationID: "conv-1",
			Role:           "assistant",
			AuthorID:       "agent-1",
			Content:        content,
			CreatedAt:      now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-other", ConversationID: "conv-2", Role: "user", AuthorID: "user-1", Content: "other", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	views, err := repo.ListConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(views) != 2 {
		t.Fatalf("views = %+v", views)
	}
	if views[0].ConversationID != "conv-1" {
		t.Fatalf("first conversation = %q, want newest updated conv-1", views[0].ConversationID)
	}
	if len(views[0].Messages) != 1 || views[0].Messages[0].Content != "latest" {
		t.Fatalf("summary messages = %+v, want only latest message", views[0].Messages)
	}
	if len(views[1].Messages) != 1 || views[1].Messages[0].Content != "other" {
		t.Fatalf("second summary messages = %+v", views[1].Messages)
	}
}

func TestRepositoryListConversationsIncludesStoredMembers(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)

	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-1",
		Title:          "Direct chat",
		Status:         "completed",
		CreatedBy:      "human-ding",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-1", MemberKind: "user", MemberID: "human-ding", CreatedAt: now},
		{ConversationID: "conv-1", MemberKind: "agent", MemberID: "bianbian", CreatedAt: now.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}

	views, err := repo.ListConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	if len(views[0].Members) != 2 || views[0].Members[0].MemberID != "human-ding" || views[0].Members[1].MemberID != "bianbian" {
		t.Fatalf("summary members = %+v", views[0].Members)
	}
}

func TestRepositoryListMessagesPageReturnsNewestWindowForUpwardScroll(t *testing.T) {
	router := newRepositoryTestRouter(t)
	repo := NewRepository(router)
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 45; i++ {
		if err := repo.SaveMessage(ctx, MessageRecord{
			MessageID:      fmt.Sprintf("msg-%02d", i+1),
			ConversationID: "conv-1",
			Role:           "assistant",
			AuthorID:       "agent-1",
			Content:        fmt.Sprintf("message %02d", i+1),
			CreatedAt:      now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	latest, latestPage, err := repo.ListMessagesPage(ctx, "conv-1", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	older, olderPage, err := repo.ListMessagesPage(ctx, "conv-1", 20, 20)
	if err != nil {
		t.Fatal(err)
	}

	if latestPage.Total != 45 || latestPage.Returned != 20 || !latestPage.HasMore {
		t.Fatalf("latest page = %+v", latestPage)
	}
	if latest[0].MessageID != "msg-26" || latest[19].MessageID != "msg-45" {
		t.Fatalf("latest messages = %s..%s, want msg-26..msg-45", latest[0].MessageID, latest[19].MessageID)
	}
	if olderPage.Total != 45 || olderPage.Returned != 20 || !olderPage.HasMore {
		t.Fatalf("older page = %+v", olderPage)
	}
	if older[0].MessageID != "msg-06" || older[19].MessageID != "msg-25" {
		t.Fatalf("older messages = %s..%s, want msg-06..msg-25", older[0].MessageID, older[19].MessageID)
	}
}

func TestRepositoryListMessagesPageOrdersByParsedCreatedAt(t *testing.T) {
	router := newRepositoryTestRouter(t)
	repo := NewRepository(router)
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []MessageRecord{
		{MessageID: "msg-second", ConversationID: "conv-1", Role: "assistant", AuthorID: "agent-1", Content: "exact second", CreatedAt: now},
		{MessageID: "msg-fraction", ConversationID: "conv-1", Role: "assistant", AuthorID: "agent-1", Content: "fractional second", CreatedAt: now.Add(100 * time.Millisecond)},
	} {
		if err := repo.SaveMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}

	messages, page, err := repo.ListMessagesPage(ctx, "conv-1", 20, 0)
	if err != nil {
		t.Fatal(err)
	}

	if page.Total != 2 || page.Returned != 2 || page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	if got := []string{messages[0].MessageID, messages[1].MessageID}; fmt.Sprint(got) != "[msg-second msg-fraction]" {
		t.Fatalf("messages = %v, want chronological order by parsed created_at", got)
	}
}

func TestRepositoryListMessagesPageReturnsNotFoundForUnknownConversation(t *testing.T) {
	repo := NewRepository(newRepositoryTestRouter(t))

	_, _, err := repo.ListMessagesPage(context.Background(), "missing", 20, 0)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRepositoryDeleteConversationRemovesRelatedRecords(t *testing.T) {
	router := newRepositoryTestRouter(t)
	repo := NewRepository(router)
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	ctx := context.Background()

	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-delete", ConversationID: "conv-delete", Role: "user", AuthorID: "user-1", Content: "remove", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-delete", MemberKind: "user", MemberID: "user-1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-delete", ConversationID: "conv-delete", AgentID: "coordinator-agent", Status: "completed", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{EventID: "event-delete", ConversationID: "conv-delete", Kind: "message_retry_requested", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-delete", MissionID: "mission-delete", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{AttachmentID: "att-delete", ConversationID: "conv-delete", FileName: "delete.txt", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteConversation(ctx, "conv-delete"); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.GetConversation(ctx, "conv-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetConversation err = %v, want ErrNotFound", err)
	}
	for _, table := range []string{messageTable, memberTable, runTable, "core.discussion_session_events", missionLinkTable, attachmentTable} {
		records, err := snapshot(ctx, router, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if got, _ := record["conversation_id"].(string); got == "conv-delete" {
				t.Fatalf("table %s retained deleted conversation record: %+v", table, record)
			}
		}
	}
}

func newRepositoryTestRouter(t *testing.T) *db.Router {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: fmt.Sprintf("discussion-test-%d", repositoryTestDatabaseSequence.Add(1))},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	return db.NewRouter(registry)
}
