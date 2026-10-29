package vector

import "errors"

var (
	ErrUnsafePath        = errors.New("knowledge vector unsafe path")
	ErrStoreUnavailable  = errors.New("knowledge vector store unavailable")
	ErrUnsupportedSearch = errors.New("knowledge vector search unsupported")
)
