package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/rebac"
)

func authorizedProjectTestViewer(viewerID string) projectadmin.ViewerRef {
	return projectadmin.ViewerRef{
		ViewerID:   viewerID,
		ViewerKind: projectadmin.ViewerKindUser,
		PermissionScope: []string{
			projectadmin.PermissionProjectRead,
			projectadmin.PermissionProjectCreate,
			projectadmin.PermissionProjectWrite,
			projectadmin.PermissionProjectManage,
		},
	}
}

func newAuthorizedProjectTestService(
	t *testing.T,
	repo projectadmin.Repository,
	ids projectadmin.IDGenerator,
	clock projectadmin.Clock,
	viewer projectadmin.ViewerRef,
	organizationID string,
) *projectadmin.Service {
	t.Helper()
	store := rebac.NewMemoryStore()
	adapter := authorization.NewReBACAdapter(store, nil)
	object := identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: "global"}
	relation := "admin"
	if organizationID == "" {
		object = identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"}
		relation = "member"
	}
	if err := adapter.WriteRelations(context.Background(), []authorization.RelationTuple{{
		TenantID: "global",
		Subject:  authorization.SubjectRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		Relation: relation,
		Object:   object,
	}}); err != nil {
		t.Fatalf("seed project authorization: %v", err)
	}
	service := projectadmin.NewService(repo, ids, clock)
	service.SetAuthorizationService(authorization.NewService(adapter))
	service.SetAuthorizationRelations(authorization.NewRelationProjector(adapter))
	return service
}
