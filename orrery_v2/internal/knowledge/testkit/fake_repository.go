package testkit

import (
	"context"
	"sort"
	"sync"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/store"
)

type FakeRepository struct {
	mu        sync.Mutex
	bases     map[core.KnowledgeBaseID]core.KnowledgeBase
	bindings  map[core.KnowledgeBindingID]core.KnowledgeBinding
	docs      map[core.KnowledgeDocumentID]core.KnowledgeDocument
	chunks    map[core.KnowledgeChunkID]core.KnowledgeChunk
	jobs      map[core.KnowledgeJobID]core.KnowledgeIngestJob
	revisions map[string]int64
}

func NewFakeRepository() *FakeRepository {
	return &FakeRepository{
		bases:     map[core.KnowledgeBaseID]core.KnowledgeBase{},
		bindings:  map[core.KnowledgeBindingID]core.KnowledgeBinding{},
		docs:      map[core.KnowledgeDocumentID]core.KnowledgeDocument{},
		chunks:    map[core.KnowledgeChunkID]core.KnowledgeChunk{},
		jobs:      map[core.KnowledgeJobID]core.KnowledgeIngestJob{},
		revisions: map[string]int64{},
	}
}

var _ store.Repository = (*FakeRepository)(nil)

func (r *FakeRepository) SaveKnowledgeBase(_ context.Context, base core.KnowledgeBase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := base.Validate(); err != nil {
		return err
	}
	if base.IsDefault {
		for _, existing := range r.bases {
			if existing.IsDefault && existing.OwnerType == base.OwnerType && existing.OwnerID == base.OwnerID && existing.ID != base.ID {
				return core.ErrConflict
			}
		}
	}
	r.bases[base.ID] = base
	return nil
}

func (r *FakeRepository) GetKnowledgeBase(_ context.Context, id core.KnowledgeBaseID) (core.KnowledgeBase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	base, ok := r.bases[id]
	if !ok {
		return core.KnowledgeBase{}, core.ErrNotFound
	}
	return base, nil
}

func (r *FakeRepository) ListKnowledgeBases(_ context.Context, query core.KnowledgeBaseQuery) (core.KnowledgeBasePage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]core.KnowledgeBase, 0)
	for _, base := range r.bases {
		if query.OwnerType != "" && base.OwnerType != query.OwnerType {
			continue
		}
		if query.OwnerID != "" && base.OwnerID != query.OwnerID {
			continue
		}
		if query.Status != "" && base.Status != query.Status {
			continue
		}
		if query.Visibility != "" && base.Visibility != query.Visibility {
			continue
		}
		items = append(items, base)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})
	return core.KnowledgeBasePage{Items: items, Total: len(items)}, nil
}

func (r *FakeRepository) CountKnowledgeBasesByStatus(_ context.Context) (map[core.KnowledgeStatus]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[core.KnowledgeStatus]int64{}
	for _, base := range r.bases {
		counts[base.Status]++
	}
	return counts, nil
}

func (r *FakeRepository) MarkKnowledgeBaseDeleting(ctx context.Context, id core.KnowledgeBaseID, at time.Time) error {
	base, err := r.GetKnowledgeBase(ctx, id)
	if err != nil {
		return err
	}
	base.Status = core.KnowledgeDeleting
	base.UpdatedAt = at
	return r.SaveKnowledgeBase(ctx, base)
}

func (r *FakeRepository) DeleteKnowledgeBaseMetadata(_ context.Context, id core.KnowledgeBaseID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bases, id)
	return nil
}

func (r *FakeRepository) SaveBinding(_ context.Context, binding core.KnowledgeBinding) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindings[binding.ID] = binding
	return nil
}

func (r *FakeRepository) GetBinding(_ context.Context, id core.KnowledgeBindingID) (core.KnowledgeBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	binding, ok := r.bindings[id]
	if !ok {
		return core.KnowledgeBinding{}, core.ErrNotFound
	}
	return binding, nil
}

func (r *FakeRepository) DeleteBinding(_ context.Context, id core.KnowledgeBindingID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bindings, id)
	return nil
}

func (r *FakeRepository) ListBindings(_ context.Context, query core.BindingQuery) ([]core.KnowledgeBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.KnowledgeBinding, 0)
	for _, binding := range r.bindings {
		if query.KnowledgeBaseID != "" && binding.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		out = append(out, binding)
	}
	return out, nil
}

