package discussionsession

import (
	"archive/zip"
	"bytes"
)

func validateRuntimeDocx(data []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) > 4096 {
		return ErrInvalidRequest
	}
	total := uint64(0)
	for _, file := range reader.File {
		if file.UncompressedSize64 > 16<<20 || total > 32<<20-file.UncompressedSize64 {
			return ErrInvalidRequest
		}
		total += file.UncompressedSize64
	}
	return nil
}
