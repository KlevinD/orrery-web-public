// Package audio defines Orrery speech contracts and replaceable backend adapters.
// Authorization and private attachment ownership belong to the calling application service.
package audio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

const MaxAudioBytes = 16 << 20

var (
	ErrNotConfigured   = errors.New("audio gateway capability is not configured")
	ErrInvalidAudio    = errors.New("audio format is unsupported or content is invalid")
	ErrEmptyTranscript = errors.New("audio gateway returned an empty transcript")
)

type Config struct {
	// BaseURL pins the gateway, including its API prefix, independently of mutable profiles.
	BaseURL                string
	TranscriptionProfileID string
	SpeechProfileID        string
	Voices                 []string
	Profiles               llmprofile.ProfileProvider
	Secrets                llmprofile.SecretResolver
	Client                 *http.Client
}

type Gateway struct{ config Config }

func NewGateway(config Config) *Gateway {
	config.Voices = append([]string(nil), config.Voices...)
	return &Gateway{config: config}
}

type Scope struct {
	TenantID string
	OwnerID  string
	// SourceID is the verified inbound event or persisted assistant message ID.
	SourceID string
}

func (s Scope) key(operation string) (string, error) {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.OwnerID) == "" || strings.TrimSpace(s.SourceID) == "" {
		return "", errors.New("audio requires a tenant, owner and source identity")
	}
	encoded, _ := json.Marshal([]string{operation, s.TenantID, s.OwnerID, s.SourceID})
	return fmt.Sprintf("audio-%x", sha256.Sum256(encoded)), nil
}

type TranscriptionRequest struct {
	Scope    Scope
	Data     []byte
	MIMEType string
	Language string
}

type Transcript struct {
	Text   string
	Source string
}

type SpeechRequest struct {
	Scope  Scope
	Text   string
	Voice  string
	Format string
}

type Speech struct {
	Data     []byte
	MIMEType string
}

