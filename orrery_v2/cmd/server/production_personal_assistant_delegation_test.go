package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

type assistantDelegationCompletionFunc func(context.Context, runtimeapproval.CompletionRequest) (string, error)

func (f assistantDelegationCompletionFunc) Complete(ctx context.Context, r runtimeapproval.CompletionRequest) (string, error) {
	return f(ctx, r)
}

func delegatedApprovalFixture(t *testing.T) (*productionPersonalAssistantDelegationEngine, hil.DecisionRequest, productionPersonalAssistantApprovalCandidate, productionPersonalAssistantWeixinTarget, *recordingGatewayViewerAuthorization) {
	t.Helper()
	env := testkit.New(t)
	t.Cleanup(env.Close)
	coordinator := migration.NewCoordinator(env.Databases)
	if err := hil.RegisterMigrations(coordinator); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attention, _, _, _, auth := attentionFixture()
	assistantID := agentcore.AgentProfileID(discussionsession.PersonalAssistantAgentID("tenant-a", "owner-a"))
	attention.profiles.(*recordingProductionHILProfileDirectory).profiles[assistantID] = agentcore.AgentProfile{
		ID: assistantID, Enabled: true, AgentKind: agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
	}
	auth.resolved = []string{"approvals.read", "approvals.decide", "runtime.action.execute"}
	d := hil.DecisionRequest{ID: "decision-1", TenantID: "tenant-a", Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "worker-1"}, Action: "information.read", ActionTypeID: "information.read", Resource: hil.ObjectRef{Kind: "information_resource", ID: "knowledge-1"}, ArgsHash: "sha256:request", Purpose: "Read internal release checklist", RiskLevel: hil.RiskLow, Route: hil.RouteDecision{Mode: hil.ModeHIL}, Context: hil.OntologyContextSnapshot{Complete: true, EvidenceRefs: []string{"evidence-1"}}, Status: hil.DecisionPending, IdempotencyKey: "create-1", CreatedAt: now, ExpiresAt: now.Add(time.Hour), PayloadPreview: map[string]any{
		"approval_assigned_to": "human-a", "approval_tool_name": "knowledge_search", "approval_kind": "tool", "approval_delegation_policy_version": "1",
		runtimeapproval.MetadataIntentRecognized: "true", runtimeapproval.MetadataActionTypeID: "information.read", runtimeapproval.MetadataIntentRisk: "low", runtimeapproval.MetadataIntentEffects: "read",
		"content": "The release checklist is in knowledge-1. Only read it; do not publish or modify records.",
	}}
	authority := &productionPersonalAssistantDelegationAuthority{accounts: auth, scopes: auth, profiles: attention.profiles, targets: attention.targets, now: func() time.Time { return now }, grants: []personalAssistantDelegationGrant{{ID: "grant-1", TenantID: d.TenantID, OwnerID: "owner-a", HumanProfileID: "human-a", ConsentReference: "consent-1", ExpiresAt: now.Add(time.Hour), Rules: []personalAssistantDelegationRule{{ActionTypeID: d.ActionTypeID, Scope: d.Resource}}}}}
	repo := hil.NewPlatformRepository(env.DB)
	if err := repo.CreateDecision(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	service := hil.NewService(repo, nil, env.IDGenerator, hil.WithClock(func() time.Time { return now }), hil.WithDelegationAuthority(authority))
	settings := httptransport.DefaultSettings()
	settings.RuntimeApprovals.Reviewer.LLMProfileID = "reviewer-1"
	e := &productionPersonalAssistantDelegationEngine{core: env.DB, authority: authority, decisions: service, settings: &runtimeApprovalSettingsStub{settings: settings}, notices: &personalAssistantNoticesStub{}, replies: &agentNoticeWriterStub{}, now: func() time.Time { return now }}
	if err := e.ensureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	candidate := productionPersonalAssistantApprovalCandidate{DecisionID: d.ID, ApprovalTenant: d.TenantID, HumanProfileID: "human-a", Action: d.Action, ActionTypeID: d.ActionTypeID, Purpose: d.Purpose, Payload: d.PayloadPreview, RiskLevel: d.RiskLevel, RouteMode: d.Route.Mode, ExpiresAt: d.ExpiresAt, CreatedAt: d.CreatedAt, Requester: d.Requester}
	return e, d, candidate, productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "weixin-a"}, auth
}

