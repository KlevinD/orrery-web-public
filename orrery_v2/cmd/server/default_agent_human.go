package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
)

type productionHumanProfileBootstrap struct {
	profiles            *agentcore.PlatformProfileRepository
	knowledge           defaultKnowledgeEnsurer
	knowledgeVectorRoot string
}

func newProductionHumanProfileBootstrap(profiles *agentcore.PlatformProfileRepository) productionHumanProfileBootstrap {
	return productionHumanProfileBootstrap{profiles: profiles}
}

func (b productionHumanProfileBootstrap) WithKnowledgeEnsurer(knowledge defaultKnowledgeEnsurer, vectorRoot string) productionHumanProfileBootstrap {
	b.knowledge = knowledge
	b.knowledgeVectorRoot = vectorRoot
	return b
}

func (b productionHumanProfileBootstrap) EnsureHumanProfile(ctx context.Context, cmd missioncontrol.EnsureHumanProfileCommand) (agentcore.AgentProfile, error) {
	humanID := strings.TrimSpace(cmd.HumanAgentID)
	if humanID == "" {
		return agentcore.AgentProfile{}, operations.ErrInvalidRequest
	}
	profileID := agentcore.AgentProfileID(humanID)
	profile, err := b.lookupEnabledHumanProfile(ctx, profileID)
	if err == nil || !errors.Is(err, agentcore.ErrNotFound) {
		return profile, err
	}
	legacyProfileID := humanAgentProfileID(humanID)
	if legacyProfileID == profileID {
		return agentcore.AgentProfile{}, err
	}
	return b.lookupEnabledHumanProfile(ctx, legacyProfileID)
}

func (b productionHumanProfileBootstrap) EnsureViewerProfile(ctx context.Context, viewer operations.ViewerRef) (agentcore.AgentProfile, error) {
	profileID := agentcore.AgentProfileID(strings.TrimSpace(viewer.AgentProfileID))
	if profileID.IsZero() {
		return agentcore.AgentProfile{}, fmt.Errorf("viewer requires a canonical human profile")
	}
	return b.lookupEnabledHumanProfile(ctx, profileID)
}

func (b productionHumanProfileBootstrap) EnsureDiscussionHumanProfile(ctx context.Context, viewer discussionsession.ViewerRef) (agentcore.AgentProfile, error) {
	viewerID := strings.TrimSpace(viewer.ViewerID)
	displayName := firstNonBlank(viewer.DisplayName, viewerID)
	if viewerID == "" || displayName == "" {
		return agentcore.AgentProfile{}, discussionsession.ErrConversationMember
	}
	if strings.HasPrefix(strings.ToLower(viewerID), "gateway-website-") {
		return defaultHumanAgentProfile(humanAgentProfileID(viewerID), displayName), nil
	}
	profileID := agentcore.AgentProfileID(strings.TrimSpace(viewer.AgentProfileID))
	if profileID.IsZero() {
		return agentcore.AgentProfile{}, fmt.Errorf("discussion viewer requires a canonical human profile")
	}
	return b.lookupEnabledHumanProfile(ctx, profileID)
}

func (b productionHumanProfileBootstrap) lookupEnabledHumanProfile(ctx context.Context, profileID agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if b.profiles == nil {
		return agentcore.AgentProfile{}, fmt.Errorf("human profile bootstrap requires agent profiles")
	}
	if userauth.IsReservedWorkforceProfileID(profileID.String()) {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	profile, err := b.profiles.GetProfile(ctx, profileID)
	if err != nil {
		return agentcore.AgentProfile{}, err
	}
	if profile.AgentKind != agentcore.AgentKindHuman || !profile.Enabled {
		return agentcore.AgentProfile{}, fmt.Errorf("agent profile %s is not an enabled human", profileID)
	}
	return profile, nil
}

func defaultHumanAgentProfile(profileID agentcore.AgentProfileID, displayName string) agentcore.AgentProfile {
	displayName = strings.TrimSpace(displayName)
	return agentcore.AgentProfile{
		ID:          profileID,
		RealName:    displayName,
		DisplayName: displayName,
		RoleName:    "Real Human Contributor",
		AgentKind:   agentcore.AgentKindHuman,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: agentcore.OrganizationID(defaultHumanOrganizationID)},
			DepartmentRef:     agentcore.DepartmentRef{ID: agentcore.DepartmentID(defaultHumanDepartmentID)},
			PositionRef:       agentcore.PositionRef{ID: agentcore.PositionID(defaultHumanPositionID)},
			RoleRefs:          []agentcore.RoleRef{{ID: "realhumancontributor"}},
			EmploymentType:    agentcore.EmploymentTypeHuman,
			SupervisionPolicy: agentcore.SupervisionPolicySupervised,
			AuthorityScope:    "mission_collaboration",
		},
		Persona: agentcore.AgentPersona{
			Backstory:              "Real human participant represented by the first user message in a discussion.",
			ResponsibilityBoundary: "Provide original intent, clarification, review, and collaboration context for the Mission.",
			BehaviorBoundary:       "Participates through MissionTeam assignment, staffing records, discussion membership, and audit trails.",
		},
		BaseAgent: defaultBaseAgentSpec(displayName, "Real Human Contributor"),
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + profileID.String()),
			OwnerAgentID:    agentcore.AgentID(profileID),
			MemoryKind:      agentcore.MemoryKindPersonal,
			AccessScope:     "human",
		},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		SkillRefs:                 []agentcore.SkillRef{},
		ToolRefs:                  []agentcore.ToolRef{},
		RuntimeSpec:               agentcore.RuntimeSpec{},
		Enabled:                   true,
	}
}

