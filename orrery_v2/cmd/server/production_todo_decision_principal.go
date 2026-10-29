package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/identity"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/app/userauth"
)

type productionTodoDecisionAccountStore interface {
	GetAccountByUsername(context.Context, string) (userauth.Account, error)
}

type productionTodoPermissionScopeResolver interface {
	ResolvePermissionScopes(context.Context, identity.Principal, []string) ([]string, error)
}

type productionTodoDecisionPrincipalResolver struct {
	accounts productionTodoDecisionAccountStore
	scopes   productionTodoPermissionScopeResolver
}

func (r productionTodoDecisionPrincipalResolver) ResolveTodoDecisionPrincipal(ctx context.Context, persisted todoapp.ViewerRef) (todoapp.ViewerRef, error) {
	if r.accounts == nil || r.scopes == nil {
		return todoapp.ViewerRef{}, todoapp.ErrServiceDependency
	}
	username := strings.TrimSpace(persisted.ViewerID)
	subjectID := strings.TrimSpace(persisted.AuthorizationSubjectID)
	if persisted.ViewerKind != todoapp.ViewerKindUser || username == "" || subjectID == "" {
		return todoapp.ViewerRef{}, todoapp.ErrConflict
	}
	account, err := r.accounts.GetAccountByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, userauth.ErrAccountNotFound) || errors.Is(err, userauth.ErrAccountConflict) {
			return todoapp.ViewerRef{}, todoapp.ErrForbidden
		}
		return todoapp.ViewerRef{}, fmt.Errorf("resolve Todo approval account: %w", err)
	}
	if !account.Enabled || !todoDecisionAccountMatches(account, username, subjectID) {
		return todoapp.ViewerRef{}, todoapp.ErrForbidden
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         subjectID,
		UserID:          account.UserID,
		Username:        account.Username,
		AgentProfileID:  account.AgentProfileID,
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        persisted.TenantID,
	})
	scopes, err := r.scopes.ResolvePermissionScopes(ctx, principal, account.PermissionScope)
	if err != nil {
		return todoapp.ViewerRef{}, fmt.Errorf("resolve current Todo approval permissions: %w", err)
	}
	persisted.DisplayName = strings.TrimSpace(account.DisplayName)
	persisted.PermissionScope = append([]string(nil), scopes...)
	return persisted, nil
}

func todoDecisionAccountMatches(account userauth.Account, username string, subjectID string) bool {
	if !strings.EqualFold(strings.TrimSpace(account.Username), username) && !strings.EqualFold(strings.TrimSpace(account.UserID), username) {
		return false
	}
	for _, candidate := range []string{account.AgentProfileID, account.UserID, account.Username} {
		if strings.EqualFold(strings.TrimSpace(candidate), subjectID) {
			return true
		}
	}
	return false
}
