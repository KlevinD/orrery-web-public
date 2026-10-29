package httptransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
)

const maxDiscussionAttachmentUploadBytes = 64 << 20

func (r *Router) handleListDiscussions(w http.ResponseWriter, req *http.Request) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.discussions.ListConversations(req.Context(), viewer, discussionsession.ListConversationsQuery{
		Page:     positiveQueryInt(req, "page", 1),
		PageSize: positiveQueryInt(req, "page_size", 20),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionPageDTO(page))
}

func (r *Router) handlePrewarmDiscussion(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionPrewarmRequest
	if strings.TrimSpace(conversationID) == "" {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
			return
		}
	}
	conversation, err := r.discussions.PrewarmConversation(req.Context(), viewer, discussionsession.PrewarmConversationCommand{
		ConversationID:   conversationID,
		ConversationKind: body.ConversationKind,
		Title:            body.Title,
		IdempotencyKey:   idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	status := http.StatusAccepted
	if strings.TrimSpace(conversationID) == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleGetDiscussion(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	conversation, err := r.discussions.GetConversationPage(req.Context(), viewer, conversationID, discussionsession.ListMessagesQuery{
		Limit:  positiveQueryInt(req, "limit", 20),
		Offset: discussionNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleListDiscussionMessages(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.discussions.ListMessages(req.Context(), viewer, conversationID, discussionsession.ListMessagesQuery{
		Limit:  positiveQueryInt(req, "limit", 20),
		Offset: discussionNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionMessagePageDTOForViewer(page, viewer))
}

func (r *Router) handleListDiscussionAttachments(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.discussions.ListAttachments(req.Context(), viewer, conversationID)
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionAttachmentPageDTO(page))
}

func (r *Router) handleUploadDiscussionAttachment(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxDiscussionAttachmentUploadBytes)
	file, header, err := req.FormFile("file")
	if err != nil {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return
	}
	defer file.Close()
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	attachment, err := r.discussions.UploadAttachment(req.Context(), viewer, discussionsession.UploadAttachmentCommand{
		ConversationID: conversationID,
		FileName:       header.Filename,
		ContentType:    contentType,
		SizeBytes:      header.Size,
		Reader:         file,
		IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, toDiscussionAttachmentDTO(attachment))
}

func (r *Router) handleStartDiscussionAttachmentDraft(w http.ResponseWriter, req *http.Request) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxDiscussionAttachmentUploadBytes)
	file, header, err := req.FormFile("file")
	if err != nil {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return
	}
	defer file.Close()
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	draft, err := r.discussions.StartAttachmentDraft(req.Context(), viewer, discussionsession.UploadAttachmentCommand{
		ForceNewConversation: strings.EqualFold(strings.TrimSpace(req.FormValue("force_new_conversation")), "true"),
		DirectAgentID:        req.FormValue("direct_agent_id"),
		DirectTargetKind:     req.FormValue("direct_target_kind"),
		DirectTargetID:       req.FormValue("direct_target_id"),
		FileName:             header.Filename,
		ContentType:          contentType,
		SizeBytes:            header.Size,
		Reader:               file,
		IdempotencyKey:       idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, toDiscussionAttachmentDraftDTOForViewer(draft, viewer))
}

func (r *Router) handleDownloadDiscussionAttachment(w http.ResponseWriter, req *http.Request, conversationID string, attachmentID string) {
	r.handleServeDiscussionAttachment(w, req, conversationID, attachmentID, false)
}

func (r *Router) handlePreviewDiscussionAttachment(w http.ResponseWriter, req *http.Request, conversationID string, attachmentID string) {
	r.handleServeDiscussionAttachment(w, req, conversationID, attachmentID, true)
}

func (r *Router) handleServeDiscussionAttachment(w http.ResponseWriter, req *http.Request, conversationID string, attachmentID string, preview bool) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	download, err := r.discussions.DownloadAttachment(req.Context(), viewer, conversationID, attachmentID)
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	defer download.Reader.Close()
	contentType := strings.TrimSpace(download.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if preview {
		if !isDiscussionHTMLAttachment(contentType, download.FileName) {
			http.Error(w, "attachment preview is only available for HTML files", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(io.LimitReader(download.Reader, maxDiscussionAttachmentUploadBytes+1))
		if err != nil {
			writeDiscussionError(w, req, err)
			return
		}
		if len(body) > maxDiscussionAttachmentUploadBytes {
			http.Error(w, "attachment preview is too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = sandboxDiscussionHTML(body)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": download.FileName}))
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; media-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "attachment"
	if strings.HasPrefix(strings.ToLower(contentType), "audio/") {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": download.FileName}))
	if download.SizeBytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(download.SizeBytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, download.Reader)
}

func isDiscussionHTMLAttachment(contentType string, fileName string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	return contentType == "text/html" || strings.HasSuffix(fileName, ".html") || strings.HasSuffix(fileName, ".htm")
}

func sandboxDiscussionHTML(body []byte) []byte {
	body = bytes.ReplaceAll(body, []byte("localStorage"), []byte("__orreryPreviewLocalStorage"))
	body = bytes.ReplaceAll(body, []byte("sessionStorage"), []byte("__orreryPreviewSessionStorage"))
	shim := []byte(`<script>(function(){function memoryStorage(){var values=Object.create(null);return {getItem:function(key){key=String(key);return Object.prototype.hasOwnProperty.call(values,key)?values[key]:null},setItem:function(key,value){values[String(key)]=String(value)},removeItem:function(key){delete values[String(key)]},clear:function(){values=Object.create(null)},key:function(index){return Object.keys(values)[index]||null},get length(){return Object.keys(values).length}}}window.__orreryPreviewLocalStorage=memoryStorage();window.__orreryPreviewSessionStorage=memoryStorage()})();</script>`)
	lower := bytes.ToLower(body)
	if head := bytes.Index(lower, []byte("<head")); head >= 0 {
		if close := bytes.IndexByte(body[head:], '>'); close >= 0 {
			at := head + close + 1
			out := make([]byte, 0, len(body)+len(shim))
			out = append(out, body[:at]...)
			out = append(out, shim...)
			out = append(out, body[at:]...)
			return out
		}
	}
	return append(shim, body...)
}

func (r *Router) handleDeleteDiscussion(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	if err := r.discussions.DeleteConversation(req.Context(), viewer, conversationID); err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleAbortDiscussion(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	conversation, err := r.discussions.AbortConversation(req.Context(), viewer, conversationID)
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleInterjectDiscussion(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionMessageRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	message, err := r.discussions.InterjectMessage(req.Context(), viewer, discussionsession.InterjectMessageCommand{
		ConversationID: conversationID,
		Content:        body.Content,
		Mentions:       mapDiscussionMessageMentions(body.Mentions),
		ExpectedTurnID: body.ExpectedTurnID,
		ExpectedRunID:  body.ExpectedRunID,
		IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDiscussionMessageDTOForViewer(message, viewer))
}

func (r *Router) handleAddDiscussionMembers(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionAddMembersRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	conversation, err := r.discussions.AddConversationMembers(req.Context(), viewer, discussionsession.AddConversationMembersCommand{
		ConversationID: conversationID,
		TargetIDs:      body.TargetIDs,
		IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleSetDiscussionWorkspace(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionWorkspaceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	conversation, err := r.discussions.SetConversationWorkspace(req.Context(), viewer, discussionsession.SetConversationWorkspaceCommand{
		ConversationID:   conversationID,
		WorkspaceRootDir: body.WorkspaceRootDir,
		IdempotencyKey:   idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleSetDiscussionHILMode(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionHILModeRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return
	}
	update, err := r.discussions.SetConversationHILMode(req.Context(), viewer, discussionsession.SetConversationHILModeCommand{
		ConversationID: conversationID,
		Mode:           body.Mode,
		IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionHILModeDTO(update))
}

func (r *Router) handleSendDiscussionMessage(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionMessageRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		events, err := r.discussions.SendMessageStream(req.Context(), viewer, discussionsession.SendMessageCommand{
			ConversationID:       conversationID,
			ConversationKind:     body.ConversationKind,
			ForceNewConversation: body.ForceNewConversation,
			Content:              body.Content,
			DirectAgentID:        body.DirectAgentID,
			DirectTargetKind:     body.DirectTargetKind,
			DirectTargetID:       body.DirectTargetID,
			WorkspaceRootDir:     body.WorkspaceRootDir,
			AttachmentIDs:        body.AttachmentIDs,
			Mentions:             mapDiscussionMessageMentions(body.Mentions),
			IdempotencyKey:       idempotencyKey(req),
		})
		if err != nil {
			writeDiscussionError(w, req, err)
			return
		}
		r.writeDiscussionSSEEvents(w, req, viewer, events)
		return
	}
	conversation, err := r.discussions.SendMessage(req.Context(), viewer, discussionsession.SendMessageCommand{
		ConversationID:       conversationID,
		ConversationKind:     body.ConversationKind,
		ForceNewConversation: body.ForceNewConversation,
		Content:              body.Content,
		DirectAgentID:        body.DirectAgentID,
		DirectTargetKind:     body.DirectTargetKind,
		DirectTargetID:       body.DirectTargetID,
		WorkspaceRootDir:     body.WorkspaceRootDir,
		AttachmentIDs:        body.AttachmentIDs,
		Mentions:             mapDiscussionMessageMentions(body.Mentions),
		IdempotencyKey:       idempotencyKey(req),
	})
	if err != nil && !(errors.Is(err, discussionsession.ErrRuntimeExecution) && conversation.ConversationID != "" && conversation.Status == "failed") {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleRetryDiscussionMessage(w http.ResponseWriter, req *http.Request, conversationID string, messageID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body groupDiscussionRetryRequest
	if req.Body != nil {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}
	cmd := discussionsession.RetryMessageCommand{
		ConversationID: conversationID,
		MessageID:      messageID,
		IdempotencyKey: idempotencyKey(req),
		Approve:        body.Approve,
	}
	if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		events, err := r.discussions.RetryMessageStream(req.Context(), viewer, cmd)
		if err != nil {
			writeDiscussionError(w, req, err)
			return
		}
		r.writeDiscussionSSEEvents(w, req, viewer, events)
		return
	}
	conversation, err := r.discussions.RetryMessage(req.Context(), viewer, cmd)
	if err != nil && !(errors.Is(err, discussionsession.ErrRuntimeExecution) && conversation.ConversationID != "" && conversation.Status == "failed") {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) handleDiscussionApprovalDecision(w http.ResponseWriter, req *http.Request, conversationID string, approvalID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	if hilDecisionID, delegated := r.discussionApprovalHILDecision(req, viewer, conversationID, approvalID); delegated {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Code:          ErrorConflict,
			Message:       "approval is managed by HIL",
			Details:       map[string]string{"hil_decision_id": hilDecisionID},
			CorrelationID: correlationID(req),
		})
		return
	}
	if idempotencyKey(req) == "" {
		status, body := invalidRequest(correlationID(req), "idempotency_key", "idempotency key is required for mutating commands")
		writeJSON(w, status, body)
		return
	}
	var body approvalDecisionRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return
	}
	conversation, err := r.discussions.DecideRuntimeApproval(req.Context(), viewer, discussionsession.RuntimeApprovalDecisionCommand{
		ConversationID: conversationID,
		ApprovalID:     approvalID,
		ActionID:       body.ActionID,
		Message:        body.Message,
		ModifiedInput:  body.ModifiedInput,
		ChoiceID:       body.ChoiceID,
		Remember:       body.Remember,
		IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toDiscussionDTOForViewer(conversation, viewer))
}

func (r *Router) discussionApprovalHILDecision(req *http.Request, viewer discussionsession.ViewerRef, conversationID string, approvalID string) (string, bool) {
	if r == nil || r.discussions == nil {
		return "", false
	}
	conversation, err := r.discussions.GetConversation(req.Context(), viewer, conversationID)
	if err != nil {
		return "", false
	}
	for _, event := range conversation.Events {
		if event.Kind != "discussion_runtime_approval_requested" {
			continue
		}
		requestID := strings.TrimSpace(event.Metadata["approval_request_id"])
		if !strings.EqualFold(strings.TrimSpace(event.EventID), strings.TrimSpace(approvalID)) && !strings.EqualFold(requestID, strings.TrimSpace(approvalID)) {
			continue
		}
		if hilDecisionID := strings.TrimSpace(event.Metadata["hil_decision_id"]); hilDecisionID != "" {
			return hilDecisionID, true
		}
	}
	return "", false
}

func (r *Router) handleGetLatestDiscussionDebug(w http.ResponseWriter, req *http.Request, conversationID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	debug, err := r.discussions.GetLatestRunDebug(req.Context(), viewer, conversationID)
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionRunDebugDTO(debug))
}

func (r *Router) handleGetDiscussionRunDebug(w http.ResponseWriter, req *http.Request, conversationID string, runID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	debug, err := r.discussions.GetRunDebug(req.Context(), viewer, conversationID, runID)
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionRunDebugDTO(debug))
}

func (r *Router) writeDiscussionSSEEvents(w http.ResponseWriter, req *http.Request, viewer discussionsession.ViewerRef, events <-chan discussionsession.StreamEventDTO) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(toDiscussionStreamEventDTOForViewer(event, viewer))
			if err != nil {
				status, response := MapError(err, correlationID(req))
				payload, _ = json.Marshal(response)
				if _, writeErr := fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload); writeErr != nil {
					return
				}
				if status >= http.StatusInternalServerError {
					return
				}
				continue
			}
			name := sanitizeSSEField(event.Kind)
			if name == "" {
				name = "message"
			}
			if eventID := sanitizeSSEField(event.EventID); eventID != "" {
				if _, err := fmt.Fprintf(w, "id: %s\n", eventID); err != nil {
					return
				}
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

func sanitizeSSEField(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", "")
	return strings.ReplaceAll(value, "\n", "")
}

type groupDiscussionMessageRequest struct {
	Content              string                                 `json:"content"`
	ConversationKind     string                                 `json:"conversation_kind"`
	ForceNewConversation bool                                   `json:"force_new_conversation"`
	DirectAgentID        string                                 `json:"direct_agent_id"`
	DirectTargetKind     string                                 `json:"direct_target_kind"`
	DirectTargetID       string                                 `json:"direct_target_id"`
	WorkspaceRootDir     string                                 `json:"workspace_root_dir"`
	ExpectedTurnID       string                                 `json:"expected_turn_id"`
	ExpectedRunID        string                                 `json:"expected_run_id"`
	AttachmentIDs        []string                               `json:"attachment_ids"`
	Mentions             []groupDiscussionMessageMentionRequest `json:"mentions"`
}

type groupDiscussionPrewarmRequest struct {
	Title            string `json:"title"`
	ConversationKind string `json:"conversation_kind"`
}

type groupDiscussionMessageMentionRequest struct {
	TargetKind  string `json:"target_kind"`
	TargetID    string `json:"target_id"`
	DisplayName string `json:"display_name"`
}

func mapDiscussionMessageMentions(records []groupDiscussionMessageMentionRequest) []discussionsession.MessageMentionDTO {
	out := make([]discussionsession.MessageMentionDTO, 0, len(records))
	for _, record := range records {
		out = append(out, discussionsession.MessageMentionDTO{TargetKind: record.TargetKind, TargetID: record.TargetID, DisplayName: record.DisplayName})
	}
	return out
}

type groupDiscussionRetryRequest struct {
	Approve bool `json:"approve"`
}

type groupDiscussionAddMembersRequest struct {
	TargetIDs []string `json:"target_ids"`
}

type groupDiscussionWorkspaceRequest struct {
	WorkspaceRootDir string `json:"workspace_root_dir"`
}

type groupDiscussionHILModeRequest struct {
	Mode string `json:"mode"`
}

func discussionViewerFromRequest(w http.ResponseWriter, req *http.Request) (discussionsession.ViewerRef, bool) {
	base, err := ViewerFromRequest(req)
	if err != nil {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return discussionsession.ViewerRef{}, false
	}
	viewerID := strings.TrimSpace(base.ViewerID)
	kind := discussionsession.ViewerKind(base.ViewerKind)
	agentProfileID := ""
	if kind == discussionsession.ViewerKindUser || kind == discussionsession.ViewerKindHuman {
		agentProfileID = strings.TrimSpace(base.AuthorizationSubjectID)
		viewerID = agentProfileID
	}
	if viewerID == "" {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return discussionsession.ViewerRef{}, false
	}
	displayName := displayNameFromRequest(req)
	tenantID := strings.TrimSpace(base.TenantID)
	ownerID := viewerID
	identityIDs := []string{viewerID, ownerID, base.ViewerID, base.AuthorizationSubjectID}
	if principal, exists := identity.PrincipalFromContext(req.Context()); exists {
		tenantID = firstNonEmpty(strings.TrimSpace(principal.TenantID), tenantID)
		ownerID = firstNonEmpty(accountOwnerIDFromPrincipal(principal), ownerID)
		agentProfileID = firstNonEmpty(strings.TrimSpace(principal.AgentProfileID), agentProfileID)
		if strings.EqualFold(agentProfileID, agentcore.SystemSuperadminPrincipalID) {
			kind = discussionsession.ViewerKindSystem
			viewerID = agentcore.SystemSuperadminPrincipalID
			agentProfileID = ""
		} else if kind == discussionsession.ViewerKindUser || kind == discussionsession.ViewerKindHuman {
			viewerID = agentProfileID
		}
		identityIDs = append(identityIDs, ownerID, principal.AgentProfileID, principal.UserID, principal.Username, principal.Subject)
	}
	if tenantID == "" {
		tenantID = "global"
	}
	return discussionsession.ViewerRef{
		ViewerID: viewerID, OwnerID: ownerID, IdentityIDs: compactDiscussionIdentityIDs(identityIDs), AgentProfileID: agentProfileID,
		ViewerKind: kind, DisplayName: displayName, TenantID: tenantID,
		Language:        memoryRequestLanguage(req),
		PermissionScope: append([]string(nil), base.PermissionScope...),
	}, true
}

func compactDiscussionIdentityIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func writeDiscussionError(w http.ResponseWriter, req *http.Request, err error) {
	status, response := MapError(err, correlationID(req))
	writeJSON(w, status, response)
}

func positiveQueryInt(req *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func discussionNonNegativeQueryInt(req *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
