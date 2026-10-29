package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

type Generator interface {
	NewID() string
}

type ErrorGenerator interface {
	NewIDContext() (string, error)
}

type Random struct {
	prefix string
	reader io.Reader
}

func NewRandom(prefix string) Random {
	return NewRandomWithReader(prefix, rand.Reader)
}

func NewRandomWithReader(prefix string, reader io.Reader) Random {
	if reader == nil {
		reader = rand.Reader
	}
	return Random{prefix: prefix, reader: reader}
}

func (g Random) NewIDContext() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(g.reader, bytes[:]); err != nil {
		return "", fmt.Errorf("generate random id: %w", err)
	}
	if g.prefix == "" {
		return hex.EncodeToString(bytes[:]), nil
	}
	return g.prefix + "-" + hex.EncodeToString(bytes[:]), nil
}

type Deterministic struct {
	mu      sync.Mutex
	prefix  string
	counter int
}

func NewDeterministic(prefix string) *Deterministic {
	return &Deterministic{prefix: prefix}
}

func (g *Deterministic) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counter++
	return fmt.Sprintf("%s-%06d", g.prefix, g.counter)
}

func (g *Deterministic) NewIDContext() (string, error) {
	return g.NewID(), nil
}
