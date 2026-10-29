package missioncontrol_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
)

func TestApprovalServiceProjectsOnlyActionableApprovalItems(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, nil)

	page, err := service.ListApprovals(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalListQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListApprovals returned error: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("approval count = %d, want only QA approvals and no inbox notification items: %+v", len(page.Items), page.Items)
	}
	qa := page.Items[0]
	if qa.ApprovalID != "qa:mission-1:qa-1" || qa.ApprovalType != "qa" || qa.Status != "pending" {
		t.Fatalf("qa approval mismatch: %+v", qa)
	}
	if qa.MissionID != "mission-1" || qa.TaskID != "task-1" || qa.SourceType != "qa" || qa.SourceID != "qa-1" {
		t.Fatalf("qa source mismatch: %+v", qa)
	}
	if len(qa.AvailableActions) != 2 || qa.AvailableActions[0].ActionID != "approve" || qa.AvailableActions[1].ActionID != "reject" {
		t.Fatalf("qa actions mismatch: %+v", qa.AvailableActions)
	}
	if !qa.AvailableActions[0].ConfirmationRequired || !qa.AvailableActions[1].ReasonRequired || !qa.AvailableActions[1].RequiresBody {
		t.Fatalf("qa action metadata mismatch: %+v", qa.AvailableActions)
	}
	for _, item := range page.Items {
		if item.ApprovalType == "inbox_action" || item.SourceType == "inbox" {
			t.Fatalf("inbox notification leaked into approval center: %+v", item)
		}
	}
}

