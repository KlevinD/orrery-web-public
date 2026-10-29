package httptransport

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	wstransport "orrery_v2/internal/transport/websocket"

	"github.com/gorilla/websocket"
)

func TestMissionStreamWebSocketSendsSnapshotFrame(t *testing.T) {
	stream := &fakeMissionStream{snapshot: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 5}}
	handler := newTestRouter(RouterOptions{MissionStream: stream})
	server := httptest.NewServer(handler)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/missions/mission-1/stream", http.Header{"X-User-ID": {"agent-1"}, "X-Viewer-Kind": {"agent"}, "X-Permission-Scope": {"mission.read"}, "X-Orrery-Internal-Viewer-Authorization": {testInternalViewerAuthorization}})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var envelope wstransport.ServerEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if envelope.Type != wstransport.MessageSnapshot || envelope.Version != 5 {
		t.Fatalf("envelope = %+v", envelope)
	}
	if stream.missionID != "mission-1" {
		t.Fatalf("stream mission id = %q", stream.missionID)
	}
}

func TestMissionStreamWebSocketSendsSnapshotBeforeWaitingForPatches(t *testing.T) {
	stream := &blockingMissionStream{
		snapshot: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 5},
		release:  make(chan struct{}),
	}
	handler := newTestRouter(RouterOptions{MissionStream: stream})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer close(stream.release)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/missions/mission-1/stream", http.Header{"X-User-ID": {"agent-1"}, "X-Viewer-Kind": {"agent"}, "X-Permission-Scope": {"mission.read"}, "X-Orrery-Internal-Viewer-Authorization": {testInternalViewerAuthorization}})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("snapshot was not sent before waiting for patches: %v", err)
	}
	var envelope wstransport.ServerEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if envelope.Type != wstransport.MessageSnapshot || envelope.Version != 5 {
		t.Fatalf("snapshot envelope = %+v", envelope)
	}
}

func TestMissionStreamSSEFallbackSendsSnapshotEvent(t *testing.T) {
	stream := &fakeMissionStream{snapshot: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 6}}
	handler := newTestRouter(RouterOptions{MissionStream: stream})
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/missions/mission-1/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Permission-Scope", "mission.read")
	req.Header.Set("Accept", "text/event-stream")
	authorizeInternalViewerTestRequest(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}
	joined := readSSEEvent(t, bufio.NewReader(resp.Body))
	if !strings.Contains(joined, "event: snapshot") || !strings.Contains(joined, `"version":6`) {
		t.Fatalf("sse body = %s", joined)
	}
}

func TestMissionStreamSSEKeepsConnectionOpenAndSendsLaterPatches(t *testing.T) {
	stream := &advancingMissionStream{
		snapshot: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 6},
		patch: missioncontrol.MissionControlPatchDTO{
			PatchID:      "patch-7",
			MissionID:    "mission-1",
			BaseVersion:  6,
			NextVersion:  7,
			ChangedViews: []string{"progress_graph"},
			Payload:      missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 7},
			EmittedAt:    time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
		},
		release: make(chan struct{}),
	}
	handler := newTestRouter(RouterOptions{MissionStream: stream})
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/missions/mission-1/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Permission-Scope", "mission.read")
	req.Header.Set("Accept", "text/event-stream")
	authorizeInternalViewerTestRequest(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	reader := bufio.NewReader(resp.Body)
	first := readSSEEvent(t, reader)
	if !strings.Contains(first, "event: snapshot") || !strings.Contains(first, `"version":6`) {
		t.Fatalf("first SSE event = %s", first)
	}

	stream.releasePatch()
	second := readSSEEvent(t, reader)
	if !strings.Contains(second, "event: patch") || !strings.Contains(second, `"version":7`) {
		t.Fatalf("second SSE event = %s", second)
	}
}

func readSSEEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	deadline := time.After(1500 * time.Millisecond)
	lines := make(chan string, 1)
	go func() {
		var eventLines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				lines <- "read error: " + err.Error()
				return
			}
			eventLines = append(eventLines, strings.TrimRight(line, "\r\n"))
			if strings.TrimSpace(line) == "" {
				lines <- strings.Join(eventLines, "\n")
				return
			}
		}
	}()
	select {
	case event := <-lines:
		return event
	case <-deadline:
		t.Fatal("timed out waiting for SSE event")
		return ""
	}
}

type fakeMissionStream struct {
	snapshot  missioncontrol.MissionControlViewDTO
	patches   []missioncontrol.MissionControlPatchDTO
	missionID string
}

func (f *fakeMissionStream) Snapshot(ctx context.Context, missionID string, req wstransport.SubscribeRequest) (missioncontrol.MissionControlViewDTO, error) {
	f.missionID = missionID
	return f.snapshot, nil
}

func (f *fakeMissionStream) Patches(ctx context.Context, missionID string, req wstransport.SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	return f.patches, nil
}

type blockingMissionStream struct {
	snapshot missioncontrol.MissionControlViewDTO
	release  chan struct{}
}

func (f *blockingMissionStream) Snapshot(ctx context.Context, missionID string, req wstransport.SubscribeRequest) (missioncontrol.MissionControlViewDTO, error) {
	return f.snapshot, nil
}

func (f *blockingMissionStream) Patches(ctx context.Context, missionID string, req wstransport.SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	select {
	case <-f.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type advancingMissionStream struct {
	snapshot missioncontrol.MissionControlViewDTO
	patch    missioncontrol.MissionControlPatchDTO
	release  chan struct{}
}

func (f *advancingMissionStream) Snapshot(ctx context.Context, missionID string, req wstransport.SubscribeRequest) (missioncontrol.MissionControlViewDTO, error) {
	return f.snapshot, nil
}

func (f *advancingMissionStream) Patches(ctx context.Context, missionID string, req wstransport.SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	select {
	case <-f.release:
	default:
		return nil, nil
	}
	if afterVersion >= f.patch.NextVersion {
		return nil, nil
	}
	return []missioncontrol.MissionControlPatchDTO{f.patch}, nil
}

func (f *advancingMissionStream) releasePatch() {
	select {
	case <-f.release:
	default:
		close(f.release)
	}
}
