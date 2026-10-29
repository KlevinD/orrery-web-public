package main

import (
	"fmt"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/platform/db"
)

type productionOntologySecurityState struct {
	Snapshots     authorization.SnapshotProvider
	Invalidations *authorization.InvalidationHub
	PolicyStore   *datapolicy.PlatformStore
	Concurrency   *ontologycontract.ConcurrencyLimiter
	Cursors       *productionOntologyCursorState
	Scopes        *productionOntologyScopeState
}

func newProductionOntologySecurityState(core *db.Store, authorizationReader authorization.AuthorizationRevisionReader, authorizationModes ...string) (*productionOntologySecurityState, error) {
	if core == nil {
		return nil, fmt.Errorf("ontology security state requires core database")
	}
	invalidations := authorization.NewInvalidationHub()
	policyStore := datapolicy.NewPlatformStore(core, datapolicy.WithPolicyRevisionObserver(invalidations))
	var authorizationRevision authorization.RevisionSource
	mode := authorizationModeLocal
	if len(authorizationModes) > 0 && authorizationModeFromConfig(authorizationModes[0]) != "" {
		mode = authorizationModeFromConfig(authorizationModes[0])
	}
	// OpenFGA does not expose a transactionally fenceable relation revision.
	// Keeping this empty makes reads use stateful cursors and makes secured
	// promotion plans fail closed instead of trusting the unrelated local epoch.
	if authorizationReader != nil && mode != authorizationModeOpenFGAEnforce {
		authorizationRevision = authorization.NewAuthorizationRevisionSource(authorizationReader)
	}
	concurrency, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		return nil, fmt.Errorf("create ontology concurrency limiter: %w", err)
	}
	return &productionOntologySecurityState{
		Snapshots:     authorization.NewSnapshotService(authorizationRevision, authorization.NewPolicyRevisionSource(policyStore)),
		Invalidations: invalidations,
		PolicyStore:   policyStore,
		Concurrency:   concurrency,
	}, nil
}
