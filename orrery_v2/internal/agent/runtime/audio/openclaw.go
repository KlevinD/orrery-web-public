package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// OpenClawConfig is deployment configuration, never supplied by a channel or user.
// The config file contains speech-only credentials, not OpenClaw's agent config.
type OpenClawConfig struct {
	NodePath, ScriptPath, PackagePath, ConfigPath string
}

type OpenClaw struct {
	config OpenClawConfig
	slots  chan struct{}
}

func NewOpenClaw(config OpenClawConfig) *OpenClaw {
	return &OpenClaw{config: config, slots: make(chan struct{}, 2)}
}

// Configured is not a provider health check. Live acceptance requires real audio.
type OpenClawCapabilities struct {
	TranscriptionConfigured bool `json:"transcription_configured"`
	SynthesisConfigured     bool `json:"synthesis_configured"`
}

type openClawRequest struct {
	Operation string `json:"operation"`
	Data      []byte `json:"data,omitempty"`
	MIMEType  string `json:"mime_type,omitempty"`
	Language  string `json:"language,omitempty"`
	Text      string `json:"text,omitempty"`
	Voice     string `json:"voice,omitempty"`
	Format    string `json:"format,omitempty"`
}

func (p *OpenClaw) Capabilities(ctx context.Context) (OpenClawCapabilities, error) {
	raw, err := p.call(ctx, openClawRequest{Operation: "capabilities"})
	var result OpenClawCapabilities
	if err == nil {
		if json.Unmarshal(raw, &result) != nil {
			err = errors.New("speech backend returned invalid capabilities")
		}
	}
	return result, err
}

func (p *OpenClaw) Transcribe(ctx context.Context, input TranscriptionRequest) (Transcript, error) {
	if _, err := input.Scope.key("transcribe"); err != nil {
		return Transcript{}, err
	}
	if _, err := ValidateAudio(input.Data, input.MIMEType); err != nil {
		return Transcript{}, err
	}
	if len(input.Language) > 32 {
		return Transcript{}, ErrInvalidAudio
	}
	raw, err := p.call(ctx, openClawRequest{Operation: "transcribe", Data: input.Data, MIMEType: DetectMIMEType(input.Data), Language: input.Language})
	if err != nil {
		return Transcript{}, err
	}
	var result struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &result) != nil || !utf8.ValidString(result.Text) || len(result.Text) > 64<<10 {
		return Transcript{}, errors.New("speech backend returned invalid transcript")
	}
	if strings.TrimSpace(result.Text) == "" {
		return Transcript{}, ErrEmptyTranscript
	}
	return Transcript{Text: strings.TrimSpace(result.Text), Source: "openclaw"}, nil
}

func (p *OpenClaw) Synthesize(ctx context.Context, input SpeechRequest) (Speech, error) {
	if _, err := input.Scope.key("speech"); err != nil {
		return Speech{}, err
	}
	if strings.TrimSpace(input.Text) == "" || !utf8.ValidString(input.Text) || utf8.RuneCountInString(input.Text) > 4096 || len(input.Voice) > 128 {
		return Speech{}, ErrInvalidAudio
	}
	// The initial backend contract is MP3; channel encoding belongs to Orrery.
	if input.Format != "mp3" {
		return Speech{}, ErrInvalidAudio
	}
	raw, err := p.call(ctx, openClawRequest{Operation: "synthesize", Text: input.Text, Voice: input.Voice, Format: input.Format})
	if err != nil {
		return Speech{}, err
	}
	if _, err := ValidateAudio(raw, "audio/mpeg"); err != nil {
		return Speech{}, err
	}
	return Speech{Data: raw, MIMEType: "audio/mpeg"}, nil
}

func (p *OpenClaw) call(ctx context.Context, input openClawRequest) ([]byte, error) {
	for _, path := range []string{p.config.NodePath, p.config.ScriptPath, p.config.PackagePath, p.config.ConfigPath} {
		if !filepath.IsAbs(path) {
			return nil, ErrNotConfigured
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, _ := json.Marshal(input)
	scratch, err := os.MkdirTemp("", "orrery-speech-")
	if err != nil {
		return nil, errors.New("create private speech workspace failed")
	}
	defer os.RemoveAll(scratch)
	cmd := exec.CommandContext(ctx, p.config.NodePath, "--max-old-space-size=512", p.config.ScriptPath, p.config.PackagePath, p.config.ConfigPath, scratch)
	// Do not inherit application secrets, proxy overrides, agent state or NODE_OPTIONS.
	cmd.Env = []string{"OPENCLAW_STATE_DIR=" + filepath.Join(scratch, "state"), "OPENCLAW_CONFIG_PATH=" + filepath.Join(scratch, "openclaw.json"), "TMPDIR=" + scratch, "TEMP=" + scratch, "TMP=" + scratch}
	cmd.Stdin = bytes.NewReader(data)
	cmd.WaitDelay = time.Second
	var output boundedAudioBuffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			switch exit.ExitCode() {
			case 10:
				return nil, ErrNotConfigured
			case 11:
				return nil, ErrInvalidAudio
			case 12:
				return nil, ErrEmptyTranscript
			}
		}
		return nil, errors.New("speech backend request failed")
	}
	return output.Bytes(), nil
}
