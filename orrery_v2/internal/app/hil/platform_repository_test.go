package hil

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegisterMigrationsCreatesHILDecisionTables(t *testing.T) {
	ctx := context.Background()
	store := openHILTestStore(t, "hil-migrations")
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{hilDecisionMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	for _, table := range []string{
		"hil_decision_requests",
		"hil_council_sessions",
		"hil_council_participants",
		"hil_council_contributions",
		"hil_council_votes",
		"hil_decision_receipts",
	} {
		exists, err := store.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("TableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("missing table %s", table)
		}
	}
}

func TestRegisterMigrationsCreatesHILWorkflowSchema(t *testing.T) {
	ctx := context.Background()
	store := openHILTestStore(t, "hil-workflow-migrations")
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{hilDecisionMigration(), hilWorkflowMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	for _, table := range []string{
		"hil_workflow_templates",
		"hil_workflow_template_versions",
		"hil_workflow_template_bindings",
		"hil_workflow_cases",
		"hil_workflow_instances",
		"hil_workflow_stage_instances",
		"hil_workflow_tasks",
		"hil_workflow_audit_events",
		"hil_workflow_notification_outbox",
	} {
		exists, err := store.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("TableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("missing table %s", table)
		}
	}
	for _, index := range []string{
		"idx_hil_workflow_instances_tenant_status",
		"idx_hil_workflow_tasks_actionable_assignee",
		"idx_hil_workflow_stages_active_deadline",
		"idx_hil_workflow_bindings_lookup",
		"idx_hil_workflow_notifications_pending_retry",
	} {
		assertHILIndexExists(t, store, index)
	}
	for _, column := range []string{
		"workflow_instance_id",
		"material_hash",
		"resolver_actor_kind",
		"resolver_actor_id",
		"resolver_reason",
		"resolver_summary_json",
	} {
		assertHILColumnExists(t, store, "hil_decision_receipts", column)
	}
}

func TestRegisterMigrationsDefinesHILWorkflowSchemaForAllDialects(t *testing.T) {
	migration := hilWorkflowMigration()
	if migration.Version != "0199_hil_approval_workflows" {
		t.Fatalf("workflow migration version = %q, want a version after existing core migrations", migration.Version)
	}
	for name, statements := range map[string][]string{
		"default":  migration.Statements,
		"postgres": migration.StatementsByDialect[db.DialectPostgres],
	} {
		t.Run(name, func(t *testing.T) {
			joined := strings.Join(statements, "\n")
			for _, expected := range []string{
				"hil_workflow_templates",
				"hil_workflow_template_versions",
				"hil_workflow_template_bindings",
				"hil_workflow_cases",
				"hil_workflow_instances",
				"hil_workflow_stage_instances",
				"hil_workflow_tasks",
				"hil_workflow_audit_events",
				"hil_workflow_notification_outbox",
				"workflow_instance_id",
				"resolver_summary_json",
			} {
				if !strings.Contains(joined, expected) {
					t.Fatalf("%s migration is missing %s", name, expected)
				}
			}
		})
	}
}

func TestPlatformRepositoryListDecisionsReturnsEmptyItems(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-empty-page")
	defer closeStore()

	page, err := repo.ListDecisions(ctx, DecisionListQuery{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if page.Items == nil {
		t.Fatal("decision page items are nil, want an empty slice")
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("decision page = %+v, want an empty page", page)
	}
}

func TestPlatformRepositoryPersistsDecisionAndCouncil(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-round-trip")
	defer closeStore()
	decision := testDecisionRequest()
	if err := repo.CreateDecision(ctx, decision); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	loaded, err := repo.GetDecision(ctx, decision.TenantID, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if loaded.ArgsHash != decision.ArgsHash || loaded.Context.BundleID != "bundle-1" || loaded.Route.Mode != ModeCouncil {
		t.Fatalf("loaded decision = %+v", loaded)
	}

	session := testPersistentCouncil(decision)
	if err := repo.CreateCouncil(ctx, session); err != nil {
		t.Fatalf("CreateCouncil: %v", err)
	}
	loadedCouncil, err := repo.GetCouncil(ctx, decision.TenantID, session.ID)
	if err != nil {
		t.Fatalf("GetCouncil: %v", err)
	}
	if len(loadedCouncil.Participants) != 3 || loadedCouncil.Participants[0].Actor.ID != "ding" {
		t.Fatalf("loaded council = %+v", loadedCouncil)
	}
	page, err := repo.ListDecisions(ctx, DecisionListQuery{TenantID: decision.TenantID, Mode: ModeCouncil, Limit: 20})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != decision.ID {
		t.Fatalf("decision page = %+v", page)
	}
	if _, err := repo.GetDecision(ctx, "tenant-2", decision.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant GetDecision error = %v, want ErrNotFound", err)
	}
}

func TestPlatformRepositoryListsOnlyActionableCouncilForParticipant(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-actionable-council")
	defer closeStore()
	decision := testDecisionRequest()
	mustCreateDecisionCouncil(t, ctx, repo, decision)

	query := DecisionListQuery{
		TenantID: decision.TenantID,
		Mode:     ModeCouncil,
		Status:   DecisionPending,
		Mine:     true,
		ActionableBy: ActorRef{
			Kind: ActorHuman,
			ID:   "ding",
		},
	}
	page, err := repo.ListDecisions(ctx, query)
	if err != nil {
		t.Fatalf("ListDecisions before vote: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != decision.ID {
		t.Fatalf("actionable page = %+v", page)
	}

	outsider, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: decision.TenantID, Mode: ModeCouncil, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "not-a-participant"},
	})
	if err != nil {
		t.Fatalf("ListDecisions for outsider: %v", err)
	}
	if outsider.Total != 0 || len(outsider.Items) != 0 {
		t.Fatalf("outsider page = %+v", outsider)
	}

	contribution := CouncilContribution{
		Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Position: "approve", Reasoning: "evidence supports proceeding",
		CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}
	if err := repo.SaveContribution(ctx, decision.TenantID, "council-1", contribution, "ding-contribution", "ding-contribution-fingerprint"); err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	vote := CouncilVote{Actor: contribution.Actor, Choice: VoteApprove, CreatedAt: testRepositoryNow}
	if err := repo.SaveVote(ctx, decision.TenantID, "council-1", vote, "ding-vote", "ding-vote-fingerprint"); err != nil {
		t.Fatalf("SaveVote: %v", err)
	}
	afterVote, err := repo.ListDecisions(ctx, query)
	if err != nil {
		t.Fatalf("ListDecisions after vote: %v", err)
	}
	if afterVote.Total != 0 || len(afterVote.Items) != 0 {
		t.Fatalf("after vote page = %+v", afterVote)
	}
	query.Status = ""
	history, err := repo.ListDecisions(ctx, query)
	if err != nil || history.Total != 1 || len(history.Items) != 1 {
		t.Fatalf("participant history after voting = %+v, %v", history, err)
	}
	query.ActionableBy.ID = "not-a-participant"
	history, err = repo.ListDecisions(ctx, query)
	if err != nil || history.Total != 0 {
		t.Fatalf("history leaked to outsider: %+v, %v", history, err)
	}
}

func TestPlatformRepositoryListsAssignedFixedApprovalForHuman(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-assigned-fixed-approval")
	defer closeStore()
	decision := testDecisionRequest()
	decision.Route = RouteDecision{Mode: ModeApproval, CouncilEffect: EffectAuthorize}
	decision.PreferredMode = ModeApproval
	decision.PayloadPreview = map[string]any{"approval_assigned_to": "ding", "source": "responsibility_autoplan"}
	if err := repo.CreateDecision(ctx, decision); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	page, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: decision.TenantID, Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "ding"},
	})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != decision.ID {
		t.Fatalf("assigned page = %+v err=%v", page, err)
	}
	outsider, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: decision.TenantID, Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "other"},
	})
	if err != nil || outsider.Total != 0 || len(outsider.Items) != 0 {
		t.Fatalf("outsider page = %+v err=%v", outsider, err)
	}
	if _, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: decision.TenantID, Mode: ModeAuto, Mine: true, ActionableBy: ActorRef{Kind: ActorHuman, ID: "ding"},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsupported mine mode error = %v, want ErrInvalidRequest", err)
	}
}

