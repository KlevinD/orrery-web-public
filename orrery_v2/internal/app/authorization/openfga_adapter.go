package authorization

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"orrery_v2/internal/app/identity"
)

const (
	DecisionSourceOpenFGA    = "openfga_relation"
	openFGAMaxChecksPerBatch = 50
	openFGAMaxTuplesPerWrite = 100
	openFGAEncodedIDPrefix   = "_orrery_b64_"
)

type OpenFGAConfig struct {
	APIURL                string
	StoreID               string
	AuthorizationModelID  string
	Token                 string
	HTTPClient            *http.Client
	IgnoreDuplicateWrites bool
}

type OpenFGAAdapter struct {
	apiURL                string
	storeID               string
	authorizationModelID  string
	token                 string
	httpClient            *http.Client
	ignoreDuplicateWrites bool
	scopes                *ScopeAdapter
}

func NewOpenFGAAdapter(cfg OpenFGAConfig) (*OpenFGAAdapter, error) {
	apiURL := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	storeID := strings.TrimSpace(cfg.StoreID)
	modelID := strings.TrimSpace(cfg.AuthorizationModelID)
	if apiURL == "" {
		return nil, fmt.Errorf("%w: openfga api_url is required", ErrInvalidRequest)
	}
	if storeID == "" {
		return nil, fmt.Errorf("%w: openfga store_id is required", ErrInvalidRequest)
	}
	if modelID == "" {
		return nil, fmt.Errorf("%w: openfga authorization_model_id is required", ErrInvalidRequest)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OpenFGAAdapter{
		apiURL:                apiURL,
		storeID:               storeID,
		authorizationModelID:  modelID,
		token:                 strings.TrimSpace(cfg.Token),
		httpClient:            client,
		ignoreDuplicateWrites: cfg.IgnoreDuplicateWrites,
		scopes:                NewScopeAdapter(nil),
	}, nil
}

func (a *OpenFGAAdapter) Check(ctx context.Context, req CheckRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if a == nil || a.httpClient == nil {
		return Decision{}, ErrServiceDependency
	}
	req = normalizeCheckRequest(req)
	if err := validateScopeCheck(req); err != nil {
		return Decision{}, err
	}
	scopeDecision, err := a.scopes.Check(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	if !scopeDecision.Allowed {
		return scopeDecision, nil
	}
	mapping, ok := relationMappingForCheck(req.Action, req.Object.Type)
	if !ok {
		return Decision{Allowed: false, Source: DecisionSourceOpenFGA, Reason: "openfga relation mapping is missing for action " + req.Action}, nil
	}
	if !openFGAModelSupportsObjectType(req.Object.Type) {
		return Decision{Allowed: false, Source: DecisionSourceOpenFGA, Reason: "openfga authorization model does not define object type " + req.Object.Type}, nil
	}
	subject := subjectRefFromPrincipalForObject(req.Principal, req.Object.Type)
	if subject.Kind == "" || subject.ID == "" || req.Object.Type == "" || req.Object.ID == "" {
		return Decision{}, fmt.Errorf("%w: principal subject, object type and object id are required for openfga check", ErrInvalidRequest)
	}
	var body openFGACheckResponse
	err = a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/check", openFGACheckRequest{
		AuthorizationModelID: a.authorizationModelID,
		TupleKey: openFGATupleKey{
			User:     openFGASubjectRef(subject.Kind, subject.ID, req.TenantID),
			Relation: mapping.Relation,
			Object:   openFGAObjectRef(req.Object.Type, req.Object.ID, req.TenantID),
		},
	}, &body)
	if err != nil {
		return Decision{}, err
	}
	if body.Allowed {
		return Decision{Allowed: true, Source: DecisionSourceOpenFGA, Reason: "openfga relation " + mapping.Relation + " allows action " + req.Action}, nil
	}
	return Decision{Allowed: false, Source: DecisionSourceOpenFGA, Reason: "openfga missing relation " + mapping.Relation + " for action " + req.Action}, nil
}

func openFGAModelSupportsObjectType(objectType string) bool {
	switch normalizeKind(objectType) {
	case "platform", "organization", "department", "role", "mission", "discussion", "project", "resource", "development_project", "scm_connection", "todo":
		return true
	default:
		return false
	}
}

