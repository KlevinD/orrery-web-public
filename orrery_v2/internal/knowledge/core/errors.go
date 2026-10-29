package core

import "errors"

var (
	ErrInvalid                = errors.New("knowledge invalid")
	ErrNotFound               = errors.New("knowledge not found")
	ErrPermissionDenied       = errors.New("knowledge permission denied")
	ErrConflict               = errors.New("knowledge conflict")
	ErrDependencyUnavailable  = errors.New("knowledge dependency unavailable")
	ErrIngestFailed           = errors.New("knowledge ingest failed")
	ErrVectorStoreUnavailable = errors.New("knowledge vector store unavailable")
	ErrEmbeddingFailed        = errors.New("knowledge embedding failed")
	ErrDocumentUnsupported    = errors.New("knowledge document unsupported")
	ErrDeleteFailed           = errors.New("knowledge delete failed")
)
