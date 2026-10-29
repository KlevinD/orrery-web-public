package knowledgeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/knowledge/provider"
	toolres "orrery_v2/internal/resource/tool"
)

var ErrDependencyUnavailable = errors.New("knowledge provider dependency unavailable")

type MCPPort interface {
	GetServer(context.Context, string) (toolres.ToolDefinition, error)
	ListTools(context.Context, toolres.ToolDefinition) ([]toolres.MCPExposedToolDefinition, error)
	CallTool(context.Context, toolres.ToolDefinition, string, string, map[string]any) (map[string]any, error)
}

type MCPAdapter struct {
	port MCPPort
}

func NewMCPAdapter(port MCPPort) *MCPAdapter {
	return &MCPAdapter{port: port}
}

func (a *MCPAdapter) DetectCapabilities(ctx context.Context, binding Binding) (map[Capability]string, error) {
	if a == nil || a.port == nil {
		return nil, ErrDependencyUnavailable
	}
	server, err := a.port.GetServer(ctx, binding.MCPServerID)
	if err != nil {
		return nil, dependencyError("get MCP server", err)
	}
	tools, err := a.port.ListTools(ctx, server)
	if err != nil {
		return nil, dependencyError("list MCP tools", err)
	}
	available := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		available[strings.TrimSpace(tool.Name)] = struct{}{}
	}
	mapping := resolvedToolMapping(binding)
	detected := make(map[Capability]string, len(mapping))
	for capability, toolName := range mapping {
		if _, ok := available[strings.TrimSpace(toolName)]; ok {
			detected[capability] = toolName
		}
	}
	required := []Capability{CapabilityListFolders, CapabilityListDocuments, CapabilitySearchDocuments, CapabilityGetDocument}
	if binding.Mode == ModeReadWrite {
		required = append(required, CapabilitySaveKnowledge, CapabilityCreateFolder)
	}
	for _, capability := range required {
		toolName := strings.TrimSpace(mapping[capability])
		if _, ok := available[toolName]; !ok {
			return nil, fmt.Errorf("%w: required capability %s maps to unavailable tool %q", ErrDependencyUnavailable, capability, toolName)
		}
	}
	return detected, nil
}

func (a *MCPAdapter) ListFolders(ctx context.Context, binding Binding, principalID string) ([]ExternalFolder, error) {
	output, err := a.call(ctx, binding, CapabilityListFolders, principalID, map[string]any{})
	if err != nil {
		return nil, err
	}
	root, err := normalizeMCPOutput(output)
	if err != nil {
		return nil, err
	}
	items := sliceField(root, "folders", "items", "result")
	folders := make([]ExternalFolder, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(entry, "id", "folder_id", "folderId")
		name := stringField(entry, "name", "folder_name", "title")
		if id == "" || name == "" {
			continue
		}
		folders = append(folders, ExternalFolder{ID: id, Name: name, Metadata: cloneAnyMap(entry)})
	}
	return folders, nil
}

func (a *MCPAdapter) ListDocuments(ctx context.Context, binding Binding, principalID, folderID string) ([]ExternalDocument, error) {
	output, err := a.call(ctx, binding, CapabilityListDocuments, principalID, map[string]any{"folder_id": folderID})
	if err != nil {
		return nil, err
	}
	root, err := normalizeMCPOutput(output)
	if err != nil {
		return nil, err
	}
	items := sliceField(root, "documents", "items", "result")
	documents := make([]ExternalDocument, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(entry, "id", "document_id", "documentId")
		name := stringField(entry, "name", "filename", "title")
		if id == "" || name == "" {
			continue
		}
		docFolderID := stringField(entry, "folder_id", "folderId")
		if docFolderID == "" {
			docFolderID = folderID
		}
		documents = append(documents, ExternalDocument{
			ID: id, FolderID: docFolderID, Name: name,
			Status: stringField(entry, "status"), Metadata: cloneAnyMap(entry),
		})
	}
	return documents, nil
}

