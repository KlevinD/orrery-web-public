package tool

type WorkingDirPolicy string

const (
	WorkingDirWorkspace WorkingDirPolicy = "workspace"
	WorkingDirProject   WorkingDirPolicy = "project"
	WorkingDirTemp      WorkingDirPolicy = "temp"
)

type CLIToolDefinition struct {
	Command          string           `json:"command"`
	Args             []string         `json:"args,omitempty"`
	EnvSecretRefs    []SecretRef      `json:"env_secret_refs,omitempty"`
	WorkingDirPolicy WorkingDirPolicy `json:"working_dir_policy"`
	TimeoutSeconds   int              `json:"timeout_seconds,omitempty"`
}

func (d CLIToolDefinition) clone() CLIToolDefinition {
	d.Args = append([]string(nil), d.Args...)
	d.EnvSecretRefs = append([]SecretRef(nil), d.EnvSecretRefs...)
	return d
}
