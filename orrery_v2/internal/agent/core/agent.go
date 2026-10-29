package core

type Agent interface {
	ID() AgentInstanceID
	Base() *BaseAgent
	Profile() AgentProfile
}

type AgentInput struct {
	RunID    AgentRunID
	TenantID string
	// ExecutionEnvelope is the server-owned identity and authorization
	// snapshot for this run. It must survive the Agent -> runtime boundary.
	ExecutionEnvelope         *ExecutionEnvelope
	ExecutionPrincipal        ExecutionPrincipal
	ContextPack               ContextPack
	InputMessages             []RuntimeChatMessage
	InputMedia                []ContentBlock
	MemoryContextRendered     bool
	DisableBoundMemoryContext bool
	Task                      TaskInput
	RuntimeOptions            RuntimeOptions
	RuntimeVariables          map[string]string
	RuntimeContextParts       []RuntimeContextPart
	RuntimeToolBindings       []RuntimeToolBinding
	RuntimeSkillBindings      []RuntimeSkillBinding
}

type TaskInput struct {
	ID                   string
	MissionID            string
	Goal                 string
	WorkerPersonalGoal   string
	AcceptanceCriteria   []TaskAcceptanceInput
	EvidenceRequirements []TaskEvidenceInput
	ExpectedDeliverables []TaskDeliverableInput
}

type TaskAcceptanceInput struct {
	ID          string
	Description string
}

type TaskEvidenceInput struct {
	ID          string
	Description string
	Kind        string
	Required    bool
}

type TaskDeliverableInput struct {
	ID          string
	Title       string
	Description string
}
