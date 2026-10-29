package audio

import (
	"bytes"
	"mime"
	"net/http"
	"strings"
)

// ValidateAudio checks the container signature, not only the caller's MIME label.
// Codecs such as SILK need an explicit conversion adapter; they are not sent as WAV.
func ValidateAudio(data []byte, declared string) (string, error) {
	if len(data) < 12 || len(data) > MaxAudioBytes {
		return "", ErrInvalidAudio
	}
	mediaType, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(declared)))
	if err != nil {
		return "", ErrInvalidAudio
	}
	detected := http.DetectContentType(data)
	switch mediaType {
	case "audio/wav", "audio/x-wav", "audio/wave":
		if bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")) {
			return ".wav", nil
		}
	case "audio/mpeg", "audio/mp3":
		if detected == "audio/mpeg" || data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x18 != 0x08 && data[1]&0x06 != 0 {
			return ".mp3", nil
		}
	case "audio/ogg", "application/ogg":
		if detected == "application/ogg" {
			return ".ogg", nil
		}
	case "audio/flac":
		if bytes.Equal(data[:4], []byte("fLaC")) {
			return ".flac", nil
		}
	case "audio/webm":
		if detected == "video/webm" {
			return ".webm", nil
		}
	case "audio/mp4", "audio/m4a":
		if detected == "video/mp4" {
			return ".m4a", nil
		}
	}
	return "", ErrInvalidAudio
}

func DetectMIMEType(data []byte) string {
	for _, mediaType := range []string{"audio/wav", "audio/mpeg", "audio/ogg", "audio/flac", "audio/webm", "audio/mp4"} {
		if _, err := ValidateAudio(data, mediaType); err == nil {
			return mediaType
		}
	}
	return "application/octet-stream"
}