func TestApprovalServiceProjectsBusinessApprovalSources(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	model.ApprovalRequests = []projection.ApprovalRequestView{
		{RequestID: "perm-1", ApprovalType: "permission_request", Status: "pending", Priority: "high", MissionID: "mission-1", TaskID: "task-1", SourceType: "permission", SourceID: "perm.deploy", Title: "Permission request", Summary: "Need deploy permission", RequestedBy: "agent-1", AssignedTo: "ops-lead", CreatedAt: at},
		{RequestID: "tool-1", ApprovalType: "tool_access", Status: "pending", Priority: "high", MissionID: "mission-1", TaskID: "task-1", SourceType: "tool", SourceID: "tool.deploy", Title: "Tool access", Summary: "Need deploy tool", RequestedBy: "agent-1", AssignedTo: "ops-lead", CreatedAt: at.Add(time.Minute)},
		{RequestID: "evidence-1", ApprovalType: "evidence_review", Status: "pending", Priority: "normal", MissionID: "mission-1", TaskID: "task-1", SourceType: "evidence", SourceID: "evidence-visible", Title: "Evidence review", Summary: "Review submitted evidence", RequestedBy: "agent-1", AssignedTo: "qa-lead", EvidenceRefs: []string{"evidence-visible"}, CreatedAt: at.Add(2 * time.Minute)},
		{RequestID: "complete-1", ApprovalType: "task_completion", Status: "pending", Priority: "normal", MissionID: "mission-1", TaskID: "task-1", SourceType: "task", SourceID: "task-1", Title: "Task completion", Summary: "Approve task completion", RequestedBy: "agent-1", AssignedTo: "review-agent", EvidenceRefs: []string{"evidence-visible"}, CreatedAt: at.Add(3 * time.Minute)},
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, nil)

	page, err := service.ListApprovals(ctx, missioncontrol.ViewerRef{ViewerID: "ops-lead", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalListQuery{MissionID: "mission-1", Status: "pending"})
	if err != nil {
		t.Fatalf("ListApprovals returned error: %v", err)
	}
	byType := approvalsByType(page.Items)
	for _, typ := range []string{"permission_request", "tool_access", "evidence_review", "task_completion"} {
		if byType[typ] == nil {
			t.Fatalf("missing approval type %s in %+v", typ, page.Items)
		}
		if byType[typ].ApprovalID == "" || !hasApprovalDecision(byType[typ].AvailableActions, "approved") || !hasApprovalDecision(byType[typ].AvailableActions, "denied") {
			t.Fatalf("approval %s should have stable id and allow/deny actions: %+v", typ, byType[typ])
		}
	}
	if byType["permission_request"].ApprovalID != "permission_request:mission-1:perm-1" {
		t.Fatalf("permission approval id = %s", byType["permission_request"].ApprovalID)
	}
	if byType["task_completion"].TaskID != "task-1" || byType["task_completion"].EvidenceRefs[0] != "evidence-visible" {
		t.Fatalf("task completion approval = %+v", byType["task_completion"])
	}
}

func TestApprovalItemsExposeOrganizationRiskPolicyAndDecisionEffect(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	expiresAt := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	model.ApprovalRequests = []projection.ApprovalRequestView{{
		RequestID:    "tool-1",
		ApprovalType: "tool_access",
		Status:       "pending",
		Priority:     "critical",
		MissionID:    "mission-1",
		TaskID:       "task-1",
		SourceType:   "tool",
		SourceID:     "tool.deploy",
		Title:        "Tool access",
		Summary:      "Need production deployment access",
		RequestedBy:  "agent-ops",
		AssignedTo:   "dept-owner",
		CreatedAt:    time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]string{
			"organization_code":       "01.01",
			"organization_name":       "AI Platform",
			"requested_by_actor_type": "agent",
			"requested_by_actor_id":   "agent-ops",
			"target_actor_type":       "human",
			"target_actor_id":         "human-ops",
			"policy_id":               "risk:high",
			"risk_level":              "critical",
			"decision_effect":         "approve_session",
			"expires_at":              expiresAt.Format(time.RFC3339),
		},
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, nil)

	page, err := service.ListApprovals(ctx, missioncontrol.ViewerRef{ViewerID: "dept-owner", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalListQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListApprovals returned error: %v", err)
	}
	item := approvalsByType(page.Items)["tool_access"]
	if item == nil {
		t.Fatalf("missing tool_access approval in %+v", page.Items)
	}
	if item.OrganizationCode != "01.01" || item.OrganizationName != "AI Platform" || item.PolicyID != "risk:high" || item.RiskLevel != "critical" || item.DecisionEffect != "approve_session" {
		t.Fatalf("approval metadata = %+v", item)
	}
	if item.RequestedByActor.ActorType != "agent" || item.RequestedByActor.ActorID != "agent-ops" {
		t.Fatalf("requested actor = %+v", item.RequestedByActor)
	}
	if item.TargetActor.ActorType != "human" || item.TargetActor.ActorID != "human-ops" {
		t.Fatalf("target actor = %+v", item.TargetActor)
	}
	if !item.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", item.ExpiresAt, expiresAt)
	}
}

func TestApprovalServiceProjectsRichHILApprovalShapes(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	choices, err := json.Marshal([]missioncontrol.ApprovalChoiceDTO{
		{ChoiceID: "continue", Label: "Continue", Description: "Proceed with current scope"},
		{ChoiceID: "narrow", Label: "Narrow scope", Description: "Limit the operation"},
	})
	if err != nil {
		t.Fatalf("marshal choices: %v", err)
	}
	model.ApprovalRequests = []projection.ApprovalRequestView{
		{
			RequestID:    "cmd-1",
			ApprovalType: "permission_request",
			Status:       "pending",
			Priority:     "high",
			MissionID:    "mission-1",
			TaskID:       "task-1",
			SourceType:   "provider_tool",
			SourceID:     "codex:exec_command",
			Title:        "Approve exec_command",
			Summary:      "Run tests before committing",
			RequestedBy:  "agent-1",
			CreatedAt:    at,
			Metadata: map[string]string{
				"approval_source":          "codex",
				"approval_kind":            "command_execution",
				"approval_tool_name":       "exec_command",
				"approval_conversation_id": "conv-1",
				"approval_request_id":      "cmd-1",
				"approval_run_id":          "run-blocked",
				"command":                  "go test ./...",
				"risk_level":               "high",
				"risk_summary":             "Runs a shell command in the workspace.",
			},
		},
		{
			RequestID:    "patch-1",
			ApprovalType: "permission_request",
			Status:       "pending",
			MissionID:    "mission-1",
			SourceType:   "provider_tool",
			SourceID:     "codex:patch_apply",
			Title:        "Apply patch",
			Summary:      "Modify approval UI",
			RequestedBy:  "agent-1",
			CreatedAt:    at,
			Metadata: map[string]string{
				"approval_source":    "codex",
				"approval_kind":      "file_change",
				"approval_tool_name": "patch_apply",
				"diff":               "--- a/ui\n+++ b/ui\n@@",
			},
		},
		{
			RequestID:    "ask-1",
			ApprovalType: "permission_request",
			Status:       "pending",
			MissionID:    "mission-1",
			SourceType:   "provider_tool",
			SourceID:     "claude:AskUserQuestion",
			Title:        "Need direction",
			Summary:      "Choose rollout strategy",
			RequestedBy:  "agent-1",
			CreatedAt:    at,
			Metadata: map[string]string{
				"approval_source":    "claude",
				"approval_kind":      "mcp_elicitation",
				"approval_tool_name": "AskUserQuestion",
				"question":           "Which rollout strategy should I use?",
				"choices_json":       string(choices),
			},
		},
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, nil)

	page, err := service.ListApprovals(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalListQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListApprovals returned error: %v", err)
	}
	byID := approvalsByID(page.Items)
	command := byID["permission_request:mission-1:cmd-1"]
	if command == nil {
		t.Fatalf("missing command approval in %+v", page.Items)
	}
	if command.DisplayKind != "command_execution" || command.Provider != "codex" || command.ToolName != "exec_command" || command.PayloadPreview.Content != "go test ./..." || command.RiskSummary == "" {
		t.Fatalf("command approval shape = %+v", command)
	}
	if len(command.AvailableActions) != 4 ||
		command.AvailableActions[0].ActionID != "approve_once" ||
		command.AvailableActions[1].ActionID != "approve_session" ||
		!command.AvailableActions[1].RememberAllowed ||
		command.AvailableActions[2].ActionID != "deny" ||
		command.AvailableActions[2].InputKind != "message" ||
		command.AvailableActions[3].ActionID != "other" {
		t.Fatalf("command actions = %+v", command.AvailableActions)
	}
	for _, action := range command.AvailableActions {
		if action.Path != "/api/discussions/conv-1/approvals/cmd-1/decision" {
			t.Fatalf("command approval action path = %q, want discussion runtime decision path", action.Path)
		}
	}
	if command.PayloadPreview.Fields["approval_conversation_id"] != "conv-1" || command.PayloadPreview.Fields["approval_request_id"] != "cmd-1" || command.PayloadPreview.Fields["approval_run_id"] != "run-blocked" {
		t.Fatalf("command payload metadata = %+v", command.PayloadPreview.Fields)
	}
	patch := byID["permission_request:mission-1:patch-1"]
	if patch == nil || patch.DisplayKind != "file_change" || patch.PayloadPreview.Content == "" || patch.AvailableActions[2].ActionID != "deny" || patch.AvailableActions[3].ActionID != "other" {
		t.Fatalf("patch approval shape = %+v", patch)
	}
	question := byID["permission_request:mission-1:ask-1"]
	if question == nil || question.DisplayKind != "user_input" || len(question.Choices) != 2 || question.AvailableActions[0].ActionID != "approve_once" || question.AvailableActions[3].ActionID != "other" {
		t.Fatalf("question approval shape = %+v", question)
	}
}

func TestApprovalServiceListsApprovalsAcrossMissionsWhenMissionFilterIsEmpty(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	first := appReadModel()
	second := appReadModel()
	second.Mission.MissionID = "mission-2"
	second.Checkpoint.MissionID = "mission-2"
	second.TaskBoard.Tasks[0].TaskID = "task-2"
	second.QA.Checks[0].QARef = "qa-2"
	second.QA.Checks[0].TaskID = "task-2"
	second.InboxSummary.Items = nil
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("Save first returned error: %v", err)
	}
	if err := repo.Save(ctx, second); err != nil {
		t.Fatalf("Save second returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, nil)

	page, err := service.ListApprovals(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalListQuery{Status: "pending"})
	if err != nil {
		t.Fatalf("ListApprovals returned error: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("approval count = %d, want approvals across missions: %+v", len(page.Items), page.Items)
	}
	byID := map[string]struct{}{}
	for _, item := range page.Items {
		byID[item.ApprovalID] = struct{}{}
	}
	for _, id := range []string{"qa:mission-1:qa-1", "qa:mission-2:qa-2"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing approval %s in %+v", id, page.Items)
		}
	}
	if _, ok := byID["inbox:inbox-1"]; ok {
		t.Fatalf("inbox notification leaked into cross-mission approvals: %+v", page.Items)
	}
}

func TestApprovalServiceBatchAuthorizesMissionsWhenListingAcrossMissions(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	for _, missionID := range []string{"mission-1", "mission-2"} {
		model := appReadModel()
		model.Mission.MissionID = missioncore.MissionID(missionID)
		model.Checkpoint.MissionID = missioncore.MissionID(missionID)
		if err := repo.Save(ctx, model); err != nil {
			t.Fatalf("Save(%s): %v", missionID, err)
		}
	}
	adapter := &recordingApprovalAuthorizationAdapter{}
	service := missioncontrol.NewApprovalService(repo, nil)
	service.SetAuthorizationService(authorization.NewService(adapter))

	if _, err := service.ListApprovals(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalListQuery{}); err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if adapter.batchCalls != 1 || adapter.checkCalls != 0 || adapter.batchSize != 2 {
		t.Fatalf("batch calls=%d size=%d single calls=%d, want 1 size 2 and 0", adapter.batchCalls, adapter.batchSize, adapter.checkCalls)
	}
}

type recordingApprovalAuthorizationAdapter struct {
	checkCalls int
	batchCalls int
	batchSize  int
}

func (a *recordingApprovalAuthorizationAdapter) Check(context.Context, authorization.CheckRequest) (authorization.Decision, error) {
	a.checkCalls++
	return authorization.Decision{Allowed: true, Source: "test"}, nil
}

func (a *recordingApprovalAuthorizationAdapter) BatchCheck(_ context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	a.batchCalls++
	a.batchSize = len(requests)
	decisions := make([]authorization.Decision, len(requests))
	for i := range decisions {
		decisions[i] = authorization.Decision{Allowed: true, Source: "test"}
	}
	return decisions, nil
}

func (*recordingApprovalAuthorizationAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, authorization.ErrUnsupported
}

func (*recordingApprovalAuthorizationAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return authorization.ErrUnsupported
}

func (*recordingApprovalAuthorizationAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return authorization.ErrUnsupported
}

func TestCommandServiceDecideBusinessApprovalRecordsResolvedProjection(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.ApprovalRequests = []projection.ApprovalRequestView{{
		RequestID:    "tool-1",
		ApprovalType: "tool_access",
		Status:       "pending",
		MissionID:    "mission-1",
		Metadata:     map[string]string{"approval_kind": "tool", "scope": "deploy:delete"},
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(&capturingRunStore{}, repo, events, &sequenceContextIDGenerator{values: []string{"evt-approval-decided"}}, fixedClock{at: at})

	receipt, err := service.DecideBusinessApproval(ctx, missioncontrol.ViewerRef{ViewerID: "ops-lead", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.BusinessApprovalDecisionCommand{
		RequestID:      "corr-approval",
		IdempotencyKey: "approval-key",
		ApprovalType:   "tool_access",
		MissionID:      "mission-1",
		ApprovalRef:    "tool-1",
		Decision:       "approved",
		ActionID:       "approve_session",
		Message:        "allowed in this session",
		Metadata:       map[string]string{"decision_effect": "session", "scope": "deploy:delete"},
	})
	if err != nil {
		t.Fatalf("DecideBusinessApproval returned error: %v", err)
	}
	if receipt.CommandID != "tool-1" || receipt.Status != "accepted" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(events.events) != 1 || events.events[0].EventType != "ApprovalDecided" {
		t.Fatalf("events = %+v", events.events)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if updated.ApprovalRequests[0].Status != "approved" || !updated.ApprovalRequests[0].DecidedAt.Equal(at) || updated.ApprovalRequests[0].Metadata["decision_effect"] != "session" {
		t.Fatalf("updated approval = %+v", updated.ApprovalRequests[0])
	}
}

func TestCommandServiceDecideProviderApprovalQueuesContinuationRun(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 20, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Discussion.DiscussionGroupID = "discussion-1"
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID:        "run-blocked",
		AgentID:      "agent-1",
		TaskID:       "task-1",
		RuntimeKind:  "codex",
		QueueID:      "discussion:discussion-1",
		Status:       projection.RuntimeSessionBlocked,
		ErrorSummary: "waiting for approval",
		CompletedAt:  at.Add(-time.Minute),
		UpdatedAt:    at.Add(-time.Minute),
	}}
	model.ApprovalRequests = []projection.ApprovalRequestView{{
		RequestID:    "cmd-1",
		ApprovalType: "command_execution",
		Status:       "pending",
		MissionID:    "mission-1",
		TaskID:       "task-1",
		SourceType:   "provider_tool",
		SourceID:     "codex:exec_command",
		Title:        "Approve exec_command",
		Summary:      "Run tests",
		RequestedBy:  "agent-1",
		CreatedAt:    at.Add(-time.Minute),
		Metadata: map[string]string{
			"approval_source":     "codex",
			"approval_provider":   "codex",
			"approval_request_id": "cmd-1",
			"approval_session_id": "thread-1",
			"approval_turn_id":    "turn-1",
			"approval_kind":       "command_execution",
			"approval_type":       "command_execution",
			"approval_tool_name":  "exec_command",
			"approval_run_id":     "run-blocked",
			"command":             "go test ./...",
		},
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"evt-approval-decided", "run-continuation", "evt-run-continuation"}}, fixedClock{at: at})

	receipt, err := service.DecideBusinessApproval(ctx, missioncontrol.ViewerRef{ViewerID: "ops-lead", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.BusinessApprovalDecisionCommand{
		RequestID:      "corr-approval",
		IdempotencyKey: "approval-key",
		ApprovalType:   "command_execution",
		MissionID:      "mission-1",
		ApprovalRef:    "cmd-1",
		Decision:       "approved",
		ActionID:       "approve_session",
		Message:        "tests are fine",
		Remember:       true,
		Metadata: map[string]string{
			"approval_source":     "codex",
			"approval_provider":   "codex",
			"approval_request_id": "cmd-1",
			"approval_session_id": "thread-1",
			"approval_turn_id":    "turn-1",
			"approval_kind":       "command_execution",
			"approval_type":       "command_execution",
			"approval_tool_name":  "exec_command",
			"approval_run_id":     "run-blocked",
			"decision_effect":     "session",
		},
	})
	if err != nil {
		t.Fatalf("DecideBusinessApproval returned error: %v", err)
	}
	if receipt.CommandID != "cmd-1" || receipt.Status != "accepted" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(runStore.runs) != 1 {
		t.Fatalf("continuation runs = %+v", runStore.runs)
	}
	run := runStore.runs[0]
	if run.RunID != "run-continuation" || run.RuntimeID != "codex" || run.Request.RuntimeOptions.ResumeSessionID != "thread-1" {
		t.Fatalf("continuation identity/resume = %+v", run)
	}
	if got := run.Request.RuntimeOptions.ProviderSpecific["approval_continuation"]; got != "true" {
		t.Fatalf("provider specific = %+v", run.Request.RuntimeOptions.ProviderSpecific)
	}
	if run.Request.RuntimeOptions.ProviderSpecific["approval_effect"] != "session" || run.Request.RuntimeOptions.ProviderSpecific["approval_continuation_of_run_id"] != "run-blocked" {
		t.Fatalf("provider specific continuation metadata = %+v", run.Request.RuntimeOptions.ProviderSpecific)
	}
	if len(events.events) != 2 || events.events[0].EventType != "ApprovalDecided" || events.events[1].EventType != "AgentRunQueued" {
		t.Fatalf("events = %+v", events.events)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if len(updated.RuntimeSessions) != 2 || updated.RuntimeSessions[1].RunID != "run-continuation" || updated.RuntimeSessions[1].Status != projection.RuntimeSessionQueued {
		t.Fatalf("runtime sessions = %+v", updated.RuntimeSessions)
	}
}

func TestApprovalServiceDecidesQAApprovalByResolvingTaskID(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appReadModel()); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	commands := &fakeApprovalCommandPort{receipt: missioncontrol.CommandReceiptDTO{CommandID: "ok", Status: "accepted", AcceptedAt: time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)}}
	service := newAuthorizedApprovalService(repo, commands)

	_, err := service.DecideApproval(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "approve-key",
		ApprovalID:     "qa:mission-1:qa-1",
		ActionID:       "approve",
		Message:        "ok",
	})
	if err != nil {
		t.Fatalf("DecideApproval qa returned error: %v", err)
	}
	if commands.qa.TaskID != "task-1" {
		t.Fatalf("qa command should resolve task id from approval id: %+v", commands.qa)
	}
}

func TestApprovalServiceRejectsQAApprovalWhenBodyTaskIDDoesNotMatchQARef(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appReadModel()); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	commands := &fakeApprovalCommandPort{receipt: missioncontrol.CommandReceiptDTO{CommandID: "ok", Status: "accepted", AcceptedAt: time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)}}
	service := newAuthorizedApprovalService(repo, commands)

	_, err := service.DecideApproval(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "approve-key",
		ApprovalID:     "qa:mission-1:qa-1",
		ActionID:       "approve",
		TaskID:         "task-other",
		Message:        "ok",
	})
	if err == nil {
		t.Fatalf("expected task_id mismatch to be rejected")
	}
	if commands.qa.TaskID != "" {
		t.Fatalf("qa command should not be submitted on mismatch: %+v", commands.qa)
	}
}

