package httptransport

import (
	"context"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/accesscontrol"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	developmentproject "orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/knowledgeprovider"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/memorydistill"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	notificationapp "orrery_v2/internal/app/notification"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/operations"
	projectadmin "orrery_v2/internal/app/projectadmin"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/app/scmconnection"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/app/userauth"
	devicedirectory "orrery_v2/internal/device/directory"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/channel/feishu"
	websitechannel "orrery_v2/internal/gateway/channel/website"
	gwconfig "orrery_v2/internal/gateway/config"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/knowledge/provider"
	missioncore "orrery_v2/internal/mission/core"
	mcptransport "orrery_v2/internal/transport/mcp"
	wstransport "orrery_v2/internal/transport/websocket"
)

// DEVELOPMENT RULE: Do not satisfy these HTTP service interfaces with demo handlers or demo API facades.
// Implement the missing backend service, storage, runtime adapter, or return an explicit service dependency error.
type MissionQueryService interface {
	GetMissionControlView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionControlViewDTO, error)
	GetMissionOpsView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionOpsViewDTO, error)
	ListMissions(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error)
}

type MissionCommandService interface {
	SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error)
	SubmitQADecision(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.QADecisionCommand) (missioncontrol.CommandReceiptDTO, error)
	PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.PostDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error)
	ReplyDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ReplyDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error)
	ListInbox(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ListInboxCommand) (missioncontrol.InboxPageDTO, error)
	MarkInboxRead(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error)
	CloseInboxItem(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error)
	CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error)
	DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) error
	CancelMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelMissionCommand) (missioncontrol.CommandReceiptDTO, error)
	CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error)
	AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error)
	StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error)
	CancelAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelAgentRunCommand) (missioncontrol.CommandReceiptDTO, error)
	RetryAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error)
}

type ApprovalService interface {
	ListApprovals(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ApprovalListQuery) (missioncontrol.ApprovalPageDTO, error)
	DecideApproval(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ApprovalDecisionCommand) (missioncontrol.CommandReceiptDTO, error)
}

type HILService interface {
	CreateDecision(context.Context, hil.CreateDecisionInput) (hil.CreateDecisionResult, error)
	ListDecisions(context.Context, hil.DecisionListQuery) (hil.DecisionPage, error)
	GetDecision(context.Context, string, string) (hil.DecisionRequest, error)
	GetCouncil(context.Context, string, string) (hil.CouncilSession, error)
	SubmitContribution(context.Context, string, string, hil.ActorRef, hil.SubmitContributionInput) error
	CastVote(context.Context, string, string, hil.ActorRef, hil.CastVoteInput) (hil.CouncilSession, error)
}

// HILDecisionResolver is implemented by the production HIL service. It is a
// separate optional capability so lightweight read/council test doubles and
// embedders remain source-compatible while ordinary HIL approvals are wired.
type HILDecisionResolver interface {
	ResolveDecision(context.Context, string, string, hil.ActorRef, hil.ResolveDecisionInput) (hil.DecisionResolution, error)
}

type HILGatewayReceiptConsumer interface {
	ConsumeGatewayDecision(context.Context, hil.GatewayReceiptConsumeInput) (hil.DecisionReceipt, error)
}

type MarketingWechatApprovalService interface {
	RequestMarketingWechatApproval(context.Context, hil.MarketingWechatApprovalInput) (hil.MarketingWechatApprovalResult, error)
}

type MarketingWechatApprovalRoute struct {
	Service       MarketingWechatApprovalService
	Authorization string
}

type HILWorkflowService interface {
	ListWorkflowTemplates(context.Context, hil.WorkflowTemplateListQuery) ([]hil.WorkflowTemplateSummary, error)
	GetWorkflowTemplate(context.Context, string, string) (hil.WorkflowTemplate, error)
	SaveWorkflowTemplateDraft(context.Context, hil.ActorRef, hil.SaveTemplateDraftInput) (hil.WorkflowTemplate, error)
	PublishWorkflowTemplate(context.Context, hil.ActorRef, string, string, []hil.WorkflowTemplateBinding, string) (hil.WorkflowTemplateVersion, error)
	DisableWorkflowTemplate(context.Context, hil.ActorRef, hil.DisableTemplateInput) error
	PutWorkflowTemplateBinding(context.Context, hil.ActorRef, hil.PutTemplateBindingInput) (hil.WorkflowTemplateBinding, error)
	ListWorkflowInstances(context.Context, hil.WorkflowListQuery) (hil.WorkflowInstancePage, error)
	GetWorkflowInstance(context.Context, string, string) (hil.WorkflowInstanceDetail, error)
	RecordWorkflowViewed(context.Context, string, string, hil.ActorRef, string) error
	DecideWorkflowTask(context.Context, string, string, hil.ActorRef, hil.DecideWorkflowTaskInput) (hil.WorkflowInstanceDetail, error)
	TransferWorkflowTask(context.Context, string, string, hil.ActorRef, hil.TransferWorkflowTaskInput) (hil.WorkflowInstanceDetail, error)
	ReassignWorkflowTask(context.Context, string, string, hil.ActorRef, hil.ReassignWorkflowTaskInput) (hil.WorkflowInstanceDetail, error)
	ResubmitWorkflow(context.Context, string, string, hil.ActorRef, hil.ResubmitWorkflowInput) (hil.WorkflowInstanceDetail, error)
}

type TodoAutomationService interface {
	CheckTodoAutomation(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CheckTodoAutomationRequest) (todoapp.TodoAutomationScanResult, error)
	ListTodoAutomationApprovals(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListTodoAutomationApprovalsQuery) ([]todoapp.TodoAutomationApproval, error)
	DecideTodoAutomationApproval(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.DecideTodoAutomationApprovalRequest) (todoapp.TodoAutomationState, error)
	StartTodoAutomationExecution(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.StartTodoAutomationExecutionRequest) (todoapp.TodoAutomationState, error)
}

type ProjectCommandService interface {
	CreateProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.CreateProjectCommand) (projectadmin.Project, error)
	ListProjects(ctx context.Context, viewer projectadmin.ViewerRef, query projectadmin.ListProjectsQuery) (projectadmin.ProjectPage, error)
	GetProjectView(ctx context.Context, viewer projectadmin.ViewerRef, projectID string) (projectadmin.ProjectView, error)
	UpdateProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.UpdateProjectCommand) (projectadmin.Project, error)
	ArchiveProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.ArchiveProjectCommand) (projectadmin.Project, error)
}

type DevelopmentProjectService interface {
	Create(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.CreateCommand) (developmentproject.ProjectDTO, error)
	List(ctx context.Context, viewer developmentproject.ViewerRef, query developmentproject.ListQuery) (developmentproject.ProjectPage, error)
	Get(ctx context.Context, viewer developmentproject.ViewerRef, projectID string) (developmentproject.ProjectDTO, error)
	Update(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.UpdateCommand) (developmentproject.ProjectDTO, error)
	Archive(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.ArchiveCommand) (developmentproject.ProjectDTO, error)
	ListMembers(ctx context.Context, viewer developmentproject.ViewerRef, projectID string) ([]developmentproject.Member, error)
	AddMember(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.AddMemberCommand) (developmentproject.Member, developmentproject.ProjectDTO, error)
	UpdateMember(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.UpdateMemberCommand) (developmentproject.Member, developmentproject.ProjectDTO, error)
	RemoveMember(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.RemoveMemberCommand) (developmentproject.ProjectDTO, error)
	ListRepositories(ctx context.Context, viewer developmentproject.ViewerRef, projectID string) ([]developmentproject.RepositoryBinding, error)
	BindRepository(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.BindRepositoryCommand) (developmentproject.RepositoryBinding, developmentproject.ProjectDTO, error)
	UpdateRepository(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.UpdateRepositoryCommand) (developmentproject.RepositoryBinding, developmentproject.ProjectDTO, error)
	VerifyRepository(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.VerifyRepositoryCommand) (developmentproject.RepositoryBinding, developmentproject.ProjectDTO, error)
	RemoveRepository(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.RemoveRepositoryCommand) (developmentproject.ProjectDTO, error)
	ListExecutions(ctx context.Context, viewer developmentproject.ViewerRef, projectID string, query developmentproject.ListExecutionsQuery) (developmentproject.ExecutionPage, error)
}

type DevelopmentExecutionService interface {
	ListWorkspaces(ctx context.Context, viewer developmentproject.ViewerRef, projectID string, query developmentproject.ListExecutionArtifactsQuery) (developmentproject.WorkspacePage, error)
	ListRuns(ctx context.Context, viewer developmentproject.ViewerRef, projectID string, query developmentproject.ListExecutionArtifactsQuery) (developmentproject.ExecutionRunPage, error)
	ListTodoCommits(ctx context.Context, viewer developmentproject.ViewerRef, todoID string, query developmentproject.ListExecutionArtifactsQuery) (developmentproject.CommitPage, error)
	ListTodoSubmissions(ctx context.Context, viewer developmentproject.ViewerRef, todoID string, query developmentproject.ListExecutionArtifactsQuery) (developmentproject.CodeSubmissionPage, error)
	GetTodoSubmission(ctx context.Context, viewer developmentproject.ViewerRef, todoID, submissionID string) (developmentproject.TodoCodeSubmission, error)
	ExportTodoSubmission(ctx context.Context, viewer developmentproject.ViewerRef, todoID, submissionID, kind string) (developmentproject.CodeExportDownload, error)
	CreateControlledCommit(ctx context.Context, viewer developmentproject.ViewerRef, todoID string, cmd developmentproject.CreateCommitCommand) (developmentproject.TodoCommitRecord, error)
	CreateCodeSubmission(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.CreateSubmissionCommand) (developmentproject.TodoCodeSubmission, error)
	PushCodeSubmission(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.SubmissionCommand) (developmentproject.TodoCodeSubmission, error)
	RequestSubmissionChanges(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.RequestChangesCommand) (developmentproject.TodoCodeSubmission, error)
	PreviewCodeSubmissionMerge(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.SubmissionCommand) (developmentproject.TodoCodeSubmission, error)
	ApproveCodeSubmissionMerge(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.MergeApprovalCommand) (developmentproject.TodoCodeSubmission, error)
	MergeCodeSubmission(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.SubmissionCommand) (developmentproject.TodoCodeSubmission, error)
	CloseCodeSubmission(ctx context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.SubmissionCommand) (developmentproject.TodoCodeSubmission, error)
}

