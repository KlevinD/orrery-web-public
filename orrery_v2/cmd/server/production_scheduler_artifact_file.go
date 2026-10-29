package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

func openScheduledArtifactInRoot(rootDir, absolutePath string) (schedulerapp.RunArtifactContent, bool, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return schedulerapp.RunArtifactContent{}, false, nil
	}
	absoluteRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return schedulerapp.RunArtifactContent{}, false, schedulerapp.ErrNotFound
	}
	absolutePath = filepath.Clean(absolutePath)
	if !filepath.IsAbs(absolutePath) {
		return schedulerapp.RunArtifactContent{}, false, nil
	}
	relativePath, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		return schedulerapp.RunArtifactContent{}, false, nil
	}
	rootSnapshot, err := scheduledArtifactSymlinkFreeRoot(absoluteRoot)
	if err != nil {
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	root, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	defer root.Close()
	openedRoot, err := root.Stat(".")
	if err != nil || !os.SameFile(rootSnapshot, openedRoot) {
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	currentRoot, err := scheduledArtifactSymlinkFreeRoot(absoluteRoot)
	if err != nil || !os.SameFile(rootSnapshot, currentRoot) || !os.SameFile(openedRoot, currentRoot) {
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	current := ""
	for _, segment := range strings.Split(filepath.ToSlash(relativePath), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
		}
		current = path.Join(current, segment)
		info, err := root.Lstat(filepath.FromSlash(current))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
		}
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScheduledArtifactBytes {
		file.Close()
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	currentInfo, err := root.Lstat(relativePath)
	if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, currentInfo) {
		file.Close()
		return schedulerapp.RunArtifactContent{}, true, schedulerapp.ErrNotFound
	}
	buffered := bufio.NewReader(file)
	header, _ := buffered.Peek(512)
	fileName := cleanArtifactFileName(filepath.Base(absolutePath), "artifact")
	return schedulerapp.RunArtifactContent{
		Reader: &schedulerArtifactFileReadCloser{reader: buffered, file: file, snapshot: info}, FileName: fileName,
		ContentType: schedulerArtifactContentType(fileName, "", header), SizeBytes: info.Size(), ModifiedAt: info.ModTime(),
	}, true, nil
}

func scheduledArtifactSymlinkFreeRoot(absoluteRoot string) (os.FileInfo, error) {
	absoluteRoot = filepath.Clean(strings.TrimSpace(absoluteRoot))
	if !filepath.IsAbs(absoluteRoot) {
		return nil, schedulerapp.ErrNotFound
	}
	current := absoluteRoot
	paths := make([]string, 0, 8)
	for {
		paths = append(paths, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	var rootInfo os.FileInfo
	for index := len(paths) - 1; index >= 0; index-- {
		info, err := os.Lstat(paths[index])
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, schedulerapp.ErrNotFound
		}
		if index == 0 {
			rootInfo = info
		}
	}
	if rootInfo == nil || !rootInfo.IsDir() {
		return nil, schedulerapp.ErrNotFound
	}
	return rootInfo, nil
}

type schedulerArtifactFileReadCloser struct {
	reader   *bufio.Reader
	file     *os.File
	snapshot os.FileInfo
}

func (r *schedulerArtifactFileReadCloser) Read(body []byte) (int, error) {
	if r == nil || r.reader == nil {
		return 0, io.ErrClosedPipe
	}
	return r.reader.Read(body)
}

func (r *schedulerArtifactFileReadCloser) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}

func (r *schedulerArtifactFileReadCloser) VerifyUnchanged() error {
	if r == nil || r.file == nil || r.snapshot == nil {
		return fmt.Errorf("scheduled artifact file snapshot is unavailable")
	}
	current, err := r.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(r.snapshot, current) || r.snapshot.Mode() != current.Mode() || r.snapshot.Size() != current.Size() ||
		!r.snapshot.ModTime().Equal(current.ModTime()) {
		return fmt.Errorf("scheduled artifact file changed")
	}
	return nil
}

type scheduledArtifactUnchangedVerifier interface {
	VerifyUnchanged() error
}

func verifyScheduledArtifactReaderUnchanged(reader io.Reader) error {
	verifier, ok := reader.(scheduledArtifactUnchangedVerifier)
	if !ok {
		return nil
	}
	return verifier.VerifyUnchanged()
}

type schedulerArtifactVerifiedReadCloser struct {
	*os.File
	path string
}

func (r *schedulerArtifactVerifiedReadCloser) Close() error {
	if r == nil || r.File == nil {
		return nil
	}
	closeErr := r.File.Close()
	removeErr := os.Remove(r.path)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}

type schedulerArtifactBoundedReadCloser struct {
	reader    io.Reader
	closer    io.Closer
	remaining int64
	exceeded  bool
}

func newSchedulerArtifactBoundedReadCloser(reader io.Reader, closer io.Closer, limit int64) io.ReadCloser {
	return &schedulerArtifactBoundedReadCloser{reader: reader, closer: closer, remaining: limit}
}

func (r *schedulerArtifactBoundedReadCloser) Read(body []byte) (int, error) {
	if r.exceeded {
		return 0, fmt.Errorf("scheduled artifact exceeds %d byte limit", maxScheduledArtifactBytes)
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			r.exceeded = true
			return 0, fmt.Errorf("scheduled artifact exceeds %d byte limit", maxScheduledArtifactBytes)
		}
		return 0, err
	}
	if int64(len(body)) > r.remaining {
		body = body[:r.remaining]
	}
	n, err := r.reader.Read(body)
	r.remaining -= int64(n)
	return n, err
}

func (r *schedulerArtifactBoundedReadCloser) Close() error {
	return r.closer.Close()
}

type schedulerArtifactByteCounter struct {
	total int64
}

func (w *schedulerArtifactByteCounter) Write(body []byte) (int, error) {
	w.total += int64(len(body))
	return len(body), nil
}
