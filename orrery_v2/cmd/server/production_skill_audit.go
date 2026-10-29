package main

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

func configureProductionResourceCommand(rt *platform.Runtime, command *resourceadmin.CommandService, audit *operations.AuditStore, profiles *agentcore.PlatformProfileRepository, relations *authorization.RelationProjector) error {
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	recorder := operations.NewResourceCommandAuditRecorder(audit)
	command.SetAuditRecorder(recorder)
	command.SetAgentProfileBindingStore(profiles)
	command.SetAuthorizationRelations(relations)
	if err := rt.RegisterSweeper("resource.skill_version_audits", resourceadmin.NewSkillVersionAuditRelay(core, recorder)); err != nil {
		return fmt.Errorf("register skill version audit relay: %w", err)
	}
	return nil
}
