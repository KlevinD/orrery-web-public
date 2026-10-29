package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "", "HTTP listen address (overrides server.addr)")
	configPath := fs.String("config", defaultConfigPath(), "YAML config file path")
	checkConfig := fs.Bool("check-config", false, "validate effective configuration and exit")
	initDB := fs.Bool("init-db", false, "initialize configured databases and exit")
	disableBackgroundWorkers := fs.Bool("disable-background-workers", false, "serve requests without background workers or startup writes (non-production only)")
	accessRefBackfill := fs.Bool("ontology-access-ref-backfill", false, "report unresolved ontology graph access references and exit")
	accessRefBackfillApply := fs.Bool("ontology-access-ref-backfill-apply", false, "apply deterministic ontology graph access references and exit")
	accessRefBackfillLimit := fs.Int("ontology-access-ref-backfill-limit", 100, "maximum ontology graph access references to inspect (max 500)")
	accessRefBackfillAfterKind := fs.String("ontology-access-ref-backfill-after-kind", "", "checkpoint resource kind")
	accessRefBackfillAfterTenant := fs.String("ontology-access-ref-backfill-after-tenant", "", "checkpoint tenant id")
	accessRefBackfillAfterID := fs.String("ontology-access-ref-backfill-after-id", "", "checkpoint resource id")
	accessRefBackfillChangedBy := fs.String("ontology-access-ref-backfill-changed-by", strings.TrimSpace(os.Getenv("ORRERY_OPERATOR_ID")), "audited operator id required for apply")
	scopeBindingBackfill := fs.Bool("ontology-scope-binding-backfill", false, "report unresolved ontology scope bindings and exit")
	scopeBindingBackfillApply := fs.Bool("ontology-scope-binding-backfill-apply", false, "apply exactly resolved ontology scope bindings and exit")
	scopeBindingBackfillLimit := fs.Int("ontology-scope-binding-backfill-limit", 100, "maximum ontology scope bindings to inspect (max 500)")
	scopeBindingBackfillAfterSource := fs.String("ontology-scope-binding-backfill-after-source", "", "checkpoint source table")
	scopeBindingBackfillAfterTenant := fs.String("ontology-scope-binding-backfill-after-tenant", "", "checkpoint tenant id")
	scopeBindingBackfillChangedBy := fs.String("ontology-scope-binding-backfill-changed-by", strings.TrimSpace(os.Getenv("ORRERY_OPERATOR_ID")), "audited operator id required for apply")
	governanceBackfill := fs.Bool("ontology-governance-backfill", false, "report legacy ontology proposal or receipt governance bindings and exit")
	governanceBackfillApply := fs.Bool("ontology-governance-backfill-apply", false, "apply deterministic ontology proposal or receipt governance bindings and exit")
	governanceBackfillResource := fs.String("ontology-governance-backfill-resource", string(productionOntologyGovernanceBackfillProposals), "governance backfill resource: proposals or receipts")
	governanceBackfillLimit := fs.Int("ontology-governance-backfill-limit", 100, "maximum ontology governance records to inspect (max 500)")
	governanceBackfillAfterID := fs.String("ontology-governance-backfill-after-id", "", "checkpoint proposal or receipt id")
	governanceBackfillChangedBy := fs.String("ontology-governance-backfill-changed-by", strings.TrimSpace(os.Getenv("ORRERY_OPERATOR_ID")), "operator id required for apply")
	peopleAccountReconcileDryRun := fs.Bool("people-account-reconcile-dry-run", false, "inventory People, Kratos, and login binding account state and exit")
	peopleAccountReconcileApply := fs.Bool("people-account-reconcile-apply", false, "apply conflict-free People account projections and exit")
	peopleDirectorySyncDryRun := fs.Bool("people-directory-sync-dry-run", false, "scan and match a complete Feishu directory snapshot without applying it")
	peopleDirectoryApplySnapshot := fs.String("people-directory-apply-snapshot", "", "apply one reviewed complete Feishu directory snapshot and exit")
	scheduledApprovalTask := fs.String("scheduled-approval-task", "", "scheduled task id whose HIL approval should be assigned")
	scheduledApprovalAssignee := fs.String("scheduled-approval-assignee", "", "human id assigned to the scheduled task HIL approval")
	ontologyInitialize := fs.Bool("ontology-initialize", false, "report incremental ontology initialization and exit")
	ontologyInitializeApply := fs.Bool("ontology-initialize-apply", false, "apply incremental ontology initialization and exit")
	ontologyInitializePageSize := fs.Int("ontology-initialize-limit", 100, "ontology initialization page size (max 500)")
	ontologyInitializeChangedBy := fs.String("ontology-initialize-changed-by", strings.TrimSpace(os.Getenv("ORRERY_OPERATOR_ID")), "operator id required for apply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *accessRefBackfill && *accessRefBackfillApply {
		return fmt.Errorf("ontology access ref report and apply modes are mutually exclusive")
	}
	if *scopeBindingBackfill && *scopeBindingBackfillApply {
		return fmt.Errorf("ontology scope binding report and apply modes are mutually exclusive")
	}
	if *governanceBackfill && *governanceBackfillApply {
		return fmt.Errorf("ontology governance report and apply modes are mutually exclusive")
	}
	if *peopleAccountReconcileDryRun && *peopleAccountReconcileApply {
		return fmt.Errorf("People account reconciliation dry-run and apply modes are mutually exclusive")
	}
	if *peopleDirectorySyncDryRun && strings.TrimSpace(*peopleDirectoryApplySnapshot) != "" {
		return fmt.Errorf("People directory sync dry-run and snapshot apply modes are mutually exclusive")
	}
	if (strings.TrimSpace(*scheduledApprovalTask) == "") != (strings.TrimSpace(*scheduledApprovalAssignee) == "") {
		return fmt.Errorf("scheduled approval task and assignee must be provided together")
	}
	if *ontologyInitialize && *ontologyInitializeApply {
		return fmt.Errorf("ontology initialization report and apply modes are mutually exclusive")
	}
	if *ontologyInitializeApply && strings.TrimSpace(*ontologyInitializeChangedBy) == "" {
		return fmt.Errorf("ontology initialization operator is required for apply")
	}
	maintenanceModes := 0
	if *accessRefBackfill || *accessRefBackfillApply {
		maintenanceModes++
	}
	if *scopeBindingBackfill || *scopeBindingBackfillApply {
		maintenanceModes++
	}
	if *governanceBackfill || *governanceBackfillApply {
		maintenanceModes++
	}
	if *peopleAccountReconcileDryRun || *peopleAccountReconcileApply {
		maintenanceModes++
	}
	if *ontologyInitialize || *ontologyInitializeApply {
		maintenanceModes++
	}
	if *peopleDirectorySyncDryRun {
		maintenanceModes++
	}
	if strings.TrimSpace(*peopleDirectoryApplySnapshot) != "" {
		maintenanceModes++
	}
	if strings.TrimSpace(*scheduledApprovalTask) != "" {
		maintenanceModes++
	}
	if *initDB && maintenanceModes > 0 {
		return fmt.Errorf("init-db and maintenance modes are mutually exclusive")
	}
	if *checkConfig && (*initDB || maintenanceModes > 0) {
		return fmt.Errorf("check-config, init-db, and maintenance modes are mutually exclusive")
	}
	if maintenanceModes > 1 {
		return fmt.Errorf("maintenance modes are mutually exclusive")
	}
	if *checkConfig {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "configuration valid (environment=%s)\n", cfg.Environment)
		return nil
	}
	if *initDB {
		return initializeProductionDatabases(ctx, *configPath, out)
	}
	if *accessRefBackfill || *accessRefBackfillApply {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		return runProductionAccessRefBackfill(ctx, cfg, productionAccessRefBackfillOptions{
			Apply:     *accessRefBackfillApply,
			Limit:     *accessRefBackfillLimit,
			ChangedBy: *accessRefBackfillChangedBy,
			After: contextgraph.AccessRefBackfillCursor{
				ResourceKind: contextgraph.AccessRefResourceKind(strings.TrimSpace(*accessRefBackfillAfterKind)),
				TenantID:     strings.TrimSpace(*accessRefBackfillAfterTenant),
				ResourceID:   strings.TrimSpace(*accessRefBackfillAfterID),
			},
		}, out)
	}
	if *scopeBindingBackfill || *scopeBindingBackfillApply {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		return runProductionScopeBindingBackfill(ctx, cfg, productionScopeBindingBackfillOptions{
			Apply:     *scopeBindingBackfillApply,
			Limit:     *scopeBindingBackfillLimit,
			ChangedBy: *scopeBindingBackfillChangedBy,
			After: ontologyscope.BindingBackfillCursor{
				Source:   strings.TrimSpace(*scopeBindingBackfillAfterSource),
				TenantID: strings.TrimSpace(*scopeBindingBackfillAfterTenant),
			},
		}, out)
	}
	if *governanceBackfill || *governanceBackfillApply {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		return runProductionOntologyGovernanceBackfill(ctx, cfg, productionOntologyGovernanceBackfillOptions{
			Apply:     *governanceBackfillApply,
			Resource:  productionOntologyGovernanceBackfillResource(strings.TrimSpace(*governanceBackfillResource)),
			Limit:     *governanceBackfillLimit,
			AfterID:   strings.TrimSpace(*governanceBackfillAfterID),
			ChangedBy: strings.TrimSpace(*governanceBackfillChangedBy),
		}, out)
	}
	if *ontologyInitialize || *ontologyInitializeApply {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		mode := ontologyInitializationDryRun
		if *ontologyInitializeApply {
			mode = ontologyInitializationApply
		}
		return runProductionOntologyInitialization(ctx, cfg, ontologyInitializationRequest{
			Mode: mode, Operator: strings.TrimSpace(*ontologyInitializeChangedBy), PageSize: *ontologyInitializePageSize,
		}, out)
	}
	if *peopleAccountReconcileDryRun || *peopleAccountReconcileApply {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		return runProductionPeopleAccountReconciliation(ctx, cfg, *peopleAccountReconcileApply, out)
	}
	if *peopleDirectorySyncDryRun || strings.TrimSpace(*peopleDirectoryApplySnapshot) != "" {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		if *peopleDirectorySyncDryRun {
			return runProductionPeopleDirectorySync(ctx, cfg, out)
		}
		return runProductionPeopleDirectoryApply(ctx, cfg, *peopleDirectoryApplySnapshot, out)
	}
	if strings.TrimSpace(*scheduledApprovalTask) != "" {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		return runProductionScheduledApprovalAssignment(ctx, cfg, *scheduledApprovalTask, *scheduledApprovalAssignee, out)
	}
	listenAddr := strings.TrimSpace(*addr)
	if listenAddr == "" {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		listenAddr = strings.TrimSpace(cfg.Server.Addr)
	}
	if listenAddr == "" {
		return fmt.Errorf("server listen address is required; set server.addr or -addr")
	}
	if *disableBackgroundWorkers {
		cfg, err := loadProductionConfig(*configPath)
		if err != nil {
			return err
		}
		if isProductionEnvironment(cfg.Environment) {
			return fmt.Errorf("background workers cannot be disabled in production")
		}
	}
	handler, cleanup, err := newServerHandlerWithConfigOptions(ctx, false, *configPath, productionHandlerOptions{
		DisableBackgroundWorkers: *disableBackgroundWorkers,
	})
	if err != nil {
		return err
	}
	defer cleanup()
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	_, _ = fmt.Fprintf(out, "Orrery v2 API listening on http://%s\n", listenAddr)
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shutdownDone <- server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if ctx.Err() != nil {
		if err := <-shutdownDone; err != nil {
			return fmt.Errorf("gracefully shut down API server: %w", err)
		}
	}
	return nil
}