func TestPlatformRepositoryListsAssignedApprovalsAcrossExplicitTenants(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-assigned-cross-tenant-approval")
	defer closeStore()

	organizationDecision := testDecisionRequest()
	organizationDecision.ID = "decision-organization"
	organizationDecision.TenantID = "tenant-organization"
	organizationDecision.Route = RouteDecision{Mode: ModeApproval, CouncilEffect: EffectAuthorize}
	organizationDecision.PreferredMode = ModeApproval
	organizationDecision.PayloadPreview = map[string]any{"approval_assigned_to": "ding"}
	organizationDecision.IdempotencyKey = "decision-organization-key"
	organizationDecision.RequestFingerprint = "decision-organization-fingerprint"
	organizationDecision.CreatedAt = testRepositoryNow.Add(-time.Minute)

	systemDecision := organizationDecision
	systemDecision.ID = "decision-system"
	systemDecision.TenantID = BuiltInWorkflowTenantID
	systemDecision.IdempotencyKey = "decision-system-key"
	systemDecision.RequestFingerprint = "decision-system-fingerprint"
	systemDecision.CreatedAt = testRepositoryNow
	systemDecision.PayloadPreview = map[string]any{"approval_assigned_to": "ding"}

	otherDecision := systemDecision
	otherDecision.ID = "decision-system-other"
	otherDecision.IdempotencyKey = "decision-system-other-key"
	otherDecision.RequestFingerprint = "decision-system-other-fingerprint"
	otherDecision.PayloadPreview = map[string]any{"approval_assigned_to": "other"}

	for _, decision := range []DecisionRequest{organizationDecision, systemDecision, otherDecision} {
		if err := repo.CreateDecision(ctx, decision); err != nil {
			t.Fatalf("CreateDecision(%s): %v", decision.ID, err)
		}
	}

	query := DecisionListQuery{
		TenantID: organizationDecision.TenantID, AdditionalTenantIDs: []string{BuiltInWorkflowTenantID},
		Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "ding"}, Limit: 100,
	}
	page, err := repo.ListDecisions(ctx, query)
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].ID != systemDecision.ID || page.Items[1].ID != organizationDecision.ID {
		t.Fatalf("cross-tenant page = %+v", page)
	}

	query.DecisionID = systemDecision.ID
	filtered, err := repo.ListDecisions(ctx, query)
	if err != nil || filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != systemDecision.ID {
		t.Fatalf("decision-filtered page = %+v err=%v", filtered, err)
	}
}

