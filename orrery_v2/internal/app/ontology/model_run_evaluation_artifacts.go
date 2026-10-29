package ontology

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type ModelRunEvaluationArtifact struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	Engine       string            `json:"engine"`
	RunID        string            `json:"run_id"`
	ArtifactKind string            `json:"artifact_kind"`
	ArtifactURI  string            `json:"artifact_uri"`
	ArtifactHash string            `json:"artifact_hash,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	RecordedAt   time.Time         `json:"recorded_at,omitempty"`
	CreatedAt    time.Time         `json:"created_at,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at,omitempty"`
}

const ModelRunEvaluationArtifactKindSkillVersionEvaluation = "skill_version_evaluation"

type ModelRunEvaluationArtifactQuery struct {
	TenantID     string
	Engine       string
	RunID        string
	ArtifactKind string
	Limit        int
	Offset       int
}

type ModelRunEvaluationArtifactPage struct {
	Artifacts []ModelRunEvaluationArtifact `json:"artifacts"`
	Page      Page                         `json:"page"`
}

type ModelRunEvaluationArtifactStore interface {
	SaveModelRunEvaluationArtifact(ctx context.Context, artifact ModelRunEvaluationArtifact) error
	ListModelRunEvaluationArtifacts(ctx context.Context, query ModelRunEvaluationArtifactQuery) (ModelRunEvaluationArtifactPage, error)
}

type ModelRunEvaluationArtifactProviderProbe interface {
	ProbeModelRunEvaluationArtifact(ctx context.Context, artifact ModelRunEvaluationArtifact) (ModelRunEvaluationArtifactProviderProbeResult, error)
}

type ModelRunEvaluationArtifactProviderProbeResult struct {
	Available bool
	Metadata  map[string]string
}

type ScopedModelRunEvaluationArtifactProviderProbe struct {
	provider        string
	allowedPrefixes []string
	delegate        ModelRunEvaluationArtifactProviderProbe
}

type modelRunEvaluationArtifactMetadataSchema struct {
	ID           string
	RequiredKeys []string
}

func NewScopedModelRunEvaluationArtifactProviderProbe(provider string, allowedPrefixes []string, delegate ModelRunEvaluationArtifactProviderProbe) ScopedModelRunEvaluationArtifactProviderProbe {
	return ScopedModelRunEvaluationArtifactProviderProbe{
		provider:        normalizeID(provider),
		allowedPrefixes: normalizeModelRunEvaluationArtifactResourcePrefixes(allowedPrefixes),
		delegate:        delegate,
	}
}

func (p ScopedModelRunEvaluationArtifactProviderProbe) ProbeModelRunEvaluationArtifact(ctx context.Context, artifact ModelRunEvaluationArtifact) (ModelRunEvaluationArtifactProviderProbeResult, error) {
	if p.delegate == nil {
		return ModelRunEvaluationArtifactProviderProbeResult{}, fmt.Errorf("%w: scoped model run evaluation artifact provider probe delegate is required", ErrInvalidDefinition)
	}
	if len(p.allowedPrefixes) == 0 {
		return ModelRunEvaluationArtifactProviderProbeResult{}, fmt.Errorf("%w: scoped model run evaluation artifact provider probe allowed prefixes are required", ErrInvalidDefinition)
	}
	if err := ValidateModelRunEvaluationArtifactReference(&artifact); err != nil {
		return ModelRunEvaluationArtifactProviderProbeResult{}, err
	}
	metadata := cloneStringMetadata(artifact.Metadata)
	provider := normalizeID(metadata["artifact_health_provider"])
	if p.provider != "" && provider != p.provider {
		return ModelRunEvaluationArtifactProviderProbeResult{}, fmt.Errorf("%w: model run evaluation artifact provider %q does not match scoped provider %q", ErrInvalidDefinition, provider, p.provider)
	}
	resource := strings.TrimSpace(metadata["artifact_health_resource"])
	if !modelRunEvaluationArtifactResourceAllowed(resource, p.allowedPrefixes) {
		return ModelRunEvaluationArtifactProviderProbeResult{}, fmt.Errorf("%w: model run evaluation artifact resource %q is not allowed for provider %q", ErrInvalidDefinition, resource, provider)
	}
	return p.delegate.ProbeModelRunEvaluationArtifact(ctx, artifact)
}

type MemoryModelRunEvaluationArtifactStore struct {
	mu        sync.RWMutex
	artifacts map[string]ModelRunEvaluationArtifact
	now       func() time.Time
}

func NewMemoryModelRunEvaluationArtifactStore() *MemoryModelRunEvaluationArtifactStore {
	return &MemoryModelRunEvaluationArtifactStore{artifacts: map[string]ModelRunEvaluationArtifact{}, now: time.Now}
}

func (s *MemoryModelRunEvaluationArtifactStore) SaveModelRunEvaluationArtifact(ctx context.Context, artifact ModelRunEvaluationArtifact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationArtifact(artifact, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.artifacts[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	s.artifacts[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationArtifactStore) ListModelRunEvaluationArtifacts(ctx context.Context, query ModelRunEvaluationArtifactQuery) (ModelRunEvaluationArtifactPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationArtifactPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]ModelRunEvaluationArtifact, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		if modelRunEvaluationArtifactMatches(artifact, query) {
			matched = append(matched, cloneModelRunEvaluationArtifact(artifact))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].RecordedAt.Equal(matched[j].RecordedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].RecordedAt.Before(matched[j].RecordedAt)
	})
	return modelRunEvaluationArtifactPage(matched, query), nil
}

type PlatformModelRunEvaluationArtifactStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunEvaluationArtifactStore(store *db.Store) *PlatformModelRunEvaluationArtifactStore {
	return &PlatformModelRunEvaluationArtifactStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationArtifactStoreTx(tx db.Tx) *PlatformModelRunEvaluationArtifactStore {
	return &PlatformModelRunEvaluationArtifactStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationArtifactStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation artifact store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationArtifactStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationArtifactStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationArtifactStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationArtifactStore) SaveModelRunEvaluationArtifact(ctx context.Context, artifact ModelRunEvaluationArtifact) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationArtifact(artifact, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_artifacts (
	id, tenant_id, engine, run_id, artifact_kind, artifact_uri, artifact_hash,
	summary, metadata_json, recorded_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	artifact_kind = excluded.artifact_kind,
	artifact_uri = excluded.artifact_uri,
	artifact_hash = excluded.artifact_hash,
	summary = excluded.summary,
	metadata_json = excluded.metadata_json,
	recorded_at = excluded.recorded_at,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.Engine,
		normalized.RunID,
		normalized.ArtifactKind,
		normalized.ArtifactURI,
		normalized.ArtifactHash,
		normalized.Summary,
		mustJSON(normalized.Metadata),
		timeToUnixNano(normalized.RecordedAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation artifact: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationArtifactStore) ListModelRunEvaluationArtifacts(ctx context.Context, query ModelRunEvaluationArtifactQuery) (ModelRunEvaluationArtifactPage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationArtifactPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationArtifactWhere(query)
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_run_evaluation_artifacts`+where, args...).Scan(&total); err != nil {
		return ModelRunEvaluationArtifactPage{}, fmt.Errorf("count ontology model run evaluation artifacts: %w", err)
	}
	rows, err := s.query(ctx, `SELECT id, tenant_id, engine, run_id, artifact_kind, artifact_uri, artifact_hash,
summary, metadata_json, recorded_at, created_at, updated_at FROM ontology_model_run_evaluation_artifacts`+where+` ORDER BY recorded_at ASC, id ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return ModelRunEvaluationArtifactPage{}, fmt.Errorf("list ontology model run evaluation artifacts: %w", err)
	}
	defer rows.Close()
	artifacts := []ModelRunEvaluationArtifact{}
	for rows.Next() {
		artifact, err := scanModelRunEvaluationArtifact(rows)
		if err != nil {
			return ModelRunEvaluationArtifactPage{}, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationArtifactPage{}, err
	}
	return ModelRunEvaluationArtifactPage{
		Artifacts: artifacts,
		Page:      Page{Limit: limit, Offset: offset, Total: total, Returned: len(artifacts), HasMore: offset+len(artifacts) < total},
	}, nil
}

func normalizeModelRunEvaluationArtifact(artifact ModelRunEvaluationArtifact, now func() time.Time) (ModelRunEvaluationArtifact, error) {
	artifact.TenantID = strings.TrimSpace(artifact.TenantID)
	artifact.Engine = normalizeID(artifact.Engine)
	artifact.RunID = strings.TrimSpace(artifact.RunID)
	artifact.ArtifactKind = normalizeID(artifact.ArtifactKind)
	artifact.ArtifactURI = strings.TrimSpace(artifact.ArtifactURI)
	artifact.ArtifactHash = strings.TrimSpace(artifact.ArtifactHash)
	artifact.Summary = strings.TrimSpace(artifact.Summary)
	artifact.Metadata = cloneStringMetadata(artifact.Metadata)
	if artifact.TenantID == "" || artifact.Engine == "" || artifact.RunID == "" || artifact.ArtifactKind == "" || artifact.ArtifactURI == "" {
		return ModelRunEvaluationArtifact{}, fmt.Errorf("%w: model run evaluation artifact tenant_id, engine, run_id, artifact_kind, and artifact_uri are required", ErrInvalidDefinition)
	}
	if err := ValidateModelRunEvaluationArtifactReference(&artifact); err != nil {
		return ModelRunEvaluationArtifact{}, err
	}
	if artifact.ID == "" {
		artifact.ID = modelRunEvaluationArtifactID(artifact.TenantID, artifact.Engine, artifact.RunID, artifact.ArtifactKind, artifact.ArtifactURI)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = at
	}
	if artifact.RecordedAt.IsZero() {
		artifact.RecordedAt = at
	}
	artifact.UpdatedAt = at
	return artifact, nil
}

func ValidateModelRunEvaluationArtifactReference(artifact *ModelRunEvaluationArtifact) error {
	if artifact == nil {
		return fmt.Errorf("%w: model run evaluation artifact is required", ErrInvalidDefinition)
	}
	uri := strings.TrimSpace(artifact.ArtifactURI)
	if uri == "" {
		return fmt.Errorf("%w: model run evaluation artifact artifact_uri is required", ErrInvalidDefinition)
	}
	if err := validateEvaluationArtifactURI(uri); err != nil {
		return err
	}
	artifact.ArtifactURI = uri
	metadata := cloneStringMetadata(artifact.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["artifact_ref_validation_status"] = "syntax_passed"
	metadata["artifact_ref_validation_mode"] = "uri_syntax"
	annotateModelRunEvaluationArtifactHealth(uri, metadata)
	annotateModelRunEvaluationArtifactMetadataSchema(artifact.ArtifactKind, metadata)
	artifact.Metadata = metadata
	return nil
}

func ProbeModelRunEvaluationArtifactHealth(artifact ModelRunEvaluationArtifact, now func() time.Time) (ModelRunEvaluationArtifact, error) {
	return ProbeModelRunEvaluationArtifactHealthWithProviders(artifact, now, nil)
}

func ProbeModelRunEvaluationArtifactHealthWithProviders(artifact ModelRunEvaluationArtifact, now func() time.Time, providers map[string]ModelRunEvaluationArtifactProviderProbe) (ModelRunEvaluationArtifact, error) {
	if err := ValidateModelRunEvaluationArtifactReference(&artifact); err != nil {
		return ModelRunEvaluationArtifact{}, err
	}
	if now == nil {
		now = time.Now
	}
	metadata := cloneStringMetadata(artifact.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["artifact_health_probe_mode"] = "manual_probe"
	metadata["artifact_health_probed_at"] = now().UTC().Format(time.RFC3339Nano)
	delete(metadata, "artifact_health_error")
	delete(metadata, "artifact_health_file_size_bytes")
	if probe, ok := providers[metadata["artifact_health_provider"]]; ok && probe != nil {
		result, err := probe.ProbeModelRunEvaluationArtifact(context.Background(), artifact)
		if err != nil {
			metadata["artifact_health_status"] = "unavailable"
			metadata["artifact_health_error"] = err.Error()
			artifact.Metadata = metadata
			return artifact, nil
		}
		if result.Available {
			metadata["artifact_health_status"] = "available"
		} else {
			metadata["artifact_health_status"] = "unavailable"
		}
		for key, value := range cloneStringMetadata(result.Metadata) {
			metadata[key] = value
		}
		artifact.Metadata = metadata
		return artifact, nil
	}
	if metadata["artifact_health_provider"] != "file" {
		metadata["artifact_health_status"] = "probe_unsupported"
		artifact.Metadata = metadata
		return artifact, nil
	}
	resource := strings.TrimSpace(metadata["artifact_health_resource"])
	info, err := os.Stat(resource)
	if err != nil {
		metadata["artifact_health_status"] = "unavailable"
		metadata["artifact_health_error"] = err.Error()
		artifact.Metadata = metadata
		return artifact, nil
	}
	if info.IsDir() {
		metadata["artifact_health_status"] = "unavailable"
		metadata["artifact_health_error"] = "artifact path is a directory"
		artifact.Metadata = metadata
		return artifact, nil
	}
	metadata["artifact_health_status"] = "available"
	metadata["artifact_health_file_size_bytes"] = strconv.FormatInt(info.Size(), 10)
	artifact.Metadata = metadata
	return artifact, nil
}

func annotateModelRunEvaluationArtifactHealth(uri string, metadata map[string]string) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return
	}
	provider := strings.ToLower(parsed.Scheme)
	metadata["artifact_health_provider"] = provider
	if strings.TrimSpace(metadata["artifact_health_probe_mode"]) == "" {
		metadata["artifact_health_probe_mode"] = "provider_metadata"
	}
	if strings.TrimSpace(metadata["artifact_health_status"]) == "" {
		metadata["artifact_health_status"] = "probe_not_run"
	}
	switch provider {
	case "s3":
		metadata["artifact_health_probe_capability"] = "head_object"
		metadata["artifact_health_resource"] = modelRunEvaluationArtifactObjectResource(parsed)
	case "gs":
		metadata["artifact_health_probe_capability"] = "objects_get"
		metadata["artifact_health_resource"] = modelRunEvaluationArtifactObjectResource(parsed)
	case "azblob":
		metadata["artifact_health_probe_capability"] = "get_blob_properties"
		metadata["artifact_health_resource"] = modelRunEvaluationArtifactObjectResource(parsed)
	case "file":
		metadata["artifact_health_probe_capability"] = "stat"
		metadata["artifact_health_resource"] = modelRunEvaluationArtifactFileResource(parsed)
	default:
		metadata["artifact_health_probe_capability"] = "unsupported"
		metadata["artifact_health_resource"] = strings.TrimSpace(uri)
	}
}

func modelRunEvaluationArtifactObjectResource(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(parsed.Host)+"/"+strings.Trim(parsed.Path, "/"), "/")
}

func modelRunEvaluationArtifactFileResource(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	path := parsed.Path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}

func normalizeModelRunEvaluationArtifactResourcePrefixes(prefixes []string) []string {
	normalized := []string{}
	for _, prefix := range prefixes {
		prefix = strings.Trim(strings.TrimSpace(prefix), "/")
		if prefix == "" {
			continue
		}
		normalized = append(normalized, prefix)
	}
	return normalized
}

func modelRunEvaluationArtifactResourceAllowed(resource string, prefixes []string) bool {
	resource = strings.Trim(strings.TrimSpace(resource), "/")
	for _, prefix := range prefixes {
		if resource == prefix || strings.HasPrefix(resource, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func annotateModelRunEvaluationArtifactMetadataSchema(artifactKind string, metadata map[string]string) {
	schema := modelRunEvaluationArtifactMetadataSchemaForKind(artifactKind)
	metadata["artifact_metadata_schema_id"] = schema.ID
	metadata["artifact_metadata_schema_mode"] = "builtin_required_keys"
	if len(schema.RequiredKeys) > 0 {
		metadata["artifact_metadata_schema_required_keys"] = strings.Join(schema.RequiredKeys, ",")
	} else {
		delete(metadata, "artifact_metadata_schema_required_keys")
	}
	missingKeys := []string{}
	for _, key := range schema.RequiredKeys {
		if strings.TrimSpace(metadata[key]) == "" {
			missingKeys = append(missingKeys, key)
		}
	}
	if len(missingKeys) > 0 {
		metadata["artifact_metadata_schema_status"] = "warning"
		metadata["artifact_metadata_schema_missing_keys"] = strings.Join(missingKeys, ",")
		return
	}
	metadata["artifact_metadata_schema_status"] = "passed"
	delete(metadata, "artifact_metadata_schema_missing_keys")
}

func modelRunEvaluationArtifactMetadataSchemaForKind(artifactKind string) modelRunEvaluationArtifactMetadataSchema {
	switch normalizeID(artifactKind) {
	case ModelRunEvaluationArtifactKindSkillVersionEvaluation:
		return modelRunEvaluationArtifactMetadataSchema{
			ID: "ontology.skill_version_evaluation.v1",
			RequiredKeys: []string{
				"evaluation_ref", "candidate_version", "baseline_version",
				"candidate_definition_hash", "baseline_definition_hash",
				"dataset_ref", "policy_version", "evaluator_ref",
				"baseline_score", "candidate_score", "held_out_passed",
				"safety_passed", "regression_count",
			},
		}
	case "evaluation_report":
		return modelRunEvaluationArtifactMetadataSchema{
			ID:           "ontology.evaluation_report.v1",
			RequiredKeys: []string{"dataset_ref", "policy_version"},
		}
	default:
		return modelRunEvaluationArtifactMetadataSchema{ID: "ontology.artifact.generic.v1"}
	}
}

func validateEvaluationArtifactURI(uri string) error {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("%w: model run evaluation artifact_uri must use s3, gs, azblob, or file scheme", ErrInvalidDefinition)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "s3", "gs", "azblob":
		if parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
			return fmt.Errorf("%w: model run evaluation artifact_uri must include bucket/container and object path", ErrInvalidDefinition)
		}
	case "file":
		if parsed.Path == "" || strings.TrimSpace(parsed.Host) != "" {
			return fmt.Errorf("%w: model run evaluation artifact_uri file references must use an absolute local path", ErrInvalidDefinition)
		}
	default:
		return fmt.Errorf("%w: model run evaluation artifact_uri scheme %q is not allowed", ErrInvalidDefinition, parsed.Scheme)
	}
	return nil
}

func modelRunEvaluationArtifactID(tenantID string, engine string, runID string, artifactKind string, artifactURI string) string {
	return "ontology_model_run_evaluation_artifact:" + hashMappingRule(map[string]any{
		"tenant_id":     strings.TrimSpace(tenantID),
		"engine":        normalizeID(engine),
		"run_id":        strings.TrimSpace(runID),
		"artifact_kind": normalizeID(artifactKind),
		"artifact_uri":  strings.TrimSpace(artifactURI),
	})
}

func modelRunEvaluationArtifactMatches(artifact ModelRunEvaluationArtifact, query ModelRunEvaluationArtifactQuery) bool {
	return (strings.TrimSpace(query.TenantID) == "" || artifact.TenantID == strings.TrimSpace(query.TenantID)) &&
		(normalizeID(query.Engine) == "" || artifact.Engine == normalizeID(query.Engine)) &&
		(strings.TrimSpace(query.RunID) == "" || artifact.RunID == strings.TrimSpace(query.RunID)) &&
		(normalizeID(query.ArtifactKind) == "" || artifact.ArtifactKind == normalizeID(query.ArtifactKind))
}

func modelRunEvaluationArtifactWhere(query ModelRunEvaluationArtifactQuery) (string, []any) {
	clauses := []string{}
	args := []any{}
	if tenantID := strings.TrimSpace(query.TenantID); tenantID != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, tenantID)
	}
	if engine := normalizeID(query.Engine); engine != "" {
		clauses = append(clauses, "engine = ?")
		args = append(args, engine)
	}
	if runID := strings.TrimSpace(query.RunID); runID != "" {
		clauses = append(clauses, "run_id = ?")
		args = append(args, runID)
	}
	if artifactKind := normalizeID(query.ArtifactKind); artifactKind != "" {
		clauses = append(clauses, "artifact_kind = ?")
		args = append(args, artifactKind)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func modelRunEvaluationArtifactPage(artifacts []ModelRunEvaluationArtifact, query ModelRunEvaluationArtifactQuery) ModelRunEvaluationArtifactPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(artifacts)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]ModelRunEvaluationArtifact(nil), artifacts[offset:end]...)
	return ModelRunEvaluationArtifactPage{Artifacts: items, Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total}}
}

func scanModelRunEvaluationArtifact(row rowScanner) (ModelRunEvaluationArtifact, error) {
	var artifact ModelRunEvaluationArtifact
	var metadataJSON string
	var recordedAt, createdAt, updatedAt int64
	if err := row.Scan(&artifact.ID, &artifact.TenantID, &artifact.Engine, &artifact.RunID, &artifact.ArtifactKind, &artifact.ArtifactURI, &artifact.ArtifactHash, &artifact.Summary, &metadataJSON, &recordedAt, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationArtifact{}, err
	}
	mustDecodeJSON(metadataJSON, &artifact.Metadata)
	artifact.Metadata = cloneStringMetadata(artifact.Metadata)
	artifact.RecordedAt = unixNanoToTime(recordedAt)
	artifact.CreatedAt = unixNanoToTime(createdAt)
	artifact.UpdatedAt = unixNanoToTime(updatedAt)
	return artifact, nil
}

func cloneModelRunEvaluationArtifact(artifact ModelRunEvaluationArtifact) ModelRunEvaluationArtifact {
	artifact.Metadata = cloneStringMetadata(artifact.Metadata)
	return artifact
}

func cloneStringMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
