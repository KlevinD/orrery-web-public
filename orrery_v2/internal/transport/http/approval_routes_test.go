package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/todo"
)

func TestHTTPApprovalsIncludesTodoExecutionPlansAndRoutesDecision(t *testing.T) {
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{}}
	automation := &fakeTodoAutomationService{items: []todo.TodoAutomationApproval{{
		ApprovalID:     "todo-execution-proposal-1",
		Status:         "pending",
		TodoID:         "todo-1",
		TodoTitle:      "Prepare launch brief",
		TodoVersion:    3,
		ConversationID: "discussion-1",
		Proposal:       "1. Prepare the brief\n2. Review evidence",
		Plan: todo.TodoExecutionPlan{
			Version: 3, Summary: "Prepare and verify the brief", PlanID: "plan-1", TodoID: "todo-1", TodoVersion: 3,
			ProposalRevision: 1, ConversationID: "discussion-1", RepositoryScopes: []todo.TodoExecutionRepositoryScope{},
			BaseCommits: []todo.TodoExecutionBaseCommit{}, AllowedFileScopes: []string{}, PlannedCommands: []string{},
			NetworkPolicy: todo.TodoExecutionNetworkPolicy{Mode: "none", AllowedHosts: []string{}}, RequiredSecretRefs: []string{},
			RiskSummary: []string{"Source material may be incomplete"}, AcceptanceChecks: []string{"Brief is reviewed"},
			Steps: []todo.TodoExecutionPlanStep{{StepID: "step-1", Title: "Prepare brief", AssigneeAgentID: "lead-agent", AcceptanceCriteria: []string{"Brief is complete"}, EvidenceRequirements: []todo.TodoExecutionEvidenceRequirement{{
				EvidenceRequirementID: "evidence-step-1-1", AcceptanceCriterion: "Brief is complete", Kind: "artifact", Required: true,
			}}}},
			RequiredApprovals: []string{"Todo execution plan approval"}, Status: "proposed",
			CreatedAt: time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		},
		PlanDigest:  "digest-1",
		RequestedBy: "todo-automation-agent",
		ApproverID:  "operator",
		CreatedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
	}}}
	router := &Router{approvals: approvals, todoAutomation: automation}

	req := authedRequest(http.MethodGet, "/api/approvals?status=pending", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	router.handleListApprovals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var page missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ApprovalType != "todo_execution_plan" || page.Items[0].TaskID != "todo-1" {
		t.Fatalf("todo approval item = %+v", page.Items)
	}
	if len(page.Items[0].AvailableActions) != 2 || page.Items[0].PayloadPreview.Fields["conversation_id"] != "discussion-1" {
		t.Fatalf("todo approval actions/payload = %+v", page.Items[0])
	}
	if page.Items[0].PayloadPreview.Language != "json" || !strings.Contains(page.Items[0].PayloadPreview.Content, `"plan_id": "plan-1"`) || strings.Contains(page.Items[0].PayloadPreview.Content, "1. Prepare the brief") {
		t.Fatalf("todo approval did not expose the exact structured plan: %+v", page.Items[0].PayloadPreview)
	}

	decision := authedRequest(http.MethodPost, "/api/approvals/todo-execution-proposal-1/decision", `{"action_id":"approve","message":"Proceed","plan_digest":"digest-1"}`)
	decision.Header.Set("X-User-ID", "operator")
	decision.Header.Set("X-Viewer-Kind", "user")
	decision.Header.Set("X-Agent-Profile-ID", "profile-operator")
	decision.Header.Set("X-Tenant-ID", "tenant-a")
	decision.Header.Set("Idempotency-Key", "todo-approve-1")
	rec = httptest.NewRecorder()
	router.handleApprovalDecision(rec, decision, "todo-execution-proposal-1")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("decision status = %d body = %s", rec.Code, rec.Body.String())
	}
	if automation.decision.ApprovalID != "todo-execution-proposal-1" || automation.decision.ActionID != "approve" || automation.decision.Message != "Proceed" || automation.decision.PlanDigest != "digest-1" || automation.viewer.TenantID != "tenant-a" || automation.viewer.AuthorizationSubjectID != "profile-operator" {
		t.Fatalf("todo decision = %+v viewer=%+v", automation.decision, automation.viewer)
	}
	if approvals.decision.ApprovalID != "" {
		t.Fatalf("mission approval service received todo decision: %+v", approvals.decision)
	}
}