func TestApprovalServiceDecidesQAAndInboxApprovals(t *testing.T) {
	ctx := context.Background()
	commands := &fakeApprovalCommandPort{receipt: missioncontrol.CommandReceiptDTO{CommandID: "ok", Status: "accepted", AcceptedAt: time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)}}
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appReadModel()); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, commands)
	viewer := missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}

	receipt, err := service.DecideApproval(ctx, viewer, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "approve-key",
		ApprovalID:     "qa:mission-1:qa-1",
		ActionID:       "approve",
		TaskID:         "task-1",
		Message:        "ok",
	})
	if err != nil {
		t.Fatalf("DecideApproval qa returned error: %v", err)
	}
	if receipt.CommandID != "ok" {
		t.Fatalf("qa receipt = %+v", receipt)
	}
	if commands.qa.QARef != "qa-1" || commands.qa.MissionID != "mission-1" || commands.qa.TaskID != "task-1" || commands.qa.Decision != "approved" {
		t.Fatalf("qa command mismatch: %+v", commands.qa)
	}

	_, err = service.DecideApproval(ctx, viewer, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "close-key",
		ApprovalID:     "inbox:inbox-1",
		ActionID:       "close",
		ReplyMessageID: "reply-1",
	})
	if err != nil {
		t.Fatalf("DecideApproval inbox returned error: %v", err)
	}
	if commands.close.InboxItemID != "inbox-1" || commands.close.ReplyMessageID != "reply-1" {
		t.Fatalf("close command mismatch: %+v", commands.close)
	}

	_, err = service.DecideApproval(ctx, viewer, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "read-key",
		ApprovalID:     "inbox:inbox-2",
		ActionID:       "read",
	})
	if err != nil {
		t.Fatalf("DecideApproval read returned error: %v", err)
	}
	if commands.read.InboxItemID != "inbox-2" {
		t.Fatalf("read command mismatch: %+v", commands.read)
	}
}

