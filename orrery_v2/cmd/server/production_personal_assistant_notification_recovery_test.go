package main

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	todoapp "orrery_v2/internal/app/todo"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type personalAssistantNoticesStub struct {
	messages map[string]string
	viewers  []discussionsession.ViewerRef
	err      error
}

type personalAssistantRepliesStub struct {
	personalAssistantNoticesStub
	replyCalls int
}

func (s *personalAssistantRepliesStub) AppendPersonalAssistantReply(ctx context.Context, viewer discussionsession.ViewerRef, key, content string) error {
	s.replyCalls++
	return s.AppendPersonalAssistantNotice(ctx, viewer, key, content)
}

func TestPersonalAssistantScheduledReplyRecoveryPreservesModelContent(t *testing.T) {
	s, _, _, _, auth := attentionFixture()
	_, recoveries := attachAttentionRecovery(&s, auth)
	replies := &personalAssistantRepliesStub{}
	s.recovery.notices, s.recovery.preserveContent = replies, true
	const body = "旧的巡检已终止，没有产生新的业务结果。"
	target := productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "weixin-a"}
	if err := s.recovery.Maintain(context.Background(), s.recipients[0], target, "scheduled-reply", "todo.read", body); err != nil {
		t.Fatal(err)
	}
	if len(recoveries.recovered) != 1 || replies.replyCalls != 2 || len(replies.messages) != 2 {
		t.Fatalf("model reply recovery was not used: %+v %+v", replies, recoveries)
	}
	for _, content := range replies.messages {
		if content != body {
			t.Fatalf("recovery added platform dialogue: %q", content)
		}
	}
}

func (s *personalAssistantNoticesStub) AppendPersonalAssistantNotice(_ context.Context, viewer discussionsession.ViewerRef, key, content string) error {
	if s.err != nil {
		return s.err
	}
	if s.messages == nil {
		s.messages = map[string]string{}
	}
	if _, found := s.messages[key]; !found {
		s.messages[key] = content
		s.viewers = append(s.viewers, viewer)
	}
	return nil
}

func (s *personalAssistantNoticesStub) AppendPersonalAssistantReply(ctx context.Context, viewer discussionsession.ViewerRef, key, content string) error {
	return s.AppendPersonalAssistantNotice(ctx, viewer, key, content)
}

type personalAssistantRecoveryDeliveriesStub struct {
	items     []gwoutbound.Delivery
	recovered []string
	listErr   error
}

func (s *personalAssistantRecoveryDeliveriesStub) ListByIdempotencyKey(context.Context, string, string, string) ([]gwoutbound.Delivery, error) {
	return s.items, s.listErr
}
func (s *personalAssistantRecoveryDeliveriesStub) RecoverDeadLetter(_ context.Context, tenant, human, account, id string) (bool, error) {
	s.recovered = append(s.recovered, id)
	return true, nil
}

func attachAttentionRecovery(s *productionPersonalAssistantAttentionSweeper, auth *recordingGatewayViewerAuthorization) (*personalAssistantNoticesStub, *personalAssistantRecoveryDeliveriesStub) {
	notices := &personalAssistantNoticesStub{}
	deliveries := &personalAssistantRecoveryDeliveriesStub{items: []gwoutbound.Delivery{{ID: "delivery", TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "weixin-a", ChannelKey: "weixin", Status: "dead_letter"}}}
	s.recovery = &productionPersonalAssistantNotificationRecovery{recipients: s.recipients, accounts: auth, scopes: auth, notices: notices, deliveries: deliveries}
	return notices, deliveries
}

