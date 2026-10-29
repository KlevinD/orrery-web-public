package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	gwconfig "orrery_v2/internal/gateway/config"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

func gatewayTenantDirectoryEntry(id string, kind agentcore.AgentKind, tenantID string) agentcore.AgentDirectoryEntry {
	return agentcore.AgentDirectoryEntry{
		AgentProfileID: agentcore.AgentProfileID(id),
		AgentKind:      kind,
		Enabled:        true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: agentcore.OrganizationID(tenantID)},
		},
	}
}

func systemGatewayTestDirectory(ids ...string) *fakeProductionGatewayAgentDirectory {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(ids))
	for _, id := range ids {
		kind := agentcore.AgentKindAI
		if strings.HasPrefix(id, "human-") {
			kind = agentcore.AgentKindHuman
		}
		entries = append(entries, gatewayTenantDirectoryEntry(id, kind, gwconfig.DefaultTenantID))
	}
	return &fakeProductionGatewayAgentDirectory{entries: entries}
}

func collectGatewayEvents(events <-chan gwprotocol.Event) []gwprotocol.Event {
	var got []gwprotocol.Event
	for event := range events {
		got = append(got, event)
	}
	return got
}

func verifiedWeixinTelemetry(senderID string) map[string]string {
	return map[string]string{
		"weixin_sender_id":       senderID,
		"weixin_sender_verified": "true",
	}
}

type fakeProductionGatewayAgentDirectory struct {
	entries []agentcore.AgentDirectoryEntry
}

func (d *fakeProductionGatewayAgentDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		if q.EnabledOnly && !entry.Enabled {
			continue
		}
		if q.IncludeAI && !q.IncludeHumans && entry.AgentKind != agentcore.AgentKindAI {
			continue
		}
		entries = append(entries, entry)
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (d *fakeProductionGatewayAgentDirectory) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	for _, entry := range d.entries {
		if entry.AgentProfileID == id {
			return agentcore.AgentProfile{ID: entry.AgentProfileID, DisplayName: entry.DisplayName, RealName: entry.RealName, AgentKind: entry.AgentKind, OrgIdentity: entry.OrgIdentity, Enabled: entry.Enabled}, nil
		}
	}
	return agentcore.AgentProfile{}, fmt.Errorf("%w: %s", agentcore.ErrNotFound, id)
}