func TestApprovalServiceDecidesBusinessApprovalThroughGenericPort(t *testing.T) {
	ctx := context.Background()
	commands := &fakeApprovalCommandPort{receipt: missioncontrol.CommandReceiptDTO{CommandID: "approval-ok", Status: "accepted", AcceptedAt: time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)}}
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.ApprovalRequests = []projection.ApprovalRequestView{{
		RequestID:    "tool-1",
		ApprovalType: "tool_access",
		Status:       "pending",
		MissionID:    "mission-1",
		Metadata:     map[string]string{"agent_id": "agent-ops", "tool_id": "tool.deploy", "scope": "deploy:delete"},
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, commands)

	receipt, err := service.DecideApproval(ctx, missioncontrol.ViewerRef{ViewerID: "ops-lead", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "approval-key",
		ApprovalID:     "tool_access:mission-1:tool-1",
		ActionID:       "approve",
		Message:        "allowed for deployment",
		Metadata:       map[string]string{"agent_id": "attacker", "tool_id": "tool.root", "scope": "root"},
	})
	if err != nil {
		t.Fatalf("DecideApproval returned error: %v", err)
	}
	if receipt.CommandID != "approval-ok" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if commands.approval.ApprovalType != "tool_access" || commands.approval.MissionID != "mission-1" || commands.approval.ApprovalRef != "tool-1" || commands.approval.Decision != "approved" {
		t.Fatalf("generic approval command = %+v", commands.approval)
	}
	if commands.approval.Metadata["agent_id"] != "agent-ops" || commands.approval.Metadata["tool_id"] != "tool.deploy" || commands.approval.Metadata["scope"] != "deploy:delete" {
		t.Fatalf("generic approval should use server-side request metadata, got %+v", commands.approval.Metadata)
	}
}