func TestTodoAutomationCheckRouteRequiresIdempotencyAndTargetsOneTodo(t *testing.T) {
	automation := &fakeTodoAutomationService{}
	handler := newTestRouter(RouterOptions{Todo: &fakeTodoService{}, TodoAutomation: automation})

	request := authedRequest(http.MethodPost, "/api/todos/todo-1/automation/check", "")
	request.Header.Set("X-User-ID", "operator")
	request.Header.Set("X-Viewer-Kind", "user")
	request.Header.Set("X-Agent-Profile-ID", "profile-operator")
	request.Header.Set("Idempotency-Key", "manual-check-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("check status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if automation.check.TodoID != "todo-1" || automation.check.IdempotencyKey != "manual-check-1" {
		t.Fatalf("check request = %+v", automation.check)
	}
	var result todo.TodoAutomationScanResult
	if err := json.NewDecoder(recorder.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.ScannedCount != 1 || result.PlannedCount != 1 {
		t.Fatalf("check result = %+v", result)
	}
}

func TestTodoExecutionAttemptRouteCarriesExactApprovedPlanIdentity(t *testing.T) {
	automation := &fakeTodoAutomationService{}
	handler := newTestRouter(RouterOptions{Todo: &fakeTodoService{}, TodoAutomation: automation})
	request := authedRequest(http.MethodPost, "/api/todos/todo-1/execution-attempts", `{"plan_id":"plan-1","plan_digest":"digest-1","todo_version":7}`)
	request.Header.Set("Idempotency-Key", "execution-attempt-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("execution attempt status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if automation.start.TodoID != "todo-1" || automation.start.PlanID != "plan-1" || automation.start.PlanDigest != "digest-1" || automation.start.TodoVersion != 7 || automation.start.IdempotencyKey != "execution-attempt-1" {
		t.Fatalf("execution attempt request=%+v", automation.start)
	}
}

func TestHTTPRoutesApprovalsListAndDecision(t *testing.T) {
	approvals := &fakeApprovalService{
		page:    missioncontrol.ApprovalPageDTO{Items: []missioncontrol.ApprovalItemDTO{{ApprovalID: "qa:mission-1:qa-1", ApprovalType: "qa", Status: "pending"}}},
		receipt: missioncontrol.CommandReceiptDTO{CommandID: "qa-1", Status: "accepted"},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals})

	list := authedRequest(http.MethodGet, "/api/approvals?mission_id=mission-1&status=pending&limit=10", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, list)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	if approvals.query.MissionID != "mission-1" || approvals.query.Status != "pending" || approvals.query.Limit != 10 {
		t.Fatalf("approval query = %+v", approvals.query)
	}
	var page missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ApprovalID != "qa:mission-1:qa-1" {
		t.Fatalf("approval page = %+v", page)
	}

	decision := authedRequest(http.MethodPost, "/api/approvals/qa:mission-1:qa-1/decision", `{"action_id":"approve","task_id":"task-1","message":"ok","modified_input":"go test ./...","choice_id":"choice-1","remember":true,"metadata":{"agent_id":"agent-ops","tool_id":"tool.deploy","scope":"deploy:delete"}}`)
	decision.Header.Set("Idempotency-Key", "approval-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, decision)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("decision status = %d body = %s", rec.Code, rec.Body.String())
	}
	if approvals.decision.ApprovalID != "qa:mission-1:qa-1" || approvals.decision.ActionID != "approve" || approvals.decision.TaskID != "task-1" || approvals.decision.IdempotencyKey != "approval-key" {
		t.Fatalf("approval decision = %+v", approvals.decision)
	}
	if approvals.decision.ModifiedInput != "go test ./..." || approvals.decision.ChoiceID != "choice-1" || !approvals.decision.Remember {
		t.Fatalf("approval hil fields = %+v", approvals.decision)
	}
	if approvals.decision.Metadata["agent_id"] != "agent-ops" || approvals.decision.Metadata["tool_id"] != "tool.deploy" || approvals.decision.Metadata["scope"] != "deploy:delete" {
		t.Fatalf("approval decision metadata = %+v", approvals.decision.Metadata)
	}
}

func TestHTTPApprovalsListAllowsReadScopeForFrontendDefaultSession(t *testing.T) {
	approvals := &fakeApprovalService{
		page: missioncontrol.ApprovalPageDTO{Items: []missioncontrol.ApprovalItemDTO{{ApprovalID: "qa:mission-1:qa-1", ApprovalType: "qa", Status: "pending"}}},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals})
	req := authedRequest(http.MethodGet, "/api/approvals", "")
	req.Header.Set("X-User-ID", "local-user")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "approvals.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if approvals.query.MissionID != "" {
		t.Fatalf("approval query should keep empty mission filter for global inbox: %+v", approvals.query)
	}
}

