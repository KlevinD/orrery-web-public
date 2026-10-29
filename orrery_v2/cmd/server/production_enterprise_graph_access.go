package main

import (
	"context"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
)

type productionEnterpriseGraphAccess struct {
	policy datavisibility.Adapter
}

func (a productionEnterpriseGraphAccess) VisibleObjectIDs(ctx context.Context, request contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (map[string]bool, error) {
	principal := identity.Principal{
		SubjectProvider: "orrery",
		Subject:         strings.TrimSpace(request.ActorRef.ID),
		ViewerKind:      strings.ToLower(strings.TrimSpace(request.ActorRef.Kind)),
		TenantID:        strings.TrimSpace(request.TenantID),
		CoarseScopes:    append([]string(nil), request.PermissionScopes...),
	}
	if principal.ViewerKind == identity.ViewerKindAgent {
		principal.AgentProfileID = principal.Subject
	} else {
		principal.UserID = principal.Subject
	}
	resources := make([]datavisibility.ResourceDescriptor, 0, len(objects))
	for _, object := range objects {
		fieldPaths := make([]string, 0, len(object.Properties))
		for field := range object.Properties {
			fieldPaths = append(fieldPaths, "properties."+field)
		}
		sort.Strings(fieldPaths)
		accessRefs := []datavisibility.AccessRef(nil)
		if object.AccessState == contextgraph.AccessStateResolved && object.OwnerAccessRef.Resolved() {
			accessRefs = []datavisibility.AccessRef{{
				Kind: object.OwnerAccessRef.Kind, ID: object.OwnerAccessRef.ID, TenantID: object.OwnerAccessRef.TenantID,
			}}
		}
		resources = append(resources, datavisibility.ResourceDescriptor{
			Key: object.ID, Kind: datavisibility.ResourceObject, TenantID: object.TenantID,
			Type: object.ObjectType, FieldPaths: fieldPaths, AccessRefs: accessRefs,
		})
	}
	if a.policy == nil {
		return nil, datavisibility.ErrDependencyUnavailable
	}
	decision, err := a.policy.Evaluate(ctx, datavisibility.AdapterRequest{
		Principal: principal, Purpose: request.Purpose, Resources: resources,
	})
	if err != nil {
		return nil, err
	}
	visible := make(map[string]bool, len(objects))
	for _, item := range decision.Resources {
		if !item.Hidden && item.ExistenceDisclosure != datavisibility.ExistenceConceal {
			visible[item.Key] = true
		}
	}
	return visible, nil
}
