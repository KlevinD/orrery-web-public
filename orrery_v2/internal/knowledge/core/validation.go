package core

import (
	"fmt"
	"strings"
)

func (t OwnerType) Validate() error {
	switch t {
	case OwnerAgent, OwnerHuman, OwnerOrganization, OwnerPosition, OwnerGlobal:
		return nil
	default:
		return fmt.Errorf("%w: invalid owner_type %q", ErrInvalid, t)
	}
}

func (t BindingSubjectType) Validate() error {
	switch t {
	case BindingSubjectAgent, BindingSubjectHuman, BindingSubjectOrganization, BindingSubjectPosition, BindingSubjectRole:
		return nil
	default:
		return fmt.Errorf("%w: invalid bound_to_type %q", ErrInvalid, t)
	}
}

func (v Visibility) Validate() error {
	switch v {
	case VisibilityPrivate, VisibilityScoped, VisibilityPublic:
		return nil
	default:
		return fmt.Errorf("%w: invalid visibility %q", ErrInvalid, v)
	}
}

func (a AccessLevel) Validate() error {
	switch a {
	case AccessRead, AccessWrite, AccessManage:
		return nil
	default:
		return fmt.Errorf("%w: invalid access %q", ErrInvalid, a)
	}
}

func (s KnowledgeStatus) Validate() error {
	switch s {
	case KnowledgeActive, KnowledgeDisabled, KnowledgeDeleting:
		return nil
	default:
		return fmt.Errorf("%w: invalid knowledge status %q", ErrInvalid, s)
	}
}

func (p KnowledgeProvider) Validate() error {
	switch p {
	case ProviderPageIndex, ProviderLocal, ProviderMCP:
		return nil
	default:
		return fmt.Errorf("%w: invalid knowledge provider %q", ErrInvalid, p)
	}
}

func (b KnowledgeBase) EffectiveProvider() KnowledgeProvider {
	if b.Provider != "" {
		return b.Provider
	}
	if b.VectorBackend != "" || strings.TrimSpace(b.VectorStorePath) != "" {
		return ProviderLocal
	}
	return ProviderPageIndex
}

func (b VectorBackend) Validate() error {
	switch b {
	case VectorBackendLanceDB:
		return nil
	default:
		return fmt.Errorf("%w: invalid vector backend %q", ErrInvalid, b)
	}
}

func (s DocumentStatus) Validate() error {
	switch s {
	case DocumentUploaded, DocumentParsing, DocumentChunking, DocumentEmbedding, DocumentIndexed, DocumentFailed, DocumentDeleting:
		return nil
	default:
		return fmt.Errorf("%w: invalid document status %q", ErrInvalid, s)
	}
}

func (s JobStatus) Validate() error {
	switch s {
	case JobQueued, JobRunning, JobCompleted, JobFailed:
		return nil
	default:
		return fmt.Errorf("%w: invalid job status %q", ErrInvalid, s)
	}
}

func (b KnowledgeBase) Validate() error {
	if strings.TrimSpace(string(b.ID)) == "" {
		return fmt.Errorf("%w: knowledge_base_id is required", ErrInvalid)
	}
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if err := b.OwnerType.Validate(); err != nil {
		return err
	}
	if b.OwnerType != OwnerGlobal && strings.TrimSpace(b.OwnerID) == "" {
		return fmt.Errorf("%w: owner_id is required", ErrInvalid)
	}
	if b.OwnerType == OwnerGlobal && b.Visibility != VisibilityPublic {
		return fmt.Errorf("%w: global knowledge base must be public", ErrInvalid)
	}
	if b.IsDefault && b.OwnerType != OwnerAgent && b.OwnerType != OwnerHuman {
		return fmt.Errorf("%w: default base is only allowed for agent or human owners", ErrInvalid)
	}
	if err := b.Visibility.Validate(); err != nil {
		return err
	}
	provider := b.EffectiveProvider()
	if err := provider.Validate(); err != nil {
		return err
	}
	if provider == ProviderLocal {
		if err := b.VectorBackend.Validate(); err != nil {
			return err
		}
	}
	if err := ValidateChunkPolicy(b.ChunkPolicy); err != nil {
		return err
	}
	if err := b.Status.Validate(); err != nil {
		return err
	}
	return nil
}

func (b KnowledgeBinding) Validate() error {
	if strings.TrimSpace(string(b.ID)) == "" {
		return fmt.Errorf("%w: binding_id is required", ErrInvalid)
	}
	if strings.TrimSpace(string(b.KnowledgeBaseID)) == "" {
		return fmt.Errorf("%w: knowledge_base_id is required", ErrInvalid)
	}
	if err := b.BoundToType.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(b.BoundToID) == "" {
		return fmt.Errorf("%w: bound_to_id is required", ErrInvalid)
	}
	if err := b.Access.Validate(); err != nil {
		return err
	}
	return nil
}

func (d KnowledgeDocument) Validate() error {
	if strings.TrimSpace(string(d.ID)) == "" {
		return fmt.Errorf("%w: document_id is required", ErrInvalid)
	}
	if strings.TrimSpace(string(d.KnowledgeBaseID)) == "" {
		return fmt.Errorf("%w: knowledge_base_id is required", ErrInvalid)
	}
	if strings.TrimSpace(d.ArtifactID) == "" && strings.TrimSpace(d.ProviderDocumentID) == "" {
		return fmt.Errorf("%w: artifact_id or provider_document_id is required", ErrInvalid)
	}
	if strings.TrimSpace(d.Filename) == "" {
		return fmt.Errorf("%w: filename is required", ErrInvalid)
	}
	return d.Status.Validate()
}

func (c KnowledgeChunk) Validate() error {
	if strings.TrimSpace(string(c.ID)) == "" {
		return fmt.Errorf("%w: chunk_id is required", ErrInvalid)
	}
	if strings.TrimSpace(string(c.KnowledgeBaseID)) == "" {
		return fmt.Errorf("%w: knowledge_base_id is required", ErrInvalid)
	}
	if strings.TrimSpace(string(c.DocumentID)) == "" {
		return fmt.Errorf("%w: document_id is required", ErrInvalid)
	}
	if c.EndOffset < c.StartOffset {
		return fmt.Errorf("%w: chunk end_offset before start_offset", ErrInvalid)
	}
	return nil
}
