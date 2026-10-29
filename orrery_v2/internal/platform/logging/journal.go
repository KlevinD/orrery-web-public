package logging

import (
	"bytes"
	"io"
	"sync"
)

type journalWriter struct {
	next io.Writer
	mu   sync.Mutex
}

func (w *journalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	priority := byte('6')
	level := p
	if i := bytes.Index(p, []byte(" level=")); i >= 0 {
		level = bytes.SplitN(p[i+7:], []byte(" "), 2)[0]
	}
	switch string(level) {
	case "ERROR":
		priority = '3'
	case "WARN":
		priority = '4'
	case "DEBUG":
		priority = '7'
	}
	data := append([]byte{'<', priority, '>'}, p...)
	n, err := w.next.Write(data)
	if n < len(data) && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
