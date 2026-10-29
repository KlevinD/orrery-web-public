package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/peopleingest"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/secret"
)

const (
	peopleIngestionPath            = "/api/v1/people/events"
	maximumPeopleDeliveryBodyBytes = 8 << 20
)

var peopleContextGraphEventTypes = map[string]struct{}{
	"people.subject.v2.changed":                {},
	"people.org_unit.v2.changed":               {},
	"people.job_profile.v2.changed":            {},
	"people.position.v2.changed":               {},
	"people.assignment.v2.changed":             {},
	"people.reporting_relationship.v2.changed": {},
	"people.responsibility.v2.changed":         {},
	"people.responsibility_binding.v2.changed": {},
}

type peopleBatchIngestor interface {
	Ingest(context.Context, peopleingest.DeliveryBatch, string) (peopleingest.DeliveryAcknowledgement, error)
}

type peopleEmploymentProfileStore interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	SaveProfile(context.Context, agentcore.AgentProfile) error
}

type peopleIngestionHTTPHandler struct {
	expectedAuthorization string
	ingestor              peopleBatchIngestor
	next                  http.Handler
}

type productionPeopleEventConsumer struct {
	events      productionOntologyEventLog
	projections *ontology.MappingRuleProjectionRunner
	profiles    peopleEmploymentProfileStore
}

func newProductionPeopleIngestionHandler(
	ctx context.Context,
	rt *platform.Runtime,
	core *db.Store,
	profiles peopleEmploymentProfileStore,
	projections *ontology.MappingRuleProjectionRunner,
	next http.Handler,
) (http.Handler, error) {
	if rt == nil || core == nil || profiles == nil || projections == nil || next == nil {
		return nil, fmt.Errorf("production People ingestion dependencies are incomplete")
	}
	if rt.Secrets == nil {
		return next, nil
	}
	secretValue, err := rt.Secrets.Get(ctx, "people_ingestion_token")
	if secret.IsNotFound(err) {
		return next, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve People ingestion token: %w", err)
	}
	token := strings.TrimSpace(secretValue.Plaintext())
	if len(token) < 32 {
		return nil, fmt.Errorf("People ingestion token must contain at least 32 bytes")
	}
	service, err := peopleingest.NewService(peopleingest.ServiceOptions{
		Store: peopleingest.NewStore(core),
		Consumer: productionPeopleEventConsumer{
			events: newProductionOntologyEventLog(core), projections: projections, profiles: profiles,
		},
		Now: rt.Clock.Now,
	})
	if err != nil {
		return nil, err
	}
	return &peopleIngestionHTTPHandler{
		expectedAuthorization: "Bearer " + token,
		ingestor:              service,
		next:                  next,
	}, nil
}

