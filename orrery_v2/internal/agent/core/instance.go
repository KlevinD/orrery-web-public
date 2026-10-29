package core

type AgentInstance interface {
	Agent
	Identity() AgentIdentity
	Bindings() AgentBindings
}

type AgentBindings struct {
	Tools  []ToolBinding
	Skills []SkillBinding
	Memory MemoryBinding
}

type ToolBinding struct {
	ToolID             ToolID
	Alias              string
	Version            string
	Name               string
	Description        string
	Source             string
	ServerToolID       string
	DownstreamToolName string
	GrantedPermissions []string
	SecretEnv          map[string]string
	Summary            string
	InputSchema        map[string]any
	OutputSchema       map[string]any
}

type SkillBinding struct {
	SkillID       string
	Alias         string
	Version       string
	Name          string
	Summary       string
	PackageDigest string
}

type MemoryBinding struct {
	PersonalDigest    MemoryDigestRef
	CollectiveDigests []MemoryDigestRef
	Warnings          []string
}
