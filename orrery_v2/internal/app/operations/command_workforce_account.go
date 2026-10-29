package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
)

func (s *CommandService) applyUserAccountCommand(profile *agentcore.AgentProfile, cmd UpsertWorkforceMemberCommand) error {
	if profile == nil {
		return nil
	}
	account := cmd.UserAccount
	partial := cmd.partialUpdate()
	if partial && cmd.updatesField("user_account.permission_scope") {
		return fmt.Errorf("%w: user_account.permission_scope is read-only; assign role_ids instead", ErrInvalidRequest)
	}
	if !partial && len(cleanPermissionScopes(account.PermissionScope)) > 0 {
		return fmt.Errorf("%w: user_account.permission_scope is read-only; assign role_ids instead", ErrInvalidRequest)
	}
	accountTouched := !partial || cmd.updatesPrefix("user_account")
	username := strings.TrimSpace(account.Username)
	if username != "" && userauth.IsReservedWorkforceUsername(username) {
		return fmt.Errorf("%w: username is reserved for a non-workforce principal", ErrInvalidRequest)
	}
	nickname := strings.TrimSpace(account.Nickname)
	email := strings.TrimSpace(account.Email)
	role := strings.TrimSpace(account.Role)
	password := account.Password
	if s != nil && s.peopleSSOEnabled && (password != "" || (partial && cmd.updatesField("user_account.password"))) {
		return fmt.Errorf("%w: ordinary People credentials are managed by Kratos", ErrInvalidRequest)
	}
	hasAccountInput := username != "" || nickname != "" || email != "" || role != "" || account.AccountEnabled || password != ""
	if partial && !accountTouched {
		if profile.AgentKind != agentcore.AgentKindHuman {
			profile.UserAccount = agentcore.UserAccountSpec{}
		}
		return nil
	}
	if !partial && !hasAccountInput {
		if profile.AgentKind != agentcore.AgentKindHuman {
			profile.UserAccount = agentcore.UserAccountSpec{}
		}
		return nil
	}
	if profile.AgentKind != agentcore.AgentKindHuman {
		return fmt.Errorf("%w: user_account is only supported for human workforce members", ErrInvalidRequest)
	}
	existing := profile.UserAccount
	if partial {
		updated := existing
		if cmd.updatesField("user_account.username") {
			updated.Username = username
		}
		if cmd.updatesField("user_account.nickname") {
			updated.Nickname = nickname
		}
		if cmd.updatesField("user_account.email") {
			updated.Email = email
		}
		if cmd.updatesField("user_account.role") {
			updated.Role = role
		}
		if cmd.updatesField("user_account.account_enabled") {
			updated.AccountEnabled = account.AccountEnabled
		}
		profile.UserAccount = updated
		if cmd.updatesField("user_account.password") && password != "" {
			if err := setUserAccountPassword(&profile.UserAccount, password); err != nil {
				return err
			}
		}
		if strings.TrimSpace(profile.DisplayName) == "" && strings.TrimSpace(profile.UserAccount.Username) != "" {
			profile.DisplayName = profile.UserAccount.Username
		}
		return nil
	}
	profile.UserAccount = agentcore.UserAccountSpec{
		Username:         username,
		Nickname:         firstNonEmpty(nickname, profile.DisplayName, profile.RealName),
		Email:            email,
		Role:             firstNonEmpty(role, existing.Role),
		AccountEnabled:   account.AccountEnabled,
		PermissionScope:  existing.PermissionScope,
		PasswordSalt:     existing.PasswordSalt,
		PasswordVerifier: existing.PasswordVerifier,
	}
	if password != "" {
		if err := setUserAccountPassword(&profile.UserAccount, password); err != nil {
			return err
		}
	}
	if strings.TrimSpace(profile.DisplayName) == "" && strings.TrimSpace(profile.UserAccount.Username) != "" {
		profile.DisplayName = profile.UserAccount.Username
	}
	return nil
}

func (s *CommandService) validateWorkforceUserAccount(profile agentcore.AgentProfile) error {
	if profile.AgentKind != agentcore.AgentKindHuman || !profile.UserAccount.AccountEnabled {
		return nil
	}
	if strings.TrimSpace(profile.UserAccount.Username) == "" {
		return fmt.Errorf("%w: enabled human user accounts require a username", ErrInvalidRequest)
	}
	requiresLocalPassword := s == nil || !s.peopleSSOEnabled
	if requiresLocalPassword && (strings.TrimSpace(profile.UserAccount.PasswordSalt) == "" || strings.TrimSpace(profile.UserAccount.PasswordVerifier) == "") {
		return fmt.Errorf("%w: enabled human user accounts require a password", ErrInvalidRequest)
	}
	return nil
}

func (s *CommandService) authorizeWorkforceAccountSecurityChanges(ctx context.Context, viewer ViewerRef, previous *agentcore.AgentProfile, current agentcore.AgentProfile) error {
	if err := validateWorkforceBreakGlassInvariant(current); err != nil {
		return err
	}
	if !workforceAccountSecurityChanged(previous, current) {
		return nil
	}
	if err := requireOperationsPermission(viewer, PermissionPermissionsManage); err != nil {
		return err
	}
	return s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsManage)
}

