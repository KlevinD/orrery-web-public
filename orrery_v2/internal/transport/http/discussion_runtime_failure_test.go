package httptransport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
)

type failedSnapshotDiscussionService struct{ fakeDiscussionService }

func (s *failedSnapshotDiscussionService) SendMessage(context.Context, discussionsession.ViewerRef, discussionsession.SendMessageCommand) (discussionsession.ConversationDTO, error) {
	return s.conversation, s.err
}

func (s *failedSnapshotDiscussionService) RetryMessage(context.Context, discussionsession.ViewerRef, discussionsession.RetryMessageCommand) (discussionsession.ConversationDTO, error) {
	return s.conversation, s.err
}

func TestDiscussionCommandsPreserveFailedRuntimeSnapshot(t *testing.T) {
	for _, path := range []string{"/api/discussions/messages", "/api/discussions/conv-1/messages/msg-1/retry"} {
		for _, tc := range []struct {
			name, id, state string
			err             error
			status          int
		}{
			{"runtime failure", "conv-1", "failed", fmt.Errorf("wrapped: %w", discussionsession.ErrRuntimeExecution), http.StatusAccepted},
			{"missing snapshot", "", "failed", discussionsession.ErrRuntimeExecution, http.StatusInternalServerError},
			{"unfinished snapshot", "conv-1", "running", discussionsession.ErrRuntimeExecution, http.StatusInternalServerError},
			{"storage failure", "conv-1", "failed", errors.New("storage unavailable"), http.StatusInternalServerError},
			{"authorization failure", "conv-1", "failed", discussionsession.ErrForbidden, http.StatusForbidden},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				service := &failedSnapshotDiscussionService{fakeDiscussionService{conversation: discussionsession.ConversationDTO{ConversationID: tc.id, Status: tc.state}, err: tc.err}}
				handler := newTestRouter(RouterOptions{Discussions: service})
				req := authedRequest(http.MethodPost, path, `{"content":"retry"}`)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != tc.status {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				if tc.status == http.StatusAccepted && (!strings.Contains(rec.Body.String(), `"discussion_id":"conv-1"`) || !strings.Contains(rec.Body.String(), `"status":"failed"`)) {
					t.Fatalf("failed snapshot missing: %s", rec.Body.String())
				}
			})
		}
	}
}
