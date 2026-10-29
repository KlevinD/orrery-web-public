package actioncontract

import (
	"context"
	"errors"
	"testing"
)

func TestInstallDefaultActionTypesIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	service := NewService(repo, WithClock(fixedClock()))

	if err := InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("InstallDefaultActionTypes first call: %v", err)
	}
	if err := InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("InstallDefaultActionTypes second call: %v", err)
	}

	types, err := service.ListActionTypes(ctx)
	if err != nil {
		t.Fatalf("ListActionTypes: %v", err)
	}
	for _, want := range []ActionTypeID{"development.merge_pull_request", "development.open_pull_request", "iris.accept_recommendation", "legal.review_contract", "mcp.tool.call", "mission.plan", "people.assign_owner", "task.complete", "value.evaluate_initiative", "value.record_metric"} {
		if !hasActionType(types, want) {
			t.Fatalf("default action %s was not installed: %#v", want, types)
		}
	}
	for _, want := range []ActionTypeID{ActionBusinessApprovalRequest, ActionCommunicationEmailSend, ActionCommunicationWechatDraftCreate, ActionSystemCommandExecute, ActionSystemFileChange, ActionToolUnclassifiedExecute} {
		if !hasActionType(types, want) {
			t.Fatalf("canonical approval action %s was not installed", want)
		}
	}
}

func TestDefaultApprovalCapabilitiesDoNotDependOnVendorPromptText(t *testing.T) {
	capability, ok := ResolveDefaultApprovalCapability("tool", "mcp_marketing_marketing_wechat_draft_create")
	if !ok || capability.ActionTypeID != ActionCommunicationWechatDraftCreate || capability.RiskLevel != RiskHigh {
		t.Fatalf("capability = %+v, ok=%v", capability, ok)
	}
	if _, ok := ResolveDefaultApprovalCapability("tool", "vendor_unknown_write"); ok {
		t.Fatal("unknown vendor tool was recognized without a trusted registration")
	}
}

func TestHumanBusinessDecisionActionsAreExplicit(t *testing.T) {
	for _, id := range []ActionTypeID{
		ActionBusinessApprovalRequest,
		ActionCommunicationEmailSend,
		ActionCommunicationWechatDraftCreate,
	} {
		if !IsHumanBusinessDecisionAction(id) {
			t.Fatalf("%s should be a business decision", id)
		}
	}
	for _, id := range []ActionTypeID{
		ActionSystemCommandExecute,
		ActionSystemFileChange,
		ActionNetworkExternalAccess,
		ActionInformationRead,
		ActionToolExternalExecute,
		ActionToolUnclassifiedExecute,
	} {
		if IsHumanBusinessDecisionAction(id) {
			t.Fatalf("system action %s must not require a human", id)
		}
	}
}

func TestRuntimeBusinessApprovalBridgeDoesNotApproveItself(t *testing.T) {
	tests := map[string]bool{
		"request_business_approval":                    false,
		"email_send":                                   true,
		"mcp_marketing_marketing_wechat_draft_create": true,
		"enterprise_graph_search_objects":             false,
		"knowledge_add":                                false,
		"patch_apply":                                  false,
	}
	for toolName, want := range tests {
		if got := RuntimeToolRequiresHumanBusinessDecision("tool", toolName); got != want {
			t.Fatalf("RuntimeToolRequiresHumanBusinessDecision(%q) = %t, want %t", toolName, got, want)
		}
	}
}

func TestDefaultEnterpriseDomainActionsIncludeGovernanceContracts(t *testing.T) {
	for _, tc := range []struct {
		id          ActionTypeID
		category    ActionCategory
		provider    ProviderKind
		resolver    string
		scope       string
		risk        RiskLevel
		objectTypes []string
	}{
		{
			id:          "people.assign_owner",
			category:    CategoryHuman,
			provider:    ProviderHuman,
			resolver:    "person",
			scope:       "people.manage",
			risk:        RiskLow,
			objectTypes: []string{"person", "team"},
		},
		{
			id:          "legal.review_contract",
			category:    CategoryHuman,
			provider:    ProviderApproval,
			resolver:    "contract",
			scope:       "legal.manage",
			risk:        RiskHigh,
			objectTypes: []string{"contract", "policy", "approval_case"},
		},
		{
			id:          "development.open_pull_request",
			category:    CategoryAgent,
			provider:    ProviderConnector,
			resolver:    "repository",
			scope:       "development.manage",
			risk:        RiskMedium,
			objectTypes: []string{"repository", "issue", "pull_request"},
		},
		{
			id:          "development.merge_pull_request",
			category:    CategoryHybrid,
			provider:    ProviderConnector,
			resolver:    "pull_request",
			scope:       "development.manage",
			risk:        RiskHigh,
			objectTypes: []string{"repository", "pull_request", "release"},
		},
		{
			id:          "value.evaluate_initiative",
			category:    CategoryHybrid,
			provider:    ProviderInternalService,
			resolver:    "initiative",
			scope:       "value.manage",
			risk:        RiskMedium,
			objectTypes: []string{"initiative", "value_objective", "metric"},
		},
		{
			id:          "value.record_metric",
			category:    CategorySystem,
			provider:    ProviderInternalService,
			resolver:    "metric",
			scope:       "value.manage",
			risk:        RiskLow,
			objectTypes: []string{"metric", "value_objective"},
		},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			actionType, ok := DefaultActionType(tc.id)
			if !ok {
				t.Fatalf("default action %s missing", tc.id)
			}
			if actionType.Category != tc.category || actionType.ProviderKind != tc.provider {
				t.Fatalf("category/provider = %s/%s", actionType.Category, actionType.ProviderKind)
			}
			if actionType.ResourceResolver != tc.resolver || actionType.PermissionScope != tc.scope {
				t.Fatalf("resolver/scope = %q/%q", actionType.ResourceResolver, actionType.PermissionScope)
			}
			if actionType.RiskLevel != tc.risk {
				t.Fatalf("risk = %q, want %q", actionType.RiskLevel, tc.risk)
			}
			for _, objectType := range tc.objectTypes {
				if !stringSliceContains(actionType.ObjectTypeRefs, objectType) {
					t.Fatalf("object refs = %+v, want %s", actionType.ObjectTypeRefs, objectType)
				}
			}
			if actionType.IdempotencyPolicy["args_hash_required"] != true {
				t.Fatalf("idempotency policy = %#v", actionType.IdempotencyPolicy)
			}
			if actionType.AuditPolicy["record_object_ref"] != true {
				t.Fatalf("audit policy = %#v", actionType.AuditPolicy)
			}
		})
	}
}