func (a *OpenFGAAdapter) BatchCheck(ctx context.Context, reqs []CheckRequest) ([]Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.httpClient == nil {
		return nil, ErrServiceDependency
	}
	out := make([]Decision, len(reqs))
	pending := make([]openFGAPendingBatchCheck, 0, len(reqs))
	for i, raw := range reqs {
		req := normalizeCheckRequest(raw)
		if err := validateScopeCheck(req); err != nil {
			return nil, err
		}
		scopeDecision, err := a.scopes.Check(ctx, req)
		if err != nil {
			return nil, err
		}
		if !scopeDecision.Allowed {
			out[i] = scopeDecision
			continue
		}
		mapping, ok := relationMappingForCheck(req.Action, req.Object.Type)
		if !ok {
			out[i] = Decision{Allowed: false, Source: DecisionSourceOpenFGA, Reason: "openfga relation mapping is missing for action " + req.Action}
			continue
		}
		if !openFGAModelSupportsObjectType(req.Object.Type) {
			out[i] = Decision{Allowed: false, Source: DecisionSourceOpenFGA, Reason: "openfga authorization model does not define object type " + req.Object.Type}
			continue
		}
		subject := subjectRefFromPrincipalForObject(req.Principal, req.Object.Type)
		if subject.Kind == "" || subject.ID == "" || req.Object.Type == "" || req.Object.ID == "" {
			return nil, fmt.Errorf("%w: principal subject, object type and object id are required for openfga check", ErrInvalidRequest)
		}
		pending = append(pending, openFGAPendingBatchCheck{
			index:   i,
			request: req,
			check: openFGABatchCheck{
				TupleKey: openFGATupleKey{
					User:     openFGASubjectRef(subject.Kind, subject.ID, req.TenantID),
					Relation: mapping.Relation,
					Object:   openFGAObjectRef(req.Object.Type, req.Object.ID, req.TenantID),
				},
				CorrelationID: strconv.Itoa(i),
			},
		})
	}
	batchCount := (len(pending) + openFGAMaxChecksPerBatch - 1) / openFGAMaxChecksPerBatch
	for start := 0; start < len(pending); start += openFGAMaxChecksPerBatch {
		end := min(start+openFGAMaxChecksPerBatch, len(pending))
		checks := make([]openFGABatchCheck, 0, end-start)
		for _, item := range pending[start:end] {
			checks = append(checks, item.check)
		}
		var body openFGABatchCheckResponse
		if err := a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/batch-check", openFGABatchCheckRequest{
			AuthorizationModelID: a.authorizationModelID,
			Checks:               checks,
		}, &body); err != nil {
			return nil, fmt.Errorf("openfga batch check %d/%d: %w", start/openFGAMaxChecksPerBatch+1, batchCount, err)
		}
		for _, item := range pending[start:end] {
			result, ok := body.Result[item.check.CorrelationID]
			if !ok {
				return nil, fmt.Errorf("%w: openfga batch check result %q is missing", ErrServiceDependency, item.check.CorrelationID)
			}
			if len(result.Error) > 0 && string(result.Error) != "null" {
				return nil, fmt.Errorf("%w: openfga batch check result %q failed: %s", ErrServiceDependency, item.check.CorrelationID, strings.TrimSpace(string(result.Error)))
			}
			relation := item.check.TupleKey.Relation
			if result.Allowed {
				out[item.index] = Decision{Allowed: true, Source: DecisionSourceOpenFGA, Reason: "openfga relation " + relation + " allows action " + item.request.Action}
			} else {
				out[item.index] = Decision{Allowed: false, Source: DecisionSourceOpenFGA, Reason: "openfga missing relation " + relation + " for action " + item.request.Action}
			}
		}
	}
	return out, nil
}

