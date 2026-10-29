package operations_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestServiceHireTalentFromMarketRejectsDuplicateInternalHumanUsernameWithoutMutation(t *testing.T) {
	ctx := context.Background()
	marketProfile := agentcore.AgentProfile{
		ID:          "profile-market-human",
		DisplayName: "Market Operator",
		AgentKind:   agentcore.AgentKindHuman,
		Placement:   agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
		OrgIdentity: agentcore.AgentOrgIdentity{EmploymentType: agentcore.EmploymentTypeHuman},
		UserAccount: agentcore.UserAccountSpec{
			Username:       " Operator ",
			AccountEnabled: true,
		},
		Enabled: true,
	}
	marketAvailability := agentcore.AgentAvailability{
		AgentProfileID:  marketProfile.ID,
		AgentInstanceID: "profile-market-human:runtime",
		Status:          agentcore.AvailabilityDisabled,
	}
	store := &fakeWorkforceProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			marketProfile.ID: marketProfile,
			"profile-internal-human": {
				ID:        "profile-internal-human",
				AgentKind: agentcore.AgentKindHuman,
				Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal},
				OrgIdentity: agentcore.AgentOrgIdentity{
					OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
					DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
					PositionRef:     agentcore.PositionRef{ID: "operator"},
					EmploymentType:  agentcore.EmploymentTypeHuman,
				},
				UserAccount: agentcore.UserAccountSpec{
					Username:       "operator",
					AccountEnabled: true,
				},
				Enabled: true,
			},
		},
		availability: map[agentcore.AgentProfileID]agentcore.AgentAvailability{
			marketProfile.ID: marketAvailability,
		},
	}
	revoker := &fakeWorkforceSessionRevoker{}
	audit := &fakeOperationsAuditRecorder{}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(revoker)
	service.SetAuditRecorder(audit)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{
		DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations",
	}}, []operations.PositionDTO{{
		PositionID: "operator", DepartmentID: "operations", Name: "Operator",
	}})
	service.SetOrganizationCatalogStore(orgs)

	_, err := service.HireTalentFromMarket(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceManage},
	}, operations.HireTalentFromMarketCommand{
		AgentProfileID: marketProfile.ID.String(),
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("HireTalentFromMarket error = %v, want ErrInvalidRequest", err)
	}
	if got := store.profiles[marketProfile.ID]; !reflect.DeepEqual(got, marketProfile) {
		t.Fatalf("market profile mutated after rejected hire: got=%+v want=%+v", got, marketProfile)
	}
	if got := store.availability[marketProfile.ID]; got != marketAvailability {
		t.Fatalf("market availability mutated after rejected hire: got=%+v want=%+v", got, marketAvailability)
	}
	if len(revoker.usernames) != 0 {
		t.Fatalf("sessions revoked after rejected hire: %+v", revoker.usernames)
	}
	if len(audit.events) != 0 {
		t.Fatalf("audit events recorded after rejected hire: %+v", audit.events)
	}
}
