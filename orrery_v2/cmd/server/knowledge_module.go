package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeprovider"
	knowledgecleanup "orrery_v2/internal/knowledge/cleanup"
	knowledgeconfig "orrery_v2/internal/knowledge/config"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgeingest "orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/observability"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/vector/jsonstore"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
	platformmetrics "orrery_v2/internal/platform/metrics"
)

type knowledgeIDGenerator struct {
	generator interface {
		NewIDContext() (string, error)
	}
}

func (g knowledgeIDGenerator) NewID(prefix string) string {
	id, err := g.generator.NewIDContext()
	if err != nil || strings.TrimSpace(id) == "" {
		return prefix
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || strings.HasPrefix(id, prefix+"-") {
		return id
	}
	return prefix + "-" + id
}

type platformKnowledgeArtifacts struct {
	store *platform.ArtifactStore
}

func (a platformKnowledgeArtifacts) PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (knowledgeadmin.ArtifactRef, error) {
	ref, err := a.store.Put(ctx, name, contentType, reader)
	if err != nil {
		return knowledgeadmin.ArtifactRef{}, err
	}
	return knowledgeadmin.ArtifactRef{
		ID:          ref.ID,
		Name:        ref.Name,
		ContentType: ref.ContentType,
		SizeBytes:   ref.Size,
		Checksum:    ref.Checksum,
	}, nil
}

func (a platformKnowledgeArtifacts) GetReader(ctx context.Context, id string) (io.ReadCloser, error) {
	return a.store.GetReader(ctx, id)
}

type platformKnowledgeExecutionSubmitter struct {
	queue *execution.QueueService
}

func (s platformKnowledgeExecutionSubmitter) Submit(ctx context.Context, topic string, payload []byte, maxAttempts int) error {
	if s.queue == nil {
		return fmt.Errorf("knowledge execution queue is not configured")
	}
	_, err := s.queue.Submit(ctx, execution.Message{
		Topic:       topic,
		Payload:     append([]byte(nil), payload...),
		MaxAttempts: maxAttempts,
	})
	return err
}

func registerKnowledgeExecutionHandlers(rt *platform.Runtime, service *knowledgeingest.Service, repository knowledgestore.Repository, catalog productionKnowledgeCatalogReconciler) error {
	if rt == nil || service == nil || repository == nil || catalog == nil {
		return fmt.Errorf("knowledge execution service is not configured")
	}
	if err := rt.RegisterExecutionHandler(knowledgeingest.TopicIngestDocument, func(ctx context.Context, job execution.Job) error {
		payload, err := decodeKnowledgeJobPayload[knowledgeingest.DocumentIngestPayload](job)
		if err != nil {
			return err
		}
		return withKnowledgeLock(ctx, rt.Locks, knowledgeingest.LockName(payload.KnowledgeBaseID, "ingest"), knowledgeExecutionOwner(job), func(ctx context.Context) error {
			err := service.HandleDocumentIngest(ctx, payload)
			knowledgeMetrics(rt).RecordIngestJob(knowledgeJobMetricStatus(err))
			return err
		})
	}); err != nil {
		return err
	}
	if err := rt.RegisterExecutionHandler(knowledgeingest.TopicReindexBase, func(ctx context.Context, job execution.Job) error {
		payload, err := decodeKnowledgeJobPayload[knowledgeingest.ReindexBasePayload](job)
		if err != nil {
			return err
		}
		return withKnowledgeLock(ctx, rt.Locks, knowledgeingest.LockName(payload.KnowledgeBaseID, "reindex"), knowledgeExecutionOwner(job), func(ctx context.Context) error {
			err := service.HandleReindexBase(ctx, payload)
			knowledgeMetrics(rt).RecordIngestJob(knowledgeJobMetricStatus(err))
			return err
		})
	}); err != nil {
		return err
	}
	catalogSweeper := newProductionKnowledgeCatalogSweeper(repository, catalog, rt.Clock.Now, productionKnowledgeCatalogSweepInterval)
	if catalogSweeper == nil {
		return fmt.Errorf("knowledge catalog sweeper is not configured")
	}
	return rt.RegisterSweeper("knowledge.catalog_reconcile", execution.NewBackgroundSweeper(
		"knowledge.catalog_reconcile", catalogSweeper, 25*time.Second, rt.Logger,
	))
}

func KnowledgeModule() platform.Module {
	return knowledgeModuleWithDefaults(knowledgeconfig.Default())
}

func KnowledgeModuleWithRoot(root string) platform.Module {
	cfg := knowledgeconfig.Default()
	cfg.Root = root
	return knowledgeModuleWithDefaults(cfg)
}

func knowledgeModuleWithDefaults(defaults knowledgeconfig.Config) platform.Module {
	return platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := knowledgeconfig.Register(registrar); err != nil {
			return err
		}
		cfg, err := knowledgeconfig.FromPlatformConfigWithDefault(registrar.Config(), defaults)
		if err != nil {
			return err
		}
		if err := knowledgestore.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := knowledgeprovider.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		coreStore, err := registrar.Databases().Store(db.RoleCore)
		if err != nil {
			return err
		}
		repo := knowledgestore.NewPlatformStore(coreStore)
		status := observability.NewStatusProvider(observability.StatusDeps{
			KnowledgeBases: repo.CountKnowledgeBasesByStatus,
			Documents:      repo.CountDocumentsByStatus,
			Jobs:           repo.CountIngestJobsByStatus,
		})
		if err := registrar.RegisterStatusMetrics(platformmetrics.StatusGroup{
			Subsystem: "knowledge",
			Name:      "bases_total",
			Providers: map[string]platformmetrics.StatusCountsProvider{
				"core": platform.StatusCountsProbe(status.KnowledgeBaseStatusCounts),
			},
		}); err != nil {
			return err
		}
		if err := registrar.RegisterStatusMetrics(platformmetrics.StatusGroup{
			Subsystem: "knowledge",
			Name:      "documents_total",
			Providers: map[string]platformmetrics.StatusCountsProvider{
				"core": platform.StatusCountsProbe(status.DocumentStatusCounts),
			},
		}); err != nil {
			return err
		}
		if err := registrar.RegisterStatusMetrics(platformmetrics.StatusGroup{
			Subsystem: "knowledge",
			Name:      "ingest_jobs_total",
			Providers: map[string]platformmetrics.StatusCountsProvider{
				"core": platform.StatusCountsProbe(status.JobStatusCounts),
			},
		}); err != nil {
			return err
		}
		if err := registrar.RegisterSweeper("knowledge.delete_cleanup", knowledgecleanup.NewSweeper(knowledgecleanup.SweeperDeps{
			Repository:        repo,
			VectorStore:       jsonstore.NewStore(cfg.Root),
			DescriptorForBase: knowledgeDescriptorBuilder(cfg.Root),
		})); err != nil {
			return err
		}
		return registrar.RegisterReadyProbe("knowledge", knowledgeReadyProbe(repo, cfg.Root))
	})
}

