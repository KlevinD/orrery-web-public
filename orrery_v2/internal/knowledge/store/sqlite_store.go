package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/platform/db"
)

const (
	baseTable          = "knowledge_bases"
	bindingTable       = "knowledge_bindings"
	documentTable      = "knowledge_documents"
	chunkTable         = "knowledge_chunks"
	jobTable           = "knowledge_ingest_jobs"
	eventRevisionTable = "knowledge_event_revisions"
)

type PlatformStore struct {
	store *db.Store
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{store: store}
}

func (s *PlatformStore) SaveKnowledgeBase(ctx context.Context, base core.KnowledgeBase) error {
	if err := base.Validate(); err != nil {
		return err
	}
	if err := s.ensureDefaultUnique(ctx, base); err != nil {
		return err
	}
	return s.putPayload(ctx, baseTable, string(base.ID), baseRecord(base, mustJSON(base)))
}

func (s *PlatformStore) GetKnowledgeBase(ctx context.Context, id core.KnowledgeBaseID) (core.KnowledgeBase, error) {
	record, ok, err := s.store.GetRecord(ctx, baseTable, string(id))
	if err != nil {
		return core.KnowledgeBase{}, err
	}
	if !ok {
		return core.KnowledgeBase{}, fmt.Errorf("%w: %s", core.ErrNotFound, id)
	}
	return decodePayload[core.KnowledgeBase](record, "knowledge base")
}

func (s *PlatformStore) GetKnowledgeBases(ctx context.Context, ids []core.KnowledgeBaseID) (map[core.KnowledgeBaseID]core.KnowledgeBase, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, string(id))
	}
	records, err := s.getRecords(ctx, baseTable, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[core.KnowledgeBaseID]core.KnowledgeBase, len(records))
	for key, record := range records {
		base, err := decodePayload[core.KnowledgeBase](record, "knowledge base")
		if err != nil {
			return nil, err
		}
		if string(base.ID) != key {
			return nil, fmt.Errorf("%w: knowledge base identity mismatch", core.ErrInvalid)
		}
		out[base.ID] = base
	}
	return out, nil
}

