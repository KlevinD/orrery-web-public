package operations_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
)

func TestSkillPublicationAuditSurvivesDeliveryFailureAndDatabaseRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore:  {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "core.db")},
		db.RoleAudit: {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "audit.db")},
	}}
	databases, err := db.OpenRegistry(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = databases.Close() }()
	core, _ := databases.Store(db.RoleCore)
	registry := skillres.NewPlatformRegistry(core)
	base := skillres.SkillDefinition{ID: "skill.delivery", Name: "Delivery", Version: "1", Enabled: true}
	candidate := base.Clone()
	candidate.Version, candidate.Description = "2", "evaluated candidate"
	if err := registry.Register(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	command := resourceadmin.NewCommandService(nil, registry)
	evaluations := resourceadmin.NewPlatformSkillVersionEvaluationStore(core)
	command.SetSkillVersionEvaluationStore(evaluations)
	if err := evaluations.SaveSkillVersionEvaluation(ctx, base.ID, resourceadmin.SkillVersionEvaluation{EvaluationRef: "eval", CandidateVersion: candidate.Version, BaselineVersion: base.Version, CandidateDefinitionHash: skillres.DefinitionHash(candidate), BaselineDefinitionHash: skillres.DefinitionHash(base), DatasetRef: "test-heldout", PolicyVersion: "test-policy", EvaluatorRef: "test-evaluator", BaselineScore: .8, CandidateScore: .9, HeldOutPassed: true, SafetyPassed: true, EvidenceRefs: []string{"test-run"}}); err != nil {
		t.Fatal(err)
	}
	viewer := resourceadmin.ViewerRef{ViewerID: "rsi-test", PermissionScope: []string{resourceadmin.PermissionResourceManage}}
	audit := operations.NewAuditStore(db.NewRouter(databases), nil, nil)
	recorder := &faultingSkillAuditDelivery{next: operations.NewResourceCommandAuditRecorder(audit), failBefore: true}
	command.SetAuditRecorder(recorder)
	if _, err := command.PromoteSkillVersion(ctx, viewer, string(base.ID), candidate.Version, resourceadmin.PromoteSkillVersionRequest{EvaluationRef: "eval"}); err != nil {
		t.Fatalf("durably audited publication failed because projection was unavailable: %v", err)
	}
	if len(core.Snapshot()["resource_skill_version_audit_events"]) != 1 || len(core.Snapshot()["resource_skill_version_audit_pending"]) != 1 {
		t.Fatal("publication did not commit both its audit journal and delivery marker")
	}
	relay := resourceadmin.NewSkillVersionAuditRelay(core, recorder)
	if err := relay.Sweep(ctx); err == nil {
		t.Fatal("audit outage was not reported")
	}
	page, err := audit.ListAuditEvents(ctx, operations.AuditLogQuery{})
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("unavailable projection = %+v, %v", page, err)
	}
	recorder.failBefore, recorder.failAfter = false, true
	if err := relay.Sweep(ctx); err == nil {
		t.Fatal("lost delivery acknowledgement was not reported")
	}
	page, err = audit.ListAuditEvents(ctx, operations.AuditLogQuery{})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("audit was not persisted before the lost ack: %+v, %v", page, err)
	}
	auditID := page.Events[0].AuditID
	if auditID == "" || page.Events[0].Metadata["previous_active_hash"] != skillres.DefinitionHash(base) || page.Events[0].Metadata["target_hash"] != skillres.DefinitionHash(candidate) {
		t.Fatal("publication audit does not identify the exact changed content")
	}
	if len(core.Snapshot()["resource_skill_version_audit_pending"]) != 1 {
		t.Fatal("unacknowledged delivery was discarded")
	}
	if err := databases.Close(); err != nil {
		t.Fatal(err)
	}
	databases, err = db.OpenRegistry(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	core, _ = databases.Store(db.RoleCore)
	audit = operations.NewAuditStore(db.NewRouter(databases), nil, nil)
	relay = resourceadmin.NewSkillVersionAuditRelay(core, operations.NewResourceCommandAuditRecorder(audit))
	if err := relay.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := relay.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = audit.ListAuditEvents(ctx, operations.AuditLogQuery{})
	if err != nil || len(page.Events) != 1 || page.Events[0].AuditID != auditID {
		t.Fatalf("restart duplicated or changed the event: %+v, %v", page, err)
	}
	if len(core.Snapshot()["resource_skill_version_audit_pending"]) != 0 || len(core.Snapshot()["resource_skill_version_audit_events"]) != 1 {
		t.Fatal("delivery did not clear the marker while preserving the journal")
	}
	active, err := skillres.NewPlatformRegistry(core).Get(ctx, base.ID)
	if err != nil || active.Version != candidate.Version {
		t.Fatalf("delivery failure or restart undid publication: %+v, %v", active, err)
	}
	altered := page.Events[0]
	altered.Summary = "overwrite the historical event"
	if err := audit.RecordAuditEventIdempotent(ctx, altered); !errors.Is(err, operations.ErrVersionConflict) {
		t.Fatalf("duplicate audit id with different content = %v", err)
	}
	page, _ = audit.ListAuditEvents(ctx, operations.AuditLogQuery{})
	if page.Events[0].Summary == altered.Summary {
		t.Fatal("idempotent delivery overwrote historical evidence")
	}
}

type faultingSkillAuditDelivery struct {
	next                  resourceadmin.AuditRecorderPort
	failBefore, failAfter bool
}

func (r *faultingSkillAuditDelivery) RecordAuditEvent(ctx context.Context, event resourceadmin.AuditEvent) error {
	if r.failBefore {
		return errors.New("audit database unavailable")
	}
	if err := r.next.RecordAuditEvent(ctx, event); err != nil {
		return err
	}
	if r.failAfter {
		return errors.New("delivery acknowledgement lost")
	}
	return nil
}
