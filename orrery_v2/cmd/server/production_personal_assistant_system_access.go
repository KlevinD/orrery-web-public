package main

import (
	"context"
	"errors"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/platform/graphprotocol"
)

// Match the repair UI: basic status requires read access; host repair receipts
// require manage access. RSI also retains the original observation's ACL.
func (p productionContextAccessPolicy) personalAssistantSystemAccess(ctx context.Context, principal identity.Principal, tenant string, ref graphprotocol.AccessRef, manage bool) (bool, error) {
	if p.scopes == nil || p.scopes.Gate == nil {
		return false, datavisibility.ErrDependencyUnavailable
	}
	// Host log capture uses "global" for platform failures. Authorize those
	// through the canonical system scope while retaining their source tenant.
	authorizationTenant := tenant
	if tenant == "global" {
		authorizationTenant = "system"
	}
	var err error
	if manage {
		_, err = p.scopes.Gate.AuthorizeManage(ctx, principal, authorizationTenant)
	} else {
		_, err = p.scopes.Gate.AuthorizeRead(ctx, principal, authorizationTenant)
	}
	if err != nil {
		if errors.Is(err, ontologyscope.ErrAccessDenied) || errors.Is(err, authorization.ErrDenied) || errors.Is(err, authorization.ErrUnsupported) {
			return false, nil
		}
		return false, err
	}
	if ref.Kind == "" {
		return true, nil
	}
	if ref.ID == "" || ref.TenantID == "" {
		return false, nil
	}
	decision, err := p.Evaluate(ctx, datavisibility.AdapterRequest{
		Principal: principal, Purpose: "personal_assistant_system_update",
		Resources: []datavisibility.ResourceDescriptor{{Key: "source", TenantID: ref.TenantID,
			AccessRefs: []datavisibility.AccessRef{{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID}}}},
	})
	if err != nil {
		return false, err
	}
	return len(decision.Resources) == 1 && decision.Resources[0].Key == "source" && !decision.Resources[0].Hidden && !decision.Resources[0].Skip, nil
}
