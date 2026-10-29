package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"

	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform/db"
	httptransport "orrery_v2/internal/transport/http"
)

// DEVELOPMENT RULE: Do not wire new backend functionality to demoAPI or newDemoHandler.
// Production gaps must be implemented in production services and storage, or return explicit service-dependency errors.
func newDemoHandler() http.Handler {
	return newDemoHandlerWithSecurity(false, "")
}

func newDemoHandlerWithSecurity(trustedViewerHeaders bool, internalViewerAuth string) http.Handler {
	api := newDemoAPI()
	if err := api.attachDemoDatabase(context.Background(), demoDatabaseURL()); err != nil {
		log.Printf("demo database unavailable, using in-memory seed: %v", err)
	}
	discussionSessions := discussionsession.NewService(discussionsession.ServiceOptions{
		Repository:       discussionsession.NewRepository(api.router),
		MissionTooling:   discussionsession.NewMissionToolApplier(api),
		MissionMessenger: demoDiscussionMissionMessenger{api: api},
	})
	return httptransport.NewRouter(httptransport.RouterOptions{
		TrustedViewerHeaders: trustedViewerHeaders,
		InternalViewerAuth:   internalViewerAuth,
		Readiness: func(context.Context) httptransport.ReadinessReport {
			return httptransport.ReadinessReport{OK: true, Checks: []httptransport.ReadinessCheck{{Name: "demo", OK: true}}}
		},
		MissionQuery:    api,
		MissionCommand:  api,
		MissionStream:   api,
		Approvals:       api,
		ResourceQuery:   api,
		ResourceCommand: api,
		Operations:      api,
		Discussions:     discussionSessions,
	})
}

type demoAPI struct {
	mu          sync.RWMutex
	missions    map[string]missioncontrol.MissionControlViewDTO
	audit       []operations.AuditEventDTO
	resources   resourceadmin.ResourceAdminViewDTO
	workforce   operations.WorkforcePageDTO
	orgGraph    operations.OrgGraphDTO
	permissions operations.PermissionMatrixDTO
	evaluations operations.EvaluationPageDTO
	router      *db.Router
}

const (
	demoMissionIndexTable = "core.demo_mission_index"
	demoAuditTable        = "core.demo_audit"
	demoResourceTable     = "core.demo_resources"
	demoWorkforceTable    = "core.demo_workforce"
	demoOrgGraphTable     = "core.demo_org_graph"
	demoPermissionsTable  = "core.demo_permissions"
	demoEvaluationTable   = "core.demo_evaluations"
)

func demoDatabaseURL() string {
	return "memory://orrery-disabled-demo"
}

func (d *demoAPI) attachDemoDatabase(ctx context.Context, rawURL string) error {
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: rawURL})
	if err != nil {
		return err
	}
	router := db.NewRouter(registry)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.router = router
	if ok, err := d.loadLocked(ctx); err != nil {
		return err
	} else if ok {
		return nil
	}
	return d.persistLocked(ctx)
}

func (d *demoAPI) persistLocked(ctx context.Context) error {
	if d.router == nil {
		return nil
	}
	missionIDs := make([]string, 0, len(d.missions))
	missionRecords := map[string]missioncontrol.MissionControlViewDTO{}
	for id := range d.missions {
		missionIDs = append(missionIDs, id)
		missionRecords[id] = d.missions[id]
	}
	sort.Strings(missionIDs)
	if err := d.router.PutRecord(ctx, demoMissionIndexTable, "default", db.Record{"payload": missionIndexPayload{IDs: missionIDs, Records: missionRecords}}); err != nil {
		return err
	}
	if err := d.router.PutRecord(ctx, demoAuditTable, "default", db.Record{"payload": d.audit}); err != nil {
		return err
	}
	if err := d.router.PutRecord(ctx, demoResourceTable, "default", db.Record{"payload": d.resources}); err != nil {
		return err
	}
	if err := d.router.PutRecord(ctx, demoWorkforceTable, "default", db.Record{"payload": d.workforce}); err != nil {
		return err
	}
	if err := d.router.PutRecord(ctx, demoOrgGraphTable, "default", db.Record{"payload": d.orgGraph}); err != nil {
		return err
	}
	if err := d.router.PutRecord(ctx, demoPermissionsTable, "default", db.Record{"payload": d.permissions}); err != nil {
		return err
	}
	return d.router.PutRecord(ctx, demoEvaluationTable, "default", db.Record{"payload": d.evaluations})
}

func (d *demoAPI) loadLocked(ctx context.Context) (bool, error) {
	if d.router == nil {
		return false, nil
	}
	missionIndexRecord, ok, err := d.router.GetRecord(ctx, demoMissionIndexTable, "default")
	if err != nil || !ok {
		return ok, err
	}
	workforceRecord, ok, err := d.router.GetRecord(ctx, demoWorkforceTable, "default")
	if err != nil || !ok {
		return ok, err
	}
	auditRecord, ok, err := d.router.GetRecord(ctx, demoAuditTable, "default")
	if err != nil || !ok {
		return ok, err
	}
	resourceRecord, ok, err := d.router.GetRecord(ctx, demoResourceTable, "default")
	if err != nil || !ok {
		return ok, err
	}
	orgGraphRecord, ok, err := d.router.GetRecord(ctx, demoOrgGraphTable, "default")
	if err != nil || !ok {
		return ok, err
	}
	permissionsRecord, ok, err := d.router.GetRecord(ctx, demoPermissionsTable, "default")
	if err != nil || !ok {
		return ok, err
	}
	evaluationRecord, ok, err := d.router.GetRecord(ctx, demoEvaluationTable, "default")
	if err != nil || !ok {
		return ok, err
	}

	if err := decodePayload(workforceRecord, &d.workforce); err != nil {
		return false, err
	}
	if err := decodePayload(auditRecord, &d.audit); err != nil {
		return false, err
	}
	if err := decodePayload(resourceRecord, &d.resources); err != nil {
		return false, err
	}
	if err := decodePayload(orgGraphRecord, &d.orgGraph); err != nil {
		return false, err
	}
	if err := decodePayload(permissionsRecord, &d.permissions); err != nil {
		return false, err
	}
	if err := decodePayload(evaluationRecord, &d.evaluations); err != nil {
		return false, err
	}

	var missionIndex missionIndexPayload
	if err := decodePayload(missionIndexRecord, &missionIndex); err != nil {
		return false, err
	}
	missions := map[string]missioncontrol.MissionControlViewDTO{}
	for _, id := range missionIndex.IDs {
		if view, ok := missionIndex.Records[id]; ok {
			missions[id] = view
		}
	}
	d.missions = missions
	return true, nil
}

type missionIndexPayload struct {
	IDs     []string                                        `json:"ids"`
	Records map[string]missioncontrol.MissionControlViewDTO `json:"records"`
}

func decodePayload(record db.Record, target any) error {
	raw, ok := record["payload"]
	if !ok {
		return fmt.Errorf("demo database payload is missing")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode demo database payload: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode demo database payload: %w", err)
	}
	return nil
}

func newDemoAPI() *demoAPI {
	return &demoAPI{
		missions:    map[string]missioncontrol.MissionControlViewDTO{},
		audit:       []operations.AuditEventDTO{},
		resources:   resourceadmin.ResourceAdminViewDTO{},
		workforce:   operations.WorkforcePageDTO{},
		orgGraph:    operations.OrgGraphDTO{},
		permissions: operations.PermissionMatrixDTO{},
		evaluations: operations.EvaluationPageDTO{},
	}
}
