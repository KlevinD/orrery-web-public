package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/app/userauth"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type attentionTodosStub struct {
	items     []todoapp.TodoItem
	fresh     map[string]todoapp.TodoItem
	getErr    error
	listCalls int
}

func (s *attentionTodosStub) ListTodos(_ context.Context, viewer todoapp.ViewerRef, q todoapp.ListTodosQuery) (todoapp.TodoPage, error) {
	s.listCalls++
	if viewer.TenantID != "tenant-a" || viewer.AuthorizationSubjectID != "human-a" {
		return todoapp.TodoPage{}, todoapp.ErrForbidden
	}
	if q.Offset >= len(s.items) {
		return todoapp.TodoPage{}, nil
	}
	end := q.Offset + q.Limit
	if end > len(s.items) {
		end = len(s.items)
	}
	return todoapp.TodoPage{Todos: s.items[q.Offset:end], Total: len(s.items), HasMore: end < len(s.items)}, nil
}

func (s *attentionTodosStub) GetTodo(_ context.Context, viewer todoapp.ViewerRef, id string) (todoapp.TodoItem, error) {
	if s.getErr != nil {
		return todoapp.TodoItem{}, s.getErr
	}
	if item, ok := s.fresh[id]; ok {
		return item, nil
	}
	for _, item := range s.items {
		if item.TodoID == id && item.TenantID == viewer.TenantID {
			return item, nil
		}
	}
	return todoapp.TodoItem{}, todoapp.ErrNotFound
}

type attentionDecisionsStub struct {
	items           []hil.DecisionRequest
	noLongerPending bool
	queries         []hil.DecisionListQuery
	councilExpired  bool
	councilVoted    bool
}

func (s *attentionDecisionsStub) GetCouncilByRequestID(_ context.Context, tenantID, decisionID string) (hil.CouncilSession, error) {
	council := hil.CouncilSession{TenantID: tenantID, RequestID: decisionID, Status: hil.CouncilOpen}
	council.Participants = []hil.CouncilParticipant{{Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "human-a"}}}
	if s.councilVoted {
		council.Votes = []hil.CouncilVote{{Actor: council.Participants[0].Actor, Choice: hil.VoteApprove}}
	}
	if s.councilExpired {
		council.ExpiresAt = time.Now().Add(-time.Minute)
	}
	return council, nil
}

func (s *attentionDecisionsStub) ListDecisions(_ context.Context, q hil.DecisionListQuery) (hil.DecisionPage, error) {
	s.queries = append(s.queries, q)
	if !q.Mine || q.TenantID != "tenant-a" || q.ActionableBy.ID != "human-a" || q.ActionableBy.Kind != hil.ActorHuman || q.Mode != hil.ModeCouncil || q.Status != hil.DecisionPending {
		return hil.DecisionPage{}, hil.ErrInvalidRequest
	}
	if q.DecisionID != "" {
		if s.noLongerPending {
			return hil.DecisionPage{}, nil
		}
		for _, item := range s.items {
			if item.ID == q.DecisionID {
				return hil.DecisionPage{Items: []hil.DecisionRequest{item}, Total: 1}, nil
			}
		}
		return hil.DecisionPage{}, nil
	}
	return hil.DecisionPage{Items: s.items, Total: len(s.items)}, nil
}

type attentionDeliveriesStub struct {
	recordingProductionHILGatewayDeliveries
	fail bool
}

func (s *attentionDeliveriesStub) Enqueue(ctx context.Context, command gwoutbound.EnqueueCommand) ([]gwoutbound.Delivery, error) {
	if s.fail {
		return nil, errors.New("queue unavailable")
	}
	if s.existingKeys == nil {
		s.existingKeys = map[string]bool{}
	}
	s.existingKeys[command.IdempotencyKey] = true
	return s.recordingProductionHILGatewayDeliveries.Enqueue(ctx, command)
}

func attentionFixture() (productionPersonalAssistantAttentionSweeper, *attentionTodosStub, *attentionDecisionsStub, *attentionDeliveriesStub, *recordingGatewayViewerAuthorization) {
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-a": {ID: "human-a", Enabled: true, AgentKind: agentcore.AgentKindHuman, OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}}},
	}}
	auth := &recordingGatewayViewerAuthorization{
		account:  userauth.Account{UserID: "owner-a", Username: "alice", AgentProfileID: "human-a", Enabled: true},
		resolved: []string{"todo.read", "approvals.read"},
	}
	todos := &attentionTodosStub{}
	decisions := &attentionDecisionsStub{}
	deliveries := &attentionDeliveriesStub{}
	s := productionPersonalAssistantAttentionSweeper{
		replies:    &agentNoticeWriterStub{},
		recipients: []productionPersonalAssistantAttentionRecipient{{TenantID: "tenant-a", HumanProfileID: "human-a"}},
		profiles:   profiles, accounts: auth, scopes: auth, todos: todos, decisions: decisions, deliveries: deliveries,
		targets: &recordingPersonalAssistantApprovalNotificationSource{targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"tenant-a": {TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "weixin-a"},
		}}, publicOrigin: "https://orrery.example.test",
	}
	return s, todos, decisions, deliveries, auth
}

