package audio

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOpenClaw(t *testing.T, script string) *OpenClaw {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for speech process tests")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "speech.mjs")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	return NewOpenClaw(OpenClawConfig{NodePath: node, ScriptPath: path, PackagePath: dir, ConfigPath: filepath.Join(dir, "config.json")})
}

func TestOpenClawTranscriptionIsolation(t *testing.T) {
	t.Setenv("ORRERY_SECRET_TEST", "must-not-leave-orrery")
	p := testOpenClaw(t, `
import { existsSync, writeFileSync } from 'node:fs';
let input = ''; for await (const chunk of process.stdin) input += chunk;
const request = JSON.parse(input);
const scratch = process.argv[4];
if (process.env.ORRERY_SECRET_TEST || request.Scope || request.scope || request.owner_id || !existsSync(scratch) || request.mime_type !== 'audio/wav') process.exit(13);
writeFileSync(scratch + '/private-audio', 'temporary');
process.stdout.write(JSON.stringify({text: scratch}));
`)
	result, err := p.Transcribe(context.Background(), TranscriptionRequest{
		Scope: Scope{TenantID: "tenant-a", OwnerID: "owner-a", SourceID: "event-a"},
		Data:  append([]byte("RIFF\x28\x00\x00\x00WAVEfmt "), make([]byte, 32)...), MIMEType: "audio/wave; codecs=pcm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != "openclaw" {
		t.Fatalf("source=%s", result.Source)
	}
	if _, err := os.Stat(result.Text); !os.IsNotExist(err) {
		t.Fatalf("private workspace retained: %v", err)
	}
}

func TestOpenClawCapabilitiesAndSpeech(t *testing.T) {
	p := testOpenClaw(t, `
let input = ''; for await (const chunk of process.stdin) input += chunk;
const request = JSON.parse(input);
if (request.operation === 'capabilities') process.stdout.write(JSON.stringify({transcription_configured:false,synthesis_configured:true}));
else process.stdout.write(Buffer.concat([Buffer.from('ID3'), Buffer.alloc(64)]));
`)
	capabilities, err := p.Capabilities(context.Background())
	if err != nil || capabilities.TranscriptionConfigured || !capabilities.SynthesisConfigured {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	result, err := p.Synthesize(context.Background(), SpeechRequest{Scope: Scope{TenantID: "t", OwnerID: "o", SourceID: "m"}, Text: "hello", Format: "mp3"})
	if err != nil || result.MIMEType != "audio/mpeg" {
		t.Fatalf("speech=%+v err=%v", result, err)
	}
}

func TestOpenClawRejectsMissingOwnerAndConfiguration(t *testing.T) {
	p := NewOpenClaw(OpenClawConfig{})
	_, err := p.Synthesize(context.Background(), SpeechRequest{Text: "hello", Format: "mp3"})
	if err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatal("missing identity was not rejected first")
	}
	_, err = p.Capabilities(context.Background())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenClawErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   error
	}{
		{"process.exit(10)", ErrNotConfigured}, {"process.exit(11)", ErrInvalidAudio}, {"process.exit(12)", ErrEmptyTranscript},
	} {
		_, err := testOpenClaw(t, tc.script).Capabilities(context.Background())
		if !errors.Is(err, tc.want) {
			t.Fatalf("err=%v want=%v", err, tc.want)
		}
	}
	_, err := testOpenClaw(t, `console.error('private secret'); process.exit(13);`).Capabilities(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = testOpenClaw(t, `setTimeout(() => {}, 10000);`).Capabilities(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
