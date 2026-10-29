package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

type recordingRetiredPrincipalGraphState struct {
	deleted    []string
	reassigned []string
}

func (s *recordingRetiredPrincipalGraphState) DeleteTenant(_ context.Context, tenantID string) (int, error) {
	s.deleted = append(s.deleted, tenantID)
	return 1, nil
}

func (s *recordingRetiredPrincipalGraphState) ReassignTenant(_ context.Context, fromID, toID string) (int, error) {
	s.reassigned = append(s.reassigned, fromID+"->"+toID)
	return 1, nil
}

func TestReconcileRetiredPrincipalOntologyTenantsDeletesOnlyLegacyHumanPrincipals(t *testing.T) {
	state := &recordingRetiredPrincipalGraphState{}
	if err := reconcileRetiredPrincipalOntologyTenants(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	want := []string{
		agentcore.LegacySuperadminAgentProfileID,
		agentcore.LegacyLocalAdminAgentProfileID,
		agentcore.LegacyLocalUserAgentProfileID,
	}
	if len(state.deleted) != len(want) {
		t.Fatalf("deleted tenants = %+v", state.deleted)
	}
	for index := range want {
		if state.deleted[index] != want[index] {
			t.Fatalf("deleted tenants = %+v, want %+v", state.deleted, want)
		}
	}
}

func TestReconcileLegacyBusinessGraphAliasesNormalizesTenantCase(t *testing.T) {
	state := &recordingRetiredPrincipalGraphState{}
	if err := reconcileLegacyBusinessGraphAliases(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	want := legacyBusinessGraphTenantCaseAlias + "->" + defaultBusinessOrganizationID
	if len(state.reassigned) != 1 || state.reassigned[0] != want {
		t.Fatalf("reassignments = %+v, want %s", state.reassigned, want)
	}
}