func TestPlatformRepositoryListsWorkflowAssignedApprovalForHuman(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "hil-workflow-assigned-approval")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.decision.PayloadPreview = map[string]any{"approval_assigned_to": "human-reviewer"}
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatalf("CreateDecisionWorkflow: %v", err)
	}

	page, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: fixture.decision.TenantID, Mode: fixture.decision.Route.Mode, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-reviewer"},
	})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != fixture.decision.ID {
		t.Fatalf("workflow assignee page = %+v err=%v", page, err)
	}

	outsider, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: fixture.decision.TenantID, Mode: fixture.decision.Route.Mode, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "other-human"},
	})
	if err != nil || outsider.Total != 0 || len(outsider.Items) != 0 {
		t.Fatalf("workflow outsider page = %+v err=%v", outsider, err)
	}

	if _, err := repo.store.Exec(ctx, `UPDATE hil_workflow_tasks SET status = ? WHERE tenant_id = ? AND id = ?`, TaskApproved, fixture.decision.TenantID, "task-review"); err != nil {
		t.Fatalf("complete first workflow task: %v", err)
	}
	if _, err := repo.store.Exec(ctx, `UPDATE hil_workflow_tasks SET status = ? WHERE tenant_id = ? AND id = ?`, TaskPending, fixture.decision.TenantID, "task-final"); err != nil {
		t.Fatalf("activate final workflow task: %v", err)
	}
	reviewerAfterStage, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: fixture.decision.TenantID, Mode: fixture.decision.Route.Mode, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-reviewer"},
	})
	if err != nil || reviewerAfterStage.Total != 0 || len(reviewerAfterStage.Items) != 0 {
		t.Fatalf("completed workflow assignee page = %+v err=%v", reviewerAfterStage, err)
	}
	finalReviewer, err := repo.ListDecisions(ctx, DecisionListQuery{
		TenantID: fixture.decision.TenantID, Mode: fixture.decision.Route.Mode, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-final"},
	})
	if err != nil || finalReviewer.Total != 1 || len(finalReviewer.Items) != 1 || finalReviewer.Items[0].ID != fixture.decision.ID {
		t.Fatalf("next workflow assignee page = %+v err=%v", finalReviewer, err)
	}
}

