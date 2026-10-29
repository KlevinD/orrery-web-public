package main

import (
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func trustedOntologySourceRef(kind, id, tenantID string) graphprotocol.AccessRef {
	return graphprotocol.AccessRef{
		Kind:     strings.ToLower(strings.TrimSpace(kind)),
		ID:       strings.TrimSpace(id),
		TenantID: strings.TrimSpace(tenantID),
	}
}

func contextGraphAccessRef(ref graphprotocol.AccessRef) contextgraph.AccessRef {
	return contextgraph.NormalizeAccessRef(contextgraph.AccessRef{
		Kind:     ref.Kind,
		ID:       ref.ID,
		TenantID: ref.TenantID,
	})
}

func protocolAccessRef(ref contextgraph.AccessRef) graphprotocol.AccessRef {
	ref = contextgraph.NormalizeAccessRef(ref)
	return graphprotocol.AccessRef{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID}
}

func missionOntologySourceRef(tenantID, missionID string) graphprotocol.AccessRef {
	missionID = strings.TrimSpace(missionID)
	return trustedOntologySourceRef("mission", missionID, missionScopeTenantID(tenantID, missionID))
}

func projectOntologySourceRef(tenantID, projectID string) graphprotocol.AccessRef {
	projectID = strings.TrimSpace(projectID)
	return trustedOntologySourceRef("project", projectID, projectScopeTenantID(tenantID, projectID))
}

func organizationOntologySourceRef(organizationID string) graphprotocol.AccessRef {
	organizationID = strings.TrimSpace(organizationID)
	return trustedOntologySourceRef("organization", organizationID, organizationID)
}

func systemResourceOntologySourceRef(resourceID string) contextgraph.AccessRef {
	return contextGraphAccessRef(trustedOntologySourceRef("resource", resourceID, "system"))
}