type SCMConnectionService interface {
	Create(ctx context.Context, viewer scmconnection.ViewerRef, cmd scmconnection.CreateCommand) (scmconnection.ConnectionDTO, error)
	List(ctx context.Context, viewer scmconnection.ViewerRef, query scmconnection.ListQuery) (scmconnection.ConnectionPage, error)
	Get(ctx context.Context, viewer scmconnection.ViewerRef, connectionID string) (scmconnection.ConnectionDTO, error)
	Update(ctx context.Context, viewer scmconnection.ViewerRef, cmd scmconnection.UpdateCommand) (scmconnection.ConnectionDTO, error)
	Rotate(ctx context.Context, viewer scmconnection.ViewerRef, cmd scmconnection.RotateCommand) (scmconnection.ConnectionDTO, error)
	Revoke(ctx context.Context, viewer scmconnection.ViewerRef, cmd scmconnection.ActionCommand) (scmconnection.ConnectionDTO, error)
	Verify(ctx context.Context, viewer scmconnection.ViewerRef, cmd scmconnection.ActionCommand) (scmconnection.ConnectionDTO, scmconnection.Verification, error)
	ListRepositories(ctx context.Context, viewer scmconnection.ViewerRef, connectionID string) ([]scmconnection.RepositoryMetadata, error)
}

type OntologyScopeService interface {
	List(ctx context.Context, principal identity.Principal, query ontologyscope.CatalogQuery) (ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem], error)
	Resolve(ctx context.Context, principal identity.Principal, tenantID string) (ontologyscope.ResolvedScope, error)
}

type AuditRecorder interface {
	RecordAuditEvent(ctx context.Context, event operations.AuditEventDTO) error
}

type ResourceQueryService interface {
	ListResourcesFiltered(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ListResourcesRequest) (resourceadmin.ResourceAdminViewDTO, error)
	ListSkillBundles(ctx context.Context, viewer resourceadmin.ViewerRef) (resourceadmin.SkillBundleListDTO, error)
	GetSkillDetail(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillDetailDTO, error)
	GetMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerDTO, error)
	GetMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string) (resourceadmin.MemoryProfileDTO, error)
	GetPersonalMemoryProfileByAgent(ctx context.Context, viewer resourceadmin.ViewerRef, agentID string) (resourceadmin.MemoryProfileDTO, error)
	BuildMemoryDigest(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BuildMemoryDigestRequest) (resourceadmin.MemoryDigestDTO, error)
	DiscoverMCPServers(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.DiscoverMCPRequest) (resourceadmin.DiscoverMCPResponseDTO, error)
}

type ResourceCommandService interface {
	RegisterTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error)
	UpdateTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error)
	RegisterMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error)
	UpdateMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error)
	SyncMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerSyncDTO, error)
	GetMCPServerHealth(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerHealthDTO, error)
	ImportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, reader io.ReaderAt, size int64) (resourceadmin.SkillDTO, error)
	CreateSkill(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillRequest) (resourceadmin.SkillDetailDTO, error)
	RegisterSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillVersionCandidateRequest) (resourceadmin.SkillVersionDTO, error)
	PromoteSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.PromoteSkillVersionRequest) (resourceadmin.SkillVersionDTO, error)
	RollbackSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.RollbackSkillVersionRequest) (resourceadmin.SkillVersionDTO, error)
	CreateSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillBundleRequest) (resourceadmin.SkillBundleDTO, error)
	AddSkillsToBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.AddSkillsToBundleRequest) (resourceadmin.SkillBundleDTO, error)
	ImportSkillIntoBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, reader io.ReaderAt, size int64) (resourceadmin.SkillBundleDTO, error)
	RemoveSkillFromBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID, skillID string) (resourceadmin.SkillBundleDTO, error)
	DeleteSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.DeleteSkillBundleRequest) error
	SaveSkillMarkdown(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, raw string) (resourceadmin.SkillDetailDTO, error)
	GetSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDTO, error)
	SaveSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileWriteRequest) (resourceadmin.SkillFileDTO, error)
	UploadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileUploadRequest) (resourceadmin.SkillFileDTO, error)
	DownloadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDownloadDTO, error)
	DeleteSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error
	CreateSkillFolder(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error
	DisableTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error
	DisableSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error
	DeleteTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error
	DeleteMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error
	DeleteSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error
	ExportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillPackageDTO, error)
	BindResourceToAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BindResourceRequest) (resourceadmin.ResourceBindingDTO, error)
	UnbindResourceFromAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.UnbindResourceRequest) error
}

type MemoryQueryService interface {
	ListCandidates(ctx context.Context, viewer memoryadmin.ViewerRef, query memoryadmin.ListCandidatesQuery) (memoryadmin.MemoryCandidatePageDTO, error)
	ListRecords(ctx context.Context, viewer memoryadmin.ViewerRef, query memoryadmin.ListRecordsQuery) (memoryadmin.MemoryRecordPageDTO, error)
	GetRecord(ctx context.Context, viewer memoryadmin.ViewerRef, memoryID string) (memoryadmin.MemoryRecordDTO, error)
}

type MemoryCommandService interface {
	CreateCandidate(ctx context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.CreateCandidateRequest) (memoryadmin.MemoryCandidateDTO, error)
	AcceptCandidate(ctx context.Context, viewer memoryadmin.ViewerRef, candidateID string, req memoryadmin.AcceptCandidateRequest) (memoryadmin.MemoryRecordDTO, error)
	RejectCandidate(ctx context.Context, viewer memoryadmin.ViewerRef, candidateID string, req memoryadmin.RejectCandidateRequest) (memoryadmin.CommandReceiptDTO, error)
	MergeCandidate(ctx context.Context, viewer memoryadmin.ViewerRef, candidateID string, req memoryadmin.MergeCandidateRequest) (memoryadmin.CommandReceiptDTO, error)
	TouchRecords(ctx context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.TouchMemoryRecordsRequest) (memoryadmin.CommandReceiptDTO, error)
	ArchiveRecord(ctx context.Context, viewer memoryadmin.ViewerRef, memoryID string, req memoryadmin.MemoryLifecycleRequest) (memoryadmin.CommandReceiptDTO, error)
	AnonymizeRecord(ctx context.Context, viewer memoryadmin.ViewerRef, memoryID string, req memoryadmin.MemoryLifecycleRequest) (memoryadmin.MemoryRecordDTO, error)
	DeleteRecord(ctx context.Context, viewer memoryadmin.ViewerRef, memoryID string, req memoryadmin.MemoryLifecycleRequest) (memoryadmin.CommandReceiptDTO, error)
	SweepRetention(ctx context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.MemoryRetentionSweepRequest) (memoryadmin.MemoryRetentionSweepResultDTO, error)
}

type MemoryDistillService interface {
	DistillExperience(ctx context.Context, viewer memorydistill.ViewerRef, req memorydistill.DistillExperienceRequest) (memorydistill.DistillExperienceResponse, error)
	DistillWorld(ctx context.Context, viewer memorydistill.ViewerRef, req memorydistill.DistillWorldRequest) (memorydistill.DistillWorldResponse, error)
}

type OperationsService interface {
	ListWorkforceMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error)
	ListTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error)
	GetOrgGraph(ctx context.Context, viewer operations.ViewerRef, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error)
	ListRoleDefinitions(ctx context.Context, viewer operations.ViewerRef) (operations.RoleDefinitionPageDTO, error)
	ListRoleAssignments(ctx context.Context, viewer operations.ViewerRef, query operations.RoleAssignmentQuery) (operations.RoleAssignmentPageDTO, error)
	PutRoleDefinitions(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleDefinitionsCommand) (operations.RoleDefinitionPageDTO, error)
	DeleteRoleAssignment(ctx context.Context, viewer operations.ViewerRef, assignmentID string) error
	GetPermissionMatrix(ctx context.Context, viewer operations.ViewerRef, query operations.PermissionMatrixQuery) (operations.PermissionMatrixDTO, error)
	DecidePermission(ctx context.Context, viewer operations.ViewerRef, req operations.PermissionDecisionRequestDTO) (operations.PermissionDecisionDTO, error)
	ListAuditLog(ctx context.Context, viewer operations.ViewerRef, query operations.AuditLogQuery) (operations.AuditEventPageDTO, error)
	ListEvaluations(ctx context.Context, viewer operations.ViewerRef, query operations.EvaluationQuery) (operations.EvaluationPageDTO, error)
	CreateOrganization(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationCommand) (operations.OrganizationDTO, error)
	CreateOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationUnitCommand) (operations.DepartmentDTO, error)
	CreateOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationPositionCommand) (operations.PositionDTO, error)
	DeleteOrganization(ctx context.Context, viewer operations.ViewerRef, organizationID string) error
	DeleteOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, departmentID string) error
	DeleteOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, positionID string) error
	UpdateOrganizationOwner(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpdateOrganizationOwnerCommand) (operations.OrganizationNodeDTO, error)
	PutRoleAssignments(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleAssignmentsCommand) (operations.RoleAssignmentPageDTO, error)
	CreateWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error)
	UpsertWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error)
	SetWorkforceMemberEnabled(ctx context.Context, viewer operations.ViewerRef, cmd operations.SetWorkforceMemberEnabledCommand) error
	ReleaseWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.ReleaseWorkforceMemberCommand) error
	HireTalentFromMarket(ctx context.Context, viewer operations.ViewerRef, cmd operations.HireTalentFromMarketCommand) (operations.WorkforceMemberDTO, error)
	DeleteTalentMarketMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMemberCommand) error
	DeleteTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMembersCommand) (operations.DeleteTalentMarketMembersResponseDTO, error)
	PreviewTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportPreviewRequest) (operations.TalentMarketImportPreviewResponseDTO, error)
	CommitTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportCommitRequest) (operations.TalentMarketImportCommitResponseDTO, error)
	ExportTalentMarket(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketExportRequest) (operations.TalentMarketExportPackageDTO, error)
}