func TestApprovalAssigneePredicateUsesStructuredJSONBLookupForPostgres(t *testing.T) {
	predicate, argument := approvalAssigneePredicate(db.DialectPostgres, "human-ding")
	if predicate != "payload_preview_json ->> 'approval_assigned_to' = ?" {
		t.Fatalf("postgres predicate = %q", predicate)
	}
	if argument != "human-ding" {
		t.Fatalf("postgres argument = %#v", argument)
	}

	predicate, argument = approvalAssigneePredicate(db.DialectSQLite, "human-ding")
	if predicate != `payload_preview_json LIKE ? ESCAPE '\'` {
		t.Fatalf("sqlite predicate = %q", predicate)
	}
	if argument != `%"approval\_assigned\_to":"human-ding"%` {
		t.Fatalf("sqlite argument = %#v", argument)
	}
}

func TestPlatformRepositoryCreatesDecisionAndCouncilAtomically(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-atomic-create")
	defer closeStore()
	first := testDecisionRequest()
	firstCouncil := testPersistentCouncil(first)
	if err := repo.CreateDecisionWithCouncil(ctx, first, &firstCouncil); err != nil {
		t.Fatalf("first CreateDecisionWithCouncil: %v", err)
	}
	second := testDecisionRequest()
	second.ID = "decision-2"
	second.IdempotencyKey = "decision-key-2"
	second.RequestFingerprint = "fingerprint-decision-2"
	secondCouncil := testPersistentCouncil(second)
	secondCouncil.ID = firstCouncil.ID
	if err := repo.CreateDecisionWithCouncil(ctx, second, &secondCouncil); err == nil {
		t.Fatal("second CreateDecisionWithCouncil succeeded with duplicate council id")
	}
	if _, err := repo.GetDecision(ctx, second.TenantID, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partially persisted decision error = %v, want ErrNotFound", err)
	}
}

func TestPlatformRepositoryContributionIsIdempotentAndConflictSafe(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-contribution")
	defer closeStore()
	decision := testDecisionRequest()
	mustCreateDecisionCouncil(t, ctx, repo, decision)
	contribution := CouncilContribution{
		Actor:        ActorRef{Kind: ActorHuman, ID: "ding"},
		Position:     "approve",
		Reasoning:    "evidence supports proceeding",
		EvidenceRefs: []string{"evidence-1"},
		CreatedAt:    testRepositoryNow,
		UpdatedAt:    testRepositoryNow,
	}
	if err := repo.SaveContribution(ctx, decision.TenantID, "council-1", contribution, "contribution-1", "fingerprint-a"); err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	if err := repo.SaveContribution(ctx, decision.TenantID, "council-1", contribution, "contribution-1", "fingerprint-a"); err != nil {
		t.Fatalf("idempotent SaveContribution: %v", err)
	}
	changed := contribution
	changed.Reasoning = "different"
	if err := repo.SaveContribution(ctx, decision.TenantID, "council-1", changed, "contribution-1", "fingerprint-b"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting SaveContribution error = %v, want ErrIdempotencyConflict", err)
	}
	loaded, err := repo.GetCouncil(ctx, decision.TenantID, "council-1")
	if err != nil {
		t.Fatalf("GetCouncil: %v", err)
	}
	if len(loaded.Contributions) != 1 || loaded.Contributions[0].Reasoning != contribution.Reasoning {
		t.Fatalf("contributions = %+v", loaded.Contributions)
	}
}

