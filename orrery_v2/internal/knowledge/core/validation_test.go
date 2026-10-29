package core_test

import (
	"errors"
	"testing"

	"orrery_v2/internal/knowledge/core"
	doccore "orrery_v2/internal/platform/document/core"
)

func TestValidateOwnerType(t *testing.T) {
	valid := []core.OwnerType{
		core.OwnerAgent,
		core.OwnerHuman,
		core.OwnerOrganization,
		core.OwnerPosition,
		core.OwnerGlobal,
	}
	for _, ownerType := range valid {
		if err := ownerType.Validate(); err != nil {
			t.Fatalf("expected %s to validate: %v", ownerType, err)
		}
	}
	if err := core.OwnerType("team").Validate(); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected invalid owner type, got %v", err)
	}
}

func TestValidateBindingSubjectType(t *testing.T) {
	valid := []core.BindingSubjectType{
		core.BindingSubjectAgent,
		core.BindingSubjectHuman,
		core.BindingSubjectOrganization,
	}
	for _, subjectType := range valid {
		if err := subjectType.Validate(); err != nil {
			t.Fatalf("expected %s to validate: %v", subjectType, err)
		}
	}
	if err := core.BindingSubjectType("global").Validate(); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected invalid binding subject type, got %v", err)
	}
}

func TestDefaultBaseOnlyAllowedForAgentAndHumanOwners(t *testing.T) {
	base := validBase()
	base.IsDefault = true
	base.OwnerType = core.OwnerOrganization
	if err := base.Validate(); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected default organization base invalid, got %v", err)
	}

	base.OwnerType = core.OwnerAgent
	if err := base.Validate(); err != nil {
		t.Fatalf("expected default agent base valid: %v", err)
	}

	base.OwnerType = core.OwnerHuman
	if err := base.Validate(); err != nil {
		t.Fatalf("expected default human base valid: %v", err)
	}
}

func TestGlobalKnowledgeBaseMustBePublic(t *testing.T) {
	for _, visibility := range []core.Visibility{core.VisibilityPrivate, core.VisibilityScoped} {
		base := validBase()
		base.OwnerType = core.OwnerGlobal
		base.OwnerID = ""
		base.Visibility = visibility
		if err := base.Validate(); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("expected global %s base invalid, got %v", visibility, err)
		}
	}

	base := validBase()
	base.OwnerType = core.OwnerGlobal
	base.OwnerID = ""
	base.Visibility = core.VisibilityPublic
	if err := base.Validate(); err != nil {
		t.Fatalf("expected global public base valid: %v", err)
	}
}

func TestValidateVisibility(t *testing.T) {
	for _, visibility := range []core.Visibility{core.VisibilityPrivate, core.VisibilityScoped, core.VisibilityPublic} {
		if err := visibility.Validate(); err != nil {
			t.Fatalf("expected %s to validate: %v", visibility, err)
		}
	}
	if err := core.Visibility("hidden").Validate(); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected invalid visibility, got %v", err)
	}
}

func TestKnowledgeProviderValidation(t *testing.T) {
	for _, provider := range []core.KnowledgeProvider{core.ProviderPageIndex, core.ProviderLocal} {
		if err := provider.Validate(); err != nil {
			t.Fatalf("expected %s provider to validate: %v", provider, err)
		}
	}
	if err := core.KnowledgeProvider("custom").Validate(); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected invalid provider, got %v", err)
	}
}

func TestKnowledgeBaseDefaultsNewMetadataToPageIndexProvider(t *testing.T) {
	base := core.KnowledgeBase{
		ID:          "kb-1",
		Name:        "Product",
		OwnerType:   core.OwnerOrganization,
		OwnerID:     "org-1",
		Visibility:  core.VisibilityScoped,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:      core.KnowledgeActive,
	}
	if got := base.EffectiveProvider(); got != core.ProviderPageIndex {
		t.Fatalf("effective provider = %q, want %q", got, core.ProviderPageIndex)
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("expected pageindex base metadata to validate without vector backend: %v", err)
	}
}

func TestKnowledgeBaseTreatsLegacyLanceDBMetadataAsLocalProvider(t *testing.T) {
	base := validBase()
	base.VectorStorePath = "D:/knowledge/kb-1"
	if got := base.EffectiveProvider(); got != core.ProviderLocal {
		t.Fatalf("effective provider = %q, want %q", got, core.ProviderLocal)
	}
}

func TestDocumentValidationAcceptsProviderDocumentID(t *testing.T) {
	doc := core.KnowledgeDocument{
		ID:                 "kdoc-1",
		KnowledgeBaseID:    "kb-1",
		ProviderDocumentID: "pageindex-doc-1",
		Filename:           "handbook.pdf",
		Status:             core.DocumentIndexed,
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("expected provider-backed document to validate without artifact id: %v", err)
	}
}

func TestValidateChunkPolicy(t *testing.T) {
	if err := core.ValidateChunkPolicy(doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 20, MinRunes: 10}); err != nil {
		t.Fatalf("expected policy valid: %v", err)
	}
	if err := core.ValidateChunkPolicy(doccore.ChunkPolicy{MaxRunes: 20, OverlapRunes: 20}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected invalid chunk policy, got %v", err)
	}
}

func TestBindingValidation(t *testing.T) {
	binding := core.KnowledgeBinding{
		ID:              "bind-1",
		KnowledgeBaseID: "kb-1",
		BoundToType:     core.BindingSubjectAgent,
		BoundToID:       "agent-1",
		Access:          core.AccessRead,
	}
	if err := binding.Validate(); err != nil {
		t.Fatalf("expected binding valid: %v", err)
	}

	binding.Access = core.AccessLevel("owner")
	if err := binding.Validate(); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expected invalid access, got %v", err)
	}
}

func validBase() core.KnowledgeBase {
	return core.KnowledgeBase{
		ID:            "kb-1",
		Name:          "Product",
		OwnerType:     core.OwnerOrganization,
		OwnerID:       "org-1",
		Visibility:    core.VisibilityScoped,
		VectorBackend: core.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:        core.KnowledgeActive,
	}
}