func humanAttentionTodo(id string) todoapp.TodoItem {
	return todoapp.TodoItem{TenantID: "tenant-a", TodoID: id, Title: "Check customer quote", Description: "Confirm amount; api_key=private-value", Status: todoapp.TodoStatusPlanned, AssigneeID: "human-a", AssigneeKind: todoapp.AssigneeKindHuman, AutomationMode: todoapp.TodoAutomationModeManual}
}

func TestPersonalAssistantAttentionRequiresExplicitRecipients(t *testing.T) {
	for _, raw := range []string{"", "[]", "null"} {
		r, err := productionPersonalAssistantAttentionRecipients(raw)
		if err != nil || len(r) != 0 {
			t.Fatalf("empty opt-in=%+v %v", r, err)
		}
	}
	valid := `[{"tenant_id":"tenant-a","human_profile_id":"human-a"}]`
	if recipients, err := productionPersonalAssistantAttentionRecipients(valid); err != nil || len(recipients) != 1 {
		t.Fatal(err)
	}
	for _, raw := range []string{`[{"tenant_id":"tenant-a"}]`, `[{"tenant_id":"tenant-a","human_profile_id":"human-a","broadcast":true}]`, valid + `[]`, `[{"tenant_id":"t","human_profile_id":"h"},{"tenant_id":"t","human_profile_id":"h"}]`} {
		if _, err := productionPersonalAssistantAttentionRecipients(raw); err == nil {
			t.Fatalf("invalid opt-in accepted: %s", raw)
		}
	}
	s, todos, _, deliveries, _ := attentionFixture()
	s.recipients = nil
	todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
	if err := s.Sweep(context.Background()); err != nil || todos.listCalls != 0 || len(deliveries.commands) != 0 {
		t.Fatal("empty heartbeat did work")
	}
}