type KnowledgeQueryService interface {
	ListKnowledgeBases(ctx context.Context, viewer knowledgeadmin.ViewerRef, query knowledgeadmin.ListKnowledgeBasesQuery) (knowledgeadmin.KnowledgeBasePageDTO, error)
	GetKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) (knowledgeadmin.KnowledgeBaseDTO, error)
	ListBindings(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.KnowledgeBindingPageDTO, error)
	ListDocuments(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.KnowledgeDocumentPageDTO, error)
	GetDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string, documentID string) (knowledgeadmin.KnowledgeDocumentDTO, error)
	ListJobs(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.KnowledgeJobPageDTO, error)
	GetKnowledgeJob(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) (knowledgeadmin.KnowledgeJobDTO, error)
}

type KnowledgeSearchService interface {
	SearchKnowledge(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error)
}

type KnowledgeCommandService interface {
	CreateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error)
	UpdateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.UpdateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error)
	DeleteKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) error
	BindKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.BindKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBindingDTO, error)
	UnbindKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, bindingID string) error
	CreateDocumentMetadata(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateDocumentCommand) (knowledgeadmin.KnowledgeDocumentDTO, error)
	UploadDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.UploadDocumentCommand) (knowledgeadmin.KnowledgeDocumentDTO, error)
	DeleteDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DeleteDocumentCommand) error
	RefreshDocumentStatus(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DocumentActionCommand) (knowledgeadmin.KnowledgeDocumentDTO, error)
	ReprocessDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DocumentActionCommand) (knowledgeadmin.KnowledgeDocumentDTO, error)
	GetDocumentProcessingResult(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DocumentProcessingResultCommand) (knowledgeadmin.KnowledgeDocumentProcessingResultDTO, error)
	CreateReindexJob(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.ReindexKnowledgeBaseCommand) (knowledgeadmin.KnowledgeJobDTO, error)
	SyncKnowledgeDocuments(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.CatalogReconcileResult, error)
}

type KnowledgeProviderService interface {
	ListBindings(context.Context) ([]knowledgeprovider.Binding, error)
	GetBinding(context.Context, string) (knowledgeprovider.Binding, error)
	CreateBinding(context.Context, knowledgeprovider.Binding, string) (knowledgeprovider.Binding, error)
	UpdateBinding(context.Context, knowledgeprovider.Binding, string) (knowledgeprovider.Binding, error)
	TestBinding(context.Context, string, string) (knowledgeprovider.CapabilityTestResult, error)
	Sync(context.Context, string, knowledgeprovider.SyncRequest) (knowledgeprovider.SyncResult, error)
}

type KnowledgeWorkspaceService interface {
	GetSource(context.Context, knowledgeadmin.ViewerRef, string, string, int) (provider.SemanticSource, error)
	UpdateSource(context.Context, knowledgeadmin.ViewerRef, string, string, int, string) (provider.DocumentRevision, error)
	ListRevisions(context.Context, knowledgeadmin.ViewerRef, string, string) ([]provider.DocumentRevision, error)
	PublishRevision(context.Context, knowledgeadmin.ViewerRef, string, string, int) (provider.DocumentRevision, error)
	GetOntologyProjection(context.Context, knowledgeadmin.ViewerRef, string, string, int) (knowledgeontology.Projection, error)
	ListDocumentRelations(context.Context, knowledgeadmin.ViewerRef, string, string, string, int) (knowledgeadmin.KnowledgeDocumentRelationPageDTO, error)
}

type KnowledgeProfileStore interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type AgentProfileDirectoryStore interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type DiscussionService interface {
	ListConversations(ctx context.Context, viewer discussionsession.ViewerRef, query discussionsession.ListConversationsQuery) (discussionsession.ConversationPageDTO, error)
	ListApprovalConversations(ctx context.Context, viewer discussionsession.ViewerRef) ([]discussionsession.ConversationDTO, error)
	GetConversation(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error)
	GetConversationPage(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string, query discussionsession.ListMessagesQuery) (discussionsession.ConversationDTO, error)
	ListMessages(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string, query discussionsession.ListMessagesQuery) (discussionsession.MessagePageDTO, error)
	GetLatestRunDebug(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.RunDebugDTO, error)
	GetRunDebug(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string, runID string) (discussionsession.RunDebugDTO, error)
	PrewarmConversation(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.PrewarmConversationCommand) (discussionsession.ConversationDTO, error)
	SendMessage(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (discussionsession.ConversationDTO, error)
	SendMessageStream(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (<-chan discussionsession.StreamEventDTO, error)
	InterjectMessage(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.InterjectMessageCommand) (discussionsession.MessageDTO, error)
	DecideRuntimeApproval(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.RuntimeApprovalDecisionCommand) (discussionsession.ConversationDTO, error)
	AddConversationMembers(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.AddConversationMembersCommand) (discussionsession.ConversationDTO, error)
	SetConversationWorkspace(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SetConversationWorkspaceCommand) (discussionsession.ConversationDTO, error)
	SetConversationHILMode(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SetConversationHILModeCommand) (discussionsession.ConversationHILModeDTO, error)
	RetryMessage(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.RetryMessageCommand) (discussionsession.ConversationDTO, error)
	RetryMessageStream(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.RetryMessageCommand) (<-chan discussionsession.StreamEventDTO, error)
	AbortConversation(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error)
	DeleteConversation(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) error
	ListAttachments(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.AttachmentPageDTO, error)
	UploadAttachment(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error)
	StartAttachmentDraft(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDraftDTO, error)
	DownloadAttachment(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string, attachmentID string) (discussionsession.AttachmentDownloadDTO, error)
}

type SchedulerService interface {
	CreateTask(ctx context.Context, viewer schedulerapp.ViewerRef, req schedulerapp.CreateTaskRequest) (schedulerapp.ScheduledTask, error)
	UpdateTask(ctx context.Context, viewer schedulerapp.ViewerRef, req schedulerapp.UpdateTaskRequest) (schedulerapp.ScheduledTask, error)
	ListTasks(ctx context.Context, query schedulerapp.ListTasksQuery) (schedulerapp.ScheduledTaskPage, error)
	GetTaskStats(ctx context.Context, tenantID string) (schedulerapp.ScheduledTaskStats, error)
	GetTask(ctx context.Context, scheduledTaskID string) (schedulerapp.ScheduledTask, error)
	CancelTask(ctx context.Context, viewer schedulerapp.ViewerRef, req schedulerapp.CancelTaskRequest) (schedulerapp.ScheduledTask, error)
	PrepareManualRun(ctx context.Context, viewer schedulerapp.ViewerRef, req schedulerapp.RunNowRequest) (schedulerapp.ScheduledRun, error)
	ListRuns(ctx context.Context, query schedulerapp.ListRunsQuery) (schedulerapp.ScheduledRunPage, error)
	ListRunInbox(ctx context.Context, viewer schedulerapp.ViewerRef, query schedulerapp.ListRunInboxQuery) (schedulerapp.ScheduledRunInboxPage, error)
	SetRunReadState(ctx context.Context, viewer schedulerapp.ViewerRef, req schedulerapp.SetRunReadStateRequest) (schedulerapp.ScheduledRunReadState, error)
	ListResponsibilityEvaluationItems(ctx context.Context, query schedulerapp.ListResponsibilityEvaluationItemsQuery) (schedulerapp.ResponsibilityEvaluationItemPage, error)
	OpenRunArtifact(ctx context.Context, query schedulerapp.OpenRunArtifactQuery) (schedulerapp.RunArtifactContent, error)
}

type SchedulerDispatcher interface {
	DispatchRun(ctx context.Context, task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) (schedulerapp.ScheduledRun, error)
}

type TodoService interface {
	CreateTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CreateTodoRequest) (todoapp.TodoItem, error)
	GetTodo(ctx context.Context, viewer todoapp.ViewerRef, todoID string) (todoapp.TodoItem, error)
	UpdateTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.UpdateTodoRequest) (todoapp.TodoItem, error)
	DeleteTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.DeleteTodoRequest) error
	ClaimTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ClaimTodoRequest) (todoapp.TodoItem, error)
	MoveTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.MoveTodoRequest) (todoapp.TodoItem, error)
	CompleteTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CompleteTodoRequest) (todoapp.TodoItem, error)
	CancelTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CancelTodoRequest) (todoapp.TodoItem, error)
	ReopenTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ReopenTodoRequest) (todoapp.TodoItem, error)
	ListTodos(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListTodosQuery) (todoapp.TodoPage, error)
	GetTodoExecutionProgress(ctx context.Context, viewer todoapp.ViewerRef, todoID string) (todoapp.TodoExecutionSummary, error)
	GetTodoExecutionPlans(ctx context.Context, viewer todoapp.ViewerRef, todoID string) (todoapp.TodoExecutionPlanPage, error)
	CreateIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CreateIdeaRequest) (todoapp.IdeaItem, error)
	GetIdea(ctx context.Context, viewer todoapp.ViewerRef, ideaID string) (todoapp.IdeaItem, error)
	UpdateIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.UpdateIdeaRequest) (todoapp.IdeaItem, error)
	TransitionIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.TransitionIdeaRequest) (todoapp.IdeaItem, error)
	VoteIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.VoteIdeaRequest) (todoapp.IdeaItem, error)
	DeleteIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.DeleteIdeaRequest) error
	ArchiveIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ArchiveIdeaRequest) (todoapp.IdeaItem, error)
	RestoreIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.RestoreIdeaRequest) (todoapp.IdeaItem, error)
	ConvertIdeaToTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ConvertIdeaToTodoRequest) (todoapp.IdeaConversionResult, error)
	ListIdeas(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListIdeasQuery) (todoapp.IdeaPage, error)
	GetWorkHubSummary(ctx context.Context, viewer todoapp.ViewerRef) (todoapp.WorkHubSummary, error)
	SuggestDraft(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.SuggestDraftRequest) (todoapp.DraftSuggestion, error)
	CreateIssueComment(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CreateIssueCommentRequest) (todoapp.IssueComment, error)
	ListIssueComments(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListIssueCommentsQuery) (todoapp.IssueCommentPage, error)
}

