package knowledgeprovider

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/knowledge/provider"
	toolres "orrery_v2/internal/resource/tool"
)

type fakeMCPPort struct {
	server toolres.ToolDefinition
	tools  []toolres.MCPExposedToolDefinition
	output map[string]any
	err    error
	called string
	args   map[string]any
}

func (f *fakeMCPPort) GetServer(context.Context, string) (toolres.ToolDefinition, error) {
	return f.server, f.err
}

func (f *fakeMCPPort) ListTools(context.Context, toolres.ToolDefinition) ([]toolres.MCPExposedToolDefinition, error) {
	return f.tools, f.err
}

func (f *fakeMCPPort) CallTool(_ context.Context, _ toolres.ToolDefinition, name, _ string, arguments map[string]any) (map[string]any, error) {
	f.called = name
	f.args = arguments
	return f.output, f.err
}

func TestDetectCapabilitiesAcceptsPageIndexAndHonorsOverrides(t *testing.T) {
	port := &fakeMCPPort{tools: exposedTools(
		"list_folders", "list_documents", "search_documents", "get_document", "save_knowledge", "create_folder", "folders_custom",
	)}
	adapter := NewMCPAdapter(port)
	binding := Binding{MCPServerID: "pageindex", Mode: ModeReadWrite, ToolMapping: map[Capability]string{CapabilityListFolders: "folders_custom"}}

	detected, err := adapter.DetectCapabilities(context.Background(), binding)
	if err != nil {
		t.Fatalf("detect capabilities: %v", err)
	}
	if detected[CapabilityListFolders] != "folders_custom" || detected[CapabilitySaveKnowledge] != "save_knowledge" {
		t.Fatalf("unexpected capability map: %+v", detected)
	}
}

func TestDetectCapabilitiesRejectsIncompleteReadWriteServer(t *testing.T) {
	port := &fakeMCPPort{tools: exposedTools("list_folders", "list_documents", "search_documents", "get_document")}
	_, err := NewMCPAdapter(port).DetectCapabilities(context.Background(), Binding{MCPServerID: "pageindex", Mode: ModeReadWrite})
	if err == nil {
		t.Fatal("expected missing write capability error")
	}
}

func TestMCPAdapterNormalizesStructuredFoldersAndDocuments(t *testing.T) {
	port := &fakeMCPPort{output: map[string]any{"structuredContent": map[string]any{"result": map[string]any{
		"folders": []any{map[string]any{"id": "folder-1", "name": "minvia-core"}},
	}}}}
	adapter := NewMCPAdapter(port)
	binding := Binding{MCPServerID: "pageindex"}
	folders, err := adapter.ListFolders(context.Background(), binding, "superadmin")
	if err != nil || len(folders) != 1 || folders[0].ID != "folder-1" || folders[0].Name != "minvia-core" {
		t.Fatalf("normalized folders = %+v, err=%v", folders, err)
	}

	port.output = map[string]any{"result": map[string]any{"documents": []any{map[string]any{
		"document_id": "doc-1", "folder_id": "folder-1", "filename": "overview.md", "status": "completed",
	}}}}
	documents, err := adapter.ListDocuments(context.Background(), binding, "superadmin", "folder-1")
	if err != nil || len(documents) != 1 || documents[0].ID != "doc-1" || documents[0].Name != "overview.md" {
		t.Fatalf("normalized documents = %+v, err=%v", documents, err)
	}
}

func TestMCPAdapterNormalizesFastMCPWrappedLists(t *testing.T) {
	port := &fakeMCPPort{output: map[string]any{"structuredContent": map[string]any{
		"result": []any{map[string]any{"id": "folder-1", "name": "minvia-core"}},
	}}}
	adapter := NewMCPAdapter(port)
	binding := Binding{MCPServerID: "pageindex"}

	folders, err := adapter.ListFolders(context.Background(), binding, "superadmin")
	if err != nil || len(folders) != 1 || folders[0].ID != "folder-1" {
		t.Fatalf("FastMCP folders = %+v, err=%v", folders, err)
	}

	port.output = map[string]any{"structuredContent": map[string]any{
		"result": []any{map[string]any{"id": "doc-1", "filename": "overview.md"}},
	}}
	documents, err := adapter.ListDocuments(context.Background(), binding, "superadmin", "folder-1")
	if err != nil || len(documents) != 1 || documents[0].ID != "doc-1" {
		t.Fatalf("FastMCP documents = %+v, err=%v", documents, err)
	}
}

