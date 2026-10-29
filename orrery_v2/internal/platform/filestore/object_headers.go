package filestore

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"
	"unicode"
)

const metaSuffix = ".meta.json"

func validContentType(value string) bool {
	if strings.ContainsAny(value, "\r\n") || value == "" {
		return false
	}
	_, _, err := mime.ParseMediaType(value)
	return err == nil
}

func downloadDisposition(contentType, filename string) string {
	mode := "attachment"
	mediaType, _, _ := mime.ParseMediaType(contentType)
	category, _, _ := strings.Cut(mediaType, "/")
	if category == "image" || category == "video" || category == "audio" || mediaType == "application/pdf" {
		mode = "inline"
	}
	filename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '_'
		}
		return r
	}, filename)
	return mime.FormatMediaType(mode, map[string]string{"filename": filename})
}

func normalizeObjectPrefix(prefix string) (string, error) {
	value := strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/")
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\x00:") {
		return "", fmt.Errorf("invalid object prefix %q", prefix)
	}
	components := strings.FieldsFunc(value, func(r rune) bool { return r == '/' })
	if len(components) == 0 {
		return "", fmt.Errorf("object prefix cannot select storage root")
	}
	for _, component := range components {
		if component == "." || component == ".." {
			return "", fmt.Errorf("object prefix contains a relative component: %q", prefix)
		}
	}
	result := strings.Join(components, "/")
	if strings.HasSuffix(value, "/") {
		result += "/"
	}
	return result, nil
}

func deleteObjectKeys(ctx context.Context, keys []string, remove func(context.Context, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var failures []error
	for _, key := range keys {
		if err := remove(ctx, key); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func lastURLComponent(raw string, parsed *url.URL) string {
	if parsed.Path == "" {
		return raw
	}
	return path.Base(parsed.Path)
}