type AuthService interface {
	BeginLogin(ctx context.Context, req userauth.BeginLoginRequest) (userauth.LoginChallengeDTO, error)
	CompleteLogin(ctx context.Context, req userauth.CompleteLoginRequest) (userauth.SessionDTO, error)
	AuthenticateToken(ctx context.Context, accessToken string) (userauth.SessionDTO, error)
	AuthenticatePrincipal(ctx context.Context, accessToken string) (identity.Principal, error)
	RevokeSession(ctx context.Context, accessToken string) error
	ListIPLocks(ctx context.Context, query userauth.IPBlockQuery) (userauth.IPBlockPageDTO, error)
	UnlockIP(ctx context.Context, ipAddress string, actorID string, reason string) error
}

type BearerAuthService interface {
	AuthenticateBearer(ctx context.Context, accessToken string) (identity.Principal, error)
}

type IdentityAuthService interface {
	LocalLoginEnabled(ctx context.Context) bool
	Providers(ctx context.Context) []identity.ProviderInfo
	BeginBrowserLogin(ctx context.Context, provider string, req identity.BeginLoginRequest) (identity.LoginRedirect, error)
	BeginBrowserLogout(ctx context.Context, provider string, req identity.BeginLogoutRequest) (identity.LoginRedirect, error)
	GetBrowserLoginFlow(ctx context.Context, provider string, req identity.LoginFlowRequest) (identity.BrowserLoginFlow, error)
	CompleteBrowserLogin(ctx context.Context, provider string, req identity.CompleteLoginRequest) (userauth.SessionDTO, error)
}

type IdentityAdminService interface {
	ListSubjects(ctx context.Context, query identity.SubjectQuery) (identity.SubjectPage, error)
	LinkSubject(ctx context.Context, req identity.LinkSubjectRequest) (identity.IdentitySubject, error)
	DisableSubject(ctx context.Context, id string) (identity.IdentitySubject, error)
	EnableSubject(ctx context.Context, id string) (identity.IdentitySubject, error)
}

type ProfileIdentityBindingService interface {
	GetBinding(ctx context.Context, agentProfileID string) (identity.SelfServiceBindingDTO, error)
	BeginBinding(ctx context.Context, agentProfileID string, returnTo string) (identity.LoginRedirect, error)
	CompleteBinding(ctx context.Context, agentProfileID string, req identity.CompleteLoginRequest) (identity.SelfServiceBindingDTO, error)
	Unbind(ctx context.Context, agentProfileID string) (identity.SelfServiceBindingDTO, error)
	BindingReturnTo(state string) string
}

type PersonalAssistantService interface {
	GetPersonalAssistant(context.Context, devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantView, error)
	UpdatePersonalAssistant(context.Context, devicedirectory.OwnerScope, devicedirectory.UpdatePersonalAssistantInput) (devicedirectory.PersonalAssistantView, error)
	GetPersonalAssistantNotifications(context.Context, devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantNotificationPreferences, error)
	UpdatePersonalAssistantNotifications(context.Context, devicedirectory.OwnerScope, devicedirectory.UpdatePersonalAssistantNotificationsInput) (devicedirectory.PersonalAssistantNotificationPreferences, error)
	GetPersonalAssistantAvatar(context.Context, devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantAvatar, error)
	SavePersonalAssistantAvatar(context.Context, devicedirectory.OwnerScope, string, []byte) (devicedirectory.PersonalAssistantAvatarInfo, error)
	DeletePersonalAssistantAvatar(context.Context, devicedirectory.OwnerScope) error
}

type OIDCLoginConsentService interface {
	AcceptLogin(ctx context.Context, challenge string, principal identity.Principal) (identity.OAuth2Redirect, error)
	AcceptConsent(ctx context.Context, challenge string, principal identity.Principal) (identity.OAuth2Redirect, error)
	ApproveConsent(ctx context.Context, challenge string, principal identity.Principal) (identity.OAuth2Redirect, error)
	PreviewConsent(ctx context.Context, challenge string, principal identity.Principal) (identity.OAuth2ConsentSummary, error)
	RejectConsent(ctx context.Context, challenge string, principal identity.Principal) (identity.OAuth2Redirect, error)
}

type OIDCClientAdminService interface {
	ListClients(ctx context.Context) ([]identity.OIDCClient, error)
	SaveClient(ctx context.Context, client identity.OIDCClient) (identity.OIDCClient, error)
}

type RouterOptions struct {
	LifecycleContext           context.Context
	Readiness                  ReadinessProbe
	Environment                string
	AuthRequireHTTPS           bool
	TrustedProxyCIDRs          []string
	TrustedViewerHeaders       bool
	InternalViewerAuth         string
	RuntimeMCPAuth             string
	Auth                       AuthService
	BearerAuth                 BearerAuthService
	IdentityAuth               IdentityAuthService
	IdentityAdmin              IdentityAdminService
	ProfileIdentity            ProfileIdentityBindingService
	PersonalAssistant          PersonalAssistantService
	PersonalAssistantSpeech    PersonalAssistantSpeechService
	OIDCLoginConsent           OIDCLoginConsentService
	OIDCClientAdmin            OIDCClientAdminService
	OAuth2LogoutReturnOrigins  []string
	MarketingOAuth2CallbackURL string
	Realtime                   http.Handler
	WebsiteAdvisor             http.Handler
	WebsiteKnowledgeGovernance websitechannel.WebsiteFAQGovernanceStore
	WebsiteFAQCatalog          *websitechannel.WebsiteFAQCatalog
	MissionQuery               MissionQueryService
	MissionCommand             MissionCommandService
	MissionStream              wstransport.StreamSource
	ProjectCommand             ProjectCommandService
	DevelopmentProjects        DevelopmentProjectService
	SCMConnections             SCMConnectionService
	Discussions                DiscussionService
	Approvals                  ApprovalService
	HIL                        HILService
	MarketingWechatApproval    MarketingWechatApprovalRoute
	TodoAutomation             TodoAutomationService
	ResourceQuery              ResourceQueryService
	ResourceCommand            ResourceCommandService
	MemoryQuery                MemoryQueryService
	MemoryCommand              MemoryCommandService
	MemoryDistill              MemoryDistillService
	Operations                 OperationsService
	Responsibilities           *responsibility.OperationsService
	WorkforceAvatars           WorkforceAvatarStore
	KnowledgeQuery             KnowledgeQueryService
	KnowledgeSearch            KnowledgeSearchService
	KnowledgeCommand           KnowledgeCommandService
	KnowledgeProviders         KnowledgeProviderService
	KnowledgeWorkspace         KnowledgeWorkspaceService
	KnowledgeProfiles          KnowledgeProfileStore
	Settings                   SettingsService
	SettingsSecrets            SettingsSecretResolver
	TaskNotifications          *notificationapp.Service
	Scheduler                  SchedulerService
	SchedulerDispatcher        SchedulerDispatcher
	Todo                       TodoService
	RuntimeDiscovery           RuntimeDiscoveryService
	AgentTraces                AgentTraceService
	GatewaySettings            *gwconfig.Store
	GatewaySettingsChanged     func(context.Context, gwconfig.Config) error
	GatewayBindingsChanged     func(context.Context, gwconfig.Config) error
	GatewayChannelBindings     *gwbindings.Store
	GatewayDeliveries          *gwoutbound.Service
	GatewayAccounts            channel.AccountStatusProvider
	FeishuRegistrationClient   feishu.RegistrationClient
	AgentProfiles              AgentProfileDirectoryStore
	MCPGateway                 *mcptransport.Gateway
	Permissions                []PermissionCapabilityDTO
	AccessControl              AccessControlService
	Authorization              AuthorizationService
	RoleAuthorization          RoleAuthorizationService
	AuthorizationDiagnostics   AuthorizationDiagnosticsService
	ClientDiagnostics          ClientDiagnosticsService
	ToolResultPolicy           contextgraph.PolicyProvider
	OntologyScopes             OntologyScopeService
	OntologySchema             OntologySchemaService
	OntologyContext            OntologyContextService
	OntologyInference          OntologyInferenceService
	OntologyExecutions         OntologyExecutionService
	OntologyActions            OntologyActionService
	OntologyReliability        OntologyReliabilityService
	OntologyProblems           OntologyProblemService
	OntologyLineage            OntologyLineageService
	OntologyGovernance         OntologyGovernanceService
	OntologyGovernanceCommand  OntologyGovernanceCommandService
	OntologyProjection         OntologyProjectionService
	OntologyProjectionCommand  OntologyProjectionCommandService
	AuditRecorder              AuditRecorder
	HTTPAuditEnabled           bool
	Logger                     RequestLogger
}