func TestPlatformRepositoryVoteIsImmutable(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-vote")
	defer closeStore()
	decision := testDecisionRequest()
	mustCreateDecisionCouncil(t, ctx, repo, decision)
	vote := CouncilVote{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteApprove, Reason: "approved", CreatedAt: testRepositoryNow}
	if err := repo.SaveContribution(ctx, decision.TenantID, "council-1", CouncilContribution{
		Actor: vote.Actor, Position: "approve", Reasoning: "reviewed", CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}, "vote-immutable-contribution", "vote-immutable-contribution-fingerprint"); err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	if err := repo.SaveVote(ctx, decision.TenantID, "council-1", vote, "vote-1", "fingerprint-a"); err != nil {
		t.Fatalf("SaveVote: %v", err)
	}
	if err := repo.SaveVote(ctx, decision.TenantID, "council-1", vote, "vote-1", "fingerprint-a"); err != nil {
		t.Fatalf("idempotent SaveVote: %v", err)
	}
	changed := vote
	changed.Choice = VoteReject
	if err := repo.SaveVote(ctx, decision.TenantID, "council-1", changed, "vote-2", "fingerprint-b"); !errors.Is(err, ErrVoteImmutable) {
		t.Fatalf("changed SaveVote error = %v, want ErrVoteImmutable", err)
	}
}

func TestPlatformRepositoryVoteRequiresContribution(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-vote-contribution-required")
	defer closeStore()
	decision := testDecisionRequest()
	mustCreateDecisionCouncil(t, ctx, repo, decision)
	err := repo.SaveVote(ctx, decision.TenantID, "council-1", CouncilVote{
		Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Choice: VoteApprove, CreatedAt: testRepositoryNow,
	}, "vote-without-contribution", "vote-without-contribution-fingerprint")
	if !errors.Is(err, ErrContributionRequired) {
		t.Fatalf("SaveVote error = %v, want ErrContributionRequired", err)
	}
	loaded, err := repo.GetCouncil(ctx, decision.TenantID, "council-1")
	if err != nil || len(loaded.Votes) != 0 {
		t.Fatalf("council after rejected vote = %+v err=%v", loaded, err)
	}
}

func TestPlatformRepositoryResolvesCouncilWithReceipt(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-resolution")
	defer closeStore()
	decision := testDecisionRequest()
	mustCreateDecisionCouncil(t, ctx, repo, decision)
	session, err := repo.GetCouncil(ctx, decision.TenantID, "council-1")
	if err != nil {
		t.Fatalf("GetCouncil: %v", err)
	}
	session.Status = CouncilPassed
	session.Effect = EffectAuthorize
	session.ResolvedAt = testRepositoryNow.Add(time.Minute)
	receipt := DecisionReceipt{
		ID:        "receipt-1",
		TenantID:  decision.TenantID,
		RequestID: decision.ID,
		CouncilID: session.ID,
		ArgsHash:  decision.ArgsHash,
		Effect:    EffectAuthorize,
		CreatedAt: session.ResolvedAt,
		ExpiresAt: session.ResolvedAt.Add(15 * time.Minute),
		SingleUse: true,
	}
	if err := repo.ResolveCouncil(ctx, session, receipt); err != nil {
		t.Fatalf("ResolveCouncil: %v", err)
	}
	receipts, err := repo.ListDecisionReceipts(ctx, decision.TenantID, decision.ID)
	if err != nil {
		t.Fatalf("ListDecisionReceipts: %v", err)
	}
	if len(receipts) != 1 || receipts[0].ArgsHash != decision.ArgsHash || !receipts[0].SingleUse {
		t.Fatalf("receipts = %+v", receipts)
	}
}