func TestApprovalRecoveryUsesActualAssigneeWithoutAttentionOptIn(t *testing.T) {
	for _, scenario := range []string{"assigned", "no_longer_pending", "scope_revoked", "owner_changed", "binding_changed", "wrong_key"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, _, auth := attentionFixture()
			_, deliveries := attachAttentionRecovery(&s, auth)
			s.recovery.recipients = nil
			target := productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "weixin-a"}
			candidate := productionPersonalAssistantApprovalCandidate{DecisionID: "decision", ApprovalTenant: "tenant-a", HumanProfileID: "human-a", TaskID: "task"}
			key := productionPersonalAssistantApprovalNotificationKey(candidate)
			source := &recordingPersonalAssistantApprovalNotificationSource{pending: true, targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{"tenant-a": target}}
			switch scenario {
			case "no_longer_pending":
				source.pending = false
			case "scope_revoked":
				auth.resolved = nil
			case "owner_changed":
				auth.account.UserID = "other-owner"
			case "binding_changed":
				source.targetsByTenant["tenant-a"] = productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "other-owner", AccountID: "other-account"}
			case "wrong_key":
				key = "wrong"
			}
			if err := s.recovery.MaintainApproval(context.Background(), source, candidate, target, key, "Agent original reply"); (err != nil) != (scenario == "wrong_key") {
				t.Fatalf("recovery: %v", err)
			}
			if (len(deliveries.recovered) == 1) != (scenario == "assigned") {
				t.Fatalf("unexpected recovery: %+v", deliveries.recovered)
			}
		})
	}
}

func TestPersonalAssistantRecoveryRechecksActionableTodosBeforeReopening(t *testing.T) {
	for _, scenario := range []string{"pending", "done", "reassigned", "revoked_scope", "opted_out", "changed_owner", "queue_unavailable", "inapp_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			s, todos, _, outgoing, auth := attentionFixture()
			notices, recoveries := attachAttentionRecovery(&s, auth)
			todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
			key := personalAssistantAttentionKey(s.recipients[0], "todo", "task", "assigned")
			outgoing.existingKeys = map[string]bool{key: true}
			switch scenario {
			case "done":
				fresh := todos.items[0]
				fresh.Status = todoapp.TodoStatusDone
				todos.fresh = map[string]todoapp.TodoItem{"task": fresh}
			case "reassigned":
				fresh := todos.items[0]
				fresh.AssigneeID = "human-b"
				todos.fresh = map[string]todoapp.TodoItem{"task": fresh}
			case "revoked_scope":
				auth.resolved = nil
			case "opted_out":
				s.recovery.recipients = nil
			case "changed_owner":
				auth.account.UserID = "other-owner"
			case "queue_unavailable":
				recoveries.listErr = errors.New("jobs unavailable")
			case "inapp_unavailable":
				notices.err = errors.New("core unavailable")
			}
			err := s.Sweep(context.Background())
			wantErr := scenario == "queue_unavailable" || scenario == "inapp_unavailable"
			if (err != nil) != wantErr {
				t.Fatalf("sweep: %v", err)
			}
			wantRecovery := scenario == "pending" || scenario == "inapp_unavailable"
			if (len(recoveries.recovered) == 1) != wantRecovery {
				t.Fatalf("unexpected recovery: %v", recoveries.recovered)
			}
			wantNotice := scenario == "pending" || scenario == "queue_unavailable"
			if (len(notices.messages) > 0) != wantNotice {
				t.Fatalf("unexpected notices: %+v", notices.messages)
			}
			if len(outgoing.commands) != 0 {
				t.Fatal("recovery created another external notification")
			}
			for _, viewer := range notices.viewers {
				if viewer.TenantID != "tenant-a" || viewer.OwnerID != "owner-a" || viewer.ViewerID != "alice" {
					t.Fatalf("incorrect notice scope: %+v", viewer)
				}
			}
		})
	}
}