func TestMCPAdapterNormalizesTextJSONFallback(t *testing.T) {
	port := &fakeMCPPort{output: map[string]any{"content": []any{map[string]any{
		"type": "text", "text": `{"result":{"folders":[{"folder_id":"folder-2","name":"minvia-operations"}]}}`,
	}}}}
	folders, err := NewMCPAdapter(port).ListFolders(context.Background(), Binding{MCPServerID: "pageindex"}, "superadmin")
	if err != nil || len(folders) != 1 || folders[0].ID != "folder-2" {
		t.Fatalf("normalized folders = %+v, err=%v", folders, err)
	}
}

func TestMCPAdapterNormalizesRuntimeTypedTextContent(t *testing.T) {
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	port := &fakeMCPPort{output: map[string]any{"content": []contentBlock{{
		Type: "text", Text: `{"folders":[{"id":"folder-3","name":"minvia-legal"}]}`,
	}}}}
	folders, err := NewMCPAdapter(port).ListFolders(context.Background(), Binding{MCPServerID: "pageindex"}, "superadmin")
	if err != nil || len(folders) != 1 || folders[0].Name != "minvia-legal" {
		t.Fatalf("normalized typed content = %+v, err=%v", folders, err)
	}
}

func TestMCPAdapterConvertsRuntimeFailures(t *testing.T) {
	port := &fakeMCPPort{err: errors.New("external mcp tool returned an error")}
	_, err := NewMCPAdapter(port).ListFolders(context.Background(), Binding{MCPServerID: "pageindex"}, "superadmin")
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("expected dependency unavailable, got %v", err)
	}
}

func TestMCPAdapterNormalizesSearchAndPageContent(t *testing.T) {
	port := &fakeMCPPort{output: map[string]any{"result": map[string]any{"results": []any{map[string]any{
		"document_id": "doc-1", "chunk_id": "doc-1:1", "filename": "release.md", "snippet": "release checklist", "score": 0.75,
	}}}}}
	adapter := NewMCPAdapter(port)
	binding := Binding{MCPServerID: "pageindex"}
	results, err := adapter.SearchDocuments(context.Background(), binding, "operator", "folder-1", "release", 8, provider.RetrievalModeFast)
	if err != nil || len(results) != 1 || results[0].DocumentID != "doc-1" || results[0].Snippet != "release checklist" {
		t.Fatalf("normalized search = %+v, err=%v", results, err)
	}
	if port.args["retrieval_mode"] != "fast" || port.args["include_content"] != false {
		t.Fatalf("fast search arguments = %+v", port.args)
	}
	_, err = adapter.SearchDocuments(context.Background(), binding, "operator", "folder-1", "release", 8, provider.RetrievalModeAgentic)
	if err != nil || port.args["retrieval_mode"] != "agentic" || port.args["include_content"] != true {
		t.Fatalf("agentic search arguments = %+v, err=%v", port.args, err)
	}

	port.output = map[string]any{"pages": []any{
		map[string]any{"page": 1.0, "text": "first"},
		map[string]any{"page": 2.0, "content": "second"},
	}}
	pages, err := adapter.GetPageContent(context.Background(), binding, "operator", "doc-1", "1-100")
	if err != nil || len(pages) != 2 || pages[0]["text"] != "first" {
		t.Fatalf("normalized pages = %+v, err=%v", pages, err)
	}
}

func exposedTools(names ...string) []toolres.MCPExposedToolDefinition {
	tools := make([]toolres.MCPExposedToolDefinition, 0, len(names))
	for _, name := range names {
		tools = append(tools, toolres.MCPExposedToolDefinition{Name: name})
	}
	return tools
}