func defaultConfigPath() string { return strings.TrimSpace(os.Getenv("ORRERY_CONFIG")) }

func defaultDemoMode() bool { return false }

func newServerHandler(ctx context.Context, demo bool) (http.Handler, func(), error) {
	return newServerHandlerWithConfig(ctx, demo, defaultConfigPath())
}

func newServerHandlerWithConfig(ctx context.Context, demo bool, configPath string) (http.Handler, func(), error) {
	return newServerHandlerWithConfigOptions(ctx, demo, configPath, productionHandlerOptions{})
}

func newServerHandlerWithConfigOptions(ctx context.Context, demo bool, configPath string, opts productionHandlerOptions) (http.Handler, func(), error) {
	if demo {
		return nil, func() {}, fmt.Errorf("demo API handler is disabled; development must use production routes")
	}
	return newProductionHandlerWithConfigOptions(ctx, configPath, opts)
}

func newProductionHandler(ctx context.Context) (http.Handler, func(), error) {
	return newProductionHandlerWithConfig(ctx, defaultConfigPath())
}

func newProductionHandlerWithConfig(ctx context.Context, configPath string) (http.Handler, func(), error) {
	return newProductionHandlerWithConfigOptions(ctx, configPath, productionHandlerOptions{})
}

type productionHandlerOptions struct {
	DisableBackgroundWorkers bool
}