func TestApprovalServiceRejectsLegacyDecisionForHILBackedBusinessApproval(t *testing.T) {
	ctx := context.Background()
	commands := &fakeApprovalCommandPort{receipt: missioncontrol.CommandReceiptDTO{CommandID: "approval-ok", Status: "accepted", AcceptedAt: time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)}}
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.ApprovalRequests = []projection.ApprovalRequestView{{
		RequestID:    "tool-hil-1",
		ApprovalType: "tool_access",
		Status:       "pending",
		MissionID:    "mission-1",
		Metadata: map[string]string{
			"agent_id":        "agent-ops",
			"tool_id":         "tool.deploy",
			"scope":           "deploy:delete",
			"hil_decision_id": "hil-decision-1",
		},
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedApprovalService(repo, commands)

	_, err := service.DecideApproval(ctx, missioncontrol.ViewerRef{ViewerID: "ops-lead", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.ApprovalDecisionCommand{
		IdempotencyKey: "approval-key",
		ApprovalID:     "tool_access:mission-1:tool-hil-1",
		ActionID:       "approve",
	})
	if !errors.Is(err, missioncontrol.ErrConflict) {
		t.Fatalf("DecideApproval error = %v, want ErrConflict", err)
	}
	if commands.approval.ApprovalRef != "" {
		t.Fatalf("legacy business decision should not be submitted: %+v", commands.approval)
	}
}

type fakeApprovalCommandPort struct {
	receipt  missioncontrol.CommandReceiptDTO
	qa       missioncontrol.QADecisionCommand
	read     missioncontrol.InboxItemCommand
	close    missioncontrol.InboxItemCommand
	approval missioncontrol.BusinessApprovalDecisionCommand
}

func (f *fakeApprovalCommandPort) SubmitQADecision(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.QADecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.qa = cmd
	return f.receipt, nil
}

func (f *fakeApprovalCommandPort) MarkInboxRead(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.read = cmd
	return f.receipt, nil
}

func (f *fakeApprovalCommandPort) CloseInboxItem(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.close = cmd
	return f.receipt, nil
}

func (f *fakeApprovalCommandPort) DecideBusinessApproval(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.BusinessApprovalDecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.approval = cmd
	return f.receipt, nil
}

func approvalsByType(items []missioncontrol.ApprovalItemDTO) map[string]*missioncontrol.ApprovalItemDTO {
	byType := make(map[string]*missioncontrol.ApprovalItemDTO, len(items))
	for i := range items {
		byType[items[i].ApprovalType] = &items[i]
	}
	return byType
}

func approvalsByID(items []missioncontrol.ApprovalItemDTO) map[string]*missioncontrol.ApprovalItemDTO {
	byID := make(map[string]*missioncontrol.ApprovalItemDTO, len(items))
	for i := range items {
		byID[items[i].ApprovalID] = &items[i]
	}
	return byID
}

func hasApprovalDecision(actions []missioncontrol.ApprovalActionDTO, decision string) bool {
	for _, action := range actions {
		if action.Decision == decision {
			return true
		}
	}
	return false
}
