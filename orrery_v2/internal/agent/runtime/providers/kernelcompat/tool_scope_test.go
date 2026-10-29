package kernelcompat

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

type scopedTestEngine struct {
	Engine
	supported bool
}

func (e scopedTestEngine) SupportsExplicitToolScopeFence() bool { return e.supported }

func TestConnectionTaskScopeFenceRequiresEngineDeclaration(t *testing.T) {
	base := EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) { return nil, nil })
	for _, engine := range []Engine{base, scopedTestEngine{Engine: base}, scopedTestEngine{Engine: base, supported: true}} {
		connection, err := NewConnection(Config{Engine: engine})
		if err != nil {
			t.Fatal(err)
		}
		declared, ok := engine.(scopedTestEngine)
		if connection.SupportsExplicitToolScopeFence() != (ok && declared.supported) {
			t.Fatalf("engine capability inferred for %T", engine)
		}
		_ = connection.Close()
	}
}