func validateWorkforceBreakGlassInvariant(profile agentcore.AgentProfile) error {
	profileID := strings.TrimSpace(profile.ID.String())
	if userauth.IsReservedWorkforceProfileID(profileID) || userauth.IsReservedWorkforceUsername(profile.UserAccount.Username) {
		return fmt.Errorf("%w: system and legacy placeholder identities cannot be workforce members", ErrInvalidRequest)
	}
	return nil
}

func workforceAccountSecurityChanged(previous *agentcore.AgentProfile, current agentcore.AgentProfile) bool {
	currentHasAccount := workforceProfileHasLoginAccount(current)
	if previous == nil {
		return currentHasAccount
	}
	previousHasAccount := workforceProfileHasLoginAccount(*previous)
	if !previousHasAccount && !currentHasAccount {
		return false
	}
	return previous.AgentKind != current.AgentKind ||
		previous.Enabled != current.Enabled ||
		previous.Placement.IsInternal(previous.OrgIdentity) != current.Placement.IsInternal(current.OrgIdentity) ||
		!strings.EqualFold(strings.TrimSpace(previous.UserAccount.Username), strings.TrimSpace(current.UserAccount.Username)) ||
		previous.UserAccount.AccountEnabled != current.UserAccount.AccountEnabled ||
		previous.UserAccount.PasswordSalt != current.UserAccount.PasswordSalt ||
		previous.UserAccount.PasswordVerifier != current.UserAccount.PasswordVerifier
}

func workforceProfileHasLoginAccount(profile agentcore.AgentProfile) bool {
	return profile.AgentKind == agentcore.AgentKindHuman && (strings.TrimSpace(profile.UserAccount.Username) != "" ||
		profile.UserAccount.AccountEnabled ||
		strings.TrimSpace(profile.UserAccount.PasswordVerifier) != "")
}

func setUserAccountPassword(account *agentcore.UserAccountSpec, password string) error {
	if account == nil {
		return nil
	}
	authAccount := userauth.Account{
		Username:     account.Username,
		PasswordSalt: account.PasswordSalt,
	}
	if err := authAccount.SetPassword(password); err != nil {
		if errors.Is(err, userauth.ErrInvalidRequest) {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		return fmt.Errorf("%w: password hash failed: %v", ErrServiceDependency, err)
	}
	account.PasswordSalt = authAccount.PasswordSalt
	account.PasswordVerifier = authAccount.PasswordVerifier
	return nil
}

func (s *CommandService) validateUniqueWorkforceUsername(ctx context.Context, profile agentcore.AgentProfile) error {
	if s == nil || s.directory == nil || profile.AgentKind != agentcore.AgentKindHuman {
		return nil
	}
	username := strings.ToLower(strings.TrimSpace(profile.UserAccount.Username))
	if username == "" {
		return nil
	}
	page, err := s.directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeHumans: true})
	if err != nil {
		return err
	}
	for _, entry := range page.Entries {
		if entry.AgentProfileID == profile.ID {
			continue
		}
		candidate, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(candidate.UserAccount.Username)) == username {
			return fmt.Errorf("%w: username is already assigned to another human profile", ErrInvalidRequest)
		}
	}
	return nil
}

func workforceSessionRevocationUsernames(previous, current agentcore.AgentProfile, hadPrevious bool) []string {
	if !hadPrevious {
		return nil
	}
	previousUsername := strings.TrimSpace(previous.UserAccount.Username)
	currentUsername := strings.TrimSpace(current.UserAccount.Username)
	if previousUsername == "" && currentUsername == "" {
		return nil
	}
	profileEnabledChanged := previous.Enabled != current.Enabled
	accountEnabledChanged := previous.UserAccount.AccountEnabled != current.UserAccount.AccountEnabled
	placementChanged := previous.Placement.IsInternal(previous.OrgIdentity) != current.Placement.IsInternal(current.OrgIdentity)
	agentKindChanged := previous.AgentKind != current.AgentKind
	credentialsChanged := previous.UserAccount.PasswordSalt != current.UserAccount.PasswordSalt ||
		previous.UserAccount.PasswordVerifier != current.UserAccount.PasswordVerifier
	usernameChanged := !strings.EqualFold(previousUsername, currentUsername)
	if !profileEnabledChanged && !accountEnabledChanged && !placementChanged && !agentKindChanged && !credentialsChanged && !usernameChanged {
		return nil
	}
	return []string{previousUsername, currentUsername}
}

func (s *CommandService) revokeWorkforceSessions(ctx context.Context, usernames ...string) error {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(usernames))
	for _, username := range usernames {
		username = strings.ToLower(strings.TrimSpace(username))
		if username == "" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		normalized = append(normalized, username)
	}
	if len(normalized) == 0 {
		return nil
	}
	if s == nil || s.sessionRevoker == nil {
		return ErrServiceDependency
	}
	for _, username := range normalized {
		if err := s.sessionRevoker.RevokeSessionsForUsername(ctx, username); err != nil {
			return err
		}
	}
	return nil
}

func (s *CommandService) failClosedWorkforceLoginAfterRevocationFailure(ctx context.Context, previous, current agentcore.AgentProfile, hadPrevious bool) error {
	if s == nil || s.profiles == nil || current.AgentKind != agentcore.AgentKindHuman {
		return nil
	}
	current.Enabled = false
	current.UserAccount.AccountEnabled = false
	return s.profiles.SaveProfile(ctx, current)
}
