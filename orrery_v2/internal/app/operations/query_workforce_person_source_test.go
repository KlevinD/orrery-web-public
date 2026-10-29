package operations

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestMapMemberExposesPeopleSourceFactsWithoutLoginRelationship(t *testing.T) {
	profile := agentcore.AgentProfile{
		PersonSource: agentcore.PersonSourceFacts{
			EmployeeNumber: "E-001",
			Source:         "people.feishu",
			SourceRecordID: "ou_123",
			SourceTenantID: "tenant-1",
		},
	}

	member := mapMemberFromProfile(agentcore.AgentDirectoryEntry{}, profile)
	if member.PersonSource.EmployeeNumber != "E-001" || member.PersonSource.Source != "people.feishu" || member.PersonSource.SourceRecordID != "ou_123" || member.PersonSource.SourceTenantID != "tenant-1" {
		t.Fatalf("person source DTO = %+v", member.PersonSource)
	}
}