func (handler *peopleIngestionHTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request == nil || request.URL.Path != peopleIngestionPath {
		handler.next.ServeHTTP(response, request)
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePeopleIngestionProblem(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if handler.ingestor == nil || handler.expectedAuthorization == "" {
		writePeopleIngestionProblem(response, http.StatusServiceUnavailable, "ingestion_unavailable")
		return
	}
	if subtle.ConstantTimeCompare(
		[]byte(request.Header.Get("Authorization")),
		[]byte(handler.expectedAuthorization),
	) != 1 {
		writePeopleIngestionProblem(response, http.StatusUnauthorized, "invalid_service_credential")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writePeopleIngestionProblem(response, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maximumPeopleDeliveryBodyBytes))
	if err != nil {
		var maximumBytesError *http.MaxBytesError
		if errors.As(err, &maximumBytesError) {
			writePeopleIngestionProblem(response, http.StatusRequestEntityTooLarge, "delivery_too_large")
			return
		}
		writePeopleIngestionProblem(response, http.StatusBadRequest, "invalid_delivery_body")
		return
	}
	digest := sha256.Sum256(body)
	if !validPeopleDeliveryDigest(request.Header.Get("Digest"), digest) {
		writePeopleIngestionProblem(response, http.StatusBadRequest, "digest_mismatch")
		return
	}
	batch, err := decodePeopleDeliveryBatch(body)
	if err != nil {
		writePeopleIngestionProblem(response, http.StatusBadRequest, "invalid_delivery_body")
		return
	}
	if request.Header.Get("Idempotency-Key") != batch.BatchID {
		writePeopleIngestionProblem(response, http.StatusBadRequest, "idempotency_key_mismatch")
		return
	}
	acknowledgement, err := handler.ingestor.Ingest(request.Context(), batch, hex.EncodeToString(digest[:]))
	if err != nil {
		switch {
		case errors.Is(err, peopleingest.ErrInvalidBatch):
			writePeopleIngestionProblem(response, http.StatusBadRequest, "invalid_delivery_batch")
		case errors.Is(err, peopleingest.ErrConflict):
			writePeopleIngestionProblem(response, http.StatusConflict, "delivery_conflict")
		case errors.Is(err, peopleingest.ErrBusy):
			response.Header().Set("Retry-After", "1")
			writePeopleIngestionProblem(response, http.StatusTooManyRequests, "delivery_in_progress")
		default:
			slog.ErrorContext(request.Context(), "People delivery processing failed",
				"batch_id", batch.BatchID,
				"tenant_id", batch.TenantID,
				"event_count", len(batch.Events),
				"error", err,
			)
			response.Header().Set("Retry-After", "1")
			writePeopleIngestionProblem(response, http.StatusServiceUnavailable, "delivery_processing_failed")
		}
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(acknowledgement)
}

func (consumer productionPeopleEventConsumer) ConsumePeopleEvent(ctx context.Context, source peopleingest.DeliveryEvent) error {
	if consumer.events.store == nil || consumer.projections == nil {
		return errors.New("People ontology consumer is unavailable")
	}
	payload, err := peopleingest.DecodePayload(source.Payload)
	if err != nil {
		return err
	}
	event := graphprotocol.DomainEvent{
		ID:            peopleOntologyEventID(source.TenantID, source.EventID),
		SourceEventID: source.EventID,
		TenantID:      source.TenantID,
		Type:          source.EventType,
		SchemaVersion: strconv.Itoa(source.SchemaVersion),
		StreamID:      source.AggregateType + ":" + source.AggregateID,
		Version:       source.AggregateRevision,
		Operation:     source.Operation,
		CorrelationID: source.CorrelationID,
		CausationID:   source.CausationID,
		OccurredAt:    source.OccurredAt.UTC(),
		Payload:       payload,
		PublishedBy:   graphprotocol.ActorRef{Kind: "service", ID: "orrery-people"},
		SourceAccessRef: trustedOntologySourceRef(
			"organization", source.TenantID, source.TenantID,
		),
	}
	if err := ensurePeopleOntologyEvent(ctx, consumer.events, event); err != nil {
		return err
	}
	if source.EventType == "people.employment.v2.changed" {
		if err := consumer.applyEmploymentProfileFacts(ctx, source.TenantID, payload); err != nil {
			return err
		}
	}
	if _, mapped := peopleContextGraphEventTypes[source.EventType]; !mapped {
		return nil
	}
	projectionEvents, err := peopleProjectionEvents(event)
	if err != nil {
		return err
	}
	for _, projectionEvent := range projectionEvents {
		if _, err := consumer.projections.ApplyEvent(ctx, projectionEvent); err != nil {
			return err
		}
	}
	return nil
}

func peopleProjectionEvents(event graphprotocol.DomainEvent) ([]graphprotocol.DomainEvent, error) {
	if event.Type != "people.assignment.v2.changed" || !strings.EqualFold(strings.TrimSpace(event.Operation), "transferred") {
		return []graphprotocol.DomainEvent{event}, nil
	}
	ended, endedOK := event.Payload["ended"].(map[string]any)
	replacement, replacementOK := event.Payload["replacement"].(map[string]any)
	if !endedOK || !replacementOK {
		return nil, errors.New("People transferred assignment payload requires ended and replacement assignments")
	}
	endedEvent, err := normalizedPeopleAssignmentProjectionEvent(event, ended, "ended")
	if err != nil {
		return nil, fmt.Errorf("normalize ended People assignment: %w", err)
	}
	replacementEvent, err := normalizedPeopleAssignmentProjectionEvent(event, replacement, "created")
	if err != nil {
		return nil, fmt.Errorf("normalize replacement People assignment: %w", err)
	}
	return []graphprotocol.DomainEvent{endedEvent, replacementEvent}, nil
}

func normalizedPeopleAssignmentProjectionEvent(
	event graphprotocol.DomainEvent,
	payload map[string]any,
	operation string,
) (graphprotocol.DomainEvent, error) {
	normalized := map[string]any{}
	required := map[string]string{
		"id":              "ID",
		"subject_id":      "SubjectID",
		"org_unit_id":     "OrgUnitID",
		"position_id":     "PositionID",
		"assignment_kind": "Kind",
		"allocation_bps":  "Allocation",
		"revision":        "Revision",
	}
	for target, source := range required {
		value, ok := payload[source]
		if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return graphprotocol.DomainEvent{}, fmt.Errorf("%s is required", source)
		}
		normalized[target] = value
	}
	for target, source := range map[string]string{
		"predecessor_assignment_id": "PredecessorID",
		"transition_kind":           "TransitionKind",
		"tenant_id":                 "TenantID",
	} {
		if value, ok := payload[source]; ok && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			normalized[target] = value
		}
	}
	effective, ok := payload["Effective"].(map[string]any)
	if !ok {
		return graphprotocol.DomainEvent{}, errors.New("Effective is required")
	}
	validFrom, ok := effective["Start"]
	if !ok || validFrom == nil || strings.TrimSpace(fmt.Sprint(validFrom)) == "" {
		return graphprotocol.DomainEvent{}, errors.New("Effective.Start is required")
	}
	normalized["valid_from"] = validFrom
	if validTo, ok := effective["End"]; ok && validTo != nil && strings.TrimSpace(fmt.Sprint(validTo)) != "" {
		normalized["valid_to"] = validTo
	}

	revision, err := peopleAssignmentRevision(normalized["revision"])
	if err != nil {
		return graphprotocol.DomainEvent{}, err
	}
	id := strings.TrimSpace(fmt.Sprint(normalized["id"]))
	event.StreamID = "assignment:" + id
	event.Version = revision
	event.Operation = operation
	event.Payload = normalized
	return event, nil
}

func peopleAssignmentRevision(value any) (int64, error) {
	switch typed := value.(type) {
	case json.Number:
		revision, err := typed.Int64()
		if err == nil && revision > 0 {
			return revision, nil
		}
	case int64:
		if typed > 0 {
			return typed, nil
		}
	case int:
		if typed > 0 {
			return int64(typed), nil
		}
	case float64:
		if typed > 0 && typed == float64(int64(typed)) {
			return int64(typed), nil
		}
	case string:
		revision, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err == nil && revision > 0 {
			return revision, nil
		}
	}
	return 0, errors.New("Revision must be a positive integer")
}

func (consumer productionPeopleEventConsumer) applyEmploymentProfileFacts(ctx context.Context, tenantID string, payload map[string]any) error {
	if consumer.profiles == nil {
		return errors.New("People employment profile store is unavailable")
	}
	subjectID, _ := payload["subject_id"].(string)
	workerNumber, _ := payload["worker_number"].(string)
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	workerNumber = strings.TrimSpace(workerNumber)
	if tenantID == "" || subjectID == "" || workerNumber == "" {
		return errors.New("People employment payload requires subject_id and worker_number")
	}

	profile, err := consumer.profiles.GetProfile(ctx, agentcore.AgentProfileID(workerNumber))
	if err == nil {
		if !samePeopleProfileSource(profile.PersonSource, tenantID, subjectID) {
			return fmt.Errorf("%w: worker_number %q belongs to a different Orrery profile source", peopleingest.ErrConflict, workerNumber)
		}
		return savePeopleWorkerNumber(ctx, consumer.profiles, profile, workerNumber)
	}
	if !errors.Is(err, agentcore.ErrNotFound) {
		return err
	}

	page, err := consumer.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		AgentKind: agentcore.AgentKindHuman, IncludeHumans: true, Placement: agentcore.TalentPlacementFilterAll,
	})
	if err != nil {
		return err
	}
	var matched *agentcore.AgentProfile
	for _, entry := range page.Entries {
		candidate, err := consumer.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return err
		}
		if !samePeopleProfileSource(candidate.PersonSource, tenantID, subjectID) {
			continue
		}
		if matched != nil {
			return fmt.Errorf("%w: People subject %q maps to multiple Orrery profiles", peopleingest.ErrConflict, subjectID)
		}
		candidateCopy := candidate
		matched = &candidateCopy
	}
	if matched == nil {
		return nil
	}
	return savePeopleWorkerNumber(ctx, consumer.profiles, *matched, workerNumber)
}

