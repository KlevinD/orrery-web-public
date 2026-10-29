package audio

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed local_speech.py
var localSpeechScript string

// These paths are operator configuration, never conversation input.
type LocalConfig struct{ PythonPath, ASRDir, TTSDir, WorkDir string }
type Local struct{ config LocalConfig }

// All application adapters share one slot, including gateway and attachment
// transcription. No resident model or independent per-conversation pool exists.
var localSpeechSlot = make(chan struct{}, 1)

func NewLocal(config LocalConfig) *Local { return &Local{config: config} }

func (p *Local) Transcribe(ctx context.Context, input TranscriptionRequest) (Transcript, error) {
	if _, err := input.Scope.key("transcribe"); err != nil {
		return Transcript{}, err
	}
	if _, err := ValidateAudio(input.Data, input.MIMEType); err != nil {
		return Transcript{}, err
	}
	if input.Language != "" && input.Language != "zh" && input.Language != "en" && input.Language != "auto" {
		return Transcript{}, ErrInvalidAudio
	}
	raw, err := p.call(ctx, openClawRequest{Operation: "transcribe", Data: input.Data, Language: input.Language})
	if err != nil {
		return Transcript{}, err
	}
	var result struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &result) != nil || !utf8.ValidString(result.Text) || len(result.Text) > 64<<10 {
		return Transcript{}, ErrInvalidAudio
	}
	if strings.TrimSpace(result.Text) == "" {
		return Transcript{}, ErrEmptyTranscript
	}
	return Transcript{Text: strings.TrimSpace(result.Text), Source: "local_sherpa"}, nil
}

func (p *Local) Synthesize(ctx context.Context, input SpeechRequest) (Speech, error) {
	if _, err := input.Scope.key("speech"); err != nil {
		return Speech{}, err
	}
	if strings.TrimSpace(input.Text) == "" || !utf8.ValidString(input.Text) || utf8.RuneCountInString(input.Text) > 1500 || input.Format != "mp3" || (input.Voice != "" && input.Voice != "default" && input.Voice != "zh") {
		return Speech{}, ErrInvalidAudio
	}
	raw, err := p.call(ctx, openClawRequest{Operation: "synthesize", Text: input.Text, Format: input.Format})
	if err != nil {
		return Speech{}, err
	}
	if _, err = ValidateAudio(raw, "audio/mpeg"); err != nil {
		return Speech{}, err
	}
	return Speech{Data: raw, MIMEType: "audio/mpeg"}, nil
}

func (p *Local) call(ctx context.Context, input openClawRequest) ([]byte, error) {
	for _, path := range []string{p.config.PythonPath, p.config.ASRDir, p.config.TTSDir, p.config.WorkDir} {
		if !filepath.IsAbs(path) {
			return nil, ErrNotConfigured
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	select {
	case localSpeechSlot <- struct{}{}:
		defer func() { <-localSpeechSlot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := os.MkdirAll(p.config.WorkDir, 0700); err != nil {
		return nil, ErrNotConfigured
	}
	scratch, err := os.MkdirTemp(p.config.WorkDir, "speech-")
	if err != nil {
		return nil, ErrNotConfigured
	}
	defer os.RemoveAll(scratch)
	data, _ := json.Marshal(input)
	cmd := exec.CommandContext(ctx, p.config.PythonPath, "-c", localSpeechScript, p.config.ASRDir, p.config.TTSDir, scratch)
	// No API keys, database credentials or agent state reach the decoder.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1", "OMP_NUM_THREADS=2", "OPENBLAS_NUM_THREADS=1", "MKL_NUM_THREADS=1", "TMPDIR=" + scratch}
	cmd.Stdin = bytes.NewReader(data)
	cmd.WaitDelay = time.Second
	var output boundedAudioBuffer
	cmd.Stdout = &output
	if err = cmd.Run(); err != nil {
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
		return nil, errors.New("bounded local speech processing failed")
	}
	return output.Bytes(), nil
}

var _ Provider = (*Local)(nil)
