package directory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxPublishedProfiles = 128
	maxPublishedGrants   = 256
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{1,159}$`)
	digestPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Options struct {
	Store           Store
	Now             func() time.Time
	DeviceConnected func(tenantID, deviceID string) bool
	DefaultTenantID string
}

type Service struct {
	store             Store
	avatarStore       PersonalAssistantAvatarStore
	notificationStore PersonalAssistantNotificationStore
	now               func() time.Time
	deviceConnected   func(string, string) bool
	defaultTenantID   string
}

func NewService(options Options) (*Service, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrDependency)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.DeviceConnected == nil {
		options.DeviceConnected = func(string, string) bool { return false }
	}
	if strings.TrimSpace(options.DefaultTenantID) == "" {
		options.DefaultTenantID = "global"
	}
	avatarStore, _ := options.Store.(PersonalAssistantAvatarStore)
	notificationStore, _ := options.Store.(PersonalAssistantNotificationStore)
	return &Service{
		store: options.Store, avatarStore: avatarStore, notificationStore: notificationStore, now: options.Now, deviceConnected: options.DeviceConnected,
		defaultTenantID: strings.TrimSpace(options.DefaultTenantID),
	}, nil
}

func (s *Service) GetPersonalAssistantNotifications(ctx context.Context, scope OwnerScope) (PersonalAssistantNotificationPreferences, error) {
	scope = s.normalizeScope(scope)
	if !validScope(scope) {
		return PersonalAssistantNotificationPreferences{}, ErrInvalidRequest
	}
	if s.notificationStore == nil {
		return PersonalAssistantNotificationPreferences{}, ErrDependency
	}
	record, exists, err := s.notificationStore.LoadPersonalAssistantNotifications(ctx, scope)
	if err != nil {
		return PersonalAssistantNotificationPreferences{}, err
	}
	if !exists {
		record = PersonalAssistantNotificationRecord{
			TenantID: scope.TenantID, OwnerID: scope.OwnerID, ApprovalNotificationsEnabled: true,
		}
	}
	return personalAssistantNotificationPreferences(record), nil
}

func (s *Service) UpdatePersonalAssistantNotifications(ctx context.Context, scope OwnerScope, input UpdatePersonalAssistantNotificationsInput) (PersonalAssistantNotificationPreferences, error) {
	scope = s.normalizeScope(scope)
	if !validScope(scope) || input.Revision < 0 {
		return PersonalAssistantNotificationPreferences{}, ErrInvalidRequest
	}
	if s.notificationStore == nil {
		return PersonalAssistantNotificationPreferences{}, ErrDependency
	}
	record, err := s.notificationStore.SavePersonalAssistantNotifications(ctx, PersonalAssistantNotificationRecord{
		TenantID: scope.TenantID, OwnerID: scope.OwnerID,
		ApprovalNotificationsEnabled: input.ApprovalNotificationsEnabled,
		UpdatedAt:                    s.now().UTC(),
	}, input.Revision)
	if err != nil {
		return PersonalAssistantNotificationPreferences{}, err
	}
	return personalAssistantNotificationPreferences(record), nil
}

func personalAssistantNotificationPreferences(record PersonalAssistantNotificationRecord) PersonalAssistantNotificationPreferences {
	view := PersonalAssistantNotificationPreferences{
		ApprovalNotificationsEnabled: record.ApprovalNotificationsEnabled,
		Channel:                      "weixin", Revision: record.Revision,
	}
	if !record.UpdatedAt.IsZero() {
		view.UpdatedAt = record.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return view
}

func (s *Service) PublishCatalog(ctx context.Context, scope OwnerScope, deviceID string, input PublishCatalogInput) error {
	scope = s.normalizeScope(scope)
	deviceID = strings.TrimSpace(deviceID)
	now := s.now().UTC()
	if !validScope(scope) || !identifierPattern.MatchString(deviceID) || !digestPattern.MatchString(strings.TrimSpace(input.CatalogDigest)) || len(input.Profiles) > maxPublishedProfiles {
		return ErrInvalidRequest
	}
	observedAt := input.ObservedAt.UTC()
	if observedAt.IsZero() || observedAt.After(now.Add(5*time.Minute)) || observedAt.Before(now.Add(-24*time.Hour)) {
		return ErrInvalidRequest
	}
	profiles := make([]OwnedAgent, 0, len(input.Profiles))
	seen := make(map[string]struct{}, len(input.Profiles))
	seenGrants := make(map[string]struct{})
	grantCount := 0
	for _, descriptor := range input.Profiles {
		descriptor.ProfileID = strings.TrimSpace(descriptor.ProfileID)
		descriptor.DisplayName = strings.TrimSpace(descriptor.DisplayName)
		descriptor.RuntimeKind = strings.ToLower(strings.TrimSpace(descriptor.RuntimeKind))
		if !identifierPattern.MatchString(descriptor.ProfileID) || descriptor.DisplayName == "" || len([]rune(descriptor.DisplayName)) > 120 || !validRuntimeKind(descriptor.RuntimeKind) {
			return ErrInvalidRequest
		}
		if _, exists := seen[descriptor.ProfileID]; exists {
			return ErrInvalidRequest
		}
		seen[descriptor.ProfileID] = struct{}{}
		capabilities, ok := normalizeCapabilities(descriptor.Capabilities)
		if !ok {
			return ErrInvalidRequest
		}
		grants := make([]WorkspaceGrantDescriptor, 0, len(descriptor.WorkspaceGrants))
		grantCount += len(descriptor.WorkspaceGrants)
		if grantCount > maxPublishedGrants {
			return ErrInvalidRequest
		}
		for _, grant := range descriptor.WorkspaceGrants {
			grant.GrantID = strings.TrimSpace(grant.GrantID)
			grant.DisplayName = strings.TrimSpace(grant.DisplayName)
			if !identifierPattern.MatchString(grant.GrantID) || grant.DisplayName == "" || len([]rune(grant.DisplayName)) > 120 {
				return ErrInvalidRequest
			}
			if _, exists := seenGrants[grant.GrantID]; exists {
				return ErrInvalidRequest
			}
			seenGrants[grant.GrantID] = struct{}{}
			grant.Capabilities, ok = normalizeCapabilities(grant.Capabilities)
			if !ok {
				return ErrInvalidRequest
			}
			grants = append(grants, grant)
		}
		sort.Slice(grants, func(i, j int) bool { return grants[i].GrantID < grants[j].GrantID })
		profiles = append(profiles, OwnedAgent{
			TenantID: scope.TenantID, OwnerID: scope.OwnerID, OwnedAgentID: ownedAgentID(scope, deviceID, descriptor.ProfileID),
			DeviceID: deviceID, LocalProfileID: descriptor.ProfileID, DisplayName: descriptor.DisplayName,
			RuntimeKind: descriptor.RuntimeKind, Capabilities: capabilities, WorkspaceGrants: grants, Available: descriptor.Available,
			CatalogDigest: strings.TrimSpace(input.CatalogDigest), ObservedAt: observedAt, UpdatedAt: now,
		})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].LocalProfileID < profiles[j].LocalProfileID })
	if err := s.store.ReplaceOwnedAgents(ctx, scope, deviceID, strings.TrimSpace(input.CatalogDigest), observedAt, profiles, now); err != nil {
		return err
	}
	return nil
}

func (s *Service) GetPersonalAssistant(ctx context.Context, scope OwnerScope) (PersonalAssistantView, error) {
	scope = s.normalizeScope(scope)
	if !validScope(scope) {
		return PersonalAssistantView{}, ErrInvalidRequest
	}
	record, exists, err := s.store.LoadHosting(ctx, scope)
	if err != nil {
		return PersonalAssistantView{}, err
	}
	if !exists {
		record = HostingRecord{TenantID: scope.TenantID, OwnerID: scope.OwnerID, AssistantEnabled: true, HostingMode: HostingModeManaged}
	}
	agents, err := s.store.ListOwnedAgents(ctx, scope)
	if err != nil {
		return PersonalAssistantView{}, err
	}
	view := s.view(record, agents)
	if s.avatarStore != nil {
		info, avatarErr := s.avatarStore.LoadPersonalAssistantAvatarInfo(ctx, scope)
		if avatarErr == nil {
			view.AvatarETag = info.ETag
		} else if !errors.Is(avatarErr, ErrNotFound) {
			return PersonalAssistantView{}, avatarErr
		}
	}
	return view, nil
}

func (s *Service) UpdatePersonalAssistant(ctx context.Context, scope OwnerScope, input UpdatePersonalAssistantInput) (PersonalAssistantView, error) {
	scope = s.normalizeScope(scope)
	input.HostingMode = strings.ToLower(strings.TrimSpace(input.HostingMode))
	input.AssignedAgentID = strings.TrimSpace(input.AssignedAgentID)
	input.OwnedAgentID = strings.TrimSpace(input.OwnedAgentID)
	input.WorkspaceGrantID = strings.TrimSpace(input.WorkspaceGrantID)
	if !validScope(scope) || input.Revision < 0 || (input.AssignedAgentID != "" && !identifierPattern.MatchString(input.AssignedAgentID)) || (input.HostingMode != HostingModeManaged && input.HostingMode != HostingModeOwnedAgent) {
		return PersonalAssistantView{}, ErrInvalidRequest
	}
	agents, err := s.store.ListOwnedAgents(ctx, scope)
	if err != nil {
		return PersonalAssistantView{}, err
	}
	if input.HostingMode == HostingModeManaged {
		input.OwnedAgentID = ""
		input.WorkspaceGrantID = ""
		input.FallbackToManaged = false
	} else {
		// Local execution is a confidentiality boundary. Never move a request to
		// the cloud without a new, explicit user choice.
		input.FallbackToManaged = false
		selected := false
		for _, agent := range agents {
			if agent.OwnedAgentID != input.OwnedAgentID || !agent.RevokedAt.IsZero() || !agent.Available || agent.RuntimeKind == RuntimeKindOrreryTools {
				continue
			}
			for _, grant := range agent.WorkspaceGrants {
				if grant.GrantID == input.WorkspaceGrantID && grant.Available {
					selected = true
					break
				}
			}
			break
		}
		if !selected {
			return PersonalAssistantView{}, ErrNotFound
		}
	}
	record, err := s.store.SaveHosting(ctx, HostingRecord{
		TenantID: scope.TenantID, OwnerID: scope.OwnerID, AssistantEnabled: true,
		AssignedAgentID: input.AssignedAgentID,
		HostingMode:     input.HostingMode, OwnedAgentID: input.OwnedAgentID, WorkspaceGrantID: input.WorkspaceGrantID,
		FallbackToManaged: input.FallbackToManaged, UpdatedAt: s.now().UTC(),
	}, input.Revision)
	if err != nil {
		return PersonalAssistantView{}, err
	}
	return s.view(record, agents), nil
}

func (s *Service) view(record HostingRecord, agents []OwnedAgent) PersonalAssistantView {
	views := make([]OwnedAgentView, 0, len(agents))
	for _, agent := range agents {
		status := AgentStatusOffline
		switch {
		case !agent.RevokedAt.IsZero():
			status = AgentStatusRevoked
		case !agent.Available:
			status = AgentStatusUnavailable
		case s.deviceConnected(agent.TenantID, agent.DeviceID):
			status = AgentStatusOnline
		}
		views = append(views, OwnedAgentView{
			OwnedAgentID: agent.OwnedAgentID, DeviceID: agent.DeviceID, DeviceName: agent.DeviceName,
			DisplayName: agent.DisplayName, RuntimeKind: agent.RuntimeKind,
			Capabilities: append([]string(nil), agent.Capabilities...), WorkspaceGrants: visibleWorkspaceGrants(agent), Status: status,
			LastSeenAt: agent.ObservedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	view := PersonalAssistantView{
		AssistantEnabled: true, HostingMode: record.HostingMode,
		AssignedAgentID: record.AssignedAgentID,
		OwnedAgentID:    record.OwnedAgentID, WorkspaceGrantID: record.WorkspaceGrantID, FallbackToManaged: false,
		Revision: record.Revision, ExecutionIdentity: "user", CloudFallback: false, OwnedAgents: views,
	}
	if !record.UpdatedAt.IsZero() {
		view.UpdatedAt = record.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return view
}

func (s *Service) GetPersonalAssistantAvatar(ctx context.Context, scope OwnerScope) (PersonalAssistantAvatar, error) {
	scope = s.normalizeScope(scope)
	if !validScope(scope) {
		return PersonalAssistantAvatar{}, ErrInvalidRequest
	}
	if s.avatarStore == nil {
		return PersonalAssistantAvatar{}, ErrDependency
	}
	return s.avatarStore.LoadPersonalAssistantAvatar(ctx, scope)
}

func (s *Service) SavePersonalAssistantAvatar(ctx context.Context, scope OwnerScope, contentType string, content []byte) (PersonalAssistantAvatarInfo, error) {
	scope = s.normalizeScope(scope)
	contentType = strings.TrimSpace(contentType)
	if !validScope(scope) || contentType == "" || len(content) == 0 {
		return PersonalAssistantAvatarInfo{}, ErrInvalidRequest
	}
	if s.avatarStore == nil {
		return PersonalAssistantAvatarInfo{}, ErrDependency
	}
	digest := sha256.Sum256(content)
	return s.avatarStore.SavePersonalAssistantAvatar(ctx, scope, contentType, append([]byte(nil), content...), hex.EncodeToString(digest[:]), s.now().UTC())
}

func (s *Service) DeletePersonalAssistantAvatar(ctx context.Context, scope OwnerScope) error {
	scope = s.normalizeScope(scope)
	if !validScope(scope) {
		return ErrInvalidRequest
	}
	if s.avatarStore == nil {
		return ErrDependency
	}
	return s.avatarStore.DeletePersonalAssistantAvatar(ctx, scope)
}

func visibleWorkspaceGrants(agent OwnedAgent) []WorkspaceGrantDescriptor {
	grants := make([]WorkspaceGrantDescriptor, 0, len(agent.WorkspaceGrants))
	for _, grant := range agent.WorkspaceGrants {
		grant.Capabilities = append([]string(nil), grant.Capabilities...)
		grant.Available = grant.Available && agent.Available && agent.RevokedAt.IsZero()
		grants = append(grants, grant)
	}
	return grants
}

func normalizeScope(scope OwnerScope) OwnerScope {
	return OwnerScope{TenantID: strings.TrimSpace(scope.TenantID), OwnerID: strings.TrimSpace(scope.OwnerID)}
}

func (s *Service) normalizeScope(scope OwnerScope) OwnerScope {
	scope = normalizeScope(scope)
	if scope.TenantID == "" {
		scope.TenantID = s.defaultTenantID
	}
	return scope
}

func validScope(scope OwnerScope) bool {
	return identifierPattern.MatchString(scope.TenantID) && identifierPattern.MatchString(scope.OwnerID)
}

func validRuntimeKind(value string) bool {
	switch value {
	case RuntimeKindOrreryTools, RuntimeKindOpenClaw, RuntimeKindHermes, RuntimeKindCodex, RuntimeKindPI, RuntimeKindAionRS, RuntimeKindACP, RuntimeKindOther:
		return true
	default:
		return false
	}
}

func normalizeCapabilities(values []string) ([]string, bool) {
	if len(values) > 16 {
		return nil, false
	}
	allowed := map[string]bool{
		"workspace.read": true, "workspace.write": true, "process.shell": true, "network.access": true,
		"artifact.patch": true, "artifact.summary": true, "artifact.selected": true,
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowed[value] {
			return nil, false
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, true
}

func ownedAgentID(scope OwnerScope, deviceID, profileID string) string {
	digest := sha256.Sum256([]byte(scope.TenantID + "\x00" + scope.OwnerID + "\x00" + deviceID + "\x00" + profileID))
	return "owned_" + hex.EncodeToString(digest[:12])
}
