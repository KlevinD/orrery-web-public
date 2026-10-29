package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestMetadataPromotionPlanServiceCreatesAppliesAndRetriesIdempotently(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	principal := promotionCommandPrincipal("reviewer-1")
	proposal := fixture.saveAcceptedMappingProposal(t, "proposal-1", "planning")

	created, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	if created.PlanID != "opplan_test_1" || created.ProposalRevision != proposal.Revision || len(created.Changes) != 1 ||
		created.Changes[0].TargetKind != "mapping_rule" || created.Changes[0].Field != "source_domain" ||
		!created.ExpiresAt.Equal(fixture.now.Add(10*time.Minute)) {
		t.Fatalf("created plan = %+v", created)
	}
	stored, err := fixture.plans.GetMetadataPromotionPlan(context.Background(), created.PlanID)
	if err != nil {
		t.Fatalf("GetMetadataPromotionPlan: %v", err)
	}
	if stored.PrincipalKey != authorization.PrincipalCacheKey(principal) || stored.BindingVersion != 3 ||
		stored.AuthorizationRevision != "authorization:0" || stored.PolicyRevision != "policy:0" ||
		len(stored.Targets) != 1 || stored.Targets[0].DefinitionTenantID != "tenant-1" || stored.Targets[0].Version != 1 {
		t.Fatalf("stored plan = %+v", stored)
	}

	receipt, err := fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
	})
	if err != nil {
		t.Fatalf("ApplyPromotionPlan: %v", err)
	}
	if receipt.Status != ontology.PromotionReceiptStatusApplied || !receipt.Applied || receipt.ActorID != "reviewer-1" || receipt.ActorKind != identity.ViewerKindUser {
		t.Fatalf("receipt = %+v", receipt)
	}
	rule := fixture.mappingRule(t)
	if rule.SourceDomain != "planning" || rule.Version != 2 {
		t.Fatalf("applied mapping rule = %+v", rule)
	}
	retried, err := fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
	})
	if err != nil || retried.ReceiptID != receipt.ReceiptID || fixture.mappingRule(t).Version != 2 {
		t.Fatalf("idempotent retry = %+v err=%v", retried, err)
	}
	if _, err := fixture.core.Exec(context.Background(), `UPDATE data_policy_revision SET revision = 1 WHERE id = 'global'`); err != nil {
		t.Fatalf("advance policy revision: %v", err)
	}
	fixture.snapshots.mu.Lock()
	fixture.snapshots.policyRevision = "policy:1"
	fixture.snapshots.mu.Unlock()
	retried, err = fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
	})
	if err != nil || retried.ReceiptID != receipt.ReceiptID || fixture.mappingRule(t).Version != 2 {
		t.Fatalf("idempotent retry after policy revision = %+v err=%v", retried, err)
	}
	fixture.policy.mu.Lock()
	fixture.policy.denied = true
	fixture.policy.mu.Unlock()
	if _, err := fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
	}); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("policy-revoked idempotent retry error = %v", err)
	}
	fixture.policy.mu.Lock()
	fixture.policy.denied = false
	fixture.policy.mu.Unlock()
	if _, err := fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "different-key", Confirmed: true,
	}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("different-key retry error = %v", err)
	}
}

func TestMetadataPromotionPlanServiceDerivesUserAgentAndSystemActors(t *testing.T) {
	tests := []struct {
		name      string
		principal identity.Principal
		actorID   string
		actorKind string
	}{
		{name: "user", principal: promotionCommandPrincipal("reviewer-1"), actorID: "reviewer-1", actorKind: identity.ViewerKindUser},
		{name: "agent", principal: identity.NormalizePrincipal(identity.Principal{
			SubjectProvider: "oidc", Subject: "agent-subject", AgentProfileID: "agent-1", ViewerKind: identity.ViewerKindAgent,
			CoarseScopes: []string{"operations.manage"},
		}), actorID: "agent-1", actorKind: identity.ViewerKindAgent},
		{name: "system", principal: identity.NormalizePrincipal(identity.Principal{
			SubjectProvider: "internal", Subject: "scheduler", ViewerKind: identity.ViewerKindSystem,
			CoarseScopes: []string{"operations.manage"},
		}), actorID: "scheduler", actorKind: identity.ViewerKindSystem},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromotionCommandFixture(t)
			defer fixture.close()
			proposal := fixture.saveAcceptedMappingProposal(t, "proposal-1", "planning")
			created, err := fixture.service.CreatePromotionPlan(context.Background(), tt.principal, CreatePromotionPlanRequest{
				TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
			})
			if err != nil {
				t.Fatalf("CreatePromotionPlan: %v", err)
			}
			receipt, err := fixture.service.ApplyPromotionPlan(context.Background(), tt.principal, ApplyPromotionPlanRequest{
				TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
			})
			if err != nil {
				t.Fatalf("ApplyPromotionPlan: %v", err)
			}
			if receipt.ActorID != tt.actorID || receipt.ActorKind != tt.actorKind {
				t.Fatalf("receipt actor = %s/%s, want %s/%s", receipt.ActorID, receipt.ActorKind, tt.actorID, tt.actorKind)
			}
		})
	}
}

