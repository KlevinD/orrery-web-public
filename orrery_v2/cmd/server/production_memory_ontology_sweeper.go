package main

import (
	"context"
	"fmt"
	"net/http"
	"orrery_v2/internal/agent/llm"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryontology"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"sync"
	"time"
)

const (
	memoryOntologyReasoningEffort  = "low"
	memoryOntologyRequestTimeout   = 20 * time.Second
	memoryOntologyReprojectTimeout = 2 * time.Minute
	memoryOntologyReprojectLimit   = 2000
)

func newProductionMemoryOntologyExtractor(settings productionSettingsStore, secrets platformSecretResolver) memoryontology.Extractor {
	models := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings,
		Secrets:  secrets,
		Client:   memoryOntologyHTTPClient(),
	})
	return memoryontology.NewChatExtractor(memoryontology.ChatModelProviderFunc(func(ctx context.Context) (llm.ChatModel, error) {
		return models.ModelForRequest(ctx, memoryOntologyModelRequest())
	}), "platform-default:"+memoryOntologyReasoningEffort)
}

func memoryOntologyModelRequest() profilemodel.ModelRequest {
	return profilemodel.ModelRequest{
		ReasoningEffort: memoryOntologyReasoningEffort,
	}
}

func memoryOntologyHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &http.Client{Transport: transport, Timeout: memoryOntologyRequestTimeout}
}

func registerProductionMemoryOntologySweeper(rt *platform.Runtime, core *db.Store, memory memstore.Repository, missions *missioncontrol.DBProjectionRepository, extractor memoryontology.Extractor, projector memoryontology.MemoryRecordProjector) error {
	if rt == nil || core == nil || memory == nil || missions == nil || extractor == nil || projector == nil {
		return fmt.Errorf("memory ontology production dependencies are required")
	}
	graph := contextgraph.NewPlatformStore(core)
	runs := memoryontology.NewPlatformStore(core)
	applier := memoryontology.NewGraphApplier(graph, ontology.NewPlatformRegistry(core), rt.Clock.Now)
	reader := newProductionMemoryOntologyReader(memory)
	resolver := newProductionMemoryOntologyScopeResolver(memory, missions, rt.Clock.Now)
	processor := memoryontology.NewProcessor(reader, runs, extractor, applier, rt.Clock.Now, projector).WithResolutionScopes(resolver)
	gate := &sync.Mutex{}
	worker := newProductionMemoryOntologyWorker(processor, productionMemoryOntologyWorkerOptions{
		Name:        "memory.ontology_bridge",
		Logger:      rt.Logger,
		Maintenance: runs,
		Gate:        gate,
		Timeout:     productionMemoryOntologyCycleTimeout,
		Cadence:     productionMemoryOntologyCycleCadence,
		Limit:       productionMemoryOntologyCycleLimit,
		Now:         rt.Clock.Now,
	})
	if err := rt.RegisterSweeper("memory.ontology_bridge", worker); err != nil {
		return err
	}
	reprojector := memoryOntologyExistingReprojector{processor: processor}
	return rt.RegisterSweeper("memory.ontology_resolution_backfill", newProductionMemoryOntologyWorker(reprojector, productionMemoryOntologyWorkerOptions{
		Name:    "memory.ontology_resolution_backfill",
		Logger:  rt.Logger,
		Gate:    gate,
		Timeout: memoryOntologyReprojectTimeout,
		Cadence: productionMemoryOntologyCycleCadence,
		Limit:   memoryOntologyReprojectLimit,
		Now:     rt.Clock.Now,
	}))
}

type memoryOntologyExistingReprojector struct {
	processor *memoryontology.Processor
}

func (r memoryOntologyExistingReprojector) Sweep(ctx context.Context, limit int) (memoryontology.SweepReport, error) {
	return r.processor.ReprojectExisting(ctx, limit)
}
