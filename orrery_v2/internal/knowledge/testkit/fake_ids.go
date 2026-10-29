package testkit

import "fmt"

type FakeIDGenerator struct {
	next int
}

func NewFakeIDGenerator() *FakeIDGenerator {
	return &FakeIDGenerator{}
}

func (g *FakeIDGenerator) NewID(prefix string) string {
	g.next++
	return fmt.Sprintf("%s-%03d", prefix, g.next)
}
