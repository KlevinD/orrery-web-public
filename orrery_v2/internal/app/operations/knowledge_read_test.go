package operations_test

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func knowledgeSearchReadToolRef() agentcore.ToolRef {
	return agentcore.ToolRef{ToolID: toolstd.KnowledgeSearch, PermissionScope: toolstd.PermissionKnowledgeRead}
}

func memorySearchReadToolRef() agentcore.ToolRef {
	return agentcore.ToolRef{ToolID: toolstd.MemorySearch, PermissionScope: toolstd.PermissionMemoryRead}
}

func assertToolRef(t *testing.T, refs []agentcore.ToolRef, want agentcore.ToolRef) {
	t.Helper()
	for _, ref := range refs {
		if ref == want {
			return
		}
	}
	t.Fatalf("tool refs should contain %+v: %+v", want, refs)
}

func assertConfiguredToolAndKnowledgeRead(t *testing.T, refs []agentcore.ToolRef, configured agentcore.ToolRef) {
	t.Helper()
	if len(refs) < 2 || len(refs) > 3 {
		t.Fatalf("tool refs length = %d, want configured tool plus required read bindings: %+v", len(refs), refs)
	}
	configuredCount, knowledgeCount, memoryCount := 0, 0, 0
	for _, ref := range refs {
		switch ref.ToolID {
		case configured.ToolID:
			if ref != configured {
				t.Fatalf("configured tool ref = %+v, want %+v", ref, configured)
			}
			configuredCount++
		case toolstd.KnowledgeSearch:
			if ref.PermissionScope != toolstd.PermissionKnowledgeRead {
				t.Fatalf("knowledge search permission = %q, want %q", ref.PermissionScope, toolstd.PermissionKnowledgeRead)
			}
			knowledgeCount++
		case toolstd.MemorySearch:
			if ref.PermissionScope != toolstd.PermissionMemoryRead {
				t.Fatalf("memory search permission = %q, want %q", ref.PermissionScope, toolstd.PermissionMemoryRead)
			}
			memoryCount++
		default:
			t.Fatalf("unexpected tool ref: %+v", ref)
		}
	}
	if configuredCount != 1 || knowledgeCount != 1 || memoryCount > 1 {
		t.Fatalf("tool refs should contain one configured and one knowledge read binding: %+v", refs)
	}
}

func assertMemoryRead(t *testing.T, refs []agentcore.ToolRef) {
	assertToolRef(t, refs, memorySearchReadToolRef())
}

func assertConfiguredWorkforceToolAndKnowledgeRead(t *testing.T, refs []operations.WorkforceToolRefDTO, configured operations.WorkforceToolRefDTO) {
	t.Helper()
	converted := make([]agentcore.ToolRef, 0, len(refs))
	for _, ref := range refs {
		converted = append(converted, agentcore.ToolRef{
			ToolID: agentcore.ToolID(ref.ToolID), VersionConstraint: ref.VersionConstraint, Alias: ref.Alias,
			PermissionScope: ref.PermissionScope, BindingPolicy: ref.BindingPolicy,
		})
	}
	assertConfiguredToolAndKnowledgeRead(t, converted, agentcore.ToolRef{
		ToolID: agentcore.ToolID(configured.ToolID), VersionConstraint: configured.VersionConstraint, Alias: configured.Alias,
		PermissionScope: configured.PermissionScope, BindingPolicy: configured.BindingPolicy,
	})
}