func TestMetadataPromotionPlanServiceRejectsTamperingAndWrongPrincipal(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	proposal := fixture.saveAcceptedMappingProposal(t, "proposal-1", "planning")
	owner := promotionCommandPrincipal("reviewer-1")
	created, err := fixture.service.CreatePromotionPlan(context.Background(), owner, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	for _, request := range []struct {
		principal identity.Principal
		tenantID  string
		planID    string
	}{
		{principal: owner, tenantID: "tenant-1", planID: created.PlanID + "-tampered"},
		{principal: promotionCommandPrincipal("reviewer-2"), tenantID: "tenant-1", planID: created.PlanID},
		{principal: owner, tenantID: "tenant-2", planID: created.PlanID},
	} {
		_, err := fixture.service.ApplyPromotionPlan(context.Background(), request.principal, ApplyPromotionPlanRequest{
			TenantID: request.tenantID, PlanID: request.planID, IdempotencyKey: "apply-1", Confirmed: true,
		})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("tampered apply error = %v", err)
		}
	}
	stored, err := fixture.plans.GetMetadataPromotionPlan(context.Background(), created.PlanID)
	if err != nil || stored.State != ontology.MetadataPromotionPlanActive {
		t.Fatalf("plan after tampering attempts = %+v err=%v", stored, err)
	}
}

func TestMetadataPromotionPlanServiceInvalidatesStaleSecurityAndDataState(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*promotionCommandFixture, ontology.OntologyMetadataProposal)
		expectedState ontology.MetadataPromotionPlanState
	}{
		{name: "expired", mutate: func(f *promotionCommandFixture, _ ontology.OntologyMetadataProposal) {
			f.now = f.now.Add(11 * time.Minute)
		}},
		{name: "proposal content", mutate: func(f *promotionCommandFixture, proposal ontology.OntologyMetadataProposal) {
			proposal.ProposedValue = "finance"
			hash := ontology.CanonicalMetadataProposalContentHash(proposal)
			if _, err := f.core.Exec(context.Background(), `UPDATE ontology_metadata_proposals SET proposed_value = ?, proposal_content_hash = ? WHERE id = ?`, proposal.ProposedValue, hash, proposal.ID); err != nil {
				t.Fatalf("mutate proposal content: %v", err)
			}
		}},
		{name: "target version", mutate: func(f *promotionCommandFixture, _ ontology.OntologyMetadataProposal) {
			rule := f.mappingRule(t)
			rule.SourceRef = "changed-source"
			if _, err := f.registry.SaveMappingRuleWithExpectedVersion(context.Background(), rule, rule.Version); err != nil {
				t.Fatalf("mutate mapping rule: %v", err)
			}
		}},
		{name: "binding version", mutate: func(f *promotionCommandFixture, _ ontology.OntologyMetadataProposal) {
			f.resolver.mu.Lock()
			binding := f.resolver.bindings["tenant-1"]
			binding.BindingVersion++
			f.resolver.bindings["tenant-1"] = binding
			f.resolver.mu.Unlock()
		}},
		{name: "authorization revision", mutate: func(f *promotionCommandFixture, _ ontology.OntologyMetadataProposal) {
			f.snapshots.mu.Lock()
			f.snapshots.authorizationRevision = "authorization:1"
			f.snapshots.mu.Unlock()
		}},
		{name: "permission revoked", mutate: func(f *promotionCommandFixture, _ ontology.OntologyMetadataProposal) {
			f.authorizer.mu.Lock()
			f.authorizer.deniedTenants["tenant-1"] = true
			f.authorizer.mu.Unlock()
		}, expectedState: ontology.MetadataPromotionPlanActive},
		{name: "mutation policy revoked", mutate: func(f *promotionCommandFixture, _ ontology.OntologyMetadataProposal) {
			f.policy.mu.Lock()
			f.policy.denied = true
			f.policy.mu.Unlock()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromotionCommandFixture(t)
			defer fixture.close()
			proposal := fixture.saveAcceptedMappingProposal(t, "proposal-1", "planning")
			principal := promotionCommandPrincipal("reviewer-1")
			created, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
				TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
			})
			if err != nil {
				t.Fatalf("CreatePromotionPlan: %v", err)
			}
			tt.mutate(fixture, proposal)
			_, err = fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
				TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
			})
			if err == nil {
				t.Fatal("stale promotion plan unexpectedly applied")
			}
			stored, getErr := fixture.plans.GetMetadataPromotionPlan(context.Background(), created.PlanID)
			expectedState := tt.expectedState
			if expectedState == "" {
				expectedState = ontology.MetadataPromotionPlanInvalidated
			}
			if getErr != nil || stored.State != expectedState && !(expectedState == ontology.MetadataPromotionPlanInvalidated && stored.State == ontology.MetadataPromotionPlanExpired) {
				t.Fatalf("terminal plan = %+v err=%v applyErr=%v", stored, getErr, err)
			}
			if fixture.mappingRule(t).SourceDomain != "mission" && tt.name != "target version" {
				t.Fatalf("stale plan mutated registry: %+v", fixture.mappingRule(t))
			}
		})
	}
}

