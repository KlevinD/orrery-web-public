package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestSILKDecoderRequiresExplicitLocalConfiguration(t *testing.T) {
	_, err := (SILKDecoder{}).Decode(context.Background(), []byte("\x02#!SILK_V3 private audio"))
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
	_, err = (SILKDecoder{}).Decode(context.Background(), []byte("not SILK"))
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecoderOutputCannotBypassSizeLimitThroughIOCopy(t *testing.T) {
	var out boundedAudioBuffer
	_, err := io.Copy(&out, struct{ io.Reader }{bytes.NewReader(make([]byte, MaxAudioBytes+1))})
	if err == nil || len(out.Bytes()) > MaxAudioBytes {
		t.Fatal("decoder output bypassed size limit")
	}
}