func defaultKnowledgeRoot() string {
	return knowledgeconfig.Default().Root
}

func knowledgeReadyProbe(repo interface {
	CountKnowledgeBasesByStatus(context.Context) (map[knowledgecore.KnowledgeStatus]int64, error)
}, root string) func(context.Context) platform.ReadyStatus {
	return func(ctx context.Context) platform.ReadyStatus {
		if repo == nil {
			return platform.ReadyStatus{OK: false, Message: "knowledge metadata unavailable"}
		}
		var counts map[knowledgecore.KnowledgeStatus]int64
		probe := observability.NewHealthProbe(observability.HealthDeps{
			MetadataReady: func(ctx context.Context) error {
				next, err := repo.CountKnowledgeBasesByStatus(ctx)
				if err != nil {
					return err
				}
				counts = next
				return nil
			},
			RootReady: func(context.Context) error {
				return ensureKnowledgeRootWritable(root)
			},
			ActiveBases: func(ctx context.Context) (int64, error) {
				if counts == nil {
					next, err := repo.CountKnowledgeBasesByStatus(ctx)
					if err != nil {
						return 0, err
					}
					counts = next
				}
				return counts[knowledgecore.KnowledgeActive], nil
			},
			VectorReady: func(ctx context.Context) error {
				descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, knowledgecore.KnowledgeBaseID("healthcheck"), 1)
				if err != nil {
					return err
				}
				return observability.CheckVectorAdapter(ctx, jsonstore.NewStore(root), descriptor)
			},
		})
		status := probe.Check(ctx)
		return platform.ReadyStatus{OK: status.OK, Message: status.Message}
	}
}

func ensureKnowledgeRootWritable(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("knowledge root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".knowledge-ready-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func knowledgeDescriptorBuilder(defaultRoot string) func(knowledgecore.KnowledgeBase) (knowledgeingest.VectorDescriptor, error) {
	return func(base knowledgecore.KnowledgeBase) (knowledgeingest.VectorDescriptor, error) {
		root := strings.TrimSpace(defaultRoot)
		if base.VectorStorePath != "" {
			root = filepath.Dir(filepath.Dir(base.VectorStorePath))
		}
		return jsonstore.DescriptorForKnowledgeBase(root, base.ID, base.EmbeddingDimensions)
	}
}

func decodeKnowledgeJobPayload[T any](job execution.Job) (T, error) {
	var payload T
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func withKnowledgeLock(ctx context.Context, locks *platform.LockManager, key, owner string, fn func(context.Context) error) error {
	if locks == nil {
		return fn(ctx)
	}
	lease, ok, err := locks.TryAcquire(ctx, key, owner)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("knowledge lock %q is already held", key)
	}
	defer lease.Release(context.Background())
	return fn(ctx)
}

func knowledgeExecutionOwner(job execution.Job) string {
	owner := strings.TrimSpace(job.WorkerID)
	if owner == "" {
		owner = strings.TrimSpace(job.ID)
	}
	if owner == "" {
		owner = fmt.Sprintf("knowledge-%d", time.Now().UnixNano())
	}
	return owner
}

type platformKnowledgeMetricSink struct {
	sink *platformmetrics.BoundedSink
}

func (s platformKnowledgeMetricSink) Capture(name string, fields map[string]any) bool {
	if s.sink == nil {
		return false
	}
	return s.sink.Capture(platformmetrics.Event{Name: name, Fields: fields})
}

func knowledgeMetrics(rt *platform.Runtime) *observability.Metrics {
	if rt == nil {
		return observability.NewMetrics(nil)
	}
	return observability.NewMetrics(platformKnowledgeMetricSink{sink: rt.Metrics})
}

func knowledgeJobMetricStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}
