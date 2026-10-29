package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/speech"
)

type PersonalAssistantSpeechService interface {
	Synthesize(context.Context, discussionsession.ViewerRef, speech.SynthesizeInput) (speech.Audio, error)
	Transcribe(context.Context, discussionsession.ViewerRef, speech.TranscribeInput) (speech.Transcript, error)
}

func (r *Router) handlePersonalAssistantSpeech(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	scope, principal, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	viewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	if r.personalAssistantSpeech == nil {
		writePersonalAssistantSpeechError(w, req, speech.ErrNotConfigured)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxPersonalAssistantRequestBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	var synthesis speech.SynthesizeInput
	var transcription speech.TranscribeInput
	var input any = &synthesis
	transcribe := strings.Trim(req.URL.Path, "/") == "api/profile/personal-assistant/audio/transcriptions"
	if transcribe {
		input = &transcription
	}
	if err := decoder.Decode(input); err != nil {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeDiscussionError(w, req, discussionsession.ErrInvalidRequest)
		return
	}
	if transcribe {
		result, err := r.personalAssistantSpeech.Transcribe(req.Context(), viewer, transcription)
		if err != nil {
			writePersonalAssistantSpeechError(w, req, err)
			return
		}
		r.recordPersonalAssistantProfileAudit(req, scope, principal, "personal_assistant.audio.transcribed", nil)
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := r.personalAssistantSpeech.Synthesize(req.Context(), viewer, synthesis)
	if err != nil {
		writePersonalAssistantSpeechError(w, req, err)
		return
	}
	r.recordPersonalAssistantProfileAudit(req, scope, principal, "personal_assistant.audio.synthesized", nil)
	w.Header().Set("Content-Type", result.MIMEType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(result.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Data)
}

func writePersonalAssistantSpeechError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, speech.ErrNotConfigured):
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "语音服务尚未配置完成，请暂时使用文字。", CorrelationID: correlationID(req)})
	case errors.Is(err, speech.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "无法处理这段音频或朗读内容，请缩短内容或改用文字。", CorrelationID: correlationID(req)})
	case errors.Is(err, speech.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "语音服务暂时不可用，请稍后重试或使用文字。", CorrelationID: correlationID(req), Retryable: true})
	default:
		writeDiscussionError(w, req, err)
	}
}
