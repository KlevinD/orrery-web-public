package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	contextgraph "orrery_v2/internal/app/contextgraph"
)

type recordingContextBundleBuilder struct {
	calls  int
	input  contextgraph.BuildContextBundleRequest
	result contextgraph.ContextBundle
}

func (b *recordingContextBundleBuilder) BuildContextBundle(_ context.Context, input contextgraph.BuildContextBundleRequest) (contextgraph.ContextBundle, error) {
	b.calls++
	b.input = input
	return b.result, nil
}

func TestConsumerBuildContextBundleRejectsUnsafeInput(t *testing.T) {
	base := BuildContextBundleInput{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "mission_assignment",
		ActorRef:    contextgraph.Ref{Kind: "agent", ID: "agent-1"},
		SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
		MaxDepth:    1, TokenBudget: 8000,
	}
	tests := []struct {
		name   string
		mutate func(*BuildContextBundleInput)
	}{
		{name: "missing subjects", mutate: func(input *BuildContextBundleInput) { input.SubjectRefs = nil }},
		{name: "query rejected", mutate: func(input *BuildContextBundleInput) { input.Query = "scan company" }},
		{name: "too many subjects", mutate: func(input *BuildContextBundleInput) {
			input.SubjectRefs = make([]contextgraph.Ref, 33)
			for index := range input.SubjectRefs {
				input.SubjectRefs[index] = contextgraph.Ref{Kind: "mission", ID: fmt.Sprintf("mission-%d", index)}
			}
		}},
		{name: "depth", mutate: func(input *BuildContextBundleInput) { input.MaxDepth = 3 }},
		{name: "small token budget", mutate: func(input *BuildContextBundleInput) { input.TokenBudget = 511 }},
		{name: "large token budget", mutate: func(input *BuildContextBundleInput) { input.TokenBudget = 16001 }},
		{name: "too many include kinds", mutate: func(input *BuildContextBundleInput) {
			input.IncludeKinds = make([]string, 9)
			for index := range input.IncludeKinds {
				input.IncludeKinds[index] = fmt.Sprintf("kind-%d", index)
			}
		}},
		{name: "include kind too long", mutate: func(input *BuildContextBundleInput) {
			input.IncludeKinds = []string{strings.Repeat("k", 257)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &recordingContextBundleBuilder{}
			input := base
			input.SubjectRefs = append([]contextgraph.Ref(nil), base.SubjectRefs...)
			test.mutate(&input)
			if _, err := NewService(builder).BuildContextBundle(context.Background(), input); !errors.Is(err, contextgraph.ErrQueryInvalid) {
				t.Fatalf("BuildContextBundle error = %v, want ErrQueryInvalid", err)
			}
			if builder.calls != 0 {
				t.Fatalf("context graph calls = %d, want 0", builder.calls)
			}
		})
	}
}

func TestNormalizeBuildContextBundleInputCanonicalizesIncludeKinds(t *testing.T) {
	input, err := NormalizeBuildContextBundleInput(BuildContextBundleInput{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "mission_assignment",
		ActorRef:     contextgraph.Ref{Kind: "agent", ID: "agent-1"},
		SubjectRefs:  []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
		IncludeKinds: []string{" Task ", "mission", "task", ""},
		TokenBudget:  8000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(input.IncludeKinds) != "[mission task]" {
		t.Fatalf("include kinds = %v", input.IncludeKinds)
	}
}

func TestConsumerBuildContextBundleAcceptsBounds(t *testing.T) {
	builder := &recordingContextBundleBuilder{result: contextgraph.ContextBundle{BundleID: "bundle-bounded"}}
	subjects := make([]contextgraph.Ref, 32)
	for index := range subjects {
		subjects[index] = contextgraph.Ref{Kind: "mission", ID: fmt.Sprintf("mission-%d", index)}
	}
	result, err := NewService(builder).BuildContextBundle(context.Background(), BuildContextBundleInput{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "mission_assignment",
		ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, SubjectRefs: subjects,
		MaxDepth: 2, TokenBudget: 16000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if builder.calls != 1 || result.BundleID != "bundle-bounded" || len(builder.input.SubjectRefs) != 32 || builder.input.MaxDepth != 2 || builder.input.TokenBudget != 16000 {
		t.Fatalf("builder calls=%d input=%+v result=%+v", builder.calls, builder.input, result)
	}
}

func TestConsumerContractExposesEnterpriseGraphToolSchemas(t *testing.T) {
	tools := ToolDescriptors()
	if len(tools) != 6 {
		t.Fatalf("tools = %+v, want 6 enterprise_graph tools", tools)
	}
	assertTool(t, tools, "enterprise_graph.build_context_bundle", true, []string{"tenant_id", "consumer", "purpose", "actor_ref"})
	buildContextBundle := findTool(t, tools, "enterprise_graph.build_context_bundle")
	if buildContextBundle.OutputSchema.Type != "object" {
		t.Fatalf("build_context_bundle output schema = %+v", buildContextBundle.OutputSchema)
	}
	schema, ok := buildContextBundle.OutputSchema.Properties["schema"]
	if !ok || schema.Type != "object" {
		t.Fatalf("build_context_bundle output missing schema dictionary: %+v", buildContextBundle.OutputSchema.Properties)
	}
	for _, field := range []string{"object_types", "link_types", "property_types", "action_types", "version_hash"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("schema dictionary properties missing %s: %+v", field, schema.Properties)
		}
	}
	for _, field := range []string{"knowledge_evidence", "partial", "partial_reason"} {
		if _, ok := buildContextBundle.OutputSchema.Properties[field]; !ok {
			t.Fatalf("build context output missing %s", field)
		}
	}
	assertTool(t, tools, "enterprise_graph.search_objects", true, []string{"tenant_id", "actor_ref", "query"})
	assertTool(t, tools, "enterprise_graph.get_neighbors", true, []string{"tenant_id", "actor_ref", "object_ref"})
	assertTool(t, tools, "enterprise_graph.explain_evidence", true, []string{"tenant_id", "actor_ref", "evidence_ref"})
	assertTool(t, tools, "enterprise_graph.submit_observation", false, []string{"tenant_id", "actor_ref", "source_ref", "object_refs", "observation"})
	assertTool(t, tools, "enterprise_graph.request_writeback", false, []string{"tenant_id", "actor_ref", "action_type_id", "object_refs", "args"})
}

func TestConsumerBuildContextBundleCallsContextGraphService(t *testing.T) {
	ctx := context.Background()
	store := contextgraph.NewMemoryStore()
	if err := store.SaveObject(ctx, contextgraph.Object{
		ID:           "mission-1",
		TenantID:     "tenant-1",
		ObjectType:   "mission",
		CanonicalKey: "mission-1",
		Label:        "Mission",
		Status:       "confirmed",
	}); err != nil {
		t.Fatalf("save object: %v", err)
	}
	service := NewService(contextgraph.NewService(store))

	result, err := service.BuildContextBundle(ctx, BuildContextBundleInput{
		TenantID:    "tenant-1",
		Consumer:    "iris",
		Purpose:     "mission_assignment",
		ActorRef:    contextgraph.Ref{Kind: "human", ID: "klevin"},
		SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
		MaxDepth:    0,
		TokenBudget: 8000,
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	if result.BundleID == "" || len(result.Objects) != 1 || result.Objects[0].ID != "mission-1" {
		t.Fatalf("bundle result = %+v", result)
	}
}

func TestConsumerRequestWritebackReturnsNeedsDecisionWithoutWriting(t *testing.T) {
	service := NewService(nil)

	result, err := service.RequestWriteback(context.Background(), RequestWritebackInput{
		TenantID:     "tenant-1",
		ActorRef:     contextgraph.Ref{Kind: "agent", ID: "marketing-agent"},
		ActionTypeID: "email.send",
		ObjectRefs:   []contextgraph.Ref{{Kind: "customer", ID: "customer-1"}},
		Args:         map[string]any{"subject": "hello"},
	})
	if err != nil {
		t.Fatalf("RequestWriteback: %v", err)
	}
	if result.Status != "needs_decision" || result.ActionRunID != "" {
		t.Fatalf("writeback result = %+v, want needs_decision without action run id", result)
	}
}

func assertTool(t *testing.T, tools []ToolDescriptor, name string, readOnly bool, required []string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		if tool.ReadOnly != readOnly {
			t.Fatalf("%s read_only = %v, want %v", name, tool.ReadOnly, readOnly)
		}
		for _, field := range required {
			if !containsString(tool.InputSchema.Required, field) {
				t.Fatalf("%s required = %+v, missing %s", name, tool.InputSchema.Required, field)
			}
		}
		return
	}
	t.Fatalf("missing tool %s in %+v", name, tools)
}

func findTool(t *testing.T, tools []ToolDescriptor, name string) ToolDescriptor {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing tool %s in %+v", name, tools)
	return ToolDescriptor{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