func (g *Gateway) Transcribe(ctx context.Context, input TranscriptionRequest) (Transcript, error) {
	key, err := input.Scope.key("transcribe")
	if err != nil {
		return Transcript{}, err
	}
	ext, err := ValidateAudio(input.Data, input.MIMEType)
	if err != nil {
		return Transcript{}, err
	}
	profile, token, endpoint, err := g.resolve(ctx, g.config.TranscriptionProfileID, "transcriptions")
	if err != nil {
		return Transcript{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "voice"+ext)
	if err != nil {
		return Transcript{}, err
	}
	if _, err = part.Write(input.Data); err != nil {
		return Transcript{}, err
	}
	_ = writer.WriteField("model", profile.Model)
	_ = writer.WriteField("response_format", "json")
	if language := strings.TrimSpace(input.Language); language != "" {
		if len(language) > 32 {
			return Transcript{}, errors.New("audio language is too long")
		}
		_ = writer.WriteField("language", language)
	}
	if err = writer.Close(); err != nil {
		return Transcript{}, err
	}
	raw, _, err := g.post(ctx, endpoint, token, key, writer.FormDataContentType(), body.Bytes(), 1<<20)
	if err != nil {
		return Transcript{}, err
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Transcript{}, errors.New("audio gateway returned invalid transcription JSON")
	}
	result.Text = strings.TrimSpace(result.Text)
	if result.Text == "" {
		return Transcript{}, ErrEmptyTranscript
	}
	if !utf8.ValidString(result.Text) || len(result.Text) > 64<<10 {
		return Transcript{}, errors.New("audio transcript exceeds limits")
	}
	return Transcript{Text: result.Text, Source: "model_gateway"}, nil
}

func (g *Gateway) Synthesize(ctx context.Context, input SpeechRequest) (Speech, error) {
	if input.Voice == "" && len(g.config.Voices) > 0 {
		input.Voice = g.config.Voices[0]
	}
	key, err := input.Scope.key("speech:" + input.Voice + ":" + input.Format)
	if err != nil {
		return Speech{}, err
	}
	text := strings.TrimSpace(input.Text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 4096 {
		return Speech{}, fmt.Errorf("%w: speech text must contain between 1 and 4096 characters", ErrInvalidAudio)
	}
	allowed := false
	for _, voice := range g.config.Voices {
		if voice == input.Voice && voice != "" {
			allowed = true
		}
	}
	if !allowed {
		return Speech{}, fmt.Errorf("%w: speech voice is not configured", ErrInvalidAudio)
	}
	mediaType := ""
	switch input.Format {
	case "mp3":
		mediaType = "audio/mpeg"
	case "wav":
		mediaType = "audio/wav"
	default:
		return Speech{}, ErrInvalidAudio
	}
	profile, token, endpoint, err := g.resolve(ctx, g.config.SpeechProfileID, "speech")
	if err != nil {
		return Speech{}, err
	}
	body, _ := json.Marshal(map[string]string{"model": profile.Model, "input": text, "voice": input.Voice, "response_format": input.Format})
	raw, _, err := g.post(ctx, endpoint, token, key, "application/json", body, MaxAudioBytes)
	if err != nil {
		return Speech{}, err
	}
	if _, err := ValidateAudio(raw, mediaType); err != nil {
		return Speech{}, err
	}
	return Speech{Data: raw, MIMEType: mediaType}, nil
}

func (g *Gateway) resolve(ctx context.Context, profileID, operation string) (llmprofile.LLMProfile, string, string, error) {
	empty := llmprofile.LLMProfile{}
	if g.config.Profiles == nil || strings.TrimSpace(profileID) == "" {
		return empty, "", "", ErrNotConfigured
	}
	base, err := gatewayURL(g.config.BaseURL)
	if err != nil {
		return empty, "", "", err
	}
	profiles, err := g.config.Profiles.LLMProfiles(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return empty, "", "", ctx.Err()
		}
		return empty, "", "", errors.New("load audio gateway profiles failed")
	}
	var selected *llmprofile.LLMProfile
	for _, profile := range profiles {
		if profile.ProfileID != profileID {
			continue
		}
		if selected != nil {
			return empty, "", "", errors.New("audio gateway profile is ambiguous")
		}
		copy := profile
		selected = &copy
	}
	if selected == nil || !selected.Enabled || strings.TrimSpace(selected.Model) == "" {
		return empty, "", "", ErrNotConfigured
	}
	profileURL, err := gatewayURL(selected.BaseURL)
	if err != nil || profileURL != base {
		return empty, "", "", errors.New("audio profile does not use the configured gateway")
	}
	token := strings.TrimSpace(selected.APIKey)
	if token == "" && selected.APIKeyRef != "" && g.config.Secrets != nil {
		token, err = g.config.Secrets.ResolveSecret(ctx, selected.APIKeyRef)
		if err != nil {
			if ctx.Err() != nil {
				return empty, "", "", ctx.Err()
			}
			return empty, "", "", errors.New("resolve audio gateway credential failed")
		}
	}
	if strings.TrimSpace(token) == "" {
		return empty, "", "", ErrNotConfigured
	}
	return *selected, token, base + "/audio/" + operation, nil
}

func gatewayURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrNotConfigured
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (g *Gateway) post(ctx context.Context, endpoint, token, key, contentType string, body []byte, limit int64) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", errors.New("create audio gateway request failed")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Idempotency-Key", key)
	client := http.Client{Timeout: 60 * time.Second}
	if g.config.Client != nil {
		client = *g.config.Client
	}
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		// Transport errors can contain the URL, credentials or private request content.
		return nil, "", errors.New("audio gateway transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("audio gateway returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, "", errors.New("audio gateway response exceeds limits")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("read audio gateway response failed")
	}
	if int64(len(raw)) > limit {
		return nil, "", errors.New("audio gateway response exceeds limits")
	}
	return raw, resp.Header.Get("Content-Type"), nil
}
