package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type productionGatewayAccountLookup interface {
	GetAccountByAgentProfileID(context.Context, string) (userauth.Account, error)
}

type productionGatewayPermissionScopeResolver interface {
	ResolvePermissionScopes(context.Context, identity.Principal, []string) ([]string, error)
}

func (r *productionMessageGatewayRunner) SetViewerAuthorization(accounts productionGatewayAccountLookup, scopes productionGatewayPermissionScopeResolver) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts = accounts
	r.scopes = scopes
}

func (r *productionMessageGatewayRunner) authorizedGatewayViewer(ctx context.Context, input gwprotocol.SubmitTurnInput, route productionGatewayRoute) (discussionsession.ViewerRef, error) {
	viewer := productionGatewayViewer(input, route)
	if r == nil || strings.TrimSpace(route.ViewerID) == "" {
		return viewer, nil
	}
	r.mu.Lock()
	accounts := r.accounts
	scopes := r.scopes
	r.mu.Unlock()
	if accounts == nil && scopes == nil {
		return viewer, nil
	}
	if accounts == nil || scopes == nil {
		return discussionsession.ViewerRef{}, fmt.Errorf("gateway viewer authorization is incomplete")
	}
	account, err := accounts.GetAccountByAgentProfileID(ctx, viewer.ViewerID)
	if err != nil {
		return discussionsession.ViewerRef{}, fmt.Errorf("resolve gateway viewer account: %w", err)
	}
	if !account.Enabled || !strings.EqualFold(strings.TrimSpace(account.AgentProfileID), strings.TrimSpace(viewer.ViewerID)) {
		return discussionsession.ViewerRef{}, fmt.Errorf("gateway viewer account is unavailable")
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         firstNonBlank(account.AgentProfileID, account.UserID, account.Username),
		UserID:          account.UserID,
		Username:        account.Username,
		DisplayName:     account.DisplayName,
		AgentProfileID:  account.AgentProfileID,
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        viewer.TenantID,
	})
	permissionScope, err := scopes.ResolvePermissionScopes(ctx, principal, account.PermissionScope)
	if err != nil {
		return discussionsession.ViewerRef{}, fmt.Errorf("resolve gateway viewer permissions: %w", err)
	}
	viewer.PermissionScope = append([]string(nil), permissionScope...)
	viewer.IdentityIDs = []string{account.AgentProfileID, account.UserID, account.Username}
	return viewer, nil
}
