package discussionsession

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func defaultTestAgentDirectory() AgentDirectoryPort {
	return &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{
			AgentProfileID:  "lead-agent",
			AgentInstanceID: "lead-agent",
			DisplayName:     "顾明澈",
			RealName:        "顾明澈",
			RoleName:        "Lead",
			AgentKind:       agentcore.AgentKindAI,
			ToolRefs:        defaultTestAgentToolRefs(),
			Enabled:         true,
		},
		{
			AgentProfileID:  "staffing-agent",
			AgentInstanceID: "staffing-agent",
			DisplayName:     "秦若兰",
			RealName:        "秦若兰",
			RoleName:        "Staffing",
			AgentKind:       agentcore.AgentKindAI,
			ToolRefs:        defaultTestAgentToolRefs(),
			Enabled:         true,
		},
		{
			AgentProfileID:  "custom-lead-agent",
			AgentInstanceID: "custom-lead-agent",
			DisplayName:     "顾明澈",
			RealName:        "顾明澈",
			RoleName:        "Lead",
			AgentKind:       agentcore.AgentKindAI,
			ToolRefs:        defaultTestAgentToolRefs(),
			Enabled:         true,
		},
		{
			AgentProfileID:  "coordinator-agent",
			AgentInstanceID: "coordinator-agent",
			DisplayName:     "林知远",
			RealName:        "林知远",
			RoleName:        "Coordinator",
			AgentKind:       agentcore.AgentKindAI,
			ToolRefs:        defaultTestAgentToolRefs(),
			Enabled:         true,
		},
	}}
}

func defaultTestAgentToolRefs() []agentcore.ToolRef {
	tools := defaultTestDiscussionBoundTools()
	refs := make([]agentcore.ToolRef, 0, len(tools))
	for _, tool := range tools {
		if toolID := strings.TrimSpace(tool.ToolID); toolID != "" {
			refs = append(refs, agentcore.ToolRef{ToolID: agentcore.ToolID(toolID)})
		}
	}
	return refs
}

type testAgentDirectory struct {
	entries []agentcore.AgentDirectoryEntry
	err     error
}

func (d *testAgentDirectory) QueryDirectory(_ context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	if d != nil && d.err != nil {
		return agentcore.AgentDirectoryPage{}, d.err
	}
	if d == nil {
		return agentcore.AgentDirectoryPage{}, nil
	}
	return agentcore.AgentDirectoryPage{Entries: append([]agentcore.AgentDirectoryEntry(nil), d.entries...), Total: len(d.entries)}, nil
}

func (d *testAgentDirectory) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	entry, ok := testDirectoryEntryByProfileID(d, id)
	if !ok {
		return agentcore.AgentProfile{}, fmt.Errorf("agent profile %s not found", id)
	}
	return agentProfileFromTestDirectoryEntry(entry), nil
}

func (d *testAgentDirectory) GetAvailability(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentAvailability, error) {
	entry, ok := testDirectoryEntryByProfileID(d, id)
	if !ok {
		return agentcore.AgentAvailability{}, fmt.Errorf("agent profile %s not found", id)
	}
	return entry.Availability, nil
}

type filteringTestAgentDirectory struct {
	entries []agentcore.AgentDirectoryEntry
	err     error
}

func (d *filteringTestAgentDirectory) QueryDirectory(_ context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	if d != nil && d.err != nil {
		return agentcore.AgentDirectoryPage{}, d.err
	}
	if d == nil {
		return agentcore.AgentDirectoryPage{}, nil
	}
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		if q.EnabledOnly && !entry.Enabled {
			continue
		}
		if q.IncludeAI && !q.IncludeHumans && entry.AgentKind != agentcore.AgentKindAI {
			continue
		}
		if q.IncludeHumans && !q.IncludeAI && entry.AgentKind != agentcore.AgentKindHuman {
			continue
		}
		entries = append(entries, entry)
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (d *filteringTestAgentDirectory) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	entry, ok := filteringTestDirectoryEntryByProfileID(d, id)
	if !ok {
		return agentcore.AgentProfile{}, fmt.Errorf("agent profile %s not found", id)
	}
	return agentProfileFromTestDirectoryEntry(entry), nil
}

func (d *filteringTestAgentDirectory) GetAvailability(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentAvailability, error) {
	entry, ok := filteringTestDirectoryEntryByProfileID(d, id)
	if !ok {
		return agentcore.AgentAvailability{}, fmt.Errorf("agent profile %s not found", id)
	}
	return entry.Availability, nil
}

func testDirectoryEntryByProfileID(d *testAgentDirectory, id agentcore.AgentProfileID) (agentcore.AgentDirectoryEntry, bool) {
	if d == nil {
		return agentcore.AgentDirectoryEntry{}, false
	}
	for _, entry := range d.entries {
		if entry.AgentProfileID == id {
			return entry, true
		}
	}
	return agentcore.AgentDirectoryEntry{}, false
}

func filteringTestDirectoryEntryByProfileID(d *filteringTestAgentDirectory, id agentcore.AgentProfileID) (agentcore.AgentDirectoryEntry, bool) {
	if d == nil {
		return agentcore.AgentDirectoryEntry{}, false
	}
	for _, entry := range d.entries {
		if entry.AgentProfileID == id {
			return entry, true
		}
	}
	return agentcore.AgentDirectoryEntry{}, false
}

func agentProfileFromTestDirectoryEntry(entry agentcore.AgentDirectoryEntry) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID:           entry.AgentProfileID,
		DisplayName:  entry.DisplayName,
		RealName:     entry.RealName,
		RoleName:     entry.RoleName,
		AgentKind:    entry.AgentKind,
		OrgIdentity:  entry.OrgIdentity,
		Placement:    entry.Placement,
		SkillRefs:    append([]agentcore.SkillRef(nil), entry.SkillRefs...),
		ToolRefs:     append([]agentcore.ToolRef(nil), entry.ToolRefs...),
		RuntimeSpec:  agentcore.RuntimeSpec{Kind: entry.RuntimeSpecSummary.Kind, ConfigRef: entry.RuntimeSpecSummary.ConfigRef},
		Availability: string(entry.Availability.Status),
		Enabled:      entry.Enabled,
	}
}

type testHumanProfiles struct {
	profile agentcore.AgentProfile
	err     error
}

func (p *testHumanProfiles) EnsureDiscussionHumanProfile(_ context.Context, viewer ViewerRef) (agentcore.AgentProfile, error) {
	if p != nil && p.err != nil {
		return agentcore.AgentProfile{}, p.err
	}
	if p != nil && p.profile.ID != "" {
		return p.profile, nil
	}
	name := firstNonBlank(viewer.DisplayName, "本地用户")
	return agentcore.AgentProfile{
		ID:          agentcore.AgentProfileID("human-" + viewer.ViewerID),
		DisplayName: name,
		RealName:    name,
		RoleName:    "Real Human Contributor",
		AgentKind:   agentcore.AgentKindHuman,
		Enabled:     true,
	}, nil
}