func TestPlatformRepositoryPromotesHighRiskCouncilToFixedApproval(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-council-approval-handoff")
	defer closeStore()
	decision := testDecisionRequest()
	decision.RiskLevel = RiskHigh
	decision.Route.CouncilEffect = EffectRequiresApproval
	decision.PayloadPreview = map[string]any{"approval_assigned_to": "human-manager"}
	mustCreateDecisionCouncil(t, ctx, repo, decision)
	session, err := repo.GetCouncil(ctx, decision.TenantID, "council-1")
	if err != nil {
		t.Fatalf("GetCouncil: %v", err)
	}
	session.Status = CouncilPassed
	session.Effect = EffectRequiresApproval
	session.ResolvedAt = testRepositoryNow.Add(time.Minute)
	receipt := DecisionReceipt{
		ID: "receipt-handoff-1", TenantID: decision.TenantID, RequestID: decision.ID, CouncilID: session.ID,
		ArgsHash: decision.ArgsHash, Effect: EffectRequiresApproval, CreatedAt: session.ResolvedAt,
	}
	if err := repo.ResolveCouncil(ctx, session, receipt); err != nil {
		t.Fatalf("ResolveCouncil: %v", err)
	}
	loaded, err := repo.GetDecision(ctx, decision.TenantID, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if loaded.Status != DecisionPending || loaded.Route.Mode != ModeApproval {
		t.Fatalf("handoff decision = %+v, want pending fixed approval", loaded)
	}
}

func TestPlatformRepositoryRejectsUnassignedHighRiskCouncilHandoff(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-unassigned-council-handoff")
	defer closeStore()
	decision := testDecisionRequest()
	decision.RiskLevel = RiskHigh
	decision.Route.CouncilEffect = EffectRequiresApproval
	if err := repo.CreateDecision(ctx, decision); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateDecision error = %v, want ErrInvalidRequest", err)
	}
}

func TestPlatformRepositoryResolvesOrdinaryDecisionOnceWithReceipt(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openHILRepository(t, "hil-ordinary-resolution")
	defer closeStore()
	decision := testDecisionRequest()
	decision.Route = RouteDecision{Mode: ModeHIL}
	decision.PreferredMode = ModeHIL
	if err := repo.CreateDecision(ctx, decision); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	decision.Status = DecisionResolved
	receipt := DecisionReceipt{
		ID: "receipt-ordinary-1", TenantID: decision.TenantID, RequestID: decision.ID, ArgsHash: decision.ArgsHash,
		Effect: EffectAuthorize, SingleUse: true, CreatedAt: testRepositoryNow, ExpiresAt: testRepositoryNow.Add(time.Minute),
	}
	if err := repo.ResolveDecision(ctx, decision, receipt); err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	if err := repo.ResolveDecision(ctx, decision, receipt); !errors.Is(err, ErrConflict) {
		t.Fatalf("second ResolveDecision error = %v, want ErrConflict", err)
	}
	loaded, err := repo.GetDecision(ctx, decision.TenantID, decision.ID)
	if err != nil || loaded.Status != DecisionResolved {
		t.Fatalf("loaded decision = %+v err=%v", loaded, err)
	}
	receipts, err := repo.ListDecisionReceipts(ctx, decision.TenantID, decision.ID)
	if err != nil || len(receipts) != 1 || receipts[0].ID != receipt.ID {
		t.Fatalf("receipts = %+v err=%v", receipts, err)
	}
}

var testRepositoryNow = time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)