func newProductionHandlerWithConfigOptions(ctx context.Context, configPath string, opts productionHandlerOptions) (http.Handler, func(), error) {
	cfg, err := loadProductionConfig(configPath)
	if err != nil {
		return nil, func() {}, err
	}
	rt, err := openProductionRuntime(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	var handlerCleanup func()
	cleanup := func() {
		if handlerCleanup != nil {
			handlerCleanup()
			handlerCleanup = nil
		}
		if err := rt.Close(); err != nil {
			log.Printf("close production runtime: %v", err)
		}
	}
	if opts.DisableBackgroundWorkers {
		ready, err := rt.Migrations.LatestApplied(ctx)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("verify remote database schema: %w", err)
		}
		if !ready {
			cleanup()
			return nil, func() {}, fmt.Errorf("remote database schema is not current; run migrations from the authoritative deployment")
		}
	} else if err := rt.Initialize(ctx); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	handler, cleanupHandler, err := newProductionHandlerFromRuntimeWithOptions(ctx, rt, opts)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	handlerCleanup = cleanupHandler
	if opts.DisableBackgroundWorkers {
		return handler, cleanup, nil
	}
	if err := rt.StartOutboxRelay(ctx, platform.OutboxRelayRuntimeConfig{WorkerID: "orrery-outbox", PollInterval: 250 * time.Millisecond, RecoverBatchSize: 100}); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{WorkerID: "orrery-mission-execution", PollInterval: 250 * time.Millisecond, Concurrency: rt.Config.Queue.WorkerConcurrency}); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := rt.StartSweeper(ctx, platform.SweeperRuntimeConfig{
		Interval:     30 * time.Second,
		InitialDelay: productionSweeperInitialDelay,
	}); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return handler, cleanup, nil
}