func mergeHumanAgentProfile(existing, seeded agentcore.AgentProfile) agentcore.AgentProfile {
	merged := existing
	if shouldReplaceHumanName(merged.RealName, seeded.RealName, merged.ID) {
		merged.RealName = seeded.RealName
	}
	if shouldReplaceHumanName(merged.DisplayName, seeded.DisplayName, merged.ID) {
		merged.DisplayName = seeded.DisplayName
	}
	if strings.TrimSpace(merged.RoleName) == "" {
		merged.RoleName = seeded.RoleName
	}
	merged.AgentKind = agentcore.AgentKindHuman
	merged.OrgIdentity = mergeDefaultAgentOrgIdentity(merged.OrgIdentity, seeded.OrgIdentity)
	if len(merged.OrgIdentity.RoleRefs) == 0 {
		merged.OrgIdentity.RoleRefs = append([]agentcore.RoleRef(nil), seeded.OrgIdentity.RoleRefs...)
	}
	if strings.TrimSpace(merged.Persona.Backstory) == "" {
		merged.Persona.Backstory = seeded.Persona.Backstory
	}
	if strings.TrimSpace(merged.Persona.ResponsibilityBoundary) == "" {
		merged.Persona.ResponsibilityBoundary = seeded.Persona.ResponsibilityBoundary
	}
	if strings.TrimSpace(merged.Persona.BehaviorBoundary) == "" {
		merged.Persona.BehaviorBoundary = seeded.Persona.BehaviorBoundary
	}
	merged.BaseAgent = mergeDefaultBaseAgentSpec(merged.BaseAgent, seeded.BaseAgent)
	if merged.PersonalMemoryProfileRef.IsZero() {
		merged.PersonalMemoryProfileRef = seeded.PersonalMemoryProfileRef
	}
	if merged.CollectiveMemoryScopeRefs == nil {
		merged.CollectiveMemoryScopeRefs = []agentcore.MemoryProfileRef{}
	}
	if merged.SkillRefs == nil {
		merged.SkillRefs = []agentcore.SkillRef{}
	}
	if merged.ToolRefs == nil {
		merged.ToolRefs = []agentcore.ToolRef{}
	}
	if !merged.RuntimeSpec.HasKind() {
		merged.RuntimeSpec = seeded.RuntimeSpec
	}
	return merged
}

func shouldReplaceHumanName(existing string, seeded string, profileID agentcore.AgentProfileID) bool {
	existing = strings.TrimSpace(existing)
	seeded = strings.TrimSpace(seeded)
	if seeded == "" || existing == seeded {
		return false
	}
	if existing == "" {
		return true
	}
	profile := strings.TrimSpace(profileID.String())
	withoutPrefix := strings.TrimPrefix(profile, "human-")
	return existing == profile || existing == withoutPrefix
}

func saveHumanAgentAvailability(ctx context.Context, profiles *agentcore.PlatformProfileRepository, profile agentcore.AgentProfile) error {
	if !profile.Enabled {
		return nil
	}
	targetID := defaultBootstrapAgentIdentity(profile.ID)
	availability, err := profiles.GetAvailability(ctx, profile.ID)
	if err == nil && availability.Status == agentcore.AvailabilityAvailable && availability.AgentInstanceID == targetID {
		return nil
	}
	return profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: targetID,
		Status:          agentcore.AvailabilityAvailable,
		Capacity:        1,
		Source:          "human-bootstrap",
		UpdatedAt:       time.Now().UTC(),
	})
}

func humanAgentProfileID(humanID string) agentcore.AgentProfileID {
	sanitized := sanitizeHumanAgentID(humanID)
	if strings.HasPrefix(sanitized, "human-") {
		return agentcore.AgentProfileID(sanitized)
	}
	return agentcore.AgentProfileID("human-" + sanitized)
}

func sanitizeHumanAgentID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "user"
	}
	return out
}