func openHILTestStore(t *testing.T, name string) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: name})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func openHILRepository(t *testing.T, name string) (*PlatformRepository, func()) {
	t.Helper()
	store := openHILTestStore(t, name)
	if err := migration.NewRunner([]migration.Migration{hilDecisionMigration(), hilWorkflowMigration()}).Up(context.Background(), store); err != nil {
		store.Close()
		t.Fatalf("migration up: %v", err)
	}
	return NewPlatformRepository(store), func() { _ = store.Close() }
}

func assertHILIndexExists(t *testing.T, store *db.Store, index string) {
	t.Helper()
	var found string
	if err := store.QueryRow(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
		t.Fatalf("missing index %s: %v", index, err)
	}
}

func assertHILColumnExists(t *testing.T, store *db.Store, table, column string) {
	t.Helper()
	rows, err := store.Query(context.Background(), `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatalf("table info %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table info %s: %v", table, err)
		}
		if name == column {
			return
		}
	}
	t.Fatalf("missing column %s.%s", table, column)
}

func testDecisionRequest() DecisionRequest {
	return DecisionRequest{
		ID:                 "decision-1",
		TenantID:           "tenant-1",
		Requester:          ActorRef{Kind: ActorAgent, ID: "agent-requester"},
		Action:             "customer.update",
		ActionTypeID:       "customer.update",
		Resource:           ObjectRef{Kind: "customer", ID: "customer-1"},
		ObjectRefs:         []ObjectRef{{Kind: "customer", ID: "customer-1"}},
		ArgsHash:           "sha256:args",
		Purpose:            "correct customer record",
		RiskLevel:          RiskMedium,
		PreferredMode:      ModeCouncil,
		Route:              RouteDecision{Mode: ModeCouncil, CouncilEffect: EffectAuthorize, Reasons: []string{"multi-party context"}},
		Context:            OntologyContextSnapshot{BundleID: "bundle-1", SchemaHash: "schema-1", Confidence: 0.91, Complete: true, EvidenceRefs: []string{"evidence-1"}},
		PayloadPreview:     map[string]any{"field": "value"},
		IdempotencyKey:     "decision-key-1",
		RequestFingerprint: "fingerprint-decision-1",
		Status:             DecisionPending,
		CreatedAt:          testRepositoryNow,
		ExpiresAt:          testRepositoryNow.Add(time.Hour),
	}
}

func testPersistentCouncil(decision DecisionRequest) CouncilSession {
	session := testCouncilSession(decision.RiskLevel)
	session.ID = "council-1"
	session.TenantID = decision.TenantID
	session.RequestID = decision.ID
	session.ArgsHash = decision.ArgsHash
	session.CreatedAt = testRepositoryNow
	session.UpdatedAt = testRepositoryNow
	session.ExpiresAt = decision.ExpiresAt
	return session
}

func mustCreateDecisionCouncil(t *testing.T, ctx context.Context, repo *PlatformRepository, decision DecisionRequest) {
	t.Helper()
	if err := repo.CreateDecision(ctx, decision); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if err := repo.CreateCouncil(ctx, testPersistentCouncil(decision)); err != nil {
		t.Fatalf("CreateCouncil: %v", err)
	}
}

func TestDecisionRequestedByMeFiltersBeforePagination(t *testing.T) {
	ctx := context.Background()
	store := openHILTestStore(t, "requester-history")
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{hilDecisionMigration(), hilWorkflowMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	repo := NewPlatformRepository(store)
	owner := ActorRef{Kind: ActorHuman, ID: "owner"}
	for i, fixture := range []struct {
		tenant string
		actor  ActorRef
	}{
		{"tenant-a", owner}, {"tenant-a", ActorRef{Kind: ActorHuman, ID: "other"}}, {"tenant-b", owner},
	} {
		decision := testDecisionRequest()
		decision.ID = []string{"own", "other", "foreign"}[i]
		decision.IdempotencyKey = decision.ID
		decision.TenantID = fixture.tenant
		decision.Requester = fixture.actor
		if err := repo.CreateDecision(ctx, decision); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListDecisions(ctx, DecisionListQuery{TenantID: "tenant-a", RequestedBy: owner, Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "own" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