type Router struct {
	readiness                  ReadinessProbe
	environment                string
	authRequireHTTPS           bool
	deploymentSecurity         deploymentSecurityConfig
	auth                       AuthService
	bearerAuth                 BearerAuthService
	identityAuth               IdentityAuthService
	identityAdmin              IdentityAdminService
	profileIdentity            ProfileIdentityBindingService
	personalAssistant          PersonalAssistantService
	personalAssistantSpeech    PersonalAssistantSpeechService
	oidcLoginConsent           OIDCLoginConsentService
	oidcClientAdmin            OIDCClientAdminService
	oauth2LogoutReturnOrigins  []string
	marketingOAuth2CallbackURL string
	realtime                   http.Handler
	websiteAdvisor             http.Handler
	websiteKnowledgeGovernance websitechannel.WebsiteFAQGovernanceStore
	websiteFAQCatalog          *websitechannel.WebsiteFAQCatalog
	missionQuery               MissionQueryService
	missionCommand             MissionCommandService
	missionStream              wstransport.StreamSource
	projectCommand             ProjectCommandService
	developmentProjects        DevelopmentProjectService
	scmConnections             SCMConnectionService
	discussions                DiscussionService
	approvals                  ApprovalService
	hil                        HILService
	marketingWechatApproval    MarketingWechatApprovalRoute
	todoAutomation             TodoAutomationService
	resourceQuery              ResourceQueryService
	resourceCommand            ResourceCommandService
	memoryQuery                MemoryQueryService
	memoryCommand              MemoryCommandService
	memoryDistill              MemoryDistillService
	operations                 OperationsService
	responsibilities           *responsibility.OperationsService
	workforceAvatars           WorkforceAvatarStore
	knowledgeQuery             KnowledgeQueryService
	knowledgeSearch            KnowledgeSearchService
	knowledgeCommand           KnowledgeCommandService
	knowledgeProviders         KnowledgeProviderService
	knowledgeWorkspace         KnowledgeWorkspaceService
	knowledgeProfiles          KnowledgeProfileStore
	settings                   SettingsService
	settingsSecrets            SettingsSecretResolver
	taskNotifications          *notificationapp.Service
	scheduler                  SchedulerService
	schedulerDispatcher        SchedulerDispatcher
	todo                       TodoService
	runtimeDiscovery           RuntimeDiscoveryService
	agentTraces                AgentTraceService
	gatewaySettings            *gwconfig.Store
	gatewaySettingsChanged     func(context.Context, gwconfig.Config) error
	gatewayBindingsChanged     func(context.Context, gwconfig.Config) error
	gatewayChannelBindings     *gwbindings.Store
	gatewayDeliveries          *gwoutbound.Service
	gatewayAccounts            channel.AccountStatusProvider
	gatewayFeishuRegistrations *feishuRegistrationManager
	agentProfiles              AgentProfileDirectoryStore
	gatewayWeixinLogins        *gatewayWeixinLoginManager
	mcpGateway                 *mcptransport.Gateway
	permissions                []PermissionCapabilityDTO
	accessControl              AccessControlService
	authorization              AuthorizationService
	roleAuthorization          RoleAuthorizationService
	authorizationDiagnostics   AuthorizationDiagnosticsService
	clientDiagnostics          ClientDiagnosticsService
	toolResultPolicy           contextgraph.PolicyProvider
	ontologyScopes             OntologyScopeService
	ontologySchema             OntologySchemaService
	ontologyContext            OntologyContextService
	ontologyInference          OntologyInferenceService
	ontologyExecutions         OntologyExecutionService
	ontologyActions            OntologyActionService
	ontologyReliability        OntologyReliabilityService
	ontologyProblems           OntologyProblemService
	ontologyLineage            OntologyLineageService
	ontologyGovernance         OntologyGovernanceService
	ontologyGovernanceCommand  OntologyGovernanceCommandService
	ontologyProjection         OntologyProjectionService
	ontologyProjectionCommand  OntologyProjectionCommandService
	auditRecorder              AuditRecorder
	httpAuditEnabled           bool
	logger                     RequestLogger
}

type AccessControlService interface {
	BuildView(ctx context.Context, capabilities []accesscontrol.CapabilityDTO) (accesscontrol.PolicyViewDTO, error)
	SavePolicy(ctx context.Context, policy accesscontrol.PolicyDTO) (accesscontrol.PolicyDTO, error)
	Authorize(ctx context.Context, req accesscontrol.AuthorizeRequest) (bool, error)
}

type AuthorizationService interface {
	Can(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef) error
}

type RoleAuthorizationService interface {
	AuthorizeRolePermission(ctx context.Context, principal identity.Principal, permission string, objects []identity.ObjectRef) (bool, error)
}

type AuthorizationDiagnosticsService interface {
	GetAuthorizationRelationBackfillDiagnostics(ctx context.Context) (authorizationapp.RelationBackfillResult, error)
	GetAuthorizationEnforceTransitionDiagnostics(ctx context.Context) (authorizationapp.EnforceTransitionDiagnostics, error)
}

func NewRouter(opts RouterOptions) http.Handler {
	settings := opts.Settings
	if settings == nil {
		settings = newMemorySettingsService(defaultSettings())
	}
	runtimeDiscovery := opts.RuntimeDiscovery
	if runtimeDiscovery == nil {
		runtimeDiscovery = newDefaultRuntimeDiscoveryService()
	}
	permissions := opts.Permissions
	if permissions == nil {
		permissions = defaultPermissionCapabilities()
	}
	websiteFAQCatalog := opts.WebsiteFAQCatalog
	if websiteFAQCatalog == nil {
		websiteFAQCatalog = websitechannel.NewWebsiteFAQCatalog(nil)
	}
	router := &Router{
		readiness:        opts.Readiness,
		environment:      opts.Environment,
		authRequireHTTPS: opts.AuthRequireHTTPS,
		deploymentSecurity: deploymentSecurityConfig{
			trustedProxyCIDRs:    append([]string(nil), opts.TrustedProxyCIDRs...),
			trustedViewerHeaders: opts.TrustedViewerHeaders,
			internalViewerAuth:   strings.TrimSpace(opts.InternalViewerAuth),
			runtimeMCPAuth:       strings.TrimSpace(opts.RuntimeMCPAuth),
		},
		auth:                       opts.Auth,
		bearerAuth:                 opts.BearerAuth,
		identityAuth:               opts.IdentityAuth,
		identityAdmin:              opts.IdentityAdmin,
		profileIdentity:            opts.ProfileIdentity,
		personalAssistant:          opts.PersonalAssistant,
		personalAssistantSpeech:    opts.PersonalAssistantSpeech,
		oidcLoginConsent:           opts.OIDCLoginConsent,
		oidcClientAdmin:            opts.OIDCClientAdmin,
		oauth2LogoutReturnOrigins:  append([]string(nil), opts.OAuth2LogoutReturnOrigins...),
		marketingOAuth2CallbackURL: strings.TrimSpace(opts.MarketingOAuth2CallbackURL),
		realtime:                   opts.Realtime,
		websiteAdvisor:             opts.WebsiteAdvisor,
		websiteKnowledgeGovernance: opts.WebsiteKnowledgeGovernance,
		websiteFAQCatalog:          websiteFAQCatalog,
		missionQuery:               opts.MissionQuery,
		missionCommand:             opts.MissionCommand,
		missionStream:              opts.MissionStream,
		projectCommand:             opts.ProjectCommand,
		developmentProjects:        opts.DevelopmentProjects,
		scmConnections:             opts.SCMConnections,
		discussions:                opts.Discussions,
		approvals:                  opts.Approvals,
		hil:                        opts.HIL,
		marketingWechatApproval:    MarketingWechatApprovalRoute{Service: opts.MarketingWechatApproval.Service, Authorization: strings.TrimSpace(opts.MarketingWechatApproval.Authorization)},
		todoAutomation:             opts.TodoAutomation,
		resourceQuery:              opts.ResourceQuery,
		resourceCommand:            opts.ResourceCommand,
		memoryQuery:                opts.MemoryQuery,
		memoryCommand:              opts.MemoryCommand,
		memoryDistill:              opts.MemoryDistill,
		operations:                 opts.Operations,
		responsibilities:           opts.Responsibilities,
		workforceAvatars:           opts.WorkforceAvatars,
		knowledgeQuery:             opts.KnowledgeQuery,
		knowledgeSearch:            opts.KnowledgeSearch,
		knowledgeCommand:           opts.KnowledgeCommand,
		knowledgeProviders:         opts.KnowledgeProviders,
		knowledgeWorkspace:         opts.KnowledgeWorkspace,
		knowledgeProfiles:          opts.KnowledgeProfiles,
		settings:                   settings,
		settingsSecrets:            opts.SettingsSecrets,
		taskNotifications:          opts.TaskNotifications,
		scheduler:                  opts.Scheduler,
		schedulerDispatcher:        opts.SchedulerDispatcher,
		todo:                       opts.Todo,
		runtimeDiscovery:           runtimeDiscovery,
		agentTraces:                opts.AgentTraces,
		gatewaySettings:            opts.GatewaySettings,
		gatewaySettingsChanged:     opts.GatewaySettingsChanged,
		gatewayBindingsChanged:     opts.GatewayBindingsChanged,
		gatewayChannelBindings:     opts.GatewayChannelBindings,
		gatewayDeliveries:          opts.GatewayDeliveries,
		gatewayAccounts:            opts.GatewayAccounts,
		gatewayFeishuRegistrations: newFeishuRegistrationManager(opts.FeishuRegistrationClient, opts.LifecycleContext),
		agentProfiles:              opts.AgentProfiles,
		gatewayWeixinLogins:        newGatewayWeixinLoginManager(!strings.EqualFold(strings.TrimSpace(opts.Environment), "production") && !strings.EqualFold(strings.TrimSpace(opts.Environment), "prod")),
		mcpGateway:                 opts.MCPGateway,
		permissions:                append([]PermissionCapabilityDTO(nil), permissions...),
		accessControl:              opts.AccessControl,
		authorization:              opts.Authorization,
		roleAuthorization:          opts.RoleAuthorization,
		authorizationDiagnostics:   opts.AuthorizationDiagnostics,
		toolResultPolicy:           opts.ToolResultPolicy,
		ontologyScopes:             opts.OntologyScopes,
		ontologySchema:             opts.OntologySchema,
		ontologyContext:            opts.OntologyContext,
		ontologyInference:          opts.OntologyInference,
		ontologyExecutions:         opts.OntologyExecutions,
		ontologyActions:            opts.OntologyActions,
		ontologyReliability:        opts.OntologyReliability,
		ontologyProblems:           opts.OntologyProblems,
		ontologyLineage:            opts.OntologyLineage,
		ontologyGovernance:         opts.OntologyGovernance,
		ontologyGovernanceCommand:  opts.OntologyGovernanceCommand,
		ontologyProjection:         opts.OntologyProjection,
		ontologyProjectionCommand:  opts.OntologyProjectionCommand,
		auditRecorder:              opts.AuditRecorder,
		httpAuditEnabled:           opts.HTTPAuditEnabled || strings.EqualFold(opts.Environment, "production"),
		logger:                     opts.Logger,
	}
	router.clientDiagnostics = opts.ClientDiagnostics
	return WithRequestLogger(router, opts.Logger)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req != nil {
		req = withDeploymentSecurity(req, r.deploymentSecurity)
	}
	if r.shouldAuditHTTPRequest(req) {
		r.serveHTTPWithAudit(w, req)
		return
	}
	r.serveHTTP(w, req)
}