func (a *OpenFGAAdapter) ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.httpClient == nil {
		return nil, ErrServiceDependency
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return nil, fmt.Errorf("%w: authenticated principal is required", ErrInvalidRequest)
	}
	relation = normalizeRelation(relation)
	objectType = normalizeKind(objectType)
	tenantID = strings.TrimSpace(tenantID)
	subject := subjectRefFromPrincipalForObject(principal, objectType)
	if subject.Kind == "" || subject.ID == "" || relation == "" || objectType == "" {
		return nil, fmt.Errorf("%w: subject, relation and object type are required for openfga list objects", ErrInvalidRequest)
	}
	var body openFGAListObjectsResponse
	err := a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/list-objects", openFGAListObjectsRequest{
		AuthorizationModelID: a.authorizationModelID,
		User:                 openFGASubjectRef(subject.Kind, subject.ID, tenantID),
		Relation:             relation,
		Type:                 objectType,
	}, &body)
	if err != nil {
		return nil, err
	}
	objects := make([]identity.ObjectRef, 0, len(body.Objects))
	for _, raw := range body.Objects {
		objectTypeOut, objectID := splitOpenFGARef(raw)
		if objectTypeOut == "" || objectID == "" {
			continue
		}
		unscopedID, ok := unscopedOpenFGAObjectID(objectID, tenantID)
		if !ok {
			continue
		}
		objects = append(objects, identity.ObjectRef{Type: objectTypeOut, ID: unscopedID, TenantID: tenantID})
	}
	return objects, nil
}

func (a *OpenFGAAdapter) CheckRelation(ctx context.Context, tuple RelationTuple) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a == nil || a.httpClient == nil {
		return false, ErrServiceDependency
	}
	keys, err := openFGATupleKeysFromRelations([]RelationTuple{tuple})
	if err != nil {
		return false, err
	}
	if len(keys) == 0 {
		return false, fmt.Errorf("%w: relation tuple is required for openfga check relation", ErrInvalidRequest)
	}
	var body openFGACheckResponse
	if err := a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/check", openFGACheckRequest{
		AuthorizationModelID: a.authorizationModelID,
		TupleKey:             keys[0],
	}, &body); err != nil {
		return false, err
	}
	return body.Allowed, nil
}

func (a *OpenFGAAdapter) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.httpClient == nil {
		return ErrServiceDependency
	}
	keys := make([]openFGATupleKey, 0, len(tuples))
	seen := map[openFGATupleKey]struct{}{}
	for _, tuple := range tuples {
		tuple = normalizeRelationTuple(tuple)
		if tuple.Subject.Kind == "" || tuple.Subject.ID == "" || tuple.Relation == "" || tuple.Object.Type == "" || tuple.Object.ID == "" {
			return fmt.Errorf("%w: subject, relation and object are required for openfga write", ErrInvalidRequest)
		}
		key := openFGATupleKey{
			User:     openFGASubjectRef(tuple.Subject.Kind, tuple.Subject.ID, tuple.TenantID),
			Relation: tuple.Relation,
			Object:   openFGAObjectRef(tuple.Object.Type, tuple.Object.ID, tuple.TenantID),
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if !a.ignoreDuplicateWrites && isIdempotentRoleOrganizationTuple(tuple) {
			exists, err := a.CheckRelation(ctx, tuple)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	batchCount := (len(keys) + openFGAMaxTuplesPerWrite - 1) / openFGAMaxTuplesPerWrite
	for batchIndex, start := 0, 0; start < len(keys); batchIndex, start = batchIndex+1, start+openFGAMaxTuplesPerWrite {
		end := min(start+openFGAMaxTuplesPerWrite, len(keys))
		writes := openFGAWriteTupleKeys{TupleKeys: keys[start:end]}
		if a.ignoreDuplicateWrites {
			writes.OnDuplicate = "ignore"
		}
		if err := a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/write", openFGAWriteRequest{
			AuthorizationModelID: a.authorizationModelID,
			Writes:               &writes,
		}, nil); err != nil {
			return fmt.Errorf("openfga write batch %d/%d: %w", batchIndex+1, batchCount, err)
		}
	}
	return nil
}

func isIdempotentRoleOrganizationTuple(tuple RelationTuple) bool {
	tuple = normalizeRelationTuple(tuple)
	return tuple.Subject.Kind == "organization" && tuple.Relation == "org" && tuple.Object.Type == "role"
}

