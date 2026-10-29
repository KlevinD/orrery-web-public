package pdf

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"orrery_v2/internal/platform/document/core"
)

type Extractor struct{}

func (Extractor) Supports(contentType, filename string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "application/pdf" {
		return true
	}
	return strings.EqualFold(filepath.Ext(filename), ".pdf")
}

func (Extractor) Extract(ctx context.Context, source core.Source) (core.ExtractedDocument, error) {
	if err := ctx.Err(); err != nil {
		return core.ExtractedDocument{}, err
	}
	if source.Reader == nil {
		return core.ExtractedDocument{}, fmt.Errorf("%w: source reader is nil", core.ErrInvalid)
	}
	data, err := io.ReadAll(source.Reader)
	if err != nil {
		return core.ExtractedDocument{}, fmt.Errorf("read pdf document: %w", err)
	}
	streams, err := extractStreams(data)
	if err != nil {
		return core.ExtractedDocument{}, err
	}
	var parts []string
	for _, stream := range streams {
		text := strings.TrimSpace(extractTextOperators(stream))
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return core.ExtractedDocument{}, fmt.Errorf("%w: pdf contains no extractable text", core.ErrInvalid)
	}
	return core.ExtractedDocument{
		Text:     text,
		Sections: []core.Section{{Text: text, StartOffset: 0, EndOffset: len([]rune(text))}},
		Metadata: map[string]string{"filename": source.Name},
		Checksum: core.ChecksumText(text),
	}, nil
}

type pdfStream struct {
	dictionary string
	data       []byte
}

func extractStreams(data []byte) ([][]byte, error) {
	var out [][]byte
	offset := 0
	for {
		streamStart := bytes.Index(data[offset:], []byte("stream"))
		if streamStart < 0 {
			break
		}
		streamStart += offset
		streamDataStart := streamStart + len("stream")
		if streamDataStart < len(data) && data[streamDataStart] == '\r' {
			streamDataStart++
		}
		if streamDataStart < len(data) && data[streamDataStart] == '\n' {
			streamDataStart++
		}
		end := bytes.Index(data[streamDataStart:], []byte("endstream"))
		if end < 0 {
			return nil, fmt.Errorf("%w: pdf stream is not terminated", core.ErrInvalid)
		}
		streamDataEnd := streamDataStart + end
		raw := bytes.TrimRight(data[streamDataStart:streamDataEnd], "\r\n")
		decoded, err := decodeStream(pdfStream{
			dictionary: dictionaryBeforeStream(data[:streamStart]),
			data:       raw,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
		offset = streamDataEnd + len("endstream")
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: pdf contains no streams", core.ErrInvalid)
	}
	return out, nil
}

func dictionaryBeforeStream(prefix []byte) string {
	end := bytes.LastIndex(prefix, []byte(">>"))
	if end < 0 {
		return ""
	}
	start := bytes.LastIndex(prefix[:end], []byte("<<"))
	if start < 0 {
		return ""
	}
	return string(prefix[start : end+2])
}

func decodeStream(stream pdfStream) ([]byte, error) {
	if strings.Contains(stream.dictionary, "/FlateDecode") {
		reader, err := zlib.NewReader(bytes.NewReader(stream.data))
		if err != nil {
			return nil, fmt.Errorf("%w: decode pdf flate stream: %v", core.ErrInvalid, err)
		}
		defer reader.Close()
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: read pdf flate stream: %v", core.ErrInvalid, err)
		}
		return decoded, nil
	}
	return stream.data, nil
}

func extractTextOperators(stream []byte) string {
	var out strings.Builder
	for i := 0; i < len(stream); i++ {
		switch stream[i] {
		case '(':
			text, next := parseLiteralString(stream, i)
			if text != "" {
				writePDFText(&out, text)
			}
			i = next
		case '<':
			if i+1 < len(stream) && stream[i+1] == '<' {
				continue
			}
			text, next := parseHexString(stream, i)
			if text != "" {
				writePDFText(&out, text)
			}
			i = next
		}
	}
	return strings.TrimSpace(out.String())
}

func parseLiteralString(data []byte, start int) (string, int) {
	var out strings.Builder
	depth := 1
	for i := start + 1; i < len(data); i++ {
		ch := data[i]
		if ch == '\\' {
			if i+1 >= len(data) {
				break
			}
			decoded, next := decodeEscape(data, i+1)
			if decoded != 0 {
				out.WriteByte(decoded)
			}
			i = next
			continue
		}
		switch ch {
		case '(':
			depth++
			out.WriteByte(ch)
		case ')':
			depth--
			if depth == 0 {
				return out.String(), i
			}
			out.WriteByte(ch)
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), len(data) - 1
}

func decodeEscape(data []byte, start int) (byte, int) {
	ch := data[start]
	switch ch {
	case 'n':
		return '\n', start
	case 'r':
		return '\r', start
	case 't':
		return '\t', start
	case 'b':
		return '\b', start
	case 'f':
		return '\f', start
	case '\\', '(', ')':
		return ch, start
	case '\r':
		if start+1 < len(data) && data[start+1] == '\n' {
			return 0, start + 1
		}
		return 0, start
	case '\n':
		return 0, start
	default:
		if ch >= '0' && ch <= '7' {
			value := int(ch - '0')
			end := start
			for end+1 < len(data) && end-start < 2 && data[end+1] >= '0' && data[end+1] <= '7' {
				end++
				value = value*8 + int(data[end]-'0')
			}
			return byte(value), end
		}
		return ch, start
	}
}

func parseHexString(data []byte, start int) (string, int) {
	end := start + 1
	var hex []byte
	for ; end < len(data); end++ {
		if data[end] == '>' {
			break
		}
		if isHex(data[end]) {
			hex = append(hex, data[end])
		}
	}
	if len(hex)%2 == 1 {
		hex = append(hex, '0')
	}
	decoded := make([]byte, 0, len(hex)/2)
	for i := 0; i+1 < len(hex); i += 2 {
		decoded = append(decoded, fromHex(hex[i])<<4|fromHex(hex[i+1]))
	}
	return string(decoded), end
}

func isHex(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func fromHex(ch byte) byte {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0'
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10
	default:
		return ch - 'A' + 10
	}
}

func writePDFText(out *strings.Builder, text string) {
	text = strings.TrimSpace(normalizePDFText(text))
	if text == "" {
		return
	}
	if out.Len() > 0 {
		last := out.String()[out.Len()-1]
		first := []rune(text)[0]
		if !unicode.IsSpace(rune(last)) && !unicode.IsPunct(first) {
			out.WriteByte(' ')
		}
	}
	out.WriteString(text)
}

func normalizePDFText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}