func (a *MCPAdapter) SearchDocuments(ctx context.Context, binding Binding, principalID, folderID, query string, limit int, retrievalMode provider.RetrievalMode) ([]ExternalSearchResult, error) {
	arguments := map[string]any{
		"folder_id": folderID, "query": query, "limit": limit,
		"include_content": retrievalMode == provider.RetrievalModeAgentic,
	}
	if retrievalMode != "" {
		arguments["retrieval_mode"] = string(retrievalMode)
	}
	output, err := a.call(ctx, binding, CapabilitySearchDocuments, principalID, arguments)
	if err != nil {
		return nil, err
	}
	root, err := normalizeMCPOutput(output)
	if err != nil {
		return nil, err
	}
	items := sliceField(root, "results", "items")
	results := make([]ExternalSearchResult, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		documentID := stringField(entry, "document_id", "documentId", "id")
		if documentID == "" {
			continue
		}
		snippet := stringField(entry, "snippet", "content", "text")
		results = append(results, ExternalSearchResult{
			DocumentID:  documentID,
			ChunkID:     stringField(entry, "chunk_id", "chunkId", "node_id"),
			Score:       float32Field(entry, "score"),
			Snippet:     snippet,
			HeadingPath: stringSliceField(entry, "heading_path", "headingPath"),
			Filename:    stringField(entry, "filename", "title", "original_filename"),
		})
	}
	return results, nil
}

func (a *MCPAdapter) GetPageContent(ctx context.Context, binding Binding, principalID, documentID, pages string) ([]map[string]any, error) {
	output, err := a.call(ctx, binding, CapabilityGetPageContent, principalID, map[string]any{"document_id": documentID, "pages": pages})
	if err != nil {
		return nil, err
	}
	root, err := normalizeMCPOutput(output)
	if err != nil {
		return nil, err
	}
	items := sliceField(root, "pages", "items")
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if page, ok := item.(map[string]any); ok {
			result = append(result, cloneAnyMap(page))
		}
	}
	return result, nil
}

func (a *MCPAdapter) call(ctx context.Context, binding Binding, capability Capability, principalID string, arguments map[string]any) (map[string]any, error) {
	if a == nil || a.port == nil {
		return nil, ErrDependencyUnavailable
	}
	server, err := a.port.GetServer(ctx, binding.MCPServerID)
	if err != nil {
		return nil, dependencyError("get MCP server", err)
	}
	toolName := resolvedToolMapping(binding)[capability]
	output, err := a.port.CallTool(ctx, server, toolName, principalID, arguments)
	if err != nil {
		return nil, dependencyError("call MCP tool "+toolName, err)
	}
	if failed, _ := output["isError"].(bool); failed {
		return nil, fmt.Errorf("%w: MCP tool %s returned an error", ErrDependencyUnavailable, toolName)
	}
	return output, nil
}

func resolvedToolMapping(binding Binding) map[Capability]string {
	mapping := DefaultToolMapping()
	for capability, toolName := range binding.ToolMapping {
		if strings.TrimSpace(toolName) != "" {
			mapping[capability] = strings.TrimSpace(toolName)
		}
	}
	return mapping
}

func normalizeMCPOutput(output map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("%w: encode MCP output: %v", ErrDependencyUnavailable, err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, fmt.Errorf("%w: decode MCP output: %v", ErrDependencyUnavailable, err)
	}
	output = normalized
	root := output
	if structured, ok := mapField(root, "structuredContent"); ok {
		root = structured
	}
	if result, ok := mapField(root, "result"); ok {
		root = result
	}
	if len(root) > 0 && (root["folders"] != nil || root["documents"] != nil || root["results"] != nil || root["pages"] != nil || root["items"] != nil) {
		return root, nil
	}
	content, ok := output["content"].([]any)
	if !ok {
		return root, nil
	}
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok || stringField(block, "type") != "text" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(stringField(block, "text")), &decoded); err != nil {
			continue
		}
		if result, ok := mapField(decoded, "result"); ok {
			decoded = result
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("%w: MCP tool returned no structured or JSON content", ErrDependencyUnavailable)
}

func dependencyError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrDependencyUnavailable, operation, err)
}

func mapField(input map[string]any, key string) (map[string]any, bool) {
	value, ok := input[key]
	if !ok {
		return nil, false
	}
	mapped, ok := value.(map[string]any)
	return mapped, ok
}

func sliceField(input map[string]any, keys ...string) []any {
	for _, key := range keys {
		if values, ok := input[key].([]any); ok {
			return values
		}
	}
	return nil
}

func stringField(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneAnyMap(input map[string]any) map[string]any {
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func float32Field(input map[string]any, key string) float32 {
	switch value := input[key].(type) {
	case float64:
		return float32(value)
	case float32:
		return value
	case int:
		return float32(value)
	default:
		return 0
	}
}

func stringSliceField(input map[string]any, keys ...string) []string {
	for _, key := range keys {
		values, ok := input[key].([]any)
		if !ok {
			continue
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			if item, ok := value.(string); ok {
				result = append(result, item)
			}
		}
		return result
	}
	return nil
}