func TestPersonalAssistantRecoveryDoesNotReopenDeliveredOrPermanentFailures(t *testing.T) {
	for _, status := range []string{"pending", "retrying", "sending", "delivered", "failed", "dead_letter"} {
		t.Run(status, func(t *testing.T) {
			s, todos, _, outgoing, auth := attentionFixture()
			notices, recoveries := attachAttentionRecovery(&s, auth)
			todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
			key := personalAssistantAttentionKey(s.recipients[0], "todo", "task", "assigned")
			outgoing.existingKeys = map[string]bool{key: true}
			recoveries.items[0].Status = status
			for i := 0; i < 3; i++ {
				if err := s.Sweep(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			wantMessages := 1
			if status == "failed" || status == "dead_letter" {
				wantMessages = 2
			}
			if len(notices.messages) != wantMessages || (len(recoveries.recovered) > 0) != (status == "dead_letter") {
				t.Fatalf("notices=%d recovery=%v", len(notices.messages), recoveries.recovered)
			}
		})
	}
}

func TestPersonalAssistantRecoveryKeepsNoticeWhenInitialEnqueueFails(t *testing.T) {
	s, todos, _, outgoing, auth := attentionFixture()
	notices, recoveries := attachAttentionRecovery(&s, auth)
	recoveries.items = nil
	outgoing.fail = true
	todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
	if err := s.Sweep(context.Background()); err == nil {
		t.Fatal("expected enqueue error")
	}
	if len(notices.messages) != 1 {
		t.Fatal("external queue failure lost the in-app notification")
	}
}

func TestPersonalAssistantApprovalRecoveryRunsForExistingPendingNotification(t *testing.T) {
	attention, _, _, deliveries, auth := attentionFixture()
	notices, recoveries := attachAttentionRecovery(&attention, auth)
	candidate := productionPersonalAssistantApprovalCandidate{DecisionID: "approval", ApprovalTenant: "tenant-a", HumanProfileID: "human-a", Action: "business.approval.request", Payload: map[string]any{"title": "Confirm quote"}}
	key := productionPersonalAssistantApprovalNotificationKey(candidate)
	deliveries.existingKeys = map[string]bool{key: true}
	source := attention.targets.(*recordingPersonalAssistantApprovalNotificationSource)
	source.candidates, source.pending = []productionPersonalAssistantApprovalCandidate{candidate}, true
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{store: source, profiles: attention.profiles, deliveries: deliveries, recovery: attention.recovery, replies: &agentNoticeWriterStub{}}
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recoveries.recovered) != 1 || len(notices.messages) != 2 || len(deliveries.commands) != 0 {
		t.Fatal("pending approval was skipped or regenerated")
	}
	source.pending = false
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recoveries.recovered) != 1 {
		t.Fatal("completed approval recovered")
	}
}

func TestPersonalAssistantNotificationGuardStopsStaleOrUnauthorizedTodoSends(t *testing.T) {
	for _, scenario := range []string{"pending", "done", "reassigned", "opted_out", "disabled", "unbound", "scope_revoked", "other_account", "other_tenant", "malformed_key", "read_error"} {
		t.Run(scenario, func(t *testing.T) {
			s, todos, _, _, auth := attentionFixture()
			todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
			delivery := gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "weixin-a", ChannelKey: "weixin", IdempotencyKey: personalAssistantAttentionKey(s.recipients[0], "todo", "task", "assigned")}
			switch scenario {
			case "done":
				todos.items[0].Status = todoapp.TodoStatusDone
			case "reassigned":
				todos.items[0].AssigneeID = "human-b"
			case "opted_out":
				s.recipients = nil
			case "disabled":
				auth.account.Enabled = false
			case "unbound":
				s.targets = &recordingPersonalAssistantApprovalNotificationSource{}
			case "scope_revoked":
				auth.resolved = nil
			case "other_account":
				delivery.AccountID = "other"
			case "other_tenant":
				delivery.TenantID = "other"
			case "malformed_key":
				delivery.IdempotencyKey = "personal-assistant:attention:v1:broken"
			case "read_error":
				todos.getErr = errors.New("unavailable")
			}
			ok, err := (productionPersonalAssistantNotificationGuard{attention: s, replies: s.replies}).Validate(context.Background(), delivery)
			if ok != (scenario == "pending") || (err != nil) != (scenario == "read_error") {
				t.Fatalf("eligible=%v err=%v", ok, err)
			}
		})
	}
}

func TestPersonalAssistantNotificationGuardRechecksCouncilVote(t *testing.T) {
	s, _, decisions, _, _ := attentionFixture()
	decisions.items = []hil.DecisionRequest{{ID: "council", TenantID: "tenant-a", Status: hil.DecisionPending}}
	delivery := gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "weixin-a", ChannelKey: "weixin", IdempotencyKey: personalAssistantAttentionKey(s.recipients[0], "council", "council", "vote")}
	guard := productionPersonalAssistantNotificationGuard{attention: s, replies: s.replies}
	if ok, err := guard.Validate(context.Background(), delivery); err != nil || !ok {
		t.Fatalf("pending: %v %v", ok, err)
	}
	decisions.councilVoted = true
	if ok, err := guard.Validate(context.Background(), delivery); err != nil || ok {
		t.Fatalf("already voted: %v %v", ok, err)
	}
}
