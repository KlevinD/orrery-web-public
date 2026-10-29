package knowledgeprovider

import "time"

type Mode string

const (
	ModeReadOnly  Mode = "read_only"
	ModeReadWrite Mode = "read_write"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusError    Status = "error"
)

type Capability string

const (
	CapabilityListFolders     Capability = "list_folders"
	CapabilityListDocuments   Capability = "list_documents"
	CapabilitySearchDocuments Capability = "search_documents"
	CapabilityGetDocument     Capability = "get_document"
	CapabilityGetPageContent  Capability = "get_page_content"
	CapabilitySaveKnowledge   Capability = "save_knowledge"
	CapabilityCreateFolder    Capability = "create_folder"
)

type ResourceKind string

const (
	ResourceFolder   ResourceKind = "folder"
	ResourceDocument ResourceKind = "document"
)

type Binding struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	MCPServerID    string                `json:"mcp_server_id"`
	Mode           Mode                  `json:"mode"`
	Status         Status                `json:"status"`
	ToolMapping    map[Capability]string `json:"tool_mapping"`
	FolderInclude  []string              `json:"folder_include,omitempty"`
	FolderExclude  []string              `json:"folder_exclude,omitempty"`
	LastSyncCursor string                `json:"last_sync_cursor,omitempty"`
	LastError      string                `json:"last_error,omitempty"`
	LastSyncedAt   time.Time             `json:"last_synced_at,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	CreatedBy      string                `json:"created_by"`
	UpdatedBy      string                `json:"updated_by"`
}

type ExternalMapping struct {
	BindingID  string       `json:"binding_id"`
	Kind       ResourceKind `json:"kind"`
	ExternalID string       `json:"external_id"`
	LocalID    string       `json:"local_id"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

type ExternalFolder struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ExternalDocument struct {
	ID       string         `json:"id"`
	FolderID string         `json:"folder_id"`
	Name     string         `json:"name"`
	Status   string         `json:"status,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type CapabilityTestResult struct {
	BindingID    string                `json:"binding_id"`
	Capabilities map[Capability]string `json:"capabilities"`
}

type ExternalSearchResult struct {
	DocumentID  string
	ChunkID     string
	Score       float32
	Snippet     string
	HeadingPath []string
	Filename    string
}

func DefaultToolMapping() map[Capability]string {
	return map[Capability]string{
		CapabilityListFolders:     "list_folders",
		CapabilityListDocuments:   "list_documents",
		CapabilitySearchDocuments: "search_documents",
		CapabilityGetDocument:     "get_document",
		CapabilityGetPageContent:  "get_page_content",
		CapabilitySaveKnowledge:   "save_knowledge",
		CapabilityCreateFolder:    "create_folder",
	}
}
