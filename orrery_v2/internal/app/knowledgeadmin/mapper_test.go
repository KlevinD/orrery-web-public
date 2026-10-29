package knowledgeadmin

import (
	"testing"

	"orrery_v2/internal/knowledge/core"
)

func TestMapBaseIncludesMCPProviderIdentity(t *testing.T) {
	dto := mapBase(core.KnowledgeBase{
		ID:       "kb-external",
		Provider: core.ProviderMCP,
		ProviderMetadata: map[string]string{
			"binding_id":            "kprovider-pageindex-minvia",
			"provider_display_name": "PageIndex",
		},
	})

	if dto.ProviderBindingID != "kprovider-pageindex-minvia" {
		t.Fatalf("provider binding ID = %q", dto.ProviderBindingID)
	}
	if dto.ProviderDisplayName != "PageIndex" {
		t.Fatalf("provider display name = %q", dto.ProviderDisplayName)
	}
}

func TestMapDocumentExposesSafeRelativeFolderPath(t *testing.T) {
	dto := mapDocument(core.KnowledgeDocument{
		Filename: "report.pdf",
		ProviderMetadata: map[string]string{
			"pageindex_upload_path": "research/2026/report.pdf",
		},
	})

	if dto.FolderPath != "research/2026" {
		t.Fatalf("folder path = %q", dto.FolderPath)
	}

	unsafe := mapDocument(core.KnowledgeDocument{
		Filename: "secret.pdf",
		ProviderMetadata: map[string]string{
			"pageindex_upload_path": "C:/private/secret.pdf",
		},
	})
	if unsafe.FolderPath != "" {
		t.Fatalf("unsafe folder path leaked: %q", unsafe.FolderPath)
	}
}

func TestMapDocumentReadsExternalFolderMetadata(t *testing.T) {
	dto := mapDocument(core.KnowledgeDocument{
		Filename: "notes.md",
		ProviderMetadata: map[string]string{
			"external_metadata": `{"folder_path":"operations/runbooks"}`,
		},
	})

	if dto.FolderPath != "operations/runbooks" {
		t.Fatalf("external folder path = %q", dto.FolderPath)
	}
}