func (s *PlatformStore) ListKnowledgeBases(ctx context.Context, query core.KnowledgeBaseQuery) (core.KnowledgeBasePage, error) {
	records, err := s.listRecords(ctx, baseTable)
	if err != nil {
		return core.KnowledgeBasePage{}, err
	}
	items := make([]core.KnowledgeBase, 0)
	for _, item := range records {
		base, err := decodePayload[core.KnowledgeBase](item.Record, "knowledge base")
		if err != nil {
			return core.KnowledgeBasePage{}, err
		}
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
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	total := len(items)
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return core.KnowledgeBasePage{Items: items, Total: total}, nil
}

func (s *PlatformStore) CountKnowledgeBasesByStatus(ctx context.Context) (map[core.KnowledgeStatus]int64, error) {
	records, err := s.listRecords(ctx, baseTable)
	if err != nil {
		return nil, err
	}
	counts := map[core.KnowledgeStatus]int64{}
	for _, item := range records {
		base, err := decodePayload[core.KnowledgeBase](item.Record, "knowledge base")
		if err != nil {
			return nil, err
		}
		counts[base.Status]++
	}
	return counts, nil
}

func (s *PlatformStore) MarkKnowledgeBaseDeleting(ctx context.Context, id core.KnowledgeBaseID, at time.Time) error {
	base, err := s.GetKnowledgeBase(ctx, id)
	if err != nil {
		return err
	}
	base.Status = core.KnowledgeDeleting
	base.UpdatedAt = at
	return s.SaveKnowledgeBase(ctx, base)
}

func (s *PlatformStore) DeleteKnowledgeBaseMetadata(ctx context.Context, id core.KnowledgeBaseID) error {
	if err := s.store.DeleteRecord(ctx, baseTable, string(id)); err != nil {
		return err
	}
	return nil
}

func (s *PlatformStore) SaveBinding(ctx context.Context, binding core.KnowledgeBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	return s.putPayload(ctx, bindingTable, string(binding.ID), bindingRecord(binding, mustJSON(binding)))
}

func (s *PlatformStore) GetBinding(ctx context.Context, id core.KnowledgeBindingID) (core.KnowledgeBinding, error) {
	record, ok, err := s.store.GetRecord(ctx, bindingTable, string(id))
	if err != nil {
		return core.KnowledgeBinding{}, err
	}
	if !ok {
		return core.KnowledgeBinding{}, fmt.Errorf("%w: %s", core.ErrNotFound, id)
	}
	return decodePayload[core.KnowledgeBinding](record, "knowledge binding")
}

func (s *PlatformStore) DeleteBinding(ctx context.Context, id core.KnowledgeBindingID) error {
	return s.store.DeleteRecord(ctx, bindingTable, string(id))
}

func (s *PlatformStore) ListBindings(ctx context.Context, query core.BindingQuery) ([]core.KnowledgeBinding, error) {
	records, err := s.listRecords(ctx, bindingTable)
	if err != nil {
		return nil, err
	}
	items := make([]core.KnowledgeBinding, 0)
	for _, item := range records {
		binding, err := decodePayload[core.KnowledgeBinding](item.Record, "knowledge binding")
		if err != nil {
			return nil, err
		}
		if query.KnowledgeBaseID != "" && binding.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		if query.BoundToType != "" && binding.BoundToType != query.BoundToType {
			continue
		}
		if query.BoundToID != "" && binding.BoundToID != query.BoundToID {
			continue
		}
		items = append(items, binding)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *PlatformStore) SaveDocument(ctx context.Context, doc core.KnowledgeDocument) error {
	if err := doc.Validate(); err != nil {
		return err
	}
	return s.putPayload(ctx, documentTable, string(doc.ID), documentRecord(doc, mustJSON(doc)))
}

func (s *PlatformStore) UpdateDocumentStatus(ctx context.Context, id core.KnowledgeDocumentID, status core.DocumentStatus, message string, at time.Time) error {
	doc, err := s.GetDocument(ctx, id)
	if err != nil {
		return err
	}
	doc.Status = status
	doc.ErrorMessage = message
	doc.UpdatedAt = at
	if status == core.DocumentIndexed {
		doc.IndexedAt = at
	}
	return s.SaveDocument(ctx, doc)
}

func (s *PlatformStore) GetDocument(ctx context.Context, id core.KnowledgeDocumentID) (core.KnowledgeDocument, error) {
	record, ok, err := s.store.GetRecord(ctx, documentTable, string(id))
	if err != nil {
		return core.KnowledgeDocument{}, err
	}
	if !ok {
		return core.KnowledgeDocument{}, fmt.Errorf("%w: %s", core.ErrNotFound, id)
	}
	return decodePayload[core.KnowledgeDocument](record, "knowledge document")
}

func (s *PlatformStore) GetDocuments(ctx context.Context, ids []core.KnowledgeDocumentID) (map[core.KnowledgeDocumentID]core.KnowledgeDocument, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, string(id))
	}
	records, err := s.getRecords(ctx, documentTable, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[core.KnowledgeDocumentID]core.KnowledgeDocument, len(records))
	for key, record := range records {
		document, err := decodePayload[core.KnowledgeDocument](record, "knowledge document")
		if err != nil {
			return nil, err
		}
		if string(document.ID) != key {
			return nil, fmt.Errorf("%w: knowledge document identity mismatch", core.ErrInvalid)
		}
		out[document.ID] = document
	}
	return out, nil
}