func (r *FakeRepository) SaveDocument(_ context.Context, doc core.KnowledgeDocument) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.docs[doc.ID] = doc
	return nil
}

func (r *FakeRepository) UpdateDocumentStatus(ctx context.Context, id core.KnowledgeDocumentID, status core.DocumentStatus, message string, at time.Time) error {
	doc, err := r.GetDocument(ctx, id)
	if err != nil {
		return err
	}
	doc.Status = status
	doc.ErrorMessage = message
	doc.UpdatedAt = at
	if status == core.DocumentIndexed {
		doc.IndexedAt = at
	}
	return r.SaveDocument(ctx, doc)
}

func (r *FakeRepository) GetDocument(_ context.Context, id core.KnowledgeDocumentID) (core.KnowledgeDocument, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	doc, ok := r.docs[id]
	if !ok {
		return core.KnowledgeDocument{}, core.ErrNotFound
	}
	return doc, nil
}

func (r *FakeRepository) ListDocuments(_ context.Context, query core.DocumentQuery) (core.DocumentPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]core.KnowledgeDocument, 0)
	for _, doc := range r.docs {
		if query.KnowledgeBaseID != "" && doc.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		items = append(items, doc)
	}
	return core.DocumentPage{Items: items, Total: len(items)}, nil
}

func (r *FakeRepository) CountDocumentsByStatus(_ context.Context) (map[core.DocumentStatus]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[core.DocumentStatus]int64{}
	for _, doc := range r.docs {
		counts[doc.Status]++
	}
	return counts, nil
}

func (r *FakeRepository) DeleteDocumentMetadata(_ context.Context, id core.KnowledgeDocumentID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.docs, id)
	return nil
}

func (r *FakeRepository) ReplaceChunks(_ context.Context, documentID core.KnowledgeDocumentID, chunks []core.KnowledgeChunk) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, chunk := range r.chunks {
		if chunk.DocumentID == documentID {
			delete(r.chunks, id)
		}
	}
	for _, chunk := range chunks {
		r.chunks[chunk.ID] = chunk
	}
	return nil
}

func (r *FakeRepository) GetChunk(_ context.Context, id core.KnowledgeChunkID) (core.KnowledgeChunk, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	chunk, ok := r.chunks[id]
	if !ok {
		return core.KnowledgeChunk{}, core.ErrNotFound
	}
	return chunk, nil
}

func (r *FakeRepository) ListChunks(_ context.Context, query core.ChunkQuery) ([]core.KnowledgeChunk, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.KnowledgeChunk, 0)
	for _, chunk := range r.chunks {
		if query.KnowledgeBaseID != "" && chunk.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		if query.DocumentID != "" && chunk.DocumentID != query.DocumentID {
			continue
		}
		out = append(out, chunk)
	}
	return out, nil
}

func (r *FakeRepository) SaveIngestJob(_ context.Context, job core.KnowledgeIngestJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[job.ID] = job
	return nil
}

func (r *FakeRepository) GetIngestJob(_ context.Context, id core.KnowledgeJobID) (core.KnowledgeIngestJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return core.KnowledgeIngestJob{}, core.ErrNotFound
	}
	return job, nil
}

func (r *FakeRepository) ListIngestJobs(_ context.Context, query core.JobQuery) ([]core.KnowledgeIngestJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.KnowledgeIngestJob, 0)
	for _, job := range r.jobs {
		if query.KnowledgeBaseID != "" && job.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		if query.DocumentID != "" && job.DocumentID != query.DocumentID {
			continue
		}
		if query.Status != "" && job.Status != query.Status {
			continue
		}
		out = append(out, job)
	}
	return out, nil
}

func (r *FakeRepository) CountIngestJobsByStatus(_ context.Context) (map[core.JobStatus]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[core.JobStatus]int64{}
	for _, job := range r.jobs {
		counts[job.Status]++
	}
	return counts, nil
}

func (r *FakeRepository) UpdateIngestJobStatus(ctx context.Context, id core.KnowledgeJobID, status core.JobStatus, message string, at time.Time) error {
	job, err := r.GetIngestJob(ctx, id)
	if err != nil {
		return err
	}
	job.Status = status
	job.ErrorMessage = message
	job.UpdatedAt = at
	return r.SaveIngestJob(ctx, job)
}

func (r *FakeRepository) NextEventRevision(_ context.Context, streamID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if streamID == "" {
		return 0, core.ErrInvalid
	}
	r.revisions[streamID]++
	return r.revisions[streamID], nil
}
