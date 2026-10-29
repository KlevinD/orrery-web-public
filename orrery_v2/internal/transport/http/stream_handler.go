package httptransport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	wstransport "orrery_v2/internal/transport/websocket"

	"github.com/gorilla/websocket"
)

var missionStreamUpgrader = websocket.Upgrader{
	CheckOrigin: websocketOriginAllowed,
}

func (r *Router) handleMissionStream(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.missionStream == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	subscriptionID := firstNonEmpty(req.URL.Query().Get("subscription_id"), "mission:"+missionID.String())
	request := wstransport.SubscribeRequest{
		MissionID:              missionID.String(),
		SubscriptionID:         subscriptionID,
		ViewNames:              splitHeader(req.URL.Query().Get("view_names")),
		ViewerID:               viewer.ViewerID,
		ViewerKind:             string(viewer.ViewerKind),
		TenantID:               viewer.TenantID,
		AuthorizationSubjectID: viewer.AuthorizationSubjectID,
		MissionRoles:           append([]string(nil), viewer.MissionRoles...),
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
	}
	if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		r.handleMissionStreamSSE(w, req, subscriptionID, request)
		return
	}
	conn, err := missionStreamUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	sub := wstransport.NewMissionSubscription(subscriptionID, missionID.String(), r.missionStream, wstransport.BackpressurePolicy{MaxAckLag: 32, MaxQueueDepth: 128})
	snapshot, err := r.missionStream.Snapshot(req.Context(), request.MissionID, request)
	if err != nil {
		status, body := MapError(err, cid)
		_ = conn.WriteJSON(body)
		_ = status
		return
	}
	frame, err := wstransport.EncodeSnapshot(subscriptionID, snapshot)
	if err != nil {
		_ = conn.WriteJSON(ErrorResponse{Code: ErrorInternal, Message: "encode stream snapshot", CorrelationID: cid})
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		return
	}
	sub.RecordSent(snapshot.Version)
	streamWebSocket(req, conn, sub, r.missionStream, request, snapshot.Version)
}

func (r *Router) handleMissionStreamSSE(w http.ResponseWriter, req *http.Request, subscriptionID string, request wstransport.SubscribeRequest) {
	cid := correlationID(req)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: ErrorInternal, Message: "streaming response is not supported", CorrelationID: cid})
		return
	}
	sub := wstransport.NewMissionSubscription(subscriptionID, request.MissionID, r.missionStream, wstransport.BackpressurePolicy{MaxAckLag: 32, MaxQueueDepth: 128})
	snapshot, err := r.missionStream.Snapshot(req.Context(), request.MissionID, request)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	frame, err := wstransport.EncodeSnapshot(subscriptionID, snapshot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: ErrorInternal, Message: "encode stream snapshot", CorrelationID: cid})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err := writeSSEFrame(w, frame); err != nil {
		return
	}
	flusher.Flush()
	sub.RecordSent(snapshot.Version)
	streamSSE(req, w, flusher, sub, r.missionStream, request, snapshot.Version)
}

func streamWebSocket(req *http.Request, conn *websocket.Conn, sub *wstransport.MissionSubscription, source wstransport.StreamSource, request wstransport.SubscribeRequest, version int) {
	ctx := req.Context()
	done := make(chan struct{})
	clientDone := make(chan struct{})
	pings := make(chan struct{}, 1)
	defer close(done)
	go readStreamClientMessages(conn, sub, done, clientDone, pings)
	pollInterval := missionStreamActivePollInterval
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-clientDone:
			return
		case <-pings:
			if err := conn.WriteJSON(wstransport.ServerEnvelope{Type: wstransport.MessageHeartbeat, ServerTime: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				return
			}
		case <-timer.C:
			patches, err := source.Patches(ctx, request.MissionID, request, version)
			if err != nil {
				_ = conn.WriteJSON(wstransport.ServerEnvelope{Type: wstransport.MessageError, Payload: ErrorResponse{Code: ErrorInternal, Message: err.Error(), CorrelationID: correlationID(req)}})
				return
			}
			pollInterval = nextMissionStreamPollInterval(pollInterval, len(patches) > 0)
			timer.Reset(pollInterval)
			for _, patch := range patches {
				frame, err := wstransport.EncodePatch(request.SubscriptionID, patch)
				if err != nil {
					_ = conn.WriteJSON(wstransport.ServerEnvelope{Type: wstransport.MessageError, Payload: ErrorResponse{Code: ErrorInternal, Message: err.Error(), CorrelationID: correlationID(req)}})
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
					return
				}
				sub.RecordSent(patch.NextVersion)
				if patch.NextVersion > version {
					version = patch.NextVersion
				}
			}
		}
	}
}

func streamSSE(req *http.Request, w http.ResponseWriter, flusher http.Flusher, sub *wstransport.MissionSubscription, source wstransport.StreamSource, request wstransport.SubscribeRequest, version int) {
	ctx := req.Context()
	pollInterval := missionStreamActivePollInterval
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			patches, err := source.Patches(ctx, request.MissionID, request, version)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				frame, encodeErr := json.Marshal(wstransport.ServerEnvelope{Type: wstransport.MessageError, Payload: ErrorResponse{Code: ErrorInternal, Message: err.Error(), CorrelationID: correlationID(req)}})
				if encodeErr != nil || writeSSEFrame(w, frame) != nil {
					return
				}
				flusher.Flush()
				return
			}
			pollInterval = nextMissionStreamPollInterval(pollInterval, len(patches) > 0)
			timer.Reset(pollInterval)
			if len(patches) == 0 {
				continue
			}
			for _, patch := range patches {
				frame, err := wstransport.EncodePatch(request.SubscriptionID, patch)
				if err != nil {
					errorFrame, encodeErr := json.Marshal(wstransport.ServerEnvelope{Type: wstransport.MessageError, Payload: ErrorResponse{Code: ErrorInternal, Message: err.Error(), CorrelationID: correlationID(req)}})
					if encodeErr != nil || writeSSEFrame(w, errorFrame) != nil {
						return
					}
					flusher.Flush()
					return
				}
				if err := writeSSEFrame(w, frame); err != nil {
					return
				}
				sub.RecordSent(patch.NextVersion)
				if patch.NextVersion > version {
					version = patch.NextVersion
				}
			}
			flusher.Flush()
		}
	}
}

func readStreamClientMessages(conn *websocket.Conn, sub *wstransport.MissionSubscription, done <-chan struct{}, clientDone chan<- struct{}, pings chan<- struct{}) {
	defer close(clientDone)
	for {
		select {
		case <-done:
			return
		default:
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Type    string `json:"type"`
			Version int    `json:"version"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "ack":
			sub.Ack(msg.Version)
		case "ping":
			select {
			case pings <- struct{}{}:
			default:
			}
		}
	}
}

func writeSSEFrame(w http.ResponseWriter, frame []byte) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", sseEventName(frame)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "data: %s\n\n", frame)
	return err
}

func sseEventName(frame []byte) string {
	if strings.Contains(string(frame), `"type":"patch"`) {
		return "patch"
	}
	if strings.Contains(string(frame), `"type":"error"`) {
		return "error"
	}
	return "snapshot"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