func TestPersonalAssistantDelegationApprovesAndRejectsWithDurableAttribution(t *testing.T) {
	for _, choice := range []string{"approve", "reject"} {
		t.Run(choice, func(t *testing.T) {
			e, d, candidate, target, _ := delegatedApprovalFixture(t)
			calls := 0
			e.completion = assistantDelegationCompletionFunc(func(ctx context.Context, request runtimeapproval.CompletionRequest) (string, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 9*time.Second {
					t.Fatal("unbounded reviewer")
				}
				if request.LLMProfileID != "reviewer-1" || request.MaxTokens != 700 || !strings.Contains(request.UserPrompt, "evidence-1") || !strings.Contains(request.UserPrompt, d.ArgsHash) {
					t.Fatal("review omitted full evidence/action binding")
				}
				return `{"decision":"` + choice + `","risk":"low","material_complete":true,"reason":"材料完整且在授权的只读范围内","missing_information":""}`, nil
			})
			handled, err := e.Handle(context.Background(), candidate, target)
			if err != nil || !handled {
				t.Fatalf("delegation: %v %v", handled, err)
			}
			if _, err = e.Handle(context.Background(), candidate, target); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("reviewed %d times", calls)
			}
			receipts, err := hil.NewPlatformRepository(e.core).ListDecisionReceipts(context.Background(), d.TenantID, d.ID)
			if err != nil || len(receipts) != 1 || receipts[0].ResolverActorKind != hil.ActorAgent || receipts[0].ResolverActorID != discussionsession.PersonalAssistantAgentID(target.TenantID, target.OwnerID) {
				t.Fatalf("invalid receipts %+v %v", receipts, err)
			}
			if receipts[0].ResolverSummary["delegation"] == nil {
				t.Fatal("grant attribution not durable")
			}
			if err := e.FlushNotices(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := e.FlushNotices(context.Background()); err != nil {
				t.Fatal(err)
			}
			notices := e.notices.(*personalAssistantNoticesStub)
			if len(notices.messages) != 1 || len(notices.viewers[0].IdentityIDs) != 3 {
				t.Fatalf("notice not idempotent/canonical: %+v", notices)
			}
			material, err := e.contextNotices(context.Background(), target.TenantID, target.OwnerID, candidate.HumanProfileID)
			if err != nil || len(material) != 1 || material[0].CurrentStatus == "pending" {
				t.Fatalf("stale context: %+v %v", material, err)
			}
		})
	}
}