func (a *OpenFGAAdapter) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.httpClient == nil {
		return ErrServiceDependency
	}
	keys, err := openFGATupleKeysFromRelations(tuples)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	batchCount := (len(keys) + openFGAMaxTuplesPerWrite - 1) / openFGAMaxTuplesPerWrite
	for batchIndex, start := 0, 0; start < len(keys); batchIndex, start = batchIndex+1, start+openFGAMaxTuplesPerWrite {
		end := min(start+openFGAMaxTuplesPerWrite, len(keys))
		deletes := &openFGADeleteTupleKeys{TupleKeys: keys[start:end]}
		if a.ignoreDuplicateWrites {
			deletes.OnMissing = "ignore"
		}
		if err := a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/write", openFGAWriteRequest{
			AuthorizationModelID: a.authorizationModelID,
			Deletes:              deletes,
		}, nil); err != nil {
			return fmt.Errorf("openfga delete batch %d/%d: %w", batchIndex+1, batchCount, err)
		}
	}
	return nil
}

func (a *OpenFGAAdapter) ListRelations(ctx context.Context) ([]RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.httpClient == nil {
		return nil, ErrServiceDependency
	}
	continuationToken := ""
	out := []RelationTuple{}
	for {
		var page openFGAReadResponse
		if err := a.postJSON(ctx, "/stores/"+url.PathEscape(a.storeID)+"/read", openFGAReadRequest{
			PageSize:          100,
			ContinuationToken: continuationToken,
		}, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Tuples {
			subjectKind, subjectID := splitOpenFGARef(item.Key.User)
			objectType, objectID := splitOpenFGARef(item.Key.Object)
			if subjectKind == "" || subjectID == "" || objectType == "" || objectID == "" || strings.TrimSpace(item.Key.Relation) == "" {
				continue
			}
			tenantID, unscopedObjectID, ok := splitScopedOpenFGAObjectID(objectID)
			if !ok {
				continue
			}
			unscopedSubjectID, ok := unscopedOpenFGASubjectID(subjectKind, subjectID, tenantID)
			if !ok {
				continue
			}
			out = append(out, normalizeRelationTuple(RelationTuple{
				TenantID: tenantID,
				Subject:  SubjectRef{Kind: subjectKind, ID: unscopedSubjectID},
				Relation: item.Key.Relation,
				Object:   identity.ObjectRef{Type: objectType, ID: unscopedObjectID, TenantID: tenantID},
			}))
		}
		continuationToken = strings.TrimSpace(page.ContinuationToken)
		if continuationToken == "" {
			break
		}
	}
	return out, nil
}

func openFGATupleKeysFromRelations(tuples []RelationTuple) ([]openFGATupleKey, error) {
	keys := make([]openFGATupleKey, 0, len(tuples))
	for _, tuple := range tuples {
		tuple = normalizeRelationTuple(tuple)
		if tuple.Subject.Kind == "" || tuple.Subject.ID == "" || tuple.Relation == "" || tuple.Object.Type == "" || tuple.Object.ID == "" {
			return nil, fmt.Errorf("%w: subject, relation and object are required for openfga tuple key", ErrInvalidRequest)
		}
		keys = append(keys, openFGATupleKey{
			User:     openFGASubjectRef(tuple.Subject.Kind, tuple.Subject.ID, tuple.TenantID),
			Relation: tuple.Relation,
			Object:   openFGAObjectRef(tuple.Object.Type, tuple.Object.ID, tuple.TenantID),
		})
	}
	return keys, nil
}

func (a *OpenFGAAdapter) postJSON(ctx context.Context, path string, payload any, out any) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return fmt.Errorf("%w: encode openfga request: %v", ErrInvalidRequest, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL+path, &body)
	if err != nil {
		return fmt.Errorf("%w: build openfga request: %v", ErrInvalidRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: call openfga: %v", ErrServiceDependency, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%w: openfga returned %d: %s", ErrServiceDependency, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decode openfga response: %v", ErrServiceDependency, err)
	}
	return nil
}

func openFGARef(kind string, id string) string {
	return normalizeKind(kind) + ":" + encodeOpenFGAID(strings.TrimSpace(id))
}

func encodeOpenFGAID(id string) string {
	if !strings.HasPrefix(id, openFGAEncodedIDPrefix) && !strings.ContainsFunc(id, func(r rune) bool {
		return r == ':' || r == '#' || unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return id
	}
	return openFGAEncodedIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeOpenFGAID(id string) string {
	if !strings.HasPrefix(id, openFGAEncodedIDPrefix) {
		return id
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, openFGAEncodedIDPrefix))
	if err != nil {
		return id
	}
	return string(decoded)
}

func openFGASubjectRef(kind string, id string, tenantID string) string {
	kind = normalizeKind(kind)
	if kind == "user" || kind == "agent" {
		return openFGARef(kind, id)
	}
	return openFGARef(kind, scopedOpenFGAObjectID(id, tenantID))
}

func openFGAObjectRef(kind string, id string, tenantID string) string {
	return openFGARef(kind, scopedOpenFGAObjectID(id, tenantID))
}

func scopedOpenFGAObjectID(id string, tenantID string) string {
	id = strings.TrimSpace(id)
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	return tenantID + "/" + id
}

func unscopedOpenFGAObjectID(id string, tenantID string) (string, bool) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	prefix := tenantID + "/"
	if !strings.HasPrefix(id, prefix) {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(id, prefix))
	return value, value != ""
}

func splitScopedOpenFGAObjectID(id string) (string, string, bool) {
	id = strings.TrimSpace(id)
	tenantID, value, ok := strings.Cut(id, "/")
	tenantID = strings.TrimSpace(tenantID)
	value = strings.TrimSpace(value)
	if !ok || tenantID == "" || value == "" {
		return "", "", false
	}
	return tenantID, value, true
}

func unscopedOpenFGASubjectID(kind string, id string, tenantID string) (string, bool) {
	kind = normalizeKind(kind)
	id = strings.TrimSpace(id)
	if kind == "user" || kind == "agent" {
		return id, id != ""
	}
	return unscopedOpenFGAObjectID(id, tenantID)
}

func splitOpenFGARef(value string) (string, string) {
	kind, id, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return "", ""
	}
	return normalizeKind(kind), decodeOpenFGAID(strings.TrimSpace(id))
}