func (s *PlatformStore) ListDocuments(ctx context.Context, query core.DocumentQuery) (core.DocumentPage, error) {
	records, err := s.listRecords(ctx, documentTable)
	if err != nil {
		return core.DocumentPage{}, err
	}
	items := make([]core.KnowledgeDocument, 0)
	for _, item := range records {
		doc, err := decodePayload[core.KnowledgeDocument](item.Record, "knowledge document")
		if err != nil {
			return core.DocumentPage{}, err
		}
		if query.KnowledgeBaseID != "" && doc.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		if query.Status != "" && doc.Status != query.Status {
			continue
		}
		items = append(items, doc)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	total := len(items)
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return core.DocumentPage{Items: items, Total: total}, nil
}

func (s *PlatformStore) CountDocumentsByStatus(ctx context.Context) (map[core.DocumentStatus]int64, error) {
	records, err := s.listRecords(ctx, documentTable)
	if err != nil {
		return nil, err
	}
	counts := map[core.DocumentStatus]int64{}
	for _, item := range records {
		doc, err := decodePayload[core.KnowledgeDocument](item.Record, "knowledge document")
		if err != nil {
			return nil, err
		}
		counts[doc.Status]++
	}
	return counts, nil
}

func (s *PlatformStore) DeleteDocumentMetadata(ctx context.Context, id core.KnowledgeDocumentID) error {
	return s.store.DeleteRecord(ctx, documentTable, string(id))
}

func (s *PlatformStore) ReplaceChunks(ctx context.Context, documentID core.KnowledgeDocumentID, chunks []core.KnowledgeChunk) error {
	records, err := s.listRecords(ctx, chunkTable)
	if err != nil {
		return err
	}
	for _, item := range records {
		chunk, err := decodePayload[core.KnowledgeChunk](item.Record, "knowledge chunk")
		if err != nil {
			return err
		}
		if chunk.DocumentID == documentID {
			if err := s.store.DeleteRecord(ctx, chunkTable, item.Key); err != nil {
				return err
			}
		}
	}
	for _, chunk := range chunks {
		if err := chunk.Validate(); err != nil {
			return err
		}
		if err := s.putPayload(ctx, chunkTable, string(chunk.ID), chunkRecord(chunk, mustJSON(chunk))); err != nil {
			return err
		}
	}
	return nil
}

func (s *PlatformStore) GetChunk(ctx context.Context, id core.KnowledgeChunkID) (core.KnowledgeChunk, error) {
	record, ok, err := s.store.GetRecord(ctx, chunkTable, string(id))
	if err != nil {
		return core.KnowledgeChunk{}, err
	}
	if !ok {
		return core.KnowledgeChunk{}, fmt.Errorf("%w: %s", core.ErrNotFound, id)
	}
	return decodePayload[core.KnowledgeChunk](record, "knowledge chunk")
}

func (s *PlatformStore) GetChunks(ctx context.Context, ids []core.KnowledgeChunkID) (map[core.KnowledgeChunkID]core.KnowledgeChunk, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, string(id))
	}
	records, err := s.getRecords(ctx, chunkTable, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[core.KnowledgeChunkID]core.KnowledgeChunk, len(records))
	for key, record := range records {
		chunk, err := decodePayload[core.KnowledgeChunk](record, "knowledge chunk")
		if err != nil {
			return nil, err
		}
		if string(chunk.ID) != key {
			return nil, fmt.Errorf("%w: knowledge chunk identity mismatch", core.ErrInvalid)
		}
		out[chunk.ID] = chunk
	}
	return out, nil
}

func (s *PlatformStore) ListChunks(ctx context.Context, query core.ChunkQuery) ([]core.KnowledgeChunk, error) {
	records, err := s.listRecords(ctx, chunkTable)
	if err != nil {
		return nil, err
	}
	items := make([]core.KnowledgeChunk, 0)
	for _, item := range records {
		chunk, err := decodePayload[core.KnowledgeChunk](item.Record, "knowledge chunk")
		if err != nil {
			return nil, err
		}
		if query.KnowledgeBaseID != "" && chunk.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		if query.DocumentID != "" && chunk.DocumentID != query.DocumentID {
			continue
		}
		items = append(items, chunk)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DocumentID == items[j].DocumentID {
			return items[i].Ordinal < items[j].Ordinal
		}
		return items[i].DocumentID < items[j].DocumentID
	})
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items, nil
}

func (s *PlatformStore) SaveIngestJob(ctx context.Context, job core.KnowledgeIngestJob) error {
	if err := job.Status.Validate(); err != nil {
		return err
	}
	return s.putPayload(ctx, jobTable, string(job.ID), jobRecord(job, mustJSON(job)))
}

func (s *PlatformStore) GetIngestJob(ctx context.Context, id core.KnowledgeJobID) (core.KnowledgeIngestJob, error) {
	record, ok, err := s.store.GetRecord(ctx, jobTable, string(id))
	if err != nil {
		return core.KnowledgeIngestJob{}, err
	}
	if !ok {
		return core.KnowledgeIngestJob{}, fmt.Errorf("%w: %s", core.ErrNotFound, id)
	}
	return decodePayload[core.KnowledgeIngestJob](record, "knowledge ingest job")
}