func TestPersonalAssistantDelegationFailsClosedWithoutRepeatingReviews(t *testing.T) {
	for _, scenario := range []string{"ask", "missing_evidence", "high_risk", "invalid_json", "unknown_fields", "model_error", "missing_model", "oversized_material", "human_won", "reassigned", "material_changed", "permissions_revoked", "grant_expired"} {
		t.Run(scenario, func(t *testing.T) {
			e, d, candidate, target, auth := delegatedApprovalFixture(t)
			calls := 0
			if scenario == "missing_model" {
				e.settings.(*runtimeApprovalSettingsStub).settings.RuntimeApprovals.Reviewer.LLMProfileID = ""
			}
			if scenario == "oversized_material" {
				d.PayloadPreview["content"] = strings.Repeat("x", 25000)
				encoded, _ := json.Marshal(d.PayloadPreview)
				if _, err := e.core.Exec(context.Background(), `UPDATE hil_decision_requests SET payload_preview_json=? WHERE id=?`, string(encoded), d.ID); err != nil {
					t.Fatal(err)
				}
			}
			e.completion = assistantDelegationCompletionFunc(func(ctx context.Context, _ runtimeapproval.CompletionRequest) (string, error) {
				calls++
				review := personalAssistantDelegationReview{Decision: "approve", Risk: "low", MaterialComplete: true, Reason: "Reviewed", MissingInformation: ""}
				switch scenario {
				case "ask":
					review.Decision = "ask"
				case "missing_evidence":
					review.MissingInformation = "Need the source evidence"
				case "high_risk":
					review.Risk = "high"
				case "invalid_json":
					return `not json`, nil
				case "unknown_fields":
					return `{"decision":"approve","grant_all":true}`, nil
				case "model_error":
					return "", context.DeadlineExceeded
				case "human_won":
					if _, err := e.decisions.ResolveDecision(ctx, d.TenantID, d.ID, hil.ActorRef{Kind: hil.ActorHuman, ID: candidate.HumanProfileID}, hil.ResolveDecisionInput{Decision: "reject", Reason: "Human decision", IdempotencyKey: "human-decision"}); err != nil {
						t.Fatal(err)
					}
				case "reassigned":
					d.PayloadPreview["approval_assigned_to"] = "human-b"
					encoded, _ := json.Marshal(d.PayloadPreview)
					if _, err := e.core.Exec(ctx, `UPDATE hil_decision_requests SET payload_preview_json=? WHERE id=?`, string(encoded), d.ID); err != nil {
						t.Fatal(err)
					}
				case "material_changed":
					if _, err := e.core.Exec(ctx, `UPDATE hil_decision_requests SET purpose='changed' WHERE id=?`, d.ID); err != nil {
						t.Fatal(err)
					}
				case "permissions_revoked":
					auth.resolved = []string{"approvals.read"}
				case "grant_expired":
					e.authority.grants[0].ExpiresAt = e.now()
				}
				encoded, _ := json.Marshal(review)
				return string(encoded), nil
			})
			if handled, err := e.Handle(context.Background(), candidate, target); err != nil || handled {
				t.Fatalf("unsafe delegation: %v %v", handled, err)
			}
			if scenario != "material_changed" && scenario != "reassigned" {
				restarted := *e
				if handled, err := restarted.Handle(context.Background(), candidate, target); err != nil || handled {
					t.Fatalf("retry: %v %v", handled, err)
				}
			}
			wantCalls := 1
			if scenario == "missing_model" || scenario == "oversized_material" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			receipts, err := hil.NewPlatformRepository(e.core).ListDecisionReceipts(context.Background(), d.TenantID, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, receipt := range receipts {
				if receipt.ResolverActorKind == hil.ActorAgent {
					t.Fatal("agent committed despite guard")
				}
			}
		})
	}
}

func TestPersonalAssistantDelegationAuthorityRequiresCurrentNarrowConsent(t *testing.T) {
	for _, scenario := range []string{"valid", "disabled", "no_grant", "expired", "other_tenant", "other_owner", "other_human", "wrong_agent", "self_review", "wrong_resource", "incidental_reference", "external_action", "unknown_tool", "missing_provenance", "forced_human", "manual_hil", "profile_disabled", "assistant_disabled", "assistant_other_tenant", "account_disabled", "read_revoked", "decide_revoked", "execute_revoked", "channel_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			e, d, _, target, auth := delegatedApprovalFixture(t)
			human := hil.ActorRef{Kind: hil.ActorHuman, ID: "human-a"}
			agent := hil.ActorRef{Kind: hil.ActorAgent, ID: discussionsession.PersonalAssistantAgentID(target.TenantID, target.OwnerID)}
			switch scenario {
			case "disabled":
				e.authority = nil
			case "no_grant":
				e.authority.grants = nil
			case "expired":
				e.authority.grants[0].ExpiresAt = e.now()
			case "other_tenant":
				d.TenantID = "tenant-b"
			case "other_owner":
				auth.account.UserID = "owner-b"
			case "other_human":
				human.ID = "human-b"
			case "wrong_agent":
				agent.ID = "random-agent"
			case "self_review":
				d.Requester = agent
			case "wrong_resource":
				d.Resource.ID = "knowledge-2"
			case "incidental_reference":
				d.ObjectRefs = []hil.ObjectRef{d.Resource}
				d.Resource.ID = "knowledge-2"
			case "external_action":
				d.ActionTypeID = "communication.email.send"
				d.Action = d.ActionTypeID
			case "unknown_tool":
				d.PayloadPreview["approval_tool_name"] = "exec_command"
			case "missing_provenance":
				delete(d.PayloadPreview, "approval_delegation_policy_version")
			case "forced_human":
				d.PayloadPreview["approval_force_manual"] = "true"
			case "manual_hil":
				d.PayloadPreview["approval_effective_hil_mode"] = "manual"
			case "profile_disabled":
				directory := e.authority.profiles.(*recordingProductionHILProfileDirectory)
				p := directory.profiles["human-a"]
				p.Enabled = false
				directory.profiles["human-a"] = p
			case "account_disabled":
				auth.account.Enabled = false
			case "assistant_disabled", "assistant_other_tenant":
				directory := e.authority.profiles.(*recordingProductionHILProfileDirectory)
				id := agentcore.AgentProfileID(agent.ID)
				p := directory.profiles[id]
				if scenario == "assistant_disabled" {
					p.Enabled = false
				} else {
					p.OrgIdentity.OrganizationRef.ID = "tenant-b"
				}
				directory.profiles[id] = p
			case "read_revoked":
				auth.resolved = []string{"approvals.decide", "runtime.action.execute"}
			case "decide_revoked":
				auth.resolved = []string{"approvals.read", "runtime.action.execute"}
			case "execute_revoked":
				auth.resolved = []string{"approvals.read", "approvals.decide"}
			case "channel_disabled":
				e.authority.targets.(*recordingPersonalAssistantApprovalNotificationSource).targetsByTenant = nil
			}
			grant, err := e.authority.AuthorizeDelegation(context.Background(), d, human, agent)
			if scenario == "valid" {
				if err != nil || grant.PolicyDigest == "" {
					t.Fatalf("valid grant: %+v %v", grant, err)
				}
			} else if err == nil {
				t.Fatal("unauthorized grant accepted")
			}
		})
	}
}