type openFGATupleKey struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

type openFGACheckRequest struct {
	AuthorizationModelID string          `json:"authorization_model_id"`
	TupleKey             openFGATupleKey `json:"tuple_key"`
}

type openFGACheckResponse struct {
	Allowed bool `json:"allowed"`
}

type openFGABatchCheck struct {
	TupleKey      openFGATupleKey `json:"tuple_key"`
	CorrelationID string          `json:"correlation_id"`
}

type openFGABatchCheckRequest struct {
	AuthorizationModelID string              `json:"authorization_model_id"`
	Checks               []openFGABatchCheck `json:"checks"`
}

type openFGABatchCheckResult struct {
	Allowed bool            `json:"allowed"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type openFGABatchCheckResponse struct {
	Result map[string]openFGABatchCheckResult `json:"result"`
}

type openFGAPendingBatchCheck struct {
	index   int
	request CheckRequest
	check   openFGABatchCheck
}

type openFGAListObjectsRequest struct {
	AuthorizationModelID string `json:"authorization_model_id"`
	User                 string `json:"user"`
	Relation             string `json:"relation"`
	Type                 string `json:"type"`
}

type openFGAListObjectsResponse struct {
	Objects []string `json:"objects"`
}

type openFGAWriteTupleKeys struct {
	TupleKeys   []openFGATupleKey `json:"tuple_keys"`
	OnDuplicate string            `json:"on_duplicate,omitempty"`
}

type openFGADeleteTupleKeys struct {
	TupleKeys []openFGATupleKey `json:"tuple_keys"`
	OnMissing string            `json:"on_missing,omitempty"`
}

type openFGAWriteRequest struct {
	AuthorizationModelID string                  `json:"authorization_model_id"`
	Writes               *openFGAWriteTupleKeys  `json:"writes,omitempty"`
	Deletes              *openFGADeleteTupleKeys `json:"deletes,omitempty"`
}

type openFGAReadRequest struct {
	PageSize          int    `json:"page_size,omitempty"`
	ContinuationToken string `json:"continuation_token,omitempty"`
}

type openFGAReadTuple struct {
	Key openFGATupleKey `json:"key"`
}

type openFGAReadResponse struct {
	Tuples            []openFGAReadTuple `json:"tuples"`
	ContinuationToken string             `json:"continuation_token"`
}

var _ Adapter = (*OpenFGAAdapter)(nil)