func TestMetadataPromotionPlanServiceReauthorizesImmediatelyBeforeApplyTransaction(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(*promotionCommandFixture)
	}{
		{name: "scope permission", revoke: func(f *promotionCommandFixture) {
			f.authorizer.mu.Lock()
			f.authorizer.deniedTenants["tenant-1"] = true
			f.authorizer.mu.Unlock()
		}},
		{name: "mutation policy", revoke: func(f *promotionCommandFixture) {
			f.policy.mu.Lock()
			f.policy.denied = true
			f.policy.mu.Unlock()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromotionCommandFixture(t)
			defer fixture.close()
			principal := promotionCommandPrincipal("reviewer-1")
			proposal := fixture.saveAcceptedMappingProposal(t, "proposal-transaction-revoke", "planning")
			plan, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
				TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
			})
			if err != nil {
				t.Fatalf("CreatePromotionPlan: %v", err)
			}
			fixture.service.unitOfWork = transactionHookUnitOfWork{
				inner: ontology.NewPlatformMetadataPromotionUnitOfWork(fixture.core), hook: func() { tt.revoke(fixture) },
			}
			_, err = fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
				TenantID: "tenant-1", PlanID: plan.PlanID, IdempotencyKey: "apply-transaction-revoke", Confirmed: true,
			})
			if !errors.Is(err, ontologyscope.ErrAccessDenied) {
				t.Fatalf("ApplyPromotionPlan error = %v", err)
			}
			stored, getErr := fixture.plans.GetMetadataPromotionPlan(context.Background(), plan.PlanID)
			if getErr != nil || stored.State != ontology.MetadataPromotionPlanInvalidated {
				t.Fatalf("invalidated plan = %+v, err=%v", stored, getErr)
			}
			if rule := fixture.mappingRule(t); rule.SourceDomain != "mission" || rule.Version != 1 {
				t.Fatalf("registry changed after transactional revocation: %+v", rule)
			}
		})
	}
}

func TestMetadataPromotionPlanServiceFencesAuthorizationRevisionAfterCommitGuard(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	principal := promotionCommandPrincipal("reviewer-1")
	proposal := fixture.saveAcceptedMappingProposal(t, "proposal-transaction-revision", "planning")
	plan, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	fixture.service.unitOfWork = transactionHookUnitOfWork{
		inner: ontology.NewPlatformMetadataPromotionUnitOfWork(fixture.core),
		afterGuard: func() {
			if _, updateErr := fixture.core.Exec(context.Background(), `UPDATE rebac_authorization_revision SET revision = 1 WHERE id = 'global'`); updateErr != nil {
				t.Fatalf("advance authorization revision: %v", updateErr)
			}
		},
	}
	_, err = fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: plan.PlanID, IdempotencyKey: "apply-transaction-revision", Confirmed: true,
	})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("ApplyPromotionPlan error = %v", err)
	}
	stored, getErr := fixture.plans.GetMetadataPromotionPlan(context.Background(), plan.PlanID)
	if getErr != nil || stored.State != ontology.MetadataPromotionPlanInvalidated || stored.InvalidationReason != "authorization_context_changed" {
		t.Fatalf("invalidated plan = %+v, err=%v", stored, getErr)
	}
	if rule := fixture.mappingRule(t); rule.SourceDomain != "mission" || rule.Version != 1 {
		t.Fatalf("registry changed after fenced revocation: %+v", rule)
	}
}