func TestPersonalAssistantDelegationConfigurationIsExplicitAndBounded(t *testing.T) {
	e, _, _, _, _ := delegatedApprovalFixture(t)
	raw, _ := json.Marshal(e.authority.grants)
	if grants, err := parsePersonalAssistantDelegationGrants(string(raw)); err != nil || len(grants) != 1 {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "[]", "null"} {
		if grants, err := parsePersonalAssistantDelegationGrants(raw); err != nil || len(grants) != 0 {
			t.Fatal("empty configuration enabled delegation")
		}
	}
	for _, scenario := range []string{"no_consent", "no_expiry", "wildcard", "external", "unbounded_write", "unknown_field", "trailing_json"} {
		t.Run(scenario, func(t *testing.T) {
			var grants []personalAssistantDelegationGrant
			if err := json.Unmarshal(raw, &grants); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "no_consent":
				grants[0].ConsentReference = ""
			case "no_expiry":
				grants[0].ExpiresAt = time.Time{}
			case "wildcard":
				grants[0].Rules[0].Scope.ID = "*"
			case "external":
				grants[0].Rules[0].ActionTypeID = "communication.email.send"
			case "unbounded_write":
				grants[0].Rules[0].ActionTypeID = "development.create_commit"
			}
			encoded, _ := json.Marshal(grants)
			input := string(encoded)
			if scenario == "unknown_field" {
				input = strings.Replace(input, `"id":`, `"allow_everything":true,"id":`, 1)
			}
			if scenario == "trailing_json" {
				input += "[]"
			}
			if _, err := parsePersonalAssistantDelegationGrants(input); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestPersonalAssistantDelegationRecoversCommitWithoutRepeatingModel(t *testing.T) {
	e, d, candidate, target, _ := delegatedApprovalFixture(t)
	e.completion = assistantDelegationCompletionFunc(func(context.Context, runtimeapproval.CompletionRequest) (string, error) {
		return `{"decision":"approve","risk":"low","material_complete":true,"reason":"Checked","missing_information":""}`, nil
	})
	if handled, err := e.Handle(context.Background(), candidate, target); err != nil || !handled {
		t.Fatal(err)
	}
	if _, err := e.core.Exec(context.Background(), `UPDATE personal_assistant_approval_reviews SET status='reviewing',summary='',created_at=?`, e.now().Add(-2*time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	e.completion = assistantDelegationCompletionFunc(func(context.Context, runtimeapproval.CompletionRequest) (string, error) {
		t.Fatal("model retried after commit")
		return "", nil
	})
	if err := e.FlushNotices(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status, summary string
	if err := e.core.QueryRow(context.Background(), `SELECT status,summary FROM personal_assistant_approval_reviews WHERE decision_id=?`, d.ID).Scan(&status, &summary); err != nil || status != "approve" || !strings.Contains(summary, "从审批回执恢复") {
		t.Fatalf("recovery %s %s %v", status, summary, err)
	}
	if len(e.notices.(*personalAssistantNoticesStub).messages) != 1 {
		t.Fatal("recovered notice missing")
	}
}

func TestPersonalAssistantDelegationFallsBackToOriginalNotification(t *testing.T) {
	e, _, candidate, target, auth := delegatedApprovalFixture(t)
	calls := 0
	e.completion = assistantDelegationCompletionFunc(func(context.Context, runtimeapproval.CompletionRequest) (string, error) {
		calls++
		return `{"decision":"ask","risk":"unknown","material_complete":false,"reason":"无法确认资料","missing_information":"缺少来源正文"}`, nil
	})
	source := &recordingPersonalAssistantApprovalNotificationSource{pending: true, candidates: []productionPersonalAssistantApprovalCandidate{candidate}, targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{target.TenantID: target}}
	outgoing := &attentionDeliveriesStub{}
	scan := productionPersonalAssistantApprovalNotificationSweeper{delegation: e, store: source, profiles: e.authority.profiles, deliveries: outgoing, replies: &agentNoticeWriterStub{}, publicOrigin: "https://orrery.example.test", recovery: &productionPersonalAssistantNotificationRecovery{accounts: auth, scopes: auth, notices: e.notices, deliveries: &personalAssistantRecoveryDeliveriesStub{}}}
	for i := 0; i < 2; i++ {
		if err := scan.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || len(outgoing.commands) != 1 || !strings.Contains(outgoing.commands[0].Body, "缺少来源正文") {
		t.Fatalf("fallback duplicated or missing explanation: %d %+v", calls, outgoing.commands)
	}
	var count int
	if err := e.core.QueryRow(context.Background(), `SELECT COUNT(*) FROM hil_decision_requests`).Scan(&count); err != nil || count != 1 {
		t.Fatal("created extra HIL")
	}
}

func TestPersonalAssistantDelegationConcurrentWorkersClaimOneReview(t *testing.T) {
	e, d, candidate, target, _ := delegatedApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	e.completion = assistantDelegationCompletionFunc(func(ctx context.Context, _ runtimeapproval.CompletionRequest) (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return `{"decision":"approve","risk":"low","material_complete":true,"reason":"Checked","missing_information":""}`, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	type result struct {
		handled bool
		err     error
	}
	done := make(chan result, 1)
	go func() { handled, err := e.Handle(ctx, candidate, target); done <- result{handled, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first review did not start")
	}
	second := *e
	if handled, err := second.Handle(ctx, candidate, target); err != nil || handled {
		t.Fatalf("duplicate worker: %v %v", handled, err)
	}
	if summary, reviewing, err := second.humanReviewSummary(ctx, candidate); err != nil || !reviewing || summary != "" {
		t.Fatalf("active review generated premature escalation: %q %v %v", summary, reviewing, err)
	}
	close(release)
	select {
	case value := <-done:
		if value.err != nil || !value.handled {
			t.Fatalf("first worker: %+v", value)
		}
	case <-ctx.Done():
		t.Fatal("first review did not finish")
	}
	receipts, err := hil.NewPlatformRepository(e.core).ListDecisionReceipts(ctx, d.TenantID, d.ID)
	if err != nil || len(receipts) != 1 || calls.Load() != 1 {
		t.Fatalf("duplicate review or receipt: calls=%d receipts=%d err=%v", calls.Load(), len(receipts), err)
	}
}
