package main

import (
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/operations"
	skillres "orrery_v2/internal/resource/skill"
)

func configureProductionOperationsQuery(
	query *operations.QueryService,
	audit operations.AuditStorePort,
	orgCatalog operations.OrganizationCatalogPort,
	roleStore operations.RoleStorePort,
	evals operations.EvaluationProjectionStore,
	rebacStore operations.ReBACStorePort,
) {
	query.SetAuditStore(audit)
	query.SetOrganizationCatalogStore(orgCatalog)
	query.SetRoleStore(roleStore)
	query.SetEvaluationProjectionStore(evals)
	query.SetReBACStore(rebacStore)
}

func configureProductionOperationsCommand(
	command *operations.CommandService,
	audit operations.OperationsAuditRecorderPort,
	orgCatalog operations.OrganizationCatalogStorePort,
	roleStore operations.RoleStorePort,
	authorizationRelations *authorizationapp.RelationProjector,
	memoryStore operations.MemoryProfileStorePort,
	skillRegistry skillres.Registry,
	skillPackageStore *skillres.PackageStore,
	knowledge operations.KnowledgeLifecyclePort,
) {
	command.SetAuditRecorder(audit)
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetRoleStore(roleStore)
	command.SetAuthorizationRelations(authorizationRelations)
	command.SetMemoryProfileStore(memoryStore)
	command.SetSkillRegistry(skillRegistry)
	command.SetSkillPackageStore(skillPackageStore)
	command.SetKnowledgeLifecycle(knowledge)
}

func configureProductionOperationsAuthorization(
	query *operations.QueryService,
	command *operations.CommandService,
	authorization operations.AuthorizationCheckerPort,
) {
	query.SetAuthorizationChecker(authorization)
	command.SetAuthorizationChecker(authorization)
}