func TestHTTPApprovalsListIncludesDiscussionRuntimeApprovals(t *testing.T) {
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{}}
	discussions := &fakeDiscussionService{
		page: discussionsession.ConversationPageDTO{
			Conversations: []discussionsession.ConversationSummaryDTO{{
				ConversationID: "conv-1",
				Title:          "Direct",
				UpdatedAt:      time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
			Page: discussionsession.PageDTO{Page: 1, PageSize: 1, Total: 1},
		},
		conversation: discussionsession.ConversationDTO{
			ConversationID: "conv-1",
			Title:          "Direct",
			CreatedAt:      time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
			UpdatedAt:      time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			Events: []discussionsession.EventDTO{{
				EventID:        "discussion-runtime-approval-conv-1-run-1-cmd-1",
				ConversationID: "conv-1",
				Kind:           "discussion_runtime_approval_requested",
				Status:         "pending",
				Summary:        "Runtime approval required for exec_command",
				Metadata: map[string]string{
					"approval_request_id":         "cmd-1",
					"approval_type":               "command_execution",
					"approval_tool_name":          "exec_command",
					"approval_source":             "codex",
					"approval_summary":            "Codex needs to run tests before continuing.",
					"approval_details":            "The command reads files and executes the unit test runner.",
					"approval_decision_source":    "ai_reviewer",
					"approval_reviewer_risk":      "low",
					"approval_reviewer_rationale": "Read-only test command in the workspace.",
					"command":                     "npm test -- DiscussionPanel.test.ts",
				},
				AvailableActions: []discussionsession.ApprovalActionDTO{{
					ActionID: "approve",
					Label:    "Approve",
					Method:   http.MethodPost,
					Path:     "/api/approvals/stale/decision",
				}},
				CreatedAt: time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals, Discussions: discussions})
	req := authedRequest(http.MethodGet, "/api/approvals?status=pending", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var page missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %+v list=%v page=%v", page.Items, discussions.listCalled, discussions.getPageCalled)
	}
	item := page.Items[0]
	if item.ApprovalID != "discussion-runtime-approval-conv-1-run-1-cmd-1" || item.SourceType != "discussion" || item.Provider != "codex" || item.ToolName != "exec_command" {
		t.Fatalf("discussion approval item = %+v", item)
	}
	if item.Summary != "Codex needs to run tests before continuing." || item.Details != "The command reads files and executes the unit test runner." {
		t.Fatalf("discussion approval explanation = summary %q details %q", item.Summary, item.Details)
	}
	if item.RiskSummary != "AI reviewer | low risk | Read-only test command in the workspace." {
		t.Fatalf("discussion approval provenance = %q", item.RiskSummary)
	}
	if item.PayloadPreview.Content != "npm test -- DiscussionPanel.test.ts" {
		t.Fatalf("payload preview = %+v", item.PayloadPreview)
	}
	if len(item.AvailableActions) != 4 ||
		item.AvailableActions[0].ActionID != "approve_once" ||
		item.AvailableActions[1].ActionID != "approve_session" ||
		!item.AvailableActions[1].RememberAllowed ||
		item.AvailableActions[2].ActionID != "deny" ||
		item.AvailableActions[3].ActionID != "other" ||
		item.AvailableActions[0].Path != "/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-run-1-cmd-1/decision" {
		t.Fatalf("discussion action path = %+v", item.AvailableActions)
	}
	if !discussions.approvalListCalled || discussions.getPageCalled {
		t.Fatalf("discussion approvals should use one batch query: batch=%v page=%v", discussions.approvalListCalled, discussions.getPageCalled)
	}
}

func TestHTTPApprovalsListDoesNotExposeHILBackedDiscussionRuntimeApproval(t *testing.T) {
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{}}
	discussions := &fakeDiscussionService{
		page: discussionsession.ConversationPageDTO{Conversations: []discussionsession.ConversationSummaryDTO{{ConversationID: "conv-hil"}}},
		conversation: discussionsession.ConversationDTO{ConversationID: "conv-hil", Events: []discussionsession.EventDTO{{
			EventID: "runtime-hil-event", ConversationID: "conv-hil", Kind: "discussion_runtime_approval_requested", Status: "pending",
			Metadata: map[string]string{"approval_request_id": "runtime-hil-request", "hil_decision_id": "hil-decision-1"},
		}}},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals, Discussions: discussions})
	req := authedRequest(http.MethodGet, "/api/approvals?status=pending", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("HIL-backed discussion approval leaked into legacy inbox: %+v", page.Items)
	}
}

func TestDiscussionRuntimeApprovalUsesToolArgumentsForIntentPreview(t *testing.T) {
	conversation := discussionsession.ConversationDTO{
		ConversationID: "conv-mission",
		Runs: []discussionsession.RunDTO{{
			RunID: "run-mission",
			ToolCalls: []discussionsession.ToolCallDTO{{
				ToolCallID: "call_vgVsQnXJV5XcuTA9PnltJJR9",
				Name:       "mission_prepare",
				Arguments: map[string]string{
					"title":           "Enterprise agent platform",
					"final_goal":      "Deliver a deployable and testable release",
					"root_goal":       "Coordinate product, design, and engineering delivery",
					"idempotency_key": "mission_prepare:call_vgVsQnXJV5XcuTA9PnltJJR9",
					"api_token":       "must-not-be-visible",
				},
			}},
		}},
	}
	event := discussionsession.EventDTO{
		EventID: "approval-mission-prepare",
		Kind:    "discussion_runtime_approval_requested",
		Status:  "pending",
		Metadata: map[string]string{
			"approval_request_id": "call_vgVsQnXJV5XcuTA9PnltJJR9",
			"approval_run_id":     "run-mission",
			"approval_type":       "tool_use",
			"approval_tool_name":  "mission_prepare",
		},
	}

	item, ok := discussionRuntimeApprovalItem(conversation, event)
	if !ok {
		t.Fatal("expected discussion runtime approval")
	}
	if item.PayloadPreview.Content != "Enterprise agent platform" {
		t.Fatalf("payload content = %q", item.PayloadPreview.Content)
	}
	for key, want := range map[string]string{
		"title":      "Enterprise agent platform",
		"final_goal": "Deliver a deployable and testable release",
		"root_goal":  "Coordinate product, design, and engineering delivery",
	} {
		if got := item.PayloadPreview.Fields[key]; got != want {
			t.Fatalf("payload field %s = %q, want %q", key, got, want)
		}
	}
	if _, exists := item.PayloadPreview.Fields["idempotency_key"]; exists {
		t.Fatalf("idempotency key must not be exposed: %+v", item.PayloadPreview.Fields)
	}
	if _, exists := item.PayloadPreview.Fields["api_token"]; exists {
		t.Fatalf("API token must not be exposed: %+v", item.PayloadPreview.Fields)
	}
}

func TestDiscussionRuntimeApprovalSeparatesBusinessInitiatorTriggerAndDecider(t *testing.T) {
	conversation := discussionsession.ConversationDTO{
		ConversationID: "conversation-test",
		CreatedBy:      "human-ding",
		CreatedByName:  "丁皮",
		Members: []discussionsession.ConversationMemberDTO{
			{Kind: "human", ID: "human-ding", DisplayName: "丁皮"},
			{Kind: "agent", ID: "lead-agent", DisplayName: "顾明澈"},
		},
		Runs: []discussionsession.RunDTO{{
			RunID:     "run-1",
			AgentID:   "lead-agent",
			AgentName: "顾明澈",
		}},
	}
	event := discussionsession.EventDTO{
		EventID: "approval-discussion-close",
		Kind:    "discussion_runtime_approval_requested",
		Status:  "rejected",
		Metadata: map[string]string{
			"approval_run_id":     "run-1",
			"approval_agent_id":   "lead-agent",
			"approval_tool_name":  "discussion_close",
			"approval_decided_by": "human-ding",
		},
	}

	item, ok := discussionRuntimeApprovalItem(conversation, event)
	if !ok {
		t.Fatal("expected discussion runtime approval")
	}
	if item.RequestedBy != "human-ding" || item.RequestedByActor.ActorType != "human" || item.RequestedByActor.DisplayName != "丁皮" {
		t.Fatalf("business initiator = %q %+v", item.RequestedBy, item.RequestedByActor)
	}
	if item.TriggeredBy != "lead-agent" || item.TriggeredByActor.ActorType != "agent" || item.TriggeredByActor.DisplayName != "顾明澈" {
		t.Fatalf("approval trigger = %q %+v", item.TriggeredBy, item.TriggeredByActor)
	}
	if item.DecidedBy != "human-ding" || item.DecidedByActor.ActorType != "human" || item.DecidedByActor.DisplayName != "丁皮" {
		t.Fatalf("approval decider = %q %+v", item.DecidedBy, item.DecidedByActor)
	}
	if item.TargetActor != item.TriggeredByActor {
		t.Fatalf("runtime target actor = %+v, trigger = %+v", item.TargetActor, item.TriggeredByActor)
	}
}

func TestDiscussionRuntimeApprovalHonorsForcedOneTimeActions(t *testing.T) {
	item, ok := discussionRuntimeApprovalItem(discussionsession.ConversationDTO{ConversationID: "conv-marketing"}, discussionsession.EventDTO{
		EventID: "approval-marketing", ConversationID: "conv-marketing", Kind: "discussion_runtime_approval_requested", Status: "pending",
		Metadata: map[string]string{
			"approval_type":            "tool_use",
			"approval_tool_name":       "marketing_wechat_draft_create",
			"approval_allowed_actions": "approve_once,deny",
			"approval_assigned_to":     "human-ding",
		},
	})
	if !ok || item.AssignedTo != "human-ding" || len(item.AvailableActions) != 2 || item.AvailableActions[0].ActionID != "approve_once" || item.AvailableActions[1].ActionID != "deny" {
		t.Fatalf("forced one-time approval item = %+v, ok=%v", item, ok)
	}
}

func TestDiscussionRuntimeApprovalDoesNotUseOpaqueRequestIDAsContent(t *testing.T) {
	item, ok := discussionRuntimeApprovalItem(discussionsession.ConversationDTO{ConversationID: "conv-1"}, discussionsession.EventDTO{
		EventID: "approval-1",
		Kind:    "discussion_runtime_approval_requested",
		Status:  "pending",
		Metadata: map[string]string{
			"approval_request_id": "call_vgVsQnXJV5XcuTA9PnltJJR9",
			"approval_type":       "tool_use",
			"approval_tool_name":  "mission_prepare",
		},
	})
	if !ok {
		t.Fatal("expected discussion runtime approval")
	}
	if item.PayloadPreview.Content != "" {
		t.Fatalf("opaque request id leaked into content: %q", item.PayloadPreview.Content)
	}
}

func TestHTTPApprovalsListUsesDiscussionRuntimeDecisionAsSourceOfTruth(t *testing.T) {
	stalePending := missioncontrol.ApprovalItemDTO{
		ApprovalID:   "command_execution:mission-1:cmd-1",
		ApprovalType: "command_execution",
		DisplayKind:  "command_execution",
		Status:       "pending",
		Priority:     "high",
		MissionID:    "mission-1",
		TaskID:       "run-1",
		SourceType:   "runtime",
		SourceID:     "mission-1",
		Provider:     "claude",
		ToolName:     "WebFetch",
		Title:        "Runtime approval: WebFetch",
		Summary:      "Runtime approval required for WebFetch",
		PayloadPreview: missioncontrol.ApprovalPayloadDTO{Fields: map[string]string{
			"approval_conversation_id": "conv-1",
			"approval_request_id":      "cmd-1",
			"approval_run_id":          "run-1",
		}},
		AvailableActions: []missioncontrol.ApprovalActionDTO{{
			ActionID: "approve_once",
			Label:    "Approve once",
			Method:   http.MethodPost,
			Path:     "/api/discussions/conv-1/approvals/cmd-1/decision",
		}},
		CreatedAt: time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
	}
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{Items: []missioncontrol.ApprovalItemDTO{stalePending}}}
	discussions := &fakeDiscussionService{
		page: discussionsession.ConversationPageDTO{
			Conversations: []discussionsession.ConversationSummaryDTO{{
				ConversationID: "conv-1",
				Title:          "Direct",
				UpdatedAt:      time.Date(2026, 6, 16, 10, 1, 0, 0, time.UTC),
			}},
			Page: discussionsession.PageDTO{Page: 1, PageSize: 1, Total: 1},
		},
		conversation: discussionsession.ConversationDTO{
			ConversationID: "conv-1",
			Title:          "Direct",
			LinkedMissions: []discussionsession.LinkedMissionDTO{{MissionID: "mission-1", Title: "Mission", Status: "active"}},
			Events: []discussionsession.EventDTO{{
				EventID:        "discussion-runtime-approval-conv-1-run-1-cmd-1",
				ConversationID: "conv-1",
				Kind:           "discussion_runtime_approval_requested",
				Status:         "approved",
				Summary:        "Runtime approval required for WebFetch",
				Metadata: map[string]string{
					"approval_conversation_id": "conv-1",
					"approval_request_id":      "cmd-1",
					"approval_run_id":          "run-1",
					"approval_type":            "tool_use",
					"approval_tool_name":       "WebFetch",
					"approval_source":          "claude",
					"approval_summary":         "Claude wants to fetch the Symphony SPEC.",
					"approval_details":         "Operator approved this request.",
					"url":                      "https://github.com/openai/symphony/blob/main/SPEC.md",
					"decision_at":              "2026-06-16T10:01:00Z",
				},
				CreatedAt: time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals, Discussions: discussions})

	pendingReq := authedRequest(http.MethodGet, "/api/approvals?mission_id=mission-1&status=pending", "")
	pendingReq.Header.Set("X-User-ID", "ding")
	pendingReq.Header.Set("X-Viewer-Kind", "user")
	pendingReq.Header.Set("X-Agent-Profile-ID", "human-ding")
	pendingRec := httptest.NewRecorder()
	handler.ServeHTTP(pendingRec, pendingReq)
	if pendingRec.Code != http.StatusOK {
		t.Fatalf("pending status = %d body = %s", pendingRec.Code, pendingRec.Body.String())
	}
	var pendingPage missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(pendingRec.Body).Decode(&pendingPage); err != nil {
		t.Fatalf("decode pending approvals: %v", err)
	}
	if len(pendingPage.Items) != 0 {
		t.Fatalf("pending approvals should be cleared by discussion decision: %+v", pendingPage.Items)
	}
	if discussions.viewer.ViewerID != "human-ding" || discussions.viewer.AgentProfileID != "human-ding" {
		t.Fatalf("discussion approval viewer = %+v, want canonical human profile", discussions.viewer)
	}

	approvedReq := authedRequest(http.MethodGet, "/api/approvals?mission_id=mission-1&status=approved", "")
	approvedRec := httptest.NewRecorder()
	handler.ServeHTTP(approvedRec, approvedReq)
	if approvedRec.Code != http.StatusOK {
		t.Fatalf("approved status = %d body = %s", approvedRec.Code, approvedRec.Body.String())
	}
	var approvedPage missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(approvedRec.Body).Decode(&approvedPage); err != nil {
		t.Fatalf("decode approved approvals: %v", err)
	}
	if len(approvedPage.Items) != 1 {
		t.Fatalf("approved approvals = %+v", approvedPage.Items)
	}
	item := approvedPage.Items[0]
	if item.ApprovalID != "discussion-runtime-approval-conv-1-run-1-cmd-1" || item.Status != "approved" || len(item.AvailableActions) != 0 {
		t.Fatalf("approved discussion approval = %+v", item)
	}
	if approvals.query.MissionID != "mission-1" {
		t.Fatalf("approval query = %+v", approvals.query)
	}
}

func TestHTTPApprovalsListDoesNotExposeCancelledDiscussionRuntimeApprovalAsPending(t *testing.T) {
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{}}
	discussions := &fakeDiscussionService{
		page: discussionsession.ConversationPageDTO{
			Conversations: []discussionsession.ConversationSummaryDTO{{
				ConversationID: "conv-cancelled",
				Title:          "Direct",
				Status:         "cancelled",
				UpdatedAt:      time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
			Page: discussionsession.PageDTO{Page: 1, PageSize: 1, Total: 1},
		},
		conversation: discussionsession.ConversationDTO{
			ConversationID: "conv-cancelled",
			Title:          "Direct",
			Status:         "cancelled",
			Runs: []discussionsession.RunDTO{{
				RunID:          "run-1",
				ConversationID: "conv-cancelled",
				AgentID:        "agent-1",
				Status:         "cancelled",
				CreatedAt:      time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
				CompletedAt:    time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
			Events: []discussionsession.EventDTO{{
				EventID:        "discussion-runtime-approval-conv-cancelled-run-1-cmd-1",
				ConversationID: "conv-cancelled",
				Kind:           "discussion_runtime_approval_requested",
				Status:         "pending",
				Summary:        "Runtime approval required for exec_command",
				Metadata: map[string]string{
					"approval_request_id": "cmd-1",
					"approval_run_id":     "run-1",
					"approval_type":       "command_execution",
					"approval_tool_name":  "exec_command",
					"approval_source":     "claude",
					"command":             "curl https://example.test/spec.md",
				},
				AvailableActions: discussionsession.RuntimeApprovalActions("/api/discussions/conv-cancelled/approvals/discussion-runtime-approval-conv-cancelled-run-1-cmd-1/decision", "command_execution", "pending"),
				CreatedAt:        time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC),
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals, Discussions: discussions})

	pendingReq := authedRequest(http.MethodGet, "/api/approvals?status=pending", "")
	pendingRec := httptest.NewRecorder()
	handler.ServeHTTP(pendingRec, pendingReq)
	if pendingRec.Code != http.StatusOK {
		t.Fatalf("pending status = %d body = %s", pendingRec.Code, pendingRec.Body.String())
	}
	var pendingPage missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(pendingRec.Body).Decode(&pendingPage); err != nil {
		t.Fatalf("decode pending approvals: %v", err)
	}
	if len(pendingPage.Items) != 0 {
		t.Fatalf("cancelled runtime approval should not be pending: %+v", pendingPage.Items)
	}

	unavailableReq := authedRequest(http.MethodGet, "/api/approvals?status=unavailable", "")
	unavailableRec := httptest.NewRecorder()
	handler.ServeHTTP(unavailableRec, unavailableReq)
	if unavailableRec.Code != http.StatusOK {
		t.Fatalf("unavailable status = %d body = %s", unavailableRec.Code, unavailableRec.Body.String())
	}
	var unavailablePage missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(unavailableRec.Body).Decode(&unavailablePage); err != nil {
		t.Fatalf("decode unavailable approvals: %v", err)
	}
	if len(unavailablePage.Items) != 1 {
		t.Fatalf("unavailable approvals = %+v", unavailablePage.Items)
	}
	item := unavailablePage.Items[0]
	if item.Status != "unavailable" || len(item.AvailableActions) != 0 {
		t.Fatalf("cancelled approval should be closed without actions: %+v", item)
	}
}

func TestHTTPApprovalsListKeepsBlockedDiscussionRuntimeApprovalPending(t *testing.T) {
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{}}
	discussions := &fakeDiscussionService{
		page: discussionsession.ConversationPageDTO{
			Conversations: []discussionsession.ConversationSummaryDTO{{
				ConversationID: "conv-blocked",
				Title:          "Direct",
				Status:         "blocked",
				UpdatedAt:      time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
			Page: discussionsession.PageDTO{Page: 1, PageSize: 1, Total: 1},
		},
		conversation: discussionsession.ConversationDTO{
			ConversationID: "conv-blocked",
			Title:          "Direct",
			Status:         "blocked",
			Runs: []discussionsession.RunDTO{{
				RunID:          "run-1",
				ConversationID: "conv-blocked",
				AgentID:        "agent-1",
				Status:         "blocked",
				RuntimeKind:    "claude",
				CreatedAt:      time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
			}},
			Events: []discussionsession.EventDTO{{
				EventID:        "discussion-runtime-approval-conv-blocked-run-1-cmd-1",
				ConversationID: "conv-blocked",
				Kind:           "discussion_runtime_approval_requested",
				Status:         "pending",
				Summary:        "Runtime approval required for WebSearch",
				Metadata: map[string]string{
					"approval_request_id": "cmd-1",
					"approval_run_id":     "run-1",
					"approval_type":       "tool_use",
					"approval_tool_name":  "WebSearch",
					"approval_source":     "claude",
				},
				AvailableActions: discussionsession.RuntimeApprovalActions("/api/discussions/conv-blocked/approvals/discussion-runtime-approval-conv-blocked-run-1-cmd-1/decision", "tool_use", "pending"),
				CreatedAt:        time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC),
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals, Discussions: discussions})

	pendingReq := authedRequest(http.MethodGet, "/api/approvals?status=pending", "")
	pendingRec := httptest.NewRecorder()
	handler.ServeHTTP(pendingRec, pendingReq)
	if pendingRec.Code != http.StatusOK {
		t.Fatalf("pending status = %d body = %s", pendingRec.Code, pendingRec.Body.String())
	}
	var pendingPage missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(pendingRec.Body).Decode(&pendingPage); err != nil {
		t.Fatalf("decode pending approvals: %v", err)
	}
	if len(pendingPage.Items) != 1 {
		t.Fatalf("blocked runtime approval should stay pending: %+v", pendingPage.Items)
	}
	item := pendingPage.Items[0]
	if item.Status != "pending" || len(item.AvailableActions) == 0 {
		t.Fatalf("blocked approval should be actionable pending: %+v", item)
	}
}

func TestHTTPApprovalsListKeepsBlockedRuntimeApprovalPendingWhenConversationCompleted(t *testing.T) {
	approvals := &fakeApprovalService{page: missioncontrol.ApprovalPageDTO{}}
	discussions := &fakeDiscussionService{
		page: discussionsession.ConversationPageDTO{
			Conversations: []discussionsession.ConversationSummaryDTO{{
				ConversationID: "conv-timeout",
				Title:          "Direct",
				Status:         "completed",
				UpdatedAt:      time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
			}},
			Page: discussionsession.PageDTO{Page: 1, PageSize: 1, Total: 1},
		},
		conversation: discussionsession.ConversationDTO{
			ConversationID: "conv-timeout",
			Title:          "Direct",
			Status:         "completed",
			Runs: []discussionsession.RunDTO{{
				RunID:          "run-timeout",
				ConversationID: "conv-timeout",
				AgentID:        "agent-1",
				Status:         "blocked",
				RuntimeKind:    "claude",
				SessionID:      "session-timeout",
				CreatedAt:      time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
				CompletedAt:    time.Date(2026, 6, 16, 9, 10, 0, 0, time.UTC),
			}},
			Events: []discussionsession.EventDTO{{
				EventID:        "discussion-runtime-approval-conv-timeout-run-timeout-cmd-1",
				ConversationID: "conv-timeout",
				Kind:           "discussion_runtime_approval_requested",
				Status:         "pending",
				Summary:        "Runtime approval required for WebSearch",
				Metadata: map[string]string{
					"approval_request_id": "cmd-1",
					"approval_run_id":     "run-timeout",
					"approval_type":       "tool_use",
					"approval_tool_name":  "WebSearch",
					"approval_source":     "claude",
					"approval_session_id": "session-timeout",
				},
				AvailableActions: discussionsession.RuntimeApprovalActions("/api/discussions/conv-timeout/approvals/discussion-runtime-approval-conv-timeout-run-timeout-cmd-1/decision", "tool_use", "pending"),
				CreatedAt:        time.Date(2026, 6, 16, 9, 5, 0, 0, time.UTC),
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Approvals: approvals, Discussions: discussions})

	pendingReq := authedRequest(http.MethodGet, "/api/approvals?status=pending", "")
	pendingRec := httptest.NewRecorder()
	handler.ServeHTTP(pendingRec, pendingReq)
	if pendingRec.Code != http.StatusOK {
		t.Fatalf("pending status = %d body = %s", pendingRec.Code, pendingRec.Body.String())
	}
	var pendingPage missioncontrol.ApprovalPageDTO
	if err := json.NewDecoder(pendingRec.Body).Decode(&pendingPage); err != nil {
		t.Fatalf("decode pending approvals: %v", err)
	}
	if len(pendingPage.Items) != 1 {
		t.Fatalf("blocked runtime approval should stay pending: %+v", pendingPage.Items)
	}
	item := pendingPage.Items[0]
	if item.Status != "pending" || len(item.AvailableActions) == 0 {
		t.Fatalf("blocked approval should be actionable pending: %+v", item)
	}
}

type fakeApprovalService struct {
	page     missioncontrol.ApprovalPageDTO
	receipt  missioncontrol.CommandReceiptDTO
	query    missioncontrol.ApprovalListQuery
	decision missioncontrol.ApprovalDecisionCommand
}

type fakeTodoAutomationService struct {
	items    []todo.TodoAutomationApproval
	viewer   todo.ViewerRef
	query    todo.ListTodoAutomationApprovalsQuery
	decision todo.DecideTodoAutomationApprovalRequest
	check    todo.CheckTodoAutomationRequest
	start    todo.StartTodoAutomationExecutionRequest
}

func (f *fakeTodoAutomationService) CheckTodoAutomation(_ context.Context, viewer todo.ViewerRef, req todo.CheckTodoAutomationRequest) (todo.TodoAutomationScanResult, error) {
	f.viewer = viewer
	f.check = req
	return todo.TodoAutomationScanResult{ScannedCount: 1, EligibleCount: 1, ClaimedCount: 1, PlannedCount: 1}, nil
}

func (f *fakeTodoAutomationService) ListTodoAutomationApprovals(_ context.Context, viewer todo.ViewerRef, query todo.ListTodoAutomationApprovalsQuery) ([]todo.TodoAutomationApproval, error) {
	f.viewer = viewer
	f.query = query
	return f.items, nil
}

func (f *fakeTodoAutomationService) DecideTodoAutomationApproval(_ context.Context, viewer todo.ViewerRef, req todo.DecideTodoAutomationApprovalRequest) (todo.TodoAutomationState, error) {
	f.viewer = viewer
	f.decision = req
	return todo.TodoAutomationState{ApprovalID: req.ApprovalID, TodoID: "todo-1", TodoVersion: 3}, nil
}

func (f *fakeTodoAutomationService) StartTodoAutomationExecution(_ context.Context, viewer todo.ViewerRef, req todo.StartTodoAutomationExecutionRequest) (todo.TodoAutomationState, error) {
	f.viewer = viewer
	f.start = req
	return todo.TodoAutomationState{TodoID: req.TodoID, PlanDigest: req.PlanDigest, Status: todo.TodoAutomationStatusExecuting}, nil
}

func (f *fakeApprovalService) ListApprovals(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ApprovalListQuery) (missioncontrol.ApprovalPageDTO, error) {
	f.query = query
	return f.page, nil
}

func (f *fakeApprovalService) DecideApproval(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ApprovalDecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.decision = cmd
	return f.receipt, nil
}