func TestMetadataPromotionPlanServiceFencesPersistedBindingAfterCommitGuard(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	principal := promotionCommandPrincipal("reviewer-1")
	proposal := fixture.saveAcceptedMappingProposal(t, "proposal-transaction-binding", "planning")
	plan, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	fixture.service.unitOfWork = transactionHookUnitOfWork{
		inner: ontology.NewPlatformMetadataPromotionUnitOfWork(fixture.core),
		afterGuard: func() {
			if _, updateErr := fixture.core.Exec(context.Background(), `UPDATE ontology_scope_bindings SET binding_version = 4 WHERE tenant_id = ?`, "tenant-1"); updateErr != nil {
				t.Fatalf("advance scope binding: %v", updateErr)
			}
		},
	}
	_, err = fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: plan.PlanID, IdempotencyKey: "apply-transaction-binding", Confirmed: true,
	})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("ApplyPromotionPlan error = %v", err)
	}
	stored, getErr := fixture.plans.GetMetadataPromotionPlan(context.Background(), plan.PlanID)
	if getErr != nil || stored.State != ontology.MetadataPromotionPlanInvalidated || stored.InvalidationReason != "authorization_context_changed" {
		t.Fatalf("invalidated plan = %+v, err=%v", stored, getErr)
	}
	if rule := fixture.mappingRule(t); rule.SourceDomain != "mission" || rule.Version != 1 {
		t.Fatalf("registry changed after fenced binding update: %+v", rule)
	}
	page, receiptErr := fixture.receipts.ListPromotionReceipts(context.Background(), ontology.PromotionReceiptQuery{TenantID: "tenant-1"})
	if receiptErr != nil || len(page.Receipts) != 0 {
		t.Fatalf("receipt persisted after fenced binding update: %+v err=%v", page, receiptErr)
	}
}

