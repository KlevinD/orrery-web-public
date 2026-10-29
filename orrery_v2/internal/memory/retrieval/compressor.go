package retrieval

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

type Compressor struct {
	options CompressionOptions
	now     func() time.Time
}

type CompressionOptions struct {
	MaxSummaryBytes int
	Now             func() time.Time
}

type CompressRequest struct {
	AgentID   agentcore.AgentID
	MissionID string
	TaskID    string
	Scope     memcore.MemoryScope
	Records   []memcore.MemoryRecord
}

func NewCompressor(options CompressionOptions) *Compressor {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Compressor{options: options, now: now}
}

func (c *Compressor) Compress(req CompressRequest) (memcore.MemoryDigest, error) {
	digest := memcore.MemoryDigest{
		AgentID:   req.AgentID,
		MissionID: req.MissionID,
		TaskID:    req.TaskID,
		Scope:     req.Scope,
		CreatedAt: c.now(),
	}
	totalConfidence := 0.0
	layerIndexes := map[layerDigestKey]int{}
	eligibleCount := 0
	for _, record := range req.Records {
		if !memcore.RetrievalEligible(record, digest.CreatedAt) {
			continue
		}
		record = record.Normalized()
		eligibleCount++
		digest.Evidence = append(digest.Evidence, memcore.RetrievalEvidence(record, digest.CreatedAt))
		digest.SourceMemoryRefs = appendUnique(digest.SourceMemoryRefs, record.ID)
		for _, ref := range record.SourceEvidenceRefs {
			digest.SourceEvidenceRefs = appendUnique(digest.SourceEvidenceRefs, ref)
		}
		digest.Facts = append(digest.Facts, record.Facts...)
		digest.Preferences = append(digest.Preferences, record.Preferences...)
		digest.Warnings = append(digest.Warnings, record.Warnings...)
		digest.Decisions = append(digest.Decisions, record.Decisions...)
		key := layerDigestKey{layer: record.Layer, topic: record.Topic}
		index, ok := layerIndexes[key]
		if !ok {
			index = len(digest.Layers)
			layerIndexes[key] = index
			digest.Layers = append(digest.Layers, memcore.LayerDigest{
				Layer: record.Layer,
				Topic: record.Topic,
			})
		}
		layer := &digest.Layers[index]
		layer.SourceMemoryRefs = appendUnique(layer.SourceMemoryRefs, record.ID)
		for _, ref := range record.SourceEvidenceRefs {
			layer.SourceEvidenceRefs = appendUnique(layer.SourceEvidenceRefs, ref)
		}
		layer.Summary = appendSummary(layer.Summary, record.Summary)
		layer.Facts = append(layer.Facts, record.Facts...)
		layer.Lessons = append(layer.Lessons, record.Lessons...)
		layer.Preferences = append(layer.Preferences, record.Preferences...)
		layer.Warnings = append(layer.Warnings, record.Warnings...)
		layer.Decisions = append(layer.Decisions, record.Decisions...)
		layer.Confidence += record.Confidence
		totalConfidence += record.Confidence
		if digest.Visibility.Mode == "" {
			digest.Visibility = record.Visibility
		}
		digest.Sensitivity = memcore.HighestSensitivity(digest.Sensitivity, record.Sensitivity)
	}
	digest.Layers = orderedLayerDigests(digest.Layers)
	digest.Summary = trimBytes(buildLayerSummary(digest.Layers), c.options.MaxSummaryBytes)
	if eligibleCount > 0 {
		digest.Confidence = totalConfidence / float64(eligibleCount)
	}
	versions := append([]string(nil), digest.SourceMemoryRefs...)
	for _, item := range digest.Evidence {
		versions = append(versions, item.Source.Version)
	}
	digest.ID = agentcore.MemoryDigestID("digest." + digestHash(versions, digest.Summary))
	return digest, nil
}

type layerDigestKey struct {
	layer memcore.MemoryLayer
	topic string
}

func appendSummary(existing, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return existing
	}
	if existing == "" {
		return summary
	}
	return existing + "\n" + summary
}

func orderedLayerDigests(layers []memcore.LayerDigest) []memcore.LayerDigest {
	if len(layers) < 2 {
		return layers
	}
	ordered := make([]memcore.LayerDigest, 0, len(layers))
	for _, wanted := range []memcore.MemoryLayer{
		memcore.MemoryLayerFact,
		memcore.MemoryLayerExperience,
		memcore.MemoryLayerWorld,
	} {
		for _, layer := range layers {
			if layer.Layer == wanted {
				ordered = append(ordered, layer)
			}
		}
	}
	return ordered
}

func buildLayerSummary(layers []memcore.LayerDigest) string {
	var b strings.Builder
	for _, layer := range layers {
		if strings.TrimSpace(layer.Summary) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(layerHeading(layer.Layer))
		b.WriteString(":\n")
		b.WriteString(strings.TrimSpace(layer.Summary))
	}
	return b.String()
}

func layerHeading(layer memcore.MemoryLayer) string {
	switch layer {
	case memcore.MemoryLayerExperience:
		return "Experience"
	case memcore.MemoryLayerWorld:
		return "Organization World"
	default:
		return "Facts"
	}
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func trimBytes(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}

func digestHash(refs []string, summary string) string {
	hash := sha1.Sum([]byte(fmt.Sprintf("%v:%s", refs, summary)))
	return hex.EncodeToString(hash[:8])
}