func (r *Router) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if r.handleMarketingWechatApprovalRoute(w, req) {
		return
	}
	if r.handleWebsiteAdvisorRoute(w, req) {
		return
	}
	if r.handleAuthRoute(w, req) {
		return
	}
	if r.handleOAuth2Route(w, req) {
		return
	}
	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] == "mcp" {
		if !r.authorizeMCPRoute(w, req) {
			return
		}
		r.handleMCP(w, req)
		return
	}
	if !r.applyAuthenticatedViewer(w, req) {
		return
	}
	if unsafeHTTPMethod(req.Method) && authCredentialFromRequest(req).source == authCredentialCookie && !cookieMutationOriginAllowed(req) {
		status, body := MapError(operations.ErrPermissionDenied, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	if !r.authorizeRegisteredRoute(w, req) {
		return
	}
	if req.URL.Path == "/api/diagnostics/client-errors" && req.Method == http.MethodPost {
		r.handleClientDiagnostics(w, req)
		return
	}
	if r.handleAgentTraceRoute(w, req, parts) {
		return
	}
	if r.handleWebsiteKnowledgeGovernanceRoute(w, req, parts) {
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "realtime" && parts[2] == "connect" {
		r.handleRealtimeConnect(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 && parts[0] == "api" && parts[1] == "health" {
		r.handleHealth(w, req)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "gateway" {
		if req.Method == http.MethodGet {
			r.handleGetGatewaySettings(w, req)
			return
		}
		if req.Method == http.MethodPut {
			r.handlePutGatewaySettings(w, req)
			return
		}
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "gateway" && parts[3] == "raw" {
		if req.Method == http.MethodPut {
			r.handlePutGatewaySettingsRaw(w, req)
			return
		}
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "gateway" && parts[3] == "accounts" && req.Method == http.MethodGet {
		r.handleGetGatewayAccounts(w, req)
		return
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "gateway" && parts[3] == "feishu" && parts[4] == "registrations" && req.Method == http.MethodPost {
		r.handleStartFeishuRegistration(w, req)
		return
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "gateway" && parts[3] == "feishu" && parts[4] == "registrations" {
		if req.Method == http.MethodGet {
			r.handleGetFeishuRegistration(w, req, parts[5])
			return
		}
		if req.Method == http.MethodDelete {
			r.handleCancelFeishuRegistration(w, req, parts[5])
			return
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "gateway" && parts[2] == "deliveries" && req.Method == http.MethodPost {
		r.handleCreateGatewayDelivery(w, req)
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "gateway" && parts[2] == "deliveries" && req.Method == http.MethodGet {
		r.handleGetGatewayDelivery(w, req, parts[3])
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "weixin" {
		if req.Method == http.MethodGet {
			r.handleGetProfileWeixin(w, req)
			return
		}
		if req.Method == http.MethodDelete {
			r.handleDeleteProfileWeixin(w, req)
			return
		}
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "personal-assistant" && parts[3] == "audio" && (parts[4] == "speech" || parts[4] == "transcriptions") {
		r.handlePersonalAssistantSpeech(w, req)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "personal-assistant" {
		switch req.Method {
		case http.MethodGet:
			r.handleGetPersonalAssistant(w, req)
		case http.MethodPost:
			r.handlePostPersonalAssistant(w, req)
		case http.MethodPut:
			r.handlePutPersonalAssistant(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "personal-assistant" && parts[3] == "avatar" {
		switch req.Method {
		case http.MethodGet:
			r.handleGetPersonalAssistantAvatar(w, req)
		case http.MethodPut:
			r.handlePutPersonalAssistantAvatar(w, req)
		case http.MethodDelete:
			r.handleDeletePersonalAssistantAvatar(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "personal-assistant" && parts[3] == "notifications" {
		switch req.Method {
		case http.MethodGet:
			r.handleGetPersonalAssistantNotifications(w, req)
		case http.MethodPut:
			r.handlePutPersonalAssistantNotifications(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "gateway-bindings" && req.Method == http.MethodGet {
		r.handleGetProfileGatewayBindings(w, req)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "identity" && parts[3] == identity.ProviderFeishu {
		if len(parts) == 4 {
			switch req.Method {
			case http.MethodGet:
				r.handleGetProfileFeishuIdentity(w, req)
			case http.MethodDelete:
				r.handleDeleteProfileFeishuIdentity(w, req)
			default:
				http.NotFound(w, req)
			}
			return
		}
		if len(parts) == 5 && parts[4] == "begin" && req.Method == http.MethodGet {
			r.handleBeginProfileFeishuIdentity(w, req)
			return
		}
		if len(parts) == 5 && parts[4] == "callback" && req.Method == http.MethodGet {
			r.handleCompleteProfileFeishuIdentity(w, req)
			return
		}
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "gateway-bindings" && parts[4] == "code" && req.Method == http.MethodPost {
		r.handleCreateProfileGatewayBindingCode(w, req, parts[3])
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "gateway-bindings" && req.Method == http.MethodDelete {
		r.handleDeleteProfileGatewayBinding(w, req, parts[3])
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "weixin" && parts[3] == "default-agent" {
		if req.Method == http.MethodPut {
			r.handlePutProfileWeixinDefaultAgent(w, req)
			return
		}
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "weixin" && parts[3] == "login" && parts[4] == "start" {
		if req.Method == http.MethodPost {
			r.handleStartProfileWeixinLogin(w, req)
			return
		}
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "weixin" && parts[3] == "login" {
		if req.Method == http.MethodGet {
			r.handleGetProfileWeixinLoginStatus(w, req, parts[4])
			return
		}
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "profile" && parts[2] == "weixin" && parts[3] == "login" && parts[5] == "verify-code" {
		if req.Method == http.MethodPost {
			r.handleSubmitProfileWeixinVerifyCode(w, req, parts[4])
			return
		}
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "runtime" && parts[3] == "discovery" {
		if req.Method == http.MethodGet {
			r.handleRuntimeDiscovery(w, req)
			return
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "image-generation" {
		if req.Method == http.MethodGet {
			r.handleGetImageGenerationSettings(w, req)
			return
		}
		if req.Method == http.MethodPut {
			r.handlePutImageGenerationSettings(w, req)
			return
		}
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "image-generation" && parts[3] == "test" && req.Method == http.MethodPost {
		r.handleTestImageGenerationConnection(w, req)
		return
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "settings" && parts[2] == "knowledge" && parts[3] == "pageindex" && parts[4] == "test" {
		if req.Method == http.MethodPost {
			r.handleTestPageIndexConnection(w, req)
			return
		}
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "settings" {
		if req.Method == http.MethodGet {
			r.handleGetSettings(w, req)
			return
		}
		if req.Method == http.MethodPut {
			r.handlePutSettings(w, req)
			return
		}
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "memory" {
		r.handleMemory(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "knowledge-providers" {
		r.handleKnowledgeProviders(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "knowledge-bases" {
		r.handleKnowledgeBases(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "notifications" {
		r.handleNotifications(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "scheduled-runs" {
		r.handleScheduledRuns(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "scheduled-tasks" {
		r.handleScheduledTasks(w, req, parts)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "todo-drafts" && parts[2] == "suggest" {
		if req.Method == http.MethodPost {
			r.handleSuggestTodoDraft(w, req)
			return
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "work-hub" && parts[2] == "summary" {
		if req.Method == http.MethodGet {
			r.handleWorkHubSummary(w, req)
			return
		}
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "todos" {
		if r.handleTodoDevelopmentArtifacts(w, req, parts) {
			return
		}
		r.handleTodos(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "responsibilities" {
		r.handleResponsibilities(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "responsibility-profiles" {
		r.handleResponsibilityProfiles(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "responsibility-policies" {
		r.handleResponsibilityPolicies(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "responsibility-blockers" {
		r.handleResponsibilityBlocker(w, req, parts)
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "responsibility-signals" {
		r.handleResponsibilitySignal(w, req)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "ideas" {
		r.handleIdeas(w, req, parts)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "knowledge-jobs" {
		r.handleKnowledgeJob(w, req, parts[2])
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "knowledge" && parts[2] == "search" {
		r.handleKnowledgeSearch(w, req)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "discussions" {
		if req.Method == http.MethodGet && len(parts) == 2 {
			r.handleListDiscussions(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "messages" {
			r.handleSendDiscussionMessage(w, req, "")
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "prewarm" {
			r.handlePrewarmDiscussion(w, req, "")
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "attachments" {
			r.handleStartDiscussionAttachmentDraft(w, req)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 3 {
			r.handleGetDiscussion(w, req, parts[2])
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 3 {
			r.handleDeleteDiscussion(w, req, parts[2])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "messages" {
			r.handleListDiscussionMessages(w, req, parts[2])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "attachments" {
			r.handleListDiscussionAttachments(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "attachments" {
			r.handleUploadDiscussionAttachment(w, req, parts[2])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 6 && parts[3] == "attachments" && parts[5] == "download" {
			r.handleDownloadDiscussionAttachment(w, req, parts[2], parts[4])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 6 && parts[3] == "attachments" && parts[5] == "preview" {
			r.handlePreviewDiscussionAttachment(w, req, parts[2], parts[4])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "members" {
			r.handleAddDiscussionMembers(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPatch && len(parts) == 4 && parts[3] == "workspace" {
			r.handleSetDiscussionWorkspace(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPatch && len(parts) == 4 && parts[3] == "hil-mode" {
			r.handleSetDiscussionHILMode(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "approvals" && parts[5] == "decision" {
			r.handleDiscussionApprovalDecision(w, req, parts[2], parts[4])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "abort" {
			r.handleAbortDiscussion(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "interjections" {
			r.handleInterjectDiscussion(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "prewarm" {
			r.handlePrewarmDiscussion(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "messages" {
			r.handleSendDiscussionMessage(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "messages" && parts[5] == "retry" {
			r.handleRetryDiscussionMessage(w, req, parts[2], parts[4])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 5 && parts[3] == "debug" && parts[4] == "latest" {
			r.handleGetLatestDiscussionDebug(w, req, parts[2])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 6 && parts[3] == "runs" && parts[5] == "debug" {
			r.handleGetDiscussionRunDebug(w, req, parts[2], parts[4])
			return
		}
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "missions" {
		if req.Method == http.MethodGet {
			r.handleListMissions(w, req)
			return
		}
		if req.Method == http.MethodPost {
			r.handleCreateMission(w, req)
			return
		}
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "development-projects" {
		r.handleDevelopmentProjects(w, req, parts)
		return
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "scm-connections" {
		r.handleSCMConnections(w, req, parts)
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "projects" {
		if req.Method == http.MethodGet {
			r.handleListProjects(w, req)
			return
		}
		if req.Method == http.MethodPost {
			r.handleCreateProject(w, req)
			return
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "projects" {
		if req.Method == http.MethodGet {
			r.handleGetProject(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPatch {
			r.handleUpdateProject(w, req, parts[2])
			return
		}
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "projects" && parts[3] == "archive" {
		if req.Method == http.MethodPost {
			r.handleArchiveProject(w, req, parts[2])
			return
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "scopes" && req.Method == http.MethodGet {
		r.handleListOntologyScopes(w, req)
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "scopes" && parts[3] == "resolve" && req.Method == http.MethodGet {
		r.handleResolveOntologyScope(w, req)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "inference" {
		r.handleOntologyInference(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "schema" {
		r.handleOntologySchema(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "context" {
		r.handleOntologyContext(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "executions" {
		r.handleOntologyExecutions(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "actions" {
		r.handleOntologyActions(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "reliability" {
		r.handleOntologyReliability(w, req, parts)
		return
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "problems" {
		r.handleOntologyProblems(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "lineage" {
		r.handleOntologyLineage(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "governance" {
		r.handleOntologyGovernance(w, req, parts)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "ontology" && parts[2] == "projections" {
		r.handleOntologyProjection(w, req, parts)
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 3 && parts[0] == "api" && parts[1] == "missions" {
		r.handleDeleteMission(w, req, missioncore.MissionID(parts[2]))
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "missions" {
		missionID := missioncore.MissionID(parts[2])
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "cancel" {
			r.handleCancelMission(w, req, missionID)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "control" {
			r.handleMissionControl(w, req, missionID)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "ops" {
			r.handleMissionOps(w, req, missionID)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && isMissionSubview(parts[3]) {
			r.handleMissionSubview(w, req, missionID, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "tasks" && parts[5] == "submit-result" {
			r.handleSubmitResult(w, req, missionID, missioncore.TaskID(parts[4]))
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "tasks" {
			r.handleCreateTask(w, req, missionID)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "tasks" && parts[5] == "assign" {
			r.handleAssignTask(w, req, missionID, missioncore.TaskID(parts[4]))
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "tasks" && parts[5] == "agent-runs" {
			r.handleStartAgentRun(w, req, missionID, missioncore.TaskID(parts[4]))
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "agent-runs" && parts[5] == "cancel" {
			r.handleCancelAgentRun(w, req, missionID, parts[4])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "agent-runs" && parts[5] == "retry" {
			r.handleRetryAgentRun(w, req, missionID, parts[4])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 6 && parts[3] == "agent-runs" && parts[5] == "recovery" {
			r.handleAgentRunRecovery(w, req, missionID, parts[4], false)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 7 && parts[3] == "agent-runs" && parts[5] == "recovery" && parts[6] == "reviews" {
			r.handleAgentRunRecovery(w, req, missionID, parts[4], true)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "qa" && parts[5] == "decision" {
			r.handleQADecision(w, req, missionID, parts[4])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[3] == "discussion" && parts[4] == "messages" {
			r.handlePostDiscussionMessage(w, req, missionID)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 7 && parts[3] == "discussion" && parts[4] == "messages" && parts[6] == "reply" {
			r.handleReplyDiscussionMessage(w, req, missionID, parts[5])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "stream" {
			r.handleMissionStream(w, req, missionID)
			return
		}
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "inbox" {
		if req.Method == http.MethodGet && len(parts) == 2 {
			r.handleListInbox(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "read" {
			r.handleMarkInboxRead(w, req, parts[2])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "close" {
			r.handleCloseInboxItem(w, req, parts[2])
			return
		}
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "approvals" {
		if req.Method == http.MethodGet && len(parts) == 2 {
			r.handleListApprovals(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "decision" {
			r.handleApprovalDecision(w, req, parts[2])
			return
		}
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "hil" {
		if parts[2] == "workflow-templates" {
			if req.Method == http.MethodGet && len(parts) == 3 {
				r.handleListHILWorkflowTemplates(w, req)
				return
			}
			if req.Method == http.MethodPost && len(parts) == 3 {
				r.handleCreateHILWorkflowTemplate(w, req)
				return
			}
			if req.Method == http.MethodGet && len(parts) == 4 {
				r.handleGetHILWorkflowTemplate(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPatch && len(parts) == 5 && parts[4] == "draft" {
				r.handleUpdateHILWorkflowTemplateDraft(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "publish" {
				r.handlePublishHILWorkflowTemplate(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "disable" {
				r.handleDisableHILWorkflowTemplate(w, req, parts[3])
				return
			}
		}
		if parts[2] == "workflow-template-bindings" && req.Method == http.MethodPut && len(parts) == 4 {
			r.handlePutHILWorkflowTemplateBinding(w, req, parts[3])
			return
		}
		if parts[2] == "workflow-instances" {
			if req.Method == http.MethodGet && len(parts) == 3 {
				r.handleListHILWorkflowInstances(w, req)
				return
			}
			if req.Method == http.MethodGet && len(parts) == 4 {
				r.handleGetHILWorkflowInstance(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "viewed" {
				r.handleHILWorkflowViewed(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "resubmit" {
				r.handleResubmitHILWorkflow(w, req, parts[3])
				return
			}
		}
		if parts[2] == "workflow-tasks" && req.Method == http.MethodPost && len(parts) == 5 {
			switch parts[4] {
			case "decision":
				r.handleDecideHILWorkflowTask(w, req, parts[3])
				return
			case "transfer":
				r.handleTransferHILWorkflowTask(w, req, parts[3], false)
				return
			case "reassign":
				r.handleTransferHILWorkflowTask(w, req, parts[3], true)
				return
			}
		}
		if parts[2] == "decisions" {
			if req.Method == http.MethodPost && len(parts) == 3 {
				r.handleCreateHILDecision(w, req)
				return
			}
			if req.Method == http.MethodGet && len(parts) == 3 {
				r.handleListHILDecisions(w, req)
				return
			}
			if req.Method == http.MethodGet && len(parts) == 4 {
				r.handleGetHILDecision(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "decision" {
				r.handleResolveHILDecision(w, req, parts[3])
				return
			}
		}
		if parts[2] == "councils" {
			if req.Method == http.MethodGet && len(parts) == 4 {
				r.handleGetHILCouncil(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "contributions" {
				r.handleSubmitHILContribution(w, req, parts[3])
				return
			}
			if req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "votes" {
				r.handleCastHILVote(w, req, parts[3])
				return
			}
		}
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "resources" {
		if req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "skill-bundles" {
			r.handleListSkillBundles(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "skill-bundles" {
			r.handleCreateSkillBundle(w, req)
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 4 && parts[2] == "skill-bundles" {
			r.handleDeleteSkillBundle(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "skill-bundles" && parts[4] == "skills" {
			r.handleAddSkillsToBundle(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 6 && parts[2] == "skill-bundles" && parts[4] == "skills" && parts[5] == "import" {
			r.handleImportSkillIntoBundle(w, req, parts[3])
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 6 && parts[2] == "skill-bundles" && parts[4] == "skills" {
			r.handleRemoveSkillFromBundle(w, req, parts[3], parts[5])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[2] == "tools" && parts[3] == "call" {
			r.handleCallGatewayTool(w, req)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "tools" {
			r.handleGatewayTools(w, req)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "memory-profiles" && parts[3] == "personal" {
			r.handleGetPersonalMemoryProfile(w, req)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "memory-profiles" {
			r.handleGetMemoryProfile(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPatch && len(parts) == 4 && parts[2] == "memory-profiles" {
			r.handleUpdateMemoryProfile(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[2] == "memory-profiles" && parts[3] == "digest" {
			r.handleBuildMemoryDigest(w, req)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "mcp-servers" && parts[3] == "discover" {
			r.handleDiscoverMCPServers(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "bindings" {
			r.handleBindResource(w, req)
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 3 && parts[2] == "bindings" {
			r.handleUnbindResource(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "tools" {
			r.handleRegisterTool(w, req)
			return
		}
		if req.Method == http.MethodPatch && len(parts) == 4 && parts[2] == "tools" {
			r.handleUpdateTool(w, req, parts[3])
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 4 && parts[2] == "tools" {
			r.handleDeleteTool(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "tools" && parts[4] == "disable" {
			r.handleDisableTool(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "mcp-servers" {
			r.handleRegisterMCPServer(w, req)
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "mcp-servers" {
			r.handleGetMCPServer(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPatch && len(parts) == 4 && parts[2] == "mcp-servers" {
			r.handleUpdateMCPServer(w, req, parts[3])
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 4 && parts[2] == "mcp-servers" {
			r.handleDeleteMCPServer(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "mcp-servers" && parts[4] == "sync" {
			r.handleSyncMCPServer(w, req, parts[3])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 5 && parts[2] == "mcp-servers" && parts[4] == "health" {
			r.handleMCPServerHealth(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "mcp-servers" && parts[4] == "disable" {
			r.handleDisableMCPServer(w, req, parts[3])
			return
		}
		if req.Method == http.MethodDelete && len(parts) == 4 && parts[2] == "skills" {
			r.handleDeleteSkill(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 4 && parts[2] == "skills" && parts[3] == "import" {
			r.handleImportSkill(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "skills" {
			r.handleCreateSkill(w, req)
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "skills" && parts[4] == "versions" {
			r.handleRegisterSkillVersion(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 7 && parts[2] == "skills" && parts[4] == "versions" && parts[6] == "promote" {
			r.handlePromoteSkillVersion(w, req, parts[3], parts[5])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 7 && parts[2] == "skills" && parts[4] == "versions" && parts[6] == "rollback" {
			r.handleRollbackSkillVersion(w, req, parts[3], parts[5])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "skills" {
			r.handleGetSkillDetail(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "skills" && parts[4] == "disable" {
			r.handleDisableSkill(w, req, parts[3])
			return
		}
		if req.Method == http.MethodGet && len(parts) == 5 && parts[2] == "skills" && parts[4] == "export" {
			r.handleExportSkill(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPut && len(parts) == 5 && parts[2] == "skills" && parts[4] == "skill-md" {
			r.handleSaveSkillMarkdown(w, req, parts[3])
			return
		}
		if req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "skills" && parts[4] == "folders" {
			r.handleCreateSkillFolder(w, req, parts[3])
			return
		}
		if len(parts) == 5 && parts[2] == "skills" && parts[4] == "files" {
			switch req.Method {
			case http.MethodGet:
				r.handleGetSkillFile(w, req, parts[3])
			case http.MethodPut:
				r.handleSaveSkillFile(w, req, parts[3])
			case http.MethodPost:
				r.handleUploadSkillFile(w, req, parts[3])
			case http.MethodDelete:
				r.handleDeleteSkillFile(w, req, parts[3])
			default:
				http.NotFound(w, req)
			}
			return
		}
		if req.Method == http.MethodGet && len(parts) == 6 && parts[2] == "skills" && parts[4] == "files" && parts[5] == "download" {
			r.handleDownloadSkillFile(w, req, parts[3])
			return
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "identity" && parts[2] == "subjects" {
		switch req.Method {
		case http.MethodGet:
			r.handleListIdentitySubjects(w, req)
		case http.MethodPost:
			r.handleLinkIdentitySubject(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "oauth2" && parts[2] == "clients" {
		switch req.Method {
		case http.MethodGet:
			r.handleListOIDCClients(w, req)
		case http.MethodPost:
			r.handleSaveOIDCClient(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "api" && parts[1] == "identity" && parts[2] == "subjects" {
		r.handleDisableIdentitySubject(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[0] == "api" && parts[1] == "identity" && parts[2] == "subjects" && parts[4] == "enable" {
		r.handleEnableIdentitySubject(w, req, parts[3])
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" {
		r.handleListWorkforceMembers(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 5 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "channel-bindings" {
		r.handleGetWorkforceChannelBindings(w, req, parts[3])
		return
	}
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "channel-bindings" && parts[5] == "feishu" {
		switch req.Method {
		case http.MethodPut:
			r.handlePutWorkforceChannelBinding(w, req, parts[3], parts[6])
		case http.MethodDelete:
			r.handleDeleteWorkforceChannelBinding(w, req, parts[3], parts[6])
		default:
			http.NotFound(w, req)
		}
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "members" {
		r.handleListTalentMarketMembers(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "import" && parts[3] == "preview" {
		r.handlePreviewTalentMarketImport(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "import" && parts[3] == "commit" {
		r.handleCommitTalentMarketImport(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 3 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "export" {
		r.handleExportTalentMarket(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "members" && parts[3] == "delete-batch" {
		r.handleDeleteTalentMarketMembers(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 3 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" {
		r.handleCreateWorkforceMember(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "avatar-defaults" {
		r.handleListWorkforceAvatarDefaults(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "avatar-defaults" {
		r.handleGetWorkforceAvatarDefault(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPut && len(parts) == 6 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "avatar" && parts[5] == "default" {
		r.handlePutWorkforceAvatarDefault(w, req, parts[3])
		return
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "avatar" {
		switch req.Method {
		case http.MethodGet:
			r.handleGetWorkforceAvatar(w, req, parts[3])
		case http.MethodPut:
			r.handlePutWorkforceAvatar(w, req, parts[3])
		case http.MethodDelete:
			r.handleDeleteWorkforceAvatar(w, req, parts[3])
		default:
			http.NotFound(w, req)
		}
		return
	}
	if (req.Method == http.MethodPut || req.Method == http.MethodPatch) && len(parts) == 4 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" {
		r.handleUpsertWorkforceMember(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "release-to-market" {
		r.handleReleaseWorkforceMember(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "members" && parts[4] == "hire" {
		r.handleHireTalentFromMarket(w, req, parts[3])
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "api" && parts[1] == "talent-market" && parts[2] == "members" {
		r.handleDeleteTalentMarketMember(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "enable" {
		r.handleSetWorkforceMemberEnabled(w, req, parts[3], true)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[0] == "api" && parts[1] == "workforce" && parts[2] == "members" && parts[4] == "disable" {
		r.handleSetWorkforceMemberEnabled(w, req, parts[3], false)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "org" && parts[2] == "graph" {
		r.handleOrgGraph(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "org" && parts[2] == "roles" {
		r.handleListOrganizationRoles(w, req)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "org" && parts[2] == "role-assignments" {
		if req.Method == http.MethodGet {
			r.handleListOrganizationRoleAssignments(w, req)
			return
		}
		if req.Method == http.MethodPut {
			r.handlePutOrganizationRoleAssignments(w, req)
			return
		}
	}
	if req.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "api" && parts[1] == "org" && parts[2] == "role-assignments" {
		r.handleDeleteOrganizationRoleAssignment(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPost && len(parts) == 2 && parts[0] == "api" && parts[1] == "org" {
		r.handleCreateOrganization(w, req)
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 3 && parts[0] == "api" && parts[1] == "org" {
		r.handleDeleteOrganization(w, req, parts[2])
		return
	}
	if req.Method == http.MethodPost && len(parts) == 3 && parts[0] == "api" && parts[1] == "org" && parts[2] == "units" {
		r.handleCreateOrganizationUnit(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 3 && parts[0] == "api" && parts[1] == "org" && parts[2] == "positions" {
		r.handleCreateOrganizationPosition(w, req)
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "api" && parts[1] == "org" && parts[2] == "positions" {
		r.handleDeleteOrganizationPosition(w, req, parts[3])
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "api" && parts[1] == "org" && parts[2] == "units" {
		r.handleDeleteOrganizationUnit(w, req, parts[3])
		return
	}
	if req.Method == http.MethodPut && len(parts) == 5 && parts[0] == "api" && parts[1] == "org" && parts[2] == "nodes" && parts[4] == "owner" {
		r.handleUpdateOrganizationOwner(w, req, parts[3])
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "matrix" {
		r.handlePermissionMatrix(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "decisions" {
		r.handlePermissionDecision(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "capabilities" {
		r.handlePermissionCapabilities(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && parts[0] == "api" && parts[1] == "authorization" && parts[2] == "relation-backfill" && parts[3] == "diagnostics" {
		r.handleAuthorizationRelationBackfillDiagnostics(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && parts[0] == "api" && parts[1] == "authorization" && parts[2] == "enforce-transition" && parts[3] == "diagnostics" {
		r.handleAuthorizationEnforceTransitionDiagnostics(w, req)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "policy" {
		if req.Method == http.MethodGet {
			r.handlePermissionPolicy(w, req)
			return
		}
		if req.Method == http.MethodPut {
			r.handleUpdatePermissionPolicy(w, req)
			return
		}
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "roles" {
		r.handleListPermissionRoles(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "packages" {
		r.handleListPermissionPackages(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 5 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "packages" && parts[4] == "capabilities" {
		r.handleListPermissionPackageCapabilities(w, req, parts[3])
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "effective-grants" {
		r.handleListEffectivePermissionGrants(w, req)
		return
	}
	if req.Method == http.MethodPut && len(parts) == 4 && parts[0] == "api" && parts[1] == "permissions" && parts[2] == "policy" && parts[3] == "batch" {
		r.handleApplyPermissionPolicyBatch(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 && parts[0] == "api" && parts[1] == "audit-log" {
		r.handleAuditLog(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "audit" && parts[2] == "ip-locks" {
		r.handleListAuthIPLocks(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[0] == "api" && parts[1] == "audit" && parts[2] == "ip-locks" && parts[3] == "unlock" {
		r.handleUnlockAuthIPLock(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 && parts[0] == "api" && parts[1] == "evaluations" {
		r.handleEvaluations(w, req)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 && parts[0] == "api" && parts[1] == "evaluations" && parts[2] == "stream" {
		r.handleEvaluationsStream(w, req)
		return
	}
	if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/resources/") {
		r.handleResources(w, req)
		return
	}
	writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
}

func (r *Router) handleWebsiteAdvisorRoute(w http.ResponseWriter, req *http.Request) bool {
	parts := strings.Split(req.URL.Path, "/")
	if len(parts) < 3 || parts[0] != "" || parts[1] != "api" || parts[2] != "advisor" {
		return false
	}
	validRoute := false
	switch {
	case len(parts) == 4 && parts[3] == "sessions":
		validRoute = req.Method == http.MethodPost || req.Method == http.MethodOptions
	case len(parts) == 4 && parts[3] == "faq-suggestions":
		validRoute = req.Method == http.MethodGet || req.Method == http.MethodOptions
	case len(parts) == 4 && parts[3] == "human-verification":
		validRoute = req.Method == http.MethodPost || req.Method == http.MethodOptions
	case len(parts) == 4 && (parts[3] == "account" || parts[3] == "conversations"):
		validRoute = req.Method == http.MethodGet || req.Method == http.MethodOptions
	case len(parts) == 5 && parts[3] == "conversations" && strings.TrimSpace(parts[4]) != "":
		validRoute = req.Method == http.MethodGet || req.Method == http.MethodDelete || req.Method == http.MethodOptions
	case len(parts) == 6 && parts[3] == "sessions" && strings.TrimSpace(parts[4]) != "" && (parts[5] == "messages" || parts[5] == "adopt" || parts[5] == "leads" || parts[5] == "feedback"):
		validRoute = req.Method == http.MethodPost || req.Method == http.MethodOptions
	case len(parts) == 6 && parts[3] == "conversations" && strings.TrimSpace(parts[4]) != "" && parts[5] == "resume":
		validRoute = req.Method == http.MethodPost || req.Method == http.MethodOptions
	}
	if r.websiteAdvisor != nil && validRoute {
		r.websiteAdvisor.ServeHTTP(w, req)
		return true
	}
	http.NotFound(w, req)
	return true
}

func isMissionSubview(name string) bool {
	switch name {
	case "team", "tasks", "progress", "evidence", "qa", "discussion":
		return true
	default:
		return false
	}
}

func (r *Router) authorizeMCPRoute(w http.ResponseWriter, req *http.Request) bool {
	if mcpRuntimeAuthorizationMatches(req) || (!r.isProductionEnvironment() && isLoopbackRequest(req)) {
		return true
	}
	status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
	writeJSON(w, status, response)
	return false
}

func (r *Router) isProductionEnvironment() bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}

func isLoopbackRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(req.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mcpRuntimeAuthorizationMatches(req *http.Request) bool {
	if req == nil {
		return false
	}
	expected := deploymentSecurityFromRequest(req).runtimeMCPAuth
	actual := strings.TrimSpace(req.Header.Get("Authorization"))
	return expected != "" && len(actual) == len(expected) && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