func (s *PlatformStore) ListIngestJobs(ctx context.Context, query core.JobQuery) ([]core.KnowledgeIngestJob, error) {
	records, err := s.listRecords(ctx, jobTable)
	if err != nil {
		return nil, err
	}
	items := make([]core.KnowledgeIngestJob, 0)
	for _, item := range records {
		job, err := decodePayload[core.KnowledgeIngestJob](item.Record, "knowledge ingest job")
		if err != nil {
			return nil, err
		}
		if query.KnowledgeBaseID != "" && job.KnowledgeBaseID != query.KnowledgeBaseID {
			continue
		}
		if query.DocumentID != "" && job.DocumentID != query.DocumentID {
			continue
		}
		if query.Status != "" && job.Status != query.Status {
			continue
		}
		items = append(items, job)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items, nil
}

func (s *PlatformStore) CountIngestJobsByStatus(ctx context.Context) (map[core.JobStatus]int64, error) {
	records, err := s.listRecords(ctx, jobTable)
	if err != nil {
		return nil, err
	}
	counts := map[core.JobStatus]int64{}
	for _, item := range records {
		job, err := decodePayload[core.KnowledgeIngestJob](item.Record, "knowledge ingest job")
		if err != nil {
			return nil, err
		}
		counts[job.Status]++
	}
	return counts, nil
}

func (s *PlatformStore) listRecords(ctx context.Context, table string) ([]db.KeyedRecord, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("%w: knowledge store is not configured", core.ErrDependencyUnavailable)
	}
	items := make([]db.KeyedRecord, 0)
	afterKey := ""
	for {
		page, err := s.store.ListRecords(ctx, table, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if !page.HasMore {
			return items, nil
		}
		afterKey = page.Items[len(page.Items)-1].Key
	}
}

func (s *PlatformStore) getRecords(ctx context.Context, table string, keys []string) (map[string]db.Record, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("%w: knowledge store is not configured", core.ErrDependencyUnavailable)
	}
	records := make(map[string]db.Record, len(keys))
	for start := 0; start < len(keys); start += db.MaxRecordPageLimit {
		end := min(start+db.MaxRecordPageLimit, len(keys))
		batch, err := s.store.GetRecords(ctx, table, keys[start:end])
		if err != nil {
			return nil, err
		}
		for key, record := range batch {
			records[key] = record
		}
	}
	return records, nil
}

func (s *PlatformStore) UpdateIngestJobStatus(ctx context.Context, id core.KnowledgeJobID, status core.JobStatus, message string, at time.Time) error {
	job, err := s.GetIngestJob(ctx, id)
	if err != nil {
		return err
	}
	job.Status = status
	job.ErrorMessage = message
	job.UpdatedAt = at
	return s.SaveIngestJob(ctx, job)
}

func (s *PlatformStore) NextEventRevision(ctx context.Context, streamID string) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("%w: knowledge store is not configured", core.ErrDependencyUnavailable)
	}
	if streamID == "" {
		return 0, fmt.Errorf("%w: event revision stream id is required", core.ErrInvalid)
	}
	var revision int64
	err := s.store.QueryRow(ctx, `
INSERT INTO knowledge_event_revisions (stream_id, last_revision)
VALUES (?, 1)
ON CONFLICT(stream_id) DO UPDATE SET last_revision = knowledge_event_revisions.last_revision + 1
RETURNING last_revision`, streamID).Scan(&revision)
	if err != nil {
		return 0, fmt.Errorf("allocate knowledge event revision: %w", err)
	}
	return revision, nil
}

func (s *PlatformStore) ensureDefaultUnique(ctx context.Context, base core.KnowledgeBase) error {
	if !base.IsDefault {
		return nil
	}
	page, err := s.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{OwnerType: base.OwnerType, OwnerID: base.OwnerID})
	if err != nil {
		return err
	}
	for _, existing := range page.Items {
		if existing.IsDefault && existing.ID != base.ID {
			return fmt.Errorf("%w: default knowledge base already exists for %s %s", core.ErrConflict, base.OwnerType, base.OwnerID)
		}
	}
	return nil
}

func (s *PlatformStore) putPayload(ctx context.Context, table, key string, record db.Record) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: knowledge store is not configured", core.ErrDependencyUnavailable)
	}
	return s.store.PutRecord(ctx, table, key, record)
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func decodePayload[T any](record db.Record, label string) (T, error) {
	var out T
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return out, fmt.Errorf("%w: %s payload missing", core.ErrInvalid, label)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("decode %s: %w", label, err)
	}
	return out, nil
}

func baseRecord(base core.KnowledgeBase, payload string) db.Record {
	return db.Record{"owner_type": string(base.OwnerType), "owner_id": base.OwnerID, "is_default": base.IsDefault, "status": string(base.Status), "visibility": string(base.Visibility), "payload": payload}
}

func bindingRecord(binding core.KnowledgeBinding, payload string) db.Record {
	return db.Record{"knowledge_base_id": string(binding.KnowledgeBaseID), "bound_to_type": string(binding.BoundToType), "bound_to_id": binding.BoundToID, "payload": payload}
}

func documentRecord(doc core.KnowledgeDocument, payload string) db.Record {
	return db.Record{"knowledge_base_id": string(doc.KnowledgeBaseID), "status": string(doc.Status), "payload": payload}
}

func chunkRecord(chunk core.KnowledgeChunk, payload string) db.Record {
	return db.Record{"knowledge_base_id": string(chunk.KnowledgeBaseID), "document_id": string(chunk.DocumentID), "payload": payload}
}

func jobRecord(job core.KnowledgeIngestJob, payload string) db.Record {
	return db.Record{"knowledge_base_id": string(job.KnowledgeBaseID), "status": string(job.Status), "payload": payload}
}
