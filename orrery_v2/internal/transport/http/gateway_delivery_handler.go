package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	gwbindings "orrery_v2/internal/gateway/binding"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

const maxGatewayDeliveryRequestBytes = 256 << 10

type gatewayDeliveryRequestDTO struct {
	HumanProfileID string   `json:"human_profile_id"`
	Channels       []string `json:"channels"`
	EventType      string   `json:"event_type"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	Format         string   `json:"format"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type gatewayDeliveriesDTO struct {
	Deliveries []gatewayDeliveryDTO `json:"deliveries"`
}

type gatewayDeliveryDTO struct {
	ID             string    `json:"id"`
	ChannelKey     string    `json:"channel_key"`
	HumanProfileID string    `json:"human_profile_id"`
	EventType      string    `json:"event_type"`
	Title          string    `json:"title,omitempty"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	LastError      string    `json:"last_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
}

func (r *Router) handleCreateGatewayDelivery(w http.ResponseWriter, req *http.Request) {
	if r.gatewayDeliveries == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_delivery_unavailable", Message: "Gateway delivery service is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewayDeliveryRequestBytes)
	}
	var body gatewayDeliveryRequestDTO
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	humanProfileID := strings.TrimSpace(body.HumanProfileID)
	if r.agentProfiles == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_delivery_target_validation_unavailable", Message: "Gateway target validation is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	profile, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(humanProfileID))
	if err != nil {
		if errors.Is(err, agentcore.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_delivery_target_not_found", Message: "Gateway delivery target was not found.", CorrelationID: correlationID(req)})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_delivery_target_validation_unavailable", Message: "Gateway target validation is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	if profile.AgentKind != agentcore.AgentKindHuman || !profile.Enabled {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_delivery_target_not_found", Message: "Gateway delivery target was not found.", CorrelationID: correlationID(req)})
		return
	}
	if strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)) != tenantID {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: "gateway_delivery_target_forbidden", Message: "Gateway delivery target is outside the tenant scope.", CorrelationID: correlationID(req)})
		return
	}
	idempotencyKey := strings.TrimSpace(body.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	}
	deliveries, err := r.gatewayDeliveries.Enqueue(req.Context(), gwoutbound.EnqueueCommand{
		TenantID: tenantID, HumanProfileID: humanProfileID, ChannelKeys: body.Channels, EventType: body.EventType,
		Title: body.Title, Body: body.Body, Format: body.Format, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "gateway_delivery_unavailable"
		message := "Gateway delivery service is temporarily unavailable."
		if errors.Is(err, gwoutbound.ErrInvalidInput) {
			status = http.StatusBadRequest
			code = "gateway_delivery_invalid"
			message = err.Error()
		} else if errors.Is(err, gwoutbound.ErrBindingMissing) || errors.Is(err, gwbindings.ErrNotFound) {
			status = http.StatusPreconditionFailed
			code = "gateway_delivery_binding_missing"
			message = err.Error()
		} else if errors.Is(err, gwoutbound.ErrIdempotencyConflict) {
			status = http.StatusConflict
			code = "gateway_delivery_idempotency_conflict"
			message = err.Error()
		}
		writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusAccepted, gatewayDeliveriesDTO{Deliveries: gatewayDeliveryDTOs(deliveries)})
}

func rejectTrailingGatewayJSON(decoder *json.Decoder) error {
	if decoder == nil {
		return errors.New("JSON decoder is required")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func (r *Router) handleGetGatewayDelivery(w http.ResponseWriter, req *http.Request, deliveryID string) {
	if r.gatewayDeliveries == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_delivery_unavailable", Message: "Gateway delivery service is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	delivery, err := r.gatewayDeliveries.Get(req.Context(), tenantID, strings.TrimSpace(deliveryID))
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "gateway_delivery_unavailable"
		message := "Gateway delivery service is temporarily unavailable."
		if errors.Is(err, gwoutbound.ErrNotFound) {
			status = http.StatusNotFound
			code = "gateway_delivery_not_found"
			message = err.Error()
		}
		writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, gatewayDeliveryDTOFromDelivery(delivery))
}

func gatewayDeliveryDTOs(deliveries []gwoutbound.Delivery) []gatewayDeliveryDTO {
	result := make([]gatewayDeliveryDTO, 0, len(deliveries))
	for _, delivery := range deliveries {
		result = append(result, gatewayDeliveryDTOFromDelivery(delivery))
	}
	return result
}

func gatewayDeliveryDTOFromDelivery(delivery gwoutbound.Delivery) gatewayDeliveryDTO {
	return gatewayDeliveryDTO{
		ID: delivery.ID, ChannelKey: delivery.ChannelKey, HumanProfileID: delivery.HumanProfileID,
		EventType: delivery.EventType, Title: delivery.Title, Status: delivery.Status, Attempts: delivery.Attempts,
		LastError: delivery.LastError, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt, DeliveredAt: delivery.DeliveredAt,
	}
}