func openProductionRuntime(ctx context.Context, cfg config.Config) (*platform.Runtime, error) {
	return platform.OpenWithModules(ctx, cfg, productionModules()...)
}

func productionModules() []platform.Module {
	return []platform.Module{
		AuthModule(),
		platform.ModuleFunc(storage.RegisterMissionStorage),
		OntologyModule(),
		KnowledgeModule(),
		platform.ModuleFunc(registerAgentTracePlatform),
		platform.ModuleFunc(registerGatewayConfigPlatform),
		platform.ModuleFunc(registerRealtimePlatform),
		platform.ModuleFunc(registerDeviceRuntimePlatform),
	}
}

func initializeProductionDatabases(ctx context.Context, configPath string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	cfg, err := loadProductionConfig(configPath)
	if err != nil {
		return err
	}
	rt, err := openProductionRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := rt.Close(); err != nil {
			log.Printf("close production runtime after database initialization: %v", err)
		}
	}()

	for _, group := range productionDatabaseInitGroups(cfg) {
		for _, role := range group.roles {
			store, err := rt.Databases.Store(role)
			if err != nil {
				return fmt.Errorf("initialize %s database: %w", role, err)
			}
			if err := store.Ping(ctx); err != nil {
				return fmt.Errorf("initialize %s database: %w", role, err)
			}
			if err := rt.Migrations.UpRole(ctx, role); err != nil {
				return fmt.Errorf("initialize %s database: %w", role, err)
			}
		}
		_, _ = fmt.Fprintln(out, group.initializedMessage(cfg))
	}
	if err := rt.Initialize(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "database initializers completed")
	return nil
}

func productionDatabaseRoles() []string {
	return []string{
		db.RoleCore,
		db.RoleAudit,
		db.RoleEvents,
		db.RoleJobs,
		db.RoleRuntime,
		db.RoleObservability,
	}
}

func loadProductionConfig(configPath string) (config.Config, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return config.Config{}, fmt.Errorf("YAML configuration is required; set -config or ORRERY_CONFIG")
	}
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		return config.Config{}, err
	}
	if cfg.SchemaVersion != 1 || strings.TrimSpace(cfg.Profile) == "" {
		return config.Config{}, fmt.Errorf("profile YAML configuration is required; schema_version and profile must be set")
	}
	return cfg, nil
}
