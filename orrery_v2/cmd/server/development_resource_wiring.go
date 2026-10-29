package main

import (
	"context"
	"fmt"
	"orrery_v2/internal/app/authorization"
	developmentproject "orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/scmconnection"
	"orrery_v2/internal/app/scmprovider"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/gitworkspace"
	"orrery_v2/internal/platform/logging"
)

type productionDevelopmentResources struct {
	projects             *developmentproject.Service
	projectRepository    *developmentproject.PlatformRepository
	connections          *scmconnection.Service
	connectionRepository *scmconnection.PlatformRepository
	executionRepository  *developmentproject.ExecutionRepository
	workspaceManager     *gitworkspace.Manager
}

func productionDevelopmentResourceCleanup(rt *platform.Runtime, resources productionDevelopmentResources, realtimeCleanup func()) func() {
	return func() {
		realtimeCleanup()
		if resources.workspaceManager != nil {
			if err := resources.workspaceManager.Close(); err != nil {
				rt.Logger.Warn(context.Background(), "remove ephemeral development signing key", logging.Any("error", err))
			}
		}
	}
}

func newProductionDevelopmentResources(
	rt *platform.Runtime,
	authorizationService *authorization.Service,
	authorizationRelations *authorization.RelationProjector,
	audit *operations.AuditStore,
) (productionDevelopmentResources, error) {
	connectionRepository := scmconnection.NewPlatformRepository(rt.DBRouter)
	connections := scmconnection.NewService(connectionRepository, rt.IDGenerator, rt.Clock)
	connections.SetAuthorization(authorizationService, authorizationRelations)
	gitPath, _ := rt.Config.String("scm.git_path")
	connections.SetVerifier(scmprovider.NewManagedLocalVerifier(gitPath, rt.Clock.Now))
	connections.SetAuditRecorder(operations.NewSCMConnectionAuditRecorder(audit))
	connections.SetSecretReferenceValidator(platformSecretResolver{provider: rt.Secrets})

	projectRepository := developmentproject.NewPlatformRepository(rt.DBRouter)
	projects := developmentproject.NewService(projectRepository, rt.IDGenerator, rt.Clock)
	projects.SetAuthorization(authorizationService, authorizationRelations)
	projects.SetSCMConnections(connections)
	projects.SetAuditRecorder(operations.NewDevelopmentProjectAuditRecorder(audit))
	projects.SetSecretReferenceValidator(platformSecretResolver{provider: rt.Secrets})
	executionRepository := developmentproject.NewExecutionRepository(rt.DBRouter)
	signingKey := ""
	if ref := rt.Config.Development.GitSigningKeyRef; ref != "" {
		value, err := (platformSecretResolver{provider: rt.Secrets}).ResolveSecret(context.Background(), ref)
		if err != nil {
			return productionDevelopmentResources{}, fmt.Errorf("resolve development git signing key: %w", err)
		}
		signingKey = value
	}
	workspaceManager, err := gitworkspace.NewManager(gitworkspace.Config{
		Root: rt.Config.Development.WorkspaceRoot, GitPath: gitPath,
		CommitterName: rt.Config.Development.GitCommitterName, CommitterEmail: rt.Config.Development.GitCommitterEmail,
		SigningKey: signingKey, SigningFormat: rt.Config.Development.GitSigningFormat,
		RequireSigning: rt.Config.Development.RequireSignedCommits, MaxFileBytes: rt.Config.Development.MaxChangedFileBytes,
	})
	if err != nil {
		return productionDevelopmentResources{}, fmt.Errorf("initialize managed git workspaces: %w", err)
	}
	projects.SetExecutionResources(executionRepository, productionGitWorkspace{manager: workspaceManager}, rt.Config.Development.WorkspaceLeaseTTL, rt.Config.Development.WorkspaceRetention)

	return productionDevelopmentResources{
		projects:             projects,
		projectRepository:    projectRepository,
		connections:          connections,
		connectionRepository: connectionRepository,
		executionRepository:  executionRepository,
		workspaceManager:     workspaceManager,
	}, nil
}
