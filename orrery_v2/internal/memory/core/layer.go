package core

type MemoryLayer string

const (
	MemoryLayerFact       MemoryLayer = "fact"
	MemoryLayerExperience MemoryLayer = "experience"
	MemoryLayerWorld      MemoryLayer = "world"
)

func (l MemoryLayer) IsValid() bool {
	switch l {
	case MemoryLayerFact, MemoryLayerExperience, MemoryLayerWorld:
		return true
	default:
		return false
	}
}

func NormalizeMemoryLayer(layer MemoryLayer) MemoryLayer {
	if layer.IsValid() {
		return layer
	}
	return MemoryLayerFact
}
