package main

import (
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	ontologyschema "orrery_v2/internal/app/ontologyview/schema"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
)

func newProductionOntologyScopeAndSchemaAPIs(
	core *db.Store,
	security *productionOntologySecurityState,
	organizations *operations.OrganizationCatalogStore,
	projects *projectadmin.PlatformRepository,
	missions *missioncontrol.DBProjectionRepository,
	tools *toolres.PlatformRegistry,
	authorizer *authorization.Service,
	roles productionRolePermissionAuthorizer,
) (*ontologyscope.Catalog, *ontologyschema.Service, error) {
	var err error
	security.Scopes, err = newProductionOntologyScopeState(
		core, organizations, projects, missions, tools, authorizer,
		security.Snapshots, roles,
	)
	if err != nil {
		return nil, nil, err
	}
	scopes, err := newProductionOntologyScopeAPI(
		security.Scopes, security.Cursors, organizations, projects, missions, tools,
	)
	if err != nil {
		return nil, nil, err
	}
	schema, err := newProductionOntologySchemaAPI(core, security.Scopes, security.Cursors, security.Concurrency, security.PolicyStore)
	if err != nil {
		return nil, nil, err
	}
	return scopes, schema, nil
}
