package main

import (
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
)

func TestProductionHILAutoplanWiringReconcilesAfterDiscussionAttach(t *testing.T) {
	coordinator := discussionsession.NewRuntimeApprovalCoordinator(discussionsession.RuntimeApprovalCoordinatorOptions{})
	reconciled := make(chan error, 1)
	wiring := productionHILAutoplanWiring{
		runtimeApprovalCoordinator: coordinator,
		runtimeApprovalReconcile:   func(err error) { reconciled <- err },
	}
	wiring.AttachDiscussionService(&discussionsession.Service{})
	select {
	case err := <-reconciled:
		if err != nil {
			t.Fatalf("startup reconciliation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup HIL reconciliation was not invoked")
	}
}

func TestProductionHILAutoplanWiringAttachesCanonicalMissionCommands(t *testing.T) {
	initial := &responsibilityAutoplanMissionCommandsFake{}
	canonical := &responsibilityAutoplanMissionCommandsFake{}
	materializer := newProductionResponsibilityAutoplanMissionMaterializer(initial, time.Now)
	wiring := productionHILAutoplanWiring{autoplanMaterializer: materializer}

	wiring.AttachAutoplanMissionCommands(canonical)

	if materializer.missions != canonical {
		t.Fatal("autoplan materializer did not receive canonical Mission commands")
	}
}