func TestMetadataPromotionPlanServiceDecidesWithServerPrincipalAndFreshTargetAuthorization(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	if err := fixture.registry.SaveObjectType(context.Background(), ontology.ObjectType{
		ID: "tool", TenantScope: ontology.TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal := ontology.OntologyMetadataProposal{
		ID: "proposal-global-decision", TenantID: "tenant-1", TargetKind: "object_type", TargetID: "tool",
		MetadataKey: "allowed_action", ProposedValue: "tool.manage", Source: "operator_suggested.admin",
		SourceKind: ontology.MetadataProposalSourceOperatorSuggested, Confidence: 1, Status: ontology.MetadataProposalStatusProposed,
	}
	if err := fixture.proposals.SaveMetadataProposal(context.Background(), proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	proposal, err := fixture.proposals.GetMetadataProposalForTenant(context.Background(), "tenant-1", proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	request := DecideProposalRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
		Decision: ontology.MetadataProposalStatusAccepted, Reason: "validated by steward",
	}
	fixture.authorizer.mu.Lock()
	fixture.authorizer.deniedTenants["global"] = true
	fixture.authorizer.mu.Unlock()
	if _, err := fixture.service.DecideProposal(context.Background(), promotionCommandPrincipal("reviewer-1"), request); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("DecideProposal without global authorization error = %v", err)
	}
	fixture.authorizer.mu.Lock()
	delete(fixture.authorizer.deniedTenants, "global")
	fixture.authorizer.mu.Unlock()
	decided, err := fixture.service.DecideProposal(context.Background(), promotionCommandPrincipal("reviewer-1"), request)
	if err != nil {
		t.Fatalf("DecideProposal: %v", err)
	}
	if decided.Status != ontology.MetadataProposalStatusAccepted || decided.ReviewedBy != "reviewer-1" || decided.ReviewedByKind != identity.ViewerKindUser || decided.Revision != proposal.Revision+1 {
		t.Fatalf("decided proposal = %+v", decided)
	}
}

func TestMetadataPromotionPlanServiceFencesDecisionSecurityStateAfterCommitGuard(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*promotionCommandFixture) error
	}{
		{
			name: "authorization revision",
			mutate: func(f *promotionCommandFixture) error {
				_, err := f.core.Exec(context.Background(), `UPDATE rebac_authorization_revision SET revision = 1 WHERE id = 'global'`)
				return err
			},
		},
		{
			name: "policy revision",
			mutate: func(f *promotionCommandFixture) error {
				_, err := f.core.Exec(context.Background(), `UPDATE data_policy_revision SET revision = 1 WHERE id = 'global'`)
				return err
			},
		},
		{
			name: "scope binding revision",
			mutate: func(f *promotionCommandFixture) error {
				_, err := f.core.Exec(context.Background(), `UPDATE ontology_scope_bindings SET binding_version = 4 WHERE tenant_id = ?`, "tenant-1")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromotionCommandFixture(t)
			defer fixture.close()
			proposal := fixture.saveProposedMappingProposal(t, "proposal-decision-fence", "planning")
			fixture.service.unitOfWork = transactionHookUnitOfWork{
				inner: ontology.NewPlatformMetadataPromotionUnitOfWork(fixture.core),
				afterGuard: func() {
					if err := tt.mutate(fixture); err != nil {
						t.Fatalf("mutate fenced state: %v", err)
					}
				},
			}
			_, err := fixture.service.DecideProposal(context.Background(), promotionCommandPrincipal("reviewer-1"), DecideProposalRequest{
				TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
				Decision: ontology.MetadataProposalStatusAccepted, Reason: "reviewed",
			})
			if !errors.Is(err, ErrStateConflict) {
				t.Fatalf("DecideProposal error = %v", err)
			}
			stored, getErr := fixture.proposals.GetMetadataProposalForTenant(context.Background(), "tenant-1", proposal.ID)
			if getErr != nil {
				t.Fatalf("GetMetadataProposalForTenant: %v", getErr)
			}
			if stored.Status != ontology.MetadataProposalStatusProposed || stored.Revision != proposal.Revision || stored.ReviewedBy != "" || stored.ReviewReason != "" {
				t.Fatalf("decision persisted across fence failure: %+v", stored)
			}
		})
	}
}