func samePeopleProfileSource(source agentcore.PersonSourceFacts, tenantID, subjectID string) bool {
	return strings.EqualFold(strings.TrimSpace(source.Source), "people") &&
		strings.TrimSpace(source.SourceTenantID) == tenantID &&
		strings.TrimSpace(source.SourceRecordID) == subjectID
}

func savePeopleWorkerNumber(ctx context.Context, profiles peopleEmploymentProfileStore, profile agentcore.AgentProfile, workerNumber string) error {
	if profile.ID.String() != workerNumber {
		return fmt.Errorf("%w: People worker_number %q does not match Orrery profile id %q", peopleingest.ErrConflict, workerNumber, profile.ID)
	}
	if strings.TrimSpace(profile.PersonSource.EmployeeNumber) == workerNumber {
		return nil
	}
	profile.PersonSource.EmployeeNumber = workerNumber
	return profiles.SaveProfile(ctx, profile)
}

func ensurePeopleOntologyEvent(ctx context.Context, eventLog productionOntologyEventLog, event graphprotocol.DomainEvent) error {
	existing, err := eventLog.Get(ctx, event.ID)
	if err == nil {
		if !samePeopleOntologyEvent(existing, event) {
			return fmt.Errorf("%w: ontology event_id %q already contains different data", peopleingest.ErrConflict, event.ID)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := eventLog.Record(ctx, event); err != nil {
		return err
	}
	existing, err = eventLog.Get(ctx, event.ID)
	if err != nil {
		return err
	}
	if !samePeopleOntologyEvent(existing, event) {
		return fmt.Errorf("%w: ontology event_id %q already contains different data", peopleingest.ErrConflict, event.ID)
	}
	return nil
}

func samePeopleOntologyEvent(left, right graphprotocol.DomainEvent) bool {
	if left.ID != right.ID || left.SourceEventID != right.SourceEventID || left.TenantID != right.TenantID || left.Type != right.Type ||
		left.SchemaVersion != right.SchemaVersion || left.StreamID != right.StreamID || left.Version != right.Version ||
		left.Operation != right.Operation || left.CorrelationID != right.CorrelationID || left.CausationID != right.CausationID ||
		!left.OccurredAt.Equal(right.OccurredAt) || left.PublishedBy != right.PublishedBy || left.SourceAccessRef != right.SourceAccessRef {
		return false
	}
	leftPayload, leftErr := json.Marshal(left.Payload)
	rightPayload, rightErr := json.Marshal(right.Payload)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftPayload, rightPayload)
}

func peopleOntologyEventID(tenantID, sourceEventID string) string {
	return "people.v2:" + strconv.Itoa(len(tenantID)) + ":" + tenantID + ":" + sourceEventID
}

func validPeopleDeliveryDigest(header string, want [sha256.Size]byte) bool {
	const prefix = "SHA-256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil || len(decoded) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(decoded, want[:]) == 1
}

func decodePeopleDeliveryBatch(body []byte) (peopleingest.DeliveryBatch, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var batch peopleingest.DeliveryBatch
	if err := decoder.Decode(&batch); err != nil {
		return peopleingest.DeliveryBatch{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return peopleingest.DeliveryBatch{}, errors.New("delivery body must contain one JSON object")
	}
	return batch, nil
}

func writePeopleIngestionProblem(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/problem+json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
		"code":   code,
	})
}
