package process

import (
	"io"
	"strings"
	"sync"
)

const defaultStderrTailBytes = 2048

type StderrTail struct {
	inner io.Writer
	max   int
	mu    sync.Mutex
	buf   []byte
}

func NewStderrTail(inner io.Writer, max int) *StderrTail {
	if max <= 0 {
		max = defaultStderrTailBytes
	}
	if inner == nil {
		inner = io.Discard
	}
	return &StderrTail{inner: inner, max: max}
}

func newStderrTail(inner io.Writer, max int) *StderrTail {
	return NewStderrTail(inner, max)
}

func (s *StderrTail) Write(p []byte) (int, error) {
	if _, err := s.inner.Write(p); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if len(s.buf) > s.max {
		s.buf = s.buf[len(s.buf)-s.max:]
	}
	return len(p), nil
}

func (s *StderrTail) Tail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.buf))
}