func TestDefaultMCPToolCallActionIncludesGovernanceContract(t *testing.T) {
	actionType, ok := DefaultActionType("mcp.tool.call")
	if !ok {
		t.Fatalf("mcp.tool.call default action missing")
	}
	if actionType.Category != CategoryTool || actionType.ProviderKind != ProviderMCPTool {
		t.Fatalf("mcp action category/provider = %s/%s", actionType.Category, actionType.ProviderKind)
	}
	if actionType.ResourceResolver != "tool" || actionType.PermissionScope != "tool.call" {
		t.Fatalf("mcp action resolver/scope = %q/%q", actionType.ResourceResolver, actionType.PermissionScope)
	}
	if actionType.RiskLevel != RiskMedium {
		t.Fatalf("mcp action risk = %q, want medium", actionType.RiskLevel)
	}
	objectTypes := map[string]bool{}
	for _, objectType := range actionType.ObjectTypeRefs {
		objectTypes[objectType] = true
	}
	for _, want := range []string{"tool", "mcp_server", "agent_run", "responsibility_assignment", "responsibility_context", "responsibility_policy"} {
		if !objectTypes[want] {
			t.Fatalf("mcp action object types = %+v, missing %q", actionType.ObjectTypeRefs, want)
		}
	}
	if actionType.IdempotencyPolicy["args_hash_required"] != true {
		t.Fatalf("mcp action idempotency policy = %#v", actionType.IdempotencyPolicy)
	}
	if actionType.AuditPolicy["record_args_hash"] != true {
		t.Fatalf("mcp action audit policy = %#v", actionType.AuditPolicy)
	}
}

func TestResolverMapsRuntimeAndResourceToActionType(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	service := NewService(repo, WithClock(fixedClock()))
	if err := InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("InstallDefaultActionTypes: %v", err)
	}
	resolver := NewResolver(repo)

	cases := []struct {
		name string
		req  ResolveRequest
		want ActionTypeID
	}{
		{
			name: "mcp gateway tool call",
			req: ResolveRequest{
				Runtime:      RuntimeRef{Kind: RuntimeMCPGateway, ID: "http"},
				ProviderKind: ProviderMCPTool,
				Resource:     ObjectRef{Kind: "tool", ID: "github.search"},
			},
			want: "mcp.tool.call",
		},
		{
			name: "human task page action",
			req: ResolveRequest{
				Runtime:  RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
				Category: CategoryHuman,
				Resource: ObjectRef{Kind: "task", ID: "task-1"},
				Intent:   "complete",
			},
			want: "task.complete",
		},
		{
			name: "iris accepted action",
			req: ResolveRequest{
				Runtime:  RuntimeRef{Kind: RuntimeIRIS, ID: "decision-run-1"},
				Category: CategoryHybrid,
				Resource: ObjectRef{Kind: "mission", ID: "mission-1"},
				Intent:   "accept_recommendation",
			},
			want: "iris.accept_recommendation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actionType, err := resolver.Resolve(ctx, tc.req)
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if actionType.ID != tc.want {
				t.Fatalf("resolved action = %q, want %q", actionType.ID, tc.want)
			}
		})
	}
}

func TestResolverRejectsAmbiguousActionWithoutExplicitIntent(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	service := NewService(repo, WithClock(fixedClock()))
	if err := InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("InstallDefaultActionTypes: %v", err)
	}
	resolver := NewResolver(repo)

	_, err := resolver.Resolve(ctx, ResolveRequest{
		Runtime:  RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
		Category: CategoryHuman,
		Resource: ObjectRef{Kind: "task", ID: "task-1"},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve ambiguous request err = %v, want ErrNotFound", err)
	}
}

func hasActionType(types []ActionType, id ActionTypeID) bool {
	for _, actionType := range types {
		if actionType.ID == id {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
