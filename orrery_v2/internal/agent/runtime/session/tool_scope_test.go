package session

import (
	"context"
	"fmt"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

type scopedTestConnection struct {
	*authDispatchConnection
	supported bool
}

func (c scopedTestConnection) SupportsExplicitToolScopeFence() bool { return c.supported }

func TestManagedRuntimeRequiresConnectionSpecificTaskScopeFence(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			connection := scopedTestConnection{authDispatchConnection: &authDispatchConnection{}, supported: supported}
			runtime, err := NewRuntime(RuntimeOptions{Kind: agentcore.RuntimeKindManaged, Connection: connection, Workspaces: NewWorkspaceBroker()})
			if err != nil {
				t.Fatal(err)
			}
			req := canonicalTestRequest(t, "scoped-managed")
			req.ContextPack.ResponsibilityContext = &agentcore.ResponsibilityContext{RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"knowledge.read"}}
			opened, err := runtime.open(context.Background(), req)
			if supported {
				if err != nil || opened == nil {
					t.Fatalf("scoped managed runtime rejected: %v", err)
				}
				runtime.closeOpen(context.Background(), opened)
			} else if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) || opened != nil || connection.Backend() != nil {
				t.Fatalf("unfenced runtime launched: %v", err)
			}
		})
	}
}
