package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	wstransport "orrery_v2/internal/transport/websocket"
)

func TestMissionStreamPollIntervalBacksOffAndResumes(t *testing.T) {
	interval := missionStreamActivePollInterval
	for _, want := range []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, time.Second, time.Second} {
		interval = nextMissionStreamPollInterval(interval, false)
		if interval != want {
			t.Fatalf("idle interval = %v, want %v", interval, want)
		}
	}
	interval = nextMissionStreamPollInterval(interval, true)
	if interval != 100*time.Millisecond {
		t.Fatalf("active interval = %v", interval)
	}
	if got := nextMissionStreamPollInterval(interval, false); got != 200*time.Millisecond {
		t.Fatalf("next idle interval = %v", got)
	}
}

func TestMissionStreamSSEPollingLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := &pollTrackingMissionStream{advancingMissionStream: advancingMissionStream{
			snapshot: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 6},
			patch: missioncontrol.MissionControlPatchDTO{
				PatchID: "patch-7", MissionID: "mission-1", BaseVersion: 6, NextVersion: 7,
				Payload: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 7},
			},
			release: make(chan struct{}),
		}}
		handler := newTestRouter(RouterOptions{MissionStream: stream})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/missions/mission-1/stream", nil)
		req.Header.Set("X-User-ID", "agent-1")
		req.Header.Set("X-Viewer-Kind", "agent")
		req.Header.Set("X-Permission-Scope", "mission.read")
		req.Header.Set("Accept", "text/event-stream")
		authorizeInternalViewerTestRequest(req)
		response := httptest.NewRecorder()
		finished := false
		go func() {
			handler.ServeHTTP(response, req)
			finished = true
		}()
		synctest.Wait()
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: snapshot") {
			t.Fatalf("initial response = %d %s", response.Code, response.Body.String())
		}

		checkNextPoll := func(interval time.Duration, wantVersion int) {
			t.Helper()
			before := len(stream.afterVersions)
			time.Sleep(interval - time.Nanosecond)
			synctest.Wait()
			if len(stream.afterVersions) != before {
				t.Fatalf("polled before %v elapsed", interval)
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if len(stream.afterVersions) != before+1 || stream.afterVersions[before] != wantVersion {
				t.Fatalf("poll history = %v, want next version %d after %v", stream.afterVersions, wantVersion, interval)
			}
		}
		for _, interval := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, time.Second} {
			checkNextPoll(interval, 6)
		}
		stream.releasePatch()
		checkNextPoll(time.Second, 6)
		if !strings.Contains(response.Body.String(), "event: patch") || !strings.Contains(response.Body.String(), `"version":7`) {
			t.Fatalf("patch response = %s", response.Body.String())
		}
		checkNextPoll(100*time.Millisecond, 7)
		cancel()
		synctest.Wait()
		if !finished {
			t.Fatal("stream did not stop after cancellation")
		}
		before := len(stream.afterVersions)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if len(stream.afterVersions) != before {
			t.Fatal("stream polled after cancellation")
		}
	})
}

type pollTrackingMissionStream struct {
	advancingMissionStream
	afterVersions []int
}

func (s *pollTrackingMissionStream) Patches(ctx context.Context, missionID string, req wstransport.SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	s.afterVersions = append(s.afterVersions, afterVersion)
	return s.advancingMissionStream.Patches(ctx, missionID, req, afterVersion)
}
