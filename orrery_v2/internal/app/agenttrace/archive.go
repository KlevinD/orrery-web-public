package agenttrace

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

type ArchiveEntry struct {
	Kind string
	Ref  string
	Data []byte
}

type archiveLine struct {
	Kind   string          `json:"kind"`
	Ref    string          `json:"ref,omitempty"`
	SHA256 string          `json:"sha256,omitempty"`
	Bytes  int             `json:"bytes,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Count  int             `json:"count,omitempty"`
}

func BuildArchive(entries []ArchiveEntry) ([]byte, error) {
	ordered := append([]ArchiveEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := archiveKindOrder(ordered[i].Kind), archiveKindOrder(ordered[j].Kind)
		if left != right {
			return left < right
		}
		return ordered[i].Ref < ordered[j].Ref
	})
	var plain bytes.Buffer
	encoder := json.NewEncoder(&plain)
	encoder.SetEscapeHTML(false)
	for _, entry := range ordered {
		if !json.Valid(entry.Data) {
			return nil, fmt.Errorf("archive entry %s is not valid JSON", entry.Ref)
		}
		digest := sha256.Sum256(entry.Data)
		if err := encoder.Encode(archiveLine{Kind: entry.Kind, Ref: entry.Ref, SHA256: hex.EncodeToString(digest[:]), Bytes: len(entry.Data), Data: json.RawMessage(entry.Data)}); err != nil {
			return nil, err
		}
	}
	integrity := sha256.Sum256(plain.Bytes())
	if err := encoder.Encode(archiveLine{Kind: "integrity", SHA256: hex.EncodeToString(integrity[:]), Count: len(ordered)}); err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	writer.Header.ModTime = time.Unix(0, 0).UTC()
	writer.Header.OS = 255
	if _, err := writer.Write(plain.Bytes()); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func VerifyArchive(data []byte, expectedEntries int) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open trace archive: %w", err)
	}
	plain, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("read trace archive: %w", errorsJoin(err, closeErr))
	}
	scanner := bufio.NewScanner(bytes.NewReader(plain))
	scanner.Buffer(make([]byte, 64*1024), 70<<20)
	count := 0
	var beforeIntegrity bytes.Buffer
	for scanner.Scan() {
		lineBytes := append([]byte(nil), scanner.Bytes()...)
		var line archiveLine
		if err := json.Unmarshal(lineBytes, &line); err != nil {
			return fmt.Errorf("decode trace archive line: %w", err)
		}
		if line.Kind == "integrity" {
			digest := sha256.Sum256(beforeIntegrity.Bytes())
			if line.Count != count || line.SHA256 != hex.EncodeToString(digest[:]) || count != expectedEntries {
				return fmt.Errorf("trace archive integrity mismatch")
			}
			return nil
		}
		if !json.Valid(line.Data) || len(line.Data) != line.Bytes {
			return fmt.Errorf("trace archive entry size mismatch")
		}
		digest := sha256.Sum256(line.Data)
		if line.SHA256 != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("trace archive entry checksum mismatch")
		}
		beforeIntegrity.Write(lineBytes)
		beforeIntegrity.WriteByte('\n')
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("trace archive integrity record missing")
}

func ParseArchive(data []byte) ([]ArchiveEntry, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 70<<20)
	entries := make([]ArchiveEntry, 0)
	for scanner.Scan() {
		var line archiveLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return nil, err
		}
		if line.Kind == "integrity" {
			break
		}
		entries = append(entries, ArchiveEntry{Kind: line.Kind, Ref: line.Ref, Data: append([]byte(nil), line.Data...)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := VerifyArchive(data, len(entries)); err != nil {
		return nil, err
	}
	return entries, nil
}

func archiveKindOrder(kind string) int {
	switch kind {
	case "manifest":
		return 0
	case "start":
		return 1
	case "event":
		return 2
	case "payload":
		return 3
	case "terminal":
		return 4
	default:
		return 5
	}
}

func errorsJoin(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
