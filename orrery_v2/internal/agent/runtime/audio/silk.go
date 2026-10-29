package audio

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"
)

func IsSILK(data []byte) bool {
	return bytes.HasPrefix(data, []byte("#!SILK_V3")) || bytes.HasPrefix(data, []byte("\x02#!SILK_V3"))
}

// SILKDecoder runs the pinned local codec helper. Neither path comes from channel input.
// The helper receives only media over stdin, with no application credentials in its environment.
type SILKDecoder struct {
	NodePath   string
	ScriptPath string
}

func (d SILKDecoder) Decode(ctx context.Context, data []byte) ([]byte, error) {
	if !IsSILK(data) || len(data) > MaxAudioBytes {
		return nil, ErrInvalidAudio
	}
	if !filepath.IsAbs(d.NodePath) || !filepath.IsAbs(d.ScriptPath) {
		return nil, ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.NodePath, "--max-old-space-size=128", d.ScriptPath)
	cmd.Env = []string{}
	cmd.Stdin = bytes.NewReader(data)
	var output boundedAudioBuffer
	cmd.Stdout = &output
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("SILK decoder failed")
	}
	if _, err := ValidateAudio(output.Bytes(), "audio/wav"); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type boundedAudioBuffer struct{ data bytes.Buffer }

func (b *boundedAudioBuffer) Bytes() []byte { return b.data.Bytes() }

func (b *boundedAudioBuffer) Write(data []byte) (int, error) {
	if len(data) > MaxAudioBytes-b.data.Len() {
		return 0, errors.New("decoded audio exceeds limits")
	}
	return b.data.Write(data)
}