func TestPersonalAssistantAttentionDeduplicatesAcrossFreshWorkers(t *testing.T) {
	s, todos, _, deliveries, _ := attentionFixture()
	todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(deliveries.commands) != 1 {
		t.Fatalf("deliveries=%d", len(deliveries.commands))
	}
	command := deliveries.commands[0]
	if command.EventType == "approval" || command.HumanProfileID != "human-a" || command.AccountID != "weixin-a" || command.TenantID != "tenant-a" || strings.Contains(command.Body, "private-value") || !strings.Contains(command.Body, "/todos/task") {
		t.Fatalf("unsafe delivery: %+v", command)
	}
	todos.items[0].Version++
	todos.items[0].Title = "Updated title"
	for i := 0; i < 3; i++ {
		restarted := s
		if err := restarted.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(deliveries.commands) != 1 {
		t.Fatal("heartbeat or title update repeated external notification")
	}
	// A distinct human-action state deserves its own notification, once.
	todos.items[0].Status = todoapp.TodoStatusBlocked
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(deliveries.commands) != 2 {
		t.Fatal("new blocker was not notified")
	}
}

func TestPersonalAssistantAttentionRetriesQueueFailure(t *testing.T) {
	s, todos, _, deliveries, _ := attentionFixture()
	todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
	deliveries.fail = true
	if err := s.Sweep(context.Background()); err == nil {
		t.Fatal("queue error lost")
	}
	deliveries.fail = false
	if err := s.Sweep(context.Background()); err != nil || len(deliveries.commands) != 1 {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestPersonalAssistantAttentionRechecksAuthorizationAndState(t *testing.T) {
	for _, scenario := range []string{"disabled", "revoked", "other_owner", "unbound", "other_tenant", "completed", "reassigned", "forbidden"} {
		t.Run(scenario, func(t *testing.T) {
			s, todos, _, deliveries, auth := attentionFixture()
			item := humanAttentionTodo("task")
			todos.items = []todoapp.TodoItem{item}
			switch scenario {
			case "disabled":
				auth.account.Enabled = false
			case "revoked":
				auth.resolved = nil
			case "other_owner":
				auth.account.UserID = "other-owner"
			case "unbound":
				s.targets = &recordingPersonalAssistantApprovalNotificationSource{}
			case "other_tenant":
				s.recipients[0].TenantID = "tenant-b"
			case "completed":
				item.Status = todoapp.TodoStatusDone
				todos.fresh = map[string]todoapp.TodoItem{"task": item}
			case "reassigned":
				item.AssigneeID = "human-b"
				todos.fresh = map[string]todoapp.TodoItem{"task": item}
			case "forbidden":
				todos.getErr = todoapp.ErrForbidden
			}
			if err := s.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(deliveries.commands) != 0 {
				t.Fatalf("unsafe external delivery for %s", scenario)
			}
		})
	}
}

func TestPersonalAssistantAttentionScansBeyondFirstPage(t *testing.T) {
	s, todos, _, deliveries, _ := attentionFixture()
	for i := 0; i < 100; i++ {
		item := humanAttentionTodo("unrelated")
		item.AssigneeID = "human-b"
		todos.items = append(todos.items, item)
	}
	todos.items = append(todos.items, humanAttentionTodo("last-task"))
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if todos.listCalls != 2 || len(deliveries.commands) != 1 {
		t.Fatalf("page scan=%d deliveries=%d", todos.listCalls, len(deliveries.commands))
	}
}

func TestPersonalAssistantAttentionCouncilRequiresPendingVote(t *testing.T) {
	for _, scenario := range []string{"pending", "voted", "expired", "council_expired", "concurrent_vote"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, decisions, deliveries, _ := attentionFixture()
			d := hil.DecisionRequest{ID: "decision", TenantID: "tenant-a", Status: hil.DecisionPending, PayloadPreview: map[string]any{"title": "Review release"}}
			if scenario == "voted" {
				decisions.noLongerPending = true
			}
			if scenario == "expired" {
				d.ExpiresAt = time.Now().Add(-time.Minute)
			}
			decisions.councilExpired = scenario == "council_expired"
			decisions.councilVoted = scenario == "concurrent_vote"
			decisions.items = []hil.DecisionRequest{d}
			if err := s.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 0
			if scenario == "pending" {
				want = 1
			}
			if len(deliveries.commands) != want {
				t.Fatalf("deliveries=%d want %d", len(deliveries.commands), want)
			}
			if want == 1 && (deliveries.commands[0].EventType == "approval" || !strings.Contains(deliveries.commands[0].Body, "mode=council")) {
				t.Fatal("council was routed as approval")
			}
		})
	}
}

func TestPersonalAssistantAttentionOnlyNotifiesHumanAction(t *testing.T) {
	viewer := todoapp.ViewerRef{TenantID: "tenant-a", ViewerID: "alice", AuthorizationSubjectID: "human-a"}
	for _, tt := range []struct {
		name   string
		mutate func(*todoapp.TodoItem)
		want   string
	}{
		{"assigned", func(*todoapp.TodoItem) {}, "assigned"},
		{"other tenant", func(item *todoapp.TodoItem) { item.TenantID = "tenant-b" }, ""},
		{"agent task", func(item *todoapp.TodoItem) { item.AssigneeKind = todoapp.AssigneeKindAgent }, ""},
		{"automatic work", func(item *todoapp.TodoItem) { item.AutomationMode = todoapp.TodoAutomationModeManaged }, ""},
		{"blocked automatic work", func(item *todoapp.TodoItem) {
			item.AutomationMode = todoapp.TodoAutomationModeManaged
			item.Status = todoapp.TodoStatusBlocked
		}, "blocked"},
		{"review for someone else", func(item *todoapp.TodoItem) { item.Status = todoapp.TodoStatusReview; item.ReviewerID = "human-b" }, ""},
		{"my review", func(item *todoapp.TodoItem) {
			item.Status = todoapp.TodoStatusReview
			item.ReviewerID = "human-a"
			item.AssigneeID = "agent-a"
			item.AssigneeKind = todoapp.AssigneeKindAgent
		}, "review"},
		{"my blocked request", func(item *todoapp.TodoItem) {
			item.Status = todoapp.TodoStatusBlocked
			item.CreatedBy = "alice"
			item.AssigneeID = "agent-a"
			item.AssigneeKind = todoapp.AssigneeKindAgent
		}, "blocked"},
		{"terminal", func(item *todoapp.TodoItem) { item.Status = todoapp.TodoStatusDone }, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			item := humanAttentionTodo("task")
			tt.mutate(&item)
			if got := personalAssistantTodoAttention(item, viewer); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}