func TestMetadataPromotionPlanServiceRequiresGlobalTargetAuthorization(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	if err := fixture.registry.SaveObjectType(context.Background(), ontology.ObjectType{
		ID: "tool", TenantScope: ontology.TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal := ontology.OntologyMetadataProposal{
		ID: "proposal-global", TenantID: "tenant-1", TargetKind: "object_type", TargetID: "tool",
		MetadataKey: "allowed_action", ProposedValue: "tool.manage", Source: "operator_suggested.admin",
		SourceKind: ontology.MetadataProposalSourceOperatorSuggested, Confidence: 1, Status: ontology.MetadataProposalStatusAccepted,
	}
	if err := fixture.proposals.SaveMetadataProposal(context.Background(), proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	proposal, _ = fixture.proposals.GetMetadataProposalForTenant(context.Background(), "tenant-1", proposal.ID)
	fixture.authorizer.mu.Lock()
	fixture.authorizer.deniedTenants["global"] = true
	fixture.authorizer.mu.Unlock()
	_, err := fixture.service.CreatePromotionPlan(context.Background(), promotionCommandPrincipal("reviewer-1"), CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("global target authorization error = %v", err)
	}
}

func TestMetadataPromotionPlanServiceCreatesGlobalDefinitionWithSystemAuthorization(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	proposal := ontology.OntologyMetadataProposal{
		ID: "proposal-global-definition", TenantID: "tenant-1", TargetKind: "object_type", TargetID: "global_case",
		MetadataKey:   "definition",
		ProposedValue: `{"id":"global_case","tenant_scope":"global","display_name":"Global case","canonical_key_rule":"global_case:{id}"}`,
		Source:        "operator_suggested.admin", SourceKind: ontology.MetadataProposalSourceOperatorSuggested,
		Confidence: 1, Status: ontology.MetadataProposalStatusAccepted,
	}
	if err := fixture.proposals.SaveMetadataProposal(context.Background(), proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	proposal, err := fixture.proposals.GetMetadataProposalForTenant(context.Background(), "tenant-1", proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	principal := promotionCommandPrincipal("reviewer-1")
	created, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	stored, err := fixture.plans.GetMetadataPromotionPlan(context.Background(), created.PlanID)
	if err != nil || len(stored.Targets) != 1 || stored.Targets[0].DefinitionTenantID != "" {
		t.Fatalf("global promotion target = %+v err=%v", stored.Targets, err)
	}
	if _, err := fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-global", Confirmed: true,
	}); err != nil {
		t.Fatalf("ApplyPromotionPlan: %v", err)
	}
	definition, err := fixture.registry.GetObjectType(context.Background(), "global_case")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if definition.TenantID != "" || definition.TenantScope != ontology.TenantScopeGlobal {
		t.Fatalf("global definition = %+v", definition)
	}
}

func TestMetadataPromotionPlanServiceRejectsUnavailableAuthorizationRevision(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	fixture.snapshots.mu.Lock()
	fixture.snapshots.authorizationRevision = ""
	fixture.snapshots.mu.Unlock()
	proposal := fixture.saveAcceptedMappingProposal(t, "proposal-1", "planning")
	principal := promotionCommandPrincipal("reviewer-1")
	_, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		t.Fatalf("CreatePromotionPlan error = %v", err)
	}
}

func TestMetadataProposalPromotionAtomicRollbackWhenPlanConsumptionFails(t *testing.T) {
	fixture := newPromotionCommandFixture(t)
	defer fixture.close()
	proposal := fixture.saveAcceptedMappingProposal(t, "proposal-1", "planning")
	principal := promotionCommandPrincipal("reviewer-1")
	created, err := fixture.service.CreatePromotionPlan(context.Background(), principal, CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	fixture.service.unitOfWork = failingPlanTransitionUnitOfWork{inner: ontology.NewPlatformMetadataPromotionUnitOfWork(fixture.core)}
	if _, err := fixture.service.ApplyPromotionPlan(context.Background(), principal, ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: created.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
	}); err == nil {
		t.Fatal("ApplyPromotionPlan unexpectedly succeeded")
	}
	if rule := fixture.mappingRule(t); rule.SourceDomain != "mission" || rule.Version != 1 {
		t.Fatalf("registry write was not rolled back: %+v", rule)
	}
	page, err := fixture.receipts.ListPromotionReceipts(context.Background(), ontology.PromotionReceiptQuery{TenantID: "tenant-1"})
	if err != nil || len(page.Receipts) != 0 {
		t.Fatalf("receipt write was not rolled back: %+v err=%v", page, err)
	}
	plan, err := fixture.plans.GetMetadataPromotionPlan(context.Background(), created.PlanID)
	if err != nil || plan.State != ontology.MetadataPromotionPlanActive {
		t.Fatalf("plan write was not rolled back: %+v err=%v", plan, err)
	}
}

type promotionCommandFixture struct {
	databases  *db.Registry
	core       *db.Store
	registry   *ontology.PlatformRegistry
	proposals  *ontology.PlatformMetadataProposalStore
	plans      *ontology.PlatformMetadataPromotionPlanStore
	receipts   *ontology.PlatformMetadataProposalPromotionReceiptStore
	resolver   *promotionCommandResolver
	authorizer *promotionCommandAuthorizer
	snapshots  *promotionCommandSnapshots
	policy     *promotionCommandPolicy
	service    *CommandService
	now        time.Time
}

func newPromotionCommandFixture(t *testing.T) *promotionCommandFixture {
	t.Helper()
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: strings.ReplaceAll(t.Name(), "/", "-")},
	}})
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		databases.Close()
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := datapolicy.RegisterMigrations(coordinator); err != nil {
		databases.Close()
		t.Fatalf("RegisterDataPolicyMigrations: %v", err)
	}
	if err := rebac.RegisterMigrations(coordinator); err != nil {
		databases.Close()
		t.Fatalf("RegisterReBACMigrations: %v", err)
	}
	if err := ontologyscope.RegisterMigrations(coordinator); err != nil {
		databases.Close()
		t.Fatalf("RegisterOntologyScopeMigrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		databases.Close()
		t.Fatalf("migration up: %v", err)
	}
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		databases.Close()
		t.Fatalf("core store: %v", err)
	}
	if _, err := core.Exec(ctx, `INSERT INTO ontology_scope_bindings (
		tenant_id, kind, source_object_type, source_object_id, authorization_tenant_id,
		read_action, manage_action, label_object_type, label_object_id, label_tenant_id,
		binding_version, state, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"tenant-1", "organization", "organization", "tenant-1", "tenant-1",
		"organization.read", "organization.manage", "", "", "",
		int64(3), "active", int64(1), int64(1)); err != nil {
		databases.Close()
		t.Fatalf("seed scope binding: %v", err)
	}
	registry := ontology.NewPlatformRegistry(core)
	if err := registry.SaveMappingRule(ctx, ontology.MappingRule{
		ID: "mission.created", TenantID: "tenant-1", EventType: "mission.created", SourceDomain: "mission",
		SourceRef: "mission.events", EventSchemaVersion: "v1", Rule: map[string]any{"objects": []any{}}, Enabled: true,
	}); err != nil {
		databases.Close()
		t.Fatalf("SaveMappingRule: %v", err)
	}
	resolver := &promotionCommandResolver{bindings: map[string]ontologyscope.ScopeBinding{
		"tenant-1": {
			TenantID: "tenant-1", Kind: ontologyscope.KindOrganization,
			SourceObject: identity.ObjectRef{Type: "organization", ID: "tenant-1"}, AuthorizationTenantID: "tenant-1",
			ReadAction: "organization.read", ManageAction: "organization.manage", BindingVersion: 3, State: ontologyscope.BindingStateActive,
			PersistedBindingVersion: 3, PersistenceRequired: true,
		},
		"tenant-2": {
			TenantID: "tenant-2", Kind: ontologyscope.KindOrganization,
			SourceObject: identity.ObjectRef{Type: "organization", ID: "tenant-2"}, AuthorizationTenantID: "tenant-2",
			ReadAction: "organization.read", ManageAction: "organization.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
		},
		"system": ontologyscope.SystemBinding(),
	}}
	authorizer := &promotionCommandAuthorizer{deniedTenants: map[string]bool{}}
	snapshots := &promotionCommandSnapshots{authorizationRevision: "authorization:0", policyRevision: "policy:0"}
	gate := ontologyscope.NewScopeAccessGate(resolver, authorizer, snapshots)
	now := time.Unix(1000, 0).UTC()
	fixture := &promotionCommandFixture{
		databases: databases, core: core, registry: registry,
		proposals: ontology.NewPlatformMetadataProposalStore(core), plans: ontology.NewPlatformMetadataPromotionPlanStore(core),
		receipts: ontology.NewPlatformMetadataProposalPromotionReceiptStore(core), resolver: resolver, authorizer: authorizer, snapshots: snapshots,
		policy: &promotionCommandPolicy{},
		now:    now,
	}
	var planSequence int
	service, err := NewCommandService(CommandServiceOptions{
		Gate: gate, Proposals: fixture.proposals, Registry: registry, Plans: fixture.plans,
		UnitOfWork: ontology.NewPlatformMetadataPromotionUnitOfWork(core),
		Policy:     fixture.policy,
		Now:        func() time.Time { return fixture.now },
		NewPlanID: func() (string, error) {
			planSequence++
			return fmt.Sprintf("opplan_test_%d", planSequence), nil
		},
	})
	if err != nil {
		databases.Close()
		t.Fatalf("NewCommandService: %v", err)
	}
	fixture.service = service
	return fixture
}

func (f *promotionCommandFixture) close() {
	if f != nil && f.databases != nil {
		_ = f.databases.Close()
	}
}

func (f *promotionCommandFixture) saveAcceptedMappingProposal(t *testing.T, id, proposedValue string) ontology.OntologyMetadataProposal {
	t.Helper()
	proposal := ontology.OntologyMetadataProposal{
		ID: id, TenantID: "tenant-1", TargetKind: "mapping_rule", TargetID: "mission.created",
		MetadataKey: "source_domain", ProposedValue: proposedValue, Source: "operator_suggested.admin",
		SourceKind: ontology.MetadataProposalSourceOperatorSuggested, Confidence: 1, Status: ontology.MetadataProposalStatusAccepted,
	}
	if err := f.proposals.SaveMetadataProposal(context.Background(), proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	stored, err := f.proposals.GetMetadataProposalForTenant(context.Background(), "tenant-1", id)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	return stored
}

func (f *promotionCommandFixture) saveProposedMappingProposal(t *testing.T, id, proposedValue string) ontology.OntologyMetadataProposal {
	t.Helper()
	proposal := ontology.OntologyMetadataProposal{
		ID: id, TenantID: "tenant-1", TargetKind: "mapping_rule", TargetID: "mission.created",
		MetadataKey: "source_domain", ProposedValue: proposedValue, Source: "operator_suggested.admin",
		SourceKind: ontology.MetadataProposalSourceOperatorSuggested, Confidence: 1, Status: ontology.MetadataProposalStatusProposed,
	}
	if err := f.proposals.SaveMetadataProposal(context.Background(), proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	stored, err := f.proposals.GetMetadataProposalForTenant(context.Background(), "tenant-1", id)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	return stored
}

func (f *promotionCommandFixture) mappingRule(t *testing.T) ontology.MappingRule {
	t.Helper()
	rules, err := f.registry.ListMappingRules(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	for _, rule := range rules {
		if rule.ID == "mission.created" {
			return rule
		}
	}
	t.Fatal("mission.created mapping rule not found")
	return ontology.MappingRule{}
}

type promotionCommandResolver struct {
	mu       sync.RWMutex
	bindings map[string]ontologyscope.ScopeBinding
}

func (r *promotionCommandResolver) Resolve(_ context.Context, tenantID string) (ontologyscope.ScopeBinding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, exists := r.bindings[strings.TrimSpace(tenantID)]
	if !exists {
		return ontologyscope.ScopeBinding{}, ontologyscope.ErrBindingNotFound
	}
	return binding, nil
}

type promotionCommandAuthorizer struct {
	mu            sync.RWMutex
	deniedTenants map[string]bool
}

func (a *promotionCommandAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	return nil
}

func (a *promotionCommandAuthorizer) AuthorizeObject(_ context.Context, _ identity.Principal, _ string, _ identity.ObjectRef, tenantID string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.deniedTenants[strings.TrimSpace(tenantID)] {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

type promotionCommandSnapshots struct {
	mu                    sync.RWMutex
	authorizationRevision string
	policyRevision        string
}

func (s *promotionCommandSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: s.authorizationRevision, PolicyRevision: s.policyRevision,
	}, nil
}

type promotionCommandPolicy struct {
	mu     sync.RWMutex
	denied bool
}

func (p *promotionCommandPolicy) Authorize(_ context.Context, _ identity.Principal, _ CommandPolicyRequest) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.denied {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

func promotionCommandPrincipal(id string) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: id, UserID: id, ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.manage"},
	})
}

type failingPlanTransitionUnitOfWork struct {
	inner ontology.MetadataPromotionUnitOfWork
}

type transactionHookUnitOfWork struct {
	inner      ontology.MetadataPromotionUnitOfWork
	hook       func()
	afterGuard func()
}

func (u transactionHookUnitOfWork) WithinMetadataPromotionTransaction(ctx context.Context, guard ontology.MetadataPromotionCommitGuard, fn func(ontology.MetadataPromotionTransactionStores) error) error {
	return u.inner.WithinMetadataPromotionTransaction(ctx, func(guardCtx context.Context) error {
		if u.hook != nil {
			u.hook()
		}
		if guard == nil {
			if u.afterGuard != nil {
				u.afterGuard()
			}
			return nil
		}
		if err := guard(guardCtx); err != nil {
			return err
		}
		if u.afterGuard != nil {
			u.afterGuard()
		}
		return nil
	}, fn)
}

func (u failingPlanTransitionUnitOfWork) WithinMetadataPromotionTransaction(ctx context.Context, guard ontology.MetadataPromotionCommitGuard, fn func(ontology.MetadataPromotionTransactionStores) error) error {
	return u.inner.WithinMetadataPromotionTransaction(ctx, guard, func(stores ontology.MetadataPromotionTransactionStores) error {
		stores.Plans = failingPlanTransitionStore{MetadataPromotionPlanStore: stores.Plans}
		return fn(stores)
	})
}

type failingPlanTransitionStore struct {
	ontology.MetadataPromotionPlanStore
}

func (s failingPlanTransitionStore) TransitionMetadataPromotionPlan(ctx context.Context, transition ontology.MetadataPromotionPlanTransition) (ontology.MetadataPromotionPlanRecord, error) {
	if transition.State == ontology.MetadataPromotionPlanConsumed {
		return ontology.MetadataPromotionPlanRecord{}, errors.New("forced plan transition failure")
	}
	return s.MetadataPromotionPlanStore.TransitionMetadataPromotionPlan(ctx, transition)
}
